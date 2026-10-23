package canary

import (
	"fmt"
	"math/rand"
	"slices"
	"testing"
)

// Independent round-five review. Shared writes are restricted to this file and
// docs/partition-successor-review.md. Execute in the recorded private snapshot;
// existing fixtures are unchanged, stores are private, and nodes never Start.

func psrvOriginal(id string, runs map[string][]int64) Incident {
	return Incident{ID: id, Aliases: []string{id}, MonitorID: "psrv-monitor", MonitorName: "Successor review", Revision: "psrv-generation", OpenedAt: 10, Runs: runs, Predecessors: []string{}}
}

func psrvProjected(t *testing.T, n *Node, id string) Incident {
	t.Helper()
	for _, i := range n.episodes {
		if slices.Contains(episodeIDs(i), id) {
			return i
		}
	}
	t.Fatal("source is absent from projection", id)
	return Incident{}
}

// The old open commitment O and complete closed witness C describe the same
// earlier lifetime. Both are proof-only embedded originals. A and B are after
// C's exact all-up frontier. Publishing C's redundant standalone header must
// not be necessary to make O's positively proven closure available to A/B.
func TestPartitionSuccessorReviewEmbeddedClosureFixedPoint(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reverse=%v", reverse), func(t *testing.T) {
			o := cr2Options(t, true)
			n := cr2New(t, o)
			old := psrvOriginal("psrv-closure-old", map[string][]int64{"a": {3}, "b": {3}})
			closed := psrvOriginal("psrv-closure-complete", map[string][]int64{"a": {3}, "b": {3}})
			closed.Closed, closed.ResolvedAt = map[string]int64{"a": 4, "b": 4}, Ptr(int64(20))
			oh, of, ol := partitionEvidence(old)
			ch, cf, cl := partitionEvidence(closed)
			for _, observer := range []string{"a", "b"} {
				branch := psrvOriginal("psrv-closure-"+observer, map[string][]int64{observer: {5}})
				branch.OpenedAt = 30
				branch.uncertainties = []incidentUncertainty{
					{Header: uncertainHeader(oh), ObserverID: observer, NodeID: observer, Run: 5},
					{Header: uncertainHeader(ch), ObserverID: observer, NodeID: observer, Run: 5},
				}
				h, facts, links := partitionEvidence(branch)
				cr2Apply(t, n, Snapshot{Incidents: []Incident{h}, IncidentFacts: facts, EvidenceLinks: links})
			}
			cr2Apply(t, n, Snapshot{Receipts: []Receipt{{NotificationView: NotificationView{ID: "psrv-closure-receipt", MonitorID: old.MonitorID, IncidentID: closed.ID, Kind: "down", NodeID: "b", SentAt: 12}}}})
			if len(n.episodes) != 2 {
				t.Fatal("incomplete proof gained authority")
			}
			for range 2 {
				partitionComplete(t, n, append(of, cf...), append(ol, cl...), reverse)
			}
			for _, h := range []Incident{oh, ch} {
				full, complete, err := hydrateIncident(h, n.incidentFacts, n.evidenceLinks)
				if err != nil || !complete || !sharedDownRun(full, old) {
					t.Fatal("fixture lacks exact complete embedded original", h.ID, err)
				}
			}
			if !causallyEquivalent(old, closed, nil) || !runsAfterClosure(map[string][]int64{"a": {5}, "b": {5}}, 30, closed) {
				t.Fatal("fixture lacks positive old closure or both later phases")
			}
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			before := psrvProjected(t, n, "psrv-closure-a")
			joinedBefore := slices.Contains(episodeIDs(before), "psrv-closure-b")
			for _, i := range n.episodes {
				if len(n.forEpisode(i)) != 0 || slices.Contains(episodeIDs(i), old.ID) || slices.Contains(episodeIDs(i), closed.ID) {
					t.Fatal("proof-only closure imported old aliases or receipt authority")
				}
			}
			// Control supplies no facts, links, runs or closure information beyond
			// the already complete embedded header and its commitment.
			cr2Apply(t, n, Snapshot{Incidents: []Incident{ch}})
			after := psrvProjected(t, n, "psrv-closure-a")
			if !slices.Contains(episodeIDs(after), "psrv-closure-b") || after.Closed != nil || len(n.forEpisode(after)) != 0 {
				t.Fatal("redundant standalone-header control failed or borrowed old authority", after)
			}
			if !joinedBefore {
				t.Error("complete embedded closure proof left A/B split after replay/restart; redundant standalone header alone joins them")
			}
		})
	}
}

