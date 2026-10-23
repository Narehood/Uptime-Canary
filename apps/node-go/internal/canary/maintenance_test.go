package canary

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	maintenanceEngine "uptime-canary/apps/node-go/internal/maintenance"
)

func maintenanceClock(hour, minute int) int64 {
	return time.Date(2026, 10, 22, hour, minute, 0, 0, time.UTC).UnixMilli()
}

func maintenanceNode(t *testing.T, clock *atomic.Int64, path string) *Node {
	t.Helper()
	n, err := New(Options{NodeID: "a", DBPath: path, Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: clock.Load, NotificationDelayMs: Ptr(int64(0))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	return n
}

func plannedWork() MaintenanceInput {
	return MaintenanceInput{Name: "Planned work", Enabled: true, AllMonitors: true, Schedule: maintenanceEngine.Config{Strategy: maintenanceEngine.Once, Timezone: "UTC", StartDate: "2026-10-22", EndDate: "2026-10-22", StartTime: "12:00", DurationMinutes: 10}}
}

func storeMaintenance(t *testing.T, n *Node, in MaintenanceInput) Maintenance {
	t.Helper()
	b, _ := json.Marshal(in)
	prepared, err := n.prepareMaintenanceRequest(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	r, err := n.writeResource(resourceMaintenance, "", in, false, prepared)
	if err != nil {
		t.Fatal(err)
	}
	return maintenanceOf(r)
}

func maintenanceSample(n *Node, m Monitor, state string, sequence int64, run *int64) {
	n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: state, CheckedAt: n.now(), Message: "fixture"}, Sequence: sequence, DownSince: run, Revision: m.Revision, CheckGeneration: m.CheckGeneration}
	n.evaluate()
}

func TestMaintenanceWholeEpisodeDoesNotReplayAfterWindow(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(11, 59))
	n := maintenanceNode(t, &clock, ":memory:")
	r := webhook(t)
	m, err := n.writeMonitor(input([]string{"a"}, "http://127.0.0.1:1", r.URL), nil, "planned-service", false)
	if err != nil {
		t.Fatal(err)
	}
	work := storeMaintenance(t, n, plannedWork())
	clock.Store(maintenanceClock(12, 1))
	maintenanceSample(n, m, "down", 1, Ptr(int64(1)))
	n.wg.Wait()
	view := n.monitorViews("viewer")[0]
	if view.State != "down" || len(view.MaintenanceIDs) != 1 || view.MaintenanceIDs[0] != work.ID || r.count("") != 0 {
		t.Fatal("planned work concealed measurement or sent an alert", view)
	}
	for _, a := range n.attempts {
		if v := n.attemptView(a); v.State != "held" || v.NextAttemptAt != 0 {
			t.Fatal("held attempt advertises a retry", v)
		}
	}
	clock.Store(maintenanceClock(12, 3))
	maintenanceSample(n, m, "up", 2, nil)
	n.wg.Wait()
	for _, i := range n.episodes {
		if i.Closed == nil || !i.MaintenanceComplete {
			t.Fatal("planned incident history lost", i)
		}
	}
	clock.Store(maintenanceClock(12, 11))
	maintenanceSample(n, m, "up", 3, nil)
	n.wg.Wait()
	if r.count("") != 0 || len(n.episodes) != 1 {
		t.Fatal("completed planned outage replayed or disappeared", r.all())
	}
}

