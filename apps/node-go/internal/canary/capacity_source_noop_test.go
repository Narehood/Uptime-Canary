package canary

import "testing"

// An original whose ID is hidden by an alias projection must still have a
// no-op path. Inject storage loss after settling the source: unchanged state
// requires no transaction, while missing local provenance must attempt one.
func TestCapacityOriginalSourceNoopPreservesLocalObligations(t *testing.T) {
	for _, change := range []string{"none", "missing-marker", "new-run", "new-uncertainty"} {
		t.Run(change, func(t *testing.T) {
			n := capacityReviewNode(t, "a", []string{"a", "b"})
			created := capacityReviewCreate(t, n, "/api/monitors", map[string]any{
				"name": "Source no-op", "type": "http", "target": "http://127.0.0.1:1/service",
				"intervalMs": 1000, "timeoutMs": 500, "nodeIds": []string{"a", "b"}, "enabled": true,
				"webhookUrl": "http://127.0.0.1:1/webhook",
			})
			m := n.current(created["id"].(string))
			n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: n.now()},
				Revision: m.Revision, Sequence: 2, DownSince: Ptr(int64(1))}
			local := Incident{ID: "z-local-source", Aliases: []string{"z-local-source"}, MonitorID: m.ID, MonitorName: m.Name,
				Revision: checkGeneration(*m), OpenedAt: n.now(), Runs: map[string][]int64{"a": {1}}, DeliveryTargets: []string{""}}
			remote := local
			remote.ID, remote.Aliases, remote.Runs = "a-remote-source", []string{"a-remote-source"}, map[string][]int64{"b": {1}}
			if err := n.applySnapshot(Snapshot{Incidents: []Incident{local, remote}}); err != nil {
				t.Fatal(err)
			}
			if _, exists := n.episodes[local.ID]; exists || !n.hasLocalIncidentDelivery(local, "") {
				t.Fatal("fixture needs a hidden original with an exact local delivery marker")
			}
			stored, complete, err := hydrateIncident(n.incidentSources[local.ID], n.incidentFacts, n.evidenceLinks)
			if err != nil || !complete {
				t.Fatal("missing original", err)
			}
			switch change {
			case "missing-marker":
				delete(n.localIncidentDeliveries, deliveryIntent(m.ID, local.ID, "").ID)
			case "new-run":
				stored.Runs["a"] = []int64{1, 2}
			case "new-uncertainty":
				stored.uncertainties = []incidentUncertainty{{Header: uncertainHeader(n.incidentSources[remote.ID]), ObserverID: "a", NodeID: "a", Run: 1}}
			}
			if err := n.store.db.Close(); err != nil {
				t.Fatal(err)
			}
			n.saveEpisode(stored)
			if change == "none" && n.fault != nil {
				t.Fatal("unchanged retained original attempted a redundant transaction", n.fault)
			}
			if change != "none" && n.fault == nil {
				t.Fatal("changed evidence or missing local obligation was mistaken for a no-op")
			}
		})
	}
}
