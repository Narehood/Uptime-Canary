package canary

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"slices"
)

// A local delivery obligation is never replicated. It records the exact
// original source, destination and locally observed down run captured together
// with an intent; it grants no alias, receipt or closure authority.
type localIncidentDelivery struct {
	MonitorID     string `json:"monitorId"`
	IncidentID    string `json:"incidentId"`
	DestinationID string `json:"destinationId"`
	Revision      string `json:"revision"`
	ObserverID    string `json:"observerId"`
	NodeID        string `json:"nodeId"`
	Run           int64  `json:"run"`
}

func (v *localIncidentDelivery) UnmarshalJSON(b []byte) error {
	bad := func() error { return fail(503, "Invalid local incident delivery") }
	d := json.NewDecoder(bytes.NewReader(b))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return bad()
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || !slices.Contains([]string{"monitorId", "incidentId", "destinationId", "revision", "observerId", "nodeId", "run"}, key) {
			return bad()
		}
		if _, exists := fields[key]; exists {
			return bad()
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil || bytes.Equal(raw, []byte("null")) {
			return bad()
		}
		fields[key] = raw
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') || len(fields) != 7 {
		return bad()
	}
	if _, err := d.Token(); err != io.EOF {
		return bad()
	}
	type plain localIncidentDelivery
	var out plain
	if json.Unmarshal(b, &out) != nil {
		return bad()
	}
	*v = localIncidentDelivery(out)
	return nil
}

func (n *Node) loadLocalIncidentDeliveries() error {
	markers, err := loadAll[localIncidentDelivery](n.store, "localIncidentDeliveries")
	if err != nil {
		return fail(503, "Invalid local incident delivery")
	}
	checked := map[string]Incident{}
	for id, marker := range markers {
		intent := deliveryIntent(marker.MonitorID, marker.IncidentID, marker.DestinationID)
		if id != intent.ID || identifier(marker.MonitorID, "monitorId") != nil || identifier(marker.IncidentID, "incidentId") != nil || marker.DestinationID != "" && identifier(marker.DestinationID, "destinationId") != nil || marker.ObserverID != n.ID || marker.NodeID != n.ID || marker.Run < 1 || marker.Run > maxSafe || identifier(marker.Revision, "generation") != nil || !equalJSON(n.intents[id], intent) {
			return fail(503, "Invalid local incident delivery")
		}
		source, ok := checked[marker.IncidentID]
		if !ok {
			stored, exists := n.incidentSources[marker.IncidentID]
			var complete bool
			if !exists {
				return fail(503, "Invalid local incident delivery")
			}
			source, complete, err = hydrateIncident(stored, n.incidentFacts, n.evidenceLinks)
			if err != nil || !complete {
				return fail(503, "Invalid local incident delivery")
			}
			checked[marker.IncidentID] = source
		}
		if source.ID != marker.IncidentID || source.MonitorID != marker.MonitorID || source.Revision != marker.Revision || !slices.Contains(source.Runs[n.ID], marker.Run) {
			return fail(503, "Invalid local incident delivery")
		}
	}
	n.localIncidentDeliveries = markers
	return nil
}

