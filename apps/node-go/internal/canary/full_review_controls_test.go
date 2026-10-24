package canary

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Controls use private stores, fake observation time and loopback receivers.
// Nodes never Start; only the root agent may execute these fixtures.
func fullReviewControlMonitors(t *testing.T, n *Node, url string, grace int64) (Monitor, Monitor) {
	t.Helper()
	c, err := n.writeResource(resourceChannel, "", hookChannel("Control route", url), false)
	if err != nil {
		t.Fatal(err)
	}
	in := input([]string{"a"}, "http://127.0.0.1:1", "")
	in.Name, in.NotificationIDs = "Parent", []string{c.ID}
	parent := maintenanceReviewMonitor(t, n, in, nil)
	g, err := n.writeGroup(GroupInput{Name: "Dependency", NodeIDs: []string{"a"}, OutageMonitorID: parent.ID, GraceMs: grace}, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	in.Name, in.GroupID = "Child", g.ID
	return parent, maintenanceReviewMonitor(t, n, in, nil)
}

func fullReviewControlEpisode(t *testing.T, n *Node, m Monitor) Incident {
	t.Helper()
	for _, i := range n.episodes {
		if i.MonitorID == m.ID {
			return i
		}
	}
	t.Fatal("missing control incident", m.ID)
	return Incident{}
}

func fullReviewControlBlock(n *Node) func() {
	for range cap(n.notificationWorkers) {
		n.notificationWorkers <- struct{}{}
	}
	return func() {
		for range cap(n.notificationWorkers) {
			<-n.notificationWorkers
		}
	}
}

func fullReviewControlFlush(n *Node) {
	for range 2 {
		n.mu.Lock()
		n.evaluate()
		n.mu.Unlock()
		n.wg.Wait()
	}
}

func TestFullReviewControlInflightCompletion(t *testing.T) {
	for _, status := range []int{503, 204} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(11, 58))
			path := filepath.Join(t.TempDir(), "inflight.db")
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
					w.WriteHeader(status)
					return
				}
				w.WriteHeader(204)
			}))
			t.Cleanup(server.Close)
			n := maintenanceNode(t, &clock, path)
			t.Cleanup(unblock)
			parent, childMonitor := fullReviewControlMonitors(t, n, server.URL, 0)
			child = childMonitor
			n.mu.Lock()
			maintenanceSample(n, child, "down", 1, Ptr(int64(1)))
			n.mu.Unlock()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("child request never reached provider")
			}
			n.mu.Lock()
			maintenanceSample(n, parent, "down", 1, Ptr(int64(1)))
			n.mu.Unlock()
			eventually(t, func() bool {
				n.mu.Lock()
				defer n.mu.Unlock()
				for _, i := range n.episodes {
					if i.MonitorID == parent.ID && hasReceipt(n.forEpisode(i), "down") {
						return true
					}
				}
				return false
			})
			clock.Add(1)
			n.mu.Lock()
			maintenanceSample(n, child, "up", 2, nil)
			id := attemptIdentity(child.ID, fullReviewControlEpisode(t, n, child).ID, "down", 0, n.notificationTargets(child)[0].ID)
			a := n.attempts[id]
			n.mu.Unlock()
			if a.State != "cancelled" || a.ErrorCode != "completed_during_dependency" || a.Held || a.NextAt != 0 {
				t.Errorf("closure did not terminally cancel the outstanding send: %+v", a)
			}
			unblock()
			n.wg.Wait()
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = maintenanceNode(t, &clock, path)
			v := n.attemptView(n.attempts[id])
			want := "cancelled"
			if status == 204 {
				want = "accepted"
			}
			if v.State != want || v.Held || v.NextAttemptAt != 0 {
				t.Errorf("provider result/restart lost terminal outcome: %+v", v)
			}
			clock.Add(2000) // Also cross the failed-send retry deadline.
			maintenanceReviewSample(n, parent, "up", 2, nil)
			maintenanceReviewSample(n, child, "up", 3, nil)
			fullReviewControlFlush(n)
			wantRecovery := 0
			if status == 204 {
				wantRecovery = 1
			}
			if down, recovery := fullReviewCount(r, child.ID, "down"), fullReviewCount(r, child.ID, "recovery"); down != 1 || recovery != wantRecovery {
				t.Errorf("in-flight outcome replayed or lost recovery: down=%d recovery=%d", down, recovery)
			}
		})
	}
}

func TestFullReviewControlNamedGraceBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name            string
		grace, duration int64
		releasedParent  bool
		want            int
	}{{"within", 1000, 999, false, 0}, {"exact", 1000, 1000, false, 1}, {"after", 1000, 1001, false, 1}, {"released-parent", 0, 2, true, 1}, {"maintenance-only", 0, 60001, false, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			var clock atomic.Int64
			opened := maintenanceClock(11, 58)
			clock.Store(opened)
			n, r := maintenanceNode(t, &clock, ":memory:"), webhook(t)
			parent, child := fullReviewControlMonitors(t, n, r.URL, tc.grace)
			if tc.name == "maintenance-only" {
				maintenanceReviewWork(t, n, plannedWork())
				opened = maintenanceClock(12, 9)
				clock.Store(opened)
			}
			if tc.releasedParent {
				maintenanceReviewSample(n, parent, "down", 1, Ptr(int64(1)))
			}
			unblock := fullReviewControlBlock(n)
			maintenanceReviewSample(n, child, "down", 1, Ptr(int64(1)))
			if tc.releasedParent {
				clock.Add(1)
				maintenanceReviewSample(n, parent, "up", 2, nil)
				maintenanceReviewSample(n, child, "down", 2, Ptr(int64(1)))
			}
			clock.Store(opened + tc.duration)
			maintenanceReviewSample(n, child, "up", 3, nil)
			unblock()
			clock.Store(opened + max(tc.duration, 1002))
			maintenanceReviewSample(n, child, "up", 4, nil)
			fullReviewControlFlush(n)
			if down, recovery := fullReviewCount(r, child.ID, "down"), fullReviewCount(r, child.ID, "recovery"); down != tc.want || recovery != tc.want {
				t.Errorf("named grace/release obligation: down=%d recovery=%d want=%d", down, recovery, tc.want)
			}
		})
	}
}

func TestFullReviewControlPeerCompletionProvenance(t *testing.T) {
	for _, mode := range []string{"global-only", "local-marker", "extended-marker"} {
		t.Run(mode, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(11, 58))
			path := filepath.Join(t.TempDir(), "peer.db")
			n, r := maintenanceReviewNode(t, &clock, path), webhook(t)
			parent, child := fullReviewControlMonitors(t, n, r.URL, 0)
			if mode != "global-only" {
				maintenanceReviewSample(n, parent, "down", 1, Ptr(int64(1)))
			}
			unblock := fullReviewControlBlock(n)
			maintenanceReviewSample(n, child, "down", 1, Ptr(int64(1)))
			clock.Add(1)
			if mode != "global-only" {
				maintenanceReviewSample(n, child, "up", 2, nil)
			}
			i := fullReviewControlEpisode(t, n, child)
			i.EvidenceRoot, i.Aliases = "", append(append([]string{}, i.Aliases...), "control-peer-alias")
			i.Closed, i.ResolvedAt = map[string]int64{"a": 2}, Ptr(clock.Load())
			i.SuppressedComplete, i.SuppressedByGroupID, i.SuppressedAt = true, child.GroupID, Ptr(i.OpenedAt)
			if mode == "extended-marker" {
				i.OpenedAt-- // Earlier evidence changes lifetime, while aliases alone do not.
			}
			// Install the up observation without locally evaluating its closure.
			n.samples[child.ID+"/a"] = Sample{Observation: Observation{MonitorID: child.ID, NodeID: "a", State: "up", CheckedAt: clock.Load(), Message: "fixture"}, Sequence: 2, Revision: child.Revision, CheckGeneration: child.CheckGeneration}
			maintenanceReviewMerge(t, n, Snapshot{Incidents: []Incident{i}})
			target := n.notificationTargets(child)[0]
			a := attempt{ID: attemptIdentity(child.ID, "control-peer-alias", "down", 0, target.ID), MonitorID: child.ID, IncidentID: "control-peer-alias", Kind: "down", DestinationID: target.ID, ChannelID: target.ChannelID, TargetRevision: target.Revision, Provider: target.Provider, State: "queued"}
			if !n.dbPut("attempts", a.ID, a) {
				t.Fatal("cannot persist queued sibling alias")
			}
			n.attempts[a.ID] = a
			if mode == "global-only" {
				clock.Add(1)
				maintenanceReviewSample(n, parent, "down", 1, Ptr(int64(1)))
				p := fullReviewControlEpisode(t, n, parent)
				receipt := Receipt{NotificationView: NotificationView{ID: attemptIdentity(parent.ID, p.ID, "down", 0, target.ID), MonitorID: parent.ID, IncidentID: p.ID, Kind: "down", NodeID: "b", SentAt: clock.Load(), DestinationID: target.ID, ChannelID: target.ChannelID, Provider: target.Provider}}
				maintenanceReviewMerge(t, n, Snapshot{Receipts: []Receipt{receipt}})
			}
			unblock()
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = maintenanceReviewNode(t, &clock, path)
			v := n.attemptView(n.attempts[a.ID])
			completed := mode == "local-marker"
			if (v.State == "cancelled") != completed || completed && (v.Held || v.NextAttemptAt != 0 || v.ErrorCode != "completed_during_dependency") {
				t.Errorf("alias/restart used incorrect completion provenance: %+v", v)
			}
			clock.Add(1)
			maintenanceReviewSample(n, parent, "up", 2, nil)
			maintenanceReviewSample(n, child, "up", 3, nil)
			fullReviewControlFlush(n)
			want := 1
			if completed {
				want = 0
			}
			if down, recovery := fullReviewCount(r, child.ID, "down"), fullReviewCount(r, child.ID, "recovery"); down != want || recovery != want {
				t.Errorf("peer history discarded/replayed pair: down=%d recovery=%d want=%d", down, recovery, want)
			}
		})
	}
}

