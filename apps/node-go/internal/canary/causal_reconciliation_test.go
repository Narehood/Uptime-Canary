package canary

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func causalPermutations(records []Incident, visit func([]Incident)) {
	var walk func(int)
	walk = func(index int) {
		if index == len(records) {
			visit(append([]Incident{}, records...))
			return
		}
		for next := index; next < len(records); next++ {
			records[index], records[next] = records[next], records[index]
			walk(index + 1)
			records[index], records[next] = records[next], records[index]
		}
	}
	walk(0)
}

func causalHistory() (Incident, Incident, Incident) {
	prior := episode("z-prior", map[string][]int64{"east": {1}})
	prior.Closed, prior.ResolvedAt = map[string]int64{"east": 2, "west": 2, "third": 2}, Ptr(int64(200))
	sibling := episode("b-delayed", map[string][]int64{"west": {1}})
	sibling.OpenedAt, sibling.DeliveryTargets = 110, []string{"old-target"}
	current := episode("c-current", map[string][]int64{"east": {3}})
	current.OpenedAt, current.Predecessors = 300, []string{prior.ID}
	return prior, sibling, current
}

func causalCurrentIsIndependent(t *testing.T, records []Incident, current, prior, sibling Incident) {
	t.Helper()
	for _, record := range records {
		if slices.Contains(episodeIDs(record), current.ID) {
			if record.Closed != nil || intersects(episodeIDs(record), []string{prior.ID, sibling.ID}) || slices.Contains(record.DeliveryTargets, "old-target") {
				t.Fatalf("successor inherited predecessor identity, audience or closure: %+v", record)
			}
			return
		}
	}
	t.Fatal("lost successor")
}

func TestCausalDelayedBoundaryConvergesAcrossEveryPagePermutation(t *testing.T) {
	prior, sibling, current := causalHistory()
	openPrior := prior
	openPrior.Closed, openPrior.ResolvedAt = nil, nil
	causalPermutations([]Incident{prior, openPrior, sibling, current}, func(order []Incident) {
		var records []Incident
		for _, page := range order {
			records = reconcileEpisodes(append(records, page))
			for _, record := range records {
				if slices.Contains(episodeIDs(record), current.ID) {
					causalCurrentIsIndependent(t, records, current, prior, sibling)
				}
			}
		}
		if len(records) != 2 {
			t.Fatalf("old aliases failed to converge: %+v", records)
		}
		causalCurrentIsIndependent(t, records, current, prior, sibling)
		// A subsequent replay must be idempotent, including after alias deletion.
		replayed := reconcileEpisodes(append(append([]Incident{}, records...), prior, sibling, current))
		if !equalJSON(records, replayed) {
			t.Fatalf("complete replay changed established lifetimes: before=%+v after=%+v", records, replayed)
		}
	})
}

func TestCausalSharedRunBridgeCannotCarryOldReceiptAuthorityAcrossBoundary(t *testing.T) {
	prior, sibling, current := causalHistory()
	// A lagging partition can retain an old run while learning a current run.
	// Sharing that current run is insufficient to cross its unresolved boundary.
	bridge := episode("d-bridge", map[string][]int64{"west": {1}, "east": {3}})
	bridge.OpenedAt = 120
	causalPermutations([]Incident{prior, sibling, current, bridge}, func(order []Incident) {
		var records []Incident
		for _, page := range order {
			records = reconcileEpisodes(append(records, page))
			for _, record := range records {
				if slices.Contains(episodeIDs(record), current.ID) {
					causalCurrentIsIndependent(t, records, current, prior, sibling)
					if slices.Contains(episodeIDs(record), bridge.ID) {
						t.Fatal("mixed old/current run bridge acquired successor identity")
					}
				}
			}
		}
		fresh := []Sample{{Observation: Observation{NodeID: "east", State: "down", CheckedAt: 400}, DownSince: Ptr(int64(3)), Sequence: 3}}
		active := activeEpisode(records, fresh)
		if active == nil || !slices.Contains(episodeIDs(*active), current.ID) {
			t.Fatalf("fresh successor run selected old bridging episode: %+v", active)
		}
	})
}

