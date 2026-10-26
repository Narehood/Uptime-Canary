package canary

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"uptime-canary/apps/node-go/internal/nodeconnection"
)

const invitationLifetime = 15 * time.Minute

var connectionImageTag = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

type invitedNodeReport struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Version      string `json:"version"`
	SourceCommit string `json:"sourceCommit,omitempty"`
	Fingerprint  string `json:"fingerprint"`
}

type nodeInvitation struct {
	ID                       string             `json:"id"`
	Label                    string             `json:"label"`
	NodeID                   string             `json:"nodeId"`
	ConnectionURL            string             `json:"connectionUrl"`
	ConnectionTLSFingerprint string             `json:"connectionTlsFingerprint,omitempty"`
	ExpiresAt                int64              `json:"expiresAt"`
	State                    string             `json:"state"`
	RegistrationID           string             `json:"registrationId,omitempty"`
	Node                     *invitedNodeReport `json:"node,omitempty"`
}

type nodeInvitationRecord struct {
	nodeInvitation
	TokenHash      string `json:"tokenHash"`
	CreatorID      string `json:"creatorId"`
	CreatorVersion uint64 `json:"creatorVersion"`
	ConnectionHash string `json:"connectionHash,omitempty"`
	Credentials    string `json:"credentials,omitempty"`
}

func (d *Dashboard) invitationRow(v nodeInvitationRecord) registeredNodeRecord {
	name := v.NodeID
	if v.Node != nil {
		name = v.Node.Name
	}
	return registeredNodeRecord{RegisteredNode: RegisteredNode{ID: v.ID, Label: v.Label, NodeID: v.NodeID, NodeName: name, State: "enrolling", Connection: "outbound"}, Credentials: v.Credentials, ConnectionHash: v.ConnectionHash, InvitationExpiresAt: v.ExpiresAt}
}

func (d *Dashboard) loadInvitations() error {
	if !connectionImageTag.MatchString(d.nodeImageTag) {
		return errors.New("Invalid onboarding node image tag")
	}
	n := d.accounts.node
	n.mu.Lock()
	values, err := loadAll[nodeInvitationRecord](n.store, "dashboardInvitations")
	n.mu.Unlock()
	if err != nil || len(values) > 16 {
		return errors.New("Cannot load bounded node invitations")
	}
	for id, value := range values {
		canonical, e := dashboardConnectionOrigin(value.ConnectionURL)
		if id != value.ID || !nodeconnection.Identifier(id, 32) || identifier(value.NodeID, "node ID") != nil || validText(value.Label, "node label", 128) != nil || e != nil || canonical != value.ConnectionURL || value.ExpiresAt < 1 || !nodeconnection.Identifier(value.TokenHash, 64) || value.CreatorID == "" || value.CreatorVersion == 0 || !slices.Contains([]string{"waiting", "connected"}, value.State) {
			return errors.New("Invalid saved node invitation")
		}
		if value.State == "connected" {
			if value.Node == nil || value.Node.ID != value.NodeID || value.Node.Fingerprint != value.ConnectionHash || !nodeconnection.Identifier(value.ConnectionHash, 64) {
				return errors.New("Invalid claimed node invitation")
			}
			if _, e = d.openSecrets(d.invitationRow(value)); e != nil {
				return errors.New("Cannot decrypt pending node invitation")
			}
		} else if value.Node != nil || value.Credentials != "" || value.ConnectionHash != "" {
			return errors.New("Invalid unclaimed node invitation")
		}
		d.invitations[id] = value
	}
	return nil
}

func (d *Dashboard) publicInvitation(v nodeInvitationRecord) nodeInvitation {
	value := v.nodeInvitation
	if value.State == "connected" && !d.invitationConnectionReady(value.ID) {
		value.State = "waiting"
	}
	if value.ExpiresAt <= time.Now().UnixMilli() {
		value.State = "expired"
	}
	return value
}

// Production nodes open only after persisting their registration ID. Requiring
// that acknowledgement prevents a confirmed journal from stranding a node
// whose claim response was lost or whose state publication failed.
func (d *Dashboard) invitationConnectionReady(id string) bool {
	if d.connections == nil {
		return false
	}
	seen := d.connections.LastSeen(id)
	return seen > 0 && time.Since(time.UnixMilli(seen)) <= nodeconnection.SessionFreshness
}

func (d *Dashboard) invitationCreatorValid(v nodeInvitationRecord) bool {
	n := d.accounts.node
	n.mu.Lock()
	defer n.mu.Unlock()
	a, ok := n.accounts[v.CreatorID]
	return ok && a.Role == "admin" && a.AuthVersion == v.CreatorVersion && !n.stopped && n.fault == nil
}

