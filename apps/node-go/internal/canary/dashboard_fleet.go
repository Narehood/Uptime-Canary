package canary

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

type dashboardNodeGroup struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	RegistrationIDs []string `json:"registrationIds"`
	Revision        string   `json:"revision"`
	UpdatedAt       int64    `json:"updatedAt"`
}
type fleetSourceReference struct {
	RegistrationID string `json:"registrationId"`
	MonitorID      string `json:"monitorId"`
	Revision       string `json:"revision"`
}

func (source *fleetSourceReference) UnmarshalJSON(b []byte) error {
	type plain fleetSourceReference
	return dashboardJSON(b, (*plain)(source), "registrationId", "monitorId", "revision")
}

type dashboardFleetPlacement struct {
	ID                      string   `json:"id"`
	RegistrationID          string   `json:"registrationId"`
	RegistrationIDs         []string `json:"registrationIds"`
	RetiringRegistrationIDs []string `json:"retiringRegistrationIds,omitempty"`
	NodeIDs                 []string `json:"nodeIds"`
	MonitorID               string   `json:"monitorId"`
	Sequence                int64    `json:"sequence"`
	Revision                string   `json:"revision"`
	State                   string   `json:"state"`
}
type dashboardFleetRecord struct {
	ID              string                    `json:"id"`
	Revision        string                    `json:"revision"`
	UpdatedAt       int64                     `json:"updatedAt"`
	RegistrationIDs []string                  `json:"registrationIds"`
	Source          *fleetSourceReference     `json:"source,omitempty"`
	SourceCompleted bool                      `json:"sourceCompleted,omitempty"`
	Placements      []dashboardFleetPlacement `json:"placements"`
	State           string                    `json:"state"`
	Reason          string                    `json:"reason,omitempty"`
	Private         string                    `json:"private"`
	ActorID         string                    `json:"actorId"`
	ActorRole       string                    `json:"actorRole"`
	ActorVersion    uint64                    `json:"actorVersion"`
	RetryAt         int64                     `json:"retryAt"`
}
type dashboardFleetPlan struct {
	ObservedRevisions map[string]string                `json:"observedRevisions,omitempty"`
	References        map[string]fleetTargetReferences `json:"references,omitempty"`
	Monitor           MonitorInput                     `json:"monitor"`
	Prepared          bool                             `json:"prepared"`
	Deleted           bool                             `json:"deleted"`
	Rejected          map[string]bool                  `json:"rejected,omitempty"`
	Unsent            map[string]bool                  `json:"unsent,omitempty"`
	// Monitor settings are held once, rather than copied into 64 encrypted commands.
	Commands map[string]fleetPlacementCommand `json:"commands"`
}

type fleetTargetReferences struct {
	NotificationIDs *[]string     `json:"notificationIds,omitempty"`
	GroupID         *string       `json:"groupId,omitempty"`
	Tags            *[]MonitorTag `json:"tags,omitempty"`
}

func fleetReferencedInput(plan dashboardFleetPlan, p dashboardFleetPlacement) (MonitorInput, error) {
	apply := func(id string) MonitorInput {
		in := plan.Monitor
		ref := plan.References[id]
		if ref.NotificationIDs != nil {
			in.NotificationIDs = slices.Clone(*ref.NotificationIDs)
		}
		if ref.GroupID != nil {
			in.GroupID = *ref.GroupID
		}
		if ref.Tags != nil {
			in.Tags = slices.Clone(*ref.Tags)
		}
		in.NodeIDs = slices.Clone(p.NodeIDs)
		return in
	}
	input := apply(p.RegistrationID)
	if len(p.RegistrationIDs) > 0 {
		input = apply(p.RegistrationIDs[0])
		for _, id := range p.RegistrationIDs[1:] {
			if !equalJSON(input, apply(id)) {
				return MonitorInput{}, fail(400, "Members of one HA placement must choose matching channel, group and tag references")
			}
		}
	}
	return input, nil
}

