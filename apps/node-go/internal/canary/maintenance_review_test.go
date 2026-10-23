package canary

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Review fixtures exercise the HTTP handler and the real peer decoder. Nodes
// are never started; samples and the clock are local, and receivers are loopback.
func maintenanceReviewNode(t *testing.T, clock *atomic.Int64, path string, configure ...func(*Options)) *Node {
	t.Helper()
	o := Options{NodeID: "a", DBPath: path, Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: clock.Load, NotificationDelayMs: Ptr(int64(0)), Peers: []Peer{{ID: "b", URL: "http://127.0.0.1:1"}}}
	for _, apply := range configure {
		apply(&o)
	}
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	return n
}

func maintenanceReviewWork(t *testing.T, n *Node, in MaintenanceInput) Maintenance {
	t.Helper()
	w := maintenanceAPIRequest(n, "POST", "/api/maintenance", in, adminToken)
	var out Maintenance
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("create maintenance: %d %s", w.Code, w.Body.String())
	}
	return out
}

func maintenanceReviewMonitor(t *testing.T, n *Node, in MonitorInput, previous *Monitor) Monitor {
	t.Helper()
	body, _ := json.Marshal(in)
	var value map[string]any
	_ = json.Unmarshal(body, &value)
	method, path, want := "POST", "/api/monitors", 201
	if previous != nil {
		method, path, want = "PUT", path+"/"+previous.ID, 200
		value["revision"] = previous.Revision
	}
	w := maintenanceAPIRequest(n, method, path, value, adminToken)
	var out Monitor
	if w.Code != want || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("save monitor: %d %s", w.Code, w.Body.String())
	}
	return out
}

func maintenanceReviewSample(n *Node, m Monitor, state string, sequence int64, run *int64) {
	n.mu.Lock()
	maintenanceSample(n, m, state, sequence, run)
	n.mu.Unlock()
	n.wg.Wait()
}

func maintenanceReviewEpisode(t *testing.T, n *Node) Incident {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.episodes) != 1 {
		t.Fatalf("expected one incident, got %d", len(n.episodes))
	}
	for _, i := range n.episodes {
		return i
	}
	panic("unreachable")
}

func maintenanceReviewMerge(t *testing.T, n *Node, s Snapshot) {
	t.Helper()
	s = reviewRound3Page(t, n, s)
	n.mu.Lock()
	err := n.mergeSnapshot(s)
	n.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	n.wg.Wait()
}

func TestReviewMaintenanceEarlierPeerAliasPreservesOwedOutage(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "restart"}[restart], func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(11, 58))
			path := filepath.Join(t.TempDir(), "alias.db")
			n := maintenanceReviewNode(t, &clock, path)
			r := webhook(t)
			m := maintenanceReviewMonitor(t, n, input([]string{"a"}, "http://127.0.0.1:1", r.URL), nil)
			maintenanceReviewWork(t, n, plannedWork())
			clock.Store(maintenanceClock(12, 1))
			// A current sample for a run first observed before the window. A
			// delayed peer can retain the original run's earlier opening.
			maintenanceReviewSample(n, m, "down", 2, Ptr(int64(1)))
			clock.Store(maintenanceClock(12, 3))
			maintenanceReviewSample(n, m, "up", 3, nil)
			i := maintenanceReviewEpisode(t, n)
			if !i.MaintenanceComplete || r.count("") != 0 {
				t.Fatal("fixture did not finish wholly under maintenance", i, r.all())
			}
			// This valid closed sibling knows the same run's original start.
			earlier := i
			earlier.ID, earlier.Aliases = "delayed-prewindow", []string{"delayed-prewindow"}
			// This synthetic sibling is a new self-contained source record;
			// it cannot reuse the other source identity's content commitment.
			earlier.EvidenceRoot = ""
			earlier.OpenedAt = maintenanceClock(11, 59)
			earlier.MaintenanceIDs, earlier.MaintenanceStartedAt = nil, nil
			earlier.MaintenanceReleasedAt, earlier.MaintenanceComplete = nil, false
			maintenanceReviewMerge(t, n, Snapshot{Incidents: []Incident{earlier}})
			if restart {
				if err := n.Stop(); err != nil {
					t.Fatal(err)
				}
				n = maintenanceReviewNode(t, &clock, path)
			}
			clock.Store(maintenanceClock(12, 11))
			maintenanceReviewSample(n, m, "up", 4, nil)
			maintenanceReviewSample(n, m, "up", 5, nil)
			i = maintenanceReviewEpisode(t, n)
			if i.MaintenanceComplete || r.count("down") != 1 || r.count("recovery") != 1 {
				t.Fatalf("pre-window alias lost owed pair: complete=%v opened=%d marker=%v down=%d recovery=%d", i.MaintenanceComplete, i.OpenedAt, i.MaintenanceStartedAt, r.count("down"), r.count("recovery"))
			}
		})
	}
}