func TestFullReviewControlCausalWireArrayLimits(t *testing.T) {
	n := cr2New(t, cr2Options(t, false))
	for _, domain := range []string{"edges", "claims", "facts", "links"} {
		t.Run(domain, func(t *testing.T) {
			s := Snapshot{}
			for index := 0; index <= pageRecords; index++ {
				id := fmt.Sprintf("control-%04d", index)
				switch domain {
				case "edges":
					s.RevisionEdges = append(s.RevisionEdges, newRevisionEdge("monitor", "monitor", id, id, []string{}, ""))
				case "claims":
					v := RevisionClaim{Kind: "monitor", EntityID: "monitor", Revision: "revision", Ancestor: id}
					v.ID = claimID(v)
					s.RevisionClaims = append(s.RevisionClaims, v)
				case "facts":
					v := IncidentFact{MonitorID: "monitor", Revision: "revision", IncidentID: "incident", Kind: "run", NodeID: "a", Run: int64(index + 1)}
					v.ID = incidentFactID(v)
					s.IncidentFacts = append(s.IncidentFacts, v)
				case "links":
					v := EvidenceLink{FactID: digest(id)}
					v.ID = linkID(v)
					s.EvidenceLinks = append(s.EvidenceLinks, v)
				}
				if index == pageRecords-1 || index == pageRecords {
					if err := n.validateCausalFacts(s); err != nil {
						t.Fatal("internally valid records were rejected", err)
					}
					_, err := n.validateSnapshot(cr2Body(t, s), "b")
					if (err != nil) != (index == pageRecords) {
						t.Fatalf("wire count=%d admission error=%v", index+1, err)
					}
				}
			}
		})
	}
}

func TestFullReviewControlMaintenanceProofArrivesBeforeClosure(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(11, 58))
	n, r := maintenanceReviewNode(t, &clock, ":memory:"), webhook(t)
	m := maintenanceReviewMonitor(t, n, input([]string{"a"}, "http://127.0.0.1:1", r.URL), nil)
	first, second := plannedWork(), plannedWork()
	first.Schedule.DurationMinutes = 6
	second.Name, second.Schedule.StartTime, second.Schedule.DurationMinutes = "Awaiting proof", "12:05", 5
	maintenanceReviewWork(t, n, first)
	data, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	raw := Resource{Kind: resourceMaintenance, ID: "control-maintenance", Revision: "child", UpdatedAt: clock.Load(), Data: data}
	edge := newRevisionEdge(resourceMaintenance, raw.ID, raw.Revision, raw, []string{"parent"}, "")
	v := ResourceVersion{Resource: raw, Ancestors: edge.Parents, EdgeID: edge.ID}
	maintenanceReviewMerge(t, n, Snapshot{Resources: []ResourceVersion{v}, RevisionEdges: []RevisionEdge{edge}})
	if n.resourceVersionReady(v) {
		t.Fatal("missing parent proof was already ready")
	}
	clock.Store(maintenanceClock(12, 1))
	maintenanceReviewSample(n, m, "down", 1, Ptr(int64(1)))
	raw.Revision = "parent"
	parent := newRevisionEdge(resourceMaintenance, raw.ID, raw.Revision, raw, []string{}, "")
	clock.Store(maintenanceClock(12, 4))
	maintenanceReviewMerge(t, n, Snapshot{RevisionEdges: []RevisionEdge{parent}})
	if !n.resourceVersionReady(v) {
		t.Fatal("arrived parent proof did not activate existing schedule")
	}
	clock.Store(maintenanceClock(12, 9))
	maintenanceReviewSample(n, m, "up", 2, nil)
	if !maintenanceReviewEpisode(t, n).MaintenanceComplete {
		t.Error("proven overlapping maintenance did not complete outage")
	}
	clock.Store(maintenanceClock(12, 10))
	maintenanceReviewSample(n, m, "up", 3, nil)
	fullReviewControlFlush(n)
	if r.count("") != 0 {
		t.Error("fully proven maintenance replayed notifications", r.all())
	}
}
