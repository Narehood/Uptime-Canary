package canary

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"
)

func cirOriginal(id string, runs map[string][]int64) Incident {
	return Incident{ID: id, Aliases: []string{id}, MonitorID: "cir-monitor", MonitorName: "Independent closure review", Revision: "cir-generation", OpenedAt: 10, Runs: runs, Predecessors: []string{}}
}

// Three current originals refer to older roots, which creates a semantic
// cycle when those roots are joined to their current originals. Two distinct
// roots of A are referenced at once. Missing one seed fact must remain missing
// proof; completing it must settle in every finite page permutation.
func TestClosureIdentityReviewMultiRootCycleWithheldSeed(t *testing.T) {
	for schedule := 0; schedule < 6; schedule++ {
		t.Run(fmt.Sprint(schedule), func(t *testing.T) {
			o := cr2Options(t, true)
			n := cr2New(t, o)
			a := cirOriginal("cir-cycle-a", map[string][]int64{"a": {3}, "b": {3}})
			oldA, oldFacts, oldLinks := partitionEvidence(a)
			seed := cirOriginal("cir-cycle-seed", map[string][]int64{"a": {3}, "b": {3}})
			seed.Closed, seed.ResolvedAt = map[string]int64{"a": 4, "b": 4}, Ptr(int64(20))
			sh, sf, sl := partitionEvidence(seed)
			b := cirOriginal("cir-cycle-b", map[string][]int64{"a": {3}, "b": {3}})
			b.uncertainties = []incidentUncertainty{{Header: uncertainHeader(oldA), ObserverID: "b", NodeID: "b", Run: 3}}
			bh, bf, bl := partitionEvidence(b)
			c := cirOriginal("cir-cycle-c", map[string][]int64{"a": {3}, "b": {3}})
			c.uncertainties = []incidentUncertainty{{Header: uncertainHeader(bh), ObserverID: "a", NodeID: "a", Run: 3}}
			ch, cf, cl := partitionEvidence(c)
			a.uncertainties = []incidentUncertainty{
				{Header: uncertainHeader(ch), ObserverID: "a", NodeID: "a", Run: 3},
				{Header: uncertainHeader(sh), ObserverID: "a", NodeID: "a", Run: 3},
			}
			ah, af, al := partitionEvidence(a)
			cr2Apply(t, n, Snapshot{Incidents: []Incident{ah, bh, ch}, IncidentFacts: append(append(append(oldFacts, af...), bf...), cf...), EvidenceLinks: append(append(append(oldLinks, al...), bl...), cl...)})
			for _, observer := range []string{"a", "b"} {
				later := cirOriginal("cir-cycle-later-"+observer, map[string][]int64{observer: {5}})
				later.OpenedAt = 30
				for _, h := range []Incident{oldA, ah, ch} {
					later.uncertainties = append(later.uncertainties, incidentUncertainty{Header: uncertainHeader(h), ObserverID: observer, NodeID: observer, Run: 5})
				}
				h, facts, links := partitionEvidence(later)
				cr2Apply(t, n, Snapshot{Incidents: []Incident{h}, IncidentFacts: facts, EvidenceLinks: links})
			}
			var held IncidentFact
			pages := []Snapshot{}
			for _, f := range sf {
				if f.Kind == "run" && f.NodeID == "b" {
					held = f
				} else {
					pages = append(pages, Snapshot{IncidentFacts: []IncidentFact{f}})
				}
			}
			for _, link := range sl {
				pages = append(pages, Snapshot{EvidenceLinks: []EvidenceLink{link}})
			}
			if held.ID == "" || oldA.EvidenceRoot == ah.EvidenceRoot {
				t.Fatal("fixture lacks its withheld seed or distinct A roots")
			}
			slices.SortFunc(pages, func(a, b Snapshot) int { return stringsCompare(contentID(a), contentID(b)) })
			rng := rand.New(rand.NewSource(int64(60221022 + schedule)))
			rng.Shuffle(len(pages), func(i, j int) { pages[i], pages[j] = pages[j], pages[i] })
			for _, page := range pages {
				for range 2 {
					cr2Apply(t, n, page)
				}
			}
			for _, i := range n.episodes {
				if i.Closed != nil {
					t.Fatal("cyclic roots manufactured closure without the exact seed fact")
				}
			}
			if slices.Contains(episodeIDs(psrvProjected(t, n, "cir-cycle-later-a")), "cir-cycle-later-b") {
				t.Fatal("unclosed cyclic boundary joined independent later branches")
			}
			cr2Apply(t, n, Snapshot{IncidentFacts: []IncidentFact{held}, Receipts: []Receipt{{NotificationView: NotificationView{ID: "cir-cycle-proof-receipt", MonitorID: a.MonitorID, IncidentID: seed.ID, Kind: "down", NodeID: "b", SentAt: 12}}}})
			before := cr2State(n)
			cr2Apply(t, n, Snapshot{IncidentFacts: []IncidentFact{held}})
			if cr2State(n) != before {
				t.Fatal("identical seed replay changed the settled fixed point")
			}
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			peer := cr2New(t, cr2Options(t, false))
			wirePages := causalCapacityPages(t, n)
			slices.Reverse(wirePages)
			for _, page := range wirePages {
				cr2Apply(t, peer, page)
			}
			for _, node := range []*Node{n, peer} {
				later := psrvProjected(t, node, "cir-cycle-later-a")
				old := psrvProjected(t, node, a.ID)
				if len(node.episodes) != 2 || !slices.Equal(episodeIDs(old), []string{a.ID, b.ID, c.ID}) || !slices.Equal(episodeIDs(later), []string{"cir-cycle-later-a", "cir-cycle-later-b"}) || later.Closed != nil || len(node.forEpisode(later)) != 0 || len(node.forEpisode(old)) != 0 {
					t.Fatal("complete multi-root cycle did not settle with isolated proof identity", len(node.episodes), episodeIDs(old), episodeIDs(later))
				}
				for _, u := range later.uncertainties {
					if u.resolved == nil || u.resolved.Closed == nil {
						t.Fatal("complete older/current root failed to acquire private closure proof", u.Header.ID)
					}
				}
				for _, source := range node.incidentSources {
					full, complete, err := hydrateIncident(source, node.incidentFacts, node.evidenceLinks)
					if err != nil || !complete || len(episodeIDs(full)) != 1 || full.Closed != nil {
						t.Fatal("derived cyclic closure overwrote an original source", full, err)
					}
				}
			}
		})
	}
}

