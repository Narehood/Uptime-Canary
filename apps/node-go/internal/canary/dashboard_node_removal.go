package canary

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// Kept inside the encrypted role bundle. Acceptance is durable before any
// cleanup; neither a closed browser nor a dashboard restart cancels the work.
type dashboardNodeRemoval struct {
	ActorID         string                 `json:"actorId"`
	ActorVersion    uint64                 `json:"actorVersion"`
	Phase           string                 `json:"phase"`
	Deployments     []string               `json:"deployments,omitempty"`
	AbandonedFleets []dashboardFleetRecord `json:"abandonedFleets,omitempty"`
}

func dashboardNodePending(row registeredNodeRecord) bool {
	return row.ForceRemoving || dashboardOperationPending(row.State)
}

func removalAffects(row dashboardFleetRecord, plan dashboardFleetPlan, id string) bool {
	if row.State == "removed" {
		return false
	}
	if slices.Contains(row.RegistrationIDs, id) || slices.Contains(row.PinnedRegistrationIDs, id) ||
		row.Source != nil && row.Source.RegistrationID == id || plan.NotificationSourceRegistrationID == id || plan.NativeResourceIDs[id] != nil {
		return true
	}
	for _, p := range row.Placements {
		if p.State != "removed" && (p.RegistrationID == id || slices.Contains(p.RegistrationIDs, id) || slices.Contains(p.RetiringRegistrationIDs, id)) {
			return true
		}
	}
	return false
}

// Caller holds d.mu. Existing workers may finish exact saved commands; new
// browser edits cannot reintroduce a dependency while its node is retiring.
func (d *Dashboard) fleetRemovalLocked(row dashboardFleetRecord) bool {
	for id, node := range d.registry {
		if !node.ForceRemoving {
			continue
		}
		plan, err := d.openFleet(row)
		if err != nil || removalAffects(row, plan, id) {
			return true
		}
		secret, err := d.openSecrets(node)
		if err != nil || secret.Removal == nil || slices.Contains(secret.Removal.Deployments, row.ID) {
			return true
		}
	}
	return false
}

// Caller holds d.mu. The label comparison and administrator reauthorization
// share the same critical section as the durable acceptance.
func (d *Dashboard) queueNodeRemoval(r *http.Request, actor AccountView, row registeredNodeRecord, confirmation string) (registeredNodeRecord, error) {
	if confirmation != row.Label {
		return row, fail(409, "Enter the node's current dashboard name exactly to confirm removal")
	}
	if row.ForceRemoving {
		return row, nil // A lost acceptance response never queues a second cleanup.
	}
	secret, err := d.openSecrets(row)
	if err != nil {
		return row, fail(503, "Saved node credentials are unavailable")
	}
	n := d.accounts.node
	n.mu.Lock()
	defer n.mu.Unlock()
	a, err := n.authenticate(r)
	if err != nil {
		return row, err
	}
	if a.ID != actor.ID || a.Role != "admin" || r.Context().Err() != nil {
		return row, fail(403, "Dashboard administrator permission is required")
	}
	// Cancellation need not provision or finish rotating unused role accounts.
	// Existing placement commands, however, must first resolve their saved intent.
	if row.State == "enrolling" || row.State == "rotating" {
		used := false
		for _, fleet := range d.fleet {
			plan, err := d.openFleet(fleet)
			if err != nil {
				return row, err
			}
			used = used || removalAffects(fleet, plan, row.ID)
		}
		if !used {
			row.State = "starting"
		}
	}
	secret.Removal = &dashboardNodeRemoval{ActorID: a.ID, ActorVersion: a.AuthVersion, Phase: "queued"}
	row.ForceRemoving, row.Error = true, "Removing node-group memberships and monitor assignments in the background"
	row.Credentials, err = d.sealSecrets(row, secret)
	if err == nil {
		err = n.identityTransactionLocked(func(e dbExec) error { return put(e, "dashboardRegistry", row.ID, row) })
	}
	if err == nil {
		d.registry[row.ID] = row
	}
	return row, err
}

