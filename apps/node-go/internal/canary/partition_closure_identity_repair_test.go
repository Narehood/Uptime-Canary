package canary

import (
	"fmt"
	"slices"
	"testing"
)

func pcirOriginal(id string, runs map[string][]int64) Incident {
	return Incident{ID: id, Aliases: []string{id}, MonitorID: "pcir-monitor", MonitorName: "Closure and identity", Revision: "pcir-generation", OpenedAt: 10, Runs: runs, Predecessors: []string{}}
}

// Seed closes Q, Q supplies C's missing predecessor closure, and only then can
// C prove O closed. Every one of those originals is embedded proof, rather
// than a standalone incident or a projected source/receipt identity.
func TestPartitionClosureIdentityRepairRecursiveEmbeddedClosureAcrossRestart(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			o := cr2Options(t, true)
			n := cr2New(t, o)
			seed := pcirOriginal("pcir-seed", map[string][]int64{"a": {1}, "b": {1}})
			seed.OpenedAt, seed.Closed, seed.ResolvedAt = 0, map[string]int64{"a": 2, "b": 2}, Ptr(int64(5))
			sh, sf, sl := partitionEvidence(seed)
			q := pcirOriginal("pcir-derived-predecessor", map[string][]int64{"a": {1}, "b": {1}})
			q.OpenedAt = 0
			q.uncertainties = []incidentUncertainty{{Header: uncertainHeader(sh), ObserverID: "a", NodeID: "a", Run: 1}}
			qh, qf, ql := partitionEvidence(q)
			old := pcirOriginal("pcir-open-boundary", map[string][]int64{"a": {3}, "b": {3}})
			oh, of, ol := partitionEvidence(old)
			closed := pcirOriginal("pcir-closed-witness", map[string][]int64{"a": {3}, "b": {3}})
			closed.Closed, closed.ResolvedAt, closed.Predecessors = map[string]int64{"a": 4, "b": 4}, Ptr(int64(20)), []string{q.ID}
			closed.uncertainties = []incidentUncertainty{{Header: uncertainHeader(qh), ObserverID: "a", NodeID: "a", Run: 3}}
			ch, cf, cl := partitionEvidence(closed)
			for _, observer := range []string{"a", "b"} {
				id := "pcir-later-" + observer
				branch := pcirOriginal(id, map[string][]int64{observer: {5}})
				branch.OpenedAt = 30
				branch.uncertainties = []incidentUncertainty{
					{Header: uncertainHeader(oh), ObserverID: observer, NodeID: observer, Run: 5},
					{Header: uncertainHeader(ch), ObserverID: observer, NodeID: observer, Run: 5},
				}
				h, facts, links := partitionEvidence(branch)
				cr2Apply(t, n, Snapshot{Incidents: []Incident{h}, IncidentFacts: facts, EvidenceLinks: links})
			}
			cr2Apply(t, n, Snapshot{Receipts: []Receipt{{NotificationView: NotificationView{ID: "pcir-proof-success", MonitorID: old.MonitorID, IncidentID: closed.ID, Kind: "down", NodeID: "b", SentAt: 12}}}})
			var held IncidentFact
			partial := []IncidentFact{}
			for _, f := range sf {
				if f.Kind == "run" && f.NodeID == "b" {
					held = f
				} else {
					partial = append(partial, f)
				}
			}
			if held.ID == "" {
				t.Fatal("fixture did not withhold the exact seed proof")
			}
			facts := append(append(append(partial, qf...), of...), cf...)
			links := append(append(append(sl, ql...), ol...), cl...)
			partitionComplete(t, n, facts, links, reverse)
			if len(n.episodes) != 2 || len(n.pendingIncidents) != 0 {
				t.Fatal("incomplete recursive predecessor proof granted closure")
			}
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			page := cr2Decode(t, n, Snapshot{IncidentFacts: []IncidentFact{held}})
			before := cr2State(n)
			if _, err := n.store.db.Exec(`CREATE TRIGGER pcir_closure_fail BEFORE INSERT ON records WHEN NEW.category='incidents' BEGIN SELECT RAISE(FAIL,'injected closure projection failure'); END`); err != nil {
				t.Fatal(err)
			}
			if err := n.applySnapshot(page); err == nil || cr2State(n) != before {
				t.Fatal("failed recursive completion published closure authority", err)
			}
			if _, err := n.store.db.Exec(`DROP TRIGGER pcir_closure_fail`); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := n.applySnapshot(page); err != nil {
					t.Fatal("identical recursive completion could not retry", err)
				}
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
				if len(node.episodes) != 1 || len(node.incidentSources) != 2 {
					t.Fatal("complete recursive proof failed to converge", len(node.episodes), len(node.incidentSources))
				}
				for _, i := range node.episodes {
					if !slices.Equal(episodeIDs(i), []string{"pcir-later-a", "pcir-later-b"}) || i.Closed != nil || len(node.forEpisode(i)) != 0 {
						t.Fatal("embedded originals gained closure, alias or receipt authority over later branches", i)
					}
				}
			}
		})
	}
}