// Only rootless original updates submitted by local writers can capture a
// marker. Authenticated peer snapshots always retain their nonempty Node.ID.
// Even a peer claim of our observer/run cannot manufacture local provenance.
func (n *Node) stageLocalIncidentDeliveries(s Snapshot, sources map[string]Incident, intents map[string]NotificationIntent, facts map[string]IncidentFact, links map[string]EvidenceLink) map[string]localIncidentDelivery {
	out := n.localIncidentDeliveries
	copied := false
	if s.Node.ID != "" || s.Protocol != 0 || !n.causalReady {
		return out
	}
	for _, update := range s.Incidents {
		if update.EvidenceRoot != "" || update.Closed != nil || update.RetiredBy != "" {
			continue
		}
		m := n.current(update.MonitorID)
		if m == nil || !m.Enabled || checkGeneration(*m) != update.Revision {
			continue
		}
		stored, exists := sources[update.ID]
		if !exists {
			continue
		}
		source, complete, err := hydrateIncident(stored, facts, links)
		if err != nil || !complete || source.ID != update.ID || source.Closed != nil || source.RetiredBy != "" {
			continue
		}
		for _, sample := range n.fresh(*m) {
			if sample.NodeID != n.ID || sample.State != "down" || sample.DownSince == nil || sampleGeneration(sample) != source.Revision || !slices.Contains(source.Runs[n.ID], *sample.DownSince) {
				continue
			}
			for _, target := range n.notificationTargets(*m) {
				intent := deliveryIntent(source.MonitorID, source.ID, target.ID)
				if !slices.Contains(update.DeliveryTargets, target.ID) || !equalJSON(intents[intent.ID], intent) {
					continue
				}
				if _, exists := out[intent.ID]; !exists {
					if !copied {
						out = maps.Clone(out)
						copied = true
					}
					out[intent.ID] = localIncidentDelivery{MonitorID: source.MonitorID, IncidentID: source.ID, DestinationID: target.ID, Revision: source.Revision, ObserverID: n.ID, NodeID: n.ID, Run: *sample.DownSince}
				}
			}
		}
	}
	return out
}

func (n *Node) hasLocalIncidentDelivery(i Incident, destination string) bool {
	for _, id := range episodeIDs(i) {
		key := deliveryIntent(i.MonitorID, id, destination).ID
		marker, exists := n.localIncidentDeliveries[key]
		if exists && marker.MonitorID == i.MonitorID && marker.IncidentID == id && marker.Revision == i.Revision && marker.DestinationID == destination && marker.ObserverID == n.ID && marker.NodeID == n.ID && slices.Contains(i.Runs[n.ID], marker.Run) {
			return true
		}
	}
	return false
}

// A successor ends repeat/recovery eligibility, but does not cancel a closed
// source's independently captured, destination-specific down obligation.
func (n *Node) owedClosedLocalDown(i Incident, destination string, successes []Receipt) bool {
	return i.Closed != nil && i.RetiredBy == "" && !hasReceipt(successes, "down") && n.hasDeliveryIntent(i, destination) && n.hasLocalIncidentDelivery(i, destination)
}

func (n *Node) localDeliveryEpisode(i Incident) bool {
	for _, id := range episodeIDs(i) {
		for destination := range n.intentIndex[i.MonitorID+"/"+id] {
			key := deliveryIntent(i.MonitorID, id, destination).ID
			marker, exists := n.localIncidentDeliveries[key]
			if exists && marker.MonitorID == i.MonitorID && marker.IncidentID == id && marker.Revision == i.Revision && marker.ObserverID == n.ID && marker.NodeID == n.ID && slices.Contains(i.Runs[n.ID], marker.Run) {
				return true
			}
		}
	}
	return false
}

// A pre-marker database can already contain an unchanged open source and
// intent. Fresh local re-witnessing must pass saveEpisode's no-op fast path
// once, so that the new obligation is captured by the ordinary transaction.
func (n *Node) needsLocalIncidentDelivery(i Incident) bool {
	if i.Closed != nil || i.RetiredBy != "" {
		return false
	}
	m := n.current(i.MonitorID)
	if m == nil || !m.Enabled || checkGeneration(*m) != i.Revision {
		return false
	}
	freshDown := false
	for _, sample := range n.fresh(*m) {
		freshDown = freshDown || sample.NodeID == n.ID && sample.State == "down" && sample.DownSince != nil && slices.Contains(i.Runs[n.ID], *sample.DownSince)
	}
	if !freshDown {
		return false
	}
	if len(episodeIDs(i)) == 1 {
		missing := false
		for _, target := range n.notificationTargets(*m) {
			missing = missing || !n.hasLocalIncidentDelivery(i, target.ID)
		}
		if !missing {
			return false
		}
	}
	updates := n.localIncidentUpdates(i)
	staged := n.stageLocalIncidentDeliveries(Snapshot{Incidents: updates}, n.incidentSources, n.intents, n.incidentFacts, n.evidenceLinks)
	return len(staged) > len(n.localIncidentDeliveries)
}

