package canary

import "slices"

func sharedDownRun(a, b Incident) bool {
	for node, runs := range a.Runs {
		for _, run := range runs {
			if slices.Contains(b.Runs[node], run) {
				return true
			}
		}
	}
	return false
}

type continuityRun struct {
	monitor, generation, node string
	run                       int64
}

// Complete embedded originals are proof nodes, not projected source members.
// Index exact runs so graph searches never invent an edge from missing closure.
type continuityGraph struct {
	nodes         []Incident
	runs          map[continuityRun][]int
	byKey         map[string]int
	referencedIDs map[string]bool
	witnesses     map[continuityRun]bool
	constraints   []continuityConstraints
	ids           [][]string
}

func continuityKey(i Incident) string {
	uncertainties := []string{}
	for _, u := range i.uncertainties {
		uncertainties = append(uncertainties, uncertaintyID(u))
	}
	return contentID([]any{i, unique(uncertainties)})
}

func newContinuityGraph(originals []Incident) *continuityGraph {
	g := &continuityGraph{runs: map[continuityRun][]int{}, byKey: map[string]int{}, referencedIDs: map[string]bool{}, witnesses: map[continuityRun]bool{}}
	queue := append([]Incident{}, originals...)
	byKey := map[string]Incident{}
	for index := 0; index < len(queue); index++ {
		i := combine(queue[index], queue[index])
		key := continuityKey(i)
		if _, seen := byKey[key]; seen {
			continue
		}
		byKey[key] = i
		for _, u := range i.uncertainties {
			if u.resolved != nil {
				queue = append(queue, *u.resolved)
			}
		}
	}
	keys := []string{}
	for key := range byKey {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		g.byKey[key] = len(g.nodes)
		g.nodes = append(g.nodes, byKey[key])
	}
	for index, i := range g.nodes {
		for _, id := range i.Predecessors {
			g.referencedIDs[id] = true
		}
		for _, u := range i.uncertainties {
			if u.resolved != nil && u.resolved.Closed == nil {
				g.witnesses[continuityRun{i.MonitorID, i.Revision, u.NodeID, u.Run}] = true
			}
		}
		for node, runs := range i.Runs {
			for _, run := range runs {
				key := continuityRun{i.MonitorID, i.Revision, node, run}
				g.runs[key] = append(g.runs[key], index)
			}
		}
	}
	for _, i := range g.nodes {
		g.constraints = append(g.constraints, g.constraintState(i))
		g.ids = append(g.ids, episodeIDs(i))
	}
	return g
}

type continuityRunRange struct{ First, Last int64 }
type continuityConstraints struct {
	key, base string
	ranges    map[string]continuityRunRange
}

// Quotient only by information read by the aggregate authority barriers. Run
// extrema suffice for covers/after-closure; separately preserve membership of
// every exact uncertainty witness. Unreferenced aliases, audience, roots and
// observer labels cannot change these barriers and must not enumerate subsets
// of otherwise identical same-run proof nodes in a dense cycle.
func (g *continuityGraph) constraintState(i Incident) continuityConstraints {
	ranges := map[string]continuityRunRange{}
	for node, runs := range i.Runs {
		for index, run := range runs {
			value := ranges[node]
			if index == 0 {
				value.First, value.Last = run, run
			} else {
				value.First, value.Last = min(value.First, run), max(value.Last, run)
			}
			ranges[node] = value
		}
	}
	witnesses := []string{}
	for witness := range g.witnesses {
		if witness.monitor == i.MonitorID && witness.generation == i.Revision && slices.Contains(i.Runs[witness.node], witness.run) {
			witnesses = append(witnesses, contentID([]any{witness.node, witness.run}))
		}
	}
	conditions := []string{}
	for _, u := range i.uncertainties {
		if u.resolved == nil {
			conditions = append(conditions, "missing/"+uncertaintyID(u))
		} else if boundary := *u.resolved; boundary.Closed != nil {
			conditions = append(conditions, "closed/"+contentID([]any{boundary.OpenedAt, boundary.ResolvedAt, boundary.Closed}))
		} else {
			conditions = append(conditions, "open/"+contentID([]any{u.NodeID, u.Run}))
		}
	}
	ids := []string{}
	for _, id := range episodeIDs(i) {
		if g.referencedIDs[id] {
			ids = append(ids, id)
		}
	}
	base := contentID([]any{i.MonitorID, i.Revision, i.OpenedAt, i.ResolvedAt, i.Closed, unique(i.Predecessors), ids, unique(witnesses), unique(conditions)})
	return continuityConstraints{key: contentID([]any{base, ranges}), base: base, ranges: ranges}
}

