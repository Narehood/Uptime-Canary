package canary

import "slices"

// Configuration references are strict on local management changes. Replication
// may arrive out of order; unknown/conflicting/cyclic dependencies fail open.
func (n *Node) validateReferences(m *Monitor, g *Group) error {
	return n.validateFleetReferences(m, g, nil)
}

func (n *Node) validateFleetReferences(m *Monitor, g *Group, bundled map[string]bool) error {
	if m != nil {
		if m.Type == "push" && m.Push != nil {
			if matches(n.clusterHash, m.Push.Token) {
				return fail(400, "Push and cluster credentials must differ")
			}
			for _, a := range n.accounts {
				if a.Hash != "" && matches(a.Hash, m.Push.Token) {
					return fail(400, "Push and management credentials must differ")
				}
			}
			if _, exists := n.sessions[digest(m.Push.Token)]; exists {
				return fail(400, "Push and management credentials must differ")
			}
			for id, heads := range n.heads {
				if id == m.ID {
					continue
				}
				for _, head := range heads {
					other := head.Monitor
					if !other.Deleted && other.Type == "push" && other.Push != nil && other.Push.Token == m.Push.Token {
						return fail(409, "Push token is already assigned")
					}
				}
			}
		}
		for _, tag := range m.Tags {
			if !bundled[resourceKey(resourceTag, tag.TagID)] && n.currentResource(resourceTag, tag.TagID) == nil {
				return fail(400, "tags must reference existing unconflicted tags")
			}
		}
		for _, id := range m.NotificationIDs {
			if !bundled[resourceKey(resourceChannel, id)] && n.currentResource(resourceChannel, id) == nil {
				return fail(400, "notificationIds must reference existing unconflicted channels")
			}
		}
	}
	if m != nil && m.GroupID != "" && n.currentGroup(m.GroupID) == nil {
		return fail(400, "groupId must reference an existing unconflicted group")
	}
	if g != nil && g.OutageMonitorID != "" && n.current(g.OutageMonitorID) == nil {
		return fail(400, "outageMonitorId must reference an existing unconflicted monitor")
	}
	if g != nil {
		if err := n.validateGroupParent(*g); err != nil {
			return err
		}
	}
	if m != nil && n.dependencyCycle(m, g, m.ID) {
		return fail(400, "Group dependency cycle")
	}
	if g != nil {
		for id := range n.heads {
			if member := n.current(id); member != nil && member.GroupID == g.ID && n.dependencyCycle(m, g, id) {
				return fail(400, "Group dependency cycle")
			}
		}
	}
	return nil
}