// A projection may temporarily alias two open original sources while a remote
// closure is still unknown. Local observations must not turn that inference
// into an irreversible alias fact. Preserve each source's original identity and
// attach new runs only to the source witnessed by the fresh matching run.
func (n *Node) localIncidentUpdates(i Incident) []Incident {
	previous, projected := n.episodes[i.ID]
	if !projected || i.EvidenceRoot == "" || len(episodeIDs(i)) == 1 {
		i.EvidenceRoot = ""
		return []Incident{i}
	}
	sources := []Incident{}
	for _, source := range n.incidentSources {
		if source.MonitorID != i.MonitorID || source.Revision != i.Revision || !slices.Contains(episodeIDs(i), source.ID) {
			continue
		}
		full, complete, err := hydrateIncident(source, n.incidentFacts, n.evidenceLinks)
		if err == nil && complete {
			sources = append(sources, full)
		}
	}
	if len(sources) == 0 {
		i.EvidenceRoot = ""
		return []Incident{i}
	}
	downs := []Sample{}
	if m := n.current(i.MonitorID); m != nil && checkGeneration(*m) == i.Revision {
		for _, s := range n.fresh(*m) {
			if s.State == "down" {
				downs = append(downs, s)
			}
		}
	}
	selected := activeEpisode(sources, downs)
	if selected == nil {
		selected = activeEpisode(sources, nil)
	}
	newRuns := map[string][]int64{}
	for node, runs := range i.Runs {
		for _, run := range runs {
			if !slices.Contains(previous.Runs[node], run) {
				newRuns[node] = append(newRuns[node], run)
			}
		}
	}
	newTargets := []string{}
	for _, target := range i.DeliveryTargets {
		if !slices.Contains(previous.DeliveryTargets, target) {
			newTargets = append(newTargets, target)
		}
	}
	// Fresh down evidence captures the current audience for the witnessed
	// source, even if another temporarily aliased source had already captured it.
	if len(downs) > 0 {
		if m := n.current(i.MonitorID); m != nil && checkGeneration(*m) == i.Revision {
			for _, target := range n.notificationTargets(*m) {
				newTargets = append(newTargets, target.ID)
			}
		}
	}
	updates := []Incident{}
	for _, source := range sources {
		out := source
		out.EvidenceRoot = ""
		out.MonitorName = i.MonitorName
		if selected != nil && source.ID == selected.ID {
			out.Runs = map[string][]int64{}
			for node, runs := range source.Runs {
				out.Runs[node] = append([]int64{}, runs...)
			}
			for node, runs := range newRuns {
				out.Runs[node] = unique(append(out.Runs[node], runs...))
			}
			out.DeliveryTargets = unique(append(append([]string{}, source.DeliveryTargets...), newTargets...))
		}
		if !equalJSON(i.Closed, previous.Closed) && i.Closed != nil && covers(i.Closed, out.Runs) {
			out.Closed = i.Closed
			out.ResolvedAt = i.ResolvedAt
		}
		if i.RetiredBy != "" {
			out.RetiredBy = i.RetiredBy
			out.Closed = i.Closed
			out.ResolvedAt = i.ResolvedAt
		}
		out.SuppressedByGroupID = i.SuppressedByGroupID
		out.SuppressionReason = i.SuppressionReason
		out.SuppressedAt = i.SuppressedAt
		out.SuppressionReleasedAt = i.SuppressionReleasedAt
		out.SuppressedComplete = i.SuppressedComplete
		maintenanceCombine(source, i, &out)
		updates = append(updates, out)
	}
	return updates
}
