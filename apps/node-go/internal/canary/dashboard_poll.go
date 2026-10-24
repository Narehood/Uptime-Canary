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
	d.mu.Lock()
	for _, row := range d.registry {
		if row.Local {
			d.mu.Unlock()
			return
		}
	}
	if len(d.registry) >= 64 {
		d.mu.Unlock()
		return
	}
	// Publish a starting local card before network contact. Authentication and
	// setup remain independent even if the bundled process has not started.
	row := registeredNodeRecord{RegisteredNode: RegisteredNode{ID: strings.ReplaceAll(newID(), "-", ""), Label: "Local node", NodeName: "Local node", NodeID: "pending", URL: d.localURL, Local: true, State: "enrolling"}}
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
	d.mu.Unlock()
}

// A local placeholder is the only unproved registry identity. Its operator-
// supplied address is fixed; replace its identity and AAD atomically before
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
	defer d.mu.Unlock()
	for id, other := range d.registry {
		if id != row.ID && other.NodeID == view.Node.ID {
			return row, secret, fail(409, "The bundled node identity is already registered")
		}
	}
	row.NodeID, row.NodeName = view.Node.ID, view.Node.Name
	secret.NodeID = row.NodeID
	row.Credentials, err = d.sealSecrets(row, secret)
	if err == nil {
		err = d.saveRegistry(row)
	}
	return row, secret, err
}