// Capture a genuine queued down while two open sources share a reversible
// projection. After a late closure separates them and the exact old down is
// accepted, every execution of that queued job must stop. Historical captured
// aliases cannot select the unsent later sibling instead of its attempt ID.
func TestPartitionSuccessorReviewQueuedSplitKeepsExactReceiptAuthority(t *testing.T) {
	for _, named := range []bool{false, true} {
		t.Run(fmt.Sprintf("named=%v", named), func(t *testing.T) {
			r := webhook(t)
			n := cr2New(t, cr2Options(t, false))
			in := input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL)
			if named {
				channel, err := n.writeResource(resourceChannel, "psrv-split-channel", hookChannel("Split review", r.URL), false)
				if err != nil {
					t.Fatal(err)
				}
				in.WebhookURL, in.NotificationIDs = "", []string{channel.ID}
			}
			m, err := n.writeMonitor(in, nil, "psrv-split-monitor", false)
			if err != nil {
				t.Fatal(err)
			}
			target := n.notificationTargets(m)[0]
			old := psrvOriginal("a-psrv-split-old", map[string][]int64{"a": {1}, "b": {1}})
			old.MonitorID, old.Revision, old.OpenedAt, old.DeliveryTargets = m.ID, checkGeneration(m), n.now()-10000, []string{target.ID}
			later := psrvOriginal("z-psrv-split-later", map[string][]int64{"a": {3}, "b": {3}})
			later.MonitorID, later.Revision, later.OpenedAt = m.ID, checkGeneration(m), n.now()-1000
			cr2Apply(t, n, Snapshot{Incidents: []Incident{old, later}})
			captured := pcrOnlyEpisode(t, n)
			for _, id := range []string{"a", "b"} {
				n.samples[m.ID+"/"+id] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: id, State: "down", CheckedAt: n.now()}, Revision: m.Revision, CheckGeneration: m.CheckGeneration, Sequence: 3, DownSince: Ptr(int64(3))}
			}
			release := pcrHoldWorkers(n)
			n.mu.Lock()
			n.scheduleNotification(m, n.fresh(m))
			n.mu.Unlock()
			release()
			var queued attempt
			for _, a := range n.attempts {
				if a.Kind == "down" && a.DestinationID == target.ID {
					queued = a
				}
			}
			if queued.ID == "" || queued.IncidentID != old.ID || len(episodeIDs(captured)) != 2 {
				t.Fatal("fixture failed to capture the original merged down worker", queued)
			}
			old.Closed, old.ResolvedAt = map[string]int64{"a": 2, "b": 2}, Ptr(n.now()-5000)
			accepted := Receipt{NotificationView: NotificationView{ID: "psrv-split-accepted", MonitorID: m.ID, IncidentID: old.ID, Kind: "down", NodeID: "b", SentAt: n.now(), DestinationID: target.ID}}
			cr2Apply(t, n, Snapshot{Incidents: []Incident{old}, Receipts: []Receipt{accepted}})
			if len(n.episodes) != 2 || len(n.forEpisode(psrvProjected(t, n, later.ID))) != 0 || !hasReceipt(receiptsForTarget(n.forEpisode(psrvProjected(t, n, old.ID)), target.ID), "down") {
				t.Fatal("fixture did not split with an exact old receipt and an unsent later lifetime")
			}
			// Repeat the final-gate control to exercise Go's unordered map lookup.
			// Every invocation has the same already-accepted exact attempt/source;
			// a correct gate refuses all of them without provider I/O.
			inflight := m.ID + "/down/" + target.ID
			n.syncing = true
			for range 64 {
				n.sending[inflight] = true
				n.sendNotification(m, captured, queued, inflight, nil)
			}
			if calls := r.count("down"); calls != 0 {
				first := r.all()[0]
				t.Errorf("accepted old queued down reached provider %d/64 times after split; attempt incident=%s payload aliases=%v later receipts=%d", calls, first.IncidentID, first.Incident.Aliases, len(n.forEpisode(psrvProjected(t, n, later.ID))))
			}
			before := r.count("down")
			n.mu.Lock()
			n.scheduleNotification(m, n.fresh(m))
			n.mu.Unlock()
			n.wg.Wait()
			if r.count("down") != before+1 || r.all()[before].IncidentID != later.ID || len(n.forEpisode(psrvProjected(t, n, later.ID))) != 1 {
				t.Fatal("fresh post-split scheduler failed to deliver the independent later down exactly once")
			}
		})
	}
}

