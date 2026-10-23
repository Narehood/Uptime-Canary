package canary

import (
	"fmt"
	"slices"
	"testing"
)

type csrHistory struct {
	headers []Incident
	facts   []IncidentFact
	links   []EvidenceLink
	seed    Incident
}

func csrOriginal(id string, run, opened int64) Incident {
	return Incident{ID: id, Aliases: []string{id}, MonitorID: "csr-monitor", MonitorName: "Mixed closure proof", Revision: "csr-generation", OpenedAt: opened, Runs: map[string][]int64{"a": {run}, "b": {run}}, Predecessors: []string{}}
}

// Q and R are standalone-only predecessor identities. Seed closes Q; embedded
// C then closes R in proof memory; R permits embedded K to close O. No header
// names Q or R, so their private derivations cannot rely on embedded copies.
func csrMixedHistory(seedEmbedded bool, barrier string) csrHistory {
	history := csrHistory{}
	add := func(i Incident, standalone bool) Incident {
		h, facts, links := partitionEvidence(i)
		history.facts = append(history.facts, facts...)
		history.links = append(history.links, links...)
		if standalone {
			history.headers = append(history.headers, h)
		}
		return h
	}
	seed := csrOriginal("csr-seed", 1, 0)
	seed.Closed, seed.ResolvedAt = map[string]int64{"a": 2, "b": 2}, Ptr(int64(5))
	switch barrier {
	case "foreign-monitor":
		seed.MonitorID = "csr-other-monitor"
	case "foreign-generation":
		seed.Revision = "csr-other-generation"
	case "uncovered-seed-run":
		seed.Runs, seed.Closed = map[string][]int64{"a": {1}}, map[string]int64{"a": 2}
	case "late-predecessor-closure":
		seed.ResolvedAt = Ptr(int64(15))
	}
	history.seed = seed
	sh := add(seed, !seedEmbedded)
	q := csrOriginal("csr-predecessor-q", 1, 0)
	if seedEmbedded {
		q.uncertainties = []incidentUncertainty{{Header: uncertainHeader(sh), ObserverID: "a", NodeID: "a", Run: 1}}
	}
	if barrier == "closed-uncertainty-phase" {
		phase := csrOriginal("csr-embedded-phase", 3, 10)
		// A partial-node frontier obstructs Q/seed equivalence without
		// independently supplying a complete closure witness for R.
		phase.Runs, phase.Closed, phase.ResolvedAt = map[string][]int64{"a": {3}}, map[string]int64{"a": 4}, Ptr(int64(20))
		ph := add(phase, false)
		q.uncertainties = []incidentUncertainty{{Header: uncertainHeader(ph), ObserverID: "a", NodeID: "a", Run: 1}}
	}
	add(q, true)
	r := csrOriginal("csr-predecessor-r", 3, 10)
	if barrier == "mixed-middle-run" {
		r.Runs["b"] = []int64{5}
	}
	add(r, true)
	closedMiddle := csrOriginal("csr-embedded-c", 3, 10)
	closedMiddle.Closed, closedMiddle.ResolvedAt, closedMiddle.Predecessors = map[string]int64{"a": 4, "b": 4}, Ptr(int64(20)), []string{q.ID}
	if barrier == "missing-middle-predecessor" {
		closedMiddle.Predecessors = append(closedMiddle.Predecessors, "csr-unknown-predecessor")
	}
	ch := add(closedMiddle, false)
	old := csrOriginal("csr-embedded-o", 5, 30)
	if barrier == "early-top-lifetime" {
		old.OpenedAt = 19
	}
	oh := add(old, false)
	closedTop := csrOriginal("csr-embedded-k", 5, 30)
	closedTop.Closed, closedTop.ResolvedAt, closedTop.Predecessors = map[string]int64{"a": 6, "b": 6}, Ptr(int64(40)), []string{r.ID}
	closedTop.uncertainties = []incidentUncertainty{{Header: uncertainHeader(ch), ObserverID: "a", NodeID: "a", Run: 5}}
	kh := add(closedTop, false)
	for _, observer := range []string{"a", "b"} {
		later := csrOriginal("csr-later-"+observer, 7, 50)
		later.Runs = map[string][]int64{observer: {7}}
		later.uncertainties = []incidentUncertainty{
			{Header: uncertainHeader(oh), ObserverID: observer, NodeID: observer, Run: 7},
			{Header: uncertainHeader(kh), ObserverID: observer, NodeID: observer, Run: 7},
		}
		add(later, true)
	}
	return history
}

func csrAssertSourceIsolation(t *testing.T, n *Node) {
	t.Helper()
	for _, source := range n.incidentSources {
		full, complete, err := hydrateIncident(source, n.incidentFacts, n.evidenceLinks)
		if err != nil || !complete || !slices.Equal(episodeIDs(full), []string{source.ID}) {
			t.Fatal("closure context changed original-source provenance", full, err)
		}
		if source.ID != "csr-seed" && full.Closed != nil {
			t.Fatal("proof-memory closure was persisted on an open original", full)
		}
	}
	for _, id := range []string{"csr-later-a", "csr-later-b"} {
		later := psrvProjected(t, n, id)
		if later.Closed != nil || intersects(episodeIDs(later), []string{"csr-seed", "csr-predecessor-q", "csr-predecessor-r", "csr-embedded-c", "csr-embedded-o", "csr-embedded-k"}) || len(n.forEpisode(later)) != 0 || n.hasDeliveryIntent(later, "csr-proof-destination") {
			t.Fatal("later lifetime gained proof aliases, closure, intent or receipt authority", later)
		}
	}
}

