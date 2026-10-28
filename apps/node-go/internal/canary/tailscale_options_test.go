package canary

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func tailscaleInput(ids []string) MonitorInput {
	return MonitorInput{Name: "Tailnet peer", Type: "tailscale-ping", Target: "fixture-peer", InternalOnly: true, IntervalMs: 100, TimeoutMs: 50, NodeIDs: ids, Enabled: false}
}

func TestTailscaleMonitorAdmissionAndWorkerRouting(t *testing.T) {
	for _, target := range []string{"office-device", "peer.example.ts.net.", "100.64.0.2", "fd7a:115c:a1e0::1"} {
		in := tailscaleInput([]string{"a"})
		in.Target = target
		raw, _ := json.Marshal(in)
		if parsed, err := parseMonitor(raw, map[string]bool{"a": true}); err != nil || parsed.Target != target {
			t.Fatal("valid typed Tailscale target did not round-trip", err)
		}
	}
	for _, target := range []string{"", "--help", "-x", "node --help", "node:443", "https://node", "user@node", "bad_name", "fe80::1%eth0"} {
		in := tailscaleInput([]string{"a"})
		in.Target = target
		if validateMonitor(&in, map[string]bool{"a": true}) == nil {
			t.Fatal("invalid peer target admitted", target)
		}
	}
	in := tailscaleInput([]string{"a"})
	in.PM2 = &PM2MonitorOptions{Transport: "windows-pipe"}
	if validateMonitor(&in, map[string]bool{"a": true}) == nil {
		t.Fatal("foreign private monitor options accepted")
	}
	n := local(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := n.probe(ctx, Monitor{MonitorInput: tailscaleInput([]string{"a"})})
	if r.State != "pending" || r.LatencyMs != 0 || r.LatencyAvailable == nil || *r.LatencyAvailable {
		t.Fatal("canceled Tailscale route became an outage or gained latency", r)
	}
}

func TestTailscalePeerLiveObservationsRestartAndGeneration(t *testing.T) {
	directory := t.TempDir()
	c := cluster(t, []string{"a", "b"}, func(o *Options) {
		o.DBPath = filepath.Join(directory, o.NodeID+".sqlite")
		o.Probe = func(context.Context, Monitor) ProbeResult {
			return ProbeResult{State: "up", Message: "Tailscale peer replied", LatencyMs: 2.5, LatencyAvailable: Ptr(true)}
		}
	})
	n := c.nodes[0]
	in := tailscaleInput([]string{"a", "b"})
	in.Enabled = true
	m := add(t, n, in)
	assertBoth := func(expected Monitor) {
		t.Helper()
		eventually(t, func() bool {
			for _, node := range c.nodes {
				view := getOverview(t, node)
				if len(view.Monitors) != 1 || view.Monitors[0].Monitor.Revision != expected.Revision || len(view.Monitors[0].Observations) != 2 {
					return false
				}
				for _, sample := range view.Monitors[0].Observations {
					if sample.State != "up" || sample.LatencyMs != 2.5 || sample.LatencyAvailable == nil || !*sample.LatencyAvailable {
						return false
					}
				}
			}
			return true
		})
	}
	assertBoth(m)
	edit := func(value Monitor) Monitor {
		t.Helper()
		status, body := api(t, n, "/api/monitors/"+m.ID, "PUT", value, adminToken)
		var saved Monitor
		if status != 200 || json.Unmarshal(body, &saved) != nil {
			t.Fatal("Tailscale update failed", status)
		}
		return saved
	}
	m.Name = "Tailnet peer renamed"
	metadata := edit(m)
	if checkGeneration(metadata) != checkGeneration(m) {
		t.Fatal("rename reset measurement generation")
	}
	metadata.Target = "other-fixture-peer"
	changed := edit(metadata)
	if checkGeneration(changed) == checkGeneration(m) {
		t.Fatal("peer target edit reused an old generation")
	}
	assertBoth(changed)
	if err := c.nodes[1].Stop(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{NodeID: "b", DBPath: filepath.Join(directory, "b.sqlite"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop()
	if saved := reopened.current(m.ID); saved == nil || saved.Type != "tailscale-ping" || saved.Target != changed.Target || saved.Revision != changed.Revision || checkGeneration(*saved) != checkGeneration(changed) {
		t.Fatal("Tailscale configuration/generation lost across restart")
	}
}

func TestTailscalePendingPreservesOutageWithInversion(t *testing.T) {
	for _, inverted := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "inverted"}[inverted], func(t *testing.T) {
			var state atomic.Int32
			hook := webhook(t)
			node := local(t, func(o *Options) {
				o.Probe = func(context.Context, Monitor) ProbeResult {
					value := "pending"
					if state.Load() == 1 {
						value = "down"
						if inverted {
							value = "up"
						}
					}
					if state.Load() == 2 {
						value = "up"
						if inverted {
							value = "down"
						}
					}
					return ProbeResult{State: value, Message: "Fixed Tailscale fixture", LatencyAvailable: Ptr(false)}
				}
			})
			input := tailscaleInput([]string{"a"})
			input.Enabled, input.UpsideDown, input.Retries, input.WebhookURL = true, inverted, 1, hook.URL
			add(t, node, input)
			pending := func() bool {
				view := getOverview(t, node)
				return len(view.Monitors) == 1 && len(view.Monitors[0].Observations) == 1 && view.Monitors[0].Observations[0].State == "pending"
			}
			eventually(t, pending)
			if hook.count("down") != 0 {
				t.Fatal("unavailable Tailscale client created an outage")
			}
			state.Store(1)
			eventually(t, func() bool { return hook.count("down") == 1 })
			state.Store(0)
			eventually(t, pending)
			if hook.count("recovery") != 0 {
				t.Fatal("Pending Tailscale status invented recovery")
			}
			state.Store(2)
			eventually(t, func() bool { return hook.count("recovery") == 1 })
		})
	}
}

func TestTailscaleTargetEditDiscardsInFlightCompletion(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	var fresh atomic.Int32
	hook := webhook(t)
	n := local(t, func(o *Options) {
		o.Probe = func(_ context.Context, m Monitor) ProbeResult {
			if m.Target == "fixture-peer" {
				select {
				case started <- struct{}{}:
				default:
				}
				<-release // A late client result deliberately ignores cancellation.
				return ProbeResult{State: "down", Message: "Tailscale process is not online", LatencyAvailable: Ptr(false)}
			}
			fresh.Add(1)
			return ProbeResult{State: "up", Message: "Tailscale process is online", LatencyAvailable: Ptr(false)}
		}
	})
	defer close(release)
	input := tailscaleInput([]string{"a"})
	input.Enabled, input.WebhookURL = true, hook.URL
	m := add(t, n, input)
	eventually(t, func() bool { return len(started) != 0 })
	oldGeneration := checkGeneration(m)
	m.Target = "other-fixture-peer"
	status, raw := api(t, n, "/api/monitors/"+m.ID, "PUT", m, adminToken)
	if status != 200 || json.Unmarshal(raw, &m) != nil || checkGeneration(m) == oldGeneration {
		t.Fatal("connection edit did not publish a new generation", status)
	}
	release <- struct{}{}
	eventually(t, func() bool {
		view := getOverview(t, n).Monitors[0]
		return fresh.Load() >= 2 && len(view.Observations) == 1 && view.Observations[0].State == "up"
	})
	if hook.count("down") != 0 || len(getOverview(t, n).Incidents) != 0 {
		t.Fatal("late Tailscale result from the previous connection opened an outage")
	}
}
