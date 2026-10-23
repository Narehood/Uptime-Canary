package canary

import "slices"

func unique[T ~string | ~int64 | ~int](vs []T) []T {
	out := append([]T{}, vs...)
	slices.Sort(out)
	return slices.Compact(out)
}
func episodeIDs(i Incident) []string { return unique(append(append([]string{}, i.Aliases...), i.ID)) }
func intersects(a, b []string) bool {
	for _, x := range a {
		if slices.Contains(b, x) {
			return true
		}
	}
	return false
}
func covers(frontier map[string]int64, runs map[string][]int64) bool {
	for node, seqs := range runs {
		v, ok := frontier[node]
		for _, seq := range seqs {
			if !ok || v < seq {
				return false
			}
		}
	}
	return true
}
func equivalent(a, b Incident) bool {
	return causallyEquivalent(a, b, nil)
}
func causallyEquivalent(a, b Incident, records []Incident) bool {
	if a.MonitorID != b.MonitorID || a.Revision != b.Revision {
		return false
	}
	if intersects(episodeIDs(a), episodeIDs(b)) {
		return true
	}
	if !uncertaintiesCompatible(a, b) || !uncertaintiesCompatible(b, a) {
		return false
	}
	return compatibleLifetimes(a, b, records)
}

// Continuity can be proved through complete original sources. The resulting
// union must still satisfy every predecessor, run frontier and closure barrier;
// a positive edge never permits a mixed old/new bridge to cross a boundary.
func compatibleLifetimes(a, b Incident, records []Incident) bool {
	if a.MonitorID != b.MonitorID || a.Revision != b.Revision {
		return false
	}
	if intersects(a.Predecessors, episodeIDs(b)) || intersects(b.Predecessors, episodeIDs(a)) {
		return false
	}
	if !compatibleFrontiers(a, b, records) || !compatibleFrontiers(b, a, records) {
		return false
	}
	if a.Closed != nil && b.Closed != nil {
		return covers(a.Closed, b.Runs) && covers(b.Closed, a.Runs) && a.OpenedAt <= *b.ResolvedAt && b.OpenedAt <= *a.ResolvedAt
	}
	if a.Closed != nil {
		return covers(a.Closed, b.Runs) && b.OpenedAt <= *a.ResolvedAt
	}
	if b.Closed != nil {
		return covers(b.Closed, a.Runs) && a.OpenedAt <= *b.ResolvedAt
	}
	return true
}
func combine(a, b Incident) Incident {
	ids := unique(append(episodeIDs(a), episodeIDs(b)...))
	out := a
	out.uncertainties = combineUncertainties(a.uncertainties, b.uncertainties)
	out.ID = ids[0]
	out.Aliases = ids
	out.DeliveryTargets = unique(append(append([]string{}, a.DeliveryTargets...), b.DeliveryTargets...))
	out.Runs = map[string][]int64{}
	for _, i := range []Incident{a, b} {
		for node, seqs := range i.Runs {
			out.Runs[node] = unique(append(out.Runs[node], seqs...))
		}
	}
	out.Closed = nil
	for _, i := range []Incident{a, b} {
		if i.Closed != nil {
			if out.Closed == nil {
				out.Closed = map[string]int64{}
			}
			for node, seq := range i.Closed {
				out.Closed[node] = max(out.Closed[node], seq)
			}
		}
	}
	out.OpenedAt = min(a.OpenedAt, b.OpenedAt)
	if a.ResolvedAt == nil {
		out.ResolvedAt = b.ResolvedAt
	} else if b.ResolvedAt != nil {
		out.ResolvedAt = Ptr(min(*a.ResolvedAt, *b.ResolvedAt))
	}
	out.Predecessors = []string{}
	// A delayed open/closed sibling cannot undo retirement or authorize recovery.
	if b.RetiredBy != "" && (out.RetiredBy == "" || b.RetiredBy < out.RetiredBy) {
		out.RetiredBy = b.RetiredBy
	}
	for _, id := range unique(append(append([]string{}, a.Predecessors...), b.Predecessors...)) {
		if !slices.Contains(ids, id) {
			out.Predecessors = append(out.Predecessors, id)
		}
	}
	// Suppression metadata is monotonic for completed never-delivered episodes.
	out.SuppressedComplete = a.SuppressedComplete || b.SuppressedComplete
	if out.SuppressedByGroupID == "" {
		out.SuppressedByGroupID = b.SuppressedByGroupID
		out.SuppressionReason = b.SuppressionReason
	}
	if b.SuppressionReleasedAt != nil && (out.SuppressionReleasedAt == nil || *b.SuppressionReleasedAt > *out.SuppressionReleasedAt) {
		out.SuppressionReleasedAt = Ptr(*b.SuppressionReleasedAt)
	}
	if b.SuppressedAt != nil && (out.SuppressedAt == nil || *b.SuppressedAt > *out.SuppressedAt) {
		out.SuppressedAt = Ptr(*b.SuppressedAt)
	}
	maintenanceCombine(a, b, &out)
	return out
}
func reconcileEpisodes(records []Incident) []Incident {
	out := make([]Incident, len(records))
	for index, record := range records {
		out[index] = combine(record, record)
	}
	slices.SortFunc(out, func(a, b Incident) int {
		if (a.Closed == nil) != (b.Closed == nil) {
			if a.Closed != nil {
				return -1
			}
			return 1
		}
		if a.OpenedAt != b.OpenedAt {
			if a.OpenedAt < b.OpenedAt {
				return -1
			}
			return 1
		}
		return stringsCompare(a.ID, b.ID)
	})
	// Keep the original witnesses while folding projections. An aggregate
	// carries every member's uncertainty, but another member need not itself
	// contain each observer's run when a complete original contains both.
	members := make([][]Incident, len(out))
	for index, i := range out {
		members[index] = []Incident{i}
	}
	originals := append([]Incident{}, out...)
	var proof *continuityGraph
	for {
		changed := false
	outer:
		for i := range out {
			for j := i + 1; j < len(out); j++ {
				equivalent := causallyEquivalent(out[i], out[j], out)
				if !equivalent && out[i].MonitorID == out[j].MonitorID && out[i].Revision == out[j].Revision && uncertaintyCompatibility(out[i], out[j], false) && uncertaintyCompatibility(out[j], out[i], false) {
					if proof == nil {
						proof = newContinuityGraph(originals)
					}
					context := append(append([]Incident{}, proof.nodes...), out...)
					if compatibleLifetimes(out[i], out[j], context) {
						for _, a := range members[i] {
							for _, b := range members[j] {
								equivalent = equivalent || proof.positivePath(a, b, combine(out[i], out[j]), out)
							}
						}
					}
				}
				if equivalent {
					out[i] = combine(out[i], out[j])
					members[i] = append(members[i], members[j]...)
					members = append(members[:j], members[j+1:]...)
					out = append(out[:j], out[j+1:]...)
					changed = true
					break outer
				}
			}
		}
		if !changed {
			return out
		}
	}
}
func addRuns(i Incident, samples []Sample) Incident {
	runs := map[string][]int64{}
	for n, s := range i.Runs {
		runs[n] = append([]int64{}, s...)
	}
	for _, s := range samples {
		if s.State == "down" && s.DownSince != nil {
			runs[s.NodeID] = unique(append(runs[s.NodeID], *s.DownSince))
		}
	}
	i.Runs = runs
	return i
}
func sampleAfterClosure(s Sample, i Incident) bool {
	seq, ok := i.Closed[s.NodeID]
	return s.DownSince != nil && (!ok || *s.DownSince > seq)
}
func stringsCompare(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