// With all other barriers and exact-witness memberships equal, a contained
// nonempty run range imposes weaker covers/after-closure constraints. Exploring
// its superset at the same original cannot unlock a positive edge. Preserve
// incomparable states, including different closures or predecessor frontiers.
func (a continuityConstraints) dominates(b continuityConstraints) bool {
	if a.base != b.base || len(a.ranges) == 0 {
		return false
	}
	for node, prior := range a.ranges {
		next, exists := b.ranges[node]
		if !exists || prior.First < next.First || prior.Last > next.Last {
			return false
		}
	}
	return true
}

func continuityUnionCompatible(a, b Incident, context []Incident) bool {
	return a.MonitorID == b.MonitorID && a.Revision == b.Revision && uncertaintyCompatibility(a, b, false) && uncertaintyCompatibility(b, a, false) && compatibleLifetimes(a, b, context)
}

type continuitySearchStats struct{ States, Edges int }

func (g *continuityGraph) positivePath(a, b, aggregate Incident, projected []Incident) bool {
	found, _ := g.searchPositivePath(a, b, aggregate, projected)
	return found
}

// Keep the aggregate of each actual path and both candidates. Individual edges
// alone cannot reject a mixed old/new bridge. Equivalent causal constraint
// states share visitation, while genuinely different frontiers stay searchable.
func (g *continuityGraph) searchPositivePath(a, b, aggregate Incident, projected []Incident) (bool, continuitySearchStats) {
	context := append(append([]Incident{}, g.nodes...), projected...)
	type state struct {
		original, aggregate Incident
		index               int
		constraints         continuityConstraints
		originalKey         string
		ids                 []string
	}
	start, exists := g.byKey[continuityKey(a)]
	if !exists {
		start = -1
	}
	type stateKey struct {
		original    int
		constraints string
	}
	initial := g.constraintState(aggregate)
	queue := []state{{original: a, aggregate: aggregate, index: start, constraints: initial, originalKey: g.constraintState(a).key, ids: episodeIDs(a)}}
	seen := map[stateKey]bool{{start, initial.key}: true}
	frontiers := map[int][]continuityConstraints{start: {initial}}
	type pair struct{ from, to string }
	type edgeClass struct {
		pair
		aliases bool
	}
	edges := map[edgeClass]bool{}
	type transition struct {
		aggregate   Incident
		constraints continuityConstraints
		compatible  bool
	}
	transitions := map[pair]transition{}
	stats := continuitySearchStats{}
	for index := 0; index < len(queue); index++ {
		current := queue[index]
		if sharedDownRun(current.original, b) && causallyEquivalent(current.original, b, context) {
			stats.States = len(seen)
			return true, stats
		}
		neighbors := map[int]bool{}
		for node, runs := range current.original.Runs {
			for _, run := range runs {
				for _, next := range g.runs[continuityRun{a.MonitorID, a.Revision, node, run}] {
					neighbors[next] = true
				}
			}
		}
		indices := []int{}
		for next := range neighbors {
			indices = append(indices, next)
		}
		slices.Sort(indices)
		for _, next := range indices {
			original := g.nodes[next]
			edge := edgeClass{pair{current.originalKey, g.constraints[next].key}, intersects(current.ids, g.ids[next])}
			positive, checked := edges[edge]
			if !checked {
				stats.Edges++
				positive = causallyEquivalent(current.original, original, context)
				edges[edge] = positive
			}
			if !positive {
				continue
			}
			// Equal causal classes have identical aggregate guards. Retain one
			// representative transition instead of rebuilding the same union
			// for every unreferenced proof ID in a dense ongoing component.
			transitionKey := pair{current.constraints.key, g.constraints[next].key}
			step, known := transitions[transitionKey]
			if !known {
				step.compatible = continuityUnionCompatible(current.aggregate, original, context)
				if step.compatible {
					step.aggregate = combine(current.aggregate, original)
					step.constraints = g.constraintState(step.aggregate)
				}
				transitions[transitionKey] = step
			}
			if !step.compatible {
				continue
			}
			combined, constraints := step.aggregate, step.constraints
			key := stateKey{next, constraints.key}
			if !seen[key] {
				seen[key] = true
				dominated := false
				for _, prior := range frontiers[next] {
					if prior.dominates(constraints) {
						dominated = true
						break
					}
				}
				if dominated {
					continue
				}
				remaining := []continuityConstraints{}
				for _, prior := range frontiers[next] {
					if !constraints.dominates(prior) {
						remaining = append(remaining, prior)
					}
				}
				frontiers[next] = append(remaining, constraints)
				queue = append(queue, state{original: original, aggregate: combined, index: next, constraints: constraints, originalKey: g.constraints[next].key, ids: g.ids[next]})
			}
		}
	}
	stats.States = len(seen)
	return false, stats
}

