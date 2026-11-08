package canary

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"
)

// Explicit, name-confirmed local detachment is distinct from verified remote
// retirement. Keep the old credentials and uncertain commands encrypted until
// best-effort revocation succeeds; never replay an abandoned monitor write.
func (d *Dashboard) queueNodeForget(r *http.Request, actor AccountView, row registeredNodeRecord, confirmation string) error {
	if confirmation != row.Label {
		return fail(409, "Enter the node's current dashboard name exactly to confirm removal")
	}
	if !row.ForceRemoving && row.State != "removing" {
		return fail(409, "Start node removal before choosing Remove now")
	}
	secret, err := d.openSecrets(row)
	if err != nil {
		return fail(503, "Saved node credentials are unavailable")
	}
	n := d.accounts.node
	n.mu.Lock()
	defer n.mu.Unlock()
	a, err := n.authenticate(r)
	if err != nil {
		return err
	}
	if a.ID != actor.ID || a.Role != "admin" || r.Context().Err() != nil {
		return fail(403, "Dashboard administrator permission is required")
	}
	existing, exists := d.forgottenNodes[row.ID]
	if exists {
		secret, err = d.openSecrets(existing)
		if err != nil {
			return err
		}
		row = existing
		secret.Removal.ActorID, secret.Removal.ActorVersion = a.ID, a.AuthVersion
	} else {
		secret.Removal = &dashboardNodeRemoval{ActorID: a.ID, ActorVersion: a.AuthVersion, Phase: "forget-queued"}
	}
	if !exists && len(d.forgottenNodes) >= 1024 {
		return fail(409, "Pending node access revocations have reached capacity")
	}
	row.Credentials, err = d.sealSecrets(row, secret)
	if err != nil {
		return err
	}
	if err = n.identityTransactionLocked(func(e dbExec) error { return put(e, "dashboardForgottenNodes", row.ID, row) }); err == nil {
		d.forgottenNodes[row.ID] = row
	}
	return err
}

// Detached registrations stay discoverable to administrators until revocation
// is confirmed. Return only public identity and whether authority needs renewal.
type pendingNodeRevocation struct {
	ID                    string `json:"id"`
	Label                 string `json:"label"`
	NodeID                string `json:"nodeId"`
	AuthorizationRequired bool   `json:"authorizationRequired"`
}

func (d *Dashboard) listNodeRevocations(w http.ResponseWriter) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []pendingNodeRevocation{}
	for id, row := range d.forgottenNodes {
		secret, err := d.openSecrets(row)
		if err != nil {
			errorReply(w, fail(503, "Pending node access revocations cannot be read"))
			return
		}
		if secret.Removal.Phase != "forget-revoke" {
			continue
		}
		out = append(out, pendingNodeRevocation{ID: id, Label: row.Label, NodeID: row.NodeID,
			AuthorizationRequired: !d.fleetAuthorized(dashboardFleetRecord{ActorID: secret.Removal.ActorID, ActorRole: "admin", ActorVersion: secret.Removal.ActorVersion})})
	}
	slices.SortFunc(out, func(a, b pendingNodeRevocation) int { return strings.Compare(a.ID, b.ID) })
	reply(w, 200, out)
}

func (d *Dashboard) loadForgottenNodes() error {
	n := d.accounts.node
	n.mu.Lock()
	defer n.mu.Unlock()
	rows, err := loadAll[registeredNodeRecord](n.store, "dashboardForgottenNodes")
	if err != nil || len(rows) > 1024 {
		return errors.New("Cannot load bounded pending node revocations")
	}
	for id, row := range rows {
		secret, err := d.openSecrets(row)
		if id != row.ID || err != nil || secret.Removal == nil || !slices.Contains([]string{"forget-queued", "forget-revoke"}, secret.Removal.Phase) {
			return errors.New("Invalid pending node revocation")
		}
	}
	d.forgottenNodes = rows
	return nil
}