// The same failure through one genuine scheduler-launched worker per private
// node. Holding the normal state mutex places proof/receipt arrival between
// queue admission and the final gate, without replacing the worker function.
func TestPartitionSuccessorReviewScheduledWorkerAfterSplit(t *testing.T) {
	r := webhook(t)
	for trial := 0; trial < 64; trial++ {
		n := cr2New(t, cr2Options(t, false))
		m, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL), nil, "psrv-worker-monitor", false)
		if err != nil {
			t.Fatal(err)
		}
		old := psrvOriginal("a-psrv-worker-old", map[string][]int64{"a": {1}, "b": {1}})
		old.MonitorID, old.Revision, old.OpenedAt, old.DeliveryTargets = m.ID, checkGeneration(m), n.now()-10000, []string{""}
		later := psrvOriginal("z-psrv-worker-later", map[string][]int64{"a": {3}, "b": {3}})
		later.MonitorID, later.Revision, later.OpenedAt = m.ID, checkGeneration(m), n.now()-1000
		cr2Apply(t, n, Snapshot{Incidents: []Incident{old, later}})
		if len(n.episodes) != 1 {
			t.Fatal("fixture did not create the reversible original projection")
		}
		for _, id := range []string{"a", "b"} {
			n.samples[m.ID+"/"+id] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: id, State: "down", CheckedAt: n.now()}, Revision: m.Revision, CheckGeneration: m.CheckGeneration, Sequence: 3, DownSince: Ptr(int64(3))}
		}
		old.Closed, old.ResolvedAt = map[string]int64{"a": 2, "b": 2}, Ptr(n.now()-5000)
		page := cr2Decode(t, n, Snapshot{Incidents: []Incident{old}, Receipts: []Receipt{{NotificationView: NotificationView{ID: "psrv-worker-accepted", MonitorID: m.ID, IncidentID: old.ID, Kind: "down", NodeID: "b", SentAt: n.now()}}}})
		n.mu.Lock()
		n.syncing = true
		n.scheduleNotification(m, n.fresh(m))
		queued := n.sending[m.ID+"/down"]
		err = n.applySnapshot(page)
		n.mu.Unlock()
		n.wg.Wait()
		if err != nil || !queued || len(n.episodes) != 2 || len(n.forEpisode(psrvProjected(t, n, later.ID))) != 0 {
			t.Fatal("fixture did not admit one old job before a successful late split", err, queued)
		}
		if err := n.Stop(); err != nil {
			t.Fatal(err)
		}
	}
	if calls := r.count("down"); calls != 0 {
		first := r.all()[0]
		t.Errorf("real queued workers resent accepted old downs in %d/64 independent schedules; attempt incident=%s payload aliases=%v", calls, first.IncidentID, first.Incident.Aliases)
	}
}