// Persist the encrypted job and every changed definition in one transaction.
// Caller holds d.mu. No remote requests are made while either lock is held.
func (d *Dashboard) commitNodeCleanup(row registeredNodeRecord, secret dashboardNodeSecrets, rows map[string]dashboardFleetRecord, groups map[string]dashboardNodeGroup, deletedGroups []string, request ...*http.Request) error {
	n := d.accounts.node
	n.mu.Lock()
	defer n.mu.Unlock()
	job := secret.Removal
	if len(request) > 0 {
		current, err := n.authenticate(request[0])
		if err != nil {
			return err
		}
		if current.ID != job.ActorID || current.Role != "admin" || request[0].Context().Err() != nil {
			return fail(403, "Dashboard administrator permission is required")
		}
	}
	a, ok := n.accounts[job.ActorID]
	if !ok || a.Role != "admin" || a.AuthVersion != job.ActorVersion || n.stopped || n.fault != nil {
		return fail(403, "An administrator must retry node removal because its authorizing account changed")
	}
	var err error
	row.Credentials, err = d.sealSecrets(row, secret)
	if err != nil {
		return err
	}
	err = n.identityTransactionLocked(func(e dbExec) error {
		for id, fleet := range rows {
			if err := put(e, "dashboardFleet", id, fleet); err != nil {
				return err
			}
		}
		for id, group := range groups {
			if group.ID != id {
				d.mu.Unlock()
				return fail(503, "Invalid saved node-group identity")
			}
			if err := put(e, "dashboardNodeGroups", id, group); err != nil {
				return err
			}
			if err := remove(e, "dashboardGroupSyncs", id); err != nil {
				return err
			}
		}
		for _, id := range deletedGroups {
			if err := remove(e, "dashboardGroupSyncs", id); err != nil {
				return err
			}
			if err := remove(e, "dashboardNodeGroups", id); err != nil {
				return err
			}
		}
		return put(e, "dashboardRegistry", row.ID, row)
	})
	if err == nil {
		for id, fleet := range rows {
			d.fleet[id] = copyFleet(fleet)
		}
		d.registry[row.ID] = row
	}
	return err
}

func (d *Dashboard) removalViews(ctx context.Context, rows map[string]dashboardFleetRecord, id string) (map[string]Overview, error) {
	ids := []string{}
	for _, row := range rows {
		plan, err := d.openFleet(row)
		if err != nil {
			return nil, err
		}
		if plan.NotificationSourceRegistrationID != id && plan.NativeResourceIDs[id] == nil {
			continue
		}
		for _, p := range row.Placements {
			if p.State == "removed" {
				continue
			}
			for _, member := range p.RegistrationIDs {
				if member != id {
					ids = append(ids, member)
				}
			}
		}
	}
	views := map[string]Overview{}
	var mu sync.Mutex
	var first error
	jobs := make(chan string)
	var workers sync.WaitGroup
	for range min(4, len(unique(ids))) {
		workers.Go(func() {
			for member := range jobs {
				view, _, err := d.fleetRead(ctx, member, "admin")
				mu.Lock()
				if err != nil && first == nil {
					first = err
				}
				if err == nil {
					views[member] = view
				}
				mu.Unlock()
			}
		})
	}
	for _, member := range unique(ids) {
		select {
		case jobs <- member:
		case <-ctx.Done():
		}
	}
	close(jobs)
	workers.Wait()
	if first != nil {
		return nil, first
	}
	return views, ctx.Err()
}