// Caller holds d.mu. Read/replace authorization is repeated in the same store
// transaction boundary as the write, rather than relying on an old UI session.
func (d *Dashboard) saveInvitation(r *http.Request, actor AccountView, value nodeInvitationRecord, removed []string) error {
	n := d.accounts.node
	n.mu.Lock()
	defer n.mu.Unlock()
	if r != nil {
		current, err := n.authenticate(r)
		if err != nil {
			return err
		}
		if current.ID != actor.ID || current.Role != "admin" {
			return fail(403, "Dashboard administrator permission is required")
		}
		if r.Context().Err() != nil {
			return fail(408, "Request cancelled")
		}
		if value.ID != "" {
			value.CreatorID, value.CreatorVersion = current.ID, n.accounts[current.ID].AuthVersion
		}
	} else if value.ID != "" {
		creator, exists := n.accounts[value.CreatorID]
		if !exists || creator.Role != "admin" || creator.AuthVersion != value.CreatorVersion || value.ExpiresAt <= time.Now().UnixMilli() {
			return fail(401, "Node invitation is no longer authorized")
		}
	}
	if err := n.identityTransactionLocked(func(e dbExec) error {
		for _, id := range removed {
			if err := remove(e, "dashboardInvitations", id); err != nil {
				return err
			}
		}
		if value.ID != "" {
			return put(e, "dashboardInvitations", value.ID, value)
		}
		return nil
	}); err != nil {
		return err
	}
	for _, id := range removed {
		delete(d.invitations, id)
		d.connections.Disconnect(id)
	}
	if value.ID != "" {
		d.invitations[value.ID] = value
	}
	return nil
}