// A finite reference enumerates exact visited-node subsets and never shares
// semantic edge/transition results or discards dominated constraints. This
// checks the production optimization against the unreduced path state space.
func psrvExactPath(g *continuityGraph, a, b Incident) bool {
	context := g.nodes
	if !continuityUnionCompatible(a, b, context) {
		return false
	}
	type position struct {
		at   int
		mask uint64
	}
	type path struct {
		position
		union Incident
	}
	start := g.byKey[continuityKey(combine(a, a))]
	initial := position{start, uint64(1) << start}
	queue := []path{{initial, combine(a, b)}}
	seen := map[position]bool{initial: true}
	for head := 0; head < len(queue); head++ {
		p := queue[head]
		current := g.nodes[p.at]
		if sharedDownRun(current, b) && causallyEquivalent(current, b, context) {
			return true
		}
		for next, candidate := range g.nodes {
			if p.mask&(uint64(1)<<next) != 0 || !sharedDownRun(current, candidate) || !causallyEquivalent(current, candidate, context) || !continuityUnionCompatible(p.union, candidate, context) {
				continue
			}
			key := position{next, p.mask | uint64(1)<<next}
			if !seen[key] {
				seen[key] = true
				queue = append(queue, path{key, combine(p.union, candidate)})
			}
		}
	}
	return false
}

func TestPartitionSuccessorReviewConstraintCacheAgainstExactPaths(t *testing.T) {
	rng := rand.New(rand.NewSource(50221022))
	positive, negative := 0, 0
	for trial := 0; trial < 192; trial++ {
		boundary := psrvOriginal("psrv-ref-closed", map[string][]int64{"a": {1}, "b": {1}, "c": {1}})
		boundary.Closed, boundary.ResolvedAt = map[string]int64{"a": 2, "b": 2, "c": 2}, Ptr(int64(20))
		open := psrvOriginal("psrv-ref-open", map[string][]int64{"a": {3}, "b": {3}, "c": {3}})
		nodes := []Incident{}
		for index := 0; index < 6; index++ {
			i := psrvOriginal(fmt.Sprintf("psrv-ref-%d", index), map[string][]int64{})
			for _, node := range []string{"a", "b", "c"} {
				for _, run := range []int64{1, 3, 5} {
					if rng.Intn(3) == 0 {
						i.Runs[node] = append(i.Runs[node], run)
					}
				}
			}
			if len(i.Runs) == 0 {
				i.Runs["a"] = []int64{3}
			}
			if trial%4 != 0 {
				i.OpenedAt = []int64{10, 30}[rng.Intn(2)]
				if rng.Intn(4) == 0 {
					i.Closed, i.ResolvedAt = map[string]int64{"a": 6, "b": 6, "c": 6}, Ptr(int64(40))
				}
				if rng.Intn(4) == 0 {
					i.Predecessors = []string{boundary.ID}
				} else if rng.Intn(5) == 0 {
					i.Predecessors = []string{fmt.Sprintf("psrv-ref-%d", rng.Intn(6))}
				}
				if rng.Intn(3) == 0 {
					i.Aliases = append(i.Aliases, fmt.Sprintf("psrv-common-%d", rng.Intn(2)))
				}
				switch rng.Intn(4) {
				case 0:
					i.uncertainties = []incidentUncertainty{{Header: uncertainHeader(boundary), ObserverID: "a", NodeID: "a", Run: 3, resolved: &boundary}}
				case 1:
					id := []string{"a", "b", "c"}[rng.Intn(3)]
					i.uncertainties = []incidentUncertainty{{Header: uncertainHeader(open), ObserverID: id, NodeID: id, Run: 3, resolved: &open}}
				case 2:
					if rng.Intn(5) == 0 {
						i.uncertainties = []incidentUncertainty{{Header: uncertainHeader(open), ObserverID: "a", NodeID: "a", Run: 3}}
					}
				}
			}
			nodes = append(nodes, i)
		}
		g := newContinuityGraph(append(nodes, boundary))
		for _, pair := range [][2]int{{0, 1}, {2, 3}, {4, 5}} {
			a, b := nodes[pair[0]], nodes[pair[1]]
			want := psrvExactPath(g, a, b)
			got := continuityUnionCompatible(a, b, g.nodes) && g.positivePath(a, b, combine(a, b), nil)
			if got != want {
				t.Fatalf("semantic cache differs from exact path enumeration: trial=%d pair=%v got=%v want=%v nodes=%+v", trial, pair, got, want, nodes)
			}
			if got {
				positive++
			} else {
				negative++
			}
		}
	}
	if positive < 30 || negative < 30 {
		t.Fatal("differential controls lack both positive and negative coverage", positive, negative)
	}
	t.Logf("exact path comparisons: positive=%d negative=%d", positive, negative)
}