func TestCausalUnequalFrontiersNeedPositiveClosureEvidence(t *testing.T) {
	prior, _, current := causalHistory()
	partition := episode("partition", map[string][]int64{"west": {3}})
	partition.OpenedAt = 310
	if len(reconcileEpisodes([]Incident{current, partition})) != 2 {
		t.Fatal("missing predecessor treated as permission to alias")
	}
	causalPermutations([]Incident{prior, current, partition}, func(order []Incident) {
		var records []Incident
		for _, page := range order {
			records = reconcileEpisodes(append(records, page))
		}
		if len(records) != 2 {
			t.Fatalf("compatible partition failed to converge after boundary: %+v", records)
		}
		for _, record := range records {
			if slices.Contains(episodeIDs(record), current.ID) && !slices.Contains(episodeIDs(record), partition.ID) {
				t.Fatal("legitimate successor partition alias lost")
			}
		}
	})
	partition.Runs = map[string][]int64{"west": {1, 3}}
	if len(reconcileEpisodes([]Incident{prior, current, partition})) != 3 {
		t.Fatal("mixed old/new runs crossed known predecessor closure")
	}
	partition.Runs = map[string][]int64{"unknown": {3}}
	if len(reconcileEpisodes([]Incident{prior, current, partition})) != 3 {
		t.Fatal("absent closure frontier treated as positive causal evidence")
	}
}

func TestCausalActiveSelectionRequiresCompatibleFreshRuns(t *testing.T) {
	prior, sibling, current := causalHistory()
	fresh := Sample{Observation: Observation{NodeID: "east", State: "down", CheckedAt: 400}, DownSince: Ptr(int64(3)), Sequence: 4}
	causalPermutations([]Incident{prior, sibling, current}, func(records []Incident) {
		active := activeEpisode(records, []Sample{fresh})
		if active == nil || active.ID != current.ID {
			t.Fatalf("selected an unrelated open incident: %+v", active)
		}
		if activeEpisode(records, nil).ID != current.ID {
			t.Fatal("healthy selection ignored causal latest incident")
		}
	})
	if activeEpisode([]Incident{sibling}, []Sample{fresh}) != nil {
		t.Fatal("extended old sibling without a matching fresh run")
	}
	current.Runs["west"] = []int64{5}
	older := Sample{Observation: Observation{NodeID: "west", State: "down", CheckedAt: 400}, DownSince: Ptr(int64(3)), Sequence: 4}
	if activeEpisode([]Incident{prior, current}, []Sample{fresh, older}) != nil {
		t.Fatal("matching east run authorized incompatible older west run")
	}
	current.Runs["west"] = []int64{3, 5}
	if activeEpisode([]Incident{prior, current}, []Sample{fresh, older}) != nil {
		t.Fatal("old matching run overrode the same node's newer run evidence")
	}
	current.Runs = map[string][]int64{"east": {3}}
	if activeEpisode([]Incident{current}, []Sample{fresh, older}) != nil {
		t.Fatal("novel run extended incident across unresolved predecessor")
	}
	if activeEpisode([]Incident{prior, current}, []Sample{fresh, older}) == nil {
		t.Fatal("positive closure evidence did not allow fresh run extension")
	}
}

