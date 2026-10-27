package canary

import (
	"context"
	"slices"
	"time"
)

type dashboardGroupSync struct {
	Proof        string `json:"proof,omitempty"`
	GroupID      string `json:"groupId"`
	Revision     string `json:"revision"`
	ActorID      string `json:"actorId"`
	ActorRole    string `json:"actorRole"`
	ActorVersion uint64 `json:"actorVersion"`
	RetryAt      int64  `json:"retryAt"`
}

func (d *Dashboard) runGroupMaintenance(ctx context.Context) {
	for ctx.Err() == nil {
		d.runGroupSyncs(ctx)
		d.refreshGroupPlacements(ctx)
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Caller holds d.mu, but never the accounts lock.
func (d *Dashboard) groupTargetsLocked(ids []string, pins ...string) ([]string, error) {
	n := d.accounts.node
	n.mu.Lock()
	defer n.mu.Unlock()
	groups, err := loadAll[dashboardNodeGroup](n.store, "dashboardNodeGroups")
	if err != nil {
		return nil, fail(503, "Node groups unavailable")
	}
	for id, group := range groups {
		if group.ID != id {
			return nil, fail(503, "Invalid saved node-group identity")
		}
		if err := d.verifyNodeGroup(group); err != nil {
			return nil, err
		}
	}
	targets, err := groupTargets(groups, ids)
	if err != nil {
		return nil, err
	}
	targets = unique(append(targets, pins...))
	slices.Sort(targets)
	if len(targets) > 64 {
		return nil, fail(400, "Groups and original runners can include at most 64 nodes")
	}
	return targets, nil
}

// Caller holds d.mu. Preserve the real original assignment, never inferred
// names or the draft's replacement nodeIds.
func (d *Dashboard) originalFleetRegistrations(nodeIDs []string) ([]string, error) {
	ids := []string{}
	for _, nodeID := range unique(nodeIDs) {
		registration := ""
		for id, node := range d.registry {
			if node.NodeID == nodeID {
				registration = id
				break
			}
		}
		if registration == "" {
			return nil, fail(409, "Register every original runner before adding this monitor to a node group")
		}
		ids = append(ids, registration)
	}
	return ids, nil
}
func groupTargets(groups map[string]dashboardNodeGroup, ids []string) ([]string, error) {
	targets := []string{}
	for _, id := range ids {
		group, ok := groups[id]
		if !ok {
			return nil, fail(409, "A selected node group no longer exists; review this monitor")
		}
		targets = append(targets, group.RegistrationIDs...)
	}
	targets = unique(targets)
	slices.Sort(targets)
	if len(targets) < 1 || len(targets) > 64 {
		return nil, fail(400, "Choose groups containing one to 64 nodes")
	}
	return targets, nil
}

func (d *Dashboard) runGroupSyncs(ctx context.Context) {
	d.mu.Lock()
	n := d.accounts.node
	n.mu.Lock()
	jobs, err := loadAll[dashboardGroupSync](n.store, "dashboardGroupSyncs")
	n.mu.Unlock()
	d.mu.Unlock()
	if err != nil {
		return
	}
	for id, job := range jobs {
		if job.GroupID != id {
			continue
		}
		if d.verifyGroupJob(job) != nil {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if job.RetryAt > time.Now().UnixMilli() {
			continue
		}
		op, cancel := context.WithTimeout(ctx, 25*time.Second)
		d.syncNodeGroup(op, id, job)
		cancel()
	}
}

func (d *Dashboard) syncNodeGroup(ctx context.Context, id string, job dashboardGroupSync) {
	d.mu.Lock()
	n := d.accounts.node
	n.mu.Lock()
	group, exists, err := loadOne[dashboardNodeGroup](n.store, "dashboardNodeGroups", id)
	n.mu.Unlock()
	d.mu.Unlock()
	if err != nil || !exists || group.ID != id || job.GroupID != id || group.Revision != job.Revision || d.verifyNodeGroup(group) != nil || d.verifyGroupJob(job) != nil {
		return
	}
	authority := dashboardFleetRecord{ActorID: job.ActorID, ActorRole: job.ActorRole, ActorVersion: job.ActorVersion}
	if !d.fleetAuthorized(authority) {
		d.finishGroupSync(group, job, "blocked", "The authorizing account changed; sync again with an editor account")
		return
	}
	views := map[string]Overview{}
	ids := slices.Clone(group.RegistrationIDs)
	slices.Sort(ids)
	for _, registration := range ids {
		view, _, err := d.fleetRead(ctx, registration, job.ActorRole)
		if err != nil {
			d.finishGroupSync(group, job, "pending", "Waiting for all group members to answer before collecting their monitors")
			return
		}
		for _, monitor := range view.Monitors {
			if monitor.ConfigConflict {
				d.finishGroupSync(group, job, "blocked", "Resolve conflicting monitor settings on a group member, then sync again")
				return
			}
		}
		views[registration] = view
	}
	// Known HA lineage deduplicates a shared monitor. Unrelated equal IDs and
	// equal names are deliberately retained as distinct monitors.
	seen := map[string][]string{}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.fleetAuthorized(authority) {
		return
	}
	n.mu.Lock()
	current, ok, e := loadOne[dashboardNodeGroup](n.store, "dashboardNodeGroups", id)
	n.mu.Unlock()
	if e != nil || !ok || current.ID != id || current.Revision != group.Revision || d.verifyNodeGroup(current) != nil {
		return
	}
	if d.groupWaitsForLegacyCreate(id) {
		d.finishGroupSyncLocked(group, job, "pending", "Waiting to confirm an older node's saved monitor creation before collecting more monitors")
		return
	}
	for _, registration := range ids {
		for _, item := range views[registration].Monitors {
			monitor := item.Monitor
			if monitor.Deleted {
				continue
			}
			shared := false
			for _, other := range seen[monitor.ID] {
				if fleetKnownPeers(views[registration], views[other]) {
					shared = true
					break
				}
			}
			if shared {
				continue
			}
			seen[monitor.ID] = append(seen[monitor.ID], registration)
			row, managed := d.fleetForSourceLocked(registration, monitor.ID)
			if managed && slices.Contains(row.GroupIDs, id) {
				continue
			}
			var plan dashboardFleetPlan
			if managed {
				plan, err = d.openFleet(row)
				if err != nil {
					return
				}
				legacyUnprepared := !plan.Prepared && !plan.Deleted && !plan.SyncNotificationsSet
				if d.fleetBusy[row.ID] || !fleetWritesConfirmed(row, plan) || row.State != "applied" && !legacyUnprepared {
					d.finishGroupSyncLocked(group, job, "pending", "Waiting for an existing monitor placement to finish before syncing it")
					return
				}
				row = copyFleet(row)
			} else {
				if len(d.fleet) >= maxMonitors {
					d.finishGroupSyncLocked(group, job, "blocked", "Fleet monitor capacity exceeded")
					return
				}
				definition := digest("group-monitor\x00" + views[registration].Node.ID + "\x00" + monitor.ID)[:32]
				if _, used := d.fleet[definition]; used {
					d.finishGroupSyncLocked(group, job, "blocked", "A previously retired monitor needs explicit review before syncing again")
					return
				}
				row = dashboardFleetRecord{ID: definition, Placements: []dashboardFleetPlacement{}, Source: &fleetSourceReference{RegistrationID: registration, MonitorID: monitor.ID, Revision: monitor.Revision}}
				row.RegistrationIDs, err = d.originalFleetRegistrations(monitor.NodeIDs)
				if err != nil {
					d.finishGroupSyncLocked(group, job, "blocked", dashboardErrorMessage(err))
					return
				}
				plan = dashboardFleetPlan{Monitor: monitor.MonitorInput, SyncNotifications: true, SyncNotificationsSet: true, NotificationSourceRegistrationID: registration}
			}
			row.GroupIDs = unique(append(row.GroupIDs, id))
			targets, err := d.groupTargetsLocked(row.GroupIDs)
			if err != nil {
				return
			}
			for _, existing := range row.RegistrationIDs {
				if !slices.Contains(targets, existing) {
					row.PinnedRegistrationIDs = unique(append(row.PinnedRegistrationIDs, existing))
				}
			}
			row.RegistrationIDs, err = d.groupTargetsLocked(row.GroupIDs, row.PinnedRegistrationIDs...)
			if err != nil {
				d.finishGroupSyncLocked(group, job, "blocked", dashboardErrorMessage(err))
				return
			}
			if !plan.SyncNotificationsSet {
				plan.SyncNotifications = true
				plan.SyncNotificationsSet = true
			}
			if plan.NotificationSourceRegistrationID == "" {
				plan.NotificationSourceRegistrationID = d.fleetNotificationSource(row, plan, registration)
			}
			row.Revision, row.UpdatedAt, row.State, row.Reason, row.RetryAt = newID(), time.Now().UnixMilli(), "pending", "", 0
			row.ActorID, row.ActorRole, row.ActorVersion = job.ActorID, job.ActorRole, job.ActorVersion
			plan.Prepared, plan.Commands, plan.ObservedRevisions = false, nil, nil
			if err = d.saveFleet(nil, AccountView{}, row, plan); err != nil {
				return
			}
		}
	}
	for _, row := range d.fleet {
		if !slices.Contains(row.GroupIDs, id) || row.State == "removed" {
			continue
		}
		if row.State == "blocked" && job.RetryAt == 0 && !d.fleetBusy[row.ID] {
			plan, err := d.openFleet(row)
			if err != nil {
				return
			}
			// An explicit group sync may retry preflight. Never replace an
			// already prepared command whose write can still be unresolved.
			if !plan.Prepared && !plan.Deleted {
				row = copyFleet(row)
				row.ActorID, row.ActorRole, row.ActorVersion = job.ActorID, job.ActorRole, job.ActorVersion
				row.Revision, row.UpdatedAt, row.RetryAt = newID(), time.Now().UnixMilli(), 0
				row.State, row.Reason = "pending", ""
				if d.saveFleet(nil, AccountView{}, row, plan) != nil {
					return
				}
			}
		}
		if row.State == "applied" && !d.fleetBusy[row.ID] && !d.fleetAuthorized(row) {
			plan, err := d.openFleet(row)
			if err != nil {
				return
			}
			row = copyFleet(row)
			row.ActorID, row.ActorRole, row.ActorVersion = job.ActorID, job.ActorRole, job.ActorVersion
			row.Revision, row.UpdatedAt, row.RetryAt = newID(), time.Now().UnixMilli(), 0
			if d.saveFleet(nil, AccountView{}, row, plan) != nil {
				return
			}
		}
	}
	// Retry and renew every eligible definition before a pending row can end
	// the status pass. One explicit sync covers the complete group.
	for _, row := range d.fleet {
		if !slices.Contains(row.GroupIDs, id) || row.State == "removed" {
			continue
		}
		if row.State == "blocked" || row.State == "unauthorized" {
			d.finishGroupSyncLocked(group, job, "blocked", row.Reason)
			return
		}
		expected, err := d.groupTargetsLocked(row.GroupIDs, row.PinnedRegistrationIDs...)
		if err != nil {
			d.finishGroupSyncLocked(group, job, "blocked", dashboardErrorMessage(err))
			return
		}
		if !sameFleetIDs(row.RegistrationIDs, expected) {
			d.finishGroupSyncLocked(group, job, "pending", "Waiting for the group's membership changes to reach its monitors")
			return
		}
		if row.State != "applied" {
			d.finishGroupSyncLocked(group, job, "pending", "Monitors collected; waiting for each node to confirm its copies")
			return
		}
	}
	d.finishGroupSyncLocked(group, job, "applied", "")
}

func fleetKnownPeers(a, b Overview) bool {
	has := func(view Overview, id string) bool {
		for _, node := range view.Nodes {
			if node.ID == id && node.State != "local" && node.LastSeenAt != nil && *node.LastSeenAt > 0 {
				return true
			}
		}
		return false
	}
	return a.Node.ID == b.Node.ID || has(a, b.Node.ID) && has(b, a.Node.ID)
}
func (d *Dashboard) fleetForSourceLocked(registration, monitor string) (dashboardFleetRecord, bool) {
	for _, row := range d.fleet {
		if row.State == "removed" {
			continue
		}
		if row.Source != nil && row.Source.RegistrationID == registration && row.Source.MonitorID == monitor {
			return row, true
		}
		for _, p := range row.Placements {
			if p.State != "removed" && p.MonitorID == monitor && (p.RegistrationID == registration || slices.Contains(p.RegistrationIDs, registration)) {
				return row, true
			}
		}
	}
	return dashboardFleetRecord{}, false
}
func (d *Dashboard) finishGroupSync(group dashboardNodeGroup, job dashboardGroupSync, state, reason string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.finishGroupSyncLocked(group, job, state, reason)
}
func (d *Dashboard) finishGroupSyncLocked(group dashboardNodeGroup, job dashboardGroupSync, state, reason string) {
	n := d.accounts.node
	n.mu.Lock()
	defer n.mu.Unlock()
	current, ok, err := loadOne[dashboardNodeGroup](n.store, "dashboardNodeGroups", group.ID)
	if err != nil || !ok || current.ID != group.ID || current.Revision != group.Revision || d.verifyNodeGroup(current) != nil {
		return
	}
	current.SyncState, current.SyncReason = state, reason
	job.RetryAt = time.Now().Add(30 * time.Second).UnixMilli()
	if d.signNodeGroup(&current) != nil || d.signGroupJob(&job) != nil {
		return
	}
	_ = n.identityTransactionLocked(func(e dbExec) error {
		if err := put(e, "dashboardNodeGroups", current.ID, current); err != nil {
			return err
		}
		if state != "pending" {
			return remove(e, "dashboardGroupSyncs", current.ID)
		}
		return put(e, "dashboardGroupSyncs", current.ID, job)
	})
}

// Group assignments and source channel settings are refreshed without replacing
// an unresolved command or an edit made directly on a monitoring node.
func (d *Dashboard) refreshGroupPlacements(ctx context.Context) {
	d.mu.Lock()
	rows := []dashboardFleetRecord{}
	for _, row := range d.fleet {
		if row.State == "applied" && !d.fleetBusy[row.ID] && row.RetryAt <= time.Now().UnixMilli() {
			rows = append(rows, copyFleet(row))
		}
	}
	d.mu.Unlock()
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		plan, err := d.openFleet(row)
		if err != nil || !d.fleetAuthorized(row) || len(row.GroupIDs) == 0 && !plan.SyncNotifications {
			continue
		}
		d.mu.Lock()
		targets := slices.Clone(row.RegistrationIDs)
		if len(row.GroupIDs) > 0 {
			targets, err = d.groupTargetsLocked(row.GroupIDs, row.PinnedRegistrationIDs...)
		}
		d.mu.Unlock()
		if err != nil {
			continue
		}
		changed := !sameFleetIDs(row.RegistrationIDs, targets)
		if plan.SyncNotifications {
			old := plan.Resources
			op, cancel := context.WithTimeout(ctx, 15*time.Second)
			err = d.prepareFleetSettings(op, row, &plan)
			cancel()
			if err != nil {
				d.delayFleetRefresh(row)
				continue
			}
			changed = changed || !equalJSON(old, plan.Resources)
		}
		d.mu.Lock()
		current := d.fleet[row.ID]
		if current.Revision == row.Revision && current.State == "applied" && !d.fleetBusy[row.ID] {
			row.RetryAt = time.Now().Add(30 * time.Second).UnixMilli()
			if changed {
				row.RegistrationIDs = targets
				row.Revision, row.UpdatedAt, row.State, row.Reason, row.RetryAt = newID(), time.Now().UnixMilli(), "pending", "", 0
				plan.Prepared, plan.Commands, plan.ObservedRevisions = false, nil, nil
			}
			_ = d.saveFleet(nil, AccountView{}, row, plan)
		}
		d.mu.Unlock()
	}
}
func (d *Dashboard) delayFleetRefresh(row dashboardFleetRecord) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if current := d.fleet[row.ID]; current.Revision != row.Revision || current.State != "applied" || d.fleetBusy[row.ID] {
		return
	}
	plan, err := d.openFleet(row)
	if err != nil {
		return
	}
	row.RetryAt = time.Now().Add(30 * time.Second).UnixMilli()
	_ = d.saveFleet(nil, AccountView{}, row, plan)
}
func sameFleetIDs(a, b []string) bool {
	left, right := slices.Clone(a), slices.Clone(b)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}
