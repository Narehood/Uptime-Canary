package canary

import (
	"errors"
	"net/http"
	"strings"
	"time"
	"uptime-canary/apps/node-go/internal/nodeconnection"
)

// ConnectionHandler is deliberately separate from DashboardHandler. Publishing
// this listener exposes no browser UI, login/account routes, peer exchange,
// public projection or general management endpoint.
func (d *Dashboard) ConnectionHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.URL.Path == "/healthz" && r.Method == "GET" {
			reply(w, 200, map[string]any{"ok": true, "connectionProtocol": 1})
			return
		}
		if !d.connectionEnabled || !d.accounts.Initialized() {
			errorReply(w, fail(503, "Node connection service is not ready"))
			return
		}
		if r.Method != "POST" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" || r.Header.Get("Origin") != "" {
			errorReply(w, fail(404, "Node connection route unavailable"))
			return
		}
		select {
		case d.connectionSlots <- struct{}{}:
			defer func() { <-d.connectionSlots }()
		default:
			errorReply(w, fail(429, "Node connection is busy"))
			return
		}
		if r.URL.Path == "/connect/v1/enroll" {
			d.claimInvitation(w, r)
			return
		}
		if r.URL.Path != "/connect/v1/open" && r.URL.Path != "/connect/v1/poll" && r.URL.Path != "/connect/v1/result" {
			errorReply(w, fail(404, "Node connection route unavailable"))
			return
		}
		id, nodeID, state, err := d.authenticateConnection(r)
		if err != nil {
			errorReply(w, err)
			return
		}
		if r.URL.Path == "/connect/v1/result" {
			select {
			case d.connectionResultSlots <- struct{}{}:
				defer func() { <-d.connectionResultSlots }()
			default:
				errorReply(w, fail(429, "Node connection is busy"))
				return
			}
			var input struct {
				RegistrationID string `json:"registrationId"`
				Session        string `json:"session"`
				nodeconnection.Response
			}
			raw, err := readBounded(r.Body, nodeconnection.MaxResultEnvelope)
			if err == nil {
				err = connectionJSON(raw, &input, "registrationId", "session", "id", "status", "contentType", "nodeId", "body")
			}
			if err == nil && (input.RegistrationID != id || input.NodeID != nodeID) {
				err = fail(409, "Node connection identity changed")
			}
			if err == nil {
				d.mu.Lock()
				_, _, _, err = d.authenticateConnectionLocked(r)
				if err == nil {
					err = d.connections.Complete(id, input.Session, input.Response)
				}
				d.mu.Unlock()
			}
			if err != nil {
				connectionError(w, err)
				return
			}
			reply(w, 204, nil)
			return
		}
		var input struct {
			RegistrationID string `json:"registrationId"`
			NodeID         string `json:"nodeId"`
			Session        string `json:"session"`
		}
		raw, err := readBounded(r.Body, 4096)
		fields := []string{"registrationId", "session"}
		if r.URL.Path == "/connect/v1/open" {
			fields = []string{"registrationId", "nodeId"}
		}
		if err == nil {
			err = connectionJSON(raw, &input, fields...)
		}
		if err != nil || input.RegistrationID != id {
			errorReply(w, fail(400, "Invalid node connection request"))
			return
		}
		if r.URL.Path == "/connect/v1/open" {
			if input.NodeID != nodeID {
				errorReply(w, fail(409, "Node connection identity changed"))
				return
			}
			d.mu.Lock()
			_, _, _, err := d.authenticateConnectionLocked(r)
			epoch := ""
			if err == nil {
				epoch, err = d.connections.Open(id)
			}
			d.mu.Unlock()
			if err != nil {
				connectionError(w, err)
				return
			}
			reply(w, 200, map[string]any{"session": epoch, "nodeId": nodeID, "state": state, "connectionProtocol": 1})
			return
		}
		job, err := d.connections.Poll(r.Context(), id, input.Session)
		if err != nil {
			connectionError(w, err)
			return
		}
		if job == nil {
			reply(w, 204, nil)
			return
		}
		reply(w, 200, job)
	})
}

func connectionError(w http.ResponseWriter, err error) {
	var api *apiError
	if errors.As(err, &api) {
		errorReply(w, api)
		return
	}
	if errors.Is(err, nodeconnection.ErrBusy) {
		errorReply(w, fail(429, "Node connection is busy"))
		return
	}
	if errors.Is(err, nodeconnection.ErrMessage) {
		errorReply(w, fail(400, "Invalid node connection message"))
		return
	}
	errorReply(w, fail(409, "Node connection session or request changed"))
}

