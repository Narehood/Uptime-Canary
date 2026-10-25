package canary

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func gameInput(ids []string) MonitorInput {
	return MonitorInput{Name: "Game fixture", Type: "gamedig", Target: "fixture-server", IntervalMs: 100, TimeoutMs: 50, NodeIDs: ids, Enabled: false,
		GameDig: &GameDigMonitorOptions{Game: "minecraft", Port: 25565, GivenPortOnly: true, Endpoint: "http://127.0.0.1:3121", BearerToken: "private-game-fixture-0123456789", AllowInsecureHTTP: true}}
}

func TestGameDigPeerPersistenceGenerationAndPrivacy(t *testing.T) {
	directory := t.TempDir()
	c := cluster(t, []string{"a", "b"}, func(o *Options) { o.DBPath = filepath.Join(directory, o.NodeID+".sqlite") })
	n := c.nodes[0]
	m := add(t, n, gameInput([]string{"a", "b"}))
	assertPeer := func(want Monitor) {
		t.Helper()
		eventually(t, func() bool {
			peer := c.nodes[1]
			peer.mu.Lock()
			defer peer.mu.Unlock()
			got := peer.current(m.ID)
			return got != nil && got.Revision == want.Revision && equalJSON(got.GameDig, want.GameDig)
		})
	}
	assertPeer(m)
	if got := publicMonitor(m, "viewer"); got.GameDig != nil {
		t.Fatal("viewer received game query credentials")
	}
	metadata := m
	metadata.Name = "Game renamed"
	status, raw := api(t, n, "/api/monitors/"+m.ID, "PUT", metadata, adminToken)
	if status != 200 || json.Unmarshal(raw, &metadata) != nil || checkGeneration(metadata) != checkGeneration(m) {
		t.Fatal("game rename reset generation", status)
	}
	changed := metadata
	options := *metadata.GameDig
	options.Port++
	changed.GameDig = &options
	status, raw = api(t, n, "/api/monitors/"+m.ID, "PUT", changed, adminToken)
	// A new generation omits checkGeneration. Decode each response into a
	// fresh value, as a network client does, rather than retain the old field.
	var saved Monitor
	if status != 200 || json.Unmarshal(raw, &saved) != nil || checkGeneration(saved) == checkGeneration(m) {
		t.Fatal("game port edit retained stale generation", status)
	}
	changed = saved
	assertPeer(changed)
	if err := c.nodes[1].Stop(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{NodeID: "b", DBPath: filepath.Join(directory, "b.sqlite"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop()
	if got := reopened.current(m.ID); got == nil || got.Type != "gamedig" || !equalJSON(got.GameDig, changed.GameDig) || checkGeneration(*got) != checkGeneration(changed) {
		t.Fatal("game profile lost across peer restart")
	}
}

func TestGameDigExactMetadataAndForeignOptions(t *testing.T) {
	in := gameInput([]string{"a"})
	raw, _ := json.Marshal(in)
	if _, err := parseMonitor(raw, map[string]bool{"a": true}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"type", "gamedig", "target", "enabled", "deleted", "checkGeneration"} {
		var fields map[string]json.RawMessage
		json.Unmarshal(raw, &fields)
		value := fields[key]
		if value == nil {
			value = json.RawMessage(`false`)
			if key == "checkGeneration" {
				value = json.RawMessage(`"generation"`)
			}
		}
		fields[strings.ToUpper(key[:1])+key[1:]] = value
		bad, _ := json.Marshal(fields)
		var version Version
		if json.Unmarshal(append(append([]byte(`{"monitor":`), bad...), []byte(`,"ancestors":[]}`)...), &version) == nil {
			t.Fatal("game metadata alias admitted")
		}
	}
	in.Type = "http"
	if validateMonitor(&in, map[string]bool{"a": true}) == nil {
		t.Fatal("game credentials acquired a foreign monitor's authority")
	}
}

func TestGameDigPendingPreservesOutageWithInversion(t *testing.T) {
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
					return ProbeResult{State: value, Message: "Fixed Game query fixture", LatencyAvailable: Ptr(false)}
				}
			})
			input := gameInput([]string{"a"})
			input.Enabled, input.UpsideDown, input.Retries, input.WebhookURL = true, inverted, 1, hook.URL
			add(t, node, input)
			pending := func() bool {
				view := getOverview(t, node)
				return len(view.Monitors) == 1 && len(view.Monitors[0].Observations) == 1 && view.Monitors[0].Observations[0].State == "pending"
			}
			eventually(t, pending)
			if hook.count("down") != 0 {
				t.Fatal("unavailable Game query client created an outage")
			}
			state.Store(1)
			eventually(t, func() bool { return hook.count("down") == 1 })
			state.Store(0)
			eventually(t, pending)
			if hook.count("recovery") != 0 {
				t.Fatal("Pending Game query status invented recovery")
			}
			state.Store(2)
			eventually(t, func() bool { return hook.count("recovery") == 1 })
		})
	}
}