func TestPartitionClosureIdentityRepairEmbeddedClosureKeepsScopeAndPredecessorBarriers(t *testing.T) {
	for _, barrier := range []string{"missing-predecessor", "foreign-monitor", "foreign-generation"} {
		t.Run(barrier, func(t *testing.T) {
			n := cr2New(t, cr2Options(t, false))
			old := pcirOriginal("pcir-scoped-old", map[string][]int64{"a": {3}, "b": {3}})
			oh, of, ol := partitionEvidence(old)
			closed := pcirOriginal("pcir-scoped-closed", map[string][]int64{"a": {3}, "b": {3}})
			closed.Closed, closed.ResolvedAt = map[string]int64{"a": 4, "b": 4}, Ptr(int64(20))
			if barrier == "missing-predecessor" {
				closed.Predecessors = []string{"pcir-unknown-predecessor"}
			} else if barrier == "foreign-monitor" {
				closed.MonitorID = "pcir-other-monitor"
			} else {
				closed.Revision = "pcir-other-generation"
			}
			ch, cf, cl := partitionEvidence(closed)
			for _, observer := range []string{"a", "b"} {
				branch := pcirOriginal("pcir-scoped-"+observer, map[string][]int64{observer: {5}})
				branch.OpenedAt = 30
				branch.uncertainties = []incidentUncertainty{{Header: uncertainHeader(oh), ObserverID: observer, NodeID: observer, Run: 5}}
				if barrier == "missing-predecessor" {
					branch.uncertainties = append(branch.uncertainties, incidentUncertainty{Header: uncertainHeader(ch), ObserverID: observer, NodeID: observer, Run: 5})
				}
				h, facts, links := partitionEvidence(branch)
				cr2Apply(t, n, Snapshot{Incidents: []Incident{h}, IncidentFacts: facts, EvidenceLinks: links})
			}
			if barrier != "missing-predecessor" {
				carrier := closed
				carrier.ID, carrier.Aliases, carrier.OpenedAt, carrier.Closed, carrier.ResolvedAt = "pcir-foreign-carrier", []string{"pcir-foreign-carrier"}, 30, nil, nil
				carrier.Runs = map[string][]int64{"a": {5}}
				carrier.uncertainties = []incidentUncertainty{{Header: uncertainHeader(ch), ObserverID: "a", NodeID: "a", Run: 5}}
				h, facts, links := partitionEvidence(carrier)
				cr2Apply(t, n, Snapshot{Incidents: []Incident{h}, IncidentFacts: facts, EvidenceLinks: links})
			}
			partitionComplete(t, n, append(of, cf...), append(ol, cl...), true)
			for _, id := range []string{"pcir-scoped-a", "pcir-scoped-b"} {
				i := n.episodes[id]
				if i.ID == "" || len(episodeIDs(i)) != 1 || i.Closed != nil {
					t.Fatal("embedded closure crossed its scope/predecessor barrier", barrier, i)
				}
				for _, u := range i.uncertainties {
					if u.Header.ID == old.ID && (u.resolved == nil || u.resolved.Closed != nil) {
						t.Fatal("unproved or foreign closure certified the old boundary")
					}
				}
			}
		})
	}
}

