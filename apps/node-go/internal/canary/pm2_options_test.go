package canary

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func pm2Input(ids []string) MonitorInput {
	return MonitorInput{Name: "PM2 process", Type: "pm2", Target: "0042", IntervalMs: 100, TimeoutMs: 50,
		NodeIDs: ids, Enabled: false, PM2: &PM2MonitorOptions{Transport: "unix", Home: "/private-pm2", Namespace: "production"}}
}

func TestPM2VersionExactMetadataAdmission(t *testing.T) {
	m := Monitor{MonitorInput: pm2Input([]string{"a"}), ID: "pm2-fixture", Revision: "revision", UpdatedAt: 1,
		CheckGeneration: "generation"}
	monitor, _ := json.Marshal(m)
	for _, scope := range []string{"monitor", "version"} {
		keys := []string{"id", "revision", "updatedAt", "deleted", "checkGeneration", "pushStartedAt"}
		if scope == "version" {
			keys = []string{"monitor", "ancestors"}
		}
		for _, key := range keys {
			for _, conflicting := range []bool{false, true} {
				t.Run(scope+"/"+key+map[bool]string{false: "/alias-only", true: "/conflicting"}[conflicting], func(t *testing.T) {
					fields := map[string]json.RawMessage{}
					if scope == "monitor" {
						json.Unmarshal(monitor, &fields)
					} else {
						fields["monitor"], fields["ancestors"] = monitor, json.RawMessage(`[]`)
					}
					value := fields[key]
					if value == nil {
						value = json.RawMessage(`0`)
						if key == "deleted" {
							value = json.RawMessage(`false`)
						}
					}
					fields[key] = value
					exact, _ := json.Marshal(fields)
					if scope == "monitor" {
						exact = append(append([]byte(`{"monitor":`), exact...), []byte(`,"ancestors":[]}`)...)
					}
					var decoded Version
					if json.Unmarshal(exact, &decoded) != nil {
						t.Fatal("exact metadata key refused")
					}
					if !conflicting {
						delete(fields, key)
					}
					fields[strings.ToUpper(key[:1])+key[1:]] = value
					bad, _ := json.Marshal(fields)
					if scope == "monitor" {
						bad = append(append([]byte(`{"monitor":`), bad...), []byte(`,"ancestors":[]}`)...)
					}
					if json.Unmarshal(bad, &decoded) == nil {
						t.Fatal("PM2 version metadata alias was admitted")
					}
				})
			}
		}
	}
}