func TestCausalFreshSuccessorBeforePredecessorPageSurvivesRestart(t *testing.T) {
	for _, successorFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(successorFirst), func(t *testing.T) {
			r := webhook(t)
			o := Options{NodeID: "a", DBPath: filepath.Join(t.TempDir(), "causal.db"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Peers: []Peer{{ID: "b", URL: "http://127.0.0.1:1"}}, NotificationDelayMs: Ptr(int64(0))}
			n, err := New(o)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = n.Stop() })
			m, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL), nil, "causal-monitor", false)
			if err != nil {
				t.Fatal(err)
			}
			prior := episode("z-prior", map[string][]int64{"a": {1}})
			prior.MonitorID, prior.MonitorName, prior.Revision, prior.OpenedAt = m.ID, m.Name, m.Revision, n.now()-10000
			prior.Closed, prior.ResolvedAt = map[string]int64{"a": 2, "b": 2}, Ptr(n.now()-9000)
			sibling := prior
			sibling.ID, sibling.Aliases, sibling.Runs = "b-old", []string{"b-old"}, map[string][]int64{"b": {1}}
			sibling.Closed, sibling.ResolvedAt = nil, nil
			current := prior
			current.ID, current.Aliases, current.Runs = "c-current", []string{"c-current"}, map[string][]int64{"a": {3}}
			current.OpenedAt, current.Closed, current.ResolvedAt, current.Predecessors = n.now()-1000, nil, nil, []string{prior.ID}
			oldReceipt := Receipt{NotificationView: NotificationView{ID: "old-receipt", MonitorID: m.ID, IncidentID: sibling.ID, Kind: "down", NodeID: "b", SentAt: prior.OpenedAt + 100}, Slot: 0}
			oldPage := Snapshot{Incidents: []Incident{sibling}, Intents: []NotificationIntent{deliveryIntent(m.ID, sibling.ID, "")}, Receipts: []Receipt{oldReceipt}}
			if successorFirst {
				reviewRound3Merge(t, n, Snapshot{Incidents: []Incident{current}})
				reviewRound3Merge(t, n, oldPage)
			} else {
				reviewRound3Merge(t, n, oldPage)
				reviewRound3Merge(t, n, Snapshot{Incidents: []Incident{current}})
			}
			// Unlike the original R3 fixture, run local evaluation while the
			// predecessor is still absent. Repeated ticks must keep extending
			// the fresh successor rather than whichever open map entry is last.
			for range 8 {
				n.mu.Lock()
				n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: n.now(), Message: "current"}, Sequence: 3, Revision: m.Revision, DownSince: Ptr(int64(3))}
				n.samples[m.ID+"/b"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "b", State: "up", CheckedAt: n.now(), Message: "healthy"}, Sequence: 4, Revision: m.Revision}
				n.evaluate()
				n.mu.Unlock()
				n.wg.Wait()
			}
			if r.count("down") != 1 || len(n.episodes) != 2 || !slices.Equal(n.episodes[sibling.ID].Runs["b"], []int64{1}) || len(n.episodes[sibling.ID].Runs["a"]) != 0 {
				t.Fatal("partial history changed the old sibling or lost the new down", n.episodes, r.all())
			}
			reviewRound3Merge(t, n, Snapshot{Incidents: []Incident{prior}})
			n.wg.Wait()
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n, err = New(o)
			if err != nil {
				t.Fatal(err)
			}
			reviewRound3Merge(t, n, Snapshot{Incidents: []Incident{prior, sibling, current}})
			n.wg.Wait()
			latest := n.episodes[current.ID]
			if latest.ID != current.ID || latest.Closed != nil || len(n.forEpisode(latest)) != 1 || n.forEpisode(latest)[0].ID == oldReceipt.ID || r.count("down") != 1 {
				t.Fatal("restart or complete replay mixed old receipt into successor", latest, n.forEpisode(latest), r.all())
			}
		})
	}
}

func TestCausalIntentDecoderRejectsAmbiguousAuthority(t *testing.T) {
	valid := `{"id":"fact","monitorId":"monitor","incidentId":"incident","destinationId":""}`
	var fact NotificationIntent
	if err := json.Unmarshal([]byte(valid), &fact); err != nil || fact.DestinationID != "" {
		t.Fatal("explicit legacy destination rejected", err)
	}
	for _, field := range []string{"id", "monitorId", "incidentId", "destinationId"} {
		for _, replacement := range []string{"null", "1", "true", "[]", "{}"} {
			raw := strings.Replace(valid, fmt.Sprintf(`"%s":%q`, field, map[string]string{"id": "fact", "monitorId": "monitor", "incidentId": "incident", "destinationId": ""}[field]), fmt.Sprintf(`"%s":%s`, field, replacement), 1)
			if json.Unmarshal([]byte(raw), &fact) == nil {
				t.Fatalf("accepted non-string %s: %s", field, raw)
			}
		}
	}
	for _, raw := range []string{
		`null`, `[]`, `{}`,
		`{"id":"fact","monitorId":"monitor","incidentId":"incident"}`,
		strings.Replace(valid, `"destinationId"`, `"DestinationId"`, 1),
		strings.Replace(valid, `"destinationId"`, `"DESTINATIONID"`, 1),
		strings.Replace(valid, `"destinationId":""`, `"destinationId":"","destinationId":""`, 1),
		strings.Replace(valid, `"destinationId":""`, `"destinationId":"","destination\u0049d":""`, 1),
		strings.Replace(valid, `"destinationId":""`, `"destinationId":"","unknown":""`, 1),
	} {
		before := fact
		if json.Unmarshal([]byte(raw), &fact) == nil || fact != before {
			t.Fatalf("malformed intent accepted or changed receiver: %s", raw)
		}
	}
}