func TestReviewMaintenanceGroupMoveCannotEraseUncoveredInterval(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(11, 58))
	n := maintenanceReviewNode(t, &clock, ":memory:")
	r := webhook(t)
	w := maintenanceAPIRequest(n, "POST", "/api/groups", GroupInput{Name: "Planned group", NodeIDs: []string{"a"}}, adminToken)
	var g Group
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &g) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	in := input([]string{"a"}, "http://127.0.0.1:1", r.URL)
	in.GroupID = g.ID
	m := maintenanceReviewMonitor(t, n, in, nil)
	work := plannedWork()
	work.AllMonitors, work.GroupIDs = false, []string{g.ID}
	maintenanceReviewWork(t, n, work)
	clock.Store(maintenanceClock(12, 1))
	maintenanceReviewSample(n, m, "down", 1, Ptr(int64(1)))
	// Occupy workers while a genuinely uncovered portion becomes owed.
	for range cap(n.notificationWorkers) {
		n.notificationWorkers <- struct{}{}
	}
	clock.Store(maintenanceClock(12, 2))
	in.GroupID = ""
	m = maintenanceReviewMonitor(t, n, in, &m)
	maintenanceReviewSample(n, m, "down", 2, Ptr(int64(1)))
	if ids := n.activeMaintenance(m); len(ids) != 0 {
		t.Fatal("fixture is still covered", ids)
	}
	clock.Store(maintenanceClock(12, 3))
	in.GroupID = g.ID
	m = maintenanceReviewMonitor(t, n, in, &m)
	maintenanceReviewSample(n, m, "down", 3, Ptr(int64(1)))
	clock.Store(maintenanceClock(12, 4))
	maintenanceReviewSample(n, m, "up", 4, nil)
	for range cap(n.notificationWorkers) {
		<-n.notificationWorkers
	}
	clock.Store(maintenanceClock(12, 11))
	maintenanceReviewSample(n, m, "up", 5, nil)
	maintenanceReviewSample(n, m, "up", 6, nil)
	i := maintenanceReviewEpisode(t, n)
	if i.MaintenanceComplete || r.count("down") != 1 || r.count("recovery") != 1 {
		t.Fatalf("group round trip erased an uncovered interval: complete=%v down=%d recovery=%d", i.MaintenanceComplete, r.count("down"), r.count("recovery"))
	}
}

func TestReviewMaintenanceQueuedWorkerHonorsHoldAndReleaseStagger(t *testing.T) {
	for _, release := range []bool{false, true} {
		t.Run(map[bool]string{false: "active-window", true: "release-stagger"}[release], func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(11, 58))
			n := maintenanceReviewNode(t, &clock, ":memory:", func(o *Options) {
				o.NodeID = "b"
				o.Peers = []Peer{{ID: "a", URL: "http://127.0.0.1:1"}}
			})
			r := webhook(t)
			m := maintenanceReviewMonitor(t, n, input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL), nil)
			maintenanceReviewWork(t, n, plannedWork())
			sample := func(sequence int64) {
				for _, id := range m.NodeIDs {
					state, run := "up", (*int64)(nil)
					if id == "b" {
						state, run = "down", Ptr(int64(1))
					}
					n.samples[m.ID+"/"+id] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: id, State: state, CheckedAt: n.now(), Message: "queued fixture"}, Sequence: sequence, Revision: m.Revision, DownSince: run}
				}
				n.evaluate()
			}
			clock.Store(maintenanceClock(11, 59))
			n.mu.Lock()
			sample(1)
			n.mu.Unlock()
			n.wg.Wait()
			// Keep the actually scheduled worker before its final I/O gate.
			n.mu.Lock()
			clock.Store(maintenanceClock(11, 59) + 6000)
			sample(2)
			queued := n.sending[m.ID+"/down"]
			clock.Store(maintenanceClock(12, 1))
			sample(3)
			if release {
				clock.Store(maintenanceClock(12, 10))
				sample(4)
			}
			n.mu.Unlock()
			n.wg.Wait()
			if !queued {
				t.Fatal("fixture did not queue a worker before maintenance")
			}
			if r.count("") != 0 {
				t.Errorf("queued rank-1 worker bypassed maintenance hold/release stagger: %+v", r.all())
			}
			if release {
				stagger := max(100, n.settings.NotificationDelayMs, n.settings.SyncIntervalMs*3+n.settings.PeerTimeoutMs)
				clock.Store(maintenanceClock(12, 10) + stagger)
				n.mu.Lock()
				sample(5)
				n.mu.Unlock()
				n.wg.Wait()
				if r.count("down") != 1 {
					t.Fatal("owed outage did not deliver once after release stagger", r.all())
				}
			}
		})
	}
}