func fleetAAD(row dashboardFleetRecord) []byte {
	// Authenticate public routing/ownership/state along with the private plan.
	// A database-only edit must not send decrypted settings to another target.
	row.Private = ""
	metadata, _ := json.Marshal(row)
	return append([]byte("canary-fleet-v1\x00"), metadata...)
}
func (d *Dashboard) sealFleet(row dashboardFleetRecord, plan dashboardFleetPlan) (string, error) {
	b, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, d.box.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", fail(503, "Cryptographic randomness unavailable")
	}
	return base64.RawStdEncoding.EncodeToString(d.box.Seal(nonce, nonce, b, fleetAAD(row))), nil
}
func (d *Dashboard) openFleet(row dashboardFleetRecord) (dashboardFleetPlan, error) {
	var plan dashboardFleetPlan
	b, err := base64.RawStdEncoding.DecodeString(row.Private)
	if err != nil || len(b) < d.box.NonceSize()+d.box.Overhead() {
		return plan, errors.New("Invalid encrypted fleet configuration")
	}
	b, err = d.box.Open(nil, b[:d.box.NonceSize()], b[d.box.NonceSize():], fleetAAD(row))
	if err != nil || json.Unmarshal(b, &plan) != nil {
		return plan, errors.New("Cannot decrypt fleet configuration")
	}
	return plan, nil
}
func (d *Dashboard) loadFleet() error {
	n := d.accounts.node
	n.mu.Lock()
	defer n.mu.Unlock()
	rows, err := loadAll[dashboardFleetRecord](n.store, "dashboardFleet")
	if err != nil || len(rows) > maxMonitors {
		return errors.New("Cannot load bounded dashboard fleet")
	}
	for id, row := range rows {
		if id != row.ID || !fleetPlacementID(id) || identifier(row.Revision, "revision") != nil || row.ActorID == "" || row.ActorVersion == 0 || len(row.Placements) > 64 || !validIDs(row.RegistrationIDs, 64) {
			return errors.New("Invalid saved fleet configuration")
		}
		if _, err = d.openFleet(row); err != nil {
			return err
		}
	}
	d.fleet = rows
	d.fleetBusy = map[string]bool{}
	return nil
}
func copyFleet(row dashboardFleetRecord) dashboardFleetRecord {
	row.RegistrationIDs = slices.Clone(row.RegistrationIDs)
	row.Placements = slices.Clone(row.Placements)
	for i := range row.Placements {
		row.Placements[i].RegistrationIDs = slices.Clone(row.Placements[i].RegistrationIDs)
		row.Placements[i].RetiringRegistrationIDs = slices.Clone(row.Placements[i].RetiringRegistrationIDs)
		row.Placements[i].NodeIDs = slices.Clone(row.Placements[i].NodeIDs)
	}
	return row
}

func fleetCanCancel(row dashboardFleetRecord, plan dashboardFleetPlan) bool {
	if plan.Prepared {
		return false
	}
	for _, p := range row.Placements {
		if p.Sequence > 0 {
			return false
		}
	}
	return true
}

// A prepared command with no durable acknowledgement can already have run.
// Changes and retirement must first resolve that exact saved command.
func fleetWritesConfirmed(row dashboardFleetRecord, plan dashboardFleetPlan) bool {
	if !plan.Prepared {
		return true
	}
	for _, p := range row.Placements {
		if command, ok := plan.Commands[p.ID]; ok && (command.Sequence != p.Sequence || p.State == "pending") {
			return false
		}
	}
	return true
}

func fleetCanReconcile(row dashboardFleetRecord, plan dashboardFleetPlan, observed map[string]string) bool {
	if !plan.Prepared {
		return true
	}
	for _, p := range row.Placements {
		command, ok := plan.Commands[p.ID]
		if ok && (command.Sequence != p.Sequence || p.State == "pending") {
			if plan.Unsent[p.ID] {
				continue
			}
			// Only a definite refusal plus a different, explicitly reviewed CAS
			// revision can replace this command. Unknown answers retain exact replay.
			if !plan.Rejected[p.ID] || observed[p.ID] == "" || observed[p.ID] == command.Revision {
				return false
			}
		}
	}
	return true
}