// Caller holds d.mu. All changes are local and atomic. Busy workers get to
// finish their current request before their routing/command journal is changed.
func (d *Dashboard) detachForgottenNode(row registeredNodeRecord, secret dashboardNodeSecrets) error {
	if d.busy[row.ID] {
		return fail(409, "Waiting for the current node operation before local removal")
	}
	n := d.accounts.node
	n.mu.Lock()
	groups, err := loadAll[dashboardNodeGroup](n.store, "dashboardNodeGroups")
	n.mu.Unlock()
	if err != nil {
		return err
	}
	deletedGroups, changedGroups := []string{}, []string{}
	for id, group := range groups {
		if slices.Contains(group.RegistrationIDs, row.ID) {
			changedGroups = append(changedGroups, id)
			if len(group.RegistrationIDs) == 1 {
				deletedGroups = append(deletedGroups, id)
			}
		}
	}
	updates := map[string]dashboardFleetRecord{}
	for id, before := range d.fleet {
		plan, err := d.openFleet(before)
		if err != nil {
			return err
		}
		affected := removalAffects(before, plan, row.ID)
		for _, group := range before.GroupIDs {
			affected = affected || slices.Contains(changedGroups, group)
		}
		if !affected {
			continue
		}
		if d.fleetBusy[id] {
			return fail(409, "Waiting for the current monitor request before local removal")
		}
		secret.Removal.AbandonedFleets = append(secret.Removal.AbandonedFleets, copyFleet(before))
		fleet := copyFleet(before)
		placements, deferred := []dashboardFleetPlacement{}, []dashboardFleetPlacement{}
		resourceTargets := slices.Clone(plan.RemovalResourceTargets)
		if plan.RetargetAfterReplay {
			resourceTargets = slices.Clone(plan.ForgetResourceTargets)
		}
		for _, p := range plan.AfterForget {
			p.RegistrationIDs = slices.DeleteFunc(slices.Clone(p.RegistrationIDs), func(id string) bool { return id == row.ID })
			p.NodeIDs = slices.DeleteFunc(slices.Clone(p.NodeIDs), func(id string) bool { return id == row.NodeID })
			p.RetiringRegistrationIDs = slices.DeleteFunc(slices.Clone(p.RetiringRegistrationIDs), func(id string) bool { return id == row.ID })
			survivors := unique(append(slices.Clone(p.RegistrationIDs), p.RetiringRegistrationIDs...))
			if len(survivors) == 0 {
				continue
			}
			if p.RegistrationID == row.ID {
				p.RegistrationID = survivors[0]
			}
			deferred = append(deferred, p)
		}
		commands := map[string]fleetPlacementCommand{}
		unplaced := slices.Clone(fleet.RegistrationIDs)
		unplaced = slices.DeleteFunc(unplaced, func(id string) bool {
			if id == row.ID {
				return true
			}
			for _, p := range fleet.Placements {
				if p.State != "removed" && slices.Contains(p.RegistrationIDs, id) {
					return true
				}
			}
			return false
		})
		replay := false
		// Resolve references through the old command before stopping source sync.
		refs := map[string]fleetTargetReferences{}
		for _, p := range fleet.Placements {
			if p.State == "removed" {
				continue
			}
			body, err := fleetCommandBytes(plan, p)
			var command fleetPlacementCommand
			var input MonitorInput
			if err != nil || json.Unmarshal(body, &command) != nil {
				return fail(503, "Cannot read the saved monitor cleanup")
			}
			if !command.Deleted && json.Unmarshal(command.Monitor, &input) == nil {
				for _, member := range p.RegistrationIDs {
					if member != row.ID {
						channels, tags, group := slices.Clone(input.NotificationIDs), slices.Clone(input.Tags), input.GroupID
						refs[member] = fleetTargetReferences{NotificationIDs: &channels, Tags: &tags, GroupID: &group}
					}
				}
			}
			p.RegistrationIDs = slices.DeleteFunc(p.RegistrationIDs, func(id string) bool { return id == row.ID })
			p.RetiringRegistrationIDs = slices.DeleteFunc(p.RetiringRegistrationIDs, func(id string) bool { return id == row.ID })
			if p.RegistrationID == row.ID {
				delete(plan.Commands, p.ID)
				survivors := unique(append(slices.Clone(p.RegistrationIDs), p.RetiringRegistrationIDs...))
				if len(survivors) > 0 {
					p.ID, p.RegistrationID, p.Sequence, p.State = strings.ReplaceAll(newID(), "-", ""), survivors[0], 0, "pending"
					if plan.ForgetHandoffs == nil {
						plan.ForgetHandoffs = map[string]bool{}
					}
					plan.ForgetHandoffs[p.ID] = true
					p.NodeIDs = slices.DeleteFunc(p.NodeIDs, func(id string) bool { return id == row.NodeID })
					deferred = append(deferred, p)
				}
				continue
			}
			commands[p.ID] = command
			replay = replay || plan.Prepared && p.State != "applied" && p.State != "removed"
			placements = append(placements, p)
		}
		if plan.NotificationSourceRegistrationID == row.ID || plan.NativeResourceIDs[row.ID] != nil {
			if plan.References == nil {
				plan.References = map[string]fleetTargetReferences{}
			}
			for member, ref := range refs {
				plan.References[member] = ref
			}
			for _, member := range unplaced {
				input, err := fleetReferencedInput(plan, dashboardFleetPlacement{RegistrationID: member, RegistrationIDs: []string{member}})
				if err != nil {
					return err
				}
				channels, tags, group := slices.Clone(input.NotificationIDs), slices.Clone(input.Tags), input.GroupID
				plan.References[member] = fleetTargetReferences{NotificationIDs: &channels, Tags: &tags, GroupID: &group}
			}
			plan.SyncNotifications, plan.SyncNotificationsSet, plan.NotificationSourceRegistrationID = false, true, ""
			// Empty target sets must exclude retained placements too. The sentinel
			// never matches a registration; it keeps frozen settings out of writes.
			if len(unplaced) == 0 {
				unplaced = []string{""}
			}
			resourceTargets = unplaced
		}
		delete(plan.References, row.ID)
		fleet.RegistrationIDs = slices.DeleteFunc(fleet.RegistrationIDs, func(id string) bool { return id == row.ID })
		fleet.PinnedRegistrationIDs = slices.DeleteFunc(fleet.PinnedRegistrationIDs, func(id string) bool { return id == row.ID })
		fleet.GroupIDs = slices.DeleteFunc(fleet.GroupIDs, func(id string) bool { return slices.Contains(deletedGroups, id) })
		if fleet.Source != nil && fleet.Source.RegistrationID == row.ID {
			fleet.Source, fleet.SourceCompleted = nil, true
		}
		plan.ForgottenNodeIDs = unique(append(plan.ForgottenNodeIDs, row.NodeID))
		plan.RetargetAfterReplay, plan.AfterForget = replay, deferred
		if replay {
			plan.Commands = commands
			plan.ForgetResourceTargets = resourceTargets
		} else {
			plan.RemovalResourceTargets, plan.ForgetResourceTargets = resourceTargets, nil
		}
		fleet.Placements = placements
		if !replay {
			fleet.Placements = append(fleet.Placements, deferred...)
			plan.Prepared, plan.Commands, plan.AfterForget = false, nil, nil
		}
		plan.Deleted = plan.Deleted || len(fleet.RegistrationIDs) == 0
		fleet.ActorID, fleet.ActorRole, fleet.ActorVersion = secret.Removal.ActorID, "admin", secret.Removal.ActorVersion
		fleet.Revision, fleet.UpdatedAt, fleet.RetryAt, fleet.State, fleet.Reason = newID(), time.Now().UnixMilli(), 0, "pending", ""
		if plan.Deleted && len(fleet.Placements) == 0 {
			fleet.State, plan.Prepared = "removed", true
		}
		fleet.Private, err = d.sealFleet(fleet, plan)
		if err != nil {
			return err
		}
		updates[id] = fleet
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	a, ok := n.accounts[secret.Removal.ActorID]
	if !ok || a.Role != "admin" || a.AuthVersion != secret.Removal.ActorVersion {
		return fail(403, "An administrator must authorize local node removal")
	}
	secret.Removal.Phase = "forget-revoke"
	row.Credentials, err = d.sealSecrets(row, secret)
	if err != nil {
		return err
	}
	err = n.identityTransactionLocked(func(e dbExec) error {
		for id, group := range groups {
			if !slices.Contains(group.RegistrationIDs, row.ID) {
				continue
			}
			if err := d.verifyNodeGroup(group); err != nil {
				return err
			}
			group.RegistrationIDs = slices.DeleteFunc(slices.Clone(group.RegistrationIDs), func(id string) bool { return id == row.ID })
			if err := remove(e, "dashboardGroupSyncs", id); err != nil {
				return err
			}
			if len(group.RegistrationIDs) == 0 {
				if err := remove(e, "dashboardNodeGroups", id); err != nil {
					return err
				}
			} else {
				group.Revision, group.UpdatedAt, group.SyncState, group.SyncReason = newID(), time.Now().UnixMilli(), "applied", ""
				if err := d.signNodeGroup(&group); err != nil {
					return err
				}
				if err := put(e, "dashboardNodeGroups", id, group); err != nil {
					return err
				}
			}
		}
		for id, fleet := range updates {
			if err := put(e, "dashboardFleet", id, fleet); err != nil {
				return err
			}
		}
		if err := put(e, "dashboardForgottenNodes", row.ID, row); err != nil {
			return err
		}
		return remove(e, "dashboardRegistry", row.ID)
	})
	if err == nil {
		for id, fleet := range updates {
			d.fleet[id] = fleet
		}
		d.forgottenNodes[row.ID] = row
		delete(d.registry, row.ID)
		delete(d.nodeCallSlots, row.ID)
	}
	return err
}

// A confirmed replay or explicitly reviewed reconciliation can now replace the
// saved survivor commands. Carry deferred HA placements into ordinary planning.
func resumeForgetRetarget(row *dashboardFleetRecord, plan *dashboardFleetPlan) {
	if !plan.RetargetAfterReplay {
		return
	}
	row.Placements = append(row.Placements, plan.AfterForget...)
	plan.RetargetAfterReplay, plan.AfterForget, plan.Prepared, plan.Commands = false, nil, false, nil
	plan.RemovalResourceTargets, plan.ForgetResourceTargets = plan.ForgetResourceTargets, nil
}

func (d *Dashboard) finishForgottenNode(ctx context.Context, id string) error {
	d.mu.Lock()
	row, ok := d.forgottenNodes[id]
	if !ok {
		d.mu.Unlock()
		return nil
	}
	secret, err := d.openSecrets(row)
	if err != nil {
		d.mu.Unlock()
		return err
	}
	if secret.Removal.Phase == "forget-queued" {
		err = d.detachForgottenNode(row, secret)
		d.mu.Unlock()
		return err
	}
	d.mu.Unlock()
	if !d.fleetAuthorized(dashboardFleetRecord{ActorID: secret.Removal.ActorID, ActorRole: "admin", ActorVersion: secret.Removal.ActorVersion}) {
		return fail(403, "Node access revocation needs administrator authorization")
	}
	body, _ := json.Marshal(secret.dashboardRoleTokens)
	response, _, err := d.nodeRequest(ctx, row, secret.Admin, "DELETE", "/api/dashboard-enrollments/"+id, body)
	if err == nil && response.StatusCode == 401 && secret.EnrollmentToken != "" {
		response, _, err = d.nodeRequest(ctx, row, secret.EnrollmentToken, "DELETE", "/api/dashboard-enrollments/"+id, body)
	}
	if err != nil || response.StatusCode != 204 {
		return fail(502, "Remote access revocation is pending")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.accounts.node
	n.mu.Lock()
	err = n.identityTransactionLocked(func(e dbExec) error { return remove(e, "dashboardForgottenNodes", id) })
	n.mu.Unlock()
	if err == nil {
		delete(d.forgottenNodes, id)
		delete(d.nodeCallSlots, id)
		d.connections.Disconnect(id)
	}
	return err
}

func (d *Dashboard) runForgottenNodes(ctx context.Context) {
	next := 0
	for {
		d.mu.Lock()
		ids := make([]string, 0, len(d.forgottenNodes))
		for id := range d.forgottenNodes {
			ids = append(ids, id)
		}
		d.mu.Unlock()
		revocations := []string{}
		for _, id := range ids {
			d.mu.Lock()
			secret, err := d.openSecrets(d.forgottenNodes[id])
			d.mu.Unlock()
			if err != nil {
				continue
			}
			if secret.Removal.Phase != "forget-queued" {
				revocations = append(revocations, id)
				continue
			}
			_ = d.finishForgottenNode(ctx, id)
		}
		slices.Sort(revocations)
		// Dead nodes must not hold up new local detach requests indefinitely.
		for range min(2, len(revocations)) {
			id := revocations[next%len(revocations)]
			next++
			op, cancel := context.WithTimeout(ctx, 10*time.Second)
			_ = d.finishForgottenNode(op, id)
			cancel()
			if ctx.Err() != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}
