package canary

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

// Differentially retain the previous unpartitioned folding algorithm as the
// oracle. Interleaved monitor/generation scopes cannot grant each other proof.
func TestCapacityReconciliationScopeIsolation(t *testing.T) {
	records := []Incident{}
	for index := range 64 {
		prefix := fmt.Sprintf("scope-%03d", index)
		prior := Incident{ID: prefix + "-prior", MonitorID: fmt.Sprintf("monitor-%03d", index/2),
			Revision: fmt.Sprintf("generation-%d", index%2), OpenedAt: 10,
			Runs: map[string][]int64{"a": {1}, "b": {1}}, Closed: map[string]int64{"a": 2, "b": 2}, ResolvedAt: Ptr(int64(20))}
		left := Incident{ID: prefix + "-left", MonitorID: prior.MonitorID, Revision: prior.Revision, OpenedAt: 30,
			Runs: map[string][]int64{"a": {5}}, Predecessors: []string{prior.ID}}
		left.uncertainties = []incidentUncertainty{{Header: uncertainHeader(prior), ObserverID: "a", NodeID: "a", Run: 5, resolved: &prior}}
		right := left
		right.ID, right.Runs, right.uncertainties = prefix+"-right", map[string][]int64{"b": {5}}, nil
		blocked := left
		blocked.ID, blocked.Runs, blocked.uncertainties = prefix+"-blocked", map[string][]int64{"a": {1}}, nil
		// A real closed record in a different generation must not discharge
		// this unmatched predecessor just because its node/run values match.
		blocked.Predecessors = []string{fmt.Sprintf("scope-%03d-prior", index^1)}
		records = append(records, prior, left, right, blocked)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for iteration := range 4 {
		rng.Shuffle(len(records), func(a, b int) { records[a], records[b] = records[b], records[a] })
		before := map[string]string{}
		for _, record := range records {
			before[record.ID] = continuityKey(record)
		}
		got, reference := reconcileEpisodes(records), reconcileEpisodeScope(records)
		slices.SortFunc(reference, incidentOrder)
		if !equalJSON(got, reference) {
			t.Fatalf("partition changed original folding results in order %d", iteration)
		}
		for index := range got {
			if continuityKey(got[index]) != continuityKey(reference[index]) {
				t.Fatal("partition changed private uncertainty dependencies")
			}
		}
		for _, record := range records {
			if continuityKey(record) != before[record.ID] {
				t.Fatal("reconciliation mutated an original source")
			}
		}
	}
}