// Caller holds d.mu. Identity generations and the journal commit share the account lock.
func (d *Dashboard) saveFleet(r *http.Request, actor AccountView, row dashboardFleetRecord, plan dashboardFleetPlan) error {
	n := d.accounts.node
	n.mu.Lock()
	defer n.mu.Unlock()
	if r != nil {
		a, err := n.authenticate(r)
		if err != nil {
			return err
		}
		if a.ID != actor.ID || a.Role != actor.Role || a.Role == "viewer" {
			return fail(403, "Dashboard editor permission is required")
		}
		if r.Context().Err() != nil {
			return fail(408, "Request cancelled")
		}
		row.ActorID, row.ActorRole, row.ActorVersion = a.ID, a.Role, a.AuthVersion
	}
	var err error
	row.Private, err = d.sealFleet(row, plan)
	if err != nil {
		return err
	}
	if err = n.identityTransactionLocked(func(e dbExec) error { return put(e, "dashboardFleet", row.ID, row) }); err != nil {
		return err
	}
	d.fleet[row.ID] = copyFleet(row)
	return nil
}
func (d *Dashboard) fleetAuthorized(row dashboardFleetRecord) bool {
	n := d.accounts.node
	n.mu.Lock()
	defer n.mu.Unlock()
	a, ok := n.accounts[row.ActorID]
	return ok && a.Role == row.ActorRole && a.Role != "viewer" && a.AuthVersion == row.ActorVersion && !n.stopped && n.fault == nil
}
func (d *Dashboard) publicFleet(row dashboardFleetRecord, role string) (any, error) {
	plan, err := d.openFleet(row)
	if err != nil {
		return nil, fail(503, "Fleet configuration unavailable")
	}
	return struct {
		ID              string                           `json:"id"`
		Revision        string                           `json:"revision"`
		UpdatedAt       int64                            `json:"updatedAt"`
		RegistrationIDs []string                         `json:"registrationIds"`
		Source          *fleetSourceReference            `json:"source,omitempty"`
		Placements      []dashboardFleetPlacement        `json:"placements"`
		State           string                           `json:"state"`
		Reason          string                           `json:"reason,omitempty"`
		Monitor         Monitor                          `json:"monitor"`
		CanCancel       bool                             `json:"canCancel"`
		CanRemove       bool                             `json:"canRemove"`
		References      map[string]fleetTargetReferences `json:"references,omitempty"`
	}{row.ID, row.Revision, row.UpdatedAt, slices.Clone(row.RegistrationIDs), func() *fleetSourceReference {
		if row.SourceCompleted {
			return nil
		}
		return row.Source
	}(), slices.Clone(row.Placements), row.State, row.Reason,
		publicMonitor(Monitor{MonitorInput: plan.Monitor, ID: row.ID, Revision: row.Revision, UpdatedAt: row.UpdatedAt, Deleted: plan.Deleted}, role), fleetCanCancel(row, plan), fleetWritesConfirmed(row, plan) && !plan.Deleted && row.State != "removed", func() map[string]fleetTargetReferences {
			if role == "viewer" {
				return nil
			}
			return plan.References
		}()}, nil
}
func (d *Dashboard) fleetRequest(w http.ResponseWriter, r *http.Request, actor AccountView) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		errorReply(w, fail(400, "Fleet routes do not accept a query"))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/dashboard/node-groups") {
		d.nodeGroupRequest(w, r, actor)
		return
	}
	var b []byte
	var err error
	if r.Method != "GET" {
		b, err = readBounded(r.Body, maxAPIBody)
		if err != nil {
			errorReply(w, err)
			return
		}
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/dashboard/fleet/monitors"), "/")
	id, action := "", ""
	if len(parts) > 1 {
		id = parts[1]
	}
	if len(parts) > 2 {
		action = parts[2]
	}
	if len(parts) > 3 || id != "" && !fleetPlacementID(id) {
		errorReply(w, fail(404, "Not found"))
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err = d.currentActor(r, actor, false); err != nil {
		errorReply(w, err)
		return
	}
	if r.Method == "GET" && id == "" {
		out := []any{}
		for _, row := range d.fleet {
			v, err := d.publicFleet(row, actor.Role)
			if err != nil {
				errorReply(w, err)
				return
			}
			out = append(out, v)
		}
		reply(w, 200, out)
		return
	}
	if actor.Role == "viewer" {
		errorReply(w, fail(403, "Dashboard editor permission is required"))
		return
	}
	row, exists := d.fleet[id]
	if id != "" && !exists {
		errorReply(w, fail(404, "Fleet monitor not found"))
		return
	}
	if id != "" && d.fleetBusy[id] {
		errorReply(w, fail(409, "A deployment is already running; reload and retry"))
		return
	}
	var in struct {
		RequestID         string                     `json:"requestId"`
		Revision          string                     `json:"revision"`
		Monitor           json.RawMessage            `json:"monitor"`
		RegistrationIDs   []string                   `json:"registrationIds"`
		Source            *fleetSourceReference      `json:"source"`
		References        map[string]json.RawMessage `json:"references"`
		ObservedRevisions map[string]string          `json:"observedRevisions"`
	}
	if err = dashboardJSON(b, &in, "requestId", "revision", "monitor", "registrationIds", "source", "references", "observedRevisions"); err != nil {
		errorReply(w, err)
		return
	}
	if id != "" && in.Revision != row.Revision {
		errorReply(w, fail(409, "Fleet configuration changed; reload before saving"))
		return
	}
	if id != "" && (r.Method == "DELETE" && action == "" || (action == "retry" || action == "reconcile") && r.Method == "POST") {
		plan, e := d.openFleet(row)
		if e != nil {
			errorReply(w, fail(503, "Fleet configuration unavailable"))
			return
		}
		if action == "reconcile" {
			if !fleetCanReconcile(row, plan, in.ObservedRevisions) || row.State == "removed" || len(in.ObservedRevisions) < 1 || len(in.ObservedRevisions) > 64 {
				errorReply(w, fail(409, "Resolve the saved command before reviewing node edits"))
				return
			}
			for placement, revision := range in.ObservedRevisions {
				found := false
				for _, p := range row.Placements {
					if p.ID == placement && p.State != "removed" && (p.Sequence > 0 || row.Source != nil && p.MonitorID == row.Source.MonitorID && p.RegistrationID == row.Source.RegistrationID) {
						found = true
					}
				}
				if !found || identifier(revision, "observed revision") != nil {
					errorReply(w, fail(400, "Review only this deployment's active placement revisions"))
					return
				}
			}
			plan.ObservedRevisions = in.ObservedRevisions
			plan.Prepared, plan.Commands = false, nil
		}
		if r.Method == "DELETE" {
			if row.State == "removed" {
				reply(w, 204, nil)
				return
			}
			if plan.Deleted {
				// Retrying removal after a lost answer must retain the original
				// deletion, including acknowledged anchors and offline members.
				v, err := d.publicFleet(row, actor.Role)
				if err != nil {
					errorReply(w, err)
					return
				}
				reply(w, 202, v)
				return
			}
			if fleetCanCancel(row, plan) {
				row.Source = nil
				row.Placements = []dashboardFleetPlacement{}
				row.RegistrationIDs = []string{}
				row.State = "removed"
				row.Reason = ""
				row.Revision = newID()
				plan.Deleted = true
				plan.Prepared = true
				plan.Commands = nil
				if err = d.saveFleet(r, actor, row, plan); err != nil {
					errorReply(w, err)
					return
				}
				reply(w, 204, nil)
				return
			}
			if !fleetWritesConfirmed(row, plan) {
				errorReply(w, fail(409, "Finish the saved deployment before removing it"))
				return
			}
			plan.Deleted = true
			plan.Prepared = false
			plan.Commands = nil
			plan.ObservedRevisions = nil
		}
		row.Revision = newID()
		row.State = "pending"
		row.Reason = ""
		row.RetryAt = 0
		if err = d.saveFleet(r, actor, row, plan); err != nil {
			errorReply(w, err)
			return
		}
		v, e := d.publicFleet(d.fleet[row.ID], actor.Role)
		if e != nil {
			errorReply(w, e)
			return
		}
		reply(w, 202, v)
		return
	}
	if action != "" || !(id == "" && r.Method == "POST" || id != "" && r.Method == "PUT") {
		errorReply(w, fail(404, "Not found"))
		return
	}
	if len(in.ObservedRevisions) > 0 {
		errorReply(w, fail(400, "Observed revisions require explicit reconciliation"))
		return
	}
	if len(in.RegistrationIDs) < 1 || !validIDs(in.RegistrationIDs, 64) || len(unique(in.RegistrationIDs)) != len(in.RegistrationIDs) {
		errorReply(w, fail(400, "Choose one to 64 distinct registered nodes"))
		return
	}
	for _, registration := range in.RegistrationIDs {
		if _, ok := d.registry[registration]; !ok {
			errorReply(w, fail(400, "A selected node is no longer registered"))
			return
		}
	}
	var input MonitorInput
	if json.Unmarshal(in.Monitor, &input) != nil {
		errorReply(w, fail(400, "Invalid monitor configuration"))
		return
	}
	known := map[string]bool{}
	for _, nodeID := range input.NodeIDs {
		known[nodeID] = true
	}
	input, err = parseMonitor(in.Monitor, known)
	if err != nil {
		errorReply(w, err)
		return
	}
	var references map[string]fleetTargetReferences
	if len(in.References) > 0 {
		references = map[string]fleetTargetReferences{}
	}
	for registration, raw := range in.References {
		if !slices.Contains(in.RegistrationIDs, registration) {
			errorReply(w, fail(400, "References must name selected registrations only"))
			return
		}
		var ref fleetTargetReferences
		if err = dashboardJSON(raw, &ref, "notificationIds", "groupId", "tags"); err != nil {
			errorReply(w, err)
			return
		}
		references[registration] = ref
	}
	// Admission on each target follows later, before any write, including local resource references.
	if validText(input.Name, "monitor name", 128) != nil {
		errorReply(w, fail(400, "Invalid monitor name"))
		return
	}
	if id == "" {
		if !fleetPlacementID(in.RequestID) {
			errorReply(w, fail(400, "A unique placement request ID is required"))
			return
		}
		if prior, ok := d.fleet[in.RequestID]; ok {
			plan, e := d.openFleet(prior)
			if e != nil {
				errorReply(w, fail(503, "Fleet configuration unavailable"))
				return
			}
			if prior.ActorID != actor.ID || !equalJSON(plan.Monitor, input) || !equalJSON(plan.References, references) || !equalJSON(prior.RegistrationIDs, in.RegistrationIDs) || !equalJSON(prior.Source, in.Source) {
				errorReply(w, fail(409, "That request already created a different fleet monitor; reload All nodes"))
				return
			}
			v, e := d.publicFleet(prior, actor.Role)
			if e != nil {
				errorReply(w, e)
				return
			}
			reply(w, 202, v)
			return
		}
		if len(d.fleet) >= maxMonitors {
			errorReply(w, fail(409, "Fleet monitor capacity exceeded"))
			return
		}
		row = dashboardFleetRecord{ID: in.RequestID, Placements: []dashboardFleetPlacement{}, Source: in.Source}
		if in.Source != nil {
			if !slices.Contains(in.RegistrationIDs, in.Source.RegistrationID) || identifier(in.Source.MonitorID, "source monitor") != nil || identifier(in.Source.Revision, "source revision") != nil {
				errorReply(w, fail(400, "Keep the original node selected when first managing an existing monitor"))
				return
			}
			for _, other := range d.fleet {
				if other.State == "removed" {
					continue
				}
				sameSource := other.Source != nil && other.Source.RegistrationID == in.Source.RegistrationID && other.Source.MonitorID == in.Source.MonitorID
				for _, p := range other.Placements {
					sameSource = sameSource || p.MonitorID == in.Source.MonitorID && (p.RegistrationID == in.Source.RegistrationID || slices.Contains(p.RegistrationIDs, in.Source.RegistrationID))
				}
				if sameSource {
					errorReply(w, fail(409, "This monitor already has a fleet placement; reload All nodes"))
					return
				}
			}
		}
	} else {
		plan, e := d.openFleet(row)
		if e != nil {
			errorReply(w, fail(503, "Fleet configuration unavailable"))
			return
		}
		if plan.Deleted || !fleetWritesConfirmed(row, plan) {
			errorReply(w, fail(409, "Finish or retry the saved deployment before editing it"))
			return
		}
		if in.References == nil {
			references = map[string]fleetTargetReferences{}
			for registration, ref := range plan.References {
				if slices.Contains(in.RegistrationIDs, registration) {
					references[registration] = ref
				}
			}
		}
	}
	row = copyFleet(row)
	row.RegistrationIDs = slices.Clone(in.RegistrationIDs)
	row.Revision = newID()
	row.UpdatedAt = time.Now().UnixMilli()
	row.State = "pending"
	row.Reason = ""
	row.RetryAt = 0
	plan := dashboardFleetPlan{Monitor: input, References: references}
	if err = d.saveFleet(r, actor, row, plan); err != nil {
		errorReply(w, err)
		return
	}
	v, e := d.publicFleet(d.fleet[row.ID], actor.Role)
	if e != nil {
		errorReply(w, e)
		return
	}
	reply(w, 202, v)
}

func (d *Dashboard) nodeGroupRequest(w http.ResponseWriter, r *http.Request, actor AccountView) {
	var b []byte
	var err error
	if r.Method != "GET" {
		b, err = readBounded(r.Body, 16384)
		if err != nil {
			errorReply(w, err)
			return
		}
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/dashboard/node-groups")
	if id != "" {
		if !strings.HasPrefix(id, "/") {
			errorReply(w, fail(404, "Not found"))
			return
		}
		id = strings.TrimPrefix(id, "/")
		if !fleetPlacementID(id) {
			errorReply(w, fail(404, "Not found"))
			return
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.accounts.node
	n.mu.Lock()
	defer n.mu.Unlock()
	a, err := n.authenticate(r)
	if err != nil {
		errorReply(w, err)
		return
	}
	if a.ID != actor.ID || a.Role != actor.Role {
		errorReply(w, fail(409, "Dashboard permissions changed"))
		return
	}
	all, err := loadAll[dashboardNodeGroup](n.store, "dashboardNodeGroups")
	if err != nil {
		errorReply(w, fail(503, "Node groups unavailable"))
		return
	}
	if r.Method == "GET" && id == "" {
		out := []dashboardNodeGroup{}
		for _, g := range all {
			out = append(out, g)
		}
		reply(w, 200, out)
		return
	}
	if a.Role == "viewer" {
		errorReply(w, fail(403, "Dashboard editor permission is required"))
		return
	}
	var g dashboardNodeGroup
	if err = dashboardJSON(b, &g, "name", "registrationIds", "revision"); err != nil {
		errorReply(w, err)
		return
	}
	if id != "" {
		old, ok := all[id]
		if !ok {
			errorReply(w, fail(404, "Node group not found"))
			return
		}
		if g.Revision != old.Revision {
			errorReply(w, fail(409, "Node group changed; reload before saving"))
			return
		}
	}
	if id != "" && r.Method == "DELETE" {
		if err = n.identityTransactionLocked(func(e dbExec) error { return remove(e, "dashboardNodeGroups", id) }); err != nil {
			errorReply(w, err)
			return
		}
		reply(w, 204, nil)
		return
	}
	if !(id == "" && r.Method == "POST" || id != "" && r.Method == "PUT") {
		errorReply(w, fail(404, "Not found"))
		return
	}
	if validText(g.Name, "group name", 128) != nil || len(g.RegistrationIDs) < 1 || !validIDs(g.RegistrationIDs, 64) || len(unique(g.RegistrationIDs)) != len(g.RegistrationIDs) {
		errorReply(w, fail(400, "Use a name and one to 64 distinct registered nodes"))
		return
	}
	for _, registration := range g.RegistrationIDs {
		if _, ok := d.registry[registration]; !ok {
			errorReply(w, fail(400, "Remove missing registrations before saving the group"))
			return
		}
	}
	for otherID, existing := range all {
		if otherID == id || existing.Name != g.Name {
			continue
		}
		left, right := slices.Clone(existing.RegistrationIDs), slices.Clone(g.RegistrationIDs)
		slices.Sort(left)
		slices.Sort(right)
		// A lost create response is recoverable without duplicating a saved
		// selection. Equal names with different membership require an explicit edit.
		if id == "" && slices.Equal(left, right) {
			reply(w, 200, existing)
			return
		}
		errorReply(w, fail(409, "A node group already uses this name; reload and edit that group"))
		return
	}
	if id == "" {
		if len(all) >= 200 {
			errorReply(w, fail(409, "Node group capacity exceeded"))
			return
		}
		id = strings.ReplaceAll(newID(), "-", "")
	}
	g.ID = id
	g.Revision = newID()
	g.UpdatedAt = time.Now().UnixMilli()
	if r.Context().Err() != nil {
		errorReply(w, fail(408, "Request cancelled"))
		return
	}
	if err = n.identityTransactionLocked(func(e dbExec) error { return put(e, "dashboardNodeGroups", id, g) }); err != nil {
		errorReply(w, err)
		return
	}
	reply(w, 200, g)
}

func fleetCommandBytes(plan dashboardFleetPlan, p dashboardFleetPlacement) ([]byte, error) {
	command := plan.Commands[p.ID]
	if !command.Deleted {
		input, err := fleetReferencedInput(plan, p)
		if err != nil {
			return nil, err
		}
		b, err := json.Marshal(input)
		if err != nil {
			return nil, err
		}
		command.Monitor = b
	}
	return json.Marshal(command)
}
func (d *Dashboard) fleetRead(ctx context.Context, registration, role string) (Overview, registeredNodeRecord, error) {
	d.mu.Lock()
	row, ok := d.registry[registration]
	d.mu.Unlock()
	if !ok || dashboardOperationPending(row.State) {
		return Overview{}, row, fail(409, "Node access is not ready")
	}
	secret, err := d.openSecrets(row)
	if err != nil {
		return Overview{}, row, err
	}
	if err = d.nodeActor(ctx, row, secret.token(role), role); err != nil {
		return Overview{}, row, err
	}
	view, err := d.readNode(ctx, row, secret.token(role))
	return view, row, err
}
func (d *Dashboard) prepareFleet(ctx context.Context, row *dashboardFleetRecord, plan *dashboardFleetPlan) error {
	desired := row.RegistrationIDs
	if plan.Deleted {
		desired = nil
	}
	ids := slices.Clone(desired)
	for _, p := range row.Placements {
		if p.State != "removed" && (!plan.Deleted || p.Sequence > 0) {
			ids = append(ids, p.RegistrationID)
		}
	}
	ids = unique(ids)
	views := map[string]Overview{}
	var lock sync.Mutex
	var first error
	jobs := make(chan string)
	var workers sync.WaitGroup
	for range min(4, len(ids)) {
		workers.Go(func() {
			for id := range jobs {
				v, _, err := d.fleetRead(ctx, id, row.ActorRole)
				lock.Lock()
				if err != nil && first == nil {
					first = err
				}
				if err == nil {
					views[id] = v
				}
				lock.Unlock()
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if first != nil {
		return fail(502, "A selected node cannot be read yet")
	}
	placements := []dashboardFleetPlacement{}
	claimed := map[string]bool{}
	knownPeer := func(a, b string) bool {
		left, right := views[a], views[b]
		has := func(v Overview, id string) bool {
			for _, n := range v.Nodes {
				if n.ID == id && n.State != "local" {
					return true
				}
			}
			return false
		}
		return a == b || has(left, right.Node.ID) && has(right, left.Node.ID)
	}
	// Keep existing anchors and physical IDs. A network partition must not move
	// a management authority to another peer or manufacture a conflicting root.
	for _, p := range row.Placements {
		if p.State == "removed" || plan.Deleted && p.Sequence == 0 {
			continue
		}
		p = copyFleet(dashboardFleetRecord{Placements: []dashboardFleetPlacement{p}}).Placements[0]
		members := []string{}
		for _, id := range desired {
			if slices.Contains(p.RegistrationIDs, id) || knownPeer(p.RegistrationID, id) {
				if !claimed[id] {
					members = append(members, id)
					claimed[id] = true
				}
			}
		}
		retiring := []string{}
		for _, id := range unique(append(slices.Clone(p.RegistrationIDs), p.RetiringRegistrationIDs...)) {
			if !slices.Contains(members, id) {
				retiring = append(retiring, id)
			}
		}
		p.RegistrationIDs, p.RetiringRegistrationIDs = members, retiring
		p.NodeIDs = []string{}
		for _, id := range members {
			p.NodeIDs = append(p.NodeIDs, views[id].Node.ID)
		}
		if p.Sequence > 0 || len(members) > 0 {
			placements = append(placements, p)
		}
	}
	remaining := slices.Clone(desired)
	slices.Sort(remaining)
	if row.Source != nil && !row.SourceCompleted && slices.Contains(desired, row.Source.RegistrationID) && !claimed[row.Source.RegistrationID] {
		remaining = append([]string{row.Source.RegistrationID}, remaining...)
	}
	for _, anchor := range remaining {
		if claimed[anchor] {
			continue
		}
		members := []string{}
		nodeIDs := []string{}
		for _, id := range desired {
			if !claimed[id] && knownPeer(anchor, id) {
				claimed[id] = true
				members = append(members, id)
				nodeIDs = append(nodeIDs, views[id].Node.ID)
			}
		}
		id := strings.ReplaceAll(newID(), "-", "")
		p := dashboardFleetPlacement{ID: id, RegistrationID: anchor, RegistrationIDs: members, NodeIDs: nodeIDs, MonitorID: "fleet-" + id}
		if row.Source != nil && !row.SourceCompleted && anchor == row.Source.RegistrationID {
			p.MonitorID = row.Source.MonitorID
			p.Revision = row.Source.Revision
		}
		placements = append(placements, p)
	}
	commands := map[string]fleetPlacementCommand{}
	for i := range placements {
		p := &placements[i]
		var current *MonitorView
		for _, view := range views[p.RegistrationID].Monitors {
			if view.Monitor.ID == p.MonitorID {
				current = &view
				break
			}
		}
		if observed, ok := plan.ObservedRevisions[p.ID]; ok {
			if current == nil || current.ConfigConflict || current.Monitor.Revision != observed {
				return fail(409, "A reviewed node revision changed again; refresh before reconciling")
			}
			if p.Sequence > 0 {
				d.mu.Lock()
				target := d.registry[p.RegistrationID]
				d.mu.Unlock()
				secret, err := d.openSecrets(target)
				if err != nil {
					return err
				}
				response, b, err := d.nodeRequest(ctx, target, secret.token(row.ActorRole), "GET", "/api/fleet-placements/"+p.ID, nil)
				var receipt fleetPlacementResult
				if err != nil || response.StatusCode != 200 || json.Unmarshal(b, &receipt) != nil || receipt.PlacementID != p.ID || receipt.MonitorID != p.MonitorID || receipt.Sequence != p.Sequence || receipt.Deleted {
					return fail(409, "The saved placement receipt differs; resolve its original command first")
				}
			}
			p.Revision = observed
		}
		if current != nil && (p.Sequence == 0 && p.MonitorID != "fleet-"+p.ID || plan.ObservedRevisions[p.ID] != "") {
			// Adoption or explicit reconciliation may replace a native assignment
			// wider than the last dashboard plan. Track every former runner; an
			// unregistered runner cannot honestly be confirmed stopped here.
			for _, nodeID := range current.Monitor.NodeIDs {
				if slices.Contains(p.NodeIDs, nodeID) {
					continue
				}
				d.mu.Lock()
				registration := ""
				for id, node := range d.registry {
					if node.NodeID == nodeID {
						registration = id
						break
					}
				}
				d.mu.Unlock()
				if registration == "" {
					return fail(409, "Register every original runner before replacing its existing monitor assignment")
				}
				p.RetiringRegistrationIDs = unique(append(p.RetiringRegistrationIDs, registration))
			}
		}
		command := fleetPlacementCommand{Sequence: p.Sequence + 1, Revision: p.Revision, Deleted: plan.Deleted || len(p.NodeIDs) == 0}
		if p.Sequence == 0 && p.MonitorID != "fleet-"+p.ID {
			command.Adopt = true
			command.MonitorID = p.MonitorID
		}
		commands[p.ID] = command
		p.State = "pending"
	}
	candidatePlan := *plan
	candidatePlan.Commands = commands
	// Validate the complete plan on every anchor before journaling or applying
	// any remote changes. Missing node-local channels/tags/groups are not dropped.
	for _, p := range placements {
		if !d.fleetAuthorized(*row) {
			return fail(403, "Deployment authorization changed")
		}
		d.mu.Lock()
		target, ok := d.registry[p.RegistrationID]
		d.mu.Unlock()
		if !ok {
			return fail(409, "A deployment registration was removed")
		}
		secret, err := d.openSecrets(target)
		if err != nil {
			return err
		}
		body, err := fleetCommandBytes(candidatePlan, p)
		if err != nil {
			return err
		}
		response, _, err := d.nodeRequest(ctx, target, secret.token(row.ActorRole), "POST", "/api/fleet-placements/"+p.ID+"/validate", body)
		if err != nil {
			return fail(502, "A selected node cannot be reached yet")
		}
		if response.StatusCode != 200 {
			return fail(response.StatusCode, "A node refused the deployment preflight; check its version, revision and local channel, tag or group references")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	row.Placements = placements
	plan.Commands = commands
	plan.Prepared = true
	plan.ObservedRevisions = nil
	plan.Rejected = nil
	plan.Unsent = map[string]bool{}
	for id := range commands {
		plan.Unsent[id] = true
	}
	return nil
}
func (d *Dashboard) finishFleet(ctx context.Context, id string) {
	d.mu.Lock()
	row, ok := d.fleet[id]
	if !ok || d.fleetBusy[id] || row.State == "applied" || row.State == "removed" || row.State == "blocked" || row.State == "unauthorized" {
		d.mu.Unlock()
		return
	}
	d.fleetBusy[id] = true
	row = copyFleet(row)
	d.mu.Unlock()
	defer func() { d.mu.Lock(); delete(d.fleetBusy, id); d.mu.Unlock() }()
	plan, err := d.openFleet(row)
	if err != nil {
		return
	}
	save := func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.fleet[id].Revision != row.Revision {
			return false
		}
		return d.saveFleet(nil, AccountView{}, row, plan) == nil
	}
	if !d.fleetAuthorized(row) {
		row.State = "unauthorized"
		row.Reason = "The authorizing account changed; an editor must review and retry this saved deployment"
		save()
		return
	}
	if !plan.Prepared {
		if err = d.prepareFleet(ctx, &row, &plan); err != nil {
			row.State = "pending"
			var api *apiError
			if errors.As(err, &api) && (api.Status == 400 || api.Status == 404 || api.Status == 409) {
				row.State = "blocked"
			}
			row.Reason = dashboardErrorMessage(err)
			row.RetryAt = time.Now().Add(30 * time.Second).UnixMilli()
			save()
			return
		}
		if !save() {
			return
		}
	}
	for i := range row.Placements {
		p := &row.Placements[i]
		if p.State == "applied" || p.State == "removed" || p.State == "syncing" {
			continue
		}
		if !d.fleetAuthorized(row) {
			row.State = "unauthorized"
			row.Reason = "The authorizing account changed; review and retry this saved deployment"
			save()
			return
		}
		d.mu.Lock()
		target, exists := d.registry[p.RegistrationID]
		d.mu.Unlock()
		if !exists || dashboardOperationPending(target.State) {
			row.Reason = "A placement is waiting for its registered node"
			row.RetryAt = time.Now().Add(30 * time.Second).UnixMilli()
			save()
			return
		}
		secret, e := d.openSecrets(target)
		if e != nil {
			return
		}
		body, e := fleetCommandBytes(plan, *p)
		if e != nil {
			return
		}
		// Verify the role immediately before each write. Never retry an ordinary
		// create POST after a missing response: only this saved command is replayed.
		if e = d.nodeActor(ctx, target, secret.token(row.ActorRole), row.ActorRole); e != nil {
			row.Reason = "A node refused its dashboard role credential"
			row.RetryAt = time.Now().Add(30 * time.Second).UnixMilli()
			save()
			return
		}
		// Every attempt becomes unknown before it leaves, including a retry
		// after a refusal that may now succeed but lose its acknowledgement.
		delete(plan.Unsent, p.ID)
		delete(plan.Rejected, p.ID)
		if !save() {
			return
		}
		response, b, e := d.nodeRequest(ctx, target, secret.token(row.ActorRole), "PUT", "/api/fleet-placements/"+p.ID, body)
		var result fleetPlacementResult
		command := plan.Commands[p.ID]
		if e != nil || response.StatusCode != 200 && response.StatusCode != 201 || json.Unmarshal(b, &result) != nil || result.PlacementID != p.ID || result.MonitorID != p.MonitorID || result.Sequence != command.Sequence || result.Deleted != command.Deleted || identifier(result.Revision, "revision") != nil {
			row.State = "pending"
			row.Reason = "A placement has not confirmed the saved command; it will be retried safely"
			if e == nil && (response.StatusCode == 400 || response.StatusCode == 404 || response.StatusCode == 409) {
				row.State = "blocked"
				row.Reason = "A placement needs review on its node; its version, references or monitor revision differs"
			}
			if e == nil && response.StatusCode == 409 {
				if plan.Rejected == nil {
					plan.Rejected = map[string]bool{}
				}
				plan.Rejected[p.ID] = true
			}
			row.RetryAt = time.Now().Add(30 * time.Second).UnixMilli()
			save()
			return
		}
		p.Sequence, p.Revision = result.Sequence, result.Revision
		p.State = "syncing"
		if command.Adopt {
			row.SourceCompleted = true
		}
		if !save() {
			return
		}
	}
	// An anchor acknowledgement is not proof that an offline HA member has
	// stopped or updated its checks. Keep the journal and retiring membership
	// until each member's own authenticated snapshot confirms the saved revision.
	for i := range row.Placements {
		p := &row.Placements[i]
		if p.State != "syncing" {
			continue
		}
		if !d.fleetAuthorized(row) {
			row.State = "unauthorized"
			row.Reason = "The authorizing account changed; review and retry this saved deployment"
			save()
			return
		}
		if err = d.confirmFleetPlacement(ctx, row, plan, *p); err != nil {
			row.Reason = "Waiting for every assigned or retiring node to confirm its configuration"
			row.RetryAt = time.Now().Add(30 * time.Second).UnixMilli()
			save()
			return
		}
		p.State = "applied"
		if plan.Commands[p.ID].Deleted {
			p.State = "removed"
		}
		p.RetiringRegistrationIDs = nil
		if !save() {
			return
		}
	}
	row.State = "applied"
	if plan.Deleted {
		row.State = "removed"
	}
	row.Reason = ""
	row.RetryAt = 0
	save()
}

func (d *Dashboard) confirmFleetPlacement(ctx context.Context, row dashboardFleetRecord, plan dashboardFleetPlan, p dashboardFleetPlacement) error {
	ids := unique(append(append([]string{p.RegistrationID}, p.RegistrationIDs...), p.RetiringRegistrationIDs...))
	for _, id := range ids {
		if !d.fleetAuthorized(row) {
			return fail(403, "Deployment authorization changed")
		}
		view, _, err := d.fleetRead(ctx, id, row.ActorRole)
		if err != nil {
			return err
		}
		var monitor *MonitorView
		for i := range view.Monitors {
			if view.Monitors[i].Monitor.ID == p.MonitorID {
				monitor = &view.Monitors[i]
				break
			}
		}
		if plan.Commands[p.ID].Deleted {
			if monitor != nil {
				return fail(409, "A node has not confirmed the monitor deletion")
			}
			continue
		}
		if monitor == nil || monitor.ConfigConflict || monitor.Monitor.Revision != p.Revision ||
			slices.Contains(monitor.Monitor.NodeIDs, view.Node.ID) != slices.Contains(p.RegistrationIDs, id) {
			return fail(409, "A node has not confirmed the saved monitor assignment")
		}
	}
	return nil
}
func (d *Dashboard) runFleet(ctx context.Context) {
	for {
		d.mu.Lock()
		ids := []string{}
		for id, row := range d.fleet {
			if row.State == "pending" && row.RetryAt <= time.Now().UnixMilli() {
				ids = append(ids, id)
			}
		}
		d.mu.Unlock()
		jobs := make(chan string)
		var workers sync.WaitGroup
		for range 2 {
			workers.Go(func() {
				for id := range jobs {
					op, cancel := context.WithTimeout(ctx, 25*time.Second)
					d.finishFleet(op, id)
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
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