func TestPartitionSuccessorReviewLongEmbeddedPathLateClosure(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("reverse=%v", reverse), func(t *testing.T) {
			o := pdrThreeMemberOptions(t, true)
			n := cr2New(t, o)
			proofs := []Incident{}
			facts, links := []IncidentFact{}, []EvidenceLink{}
			var held IncidentFact
			for index := 0; index < 12; index++ {
				i := psrvOriginal(fmt.Sprintf("psrv-long-%02d", index), map[string][]int64{"c": {int64(index + 3), int64(index + 4)}})
				if index == 0 {
					i.Runs["a"] = []int64{5}
				}
				if index == 11 {
					i.Runs["b"] = []int64{3}
				}
				h, fs, ls := partitionEvidence(i)
				proofs = append(proofs, h)
				links = append(links, ls...)
				for _, f := range fs {
					if index == 6 && f.Kind == "run" && held.ID == "" {
						held = f
					} else {
						facts = append(facts, f)
					}
				}
			}
			a := psrvOriginal("psrv-long-a", map[string][]int64{"a": {5}})
			a.OpenedAt = 30
			for _, h := range proofs {
				a.uncertainties = append(a.uncertainties, incidentUncertainty{Header: uncertainHeader(h), ObserverID: "a", NodeID: "a", Run: 5})
			}
			b := psrvOriginal("psrv-long-b", map[string][]int64{"b": {3}})
			b.OpenedAt = 15
			b.uncertainties = []incidentUncertainty{{Header: uncertainHeader(proofs[11]), ObserverID: "b", NodeID: "b", Run: 3}}
			for _, branch := range []Incident{a, b} {
				h, fs, ls := partitionEvidence(branch)
				cr2Apply(t, n, Snapshot{Incidents: []Incident{h}, IncidentFacts: fs, EvidenceLinks: ls})
			}
			cr2Apply(t, n, Snapshot{Receipts: []Receipt{{NotificationView: NotificationView{ID: "psrv-long-proof-receipt", MonitorID: a.MonitorID, IncidentID: proofs[11].ID, Kind: "down", NodeID: "b", SentAt: 12}}}})
			partitionComplete(t, n, facts, links, reverse)
			if held.ID == "" || len(n.episodes) != 2 {
				t.Fatal("incomplete twelve-original proof gained authority")
			}
			cr2Apply(t, n, Snapshot{IncidentFacts: []IncidentFact{held}})
			if len(n.episodes) != 1 || len(episodeIDs(psrvProjected(t, n, a.ID))) != 2 || len(n.forEpisode(psrvProjected(t, n, a.ID))) != 0 {
				t.Fatal("complete twelve-original proof did not converge with only endpoint authority")
			}
			late := proofs[11]
			late.Closed, late.ResolvedAt = map[string]int64{"a": 4, "b": 4, "c": 100}, Ptr(int64(20))
			for range 2 {
				cr2Apply(t, n, Snapshot{Incidents: []Incident{late}})
			}
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			for _, candidate := range []*Node{n, cr2New(t, pdrThreeMemberOptions(t, false))} {
				if candidate != n {
					pcrSweep(t, n, candidate)
				}
				later := psrvProjected(t, candidate, a.ID)
				if later.Closed != nil || len(episodeIDs(later)) != 1 || len(candidate.forEpisode(later)) != 0 || slices.Contains(episodeIDs(psrvProjected(t, candidate, b.ID)), a.ID) {
					t.Fatal("late closure crossed phases or imported old receipt authority on restart/sweep")
				}
			}
		})
	}
}
