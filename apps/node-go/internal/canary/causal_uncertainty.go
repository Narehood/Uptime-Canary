package canary

import "slices"

// Only bounded, non-authoritative header metadata is carried here. Its root
// still needs every original fact/link before it can justify any relationship.
// Audience, aliases and run suffixes from an opaque summary are never imported.
type uncertainHistory struct {
	ID           string           `json:"id"`
	MonitorID    string           `json:"monitorId"`
	Revision     string           `json:"revision"`
	OpenedAt     int64            `json:"openedAt"`
	Closed       map[string]int64 `json:"closed"`
	ResolvedAt   *int64           `json:"resolvedAt"`
	EvidenceRoot string           `json:"evidenceRoot"`
}

type incidentUncertainty struct {
	Header     uncertainHistory
	ObserverID string
	NodeID     string
	Run        int64
	resolved   *Incident
}

func uncertainHeader(i Incident) uncertainHistory {
	return uncertainHistory{ID: i.ID, MonitorID: i.MonitorID, Revision: i.Revision, OpenedAt: i.OpenedAt, Closed: i.Closed, ResolvedAt: i.ResolvedAt, EvidenceRoot: i.EvidenceRoot}
}
func (h uncertainHistory) incident() Incident {
	return Incident{ID: h.ID, MonitorID: h.MonitorID, Revision: h.Revision, OpenedAt: h.OpenedAt, Closed: h.Closed, ResolvedAt: h.ResolvedAt, EvidenceRoot: h.EvidenceRoot}
}
func uncertaintyID(u incidentUncertainty) string { u.resolved = nil; return contentID(u) }
func combineUncertainties(a, b []incidentUncertainty) []incidentUncertainty {
	byID := map[string]incidentUncertainty{}
	for _, set := range [][]incidentUncertainty{a, b} {
		for _, u := range set {
			id := uncertaintyID(u)
			if old, known := byID[id]; !known || old.resolved == nil {
				byID[id] = u
			}
		}
	}
	ids := []string{}
	for id := range byID {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := []incidentUncertainty{}
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out
}

// Bind every committed boundary before deriving closure. This handles chains
// of interrupted branches without depending on map iteration or page order.
func resolveUncertaintySet(original map[string]Incident, facts map[string]IncidentFact, links map[string]EvidenceLink) (map[string]Incident, error) {
	sources := map[string]*Incident{}
	queue := []*Incident{}
	for id, i := range original {
		copy := i
		copy.uncertainties = append([]incidentUncertainty{}, i.uncertainties...)
		sources[id] = &copy
		queue = append(queue, &copy)
	}
	boundaries := map[string]*Incident{}
	checked := map[string]bool{}
	for index := 0; index < len(queue); index++ {
		record := queue[index]
		for next, u := range record.uncertainties {
			key := contentID(u.Header)
			if !checked[key] {
				checked[key] = true
				full, complete, err := hydrateIncident(u.Header.incident(), facts, links)
				if err != nil {
					return nil, err
				}
				if complete {
					if latest, ok := original[full.ID]; ok {
						if latest.MonitorID != full.MonitorID || latest.Revision != full.Revision {
							return nil, fail(400, "Uncertainty boundary scope mismatch")
						}
						full = combine(full, latest)
					}
					boundaries[key] = &full
					queue = append(queue, &full)
				}
			}
			record.uncertainties[next].resolved = boundaries[key]
		}
	}
	sourceIDs := []string{}
	for id := range sources {
		sourceIDs = append(sourceIDs, id)
	}
	slices.Sort(sourceIDs)
	keys := []string{}
	for key := range boundaries {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	// Standalone predecessors need the same closure derivations as embedded
	// boundaries, even when no uncertainty header names the predecessor. Keep
	// separate proof copies: returning a derived standalone union would promote
	// proof-only aliases and closure into the original source set.
	proof := []*Incident{}
	if len(keys) > 0 {
		// With no complete embedded boundary there is no dependent proof to
		// refresh; ordinary source projection handles standalone lifetimes.
		for _, id := range sourceIDs {
			copy := *sources[id]
			proof = append(proof, &copy)
		}
	}
	for _, key := range keys {
		proof = append(proof, boundaries[key])
	}
	// Refresh all complete proof originals each round so a derived closure can
	// satisfy another original's dependency without depending on page order.
	for {
		context := make([]Incident, len(proof))
		for index, record := range proof {
			context[index] = *record
		}
		changed := false
		for _, boundary := range proof {
			before := continuityKey(*boundary)
			for _, closed := range context {
				if closed.Closed != nil && causallyEquivalent(*boundary, closed, context) {
					*boundary = combine(*boundary, closed)
				}
			}
			changed = changed || before != continuityKey(*boundary)
		}
		if !changed {
			break
		}
	}
	out := map[string]Incident{}
	for id, source := range sources {
		out[id] = *source
	}
	return out, nil
}

func insideClosure(i, boundary Incident) bool {
	return boundary.Closed != nil && boundary.ResolvedAt != nil && covers(boundary.Closed, i.Runs) && i.OpenedAt <= *boundary.ResolvedAt && (i.ResolvedAt == nil || boundary.OpenedAt <= *i.ResolvedAt)
}
func hasUncertaintyReferences(episodes map[string]Incident) bool {
	for _, i := range episodes {
		if len(i.uncertainties) > 0 {
			return true
		}
	}
	return false
}
func uncertaintiesCompatible(a, b Incident) bool {
	return uncertaintyCompatibility(a, b, true)
}

// A complete positive path may replace a direct shared run, but cannot replace
// proof that both aggregates stay on the same side of every closed boundary.
func uncertaintyCompatibility(a, b Incident, directWitness bool) bool {
	for _, u := range a.uncertainties {
		if u.resolved == nil {
			return false
		}
		boundary := *u.resolved
		if boundary.Closed != nil {
			if insideClosure(a, boundary) && insideClosure(b, boundary) {
				continue
			}
			if runsAfterClosure(a.Runs, a.OpenedAt, boundary) && runsAfterClosure(b.Runs, b.OpenedAt, boundary) {
				continue
			}
			return false
		}
		// Two open records need a positive continuity witness. Mere absence
		// of a closure page cannot transfer an old successful receipt.
		if !slices.Contains(a.Runs[u.NodeID], u.Run) || directWitness && !slices.Contains(b.Runs[u.NodeID], u.Run) {
			return false
		}
	}
	return true
}

func (n *Node) interruptedHeaders(m Monitor) []uncertainHistory {
	headers := map[string]uncertainHistory{}
	for _, i := range n.pendingIncidents {
		if i.MonitorID == m.ID && i.Revision == checkGeneration(m) {
			h := uncertainHeader(i)
			headers[contentID(h)] = h
		}
	}
	for _, i := range n.episodes {
		if i.MonitorID == m.ID && i.Revision == checkGeneration(m) && i.Closed == nil {
			for _, u := range i.uncertainties {
				headers[contentID(u.Header)] = u.Header
			}
		}
	}
	ids := []string{}
	for id := range headers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := []uncertainHistory{}
	for _, id := range ids {
		out = append(out, headers[id])
	}
	return out
}
func (n *Node) interruptedHistory(m Monitor) bool {
	for _, i := range n.pendingIncidents {
		if i.MonitorID == m.ID && i.Revision == checkGeneration(m) {
			return true
		}
	}
	for _, i := range n.episodes {
		if i.MonitorID == m.ID && i.Revision == checkGeneration(m) && i.Closed == nil && len(i.uncertainties) > 0 {
			return true
		}
	}
	return false
}

func downWitness(observations []Sample, observer string) *Sample {
	var best *Sample
	for _, s := range observations {
		if s.State != "down" || s.DownSince == nil {
			continue
		}
		if best == nil || s.NodeID == observer || best.NodeID != observer && s.NodeID < best.NodeID {
			copy := s
			best = &copy
		}
	}
	return best
}
func localBranch(i Incident, observer string, witness *Sample) bool {
	for _, u := range i.uncertainties {
		if u.ObserverID == observer && (witness == nil || u.NodeID == witness.NodeID && u.Run == *witness.DownSince) {
			return true
		}
	}
	return false
}

func endedLocalBranch(i Incident, observer string, observations []Sample) bool {
	for _, u := range i.uncertainties {
		if u.ObserverID == observer {
			for _, s := range observations {
				if s.NodeID == u.NodeID && s.State == "up" && s.Sequence > u.Run {
					return true
				}
			}
		}
	}
	return false
}

func (n *Node) owedLocalBranchDown(i Incident, destination string, observations []Sample) bool {
	return n.hasLocalIncidentDelivery(i, destination) || endedLocalBranch(i, n.ID, observations) && n.hasDeliveryIntent(i, destination)
}

// While historical authority is opaque, only a fresh branch's complete local
// evidence may deliver. In-flight workers repeat this gate immediately at I/O.
func (n *Node) deliveryEpisodeEligible(m Monitor, i Incident, observations []Sample) bool {
	if !n.interruptedHistory(m) {
		return true
	}
	if n.localDeliveryEpisode(i) {
		return true
	}
	witness := downWitness(observations, n.ID)
	if witness != nil {
		return localBranch(i, n.ID, witness)
	}
	return i.Closed != nil && localBranch(i, n.ID, nil) || endedLocalBranch(i, n.ID, observations)
}

func (n *Node) evaluateInterrupted(m Monitor, observations []Sample) {
	headers := n.interruptedHeaders(m)
	witness := downWitness(observations, n.ID)
	if witness != nil {
		var active *Incident
		for _, source := range n.incidentSources {
			if source.MonitorID != m.ID || source.Revision != checkGeneration(m) {
				continue
			}
			full, complete, err := hydrateIncident(source, n.incidentFacts, n.evidenceLinks)
			if err != nil || !complete || full.Closed != nil || !localBranch(full, n.ID, witness) {
				continue
			}
			if active == nil || full.OpenedAt > active.OpenedAt || full.OpenedAt == active.OpenedAt && full.ID < active.ID {
				copy := full
				active = &copy
			}
		}
		opening := active == nil
		if opening {
			id := newID()
			pred := []string{}
			for _, old := range n.episodes {
				if old.MonitorID == m.ID && old.Revision == checkGeneration(m) && old.Closed != nil {
					pred = append(pred, episodeIDs(old)...)
				}
			}
			active = &Incident{ID: id, Aliases: []string{id}, MonitorID: m.ID, MonitorName: m.Name, Revision: checkGeneration(m), OpenedAt: witness.CheckedAt, Runs: map[string][]int64{}, Predecessors: unique(pred)}
		}
		for _, header := range headers {
			if header.ID == active.ID {
				continue
			}
			active.uncertainties = combineUncertainties(active.uncertainties, []incidentUncertainty{{Header: header, ObserverID: n.ID, NodeID: witness.NodeID, Run: *witness.DownSince}})
		}
		*active = n.recordMaintenance(m, *active, opening)
		for _, target := range n.notificationTargets(m) {
			active.DeliveryTargets = append(active.DeliveryTargets, target.ID)
		}
		active.DeliveryTargets = unique(active.DeliveryTargets)
		n.saveEpisode(addRuns(*active, []Sample{*witness}))
		return
	}
	if len(observations) != len(m.NodeIDs) {
		return
	}
	for _, s := range observations {
		if s.State != "up" {
			return
		}
	}
	// Known current all-up evidence can close each locally witnessed branch;
	// an opaque remote run set cannot close or expand these local sources.
	for _, source := range n.incidentSources {
		if source.MonitorID != m.ID || source.Revision != checkGeneration(m) {
			continue
		}
		i, complete, err := hydrateIncident(source, n.incidentFacts, n.evidenceLinks)
		if err != nil || !complete || i.Closed != nil || !localBranch(i, n.ID, nil) {
			continue
		}
		up := true
		for node, runs := range i.Runs {
			for _, run := range runs {
				found := false
				for _, s := range observations {
					found = found || s.NodeID == node && s.Sequence > run
				}
				up = up && found
			}
		}
		if !up {
			continue
		}
		i = n.recordMaintenance(m, i, false)
		i.ResolvedAt = Ptr(n.now())
		i.Closed = map[string]int64{}
		for _, s := range observations {
			i.Closed[s.NodeID] = s.Sequence
		}
		if i.SuppressedByGroupID != "" && !hasReceipt(n.forEpisode(i), "down") {
			i.SuppressedComplete = true
		}
		i.MaintenanceComplete = n.maintenanceCovers(m, i, *i.ResolvedAt)
		n.saveEpisode(i)
	}
}