// A monitor's edge points to its group's ordinary dependency monitor. The
// dependency itself is exempt from its own group, so it has no self edge.
func (n *Node) dependencyCycle(overrideM *Monitor, overrideG *Group, start string) bool {
	monitors := map[string]Monitor{}
	groups := map[string]Group{}
	for id := range n.heads {
		if m := n.current(id); m != nil {
			monitors[id] = *m
		}
	}
	for id := range n.groups {
		if g := n.currentGroup(id); g != nil {
			groups[id] = *g
		}
	}
	if overrideM != nil {
		monitors[overrideM.ID] = *overrideM
	}
	if overrideG != nil {
		groups[overrideG.ID] = *overrideG
	}
	marks := map[string]int{}
	var visit func(string) bool
	visit = func(id string) bool {
		if marks[id] == 1 {
			return true
		}
		if marks[id] == 2 {
			return false
		}
		marks[id] = 1
		m, ok := monitors[id]
		if ok {
			if g, ok := groups[m.GroupID]; ok && g.OutageMonitorID != "" && g.OutageMonitorID != id {
				if visit(g.OutageMonitorID) {
					return true
				}
			}
		}
		marks[id] = 2
		return false
	}
	if start != "" {
		return visit(start)
	}
	for id := range monitors {
		if visit(id) {
			return true
		}
	}
	return false
}
func (n *Node) groupEvidence(g Group) (state, reason string, active bool) {
	if len(n.groups[g.ID]) != 1 {
		return "unknown", "Group configuration has conflicting revisions", false
	}
	if !n.groupVersionReady(n.groups[g.ID][0]) {
		return "unknown", "Awaiting causal revision evidence", false
	}
	if g.OutageMonitorID == "" {
		return "unknown", "No outage dependency configured", false
	}
	p := n.current(g.OutageMonitorID)
	if p == nil {
		return "unknown", "Outage dependency is missing, deleted, or conflicting", false
	}
	if !p.Enabled {
		return "unknown", "Outage dependency is paused", false
	}
	if n.dependencyCycle(nil, nil, p.ID) {
		return "unknown", "Outage dependency cycle; notifications fail open", false
	}
	fresh := n.fresh(*p)
	for _, s := range fresh {
		if s.State == "down" {
			if len(n.notificationTargets(*p)) == 0 {
				return "down", "Outage dependency is down without a notification destination; notifications fail open after grace", false
			}
			for _, i := range n.episodes {
				if i.MonitorID == p.ID && i.Revision == checkGeneration(*p) && i.Closed == nil && s.DownSince != nil && slices.Contains(i.Runs[s.NodeID], *s.DownSince) && n.deliveryEpisodeEligible(*p, i, fresh) && hasReceipt(n.forEpisode(i), "down") {
					return "down", "Outage dependency is down and its current outage notification was delivered", true
				}
			}
			return "down", "Outage dependency is down without successful current outage delivery; notifications fail open after grace", false
		}
	}
	if len(fresh) != len(p.NodeIDs) {
		return "unknown", "Outage dependency evidence is unknown or stale", false
	}
	for _, s := range fresh {
		if s.State != "up" {
			return "unknown", "Outage dependency check is pending", false
		}
	}
	return "up", "Outage dependency is up", false
}
func (n *Node) baseSuppression(m Monitor) (groupID, reason string, grace int64) {
	if m.GroupID == "" {
		return
	}
	heads := n.groups[m.GroupID]
	if len(heads) == 0 {
		return
	}
	if len(heads) == 1 && !n.groupVersionReady(heads[0]) {
		return
	}
	var g *Group
	for _, h := range heads {
		if !h.Group.Deleted && h.Group.OutageMonitorID == m.ID {
			return
		}
		if !h.Group.Deleted {
			if g == nil {
				copy := h.Group
				g = &copy
			}
		}
	}
	if g == nil || g.OutageMonitorID == "" || g.OutageMonitorID == m.ID {
		return
	}
	grace = g.GraceMs
	if n.dependencyCycle(nil, nil, m.ID) {
		return "", "", grace
	}
	_, why, active := n.groupEvidence(*g)
	if active {
		return g.ID, why, grace
	}
	// Settling is bounded by this child's episode, never by refreshed parent
	// observations or provider retries. Only a delivered current parent episode
	// can extend suppression beyond the user's grace period.
	for _, i := range n.episodes {
		if i.MonitorID == m.ID && i.Revision == checkGeneration(m) && i.Closed == nil && n.now() < i.OpenedAt+grace {
			return g.ID, "Waiting for outage dependency grace period", grace
		}
	}
	return "", "", grace
}
func (n *Node) groupViews(monitors ...[]MonitorView) []GroupView {
	out := []GroupView{}
	for _, heads := range n.groups {
		var g *Group
		for _, v := range heads {
			if !v.Group.Deleted {
				c := v.Group
				g = &c
				break
			}
		}
		if g == nil {
			continue
		}
		state, reason, active := n.groupEvidence(*g)
		if !active {
			for id := range n.heads {
				if m := n.current(id); m != nil && m.Enabled && m.GroupID == g.ID {
					if group, why, _ := n.suppression(*m); group == g.ID {
						active, reason = true, why
						break
					}
				}
			}
		}
		reachable := 0
		for _, id := range g.NodeIDs {
			if n.connected(id) {
				reachable++
			}
		}
		out = append(out, GroupView{Group: *g, State: state, ConfigConflict: len(heads) != 1 || !n.groupVersionReady(heads[0]), SuppressionActive: active, Reason: reason, ReachableNodes: reachable, TotalNodes: len(g.NodeIDs)})
	}
	var healthMonitors []MonitorView
	if len(monitors) == 1 {
		healthMonitors = monitors[0]
	} else {
		healthMonitors = n.monitorViews("viewer")
	}
	n.projectGroupHealth(out, healthMonitors)
	slices.SortFunc(out, func(a, b GroupView) int { return stringsCompare(a.Group.Name, b.Group.Name) })
	return out
}
