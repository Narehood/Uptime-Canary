package canary

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSQLiteIdentityAccountsHistoryIncidentsAndReceipts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	r := webhook(t)
	var down atomic.Bool
	down.Store(true)
	options := Options{NodeName: "Persistent node", DBPath: path, Port: Ptr(0), ClusterToken: clusterToken, NotificationDelayMs: Ptr(int64(30)), SyncIntervalMs: Ptr(int64(60)), PeerTimeoutMs: Ptr(int64(60)), TickInterval: 15 * time.Millisecond, HistoryLimit: 3, Probe: controlled(&down)}
	first := options
	first.AdminToken = adminToken
	node, err := New(first)
	if err != nil {
		t.Fatal(err)
	}
	if err = node.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if node != nil {
			_ = node.Stop()
		}
	})
	id := node.ID
	m := add(t, node, input([]string{id}, "http://127.0.0.1:1", r.URL))
	g := addGroup(t, node, GroupInput{Name: "Persisted custom group", NodeIDs: []string{id}, GraceMs: 10000})
	code, b := api(t, node, "/api/accounts", "POST", map[string]string{"name": "Read only", "role": "viewer", "token": viewerToken}, adminToken)
	if code != 201 {
		t.Fatal(code, string(b))
	}
	eventually(t, func() bool { return len(getOverview(t, node).Notifications) == 1 })
	time.Sleep(420 * time.Millisecond)
	code, b = api(t, node, "/api/monitors/"+m.ID+"/history", "GET", nil, adminToken)
	var h []Observation
	if code != 200 || json.Unmarshal(b, &h) != nil || len(h) != 3 || h[0].CheckedAt < h[1].CheckedAt {
		t.Fatal(code, string(b))
	}
	if err = node.Stop(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{adminToken, viewerToken, clusterToken} {
		if strings.Contains(string(raw), token) {
			t.Fatal("Plaintext credential persisted")
		}
	}
	inspect, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := loadAll[account](inspect, "accounts")
	if err != nil {
		t.Fatal(err)
	}
	if err = inspect.close(); err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 {
		t.Fatal(accounts)
	}
	for _, a := range accounts {
		if len(a.Hash) != 64 {
			t.Fatal("Missing account digest")
		}
	}
	node, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = node.Start(); err != nil {
		t.Fatal(err)
	}
	if node.ID != id || node.Name != "Persistent node" {
		t.Fatal(node.ID, node.Name)
	}
	v := getOverview(t, node)
	if v.Monitors[0].Monitor.Revision != m.Revision || v.Groups[0].Group.Revision != g.Revision {
		t.Fatal(v)
	}
	code, _ = api(t, node, "/api/overview", "GET", nil, viewerToken)
	if code != 200 {
		t.Fatal(code)
	}
	time.Sleep(200 * time.Millisecond)
	if len(r.all()) != 1 {
		t.Fatal("Receipt lost across restart", r.all())
	}
	down.Store(false)
	eventually(t, func() bool { return len(getOverview(t, node).Notifications) == 2 })
	if err = node.Stop(); err != nil {
		t.Fatal(err)
	}
	node, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = node.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if len(r.all()) != 2 {
		t.Fatal("Recovery receipt lost", r.all())
	}
	down.Store(true)
	eventually(t, func() bool { return r.count("down") == 2 })
	if r.all()[0].IncidentID == r.all()[2].IncidentID {
		t.Fatal("Identity did not advance")
	}
	bad := options
	bad.NodeID = "other-id"
	if _, err := New(bad); err == nil {
		t.Fatal("Persisted identity could change")
	}
}
func TestRetryTimersAndKeysSurviveRestart(t *testing.T) {
	r := webhook(t)
	r.status.Store(503)
	options := Options{NodeID: "a", DBPath: filepath.Join(t.TempDir(), "state.db"), Port: Ptr(0), ClusterToken: clusterToken, NotificationDelayMs: Ptr(int64(20)), TickInterval: 15 * time.Millisecond, Probe: func(context.Context, Monitor) ProbeResult {
		return ProbeResult{State: "down", LatencyMs: 1, Message: "Down"}
	}}
	first := options
	first.AdminToken = adminToken
	node, err := New(first)
	if err != nil {
		t.Fatal(err)
	}
	if err = node.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Stop() })
	add(t, node, input([]string{"a"}, "http://127.0.0.1:1", r.URL))
	eventually(t, func() bool {
		node.mu.Lock()
		defer node.mu.Unlock()
		for _, a := range node.attempts {
			if a.Failures == 1 {
				return true
			}
		}
		return false
	})
	if len(getOverview(t, node).Notifications) != 0 {
		t.Fatal("Premature receipt")
	}
	key := r.all()[0].Key
	_ = node.Stop()
	r.status.Store(204)
	node, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = node.Start(); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return len(getOverview(t, node).Notifications) == 1 })
	if len(r.all()) != 2 || r.all()[1].Key != key {
		t.Fatal(r.all())
	}
}
