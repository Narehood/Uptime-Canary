package canary

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Source-authored follow-ups: private stores, fake observation time, loopback
// providers, no Node.Start. Only root executes these through the bounded runner.
func fullReviewFollowupBlock(n *Node) func() {
	release := fullReviewControlBlock(n)
	var once sync.Once
	return func() { once.Do(release) }
}

func TestFullReviewFollowupInterruptedClosureUsesProjectedBoundary(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(fmt.Sprintf("restart=%v", restart), func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(11, 58))
			path := filepath.Join(t.TempDir(), "interrupted.db")
			n, r := maintenanceReviewNode(t, &clock, path), webhook(t)
			parent, child := fullReviewControlMonitors(t, n, r.URL, 0)
			in := child.MonitorInput
			in.NodeIDs = []string{"a", "b"}
			child = maintenanceReviewMonitor(t, n, in, &child)
			maintenanceReviewSample(n, parent, "down", 1, Ptr(int64(1)))
			target := n.notificationTargets(child)[0]
			if group, _, _ := n.suppressionForTarget(child, target); group != child.GroupID {
				t.Fatal("fixture lacks accepted named parent coverage")
			}
			unblock := fullReviewFollowupBlock(n)
			defer unblock()
			old := Incident{ID: "followup-embedded-old", Aliases: []string{"followup-embedded-old"}, MonitorID: child.ID, MonitorName: child.Name, Revision: checkGeneration(child),
				OpenedAt: clock.Load() - 20, ResolvedAt: Ptr(clock.Load() - 10), Closed: map[string]int64{"a": 2, "b": 2}, Runs: map[string][]int64{"a": {1}, "b": {1}}, Predecessors: []string{}}
			header, facts, links := partitionEvidence(old)
			for _, observer := range []string{"a", "b"} {
				id := "followup-branch-" + observer
				branch := Incident{ID: id, Aliases: []string{id}, MonitorID: child.ID, MonitorName: child.Name, Revision: checkGeneration(child), OpenedAt: clock.Load(),
					Runs: map[string][]int64{observer: {3}}, Predecessors: []string{}, uncertainties: []incidentUncertainty{{Header: uncertainHeader(header), ObserverID: observer, NodeID: observer, Run: 3}}}
				bh, bf, bl := partitionEvidence(branch)
				cr2Apply(t, n, Snapshot{Incidents: []Incident{bh}, IncidentFacts: bf, EvidenceLinks: bl})
			}
			if slices.Contains(episodeIDs(psrvProjected(t, n, "followup-branch-a")), "followup-branch-b") {
				t.Fatal("incomplete embedded proof already joined the branches")
			}
			maintenanceReviewSample(n, child, "down", 3, Ptr(int64(3)))
			cr2Apply(t, n, Snapshot{IncidentFacts: facts, EvidenceLinks: links})
			joined := psrvProjected(t, n, "followup-branch-a")
			if joined.Closed != nil || !slices.Equal(episodeIDs(joined), []string{"followup-branch-a", "followup-branch-b"}) || !equalJSON(joined.Runs, map[string][]int64{"a": {3}, "b": {3}}) || !n.hasLocalIncidentDelivery(joined, target.ID) {
				t.Fatal("fixture lacks one ongoing reconciled episode and a captured local obligation", joined)
			}
			clock.Add(1)
			partitionPeerUp(t, n, child, 4)
			maintenanceReviewSample(n, child, "up", 4, nil)
			closed := psrvProjected(t, n, "followup-branch-a")
			if closed.Closed == nil || !equalJSON(closed.Runs, joined.Runs) || n.Error() != nil {
				t.Fatal("fresh all-up observations failed to close the joined lifetime", closed, n.Error())
			}
			if !n.targetSuppressionComplete(closed, target.ID) {
				t.Error("observed covered closure did not complete the reconciled destination boundary")
			}
			unblock()
			if restart {
				if err := n.Stop(); err != nil {
					t.Fatal(err)
				}
				n = maintenanceReviewNode(t, &clock, path)
			}
			clock.Add(1)
			partitionPeerUp(t, n, child, 5)
			maintenanceReviewSample(n, parent, "up", 2, nil)
			maintenanceReviewSample(n, child, "up", 5, nil)
			fullReviewControlFlush(n)
			if down, recovery := fullReviewCount(r, child.ID, "down"), fullReviewCount(r, child.ID, "recovery"); down != 0 || recovery != 0 {
				t.Errorf("covered interrupted episode replayed after parent recovery: down=%d recovery=%d", down, recovery)
			}
		})
	}
}

