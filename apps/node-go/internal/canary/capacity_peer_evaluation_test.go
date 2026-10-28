package canary

import "testing"

// Model the running scheduler with explicit ticks, without a listener or a
// background timer. Peer-page persistence must finish before lifecycle work;
// the next tick still opens and closes the same durable incident.
func TestCapacityPeerPagesDeferRunningEvaluationUntilTick(t *testing.T) {
	for _, running := range []bool{false, true} {
		name := "bootstrap"
		if running {
			name = "running"
		}
		t.Run(name, func(t *testing.T) {
			n := capacityReviewNode(t, "a", []string{"a", "b"})
			in := input([]string{"b"}, "http://127.0.0.1:9", "")
			m := groupMonitorFixture(t, n, in)
			n.started = running
			n.syncing = true // Suppress peer I/O even when saveEpisode resets nextSync.
			down := Sample{Observation: Observation{MonitorID: m.ID, NodeID: "b", State: "down", CheckedAt: n.now()},
				Revision: m.Revision, Sequence: 2, DownSince: Ptr(int64(1)), CheckGeneration: m.CheckGeneration}
			if err := n.mergeSnapshot(Snapshot{Observations: []Sample{down}}); err != nil {
				t.Fatal(err)
			}
			stored, err := loadAll[Sample](n.store, "samples")
			if err != nil || stored[m.ID+"/b"].Sequence != down.Sequence || n.samples[m.ID+"/b"].State != "down" {
				t.Fatal("peer page did not durably publish its observation before evaluation", err)
			}
			if running && len(n.episodes) != 0 || !running && len(n.episodes) != 1 {
				t.Fatal("peer page used the wrong lifecycle evaluation boundary", len(n.episodes))
			}
			n.tick()
			if n.fault != nil || len(n.episodes) != 1 {
				t.Fatal("next scheduler tick did not open the incident", n.fault)
			}
			var incidentID string
			for id := range n.episodes {
				incidentID = id
			}
			up := down
			up.State, up.Sequence, up.DownSince = "up", 3, nil
			if err := n.mergeSnapshot(Snapshot{Observations: []Sample{up}}); err != nil {
				t.Fatal(err)
			}
			if running && n.episodes[incidentID].Closed != nil {
				t.Fatal("running peer page evaluated recovery before its scheduler tick")
			}
			n.tick()
			closed := n.episodes[incidentID]
			if n.fault != nil || len(n.episodes) != 1 || closed.ResolvedAt == nil || closed.Closed["b"] != 3 {
				t.Fatal("next tick did not close the original incident", n.fault, closed)
			}
			persisted, err := loadAll[Incident](n.store, "incidentSources")
			if err != nil || persisted[incidentID].Closed["b"] != 3 {
				t.Fatal("scheduler recovery was not durable", err)
			}
		})
	}
}
