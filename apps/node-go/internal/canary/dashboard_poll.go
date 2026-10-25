package canary

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// Start is explicitly separate from construction, so identity-only unit tests
// and callers that do not own a lifecycle never acquire background work.
// The caller cancels ctx and waits for the returned channel before Close.
func (d *Dashboard) Start(ctx context.Context) <-chan struct{} {
	d.start.Do(func() { go func() { defer close(d.done); d.poll(ctx) }() })
	return d.done
}

func (d *Dashboard) poll(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if d.accounts.Initialized() {
			if d.localURL != "" {
				d.ensureLocalNode(ctx)
			}
			d.mu.Lock()
			ids := make([]string, 0, len(d.registry))
			for id := range d.registry {
				ids = append(ids, id)
			}
			for key, v := range d.tickets {
				if v.ExpiresAt <= time.Now().UnixMilli() {
					delete(d.tickets, key)
				}
			}
			d.mu.Unlock()
			jobs := make(chan string)
			var workers sync.WaitGroup
			for range 2 {
				workers.Go(func() {
					for id := range jobs {
						op, cancel := context.WithTimeout(ctx, 10*time.Second)
						d.mu.Lock()
						row := d.registry[id]
						d.mu.Unlock()
						if row.State == "enrolling" || row.State == "removing" {
							_, _ = d.finishEnrollment(op, id)
						} else {
							_, _ = d.checkNode(op, id)
						}
						cancel()
					}
				})
			}
			for _, id := range ids {
				select {
				case jobs <- id:
				case <-ctx.Done():
				}
			}
			close(jobs)
			workers.Wait()
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
	}
}

func (d *Dashboard) ensureLocalNode(ctx context.Context) {
	if ctx.Err() != nil || d.localURL == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, row := range d.registry {
		if row.Local {
			// A pending identity has never received a role-enrollment request.
			// Correct its configuration in place, retaining the journal and roles.
			// Once identity is proved, never send its credentials to a new URL.
			if row.State == "enrolling" && !d.busy[row.ID] && (row.Unproved || row.URL == d.localURL) {
				secret, err := d.openSecrets(row)
				if err == nil && (row.URL != d.localURL || secret.EnrollmentToken != d.localToken) {
					row.URL, row.Error, secret.EnrollmentToken = d.localURL, "", d.localToken
					row.Credentials, err = d.sealSecrets(row, secret)
					if err == nil {
						_ = d.saveRegistry(row)
					}
				}
			}
			return
		}
	}
	// The same saved destination already owns its role bundle. Deployment
	// management does not require another registration or fresh credentials.
	for _, row := range d.registry {
		if row.URL == d.localURL {
			if !d.busy[row.ID] && row.State != "removing" {
				row.Local = true
				_ = d.saveRegistry(row)
			}
			return
		}
	}
	if len(d.registry) >= 64 {
		return
	}
	// Publish a starting local card before network contact. Authentication and
	// setup remain independent even if the bundled process has not started.
	row := registeredNodeRecord{RegisteredNode: RegisteredNode{ID: strings.ReplaceAll(newID(), "-", ""), Label: "Local node", NodeName: "Local node", NodeID: "pending", URL: d.localURL, Local: true, State: "enrolling"}, Unproved: true}
	secret := dashboardNodeSecrets{dashboardRoleTokens: dashboardRoleTokens{NodeID: "pending"}, EnrollmentToken: d.localToken}
	var err error
	secret.Admin, err = randomCredential(32)
	if err == nil {
		secret.Editor, err = randomCredential(32)
	}
	if err == nil {
		secret.Viewer, err = randomCredential(32)
	}
	if err == nil {
		row.Credentials, err = d.sealSecrets(row, secret)
	}
	if err == nil {
		_ = d.saveRegistry(row)
	}
}

// A local placeholder is the only unproved registry identity. Its operator-
// supplied address is reconciled before discovery; replace identity and AAD before
// using the remote role-enrollment endpoint.
func (d *Dashboard) identifyLocal(ctx context.Context, row registeredNodeRecord, secret dashboardNodeSecrets) (registeredNodeRecord, dashboardNodeSecrets, error) {
	probe := row
	probe.ID = ""
	probe.NodeID = ""
	if err := d.nodeActor(ctx, probe, secret.EnrollmentToken, "admin"); err != nil {
		return row, secret, err
	}
	view, err := d.readNode(ctx, probe, secret.EnrollmentToken)
	if err != nil {
		return row, secret, err
	}
	probe.NodeID = view.Node.ID
	resp, b, err := d.nodeRequest(ctx, probe, "", "GET", "/api/auth/status", nil)
	var capability struct {
		DashboardEnrollment bool `json:"dashboardEnrollment"`
	}
	if err == nil && (resp.StatusCode != 200 || json.Unmarshal(b, &capability) != nil || !capability.DashboardEnrollment) {
		err = fail(409, "Upgrade the bundled local node before enrollment")
	}
	if err != nil {
		return row, secret, err
	}
	d.mu.Lock()
	for id, other := range d.registry {
		if id != row.ID && other.NodeID == view.Node.ID {
			d.mu.Unlock()
			return d.adoptLocalIdentity(ctx, row, id, view.Node)
		}
	}
	defer d.mu.Unlock()
	row.NodeID, row.NodeName = view.Node.ID, view.Node.Name
	row.Unproved = false
	secret.NodeID = row.NodeID
	row.Credentials, err = d.sealSecrets(row, secret)
	if err == nil {
		err = d.saveRegistry(row)
	}
	return row, secret, err
}

// A manually registered node can also be the configured local node through a
// different origin. Prove every existing role at that origin before rebinding
// its encrypted record, and remove only the unproved placeholder atomically.
func (d *Dashboard) adoptLocalIdentity(ctx context.Context, placeholder registeredNodeRecord, id string, identity Identity) (registeredNodeRecord, dashboardNodeSecrets, error) {
	d.mu.Lock()
	row, ok := d.registry[id]
	if !ok || row.NodeID != identity.ID || d.busy[id] || row.State == "enrolling" || row.State == "removing" {
		d.mu.Unlock()
		return placeholder, dashboardNodeSecrets{}, fail(409, "Finish the existing node operation before local enrollment")
	}
	secret, err := d.openSecrets(row)
	if err != nil {
		d.mu.Unlock()
		return placeholder, secret, fail(503, "Saved node credentials are unavailable")
	}
	d.busy[id] = true
	d.mu.Unlock()
	defer func() { d.mu.Lock(); delete(d.busy, id); d.mu.Unlock() }()
	probe := row
	probe.URL = placeholder.URL
	for _, role := range dashboardRoles {
		if err = d.nodeActor(ctx, probe, secret.token(role), role); err != nil {
			return placeholder, secret, err
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	row = d.registry[id]
	row.URL, row.Local, row.NodeName, row.State, row.Error = placeholder.URL, true, identity.Name, "starting", ""
	row.Credentials, err = d.sealSecrets(row, secret)
	if err != nil {
		return placeholder, secret, err
	}
	n := d.accounts.node
	n.mu.Lock()
	err = n.identityTransactionLocked(func(e dbExec) error {
		if err := put(e, "dashboardRegistry", id, row); err != nil {
			return err
		}
		return remove(e, "dashboardRegistry", placeholder.ID)
	})
	n.mu.Unlock()
	if err != nil {
		return placeholder, secret, err
	}
	d.registry[id] = row
	delete(d.registry, placeholder.ID)
	return row, secret, nil
}