func TestReviewMaintenanceHalfOpenBoundariesAndClockRollback(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(11, 58))
	n := maintenanceReviewNode(t, &clock, ":memory:")
	maintenanceReviewWork(t, n, plannedWork())
	for _, tc := range []struct {
		at     int64
		active bool
	}{
		{maintenanceClock(12, 0) - 1, false},
		{maintenanceClock(12, 0), true},
		{maintenanceClock(12, 10) - 1, true},
		{maintenanceClock(12, 10), false},
		{maintenanceClock(12, 5), true},
		{maintenanceClock(11, 59), false},
		{maintenanceClock(12, 11), false},
	} {
		clock.Store(tc.at)
		w := maintenanceAPIRequest(n, "GET", "/api/maintenance", nil, adminToken)
		var views []MaintenanceView
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &views) != nil || len(views) != 1 || views[0].Active != tc.active {
			t.Fatalf("boundary %d want active=%v: %d %s", tc.at, tc.active, w.Code, w.Body.String())
		}
	}
}

func TestReviewMaintenanceOverlapUnionAndUnobservedGap(t *testing.T) {
	for _, gap := range []bool{false, true} {
		t.Run(map[bool]string{false: "overlap", true: "gap"}[gap], func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(11, 58))
			n := maintenanceReviewNode(t, &clock, ":memory:")
			r := webhook(t)
			m := maintenanceReviewMonitor(t, n, input([]string{"a"}, "http://127.0.0.1:1", r.URL), nil)
			first, second := plannedWork(), plannedWork()
			first.Schedule.DurationMinutes = 6
			second.Name, second.Schedule.StartTime, second.Schedule.DurationMinutes = "Second", "12:05", 5
			if gap {
				second.Schedule.StartTime, second.Schedule.DurationMinutes = "12:07", 3
			}
			maintenanceReviewWork(t, n, first)
			maintenanceReviewWork(t, n, second)
			clock.Store(maintenanceClock(12, 1))
			maintenanceReviewSample(n, m, "down", 1, Ptr(int64(1)))
			// There is intentionally no evaluation during the possible gap.
			clock.Store(maintenanceClock(12, 9))
			maintenanceReviewSample(n, m, "up", 2, nil)
			i := maintenanceReviewEpisode(t, n)
			if i.MaintenanceComplete == gap || r.count("") != 0 {
				t.Fatal("incorrect union coverage or delivery during maintenance", i, r.all())
			}
			clock.Store(maintenanceClock(12, 10))
			maintenanceReviewSample(n, m, "up", 3, nil)
			maintenanceReviewSample(n, m, "up", 4, nil)
			want := 0
			if gap {
				want = 1
			}
			if r.count("down") != want || r.count("recovery") != want {
				t.Fatal("union/gap owed work incorrect", r.all())
			}
		})
	}
}