func TestFullReviewFollowupAcceptedDownKeepsSiblingRecoveryQueued(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(11, 58))
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	r := &receiver{}
	var child Monitor
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var d delivery
		if json.NewDecoder(req.Body).Decode(&d) != nil {
			w.WriteHeader(400)
			return
		}
		r.mu.Lock()
		r.deliveries = append(r.deliveries, d)
		r.mu.Unlock()
		if d.MonitorID == child.ID && d.Kind == "down" {
			select {
			case entered <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-req.Context().Done():
			}
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(server.Close)
	n := maintenanceReviewNode(t, &clock, ":memory:")
	t.Cleanup(unblock)
	parent, childMonitor := fullReviewControlMonitors(t, n, server.URL, 0)
	child = childMonitor
	n.mu.Lock()
	maintenanceSample(n, child, "down", 1, Ptr(int64(1)))
	n.mu.Unlock()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("child down did not reach the loopback provider")
	}
	n.mu.Lock()
	i := fullReviewControlEpisode(t, n, child)
	target := n.notificationTargets(child)[0]
	downID := attemptIdentity(child.ID, i.ID, "down", 0, target.ID)
	n.mu.Unlock()
	alias := "0-followup-inflight"
	i.EvidenceRoot, i.Aliases = "", append(append([]string{}, i.Aliases...), alias)
	page := reviewRound3Page(t, n, Snapshot{Incidents: []Incident{i}})
	n.mu.Lock()
	err := n.applySnapshot(page)
	// A persisted sibling queue can remain when independently queued aliases
	// reconcile while another alias's request has already reached the provider.
	sibling := attempt{ID: attemptIdentity(child.ID, alias, "down", 0, target.ID), MonitorID: child.ID, IncidentID: alias, Kind: "down", DestinationID: target.ID, ChannelID: target.ChannelID, TargetRevision: target.Revision, Provider: target.Provider, State: "queued"}
	saved := n.dbPut("attempts", sibling.ID, sibling)
	if saved {
		n.attempts[sibling.ID] = sibling
	}
	maintenanceSample(n, parent, "down", 1, Ptr(int64(1)))
	n.mu.Unlock()
	if err != nil || !saved {
		t.Fatal("cannot stage alias sibling", err, saved)
	}
	eventually(t, func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()
		return hasReceipt(n.forEpisode(fullReviewControlEpisode(t, n, parent)), "down")
	})
	clock.Add(1)
	n.mu.Lock()
	maintenanceSample(n, child, "up", 2, nil)
	marked := completedDependencyAttempt(n.attempts[downID]) && completedDependencyAttempt(n.attempts[sibling.ID])
	n.mu.Unlock()
	if !marked {
		t.Fatal("closure did not cancel both outstanding alias attempts")
	}
	unblock()
	n.wg.Wait()
	releaseWorkers := fullReviewFollowupBlock(n)
	defer releaseWorkers()
	n.mu.Lock()
	n.evaluate()
	closed := fullReviewControlEpisode(t, n, child)
	recoveryID := attemptIdentity(child.ID, i.ID, "recovery", 0, target.ID)
	queued, exists := n.attempts[recoveryID]
	v := n.attemptView(queued)
	actualDown := hasReceipt(receiptsForTarget(n.forEpisode(closed), target.ID), "down")
	remainingMarker := completedDependencyAttempt(n.attempts[sibling.ID])
	n.mu.Unlock()
	if !actualDown || !remainingMarker || !exists || queued.State != "queued" {
		t.Fatal("fixture lacks accepted down, surviving alias marker and queued recovery", actualDown, remainingMarker, exists, queued)
	}
	if v.State != "queued" && v.State != "held" || v.ErrorCode == completedDependency {
		t.Errorf("accepted down's owed recovery is displayed as completed: %+v", v)
	}
	releaseWorkers()
	fullReviewControlFlush(n)
	if down, recovery := fullReviewCount(r, child.ID, "down"), fullReviewCount(r, child.ID, "recovery"); down != 1 || recovery != 1 {
		t.Errorf("accepted in-flight down lost its recovery: down=%d recovery=%d", down, recovery)
	}
}

func TestFullReviewFollowupSourceIdentityAliasMatrix(t *testing.T) {
	o := cr2Options(t, true)
	n := cr2New(t, o)
	want := map[string]Incident{}
	for _, order := range []string{"low", "high"} {
		for _, extended := range []bool{false, true} {
			name := fmt.Sprintf("%s-%v", order, extended)
			t.Run(name, func(t *testing.T) {
				id, alias := "m-followup-"+name, "0-followup-"+name
				if order == "high" {
					alias = "z-followup-" + name
				}
				i := Incident{ID: id, Aliases: []string{id}, MonitorID: "monitor-" + name, MonitorName: "Source identity", Revision: "source-generation", OpenedAt: 10, ResolvedAt: Ptr(int64(20)), Closed: map[string]int64{"a": 2}, Runs: map[string][]int64{"a": {1}}, Predecessors: []string{}}
				cr2Apply(t, n, Snapshot{Incidents: []Incident{i}})
				i.Aliases = unique([]string{id, alias})
				if extended {
					i.OpenedAt = 9
				}
				want[id] = i
				for range 2 {
					cr2Apply(t, n, Snapshot{Incidents: []Incident{i}})
				}
			})
		}
	}
	check := func(n *Node) {
		t.Helper()
		if len(n.incidentSources) != len(want) || len(n.pendingIncidents) != 0 {
			t.Fatal("source update manufactured or lost source identities", len(n.incidentSources), len(n.pendingIncidents))
		}
		for id, expected := range want {
			source, complete, err := hydrateIncident(n.incidentSources[id], n.incidentFacts, n.evidenceLinks)
			if err != nil || !complete || source.ID != id || !slices.Equal(source.Aliases, expected.Aliases) || !equalJSON(source.Runs, expected.Runs) || source.OpenedAt != expected.OpenedAt || !equalJSON(source.Closed, expected.Closed) || !equalJSON(source.ResolvedAt, expected.ResolvedAt) {
				t.Fatalf("original source %s no longer hydrates with its full lifetime: complete=%v err=%v source=%+v", id, complete, err, source)
			}
			if projected := psrvProjected(t, n, id); projected.ID != expected.Aliases[0] || projected.OpenedAt != expected.OpenedAt {
				t.Fatal("projection lost deterministic canonical alias or extended lifetime", projected)
			}
		}
	}
	check(n)
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = cr2New(t, o)
	check(n)
	peer := cr2New(t, cr2Options(t, false))
	pages := causalCapacityPages(t, n)
	slices.Reverse(pages)
	for _, page := range pages {
		cr2Apply(t, peer, page)
	}
	check(peer)
}
