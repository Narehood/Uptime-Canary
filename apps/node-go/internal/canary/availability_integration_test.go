package canary

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func observedManual(t *testing.T, n *Node) Monitor {
	t.Helper()
	in := input([]string{"a"}, "", "")
	in.Type, in.Manual = "manual", &ManualMonitorOptions{State: "up"}
	if err := validateMonitor(&in, n.known); err != nil {
		t.Fatal(err)
	}
	m, err := n.writeMonitor(in, nil, "observed-manual", false)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func observedAPI(t *testing.T, n *Node, id, token string, from, to int64) availabilityHistoryResult {
	t.Helper()
	w := maintenanceAPIRequest(n, "GET", fmt.Sprintf("/api/monitors/%s/availability?from=%d&to=%d&stepMs=100", id, from, to), nil, token)
	var result availabilityHistoryResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal("observed history request", w.Code, w.Body.String())
	}
	return result
}

func TestObservedArchiveRuntimePruningEditsRestartAndPeerIsolation(t *testing.T) {
	var clock atomic.Int64
	base := maintenanceClock(12, 0)
	clock.Store(base)
	path := filepath.Join(t.TempDir(), "observed.db")
	n := maintenanceNode(t, &clock, path)
	n.historyLimit = 1
	m := observedManual(t, n)
	partitionDrive(t, n)
	clock.Add(100)
	partitionDrive(t, n)
	if rows, err := n.store.history(m.ID, 10); err != nil || len(rows) != 1 {
		t.Fatal("raw pruning fixture", rows, err)
	}
	clock.Add(50)
	in := m.MonitorInput
	in.Name, in.IntervalMs = "Same check, slower interval", 10000
	changed, err := n.writeMonitor(in, n.heads[m.ID], m.ID, false)
	if err != nil || checkGeneration(changed) != checkGeneration(m) {
		t.Fatal("metadata edit changed semantics", err)
	}
	clock.Store(base + 1000)
	check := func(n *Node) {
		t.Helper()
		result := observedAPI(t, n, m.ID, adminToken, base, clock.Load())
		if result.Summary.UpMs != 150 || result.Summary.UnknownMs != 850 || result.Summary.KnownMs != 150 ||
			result.Summary.AvailabilityPercent == nil || *result.Summary.AvailabilityPercent != 100 || result.Summary.CoveragePercent != 15 {
			t.Fatal("current interval or raw pruning rewrote original coverage", result.Summary)
		}
		var sum int64
		for _, bucket := range result.Buckets {
			sum += bucket.UpMs
		}
		if sum != 150 {
			t.Fatal("bucket coverage diverged from summary")
		}
	}
	check(n)
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = maintenanceNode(t, &clock, path)
	check(n)
	if w := maintenanceAPIRequest(n, "POST", "/api/accounts", map[string]string{"name": "reader", "role": "viewer", "token": viewerToken}, adminToken); w.Code != 201 {
		t.Fatal("viewer fixture", w.Code)
	}
	if result := observedAPI(t, n, m.ID, viewerToken, base, clock.Load()); result.Summary.UpMs != 150 {
		t.Fatal("viewer could not read local aggregate")
	}
	if w := maintenanceAPIRequest(n, "GET", "/api/monitors/"+m.ID+"/availability", nil, clusterToken); w.Code != 401 {
		t.Fatal("cluster token accessed local archive", w.Code)
	}
	for _, malformed := range []string{"from=%ZZ", "from=1;to=2", "%FF=%ZZ"} {
		if w := maintenanceAPIRequest(n, "GET", "/api/monitors/"+m.ID+"/availability?"+malformed, nil, adminToken); w.Code != 400 {
			t.Fatal("malformed query silently became a default range", w.Code)
		}
	}
	peer, err := New(Options{NodeID: "b", DBPath: ":memory:", AdminToken: adminToken, ClusterToken: clusterToken, Now: clock.Load,
		Peers: []Peer{{ID: "a", URL: "http://127.0.0.1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Stop()
	for _, page := range causalCapacityPages(t, n) {
		revisionJoinsApply(t, peer, page)
	}
	result := observedAPI(t, peer, m.ID, adminToken, base, clock.Load())
	if result.Summary.KnownMs != 0 || result.Summary.UnknownMs != 1000 || result.Summary.AvailabilityPercent != nil {
		t.Fatal("peer snapshots backfilled local coverage", result)
	}
}

func TestObservedArchiveConfigClosureSharesCommit(t *testing.T) {
	var clock atomic.Int64
	base := maintenanceClock(12, 0)
	clock.Store(base)
	n := maintenanceNode(t, &clock, ":memory:")
	m := observedManual(t, n)
	partitionDrive(t, n)
	clock.Add(50)
	if _, err := n.store.db.Exec(`CREATE TRIGGER refuse_archive_closure BEFORE UPDATE ON availability_heads WHEN NEW.closed=1 BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	in := m.MonitorInput
	in.Enabled = false
	if _, err := n.writeMonitor(in, n.heads[m.ID], m.ID, false); err == nil {
		t.Fatal("configuration committed after archive closure failed")
	}
	if current := n.current(m.ID); current == nil || !current.Enabled || current.Revision != m.Revision {
		t.Fatal("failed commit published staged configuration")
	}
	var closed int
	if err := n.store.db.QueryRow(`SELECT closed FROM availability_heads WHERE monitor_id=?`, m.ID).Scan(&closed); err != nil || closed != 0 {
		t.Fatal("failed closure escaped rollback", closed, err)
	}
	if _, err := n.store.db.Exec(`DROP TRIGGER refuse_archive_closure`); err != nil {
		t.Fatal(err)
	}
	if _, err := n.writeMonitor(in, n.heads[m.ID], m.ID, false); err != nil {
		t.Fatal(err)
	}
	clock.Store(base + 100)
	if result := observedAPI(t, n, m.ID, adminToken, base, clock.Load()); result.Summary.UpMs != 50 || result.Summary.UnknownMs != 50 {
		t.Fatal("pause did not close at its commit", result.Summary)
	}
}

func TestObservedArchiveFailedSampleDoesNotPublishOrPrune(t *testing.T) {
	var clock atomic.Int64
	base := maintenanceClock(12, 0)
	clock.Store(base)
	n := maintenanceNode(t, &clock, ":memory:")
	n.historyLimit = 1
	m := observedManual(t, n)
	partitionDrive(t, n)
	previous := n.samples[m.ID+"/a"]
	clock.Add(100)
	if _, err := n.store.db.Exec(`CREATE TRIGGER refuse_archive_sample BEFORE UPDATE ON availability_heads WHEN NEW.sequence>OLD.sequence BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	n.nextSync = maxSafe
	n.mu.Unlock()
	n.tick()
	n.wg.Wait()
	if n.Error() == nil || !equalJSON(previous, n.samples[m.ID+"/a"]) {
		t.Fatal("failed sample published or did not fault")
	}
	rows, err := n.store.history(m.ID, 10)
	if err != nil || len(rows) != 1 || rows[0].CheckedAt != previous.CheckedAt {
		t.Fatal("failed sample pruned committed raw history", rows, err)
	}
	stored, ok, err := loadOne[Sample](n.store, "samples", m.ID+"/a")
	if err != nil || !ok || !equalJSON(stored, previous) {
		t.Fatal("sample escaped SQL rollback", err)
	}
}

func TestObservedArchiveOriginalFreshnessAndMaintenanceBoundary(t *testing.T) {
	var clock atomic.Int64
	base := maintenanceClock(11, 59) + 59950
	clock.Store(base)
	n := maintenanceNode(t, &clock, ":memory:")
	m := observedManual(t, n)
	storeMaintenance(t, n, plannedWork())
	s := Sample{Observation: Observation{CheckedAt: base, State: "up"}}
	until, held := n.localAvailabilityPolicy(m, s)
	if held || until != base+50 {
		t.Fatal("coverage crossed planned-maintenance start", until, held)
	}
	s.CheckedAt = maintenanceClock(12, 10) - 30
	until, held = n.localAvailabilityPolicy(m, s)
	if !held || until != maintenanceClock(12, 10) {
		t.Fatal("coverage crossed planned-maintenance end", until, held)
	}
	partitionDrive(t, n)
	clock.Store(base + 200)
	result := observedAPI(t, n, m.ID, adminToken, base, clock.Load())
	if result.Summary.UpMs != 50 || result.Summary.MaintenanceMs != 0 || result.Summary.UnknownMs != 150 {
		t.Fatal("unobserved schedule transition was projected as measured time", result.Summary)
	}
}