func TestPM2MonitorJSONAdmission(t *testing.T) {
	in := pm2Input([]string{"a"})
	raw, _ := json.Marshal(in)
	if parsed, err := parseMonitor(raw, map[string]bool{"a": true}); err != nil || parsed.Target != "0042" {
		t.Fatal("PM2 name/configuration did not round-trip", err)
	}
	for _, value := range []string{`null`, `{}`, `{"transport":"unix"}`, `{"transport":"unix","home":"/tmp","home":"/other"}`,
		`{"Transport":"unix","home":"/tmp"}`, `{"transport":"windows-pipe","home":""}`,
		`{"transport":"unix","home":"/tmp","namespace":null}`, `{"transport":"unix","home":"/tmp","namespace":""}`,
		`{"transport":"unix","home":"/tmp","unknown":true}`} {
		object := map[string]json.RawMessage{}
		json.Unmarshal(raw, &object)
		object["pm2"] = json.RawMessage(value)
		bad, _ := json.Marshal(object)
		if _, err := parseMonitor(bad, map[string]bool{"a": true}); err == nil {
			t.Fatal("malformed PM2 options were admitted")
		}
	}
	options, _ := json.Marshal(in.PM2)
	for _, invalid := range []string{
		strings.TrimSuffix(string(raw), "}") + `,"pm2":` + string(options) + `}`,
		strings.TrimSuffix(string(raw), "}") + `,"PM2":` + string(options) + `}`,
		strings.TrimSuffix(string(raw), "}") + `,"Target":"other"}`,
		strings.Replace(string(raw), `"target":"0042"`, `"target":"\ud800"`, 1),
		strings.Replace(string(raw), `"target":"0042"`, "\"target\":\"\xff\"", 1),
	} {
		if _, err := parseMonitor([]byte(invalid), map[string]bool{"a": true}); err == nil {
			t.Fatal("ambiguous PM2 monitor authority was admitted")
		}
		var version Version
		if json.Unmarshal([]byte(`{"monitor":`+invalid+`,"ancestors":[]}`), &version) == nil {
			t.Fatal("peer/persisted PM2 monitor bypassed raw admission")
		}
	}
	paired := strings.Replace(string(raw), `"target":"0042"`, `"target":"\ud83d\ude80"`, 1)
	if parsed, err := parseMonitor([]byte(paired), map[string]bool{"a": true}); err != nil || parsed.Target != "🚀" {
		t.Fatal("valid paired Unicode name refused", err)
	}
	for _, kind := range []string{"http", "steam", "system-service", "manual", "globalping", "push"} {
		other := in
		other.Type = kind
		if validateMonitor(&other, map[string]bool{"a": true}) == nil {
			t.Fatal("PM2 options acquired another monitor type's authority")
		}
	}
	missing := in
	missing.PM2 = nil
	if validateMonitor(&missing, map[string]bool{"a": true}) == nil {
		t.Fatal("PM2 monitor admitted without explicit connection configuration")
	}
	pipe := in
	pipe.PM2 = &PM2MonitorOptions{Transport: "windows-pipe"}
	if validateMonitor(&pipe, map[string]bool{"a": true}) != nil {
		t.Fatal("Windows connection configuration was not admitted independently of peer OS")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n := local(t, nil)
	result := n.probe(ctx, Monitor{MonitorInput: in})
	if result.State != "pending" || result.LatencyAvailable == nil || *result.LatencyAvailable || result.LatencyMs != 0 {
		t.Fatal("PM2 route bypassed the worker or invented manager latency", result)
	}
}

func TestPM2PeerPersistenceGenerationAndPrivacy(t *testing.T) {
	directory := t.TempDir()
	c := cluster(t, []string{"a", "b"}, func(o *Options) { o.DBPath = filepath.Join(directory, o.NodeID+".sqlite") })
	n := c.nodes[0]
	m := add(t, n, pm2Input([]string{"a", "b"}))
	assertPeer := func(expected Monitor) {
		t.Helper()
		eventually(t, func() bool {
			peer := c.nodes[1]
			peer.mu.Lock()
			defer peer.mu.Unlock()
			saved := peer.current(m.ID)
			return saved != nil && equalJSON(saved.PM2, expected.PM2) && saved.Revision == expected.Revision
		})
	}
	assertPeer(m)
	if status, _ := api(t, n, "/api/accounts", "POST", map[string]string{"name": "Viewer", "role": "viewer", "token": viewerToken}, adminToken); status != 201 {
		t.Fatal(status)
	}
	for _, endpoint := range []string{"/api/overview", "/api/monitors", "/api/monitors/" + m.ID + "/conflicts"} {
		status, body := api(t, n, endpoint, "GET", nil, viewerToken)
		if status != 200 || strings.Contains(string(body), "private-pm2") || strings.Contains(string(body), `"pm2":`) {
			t.Fatal("viewer received PM2 connection details", endpoint, status)
		}
	}
	edit := func(value Monitor) Monitor {
		t.Helper()
		status, body := api(t, n, "/api/monitors/"+m.ID, "PUT", value, adminToken)
		var saved Monitor
		if status != 200 || json.Unmarshal(body, &saved) != nil {
			t.Fatal("PM2 update failed", status)
		}
		return saved
	}
	m.Name = "PM2 renamed"
	metadata := edit(m)
	if checkGeneration(metadata) != checkGeneration(m) || !equalJSON(metadata.PM2, m.PM2) {
		t.Fatal("metadata edit changed PM2 semantics")
	}
	options := *metadata.PM2
	options.Namespace = "other-namespace"
	metadata.PM2 = &options
	changed := edit(metadata)
	if checkGeneration(changed) == checkGeneration(m) {
		t.Fatal("namespace edit retained stale PM2 generation")
	}
	assertPeer(changed)
	if err := c.nodes[1].Stop(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{NodeID: "b", DBPath: filepath.Join(directory, "b.sqlite"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop()
	if saved := reopened.current(m.ID); saved == nil || !equalJSON(saved.PM2, changed.PM2) || saved.Target != "0042" || saved.Revision != changed.Revision {
		t.Fatal("PM2 source/options lost across restart")
	}
}

func TestPM2PendingPreservesOutageWithInversion(t *testing.T) {
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
					return ProbeResult{State: value, Message: "Fixed PM2 fixture", LatencyAvailable: Ptr(false)}
				}
			})
			input := pm2Input([]string{"a"})
			input.Enabled, input.UpsideDown, input.Retries, input.WebhookURL = true, inverted, 1, hook.URL
			add(t, node, input)
			pending := func() bool {
				view := getOverview(t, node)
				return len(view.Monitors) == 1 && len(view.Monitors[0].Observations) == 1 && view.Monitors[0].Observations[0].State == "pending"
			}
			eventually(t, pending)
			if hook.count("down") != 0 {
				t.Fatal("unavailable PM2 manager created an outage")
			}
			state.Store(1)
			eventually(t, func() bool { return hook.count("down") == 1 })
			state.Store(0)
			eventually(t, pending)
			if hook.count("recovery") != 0 {
				t.Fatal("Pending PM2 status invented recovery")
			}
			state.Store(2)
			eventually(t, func() bool { return hook.count("recovery") == 1 })
		})
	}
}

func TestPM2ConnectionEditDiscardsInFlightCompletion(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	var fresh atomic.Int32
	hook := webhook(t)
	n := local(t, func(o *Options) {
		o.Probe = func(_ context.Context, m Monitor) ProbeResult {
			if m.PM2.Home == "/private-pm2" {
				select {
				case started <- struct{}{}:
				default:
				}
				<-release // A late manager result deliberately ignores cancellation.
				return ProbeResult{State: "down", Message: "PM2 process is not online", LatencyAvailable: Ptr(false)}
			}
			fresh.Add(1)
			return ProbeResult{State: "up", Message: "PM2 process is online", LatencyAvailable: Ptr(false)}
		}
	})
	defer close(release)
	input := pm2Input([]string{"a"})
	input.Enabled, input.WebhookURL = true, hook.URL
	m := add(t, n, input)
	eventually(t, func() bool { return len(started) != 0 })
	oldGeneration := checkGeneration(m)
	options := *m.PM2
	options.Home = "/new-private-pm2"
	m.PM2 = &options
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
		t.Fatal("late PM2 result from the previous connection opened an outage")
	}
}