// Preserve each survivor's actual delivery/tag/group references when its source
// is being removed. Copied settings remain, but no longer follow a retired source.
func freezeRemovalReferences(row dashboardFleetRecord, plan *dashboardFleetPlan, id string, views map[string]Overview) error {
	if plan.NotificationSourceRegistrationID != id && plan.NativeResourceIDs[id] == nil {
		return nil
	}
	if plan.References == nil {
		plan.References = map[string]fleetTargetReferences{}
	}
	for _, p := range row.Placements {
		if p.State == "removed" {
			continue
		}
		for _, member := range p.RegistrationIDs {
			if member == id {
				continue
			}
			var found *Monitor
			for _, view := range views[member].Monitors {
				if view.Monitor.ID == p.MonitorID && !view.ConfigConflict && view.Monitor.Revision == p.Revision {
					copy := view.Monitor
					found = &copy
					break
				}
			}
			if found == nil {
				return fail(409, "Waiting for a surviving monitor's confirmed settings before removing its source")
			}
			channels, tags, group := append([]string{}, found.NotificationIDs...), append([]MonitorTag{}, found.Tags...), found.GroupID
			plan.References[member] = fleetTargetReferences{NotificationIDs: &channels, Tags: &tags, GroupID: &group}
		}
	}
	plan.SyncNotifications, plan.SyncNotificationsSet = false, true
	plan.NotificationSourceRegistrationID = ""
	plan.Resources, plan.ResourceIDs, plan.NativeResourceIDs = nil, nil, nil
	return nil
}

// A command marked Unsent has not crossed the durable pre-send boundary.
// Only a wholly unstarted last-runner definition can be cancelled this way.
func removalCanCancelUnsent(row dashboardFleetRecord, plan dashboardFleetPlan, id string) bool {
	if len(row.RegistrationIDs) != 1 || row.RegistrationIDs[0] != id {
		return false
	}
	for _, p := range row.Placements {
		if p.Sequence != 0 || !plan.Unsent[p.ID] {
			return false
		}
	}
	return true
}

// Capture settings for unstarted survivor placements before losing their source.
// The snapshot is committed with cleanup, then ordinary placement workers copy
// it to the remaining nodes. No remote writes happen before that journal commit.
func (d *Dashboard) snapshotUnstartedRemovalSettings(ctx context.Context, rows map[string]dashboardFleetRecord, id string) (map[string]dashboardFleetPlan, error) {
	frozen := map[string]dashboardFleetPlan{}
	for key, row := range rows {
		plan, err := d.openFleet(row)
		if err != nil {
			return nil, err
		}
		if !plan.SyncNotifications || plan.Deleted || plan.NotificationSourceRegistrationID != id && plan.NativeResourceIDs[id] == nil {
			continue
		}
		unplaced := slices.Clone(row.RegistrationIDs)
		unplaced = slices.DeleteFunc(unplaced, func(member string) bool {
			if member == id {
				return true
			}
			for _, p := range row.Placements {
				if p.State != "removed" && slices.Contains(p.RegistrationIDs, member) {
					return true
				}
			}
			return false
		})
		if len(unplaced) == 0 {
			continue
		}
		// Removal is authorized by an administrator, regardless of which role
		// originally queued the unstarted deployment.
		reader := row
		reader.ActorRole = "admin"
		if err = d.prepareFleetSettings(ctx, reader, &plan); err != nil {
			return nil, err
		}
		if plan.References == nil {
			plan.References = map[string]fleetTargetReferences{}
		}
		for _, member := range unplaced {
			input, err := fleetReferencedInput(plan, dashboardFleetPlacement{RegistrationID: member, RegistrationIDs: []string{member}})
			if err != nil {
				return nil, err
			}
			channels, tags, group := slices.Clone(input.NotificationIDs), slices.Clone(input.Tags), input.GroupID
			plan.References[member] = fleetTargetReferences{NotificationIDs: &channels, Tags: &tags, GroupID: &group}
		}
		plan.RemovalResourceTargets = slices.Clone(unplaced)
		frozen[key] = plan
	}
	return frozen, nil
}

