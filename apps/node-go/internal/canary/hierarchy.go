package canary

import "uptime-canary/apps/node-go/internal/rollup"

// Parent links organize health summaries. They do not silently extend alert
// suppression or maintenance selection to an ancestor's monitors.
func (n *Node) validateGroupParent(g Group) error {
	seen := map[string]bool{g.ID: true}
	for id := g.ParentID; id != ""; {
		if seen[id] {
			return fail(400, "Group hierarchy cycle")
		}
		seen[id] = true
		parent := n.currentGroup(id)
		if parent == nil {
			return fail(400, "parentId must reference a complete, unconflicted group hierarchy")
		}
		id = parent.ParentID
	}
	return nil
}

// The projection is read-only: late or contradictory hierarchy pages never
// remove configuration, stop probes, create incidents, or confer send authority.
// An ambiguous membership invalidates counts for its whole connected hierarchy
// component. Unrelated components remain evaluable.
func (n *Node) projectGroupHealth(groups []GroupView, monitors []MonitorView) {
	const invalid = "Hierarchy configuration is incomplete, conflicting, or cyclic"
	const capacity = "Health summary exceeds local evaluation capacity"
	unknownAll := func(reason string) {
		for i := range groups {
			groups[i].HealthState, groups[i].Health, groups[i].HealthReason = "unknown", nil, reason
		}
	}
	if len(groups) > rollup.MaxNodes {
		unknownAll(capacity)
		return
	}
	index := make(map[string]int, len(groups))
	parents := make([]int, len(groups))
	sizes := make([]int, len(groups))
	for i := range groups {
		index[groups[i].Group.ID], parents[i], sizes[i] = i, i, 1
	}
	find := func(i int) int {
		for parents[i] != i {
			parents[i] = parents[parents[i]]
			i = parents[i]
		}
		return i
	}
	join := func(a, b int) bool {
		a, b = find(a), find(b)
		if a == b {
			return false
		}
		if sizes[a] < sizes[b] {
			a, b = b, a
		}
		parents[b], sizes[a] = a, sizes[a]+sizes[b]
		return true
	}
	bad := make([]bool, len(groups))
	for i, view := range groups {
		heads := n.groups[view.Group.ID]
		if len(heads) != 1 || view.ConfigConflict {
			bad[i] = true
		}
		// Include every possible live parent, even when its proof is incomplete.
		// Picking the first conflict candidate could expose healthy false counts.
		for _, head := range heads {
			g := head.Group
			if g.Deleted || g.ParentID == "" {
				continue
			}
			parent, exists := index[g.ParentID]
			if !exists {
				bad[i] = true
				continue
			}
			if !join(i, parent) {
				bad[i] = true
			}
		}
	}
	grouped := make([]int, 0, min(len(monitors), rollup.MaxNodes))
	for monitorIndex, view := range monitors {
		if view.ConfigConflict {
			for _, head := range n.heads[view.Monitor.ID] {
				if !head.Monitor.Deleted {
					if i, exists := index[head.Monitor.GroupID]; exists {
						bad[i] = true
					}
				}
			}
			continue
		}
		if _, exists := index[view.Monitor.GroupID]; !exists {
			continue
		}
		if len(groups)+len(grouped) == rollup.MaxNodes {
			unknownAll(capacity)
			return
		}
		grouped = append(grouped, monitorIndex)
	}
	badComponent := make(map[int]bool)
	for i, isInvalid := range bad {
		if isInvalid {
			badComponent[find(i)] = true
		}
	}
	nodes := make([]rollup.Node, 0, len(groups)+len(grouped))
	for i, view := range groups {
		if badComponent[find(i)] {
			continue
		}
		g := view.Group
		parent := ""
		if g.ParentID != "" {
			parent = "group:" + g.ParentID
		}
		nodes = append(nodes, rollup.Node{ID: "group:" + g.ID, ParentID: parent, Group: true, Enabled: true})
	}
	observations := make([]rollup.Observation, 0, len(grouped))
	for _, monitorIndex := range grouped {
		view := monitors[monitorIndex]
		m := view.Monitor
		if badComponent[find(index[m.GroupID])] {
			continue
		}
		id := "monitor:" + m.ID
		nodes = append(nodes, rollup.Node{ID: id, ParentID: "group:" + m.GroupID, Enabled: m.Enabled})
		if m.Enabled {
			// The monitor view already applies exact revision proof, assigned-node
			// freshness, and the runtime's down/unknown/pending health policy.
			observations = append(observations, rollup.Observation{ID: id, State: rollup.State(view.State), Fresh: true, Maintenance: len(view.MaintenanceIDs) != 0})
		}
	}
	plan, err := rollup.Compile(nodes)
	if err != nil {
		unknownAll("Health summary is unavailable")
		return
	}
	results, err := plan.Evaluate(observations)
	if err != nil {
		unknownAll("Health summary is unavailable")
		return
	}
	for i := range groups {
		if badComponent[find(i)] {
			groups[i].HealthState, groups[i].Health, groups[i].HealthReason = "unknown", nil, invalid
			continue
		}
		result, exists := results["group:"+groups[i].Group.ID]
		if !exists {
			groups[i].HealthState, groups[i].Health, groups[i].HealthReason = "unknown", nil, "Health summary is unavailable"
			continue
		}
		groups[i].HealthState, groups[i].Health, groups[i].HealthReason = string(result.State), &result, ""
	}
}
