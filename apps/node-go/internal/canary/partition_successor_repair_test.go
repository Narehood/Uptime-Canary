package canary

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
)

func psrOriginal(id string, runs map[string][]int64) Incident {
	return Incident{ID: id, Aliases: []string{id}, MonitorID: "psr-proof-monitor", MonitorName: "Positive graph", Revision: "psr-proof-generation", OpenedAt: 10, Runs: runs, Predecessors: []string{}}
}

func psrWitness(source, boundary Incident, node string, run int64) Incident {
	source.uncertainties = []incidentUncertainty{{Header: uncertainHeader(boundary), ObserverID: node, NodeID: node, Run: run, resolved: &boundary}}
	return source
}

// Explore the entire dense component with a disconnected target. A successful
// short path alone would conceal exponential enumeration of irrelevant IDs.
func psrDenseGraph(count int) (*continuityGraph, Incident, Incident) {
	originals := []Incident{}
	for index := 0; index < count; index++ {
		originals = append(originals, psrOriginal(fmt.Sprintf("psr-dense-%03d", index), map[string][]int64{"a": {1}, "c": {1}}))
	}
	right := psrOriginal("psr-dense-isolated", map[string][]int64{"b": {1}})
	a := psrWitness(psrOriginal("psr-dense-a", map[string][]int64{"a": {1}}), originals[0], "a", 1)
	b := psrWitness(psrOriginal("psr-dense-b", map[string][]int64{"b": {1}}), right, "b", 1)
	return newContinuityGraph(append(originals, a, b)), a, b
}

func psrLongGraph(count int) (*continuityGraph, Incident, Incident) {
	originals := []Incident{}
	for index := 0; index < count; index++ {
		runs := map[string][]int64{"c": {int64(index + 1), int64(index + 2)}}
		if index == 0 {
			runs["a"] = []int64{1}
		}
		if index == count-1 {
			runs["b"] = []int64{1}
		}
		originals = append(originals, psrOriginal(fmt.Sprintf("psr-chain-%03d", index), runs))
	}
	a := psrWitness(psrOriginal("psr-chain-a", map[string][]int64{"a": {1}}), originals[0], "a", 1)
	b := psrWitness(psrOriginal("psr-chain-b", map[string][]int64{"b": {1}}), originals[len(originals)-1], "b", 1)
	return newContinuityGraph(append(originals, a, b)), a, b
}

func TestPartitionSuccessorRepairGraphDenseCyclesAndLongChains(t *testing.T) {
	for _, count := range []int{8, 32, 96} {
		t.Run(fmt.Sprintf("dense/%d", count), func(t *testing.T) {
			g, a, b := psrDenseGraph(count)
			found, stats := g.searchPositivePath(a, b, combine(a, b), nil)
			if found || stats.States > 3*(count+2) || stats.Edges > (count+2)*(count+2) {
				t.Fatal("dense cycle invented continuity or enumerated path subsets", found, stats)
			}
			t.Logf("nodes=%d causal states=%d distinct edge checks=%d", len(g.nodes), stats.States, stats.Edges)
		})
		t.Run(fmt.Sprintf("chain/%d", count), func(t *testing.T) {
			g, a, b := psrLongGraph(count)
			found, stats := g.searchPositivePath(a, b, combine(a, b), nil)
			if !found || stats.States > 4*(count+2) || stats.Edges > 5*(count+2) {
				t.Fatal("long positive path was capped or revisited dominated constraints", found, stats)
			}
			t.Logf("nodes=%d causal states=%d distinct edge checks=%d", len(g.nodes), stats.States, stats.Edges)
		})
	}
}