func (d *Dashboard) prepareNodeCleanup(ctx context.Context, row registeredNodeRecord, secret dashboardNodeSecrets) error {
	d.mu.Lock()
	rows := map[string]dashboardFleetRecord{}
	n := d.accounts.node
	n.mu.Lock()
	groups, err := loadAll[dashboardNodeGroup](n.store, "dashboardNodeGroups")
	n.mu.Unlock()
	if err != nil {
		d.mu.Unlock()
		return err
	}
	changedGroups := map[string]dashboardNodeGroup{}
	deletedGroups := []string{}
	for id, group := range groups {
		if err = d.verifyNodeGroup(group); err != nil {
			d.mu.Unlock()
			return err
		}
		if !slices.Contains(group.RegistrationIDs, row.ID) {
			continue
		}
		group.RegistrationIDs = slices.DeleteFunc(slices.Clone(group.RegistrationIDs), func(id string) bool { return id == row.ID })
		if len(group.RegistrationIDs) == 0 {
			deletedGroups = append(deletedGroups, id)
			continue
		}
		group.Revision, group.UpdatedAt, group.SyncState, group.SyncReason = newID(), time.Now().UnixMilli(), "applied", ""
		if err = d.signNodeGroup(&group); err != nil {
			d.mu.Unlock()
			return err
		}
		changedGroups[id] = group
	}
	for id, fleet := range d.fleet {
		plan, err := d.openFleet(fleet)
		if err != nil {
			d.mu.Unlock()
			return err
		}
		affected := removalAffects(fleet, plan, row.ID)
		for _, group := range fleet.GroupIDs {
			affected = affected || slices.Contains(deletedGroups, group)
		}
		if !affected || fleet.State == "removed" {
			continue
		}
		if d.fleetBusy[id] {
			d.mu.Unlock()
			return fail(409, "Waiting for the running monitor operation to finish before removing its node")
		}
		// A failed preflight or an operation whose writes are all acknowledged
		// need not become applied before removal can replace its desired targets.
		// Unknown commands, however, retain their exact payload and sequence.
		if !fleetWritesConfirmed(fleet, plan) && !removalCanCancelUnsent(fleet, plan, row.ID) {
			if fleet.State == "blocked" || fleet.State == "unauthorized" || fleet.ActorID != secret.Removal.ActorID || fleet.ActorRole != "admin" || fleet.ActorVersion != secret.Removal.ActorVersion || !d.fleetAuthorized(fleet) {
				fleet = copyFleet(fleet)
				fleet.ActorID, fleet.ActorRole, fleet.ActorVersion = secret.Removal.ActorID, "admin", secret.Removal.ActorVersion
				fleet.Revision, fleet.State, fleet.Reason, fleet.RetryAt = newID(), "pending", "Resolving the saved monitor command before node cleanup", 0
				fleet.Private, err = d.sealFleet(fleet, plan)
				if err == nil {
					err = d.commitNodeCleanup(row, secret, map[string]dashboardFleetRecord{id: fleet}, nil, nil)
				}
				if err != nil {
					d.mu.Unlock()
					return err
				}
			}
			d.mu.Unlock()
			return fail(409, "Waiting for the saved command for monitor "+plan.Monitor.Name+" to be confirmed before removing its node")
		}
		rows[id] = copyFleet(fleet)
	}
	d.mu.Unlock()
	frozen, err := d.snapshotUnstartedRemovalSettings(ctx, rows, row.ID)
	if err != nil {
		return err
	}
	views, err := d.removalViews(ctx, rows, row.ID)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	n.mu.Lock()
	currentGroups, groupErr := loadAll[dashboardNodeGroup](n.store, "dashboardNodeGroups")
	n.mu.Unlock()
	if groupErr != nil {
		return groupErr
	}
	for id := range groups {
		if _, changed := changedGroups[id]; !changed && !slices.Contains(deletedGroups, id) {
			continue
		}
		current, exists := currentGroups[id]
		if !exists || current.Revision != groups[id].Revision || d.verifyNodeGroup(current) != nil {
			return fail(409, "Node-group membership changed; cleanup will retry")
		}
	}
	for id, fleet := range rows {
		if d.fleetBusy[id] || !equalJSON(d.fleet[id], fleet) {
			return fail(409, "Monitor settings changed; cleanup will retry")
		}
		plan, err := d.openFleet(fleet)
		if err != nil {
			return err
		}
		if snapshot, ok := frozen[id]; ok {
			plan = snapshot
		}
		resources := plan.Resources
		if err = freezeRemovalReferences(fleet, &plan, row.ID, views); err != nil {
			return err
		}
		if _, ok := frozen[id]; ok {
			plan.Resources = resources
		}
		// Merely selecting a node in an unstarted deployment creates no remote
		// assignment. Do not make its removal wait on work only for other nodes.
		needsConfirmation := false
		for _, p := range fleet.Placements {
			if p.State != "removed" && (p.RegistrationID == row.ID || slices.Contains(p.RegistrationIDs, row.ID) || slices.Contains(p.RetiringRegistrationIDs, row.ID)) {
				needsConfirmation = true
			}
		}
		fleet.RegistrationIDs = slices.DeleteFunc(fleet.RegistrationIDs, func(id string) bool { return id == row.ID })
		fleet.PinnedRegistrationIDs = slices.DeleteFunc(fleet.PinnedRegistrationIDs, func(id string) bool { return id == row.ID })
		fleet.GroupIDs = slices.DeleteFunc(fleet.GroupIDs, func(id string) bool { return slices.Contains(deletedGroups, id) })
		if fleet.Source == nil || fleet.Source.RegistrationID == row.ID {
			fleet.SourceCompleted, fleet.Source = true, nil
		}
		delete(plan.References, row.ID)
		plan.Deleted = plan.Deleted || len(fleet.RegistrationIDs) == 0
		plan.Prepared, plan.Commands, plan.ObservedRevisions = false, nil, nil
		fleet.ActorID, fleet.ActorRole, fleet.ActorVersion = secret.Removal.ActorID, "admin", secret.Removal.ActorVersion
		fleet.Revision, fleet.UpdatedAt, fleet.State, fleet.Reason, fleet.RetryAt = newID(), time.Now().UnixMilli(), "pending", "", 0
		if plan.Deleted && fleetCanCancel(fleet, plan) {
			fleet.State, plan.Prepared = "removed", true
			fleet.Placements, needsConfirmation = []dashboardFleetPlacement{}, false
		}
		fleet.Private, err = d.sealFleet(fleet, plan)
		if err != nil {
			return err
		}
		rows[id] = fleet
		if needsConfirmation {
			secret.Removal.Deployments = append(secret.Removal.Deployments, id)
		}
	}
	secret.Removal.Phase = "draining"
	row.Error = "Waiting for monitor assignments to be retired or confirmed on their remaining nodes"
	return d.commitNodeCleanup(row, secret, rows, changedGroups, deletedGroups)
}

