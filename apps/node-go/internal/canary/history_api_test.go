package canary

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestHistoryTimeWindowStatisticsAndRestart(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	path := filepath.Join(t.TempDir(), "history.db")
	n := maintenanceNode(t, &clock, path)
	m, err := n.writeMonitor(input([]string{"a"}, "http://127.0.0.1:1", ""), nil, "window-history", false)
	if err != nil {
		t.Fatal(err)
	}
	base := clock.Load()
	for index, row := range []struct {
		state   string
		latency float64
	}{{"up", 10}, {"down", 30}, {"pending", 20}, {"up", 40}} {
		o := Observation{MonitorID: m.ID, NodeID: n.ID, State: row.state, CheckedAt: base + int64(index+1)*100, LatencyMs: row.latency, Message: "fixture"}
		if err = appendHistory(n.store.db, o, 1000, 0); err != nil {
			t.Fatal(err)
		}
	}
	query := fmt.Sprintf("?from=%d&to=%d", base+100, base+400)
	check := func(n *Node) {
		t.Helper()
		w := maintenanceAPIRequest(n, "GET", "/api/monitors/"+m.ID+"/history"+query+"&limit=2", nil, adminToken)
		var rows []Observation
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rows) != nil || len(rows) != 2 || rows[0].CheckedAt != base+300 || rows[1].CheckedAt != base+200 {
			t.Fatal("window must filter before limit with inclusive start/exclusive end", w.Code, w.Body.String())
		}
		w = maintenanceAPIRequest(n, "GET", "/api/monitors/"+m.ID+"/statistics"+query, nil, adminToken)
		var s historyStatistics
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &s) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		if s.Count != 3 || s.Up != 1 || s.Down != 1 || s.Pending != 1 || s.NodeID != "a" || s.SampleAvailability == nil || *s.SampleAvailability != 0.5 || s.AverageLatencyMs == nil || *s.AverageLatencyMs != 20 || s.P50LatencyMs == nil || *s.P50LatencyMs != 20 || s.P95LatencyMs == nil || *s.P95LatencyMs != 30 || s.FirstCheckedAt == nil || *s.FirstCheckedAt != base+100 || s.LastCheckedAt == nil || *s.LastCheckedAt != base+300 {
			t.Fatal("statistics misrepresented retained local samples", s)
		}
	}
	check(n)
	if err = n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = maintenanceNode(t, &clock, path)
	check(n)
}

func TestHistoryEmptyUnknownAndInvalidQueries(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := maintenanceNode(t, &clock, ":memory:")
	m, err := n.writeMonitor(input([]string{"a"}, "http://127.0.0.1:1", ""), nil, "empty-history", false)
	if err != nil {
		t.Fatal(err)
	}
	w := maintenanceAPIRequest(n, "GET", "/api/monitors/"+m.ID+"/statistics", nil, adminToken)
	var s historyStatistics
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &s) != nil || s.Count != 0 || s.SampleAvailability != nil || s.P95LatencyMs != nil || s.FirstCheckedAt != nil {
		t.Fatal("empty data was reported as healthy", w.Code, w.Body.String())
	}
	for _, route := range []string{"history?from=2&to=1", "history?from=-1", "history?from=1&from=2", "history?limit=0", "history?limit=1001", "history?from=9007199254740992", "history?to=", "statistics?limit=1", "statistics?from=1.5", "statistics?unexpected=1"} {
		w = maintenanceAPIRequest(n, "GET", "/api/monitors/"+m.ID+"/"+route, nil, adminToken)
		if w.Code != 400 {
			t.Fatal("invalid history query accepted", route, w.Code)
		}
	}
	w = maintenanceAPIRequest(n, "GET", "/api/monitors/missing/statistics", nil, adminToken)
	if w.Code != 404 {
		t.Fatal("unknown monitor did not return 404", w.Code)
	}
	w = maintenanceAPIRequest(n, "GET", "/api/monitors/"+m.ID+"/statistics", nil, clusterToken)
	if w.Code != 401 {
		t.Fatal("cluster credential read local history", w.Code)
	}
}
