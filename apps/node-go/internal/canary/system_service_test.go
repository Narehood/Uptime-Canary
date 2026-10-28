package canary

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func systemServiceInput(ids []string) MonitorInput {
	return MonitorInput{Name: "Local service", Type: "system-service", Target: "fixture-service", InternalOnly: true, IntervalMs: 100, TimeoutMs: 50, NodeIDs: ids, Enabled: false}
}

func TestSystemServiceMonitorValidationAndDefaultRouting(t *testing.T) {
	for _, target := range []string{"Spooler", "sshd.service", "app@instance.service"} {
		input := systemServiceInput([]string{"a"})
		input.Target = target
		if err := validateMonitor(&input, map[string]bool{"a": true}); err != nil {
			t.Fatal(target, err)
		}
	}
	for _, target := range []string{"", "-x", "with space", "../service", "a/b", "a*", "a;bad"} {
		input := systemServiceInput([]string{"a"})
		input.Target = target
		if validateMonitor(&input, map[string]bool{"a": true}) == nil {
			t.Fatal("invalid local service accepted", target)
		}
	}
	input := systemServiceInput([]string{"a"})
	input.Manual = &ManualMonitorOptions{State: "up"}
	if validateMonitor(&input, map[string]bool{"a": true}) == nil {
		t.Fatal("foreign options accepted")
	}
	n := local(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := n.probe(ctx, Monitor{MonitorInput: systemServiceInput([]string{"a"})})
	if result.State != "pending" || result.LatencyAvailable == nil || *result.LatencyAvailable {
		t.Fatal("default service routing bypassed isolated worker or invented latency", result)
	}
}

func TestSystemServiceMonitorPeerPersistenceAndGeneration(t *testing.T) {
	directory := t.TempDir()
	cluster := cluster(t, []string{"a", "b"}, func(o *Options) { o.DBPath = filepath.Join(directory, o.NodeID+".sqlite") })
	node := cluster.nodes[0]
	monitor := add(t, node, systemServiceInput([]string{"a", "b"}))
	assertPeer := func(expected Monitor) {
		t.Helper()
		eventually(t, func() bool {
			peer := cluster.nodes[1]
			peer.mu.Lock()
			defer peer.mu.Unlock()
			current := peer.current(monitor.ID)
			return current != nil && current.Type == "system-service" && current.Target == expected.Target && current.Revision == expected.Revision
		})
	}
	assertPeer(monitor)
	edit := func(monitor Monitor) Monitor {
		t.Helper()
		status, raw := api(t, node, "/api/monitors/"+monitor.ID, "PUT", monitor, adminToken)
		var result Monitor
		if status != 200 || json.Unmarshal(raw, &result) != nil {
			t.Fatal(status, string(raw))
		}
		return result
	}
	monitor.Name = "Service renamed"
	metadata := edit(monitor)
	if checkGeneration(metadata) != checkGeneration(monitor) {
		t.Fatal("metadata edit reset service generation")
	}
	metadata.Target = "fixture-service-rotated"
	changed := edit(metadata)
	if checkGeneration(changed) == checkGeneration(monitor) {
		t.Fatal("different local service retained generation")
	}
	assertPeer(changed)
	if err := cluster.nodes[1].Stop(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{NodeID: "b", DBPath: filepath.Join(directory, "b.sqlite"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop()
	if saved := reopened.current(monitor.ID); saved == nil || saved.Type != "system-service" || saved.Target != changed.Target || saved.Revision != changed.Revision {
		t.Fatal("service configuration lost across restart")
	}
}

func TestSystemServicePendingDownAndRecoveryPreserveLocalPolicy(t *testing.T) {
	var state atomic.Int32
	hook := webhook(t)
	node := local(t, func(o *Options) {
		o.Probe = func(context.Context, Monitor) ProbeResult {
			value := "pending"
			if state.Load() == 1 {
				value = "down"
			}
			if state.Load() == 2 {
				value = "up"
			}
			return ProbeResult{State: value, Message: "Fixed service fixture", LatencyAvailable: Ptr(false)}
		}
	})
	input := systemServiceInput([]string{"a"})
	input.Enabled = true
	input.WebhookURL = hook.URL
	add(t, node, input)
	latestPending := func() bool {
		overview := getOverview(t, node)
		return len(overview.Monitors) == 1 && len(overview.Monitors[0].Observations) == 1 && overview.Monitors[0].Observations[0].State == "pending"
	}
	eventually(t, latestPending)
	if hook.count("down") != 0 || hook.count("recovery") != 0 {
		t.Fatal("unavailable service manager became a delivered outage")
	}
	state.Store(1)
	eventually(t, func() bool { return hook.count("down") == 1 })
	state.Store(0)
	eventually(t, latestPending)
	if hook.count("recovery") != 0 {
		t.Fatal("unavailable manager invented recovery")
	}
	state.Store(2)
	eventually(t, func() bool { return hook.count("recovery") == 1 })
	view := getOverview(t, node).Monitors[0]
	if hook.count("down") != 1 || view.Observations[0].LatencyAvailable == nil || *view.Observations[0].LatencyAvailable {
		t.Fatal("service observation changed destination identity or invented RTT")
	}
}