func BenchmarkPartitionSuccessorRepairPositiveGraph(b *testing.B) {
	for _, count := range []int{16, 64, 128} {
		for _, dense := range []bool{false, true} {
			name := "chain"
			if dense {
				name = "dense"
			}
			b.Run(fmt.Sprintf("%s/%d", name, count), func(b *testing.B) {
				g, a, target := psrLongGraph(count)
				if dense {
					g, a, target = psrDenseGraph(count)
				}
				aggregate := combine(a, target)
				b.ReportAllocs()
				b.ResetTimer()
				for index := 0; index < b.N; index++ {
					found, stats := g.searchPositivePath(a, target, aggregate, nil)
					if found == dense {
						b.Fatal("unexpected continuity result")
					}
					b.ReportMetric(float64(stats.States), "states/op")
					b.ReportMetric(float64(stats.Edges), "edges/op")
				}
			})
		}
	}
}

// All adjacent original pairs have exact positive edges. The later portion of
// their path nevertheless contains an old run behind L's complete predecessor
// frontier, so an edge-only connected-components merge would be unsound.
func TestPartitionSuccessorRepairGraphRetainsAccumulatedPredecessorBarrier(t *testing.T) {
	prior := psrOriginal("psr-path-prior", map[string][]int64{"a": {1}, "b": {1}, "c": {1}, "d": {1}, "e": {1}})
	prior.Closed, prior.ResolvedAt = map[string]int64{"a": 4, "b": 4, "c": 4, "d": 4, "e": 4}, Ptr(int64(20))
	left := psrOriginal("psr-path-left", map[string][]int64{"a": {5}, "c": {5}})
	left.OpenedAt, left.Predecessors = 30, []string{prior.ID}
	x := psrOriginal("psr-path-x", map[string][]int64{"c": {5}, "d": {5}})
	y := psrOriginal("psr-path-y", map[string][]int64{"d": {5}, "e": {1}})
	right := psrOriginal("psr-path-right", map[string][]int64{"e": {1}, "b": {5}})
	x.OpenedAt, y.OpenedAt, right.OpenedAt = 30, 30, 30
	a := psrWitness(psrOriginal("psr-path-a", map[string][]int64{"a": {5}}), left, "a", 5)
	b := psrWitness(psrOriginal("psr-path-b", map[string][]int64{"b": {5}}), right, "b", 5)
	a.OpenedAt, b.OpenedAt = 30, 30
	records := []Incident{prior, left, x, y, right, a, b}
	path := []Incident{a, left, x, y, right, b}
	for index := 1; index < len(path); index++ {
		if !sharedDownRun(path[index-1], path[index]) || !causallyEquivalent(path[index-1], path[index], records) {
			t.Fatal("fixture lacks a direct positive edge", index)
		}
	}
	if positiveContinuity(a, b, records) {
		t.Fatal("positive path crossed an accumulated predecessor closure")
	}
}