func TestMaintenanceOpenOutageReleasesAndDeliveredDownGetsRecovery(t *testing.T) {
	for _, startsBefore := range []bool{false, true} {
		t.Run(map[bool]string{false: "starts-during", true: "already-notified"}[startsBefore], func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(11, 58))
			n := maintenanceNode(t, &clock, ":memory:")
			r := webhook(t)
			m, err := n.writeMonitor(input([]string{"a"}, "http://127.0.0.1:1", r.URL), nil, "release-service", false)
			if err != nil {
				t.Fatal(err)
			}
			storeMaintenance(t, n, plannedWork())
			clock.Store(maintenanceClock(12, 1))
			if startsBefore {
				clock.Store(maintenanceClock(11, 59))
			}
			maintenanceSample(n, m, "down", 1, Ptr(int64(1)))
			n.wg.Wait()
			if startsBefore && r.count("down") != 1 {
				t.Fatal("pre-window down not delivered")
			}
			clock.Store(maintenanceClock(12, 3))
			if startsBefore {
				maintenanceSample(n, m, "up", 2, nil)
			} else {
				maintenanceSample(n, m, "down", 2, Ptr(int64(1)))
			}
			n.wg.Wait()
			if r.count("recovery") != 0 || !startsBefore && r.count("down") != 0 {
				t.Fatal("delivered inside maintenance")
			}
			clock.Store(maintenanceClock(12, 11))
			if startsBefore {
				maintenanceSample(n, m, "up", 3, nil)
			} else {
				maintenanceSample(n, m, "down", 3, Ptr(int64(1)))
			}
			n.wg.Wait()
			if r.count("down") != 1 || startsBefore && r.count("recovery") != 1 {
				t.Fatal("owed alert did not release", r.all())
			}
		})
	}
}

func TestMaintenancePreWindowUnsentOutageSurvivesHold(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(11, 58))
	n := maintenanceNode(t, &clock, ":memory:")
	r := webhook(t)
	m, err := n.writeMonitor(input([]string{"a"}, "http://127.0.0.1:1", r.URL), nil, "owed-service", false)
	if err != nil {
		t.Fatal(err)
	}
	storeMaintenance(t, n, plannedWork())
	for range cap(n.notificationWorkers) {
		n.notificationWorkers <- struct{}{}
	}
	clock.Store(maintenanceClock(11, 59))
	maintenanceSample(n, m, "down", 1, Ptr(int64(1)))
	clock.Store(maintenanceClock(12, 1))
	maintenanceSample(n, m, "up", 2, nil)
	for range cap(n.notificationWorkers) {
		<-n.notificationWorkers
	}
	n.evaluate()
	n.wg.Wait()
	clock.Store(maintenanceClock(12, 11))
	maintenanceSample(n, m, "up", 3, nil)
	n.wg.Wait()
	n.evaluate()
	n.wg.Wait()
	if r.count("down") != 1 || r.count("recovery") != 1 {
		t.Fatal("maintenance discarded a previously owed brief outage", r.all())
	}
}

func maintenanceAPIRequest(n *Node, method, path string, body any, token string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, strings.NewReader(string(b)))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	n.ServeHTTP(w, req)
	return w
}

func TestMaintenanceAPIReplicationRestartAndStrictReferences(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	path := filepath.Join(t.TempDir(), "maintenance.db")
	n := maintenanceNode(t, &clock, path)
	w := maintenanceAPIRequest(n, "POST", "/api/maintenance", plannedWork(), adminToken)
	var work Maintenance
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &work) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(n.maintenanceViews()) != 1 || !n.maintenanceViews()[0].Active {
		t.Fatal("schedule not applied")
	}
	bad := plannedWork()
	bad.AllMonitors, bad.MonitorIDs = false, []string{"missing"}
	if w = maintenanceAPIRequest(n, "POST", "/api/maintenance", bad, adminToken); w.Code != 400 {
		t.Fatal("missing reference accepted", w.Code)
	}
	n.accounts["viewer"] = account{AccountView: AccountView{ID: "viewer", Name: "Viewer", Role: "viewer"}, Hash: digest(viewerToken)}
	if w = maintenanceAPIRequest(n, "POST", "/api/maintenance", plannedWork(), viewerToken); w.Code != 403 {
		t.Fatal("viewer changed schedules", w.Code)
	}
	if w = maintenanceAPIRequest(n, "GET", "/api/maintenance", nil, viewerToken); w.Code != 200 {
		t.Fatal("viewer cannot read schedule", w.Code)
	}
	p, err := n.snapshotPage("", "")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(p)
	o := Options{NodeID: "b", DBPath: ":memory:", Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: clock.Load, Peers: []Peer{{ID: "a", URL: "http://127.0.0.1:1"}}}
	other, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Stop()
	validated, err := other.validateSnapshot(b, "a")
	if err != nil {
		t.Fatal(err)
	}
	if err = other.mergeSnapshot(validated); err != nil || !other.maintenanceViews()[0].Active {
		t.Fatal("peer schedule unavailable", err)
	}
	if err = n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = maintenanceNode(t, &clock, path)
	if views := n.maintenanceViews(); len(views) != 1 || !views[0].Active {
		t.Fatal("restart lost compiled schedule")
	}
	w = maintenanceAPIRequest(n, "DELETE", "/api/maintenance/"+work.ID, map[string]string{"revision": work.Revision}, adminToken)
	if w.Code != 204 || len(n.maintenanceViews()) != 0 {
		t.Fatal("delete did not remove active schedule", w.Code)
	}
}