func TestReviewMaintenanceEditsAndDeletionPreserveOwedWork(t *testing.T) {
	for _, action := range []string{"delete", "disable", "retime", "created-after-opening"} {
		t.Run(action, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(11, 58))
			n := maintenanceReviewNode(t, &clock, ":memory:")
			r := webhook(t)
			m := maintenanceReviewMonitor(t, n, input([]string{"a"}, "http://127.0.0.1:1", r.URL), nil)
			var work Maintenance
			if action != "created-after-opening" {
				work = maintenanceReviewWork(t, n, plannedWork())
			}
			for range cap(n.notificationWorkers) {
				n.notificationWorkers <- struct{}{}
			}
			clock.Store(maintenanceClock(12, 1))
			maintenanceReviewSample(n, m, "down", 1, Ptr(int64(1)))
			clock.Store(maintenanceClock(12, 2))
			if action == "created-after-opening" {
				maintenanceReviewWork(t, n, plannedWork())
			} else {
				method, want := "PUT", 200
				if action == "delete" {
					method, want = "DELETE", 204
				}
				if action == "disable" {
					work.Enabled = false
				}
				if action == "retime" {
					work.Schedule.StartTime = "13:00"
				}
				w := maintenanceAPIRequest(n, method, "/api/maintenance/"+work.ID, work, adminToken)
				if w.Code != want {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			clock.Store(maintenanceClock(12, 3))
			maintenanceReviewSample(n, m, "up", 2, nil)
			for range cap(n.notificationWorkers) {
				<-n.notificationWorkers
			}
			clock.Store(maintenanceClock(12, 11))
			maintenanceReviewSample(n, m, "up", 3, nil)
			maintenanceReviewSample(n, m, "up", 4, nil)
			if i := maintenanceReviewEpisode(t, n); i.MaintenanceComplete || r.count("down") != 1 || r.count("recovery") != 1 {
				t.Fatal("edited/deleted/new plan erased owed work", i, r.all())
			}
		})
	}
}

func TestReviewMaintenanceHoldsRepeatAndOwedRecovery(t *testing.T) {
	for _, recovery := range []bool{false, true} {
		t.Run(map[bool]string{false: "repeat", true: "recovery"}[recovery], func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(11, 58))
			n := maintenanceReviewNode(t, &clock, ":memory:")
			r := webhook(t)
			in := input([]string{"a"}, "http://127.0.0.1:1", r.URL)
			in.RepeatAfterMs = Ptr(int64(1000))
			m := maintenanceReviewMonitor(t, n, in, nil)
			maintenanceReviewWork(t, n, plannedWork())
			clock.Store(maintenanceClock(11, 59))
			maintenanceReviewSample(n, m, "down", 1, Ptr(int64(1)))
			if r.count("down") != 1 {
				t.Fatal("fixture's original down did not deliver", r.all())
			}
			clock.Store(maintenanceClock(12, 2))
			state, run := "down", Ptr(int64(1))
			if recovery {
				state, run = "up", nil
			}
			maintenanceReviewSample(n, m, state, 2, run)
			if r.count("") != 1 {
				t.Fatal("repeat/recovery escaped active maintenance", r.all())
			}
			clock.Store(maintenanceClock(12, 10))
			maintenanceReviewSample(n, m, state, 3, run)
			kind := "repeat"
			if recovery {
				kind = "recovery"
			}
			if r.count(kind) != 1 || r.count("") != 2 {
				t.Fatal("owed repeat/recovery did not release", r.all())
			}
		})
	}
}

func TestReviewMaintenanceAllowsExplicitChannelTest(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := maintenanceReviewNode(t, &clock, ":memory:")
	r := webhook(t)
	maintenanceReviewWork(t, n, plannedWork())
	w := maintenanceAPIRequest(n, "POST", "/api/notification-channels", hookChannel("Explicit test", r.URL), adminToken)
	var channel Channel
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &channel) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	w = maintenanceAPIRequest(n, "POST", "/api/notification-channels/"+channel.ID+"/test", map[string]string{"revision": channel.Revision}, adminToken)
	n.wg.Wait()
	if w.Code != 202 || r.count("test") != 1 {
		t.Fatal("explicit channel test was held", w.Code, w.Body.String(), r.all())
	}
}