func TestClosureStandaloneRepairMixedFixedPointRollbackRestart(t *testing.T) {
	for _, seedEmbedded := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("embeddedSeed=%v/reverse=%v", seedEmbedded, reverse), func(t *testing.T) {
				o := cr2Options(t, true)
				n := cr2New(t, o)
				history := csrMixedHistory(seedEmbedded, "")
				cr2Apply(t, n, Snapshot{Incidents: history.headers, Receipts: []Receipt{
					{NotificationView: NotificationView{ID: "csr-seed-down", MonitorID: history.seed.MonitorID, IncidentID: history.seed.ID, Kind: "down", NodeID: "b", SentAt: 2}},
					{NotificationView: NotificationView{ID: "csr-proof-down", MonitorID: "csr-monitor", IncidentID: "csr-embedded-k", Kind: "down", NodeID: "b", SentAt: 32}},
				}, Intents: []NotificationIntent{deliveryIntent("csr-monitor", "csr-embedded-k", "csr-proof-destination")}})
				var held IncidentFact
				partial := []IncidentFact{}
				for _, f := range history.facts {
					if f.IncidentID == history.seed.ID && f.Kind == "run" && f.NodeID == "b" {
						held = f
					} else {
						partial = append(partial, f)
					}
				}
				if held.ID == "" {
					t.Fatal("missing withheld exact seed fact")
				}
				partitionComplete(t, n, partial, history.links, reverse)
				if slices.Contains(episodeIDs(psrvProjected(t, n, "csr-later-a")), "csr-later-b") {
					t.Fatal("incomplete seed proved the mixed predecessor chain")
				}
				csrAssertSourceIsolation(t, n)
				if err := n.Stop(); err != nil {
					t.Fatal(err)
				}
				n = cr2New(t, o)
				page := cr2Decode(t, n, Snapshot{IncidentFacts: []IncidentFact{held}})
				before, inputBefore := cr2State(n), contentID(page)
				if _, err := n.store.db.Exec(`CREATE TRIGGER csr_projection_fail BEFORE INSERT ON records WHEN NEW.category='incidents' BEGIN SELECT RAISE(FAIL,'injected mixed closure failure'); END`); err != nil {
					t.Fatal(err)
				}
				if err := n.applySnapshot(page); err == nil || cr2State(n) != before || contentID(page) != inputBefore {
					t.Fatal("failed transaction published mixed closure proof or changed retry input", err)
				}
				if _, err := n.store.db.Exec(`DROP TRIGGER csr_projection_fail`); err != nil {
					t.Fatal(err)
				}
				// Reopening before the retry checks the rolled-back durable state,
				// rather than relying only on the unchanged in-memory maps.
				if err := n.Stop(); err != nil {
					t.Fatal(err)
				}
				n = cr2New(t, o)
				if _, ok := n.incidentFacts[held.ID]; ok || slices.Contains(episodeIDs(psrvProjected(t, n, "csr-later-a")), "csr-later-b") {
					t.Fatal("rolled-back seed survived reopening")
				}
				for range 2 {
					if err := n.applySnapshot(page); err != nil {
						t.Fatal("identical seed completion could not retry", err)
					}
				}
				settled := cr2State(n)
				cr2Apply(t, n, Snapshot{IncidentFacts: []IncidentFact{held}})
				if cr2State(n) != settled {
					t.Fatal("identical complete seed replay changed the fixed point")
				}
				if err := n.Stop(); err != nil {
					t.Fatal(err)
				}
				n = cr2New(t, o)
				peer := cr2New(t, cr2Options(t, false))
				pages := causalCapacityPages(t, n)
				slices.Reverse(pages)
				for _, p := range pages {
					cr2Apply(t, peer, p)
				}
				for _, node := range []*Node{n, peer} {
					later := psrvProjected(t, node, "csr-later-a")
					if !slices.Equal(episodeIDs(later), []string{"csr-later-a", "csr-later-b"}) || len(node.pendingIncidents) != 0 {
						t.Fatal("complete mixed closure chain failed after restart or peer replay", episodeIDs(later))
					}
					for _, u := range later.uncertainties {
						if u.resolved == nil || u.resolved.Closed == nil {
							t.Fatal("mixed closure dependency did not settle", u.Header.ID)
						}
					}
					csrAssertSourceIsolation(t, node)
				}
			})
		}
	}
}

func TestClosureStandaloneRepairMixedFixedPointKeepsBarriers(t *testing.T) {
	for _, barrier := range []string{"foreign-monitor", "foreign-generation", "uncovered-seed-run", "late-predecessor-closure", "missing-middle-predecessor", "mixed-middle-run", "early-top-lifetime", "closed-uncertainty-phase"} {
		t.Run(barrier, func(t *testing.T) {
			o := cr2Options(t, true)
			n := cr2New(t, o)
			history := csrMixedHistory(false, barrier)
			cr2Apply(t, n, Snapshot{Incidents: history.headers})
			partitionComplete(t, n, history.facts, history.links, true)
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			if slices.Contains(episodeIDs(psrvProjected(t, n, "csr-later-a")), "csr-later-b") || len(n.pendingIncidents) != 0 {
				t.Fatal("mixed closure proof bypassed a complete authority barrier", barrier)
			}
			csrAssertSourceIsolation(t, n)
		})
	}
}
