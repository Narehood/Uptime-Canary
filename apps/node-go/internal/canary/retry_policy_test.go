package canary

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProbeRetriesPersistAndUseFailureSpacing(t *testing.T) {
	var now atomic.Int64
	now.Store(maintenanceClock(12, 0))
	var probes atomic.Int64
	r := webhook(t)
	o := Options{NodeID: "a", DBPath: filepath.Join(t.TempDir(), "policy.db"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: now.Load, NotificationDelayMs: Ptr(int64(0)), Probe: func(context.Context, Monitor) ProbeResult {
		probes.Add(1)
		return ProbeResult{State: "down", Message: "fixture failure"}
	}}
	newNode := func() *Node {
		n, err := New(o)
		if err != nil {
			t.Fatal(err)
		}
		n.nextSync = maxSafe
		t.Cleanup(func() { _ = n.Stop() })
		return n
	}
	n := newNode()
	in := input([]string{"a"}, "http://127.0.0.1:1", r.URL)
	in.IntervalMs, in.RetryIntervalMs, in.Retries = 1000, Ptr(int64(100)), 2
	m, err := n.writeMonitor(in, nil, "retry-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	tick := func() { n.tick(); n.wg.Wait() }
	tick()
	if s := n.samples[m.ID+"/a"]; s.State != "pending" || s.RawState != "down" || s.ConsecutiveFailures != 1 || len(n.episodes) != 0 || r.count("down") != 0 {
		t.Fatal("first retry was treated as an outage", s)
	}
	now.Add(99)
	tick()
	if probes.Load() != 1 {
		t.Fatal("retry ran before its due time")
	}
	now.Add(1)
	tick()
	if s := n.samples[m.ID+"/a"]; s.State != "pending" || s.ConsecutiveFailures != 2 {
		t.Fatal("retry spacing was ignored", s)
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = newNode()
	tick()
	if s := n.samples[m.ID+"/a"]; s.State != "down" || s.ConsecutiveFailures != 3 || s.DownSince == nil || r.count("down") != 1 {
		t.Fatal("restart lost retry progress", s, r.count("down"))
	}
	before := probes.Load()
	now.Add(100)
	tick()
	if probes.Load() != before+1 || r.count("down") != 1 {
		t.Fatal("confirmed down did not retain retry spacing/deduplication")
	}
	history, err := n.store.history(m.ID, 10)
	if err != nil || len(history) != 4 || history[0].State != "down" || history[2].RawState != "down" || history[2].State != "pending" {
		t.Fatal("retry policy hid raw observations", history, err)
	}
}

func TestPendingRetryCannotRecoverOrReplaceConfirmedOutage(t *testing.T) {
	var now atomic.Int64
	now.Store(maintenanceClock(12, 0))
	raw := "down"
	r := webhook(t)
	n, err := New(Options{NodeID: "a", DBPath: ":memory:", Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: now.Load, NotificationDelayMs: Ptr(int64(0)), Probe: func(context.Context, Monitor) ProbeResult { return ProbeResult{State: raw, Message: "fixture"} }})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Stop()
	n.nextSync = maxSafe
	in := input([]string{"a"}, "http://127.0.0.1:1", r.URL)
	in.Retries = 1
	m, err := n.writeMonitor(in, nil, "continuity", false)
	if err != nil {
		t.Fatal(err)
	}
	tick := func() { now.Add(100); n.tick(); n.wg.Wait() }
	tick()
	tick()
	if r.count("down") != 1 {
		t.Fatal("fixture did not confirm outage")
	}
	incident := r.all()[0].IncidentID
	run := *n.samples[m.ID+"/a"].DownSince
	for _, state := range []string{"pending", "down", "down", "pending", "down", "down"} {
		raw = state
		tick()
		if r.count("down") != 1 || r.count("recovery") != 0 {
			t.Fatal("pending invented recovery or another down", state)
		}
	}
	if s := n.samples[m.ID+"/a"]; s.DownSince == nil || *s.DownSince != run {
		t.Fatal("pending lost the ongoing down run", s)
	}
	raw = "up"
	tick()
	if r.count("recovery") != 1 || r.all()[1].IncidentID != incident {
		t.Fatal("real recovery lost the delivered incident identity")
	}
}

func TestProbePolicyRollbackAndConfigurationValidation(t *testing.T) {
	var now atomic.Int64
	now.Store(maintenanceClock(12, 0))
	n, err := New(Options{NodeID: "a", DBPath: ":memory:", Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: now.Load, NotificationDelayMs: Ptr(int64(0)), Probe: func(context.Context, Monitor) ProbeResult { return ProbeResult{State: "down", Message: "fixture"} }})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Stop()
	n.nextSync = maxSafe
	in := input([]string{"a"}, "http://127.0.0.1:1", "")
	in.Retries = 2
	m, err := n.writeMonitor(in, nil, "rollback-policy", false)
	if err != nil {
		t.Fatal(err)
	}
	n.tick()
	n.wg.Wait()
	before := n.samples[m.ID+"/a"]
	beforePolicy := n.probePolicies[m.ID]
	if _, err := n.store.db.Exec(`CREATE TRIGGER reject_policy BEFORE UPDATE ON records WHEN NEW.category='probePolicies' BEGIN SELECT RAISE(FAIL,'private injected diagnostic'); END`); err != nil {
		t.Fatal(err)
	}
	now.Add(100)
	n.tick()
	n.wg.Wait()
	if n.Error() == nil || strings.Contains(n.Error().Error(), "private") || !equalJSON(n.samples[m.ID+"/a"], before) || !equalJSON(n.probePolicies[m.ID], beforePolicy) {
		t.Fatal("failed policy commit published partial state", n.Error())
	}
	stored, _, err := loadOne[Sample](n.store, "samples", m.ID+"/a")
	history, histErr := n.store.history(m.ID, 10)
	if err != nil || histErr != nil || !equalJSON(stored, before) || len(history) != 1 {
		t.Fatal("sample/history were not rolled back with policy", err, histErr)
	}
	for _, field := range []map[string]any{{"retries": -1}, {"retries": 101}, {"retries": 1.5}, {"retries": nil}, {"retryIntervalMs": nil}, {"retryIntervalMs": 99}, {"retryIntervalMs": 86400001}} {
		b, _ := json.Marshal(in)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		for key, value := range field {
			body[key] = value
		}
		b, _ = json.Marshal(body)
		if _, err := parseMonitor(b, map[string]bool{"a": true}); err == nil {
			t.Fatal("accepted invalid retry configuration", field)
		}
	}
}