// Follow-ups are queued by the real scheduler under the identity of the
// accepted down. That identity must survive canonical alias growth, a split,
// and reopening the latest store. Old-sibling/different-destination follow-up
// receipts must neither authorize nor suppress this exact later attempt.
func TestClosureIdentityReviewQueuedFollowupsAfterAliasGrowthAndSplit(t *testing.T) {
	for _, kind := range []string{"repeat", "recovery"} {
		for _, named := range []bool{false, true} {
			for _, acceptance := range []string{"old-followup", "exact-followup", "other-destination", "unknown-peer"} {
				t.Run(fmt.Sprintf("%s/named=%v/%s", kind, named, acceptance), func(t *testing.T) {
					r := webhook(t)
					o := cr2Options(t, true)
					o.Now = func() int64 { return 1_800_000_000_000 }
					n := cr2New(t, o)
					in := input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL)
					in.RepeatAfterMs = Ptr(int64(1000))
					if named {
						channel, err := n.writeResource(resourceChannel, "cir-followup-channel", hookChannel("Follow-up identity", r.URL), false)
						if err != nil {
							t.Fatal(err)
						}
						in.WebhookURL, in.NotificationIDs = "", []string{channel.ID}
					}
					m, err := n.writeMonitor(in, nil, "cir-followup-monitor", false)
					if err != nil {
						t.Fatal(err)
					}
					target := n.notificationTargets(m)[0]
					later := cirOriginal("z-cir-followup-later", map[string][]int64{"a": {3}, "b": {3}})
					later.MonitorID, later.Revision, later.OpenedAt = m.ID, checkGeneration(m), n.now()-10000
					if kind == "recovery" {
						later.Closed, later.ResolvedAt = map[string]int64{"a": 4, "b": 4}, Ptr(n.now()-1000)
					}
					down := Receipt{NotificationView: NotificationView{ID: "cir-followup-down", MonitorID: m.ID, IncidentID: later.ID, DestinationID: target.ID, Kind: "down", NodeID: "b", SentAt: n.now() - 9000}}
					cr2Apply(t, n, Snapshot{Incidents: []Incident{later}, Receipts: []Receipt{down}})
					for _, node := range []string{"a", "b"} {
						s := Sample{Observation: Observation{MonitorID: m.ID, NodeID: node, State: "down", CheckedAt: n.now()}, Revision: m.Revision, CheckGeneration: m.CheckGeneration, Sequence: 3, DownSince: Ptr(int64(3))}
						if kind == "recovery" {
							s.State, s.Sequence, s.DownSince = "up", 5, nil
						}
						n.samples[m.ID+"/"+node] = s
						if err := put(n.store.db, "samples", m.ID+"/"+node, s); err != nil {
							t.Fatal(err)
						}
					}
					release := pcrHoldWorkers(n)
					n.mu.Lock()
					n.scheduleNotification(m, n.fresh(m))
					n.mu.Unlock()
					release()
					var queued attempt
					for _, a := range n.attempts {
						if a.Kind == kind {
							queued = a
						}
					}
					if queued.ID == "" || queued.IncidentID != later.ID || queued.ID != attemptIdentity(m.ID, later.ID, kind, queued.Slot, target.ID) {
						t.Fatal("scheduler did not persist the delivered down's follow-up identity", queued)
					}
					old := cirOriginal("a-cir-followup-old", map[string][]int64{"a": {1}, "b": {1}})
					old.MonitorID, old.Revision, old.OpenedAt = m.ID, checkGeneration(m), n.now()-100000
					cr2Apply(t, n, Snapshot{Incidents: []Incident{old}})
					captured := pcrOnlyEpisode(t, n)
					if captured.ID != old.ID || !slices.Contains(episodeIDs(captured), later.ID) {
						t.Fatal("fixture did not change the canonical alias after queuing")
					}
					old.Closed, old.ResolvedAt = map[string]int64{"a": 2, "b": 2}, Ptr(n.now()-50000)
					accepted := Receipt{NotificationView: NotificationView{ID: "cir-followup-accepted", MonitorID: m.ID, IncidentID: old.ID, DestinationID: target.ID, Kind: kind, NodeID: "b", SentAt: n.now() - 500}, Slot: queued.Slot}
					if acceptance == "exact-followup" || acceptance == "other-destination" {
						accepted.IncidentID = later.ID
					}
					if acceptance == "other-destination" {
						accepted.DestinationID = "cir-followup-other-destination"
					}
					cr2Apply(t, n, Snapshot{Incidents: []Incident{old}, Receipts: []Receipt{accepted}})
					if len(n.episodes) != 2 {
						t.Fatal("late closure did not split the follow-up projection")
					}
					if acceptance == "unknown-peer" {
						s := n.samples[m.ID+"/b"]
						s.State, s.DownSince = "pending", nil
						n.samples[m.ID+"/b"] = s
						if err := put(n.store.db, "samples", m.ID+"/b", s); err != nil {
							t.Fatal(err)
						}
					}
					n.syncing = true
					for range 4 {
						n.sendNotification(m, captured, queued, m.ID+"/"+kind, nil)
					}
					if err := n.Stop(); err != nil {
						t.Fatal(err)
					}
					n = cr2New(t, o)
					n.syncing = true
					n.sendNotification(m, captured, queued, m.ID+"/"+kind, nil)
					want := 1
					if acceptance == "exact-followup" || kind == "recovery" && acceptance == "unknown-peer" {
						want = 0
					}
					if r.count("") != want {
						t.Fatal("queued follow-up borrowed sibling/destination authority or replayed after restart", r.all())
					}
					if want == 1 {
						body := r.all()[0]
						if body.Kind != kind || body.IncidentID != later.ID || body.Key != queued.ID || !slices.Equal(body.Incident.Aliases, []string{later.ID}) || body.RepeatSlot != queued.Slot || n.receipts[queued.ID].IncidentID != later.ID || n.attempts[queued.ID].IncidentID != later.ID {
							t.Fatal("follow-up identity, current aliases or provider key changed", body)
						}
					}
				})
			}
		}
	}
}