func TestReviewMaintenanceCompiledCachePeerRollbackAndRestart(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	path := filepath.Join(t.TempDir(), "cache.db")
	n := maintenanceReviewNode(t, &clock, path)
	work := maintenanceReviewWork(t, n, plannedWork())
	before := n.maintenancePlans.Load()
	if views := n.maintenanceViews(); len(views) != 1 || !views[0].Active {
		t.Fatal("fixture must begin active")
	}
	oldViews := n.maintenanceWindows
	in := plannedWork()
	in.Schedule.StartTime = "13:00"
	data, _ := json.Marshal(in)
	v := ResourceVersion{Resource: Resource{Kind: resourceMaintenance, ID: work.ID, Revision: "replacement-plan", UpdatedAt: clock.Load(), Data: data}, Ancestors: []string{work.Revision}}
	page := reviewRound3Page(t, n, Snapshot{Resources: []ResourceVersion{v}})
	_, err := n.store.db.Exec(`CREATE TRIGGER maintenance_review_cache_failure BEFORE INSERT ON records WHEN NEW.category='resourceHeads' BEGIN SELECT RAISE(FAIL,'injected maintenance write failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	err = n.mergeSnapshot(page)
	n.mu.Unlock()
	if err == nil || n.maintenancePlans.Load() != before || n.maintenanceWindows != oldViews || n.currentResource(resourceMaintenance, work.ID).Revision != work.Revision {
		t.Fatal("failed peer transaction published a cache/resource", err)
	}
	saved, ok, err := loadOne[[]ResourceVersion](n.store, "resourceHeads", resourceKey(resourceMaintenance, work.ID))
	if err != nil || !ok || len(saved) != 1 || saved[0].Resource.Revision != work.Revision {
		t.Fatal("failed peer transaction altered SQLite", err, saved)
	}
	if _, err = n.store.db.Exec(`DROP TRIGGER maintenance_review_cache_failure`); err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	err = n.mergeSnapshot(page)
	n.mu.Unlock()
	if err != nil || n.maintenanceViews()[0].Active {
		t.Fatal("retry did not publish the replacement plan", err)
	}
	if err = n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = maintenanceReviewNode(t, &clock, path)
	if views := n.maintenanceViews(); len(views) != 1 || views[0].Active || views[0].Maintenance.Revision != v.Resource.Revision {
		t.Fatal("restart did not compile the committed replacement", views)
	}
}

type maintenanceReviewPreflightContext struct {
	context.Context
	once sync.Once
	hook func()
}

func (c *maintenanceReviewPreflightContext) Done() <-chan struct{} {
	c.once.Do(c.hook)
	return nil
}

func TestReviewMaintenanceReauthorizesAfterPreflight(t *testing.T) {
	for _, action := range []string{"demote", "revoke"} {
		t.Run(action, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(11, 58))
			n := maintenanceReviewNode(t, &clock, ":memory:")
			w := maintenanceAPIRequest(n, "POST", "/api/accounts", map[string]string{"name": "Schedule editor", "role": "editor", "token": viewerToken}, adminToken)
			var account AccountView
			if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &account) != nil {
				t.Fatal(w.Code, w.Body.String())
			}
			called := false
			ctx := &maintenanceReviewPreflightContext{Context: context.Background(), hook: func() {
				called = true
				method, want := "PUT", 200
				change := map[string]string{"role": "viewer"}
				if action == "revoke" {
					method, want = "DELETE", 204
					change = map[string]string{}
				}
				response := maintenanceAPIRequest(n, method, "/api/accounts/"+account.ID, change, adminToken)
				if response.Code != want {
					t.Fatal(response.Code, response.Body.String())
				}
			}}
			body, _ := json.Marshal(plannedWork())
			req := httptest.NewRequest("POST", "/api/maintenance", strings.NewReader(string(body))).WithContext(ctx)
			req.Header.Set("Authorization", "Bearer "+viewerToken)
			w = httptest.NewRecorder()
			n.ServeHTTP(w, req)
			want := 403
			if action == "revoke" {
				want = 401
			}
			if !called || w.Code != want || len(n.resources) != 0 || len(n.currentMaintenancePlans()) != 0 {
				t.Fatalf("stale editor authorized after preflight: hook=%v status=%d body=%s", called, w.Code, w.Body.String())
			}
		})
	}
}

func TestReviewMaintenanceContinuesRealChecksHistoryAndIncidents(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(11, 58))
	var probes atomic.Int64
	n := maintenanceReviewNode(t, &clock, ":memory:", func(o *Options) {
		o.Probe = func(context.Context, Monitor) ProbeResult {
			probes.Add(1)
			return ProbeResult{State: "down", Message: "local injected probe", LatencyMs: 1}
		}
	})
	r := webhook(t)
	m := maintenanceReviewMonitor(t, n, input([]string{"a"}, "http://127.0.0.1:1", r.URL), nil)
	maintenanceReviewWork(t, n, plannedWork())
	clock.Store(maintenanceClock(12, 1))
	n.tick()
	n.wg.Wait()
	w := maintenanceAPIRequest(n, "GET", "/api/monitors/"+m.ID+"/history", nil, adminToken)
	var history []Observation
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &history) != nil || len(history) != 1 || history[0].State != "down" || probes.Load() != 1 || r.count("") != 0 {
		t.Fatal("maintenance changed checks/history/delivery", w.Code, w.Body.String(), probes.Load(), r.all())
	}
	i := maintenanceReviewEpisode(t, n)
	if i.Closed != nil || i.MaintenanceStartedAt == nil {
		t.Fatal("active outage was not retained during maintenance", i)
	}
	n.mu.Lock()
	views := n.monitorViews("viewer")
	n.mu.Unlock()
	if len(views) != 1 || views[0].State != "down" || len(views[0].MaintenanceIDs) != 1 {
		t.Fatal("maintenance concealed raw monitor state", views)
	}
}

func TestReviewMaintenanceExplicitSelectionIsORAndDeletedGroupFailsOpen(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(11, 58))
	n := maintenanceReviewNode(t, &clock, ":memory:")
	w := maintenanceAPIRequest(n, "POST", "/api/groups", GroupInput{Name: "Selected group", NodeIDs: []string{"a"}}, adminToken)
	var g Group
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &g) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	receivers := []*receiver{webhook(t), webhook(t), webhook(t)}
	monitors := []Monitor{}
	for index, r := range receivers {
		in := input([]string{"a"}, "http://127.0.0.1:1", r.URL)
		if index == 1 {
			in.GroupID = g.ID
		}
		monitors = append(monitors, maintenanceReviewMonitor(t, n, in, nil))
	}
	work := plannedWork()
	work.AllMonitors, work.MonitorIDs, work.GroupIDs = false, []string{monitors[0].ID}, []string{g.ID}
	maintenanceReviewWork(t, n, work)
	clock.Store(maintenanceClock(12, 1))
	for _, m := range monitors {
		maintenanceReviewSample(n, m, "down", 1, Ptr(int64(1)))
	}
	if receivers[0].count("") != 0 || receivers[1].count("") != 0 || receivers[2].count("down") != 1 {
		t.Fatal("monitor/group selection did not use OR")
	}
	w = maintenanceAPIRequest(n, "DELETE", "/api/groups/"+g.ID, map[string]string{"revision": g.Revision}, adminToken)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	maintenanceReviewSample(n, monitors[1], "down", 2, Ptr(int64(1)))
	if receivers[1].count("down") != 1 || receivers[0].count("") != 0 {
		t.Fatal("deleted group failed closed or broke explicit selection")
	}
}

func TestReviewMaintenancePeerConflictFailsOpenAndResolutionHoldsRecovery(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(11, 58))
	n := maintenanceReviewNode(t, &clock, ":memory:")
	r := webhook(t)
	m := maintenanceReviewMonitor(t, n, input([]string{"a"}, "http://127.0.0.1:1", r.URL), nil)
	work := maintenanceReviewWork(t, n, plannedWork())
	data, _ := json.Marshal(plannedWork())
	v := ResourceVersion{Resource: Resource{Kind: resourceMaintenance, ID: work.ID, Revision: "concurrent-maintenance", UpdatedAt: clock.Load(), Data: data}, Ancestors: []string{}}
	maintenanceReviewMerge(t, n, Snapshot{Resources: []ResourceVersion{v}})
	clock.Store(maintenanceClock(12, 1))
	maintenanceReviewSample(n, m, "down", 1, Ptr(int64(1)))
	w := maintenanceAPIRequest(n, "GET", "/api/maintenance", nil, adminToken)
	var views []MaintenanceView
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &views) != nil || len(views) != 1 || !views[0].ConfigConflict || views[0].Active || r.count("down") != 1 {
		t.Fatal("conflicting schedule failed closed", w.Body.String(), r.all())
	}
	clock.Store(maintenanceClock(12, 2))
	body, _ := json.Marshal(plannedWork())
	var resolution map[string]any
	_ = json.Unmarshal(body, &resolution)
	resolution["revisions"] = []string{work.Revision, v.Resource.Revision}
	w = maintenanceAPIRequest(n, "POST", "/api/maintenance/"+work.ID+"/resolve", resolution, adminToken)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	maintenanceReviewSample(n, m, "up", 2, nil)
	if r.count("recovery") != 0 {
		t.Fatal("recovery escaped resolved maintenance", r.all())
	}
	clock.Store(maintenanceClock(12, 10))
	maintenanceReviewSample(n, m, "up", 3, nil)
	if r.count("recovery") != 1 {
		t.Fatal("owed recovery did not release", r.all())
	}
}

func TestReviewMaintenanceLateScheduleCannotEraseClosedUnknownWork(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(11, 58))
	n := maintenanceReviewNode(t, &clock, ":memory:")
	r := webhook(t)
	m := maintenanceReviewMonitor(t, n, input([]string{"a"}, "http://127.0.0.1:1", r.URL), nil)
	for range cap(n.notificationWorkers) {
		n.notificationWorkers <- struct{}{}
	}
	clock.Store(maintenanceClock(12, 1))
	maintenanceReviewSample(n, m, "down", 1, Ptr(int64(1)))
	clock.Store(maintenanceClock(12, 3))
	maintenanceReviewSample(n, m, "up", 2, nil)
	// A missing resource is not a historical proof. Its old timestamp cannot
	// substitute for having known maintenance when this incident opened.
	data, _ := json.Marshal(plannedWork())
	v := ResourceVersion{Resource: Resource{Kind: resourceMaintenance, ID: "late-schedule", Revision: "late-revision", UpdatedAt: maintenanceClock(11, 58), Data: data}, Ancestors: []string{}}
	maintenanceReviewMerge(t, n, Snapshot{Resources: []ResourceVersion{v}})
	for range cap(n.notificationWorkers) {
		<-n.notificationWorkers
	}
	clock.Store(maintenanceClock(12, 10))
	maintenanceReviewSample(n, m, "up", 3, nil)
	maintenanceReviewSample(n, m, "up", 4, nil)
	if i := maintenanceReviewEpisode(t, n); i.MaintenanceComplete || r.count("down") != 1 || r.count("recovery") != 1 {
		t.Fatal("late schedule erased owed closed episode", i, r.all())
	}
}

func TestReviewMaintenanceCompilerAdmissionAndInvalidPlanDoNotPublish(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(11, 58))
	n := maintenanceReviewNode(t, &clock, ":memory:")
	maintenanceReviewWork(t, n, plannedWork())
	before := n.maintenancePlans.Load()
	uncached := plannedWork()
	uncached.Schedule.StartTime = "13:00"
	n.maintenanceCompiler <- struct{}{}
	w := maintenanceAPIRequest(n, "POST", "/api/maintenance", uncached, adminToken)
	// A reusable immutable plan does not require compiler admission.
	cached := maintenanceAPIRequest(n, "POST", "/api/maintenance", plannedWork(), adminToken)
	<-n.maintenanceCompiler
	if w.Code != 503 || cached.Code != 201 || len(n.resources) != 2 || len(n.currentMaintenancePlans()) != 1 {
		t.Fatal("compiler admission/cache reuse incorrect", w.Code, w.Body.String(), cached.Code)
	}
	if (*before)[maintenancePlanKey(plannedWork().Schedule)] != n.currentMaintenancePlans()[maintenancePlanKey(plannedWork().Schedule)] {
		t.Fatal("cached immutable plan was replaced")
	}
	invalid := uncached
	invalid.Schedule.DurationMinutes = 0
	w = maintenanceAPIRequest(n, "POST", "/api/maintenance", invalid, adminToken)
	if w.Code != 400 || len(n.resources) != 2 || len(n.currentMaintenancePlans()) != 1 || len(n.maintenanceCompiler) != 0 {
		t.Fatal("invalid preflight published state or leaked admission", w.Code, w.Body.String())
	}
	maintenanceReviewWork(t, n, uncached)
	if len(n.currentMaintenancePlans()) != 2 {
		t.Fatal("compiler did not recover after invalid preflight")
	}
}