// References form an immutable older-root chain, while combining that root
// with its latest original creates a semantic dependency cycle A--B--A. The
// complete embedded closed witness must settle without dropping the reference,
// recursing indefinitely, or persisting proof-only aliases as original facts.
func TestPartitionClosureIdentityRepairOlderRootCycleSettles(t *testing.T) {
	o := cr2Options(t, true)
	n := cr2New(t, o)
	a := pcirOriginal("pcir-cycle-a", map[string][]int64{"a": {3}, "b": {3}})
	oldHeader, oldFacts, oldLinks := partitionEvidence(a)
	closed := pcirOriginal("pcir-cycle-closed", map[string][]int64{"a": {3}, "b": {3}})
	closed.Closed, closed.ResolvedAt = map[string]int64{"a": 4, "b": 4}, Ptr(int64(20))
	ch, cf, cl := partitionEvidence(closed)
	b := pcirOriginal("pcir-cycle-b", map[string][]int64{"a": {3}, "b": {3}})
	b.uncertainties = []incidentUncertainty{{Header: uncertainHeader(oldHeader), ObserverID: "b", NodeID: "b", Run: 3}}
	bh, bf, bl := partitionEvidence(b)
	a.uncertainties = []incidentUncertainty{
		{Header: uncertainHeader(bh), ObserverID: "a", NodeID: "a", Run: 3},
		{Header: uncertainHeader(ch), ObserverID: "a", NodeID: "a", Run: 3},
	}
	ah, af, al := partitionEvidence(a)
	cr2Apply(t, n, Snapshot{Incidents: []Incident{ah, bh}, IncidentFacts: append(append(oldFacts, af...), bf...), EvidenceLinks: append(append(oldLinks, al...), bl...)})
	if len(n.episodes) != 2 {
		t.Fatal("incomplete cyclic closure proof joined original sources")
	}
	for range 2 {
		partitionComplete(t, n, cf, cl, true)
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = cr2New(t, o)
	if len(n.episodes) != 1 || len(n.incidentSources) != 2 {
		t.Fatal("complete older-root dependency cycle did not settle")
	}
	for _, i := range n.episodes {
		if !slices.Equal(episodeIDs(i), []string{a.ID, b.ID}) {
			t.Fatal("proof-only closed identity escaped the cycle", episodeIDs(i))
		}
	}
	for _, stored := range n.incidentSources {
		full, complete, err := hydrateIncident(stored, n.incidentFacts, n.evidenceLinks)
		if err != nil || !complete || len(episodeIDs(full)) != 1 || full.Closed != nil {
			t.Fatal("cyclic proof overwrote original source provenance", full, err)
		}
	}
}

// Queue the later source before learning an older alias. It retains its own
// persisted identity through alias growth and the subsequent reversible split.
func TestPartitionClosureIdentityRepairLaterAttemptKeepsReceiptAndDestinationScope(t *testing.T) {
	for _, named := range []bool{false, true} {
		for _, acceptance := range []string{"old-source", "exact-source", "other-destination"} {
			t.Run(fmt.Sprintf("named=%v/%s", named, acceptance), func(t *testing.T) {
				r := webhook(t)
				n := cr2New(t, cr2Options(t, false))
				in := input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL)
				if named {
					channel, err := n.writeResource(resourceChannel, "pcir-channel", hookChannel("Exact queue identity", r.URL), false)
					if err != nil {
						t.Fatal(err)
					}
					in.WebhookURL, in.NotificationIDs = "", []string{channel.ID}
				}
				m, err := n.writeMonitor(in, nil, "pcir-queue-monitor", false)
				if err != nil {
					t.Fatal(err)
				}
				target := n.notificationTargets(m)[0]
				old := pcirOriginal("a-pcir-queue-old", map[string][]int64{"a": {1}, "b": {1}})
				old.MonitorID, old.Revision, old.OpenedAt, old.DeliveryTargets = m.ID, checkGeneration(m), n.now()-10000, []string{target.ID}
				later := pcirOriginal("z-pcir-queue-later", map[string][]int64{"a": {3}, "b": {3}})
				later.MonitorID, later.Revision, later.OpenedAt, later.DeliveryTargets = m.ID, checkGeneration(m), n.now()-1000, []string{target.ID}
				cr2Apply(t, n, Snapshot{Incidents: []Incident{later}})
				for _, node := range []string{"a", "b"} {
					n.samples[m.ID+"/"+node] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: node, State: "down", CheckedAt: n.now()}, Revision: m.Revision, CheckGeneration: m.CheckGeneration, Sequence: 3, DownSince: Ptr(int64(3))}
				}
				release := pcrHoldWorkers(n)
				n.mu.Lock()
				n.scheduleNotification(m, n.fresh(m))
				n.mu.Unlock()
				release()
				var queued attempt
				for _, a := range n.attempts {
					if a.Kind == "down" && a.IncidentID == later.ID {
						queued = a
					}
				}
				if queued.ID == "" {
					t.Fatal("fixture did not persist the later source's queued attempt")
				}
				cr2Apply(t, n, Snapshot{Incidents: []Incident{old}})
				captured := pcrOnlyEpisode(t, n)
				if len(episodeIDs(captured)) != 2 {
					t.Fatal("fixture did not grow the reversible captured aliases")
				}
				old.Closed, old.ResolvedAt = map[string]int64{"a": 2, "b": 2}, Ptr(n.now()-5000)
				receipt := Receipt{NotificationView: NotificationView{ID: "pcir-queue-accepted", MonitorID: m.ID, IncidentID: old.ID, Kind: "down", NodeID: "b", SentAt: n.now(), DestinationID: target.ID}}
				if acceptance != "old-source" {
					receipt.IncidentID = later.ID
				}
				if acceptance == "other-destination" {
					receipt.DestinationID = "pcir-other-destination"
				}
				cr2Apply(t, n, Snapshot{Incidents: []Incident{old}, Receipts: []Receipt{receipt}})
				if len(n.episodes) != 2 {
					t.Fatal("late closure did not split the captured alias projection")
				}
				n.syncing = true
				for range 8 {
					inflight := m.ID + "/down/" + target.ID
					n.sending[inflight] = true
					n.sendNotification(m, captured, queued, inflight, nil)
				}
				want := 1
				if acceptance == "exact-source" {
					want = 0
				}
				if r.count("down") != want {
					t.Fatal("captured sibling overrode exact attempt/destination acceptance", acceptance, r.all())
				}
				if want == 1 {
					body := r.all()[0]
					if body.IncidentID != later.ID || !slices.Equal(body.Incident.Aliases, []string{later.ID}) || body.Key != queued.ID || n.receipts[queued.ID].IncidentID != later.ID {
						t.Fatal("final worker changed stable identity or borrowed obsolete siblings", body)
					}
				}
			})
		}
	}
}

