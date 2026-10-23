package canary

import (
	"slices"
	"testing"
)

func episode(id string, runs map[string][]int64) Incident {
	return Incident{ID: id, Aliases: []string{id}, MonitorID: "monitor", MonitorName: "Service", Revision: "rev", Runs: runs, OpenedAt: 100, Predecessors: []string{}}
}
func TestConcurrentEpisodesAndCompletedPartitions(t *testing.T) {
	a, b := episode("a", map[string][]int64{"east": {1}}), episode("b", map[string][]int64{"west": {1}})
	if !equivalent(a, b) {
		t.Fatal("Concurrent episodes must merge")
	}
	if !slices.Equal(combine(a, b).Aliases, []string{"a", "b"}) {
		t.Fatal("Aliases lost")
	}
	a.ResolvedAt = Ptr(int64(200))
	a.Closed = map[string]int64{"east": 2, "west": 2}
	b.ResolvedAt = Ptr(int64(220))
	b.Closed = map[string]int64{"east": 2, "west": 2}
	result := reconcileEpisodes([]Incident{a, b})
	if len(result) != 1 || *result[0].ResolvedAt != 200 {
		t.Fatal(result)
	}
}
func TestDelayedSiblingDoesNotAbsorbLaterOutage(t *testing.T) {
	prior := episode("prior", map[string][]int64{"east": {1}})
	prior.ResolvedAt = Ptr(int64(200))
	prior.Closed = map[string]int64{"east": 2, "west": 2}
	sibling := episode("late-sibling", map[string][]int64{"west": {1}})
	sibling.OpenedAt = 110
	newer := episode("new", map[string][]int64{"east": {3}})
	newer.OpenedAt = 300
	newer.Predecessors = []string{"prior"}
	for _, ordering := range [][]Incident{{newer, sibling, prior}, {prior, newer, sibling}, {sibling, prior, newer}} {
		result := reconcileEpisodes(ordering)
		if len(result) != 2 {
			t.Fatalf("Wrong episodes: %+v", result)
		}
		for _, i := range result {
			if i.ID == "new" && i.Closed != nil {
				t.Fatal("Late sibling closed new episode")
			}
		}
	}
	newer.Closed = map[string]int64{"east": 4, "west": 4}
	newer.ResolvedAt = Ptr(int64(400))
	if equivalent(prior, newer) {
		t.Fatal("Successive completed episodes merged")
	}
}