func (d *Dashboard) invitationRequest(w http.ResponseWriter, r *http.Request, actor AccountView) {
	if actor.Role != "admin" {
		errorReply(w, fail(403, "Dashboard administrator permission is required"))
		return
	}
	if err := d.currentActor(r, actor, true); err != nil {
		errorReply(w, err)
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		errorReply(w, fail(400, "Invitation routes do not accept query parameters"))
		return
	}
	if r.URL.Path == "/api/dashboard/onboarding" && r.Method == "GET" {
		out := map[string]any{"outboundEnabled": d.connectionEnabled && d.connectionURL != "", "connectionProtocol": 1, "imageTag": d.nodeImageTag, "containerPlatforms": []string{"linux/amd64"}, "nativeDownloadAvailable": false}
		if d.connectionURL != "" {
			out["connectionUrl"] = d.connectionURL
			if d.connectionTLSFingerprint != "" {
				out["connectionTlsFingerprint"] = d.connectionTLSFingerprint
			}
		}
		reply(w, 200, out)
		return
	}
	if r.URL.Path == "/api/dashboard/invitations" {
		switch r.Method {
		case "GET":
			d.mu.Lock()
			out := []nodeInvitation{}
			for _, value := range d.invitations {
				out = append(out, d.publicInvitation(value))
			}
			d.mu.Unlock()
			slices.SortFunc(out, func(a, b nodeInvitation) int { return strings.Compare(a.ID, b.ID) })
			reply(w, 200, out)
			return
		case "POST":
			d.createInvitation(w, r, actor)
			return
		}
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/dashboard/invitations/"), "/")
	if len(parts) < 1 || !nodeconnection.Identifier(parts[0], 32) {
		errorReply(w, fail(404, "Node invitation not found"))
		return
	}
	id := parts[0]
	if len(parts) == 2 && parts[1] == "confirm" && r.Method == "POST" {
		d.confirmInvitation(w, r, actor, id)
		return
	}
	if len(parts) != 1 {
		errorReply(w, fail(404, "Node invitation not found"))
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if row, ok := d.registry[id]; ok && row.Connection == "outbound" {
		if r.Method == "GET" {
			reply(w, 200, nodeInvitation{ID: id, Label: row.Label, NodeID: row.NodeID, ConnectionURL: d.connectionURL, ConnectionTLSFingerprint: d.connectionTLSFingerprint, ExpiresAt: row.InvitationExpiresAt, State: "confirmed", RegistrationID: id})
			return
		}
		errorReply(w, fail(409, "This invitation is already paired; use Remove node to revoke access"))
		return
	}
	v, ok := d.invitations[id]
	if !ok {
		errorReply(w, fail(404, "Node invitation not found"))
		return
	}
	if r.Method == "GET" {
		reply(w, 200, d.publicInvitation(v))
		return
	}
	if r.Method != "DELETE" {
		errorReply(w, fail(405, "Invitation method unavailable"))
		return
	}
	if err := d.saveInvitation(r, actor, nodeInvitationRecord{}, []string{id}); err != nil {
		errorReply(w, err)
		return
	}
	reply(w, 204, nil)
}

func (d *Dashboard) createInvitation(w http.ResponseWriter, r *http.Request, actor AccountView) {
	if !d.connectionEnabled || d.connectionURL == "" {
		errorReply(w, fail(409, "Configure the dedicated HTTPS node connection endpoint first"))
		return
	}
	var input struct {
		Label  string `json:"label"`
		NodeID string `json:"nodeId"`
	}
	raw, err := readBounded(r.Body, 4096)
	if err == nil {
		err = connectionJSON(raw, &input, "label", "nodeId")
	}
	if err == nil {
		err = validText(input.Label, "node label", 128)
	}
	if err == nil {
		err = identifier(input.NodeID, "node ID")
	}
	if err != nil {
		errorReply(w, err)
		return
	}
	id, err := nodeconnection.RandomKey()
	if err != nil {
		errorReply(w, err)
		return
	}
	// Existing registry IDs are lowercase hex; invitations use the same shape.
	id = id[:32]
	secret, err := nodeconnection.RandomKey()
	if err != nil {
		errorReply(w, err)
		return
	}
	token := id + "." + secret
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now().UnixMilli()
	removed := []string{}
	active := 0
	for key, value := range d.invitations {
		if value.ExpiresAt <= now {
			removed = append(removed, key)
			continue
		}
		active++
		if value.NodeID == input.NodeID {
			errorReply(w, fail(409, "An invitation already exists for this node ID"))
			return
		}
	}
	for _, row := range d.registry {
		if row.NodeID == input.NodeID {
			errorReply(w, fail(409, "This node identity is already registered"))
			return
		}
	}
	if active >= 16 || len(d.registry) >= 64 {
		errorReply(w, fail(409, "Dashboard node or invitation capacity exceeded"))
		return
	}
	n := d.accounts.node
	n.mu.Lock()
	creator, ok := n.accounts[actor.ID]
	n.mu.Unlock()
	if !ok || creator.Role != "admin" {
		errorReply(w, fail(403, "Dashboard administrator permission is required"))
		return
	}
	v := nodeInvitationRecord{nodeInvitation: nodeInvitation{ID: id, Label: input.Label, NodeID: input.NodeID, ConnectionURL: d.connectionURL, ConnectionTLSFingerprint: d.connectionTLSFingerprint, ExpiresAt: now + invitationLifetime.Milliseconds(), State: "waiting"}, TokenHash: digest(token), CreatorID: actor.ID, CreatorVersion: creator.AuthVersion}
	if err = d.saveInvitation(r, actor, v, removed); err != nil {
		errorReply(w, err)
		return
	}
	// Only this response contains the one-time secret. It is absent from both
	// persisted JSON and every subsequent list/status response.
	encoded, _ := json.Marshal(v.nodeInvitation)
	var out map[string]any
	_ = json.Unmarshal(encoded, &out)
	out["token"] = token
	reply(w, 201, out)
}

func (d *Dashboard) confirmInvitation(w http.ResponseWriter, r *http.Request, actor AccountView, id string) {
	raw, err := readBounded(r.Body, 4096)
	if err != nil || len(raw) != 0 {
		errorReply(w, fail(400, "Confirm pairing takes no request body"))
		return
	}
	d.mu.Lock()
	if row, ok := d.registry[id]; ok && row.Connection == "outbound" {
		d.mu.Unlock()
		reply(w, 202, d.publicNode(row, actor.Role))
		return
	}
	v, ok := d.invitations[id]
	if !ok || v.ExpiresAt <= time.Now().UnixMilli() || v.State != "connected" || v.Node == nil || !d.invitationConnectionReady(id) || !d.invitationCreatorValid(v) {
		d.mu.Unlock()
		errorReply(w, fail(409, "A current connected invitation must be confirmed"))
		return
	}
	if len(d.registry) >= 64 {
		d.mu.Unlock()
		errorReply(w, fail(409, "Dashboard node capacity exceeded"))
		return
	}
	for _, row := range d.registry {
		if row.NodeID == v.NodeID {
			d.mu.Unlock()
			errorReply(w, fail(409, "This node identity is already registered"))
			return
		}
	}
	row := d.invitationRow(v)
	if _, err = d.openSecrets(row); err != nil {
		d.mu.Unlock()
		errorReply(w, fail(503, "Pending pairing credentials are unavailable"))
		return
	}
	n := d.accounts.node
	n.mu.Lock()
	current, authErr := n.authenticate(r)
	if authErr == nil && (current.ID != actor.ID || current.Role != "admin") {
		authErr = fail(403, "Dashboard administrator permission is required")
	}
	if authErr == nil && r.Context().Err() != nil {
		authErr = fail(408, "Request cancelled")
	}
	if authErr == nil {
		creator, exists := n.accounts[v.CreatorID]
		if !exists || creator.Role != "admin" || creator.AuthVersion != v.CreatorVersion || v.ExpiresAt <= time.Now().UnixMilli() {
			authErr = fail(409, "Node invitation is no longer authorized")
		}
	}
	if authErr == nil {
		authErr = n.identityTransactionLocked(func(e dbExec) error {
			if e := put(e, "dashboardRegistry", id, row); e != nil {
				return e
			}
			return remove(e, "dashboardInvitations", id)
		})
	}
	n.mu.Unlock()
	if authErr != nil {
		d.mu.Unlock()
		errorReply(w, authErr)
		return
	}
	d.registry[id] = row
	delete(d.invitations, id)
	d.mu.Unlock()
	// The journal exists before the first role-creation request. A timeout or
	// lost answer returns the saved pending registration, never a false rollback.
	finishContext, finishCancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer finishCancel()
	completed, finishErr := d.finishEnrollment(finishContext, id)
	if finishErr == nil {
		reply(w, 201, d.publicNode(completed, actor.Role))
		return
	}
	d.mu.Lock()
	row = d.registry[id]
	d.mu.Unlock()
	reply(w, 202, d.publicNode(row, actor.Role))
}
