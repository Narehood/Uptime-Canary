package canary

import "time"

// The archive uses the exact policy and explicit clock of the accepted sample.
// Known schedule boundaries shorten coverage rather than carrying an annotation
// across a future transition without another local observation.
func (n *Node) localAvailabilityPolicy(m Monitor, s Sample) (int64, bool) {
	until := min(maxSafe, s.CheckedAt+max(probeSpacing(m, s)*3, m.TimeoutMs*2, n.settings.SyncIntervalMs*3)+1)
	maintenance := false
	plans := n.currentMaintenancePlans()
	for _, heads := range n.resources {
		if len(heads) != 1 || heads[0].Resource.Kind != resourceMaintenance || heads[0].Resource.Deleted || !n.resourceVersionReady(heads[0]) {
			continue
		}
		in := maintenanceOf(heads[0].Resource)
		if !in.Enabled || !n.maintenanceSelects(in, m) {
			continue
		}
		plan := plans[maintenancePlanKey(in.Schedule)]
		if plan == nil {
			continue
		}
		state, err := plan.Evaluate(time.UnixMilli(s.CheckedAt))
		if err != nil {
			continue
		}
		maintenance = maintenance || state.Active
		for _, boundary := range []*time.Time{state.End, state.NextStart} {
			if boundary != nil && boundary.UnixMilli() > s.CheckedAt {
				until = min(until, boundary.UnixMilli())
			}
		}
	}
	return until, maintenance
}

// Configuration and proof changes can alter maintenance selection immediately.
// Closing all local heads is conservative; only fresh local probes reopen them.
func (n *Node) availabilityAnnotationsChanged(proof *Node, groups map[string][]GroupVersion, resources map[string][]ResourceVersion) bool {
	if !equalJSON(n.groups, groups) {
		return true
	}
	for _, heads := range groups {
		if len(heads) == 1 && n.groupVersionReady(heads[0]) != proof.groupVersionReady(heads[0]) {
			return true
		}
	}
	for key, heads := range resources {
		if len(heads) == 0 || heads[0].Resource.Kind != resourceMaintenance {
			continue
		}
		if !equalJSON(n.resources[key], heads) || len(heads) == 1 && n.resourceVersionReady(heads[0]) != proof.resourceVersionReady(heads[0]) {
			return true
		}
	}
	return false
}
