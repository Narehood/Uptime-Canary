package canary

import (
	"context"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
)

// Independent round-four review. Only this file and its report are shared
// writes. Execute against the private clone; every database and receiver is
// private, probes are controlled, and nodes never Start.

// A complete closure must not make an already captured down disposable when
// a later outage starts before notification workers become available.
func TestPartitionDeliveryReviewOwedClosedDownSurvivesSuccessor(t *testing.T) {
	for _, scenario := range []string{"pending-control", "successor", "opaque-successor"} {
		t.Run(scenario, func(t *testing.T) {
			var now atomic.Int64
			now.Store(maintenanceClock(12, 0))
			var state atomic.Value
			state.Store("up")
			r := webhook(t)
			o := cr2Options(t, true)
			o.Now = now.Load
			o.Probe = func(context.Context, Monitor) ProbeResult {
				return ProbeResult{State: state.Load().(string), Message: "review controlled result"}
			}
			n := cr2New(t, o)
			m, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL), nil, "pdr-successor-monitor", false)
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
			state.Store("up")
			now.Add(1000)
			partitionDrive(t, n)
			if n.episodes[brief.ID].Closed == nil || !n.hasLocalIncidentDelivery(n.episodes[brief.ID], "") {
				t.Fatal("fixture lacks a closed, positively witnessed owed down")
			}
			if scenario == "opaque-successor" {
				h, _, _ := pcrOpaque(t, n, m, "pdr-successor-opaque")
				cr2Apply(t, n, Snapshot{Incidents: []Incident{h}})
			}
			next := "pending"
			if scenario != "pending-control" {
				next = "down"
			}
			state.Store(next)
			now.Add(1000)
			partitionDrive(t, n)
			if next == "down" {
				linked := false
				for _, i := range n.episodes {
					linked = linked || i.ID != brief.ID && slices.Contains(i.Predecessors, brief.ID)
				}
				if !linked {
					t.Fatal("fixture failed to create the independent successor")
				}
			}
			release()
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			for range 3 {
				now.Add(3600000)
				partitionDrive(t, n)
			}
			if !n.hasLocalIncidentDelivery(n.episodes[brief.ID], "") || !n.hasDeliveryIntent(n.episodes[brief.ID], "") {
				t.Fatal("fixture lost the durable obligation or its intent")
			}
			oldDown, newDown := 0, 0
			for _, body := range r.all() {
				if body.Kind != "down" {
					t.Fatal("stale/unknown peer invented a follow-up", r.all())
				}
				if body.IncidentID == brief.ID {
					oldDown++
				} else {
					newDown++
				}
			}
			if next == "down" && newDown != 1 {
				t.Fatal("later outage did not independently deliver exactly once", r.all())
			}
			if oldDown != 1 {
				t.Fatalf("successor revoked a committed local down obligation: want old down=1, got %d; new down=%d; sends=%v", oldDown, newDown, r.all())
			}
		})
	}
}

func pdrThreeMemberOptions(t *testing.T, disk bool) Options {
	t.Helper()
	o := cr2Options(t, disk)
	o.Peers = append(o.Peers, Peer{ID: "c", URL: "http://127.0.0.1:1"})
	return o
}

// The complete positive path is A--O1--O2--B. O1/O2 are complete original
// sources carried only by committed embedded headers and paged evidence.
// Publishing their redundant standalone headers is a control, not new proof.
func TestPartitionDeliveryReviewEmbeddedPositivePathConverges(t *testing.T) {
	for _, standalone := range []bool{false, true} {
		t.Run(fmt.Sprintf("standalone=%v", standalone), func(t *testing.T) {
			o := pdrThreeMemberOptions(t, true)
			n := cr2New(t, o)
			makeOriginal := func(id string, runs map[string][]int64) Incident {
				return Incident{ID: id, Aliases: []string{id}, MonitorID: "pdr-path-monitor", MonitorName: "Embedded positive path", Revision: "pdr-path-generation", OpenedAt: 10, Runs: runs, Predecessors: []string{}}
			}
			left := makeOriginal("pdr-path-original-left", map[string][]int64{"a": {3}, "c": {3}})
			right := makeOriginal("pdr-path-original-right", map[string][]int64{"c": {3}, "b": {3}})
			lh, lf, ll := partitionEvidence(left)
			rh, rf, rl := partitionEvidence(right)
			for _, v := range []struct {
				id, observer string
				header       Incident
			}{{"pdr-path-a", "a", lh}, {"pdr-path-b", "b", rh}} {
				branch := makeOriginal(v.id, map[string][]int64{v.observer: {3}})
				branch.OpenedAt = 15
				branch.uncertainties = []incidentUncertainty{{Header: uncertainHeader(v.header), ObserverID: v.observer, NodeID: v.observer, Run: 3}}
				h, facts, links := partitionEvidence(branch)
				cr2Apply(t, n, Snapshot{Incidents: []Incident{h}, IncidentFacts: facts, EvidenceLinks: links})
			}
			if len(n.episodes) != 2 || len(n.pendingIncidents) != 0 {
				t.Fatal("incomplete embedded proof gained authority")
			}
			partitionComplete(t, n, append(lf, rf...), append(ll, rl...), true)
			if standalone {
				cr2Apply(t, n, Snapshot{Incidents: []Incident{lh, rh}})
			}
			// Every direct edge is independently positive and all uncertainty
			// roots are complete; there is no healthy boundary or predecessor.
			a, aComplete, err := hydrateIncident(n.incidentSources["pdr-path-a"], n.incidentFacts, n.evidenceLinks)
			if err != nil || !aComplete {
				t.Fatal("left original source is incomplete", err)
			}
			b, bComplete, err := hydrateIncident(n.incidentSources["pdr-path-b"], n.incidentFacts, n.evidenceLinks)
			if err != nil || !bComplete {
				t.Fatal("right original source is incomplete", err)
			}
			resolved, err := resolveUncertaintySet(map[string]Incident{a.ID: a, b.ID: b}, n.incidentFacts, n.evidenceLinks)
			if err != nil || !positiveContinuity(resolved[a.ID], left, nil) || !positiveContinuity(left, right, nil) || !positiveContinuity(right, resolved[b.ID], nil) {
				t.Fatal("fixture does not prove every edge of the ongoing path", err)
			}
			for range 2 {
				partitionComplete(t, n, append(lf, rf...), append(ll, rl...), false)
			}
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			if len(n.episodes) != 1 {
				t.Fatalf("complete embedded original path did not converge after proof replay and restart: want one lifetime, got %d", len(n.episodes))
			}
			if !standalone {
				for _, i := range n.episodes {
					if slices.Contains(episodeIDs(i), left.ID) || slices.Contains(episodeIDs(i), right.ID) {
						t.Fatal("proof-only original acquired projected alias authority")
					}
				}
			}
		})
	}
}
