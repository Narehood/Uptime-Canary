package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"
)

func (d *Dashboard) editRegisteredNode(w http.ResponseWriter, r *http.Request, actor AccountView, id string) {
	raw, err := readBounded(r.Body, 4096)
	var input struct {
		Label string  `json:"label"`
		URL   *string `json:"url"`
	}
	if err == nil {
		err = dashboardJSON(raw, &input, "label", "url")
	}
	if err == nil {
		err = validText(input.Label, "node label", 128)
	}
	if err == nil {
		err = d.currentActor(r, actor, true)
	}
	if err != nil {
		errorReply(w, err)
		return
	}
	d.mu.Lock()
	before, ok := d.registry[id]
	if !ok {
		d.mu.Unlock()
		errorReply(w, fail(404, "Registered node not found"))
		return
	}
	if d.busy[id] || dashboardOperationPending(before.State) {
		d.mu.Unlock()
		errorReply(w, fail(409, "Finish the pending node operation first"))
		return
	}
	d.mu.Unlock()
	candidate := before
	candidate.Label = input.Label
	if input.URL != nil && before.Connection == "outbound" {
		errorReply(w, fail(400, "Node-initiated connections have no dashboard-dialed node address"))
		return
	}
	if input.URL != nil {
		candidate.URL, err = dashboardNodeURL(*input.URL)
		if err == nil && (slices.Contains(d.origins, candidate.URL) || strings.EqualFold(strings.SplitN(candidate.URL, "://", 2)[1], r.Host)) {
			err = fail(400, "Use the monitoring node address, not this dashboard address")
		}
	}
	if err != nil {
		errorReply(w, err)
		return
	}
	secret := dashboardNodeSecrets{}
	if candidate.URL != before.URL {
		if before.Local && d.localURL != "" && before.URL == d.localURL {
			errorReply(w, fail(409, "The bundled node address is managed in deployment settings"))
			return
		}
		secret, err = d.openSecrets(before)
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		for _, role := range dashboardRoles {
			if err == nil {
				err = d.nodeActor(ctx, candidate, secret.token(role), role)
			}
		}
		if err != nil {
			errorReply(w, err)
			return
		}
		candidate.Local, candidate.State, candidate.Error, candidate.LastReadAt, candidate.MonitorCount = false, "starting", "", 0, nil
		candidate.Credentials, err = d.sealSecrets(candidate, secret)
		if err != nil {
			errorReply(w, err)
			return
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	current, exists := d.registry[id]
	if !exists || current.URL != before.URL || current.Credentials != before.Credentials || current.Label != before.Label || d.busy[id] || dashboardOperationPending(current.State) {
		errorReply(w, fail(409, "Registered node changed; refresh before editing"))
		return
	}
	for key, saved := range d.registry {
		if key != id && candidate.URL != "" && saved.URL == candidate.URL {
			errorReply(w, fail(409, "This node address is already registered"))
			return
		}
	}
	if candidate.URL == before.URL {
		candidate = current
		candidate.Label = input.Label
	}
	if err = d.saveAuthorizedRegistry(candidate, r, actor); err != nil {
		errorReply(w, err)
		return
	}
	reply(w, 200, d.publicNode(candidate, actor.Role))
}

func (d *Dashboard) rotateRegisteredNode(w http.ResponseWriter, r *http.Request, actor AccountView, id string) {
	raw, err := readBounded(r.Body, 4096)
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	var input struct {
		Token string `json:"token"`
	}
	if err == nil {
		err = dashboardJSON(raw, &input, "token")
	}
	if err == nil && input.Token != "" {
		err = tokenValue(input.Token)
	}
	if err == nil {
		err = d.currentActor(r, actor, true)
	}
	if err != nil {
		errorReply(w, err)
		return
	}
	d.mu.Lock()
	before, ok := d.registry[id]
	if !ok {
		d.mu.Unlock()
		errorReply(w, fail(404, "Registered node not found"))
		return
	}
	if before.Unproved || d.busy[id] || dashboardOperationPending(before.State) {
		d.mu.Unlock()
		errorReply(w, fail(409, "Finish the pending node operation first"))
		return
	}
	secret, err := d.openSecrets(before)
	d.mu.Unlock()
	if err != nil {
		errorReply(w, fail(503, "Saved node credentials unavailable"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	authority := secret.Admin
	if input.Token != "" {
		authority = input.Token
	}
	if err = d.rotationPreflight(ctx, before, authority, input.Token == ""); err != nil {
		errorReply(w, err)
		return
	}
	next := dashboardNodeSecrets{dashboardRoleTokens: dashboardRoleTokens{NodeID: before.NodeID}, EnrollmentToken: secret.Admin}
	if input.Token != "" {
		next.EnrollmentToken = input.Token
	}
	for _, destination := range []*string{&next.Admin, &next.Editor, &next.Viewer} {
		*destination, err = randomCredential(32)
		if err != nil {
			errorReply(w, err)
			return
		}
	}
	d.mu.Lock()
	current, exists := d.registry[id]
	if !exists || current.URL != before.URL || current.Credentials != before.Credentials || d.busy[id] || dashboardOperationPending(current.State) {
		d.mu.Unlock()
		errorReply(w, fail(409, "Registered node changed; refresh before rotating"))
		return
	}
	current.State, current.Error = "rotating", ""
	current.Credentials, err = d.sealSecrets(current, next)
	if err == nil {
		err = d.saveAuthorizedRegistry(current, r, actor)
	}
	d.mu.Unlock()
	if err != nil {
		errorReply(w, err)
		return
	}
	// The encrypted desired bundle and recovery credential are durable BEFORE
	// the node sees PATCH. A lost answer can use the new admin on the retry.
	row, err := d.finishEnrollment(r.Context(), id)
	if err != nil {
		d.mu.Lock()
		row = d.registry[id]
		d.mu.Unlock()
		reply(w, 202, d.publicNode(row, actor.Role))
		return
	}
	reply(w, 200, d.publicNode(row, actor.Role))
}

// This is read-only preflight, never enrollment or repair of a revoked account.
// An already-known refusal must not strand the registry in a new rotation.
func (d *Dashboard) rotationPreflight(ctx context.Context, row registeredNodeRecord, token string, delegated bool) error {
	response, raw, err := d.nodeRequest(ctx, row, "", "GET", "/api/auth/status", nil)
	if err != nil {
		return err
	}
	var profile struct {
		Rotation bool `json:"dashboardCredentialRotation"`
	}
	if response.StatusCode != 200 || json.Unmarshal(raw, &profile) != nil || !profile.Rotation {
		return fail(409, "Upgrade this monitoring node before rotating its dashboard credentials")
	}
	probe := row
	if !delegated {
		probe.ID = ""
	}
	if err := d.nodeActor(ctx, probe, token, "admin"); err != nil {
		return err
	}
	response, raw, err = d.nodeRequest(ctx, row, token, "GET", "/api/accounts", nil)
	if err != nil {
		return err
	}
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return fail(502, "Node credential was refused")
	}
	var accounts []AccountView
	if response.StatusCode != 200 || json.Unmarshal(raw, &accounts) != nil || len(accounts) > 64 {
		return fail(502, "Cannot verify the node's dashboard delegation")
	}
	seen := map[string]bool{}
	for _, account := range accounts {
		for _, role := range dashboardRoles {
			id := dashboardDelegateID(row.ID, role)
			if account.ID == id {
				if seen[id] || account.Role != role || account.PasswordEnabled {
					return fail(409, "The node's dashboard delegation changed; restore its retained state before rotating access")
				}
				seen[id] = true
			}
		}
	}
	if len(seen) != len(dashboardRoles) {
		return fail(409, "The node's dashboard delegation is incomplete; restore its retained state before rotating access")
	}
	return nil
}

func (d *Dashboard) retryRegisteredNode(w http.ResponseWriter, r *http.Request, actor AccountView, id string) {
	raw, err := readBounded(r.Body, 4096)
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	var input struct {
		Token string `json:"token"`
	}
	if err == nil {
		err = dashboardJSON(raw, &input, "token")
	}
	if err == nil && input.Token != "" {
		err = tokenValue(input.Token)
	}
	if err == nil {
		err = d.currentActor(r, actor, true)
	}
	if err == nil && input.Token != "" {
		err = d.replaceRotationRecovery(r, actor, id, input.Token)
	}
	if err != nil {
		errorReply(w, err)
		return
	}
	row, err := d.finishEnrollment(r.Context(), id)
	if err != nil {
		if input.Token == "" {
			errorReply(w, err)
			return
		}
		// The replacement recovery authority is already durable. A refused or
		// lost node answer cannot turn that into a claim that nothing changed.
		d.mu.Lock()
		row = d.registry[id]
		d.mu.Unlock()
		if row.ID == "" {
			reply(w, 204, nil)
			return
		}
		status := 200
		if dashboardOperationPending(row.State) {
			status = 202
		}
		reply(w, status, d.publicNode(row, actor.Role))
		return
	}
	if row.ID == "" {
		reply(w, 204, nil)
		return
	}
	reply(w, 200, d.publicNode(row, actor.Role))
}

// Replacing recovery authority never generates another desired role bundle.
// Preserve the same pending operation even when revocation raced its preflight.
func (d *Dashboard) replaceRotationRecovery(r *http.Request, actor AccountView, id, token string) error {
	d.mu.Lock()
	before, ok := d.registry[id]
	if !ok {
		d.mu.Unlock()
		return fail(404, "Registered node not found")
	}
	if before.State != "rotating" || d.busy[id] {
		d.mu.Unlock()
		return fail(409, "Recovery credentials apply only to an idle pending access update")
	}
	secret, err := d.openSecrets(before)
	d.mu.Unlock()
	if err != nil {
		return fail(503, "Saved node credentials unavailable")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := d.rotationPreflight(ctx, before, token, false); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	current, ok := d.registry[id]
	if !ok || current.State != "rotating" || current.URL != before.URL || current.Credentials != before.Credentials || d.busy[id] {
		return fail(409, "Registered node changed; refresh before recovering access")
	}
	secret.EnrollmentToken = token
	current.Credentials, err = d.sealSecrets(current, secret)
	if err != nil {
		return err
	}
	current.Error = ""
	return d.saveAuthorizedRegistry(current, r, actor)
}