// Return ready only after retirement, explicit anchor handoff, and all survivor
// acknowledgements. An offline runner remains visible as removal unfinished.
func (d *Dashboard) finishNodeCleanup(ctx context.Context, row registeredNodeRecord, secret dashboardNodeSecrets) (bool, error) {
	job := secret.Removal
	if job == nil {
		return false, fail(503, "Saved node cleanup is unavailable")
	}
	if !d.fleetAuthorized(dashboardFleetRecord{ActorID: job.ActorID, ActorRole: "admin", ActorVersion: job.ActorVersion}) {
		return false, fail(403, "An administrator must retry node removal because its authorizing account changed")
	}
	if job.Phase == "queued" {
		return false, d.prepareNodeCleanup(ctx, row, secret)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	updates := map[string]dashboardFleetRecord{}
	for _, id := range job.Deployments {
		fleet, ok := d.fleet[id]
		if !ok {
			return false, fail(503, "A saved cleanup deployment is unavailable")
		}
		if d.fleetBusy[id] || fleet.State != "applied" && fleet.State != "removed" {
			return false, fail(409, "Waiting for monitor cleanup: "+fleet.Reason)
		}
		if job.Phase != "draining" || fleet.State == "removed" {
			continue
		}
		fleet = copyFleet(fleet)
		plan, err := d.openFleet(fleet)
		if err != nil {
			return false, err
		}
		changed := false
		for i := range fleet.Placements {
			old := &fleet.Placements[i]
			if old.State == "removed" || old.RegistrationID != row.ID {
				continue
			}
			if len(old.RegistrationIDs) == 0 {
				return false, fail(409, "Waiting for monitor retirement")
			}
			// The old anchor first confirmed the reduced runner set. A new
			// local receipt then adopts that same physical ID and history on a
			// surviving peer; an ordinary partition never triggers this move.
			next := *old
			next.ID = strings.ReplaceAll(newID(), "-", "")
			next.RegistrationID, next.Sequence, next.State = next.RegistrationIDs[0], 0, "pending"
			next.RetiringRegistrationIDs = nil
			old.State = "removed"
			fleet.Placements = append(fleet.Placements, next)
			changed = true
		}
		if changed {
			plan.Prepared, plan.Commands, plan.ObservedRevisions = false, nil, nil
			fleet.Revision, fleet.UpdatedAt, fleet.State, fleet.Reason, fleet.RetryAt = newID(), time.Now().UnixMilli(), "pending", "", 0
			fleet.Private, err = d.sealFleet(fleet, plan)
			if err != nil {
				return false, err
			}
			updates[id] = fleet
		}
	}
	if job.Phase == "draining" {
		job.Phase = "handoff"
		row.Error = "Confirming monitor management on the remaining nodes before revoking access"
		return false, d.commitNodeCleanup(row, secret, updates, nil, nil)
	}
	if job.Phase == "handoff" {
		for _, id := range job.Deployments {
			fleet := d.fleet[id]
			if fleet.State == "removed" {
				continue
			}
			plan, err := d.openFleet(fleet)
			if err != nil {
				return false, err
			}
			if removalAffects(fleet, plan, row.ID) {
				return false, fail(409, "Waiting for all active node references to be removed")
			}
		}
	}
	if job.Phase != "handoff" {
		return false, fail(503, "Invalid saved node cleanup phase")
	}
	return true, nil
}

// An explicit administrator retry renews authority without replacing unknown
// commands. Their exact payloads and placement sequences remain unchanged.
func (d *Dashboard) renewNodeRemoval(r *http.Request, actor AccountView, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	row, ok := d.registry[id]
	if !ok || !row.ForceRemoving {
		return nil
	}
	if d.busy[id] {
		return fail(409, "Node cleanup is already running")
	}
	secret, err := d.openSecrets(row)
	if err != nil || secret.Removal == nil {
		return fail(503, "Saved node cleanup is unavailable")
	}
	n := d.accounts.node
	n.mu.Lock()
	a, err := n.authenticate(r)
	n.mu.Unlock()
	if err != nil {
		return err
	}
	if a.ID != actor.ID || a.Role != "admin" {
		return fail(403, "Dashboard administrator permission is required")
	}
	secret.Removal.ActorID, secret.Removal.ActorVersion = a.ID, a.AuthVersion
	updates := map[string]dashboardFleetRecord{}
	for key, fleet := range d.fleet {
		plan, err := d.openFleet(fleet)
		if err != nil {
			return err
		}
		if !removalAffects(fleet, plan, id) && !slices.Contains(secret.Removal.Deployments, key) {
			continue
		}
		if d.fleetBusy[key] {
			return fail(409, "Wait for the current monitor operation before retrying removal")
		}
		if fleet.State == "removed" {
			continue
		}
		fleet = copyFleet(fleet)
		fleet.ActorID, fleet.ActorRole, fleet.ActorVersion = a.ID, "admin", a.AuthVersion
		fleet.Revision, fleet.RetryAt = newID(), 0
		if fleet.State == "blocked" || fleet.State == "unauthorized" {
			fleet.State, fleet.Reason = "pending", ""
		}
		fleet.Private, err = d.sealFleet(fleet, plan)
		if err != nil {
			return err
		}
		updates[key] = fleet
	}
	return d.commitNodeCleanup(row, secret, updates, nil, nil, r)
}