func TestPartitionSuccessorRepairEmbeddedPathLateClosureRemainsReversible(t *testing.T) {
	o := pdrThreeMemberOptions(t, true)
	n := cr2New(t, o)
	left := psrOriginal("psr-late-left", map[string][]int64{"a": {5}, "c": {3}})
	right := psrOriginal("psr-late-right", map[string][]int64{"b": {3}, "c": {3}})
	lh, lf, ll := partitionEvidence(left)
	rh, rf, rl := partitionEvidence(right)
	a := psrOriginal("psr-late-a", map[string][]int64{"a": {5}})
	a.OpenedAt, a.DeliveryTargets = 30, []string{"owed-a"}
	a.uncertainties = []incidentUncertainty{{Header: uncertainHeader(lh), ObserverID: "a", NodeID: "a", Run: 5}}
	b := psrOriginal("psr-late-b", map[string][]int64{"b": {3}})
	b.OpenedAt = 15
	b.uncertainties = []incidentUncertainty{{Header: uncertainHeader(rh), ObserverID: "b", NodeID: "b", Run: 3}}
	ah, af, al := partitionEvidence(a)
	bh, bf, bl := partitionEvidence(b)
	cr2Apply(t, n, Snapshot{Incidents: []Incident{ah, bh}, IncidentFacts: append(af, bf...), EvidenceLinks: append(al, bl...), Receipts: []Receipt{{NotificationView: NotificationView{ID: "psr-late-success", MonitorID: right.MonitorID, IncidentID: right.ID, Kind: "down", NodeID: "b", SentAt: 12}}}})
	var held IncidentFact
	partial := []IncidentFact{}
	for _, f := range rf {
		if f.Kind == "run" && f.NodeID == "c" {
			held = f
		} else {
			partial = append(partial, f)
		}
	}
	if held.ID == "" {
		t.Fatal("fixture did not withhold the intermediate exact shared run")
	}
	partitionComplete(t, n, append(lf, partial...), append(ll, rl...), true)
	if len(n.episodes) != 2 {
		t.Fatal("partial embedded path granted equivalence")
	}
	cr2Apply(t, n, Snapshot{IncidentFacts: []IncidentFact{held}})
	if len(n.episodes) != 1 {
		t.Fatal("complete two-original path failed to join")
	}
	for _, i := range n.episodes {
		if len(n.forEpisode(i)) != 0 || slices.Contains(episodeIDs(i), left.ID) || slices.Contains(episodeIDs(i), right.ID) {
			t.Fatal("proof-only intermediate transferred alias or receipt authority")
		}
	}
	closed := rh
	closed.Closed, closed.ResolvedAt = map[string]int64{"a": 4, "b": 4, "c": 4}, Ptr(int64(20))
	for range 2 {
		cr2Apply(t, n, Snapshot{Incidents: []Incident{closed}})
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = cr2New(t, o)
	if len(n.episodes) != 2 || n.episodes[a.ID].Closed != nil || len(episodeIDs(n.episodes[a.ID])) != 1 || len(n.forEpisode(n.episodes[a.ID])) != 0 || !n.hasDeliveryIntent(n.episodes[a.ID], "owed-a") {
		t.Fatal("late closure failed to reverse proof inference without contaminating the later source", n.episodes)
	}
}

// Closed predecessor obligations remain per destination through a successor,
// temporary route loss and restart. Acceptance and a destination added after
// closure cannot be turned into additional historical deliveries.
func TestPartitionSuccessorRepairClosedDownKeepsExactDestinationAcrossRestart(t *testing.T) {
	var now atomic.Int64
	now.Store(maintenanceClock(12, 0))
	var state atomic.Value
	state.Store("up")
	acceptedHook, owedHook, newHook := webhook(t), webhook(t), webhook(t)
	o := cr2Options(t, true)
	o.Now = now.Load
	o.Probe = func(context.Context, Monitor) ProbeResult {
		return ProbeResult{State: state.Load().(string), Message: "controlled successor"}
	}
	n := cr2New(t, o)
	acceptedChannel := hookChannel("Accepted", acceptedHook.URL)
	owedChannel := hookChannel("Owed", owedHook.URL)
	newChannel := hookChannel("New after closure", newHook.URL)
	for id, channel := range map[string]ChannelInput{"psr-accepted": acceptedChannel, "psr-owed": owedChannel, "psr-new": newChannel} {
		if _, err := n.writeResource(resourceChannel, id, channel, false); err != nil {
			t.Fatal(err)
		}
	}
	in := input([]string{"a", "b"}, "http://127.0.0.1:1", "")
	in.NotificationIDs, in.RepeatAfterMs = []string{"psr-accepted", "psr-owed"}, Ptr(int64(1000))
	m, err := n.writeMonitor(in, nil, "psr-destination-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	partitionPeerUp(t, n, m, 2)
	partitionDrive(t, n)
	release := pcrHoldWorkers(n)
	state.Store("down")
	now.Add(1000)
	partitionDrive(t, n)
	brief := pcrOnlyEpisode(t, n)
	acceptedID, owedID := targetsForChannel(channelOf(n.resources[resourceKey(resourceChannel, "psr-accepted")][0].Resource, "admin"))[0].ID, targetsForChannel(channelOf(n.resources[resourceKey(resourceChannel, "psr-owed")][0].Resource, "admin"))[0].ID
	cr2Apply(t, n, Snapshot{Receipts: []Receipt{{NotificationView: NotificationView{ID: "psr-already-accepted", MonitorID: m.ID, IncidentID: brief.ID, Kind: "down", NodeID: "b", SentAt: now.Load(), DestinationID: acceptedID, ChannelID: "psr-accepted", Provider: "generic"}}}})
	state.Store("up")
	now.Add(1000)
	partitionDrive(t, n)
	if n.episodes[brief.ID].Closed == nil || !n.hasLocalIncidentDelivery(brief, owedID) {
		t.Fatal("fixture did not capture the closed destination obligation")
	}
	in.NotificationIDs = append(in.NotificationIDs, "psr-new")
	m, err = n.writeMonitor(in, n.heads[m.ID], m.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	newID := targetsForChannel(channelOf(n.resources[resourceKey(resourceChannel, "psr-new")][0].Resource, "admin"))[0].ID
	if n.hasDeliveryIntent(n.episodes[brief.ID], newID) || n.hasLocalIncidentDelivery(n.episodes[brief.ID], newID) {
		t.Fatal("late destination gained historical delivery authority")
	}
	state.Store("down")
	now.Add(1000)
	partitionDrive(t, n)
	owedChannel.Enabled = false
	if _, err := n.writeResource(resourceChannel, "psr-owed", owedChannel, false); err != nil {
		t.Fatal(err)
	}
	release()
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = cr2New(t, o)
	now.Add(3600000)
	partitionDrive(t, n)
	if owedHook.count("") != 0 || !n.hasLocalIncidentDelivery(n.episodes[brief.ID], owedID) {
		t.Fatal("disabled route delivered or lost its closed obligation")
	}
	for _, a := range n.attempts {
		if a.IncidentID == brief.ID && a.DestinationID == owedID && n.attemptView(a).NextAttemptAt != 0 {
			t.Fatal("disabled closed obligation advertised an executable retry")
		}
	}
	owedChannel.Enabled = true
	if _, err := n.writeResource(resourceChannel, "psr-owed", owedChannel, false); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		now.Add(3600000)
		partitionDrive(t, n)
	}
	for _, control := range []struct {
		bodies []delivery
		want   int
	}{{acceptedHook.all(), 0}, {owedHook.all(), 1}, {newHook.all(), 0}} {
		old := 0
		for _, body := range control.bodies {
			if body.IncidentID == brief.ID {
				if body.Kind != "down" {
					t.Fatal("successor permitted predecessor repeat/recovery", body)
				}
				old++
			}
		}
		if old != control.want {
			t.Fatal("closed obligation lost destination/acceptance scope", old, control.want, control.bodies)
		}
	}
}

func TestPartitionSuccessorRepairFinalGateKeepsOnlyOwedDown(t *testing.T) {
	for _, scenario := range []string{"owed", "accepted", "repeat", "recovery", "unknown-recovery", "missing-marker", "retired", "maintenance"} {
		t.Run(scenario, func(t *testing.T) {
			var now atomic.Int64
			now.Store(maintenanceClock(12, 0))
			var state atomic.Value
			state.Store("up")
			r := webhook(t)
			o := cr2Options(t, false)
			o.Now = now.Load
			o.Probe = func(context.Context, Monitor) ProbeResult {
				return ProbeResult{State: state.Load().(string), Message: "controlled queued successor"}
			}
			n := cr2New(t, o)
			m, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL), nil, "psr-final-monitor", false)
			if err != nil {
				t.Fatal(err)
			}
			partitionPeerUp(t, n, m, 2)
			partitionDrive(t, n)
			release := pcrHoldWorkers(n)
			defer release()
			state.Store("down")
			now.Add(1000)
			partitionDrive(t, n)
			brief := pcrOnlyEpisode(t, n)
			var queued attempt
			for _, a := range n.attempts {
				if a.IncidentID == brief.ID && a.Kind == "down" {
					queued = a
				}
			}
			if queued.ID == "" {
				t.Fatal("fixture did not capture an unsent down worker")
			}
			state.Store("up")
			now.Add(1000)
			partitionDrive(t, n)
			state.Store("down")
			now.Add(1000)
			partitionDrive(t, n)
			linked := false
			for _, i := range n.episodes {
				linked = linked || slices.Contains(i.Predecessors, brief.ID)
			}
			if !linked || n.episodes[brief.ID].Closed == nil {
				t.Fatal("fixture failed to close and supersede the queued source")
			}
			switch scenario {
			case "accepted", "repeat", "recovery", "unknown-recovery":
				cr2Apply(t, n, Snapshot{Receipts: []Receipt{{NotificationView: NotificationView{ID: "psr-final-accepted", MonitorID: m.ID, IncidentID: brief.ID, Kind: "down", NodeID: "b", SentAt: now.Load()}}}})
				if scenario == "repeat" {
					queued.Kind, queued.Slot = "repeat", 1
				} else if scenario != "accepted" {
					queued.Kind = "recovery"
					sample := n.samples[m.ID+"/a"]
					sample.State, sample.DownSince = "up", nil
					n.samples[m.ID+"/a"] = sample
					if scenario == "unknown-recovery" {
						peer := n.samples[m.ID+"/b"]
						peer.State = "unknown"
						n.samples[m.ID+"/b"] = peer
					}
				}
			case "missing-marker":
				delete(n.localIncidentDeliveries, deliveryIntent(m.ID, brief.ID, "").ID)
			case "retired":
				i := n.episodes[brief.ID]
				i.RetiredBy = "psr-retired-generation"
				n.episodes[i.ID] = i
			case "maintenance":
				storeMaintenance(t, n, plannedWork())
			}
			queued.ID = attemptIdentity(m.ID, queued.IncidentID, queued.Kind, queued.Slot, queued.DestinationID)
			inflight := m.ID + "/" + queued.Kind
			n.sending[inflight], n.syncing = true, true
			// Execute the job captured before closure/successor directly: a
			// scheduler-only repair cannot make this control pass.
			n.sendNotification(m, brief, queued, inflight, nil)
			want := 0
			if scenario == "owed" {
				want = 1
			}
			if r.count("down") != want || r.count("recovery") != 0 || r.count("repeat") != 0 || n.sending[inflight] {
				t.Fatal("final gate lost owed down or broadened marker authority", scenario, r.all())
			}
		})
	}
}