func TestPartitionClosureIdentityRepairAbsentExactSourceCannotSelectCapturedSibling(t *testing.T) {
	r := webhook(t)
	n := cr2New(t, cr2Options(t, false))
	m, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL), nil, "pcir-missing-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	later := pcirOriginal("pcir-only-current", map[string][]int64{"a": {3}, "b": {3}})
	later.MonitorID, later.Revision = m.ID, checkGeneration(m)
	cr2Apply(t, n, Snapshot{Incidents: []Incident{later}})
	for _, node := range []string{"a", "b"} {
		n.samples[m.ID+"/"+node] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: node, State: "down", CheckedAt: n.now()}, Revision: m.Revision, CheckGeneration: m.CheckGeneration, Sequence: 3, DownSince: Ptr(int64(3))}
	}
	captured := later
	captured.ID, captured.Aliases = "pcir-absent-source", []string{"pcir-absent-source", later.ID}
	a := attempt{ID: "pcir-persisted-absent-attempt", MonitorID: m.ID, IncidentID: captured.ID, Kind: "down", TargetRevision: n.notificationTargets(m)[0].Revision}
	n.attempts[a.ID] = a
	if err := put(n.store.db, "attempts", a.ID, a); err != nil {
		t.Fatal(err)
	}
	n.syncing = true
	n.sendNotification(m, captured, a, m.ID+"/down", nil)
	if r.count("") != 0 || len(n.receipts) != 0 {
		t.Fatal("absent exact source borrowed a captured sibling's fresh evidence", r.all())
	}
}