func TestMaintenanceConflictsFailOpenAndResolveAllHeads(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(11, 59))
	a := maintenanceNode(t, &clock, ":memory:")
	b, err := New(Options{NodeID: "b", DBPath: ":memory:", Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: clock.Load, Peers: []Peer{{ID: "a", URL: "http://127.0.0.1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Stop()
	a.known["b"] = true
	work := storeMaintenance(t, a, plannedWork())
	push := func(from, to *Node) {
		t.Helper()
		page, pageErr := from.snapshotPage("", "")
		if pageErr != nil {
			t.Fatal(pageErr)
		}
		body, _ := json.Marshal(page)
		validated, decodeErr := to.validateSnapshot(body, from.ID)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if mergeErr := to.mergeSnapshot(validated); mergeErr != nil {
			t.Fatal(mergeErr)
		}
	}
	push(a, b)
	for _, n := range []*Node{a, b} {
		edit := work
		edit.Name += n.ID
		w := maintenanceAPIRequest(n, "PUT", "/api/maintenance/"+work.ID, edit, adminToken)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	push(a, b)
	push(b, a)
	clock.Store(maintenanceClock(12, 1))
	for _, n := range []*Node{a, b} {
		v := n.maintenanceViews()
		if len(v) != 1 || v[0].Active || !v[0].ConfigConflict {
			t.Fatal("conflicted planned work did not fail open", v)
		}
	}
	heads := a.resources[resourceKey(resourceMaintenance, work.ID)]
	resolution := map[string]any{"name": "Resolved work", "enabled": true, "allMonitors": true, "schedule": work.Schedule, "revisions": []string{heads[0].Resource.Revision}}
	w := maintenanceAPIRequest(a, "POST", "/api/maintenance/"+work.ID+"/resolve", resolution, adminToken)
	if w.Code != 409 {
		t.Fatal("partial head resolution accepted", w.Code)
	}
	resolution["revisions"] = []string{heads[0].Resource.Revision, heads[1].Resource.Revision}
	w = maintenanceAPIRequest(a, "POST", "/api/maintenance/"+work.ID+"/resolve", resolution, adminToken)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	push(a, b)
	if !b.maintenanceViews()[0].Active || b.maintenanceViews()[0].ConfigConflict {
		t.Fatal("resolved maintenance failed to converge")
	}
}

func TestMaintenanceCLIUsesAuthenticatedResourceLifecycle(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(11, 59))
	n := maintenanceNode(t, &clock, ":memory:")
	server := httptest.NewServer(n)
	defer server.Close()
	get := func(k string) string {
		if k == "CANARY_URL" {
			return server.URL
		}
		if k == "CANARY_TOKEN" {
			return adminToken
		}
		return ""
	}
	run := func(args ...string) any {
		t.Helper()
		v, err := RunCLI(context.Background(), args, get, nil)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	file := filepath.Join(t.TempDir(), "work.json")
	write := func(v any) {
		t.Helper()
		body, _ := json.Marshal(v)
		if err := os.WriteFile(file, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(plannedWork())
	work := run("add-maintenance", file).(map[string]any)
	id := work["id"].(string)
	if len(run("maintenance").([]any)) != 1 {
		t.Fatal("missing CLI schedule")
	}
	work["name"] = "Edited work"
	write(work)
	work = run("update-maintenance", id, file).(map[string]any)
	if work["name"] != "Edited work" || len(run("maintenance-conflicts", id).([]any)) != 1 {
		t.Fatal("CLI edit did not persist")
	}
	run("delete-maintenance", id, work["revision"].(string))
	if len(run("maintenance").([]any)) != 0 {
		t.Fatal("CLI deletion did not remove active schedule")
	}
}