func TestPartitionSuccessorRepairConstraintQuotientKeepsExactWitnesses(t *testing.T) {
	boundary := psrOriginal("psr-quotient-boundary", map[string][]int64{"c": {3}})
	witness := psrWitness(psrOriginal("psr-quotient-witness", map[string][]int64{"c": {3}}), boundary, "c", 3)
	g := newContinuityGraph([]Incident{witness})
	a := psrOriginal("psr-quotient-a", map[string][]int64{"c": {1, 5}})
	b := a
	b.Runs = map[string][]int64{"c": {1, 3, 5}}
	without, with := g.constraintState(a), g.constraintState(b)
	if without.key == with.key || without.dominates(with) || with.dominates(without) {
		t.Fatal("same run extrema erased an exact uncertainty witness")
	}
	// Changes irrelevant to aggregate barriers must not enumerate another
	// state for every proof-only ID, observer label, audience or old root.
	c := b
	c.ID, c.Aliases, c.MonitorName, c.EvidenceRoot = "psr-unreferenced-proof", []string{"psr-unreferenced-proof"}, "Other name", "other-old-root"
	c.DeliveryTargets = []string{"other-destination"}
	if g.constraintState(c).key != with.key {
		t.Fatal("irrelevant proof metadata prevented causal quotienting")
	}
}