// Q's complete standalone original is closed by an independent complete seed.
// C names Q as its predecessor, and C in turn closes O. Only O/C are embedded
// boundaries in the first case. Moving Q into the embedded set must not be
// necessary to make exactly the same positive predecessor proof usable.
func TestClosureIdentityReviewStandalonePredecessorClosureFixedPoint(t *testing.T) {
	for _, embeddedQ := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("embeddedQ=%v/reverse=%v", embeddedQ, reverse), func(t *testing.T) {
				o := cr2Options(t, true)
				n := cr2New(t, o)
				q := cirOriginal("cir-predecessor", map[string][]int64{"a": {1}, "b": {1}})
				q.OpenedAt = 0
				qh, qf, ql := partitionEvidence(q)
				seed := q
				seed.ID, seed.Aliases = "cir-seed", []string{"cir-seed"}
				seed.Closed, seed.ResolvedAt = map[string]int64{"a": 2, "b": 2}, Ptr(int64(5))
				sh, sf, sl := partitionEvidence(seed)
				old := cirOriginal("cir-open", map[string][]int64{"a": {3}, "b": {3}})
				oh, of, ol := partitionEvidence(old)
				closed := cirOriginal("cir-closed", map[string][]int64{"a": {3}, "b": {3}})
				closed.Closed, closed.ResolvedAt, closed.Predecessors = map[string]int64{"a": 4, "b": 4}, Ptr(int64(20)), []string{q.ID}
				if embeddedQ {
					closed.uncertainties = []incidentUncertainty{{Header: uncertainHeader(qh), ObserverID: "a", NodeID: "a", Run: 3}}
				}
				ch, cf, cl := partitionEvidence(closed)
				cr2Apply(t, n, Snapshot{Incidents: []Incident{qh, sh}, IncidentFacts: append(qf, sf...), EvidenceLinks: append(ql, sl...)})
				cr2Apply(t, n, Snapshot{Receipts: []Receipt{
					{NotificationView: NotificationView{ID: "cir-seed-accepted", MonitorID: q.MonitorID, IncidentID: seed.ID, Kind: "down", NodeID: "b", SentAt: 2}},
					{NotificationView: NotificationView{ID: "cir-closed-accepted", MonitorID: q.MonitorID, IncidentID: closed.ID, Kind: "down", NodeID: "b", SentAt: 12}},
				}, Intents: []NotificationIntent{deliveryIntent(q.MonitorID, closed.ID, "cir-proof-destination")}})
				for _, observer := range []string{"a", "b"} {
					branch := cirOriginal("cir-later-"+observer, map[string][]int64{observer: {5}})
					branch.OpenedAt = 30
					branch.uncertainties = []incidentUncertainty{
						{Header: uncertainHeader(oh), ObserverID: observer, NodeID: observer, Run: 5},
						{Header: uncertainHeader(ch), ObserverID: observer, NodeID: observer, Run: 5},
					}
					h, facts, links := partitionEvidence(branch)
					cr2Apply(t, n, Snapshot{Incidents: []Incident{h}, IncidentFacts: facts, EvidenceLinks: links})
				}
				for range 2 {
					partitionComplete(t, n, append(of, cf...), append(ol, cl...), reverse)
				}
				if len(n.pendingIncidents) != 0 {
					t.Fatal("complete fixture retained pending originals")
				}
				for _, h := range []Incident{qh, sh, oh, ch} {
					if _, complete, err := hydrateIncident(h, n.incidentFacts, n.evidenceLinks); err != nil || !complete {
						t.Fatal("fixture lacks complete exact proof", h.ID, err)
					}
				}
				derivedQ := combine(q, seed)
				closureWitness := closed
				if embeddedQ {
					closureWitness.uncertainties = []incidentUncertainty{{Header: uncertainHeader(qh), ObserverID: "a", NodeID: "a", Run: 3, resolved: &derivedQ}}
				}
				if !causallyEquivalent(q, seed, nil) || !causallyEquivalent(old, closureWitness, []Incident{derivedQ}) {
					t.Fatal("fixture lacks affirmative closure/predecessor proof")
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
					pred := psrvProjected(t, node, q.ID)
					if pred.Closed == nil || !slices.Contains(episodeIDs(pred), seed.ID) {
						t.Fatal("ordinary projection failed to prove Q closed")
					}
					later := psrvProjected(t, node, "cir-later-a")
					if later.Closed != nil || intersects(episodeIDs(later), []string{old.ID, closed.ID, q.ID, seed.ID}) || len(node.forEpisode(later)) != 0 || node.hasDeliveryIntent(later, "cir-proof-destination") {
						t.Fatal("later lifetime borrowed predecessor/proof identity", later)
					}
					if !slices.Contains(episodeIDs(later), "cir-later-b") {
						t.Error("complete standalone Q--seed closure is absent from embedded O--C fixed point; later A/B remain split after replay/restart/peer sweep")
					}
				}
			})
		}
	}
}