func (d *Dashboard) authenticateConnection(r *http.Request) (string, string, string, error) {
	id := r.Header.Get("X-Canary-Registration")
	token := bearer(r)
	if len(r.Header.Values("Authorization")) != 1 || len(r.Header.Values("X-Canary-Registration")) != 1 || !nodeconnection.Identifier(id, 32) || !nodeconnection.Identifier(token, 64) {
		return "", "", "", fail(401, "Node connection credential was refused")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.authenticateConnectionLocked(r)
}

// Caller holds d.mu, keeping session publication/result completion ordered with
// cancellation, first confirmation and registry removal.
func (d *Dashboard) authenticateConnectionLocked(r *http.Request) (string, string, string, error) {
	id, token := r.Header.Get("X-Canary-Registration"), bearer(r)
	if row, ok := d.registry[id]; ok && row.Connection == "outbound" && matches(row.ConnectionHash, token) {
		return id, row.NodeID, row.State, nil
	}
	if v, ok := d.invitations[id]; ok && v.State == "connected" && v.ExpiresAt > time.Now().UnixMilli() && d.invitationCreatorValid(v) && matches(v.ConnectionHash, token) {
		return id, v.NodeID, "awaiting-confirmation", nil
	}
	return "", "", "", fail(401, "Node connection credential was refused")
}

func (d *Dashboard) claimInvitation(w http.ResponseWriter, r *http.Request) {
	token := bearer(r)
	parts := strings.Split(token, ".")
	if len(r.Header.Values("Authorization")) != 1 || len(parts) != 2 || !nodeconnection.Identifier(parts[0], 32) || !nodeconnection.Identifier(parts[1], 64) {
		errorReply(w, fail(401, "Node invitation was refused"))
		return
	}
	id := parts[0]
	// Authenticate before reading a body. The dedicated endpoint must not
	// allocate large response envelopes for an unauthenticated network caller.
	d.mu.Lock()
	v, ok := d.invitations[id]
	allowed := ok && v.ExpiresAt > time.Now().UnixMilli() && matches(v.TokenHash, token) && d.invitationCreatorValid(v)
	d.mu.Unlock()
	if !allowed {
		errorReply(w, fail(401, "Node invitation was refused or expired"))
		return
	}
	var input struct {
		NodeID          string `json:"nodeId"`
		Name            string `json:"name"`
		Version         string `json:"version"`
		SourceCommit    string `json:"sourceCommit"`
		ConnectionToken string `json:"connectionToken"`
		AdminToken      string `json:"adminToken"`
	}
	raw, err := readBounded(r.Body, 8192)
	if err == nil {
		err = connectionJSON(raw, &input, "nodeId", "name", "version", "sourceCommit", "connectionToken", "adminToken")
	}
	if err == nil {
		err = identifier(input.NodeID, "node ID")
	}
	if err == nil {
		err = validText(input.Name, "node name", 128)
	}
	if err == nil {
		err = validText(input.Version, "node version", 128)
	}
	if err == nil {
		err = tokenValue(input.AdminToken)
	}
	if err == nil && (!nodeconnection.Identifier(input.ConnectionToken, 64) || input.SourceCommit != "" && !nodeconnection.Identifier(input.SourceCommit, 40)) {
		err = fail(400, "Invalid node connection identity")
	}
	if err != nil {
		errorReply(w, err)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	v, ok = d.invitations[id]
	if !ok || v.ExpiresAt <= time.Now().UnixMilli() || !matches(v.TokenHash, token) || !d.invitationCreatorValid(v) {
		errorReply(w, fail(401, "Node invitation was refused or expired"))
		return
	}
	if v.NodeID != input.NodeID {
		errorReply(w, fail(409, "The invitation requires a different node ID"))
		return
	}
	for _, row := range d.registry {
		if row.NodeID == input.NodeID {
			errorReply(w, fail(409, "This node identity is already registered"))
			return
		}
	}
	var secret dashboardNodeSecrets
	if v.State == "connected" {
		if !matches(v.ConnectionHash, input.ConnectionToken) {
			errorReply(w, fail(409, "This invitation was claimed by another connection"))
			return
		}
		secret, err = d.openSecrets(d.invitationRow(v))
	} else {
		secret.NodeID = input.NodeID
		secret.Admin, err = nodeconnection.RandomKey()
		if err == nil {
			secret.Editor, err = nodeconnection.RandomKey()
		}
		if err == nil {
			secret.Viewer, err = nodeconnection.RandomKey()
		}
	}
	if err != nil {
		errorReply(w, fail(503, "Pairing credentials are unavailable"))
		return
	}
	secret.EnrollmentToken = input.AdminToken
	v.State, v.ConnectionHash = "connected", digest(input.ConnectionToken)
	v.Node = &invitedNodeReport{ID: input.NodeID, Name: input.Name, Version: input.Version, SourceCommit: input.SourceCommit, Fingerprint: v.ConnectionHash}
	v.Credentials, err = d.sealSecrets(d.invitationRow(v), secret)
	if err == nil {
		err = d.saveInvitation(nil, AccountView{}, v, nil)
	}
	if err != nil {
		errorReply(w, err)
		return
	}
	reply(w, 201, map[string]any{"registrationId": id, "nodeId": input.NodeID, "state": "awaiting-confirmation", "connectionProtocol": 1})
}