// Also used by proof controls without a standalone original source set.
func positiveContinuity(a, b Incident, records []Incident) bool {
	g := newContinuityGraph(append(append([]Incident{}, records...), a, b))
	context := append(append([]Incident{}, g.nodes...), records...)
	return continuityUnionCompatible(a, b, context) && g.positivePath(a, b, combine(a, b), records)
}

// Alias and run facts are retained monotonically. In particular, never use the
// absence of a predecessor page as proof that two open lifetimes are the same.
// An unmatched boundary may be crossed only with affirmative closure evidence.
// This prevents new merges; already merged protocol-4 histories lack the source
// provenance needed to split them safely and require coordinated migration.
func compatibleFrontiers(a, b Incident, records []Incident) bool {
	for _, predecessor := range a.Predecessors {
		if slices.Contains(b.Predecessors, predecessor) {
			continue
		}
		boundary := predecessorEpisode(a, predecessor, records)
		if boundary == nil {
			return false
		}
		if intersects(b.Predecessors, episodeIDs(*boundary)) {
			continue
		}
		if !runsAfterClosure(b.Runs, b.OpenedAt, *boundary) {
			return false
		}
	}
	return true
}

func predecessorEpisode(i Incident, id string, records []Incident) *Incident {
	var boundary *Incident
	for _, record := range records {
		if record.MonitorID != i.MonitorID || record.Revision != i.Revision || record.Closed == nil || !slices.Contains(episodeIDs(record), id) {
			continue
		}
		if boundary == nil {
			copy := record
			boundary = &copy
		} else {
			copy := combine(*boundary, record)
			boundary = &copy
		}
	}
	return boundary
}

func runsAfterClosure(runs map[string][]int64, opened int64, boundary Incident) bool {
	if boundary.Closed == nil || boundary.ResolvedAt == nil || opened <= *boundary.ResolvedAt {
		return false
	}
	found := false
	for node, seqs := range runs {
		for _, seq := range seqs {
			closed, known := boundary.Closed[node]
			if !known || seq <= closed {
				return false
			}
			found = true
		}
	}
	return found
}

// activeEpisode receives records for one monitor/check generation, including
// closed predecessors, and the caller's already filtered fresh down samples.
// Extending an incident requires a matching run as a continuity witness. With
// no downs it selects a causal latest open record for the caller's all-up gate.
// Return a copy: the caller may append runs without mutating the source map.
func activeEpisode(records []Incident, downs []Sample) *Incident {
	var best *Incident
	bestMatches := -1
	for _, candidate := range records {
		if candidate.Closed != nil || candidate.RetiredBy != "" {
			continue
		}
		superseded := false
		for _, other := range records {
			if other.ID != candidate.ID && intersects(other.Predecessors, episodeIDs(candidate)) {
				superseded = true
				break
			}
		}
		if superseded {
			continue
		}
		matches := 0
		compatible := true
		for _, sample := range downs {
			if sample.State != "down" || sample.DownSince == nil {
				compatible = false
				break
			}
			run := *sample.DownSince
			for _, known := range candidate.Runs[sample.NodeID] {
				if run < known {
					compatible = false
				}
			}
			if slices.Contains(candidate.Runs[sample.NodeID], run) {
				matches++
				continue
			}
			for _, predecessor := range candidate.Predecessors {
				boundary := predecessorEpisode(candidate, predecessor, records)
				if boundary == nil || !runsAfterClosure(map[string][]int64{sample.NodeID: {run}}, sample.CheckedAt, *boundary) {
					compatible = false
				}
			}
		}
		if !compatible || len(downs) > 0 && matches == 0 {
			continue
		}
		if best == nil || len(candidate.Predecessors) > len(best.Predecessors) || len(candidate.Predecessors) == len(best.Predecessors) && (matches > bestMatches || matches == bestMatches && (candidate.OpenedAt > best.OpenedAt || candidate.OpenedAt == best.OpenedAt && candidate.ID < best.ID)) {
			copy := candidate
			best, bestMatches = &copy, matches
		}
	}
	return best
}
