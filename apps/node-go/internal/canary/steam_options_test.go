package canary

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func steamInput(ids []string) MonitorInput {
	return MonitorInput{Name: "Steam directory", Type: "steam", Target: "127.0.0.9:27015", InternalOnly: true, IntervalMs: 1000, TimeoutMs: 750,
		NodeIDs: ids, Enabled: false, Steam: &SteamMonitorOptions{APIKey: "fictional-private-steam-key", SkipLatency: true}}
}

func TestSteamOptionsDecodeAndForeignConfiguration(t *testing.T) {
	in := steamInput([]string{"a"})
	raw, _ := json.Marshal(in)
	if _, err := parseMonitor(raw, map[string]bool{"a": true}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`null`, `{}`, `{"apiKey":"x","apiKey":"y"}`, `{"APIKey":"x"}`, `{"apiKey":"x","skipLatency":null}`, `{"apiKey":"x","packetSize":0.5}`, `{"apiKey":"x","unknown":true}`} {
		object := map[string]json.RawMessage{}
		if json.Unmarshal(raw, &object) != nil {
			t.Fatal("fixture JSON")
		}
		object["steam"] = json.RawMessage(value)
		invalid, _ := json.Marshal(object)
		if _, err := parseMonitor(invalid, map[string]bool{"a": true}); err == nil {
			t.Fatal("ambiguous Steam options admitted", value)
		}
	}
	for _, kind := range []string{"http", "globalping", "manual", "push", "system-service"} {
		copy := in
		copy.Type = kind
		if validateMonitor(&copy, map[string]bool{"a": true}) == nil {
			t.Fatal("Steam credentials admitted under another type", kind)
		}
	}
	for _, edit := range []func(*MonitorInput){
		func(m *MonitorInput) { m.Steam = nil },
		func(m *MonitorInput) { m.Ping = &PingMonitorOptions{} },
		func(m *MonitorInput) { m.Target = "https://example.test/private" },
		func(m *MonitorInput) { m.ExpectedStatusCodes = []int{200} },
	} {
		copy := in
		edit(&copy)
		if validateMonitor(&copy, map[string]bool{"a": true}) == nil {
			t.Fatal("invalid Steam configuration admitted")
		}
	}
}

func TestSteamPeerPersistenceGenerationAndViewerRedaction(t *testing.T) {
	directory := t.TempDir()
	c := cluster(t, []string{"a", "b"}, func(o *Options) { o.DBPath = filepath.Join(directory, o.NodeID+".sqlite") })
	n := c.nodes[0]
	m := add(t, n, steamInput([]string{"a", "b"}))
	assertPeer := func(expected Monitor) {
		t.Helper()
		eventually(t, func() bool {
			peer := c.nodes[1]
			peer.mu.Lock()
			defer peer.mu.Unlock()
			saved := peer.current(m.ID)
			return saved != nil && equalJSON(saved.Steam, expected.Steam) && saved.Revision == expected.Revision
		})
	}
	assertPeer(m)
	if status, _ := api(t, n, "/api/accounts", "POST", map[string]string{"name": "Viewer", "role": "viewer", "token": viewerToken}, adminToken); status != 201 {
		t.Fatal(status)
	}
	for _, endpoint := range []string{"/api/overview", "/api/monitors", "/api/monitors/" + m.ID + "/conflicts"} {
		status, raw := api(t, n, endpoint, "GET", nil, viewerToken)
		if status != 200 || strings.Contains(string(raw), "fictional-private") || strings.Contains(string(raw), `"steam":`) {
			t.Fatal("viewer received directory credentials/options", endpoint, status)
		}
	}
	edit := func(current Monitor) Monitor {
		t.Helper()
		status, raw := api(t, n, "/api/monitors/"+m.ID, "PUT", current, adminToken)
		var saved Monitor
		if status != 200 || json.Unmarshal(raw, &saved) != nil {
			t.Fatal("Steam edit failed", status)
		}
		return saved
	}
	m.Name = "Directory renamed"
	metadata := edit(m)
	if checkGeneration(metadata) != checkGeneration(m) || !equalJSON(metadata.Steam, m.Steam) {
		t.Fatal("metadata edit changed directory generation/options")
	}
	options := *metadata.Steam
	options.APIKey = "fictional-private-steam-rotated"
	metadata.Steam = &options
	rotated := edit(metadata)
	if checkGeneration(rotated) == checkGeneration(m) {
		t.Fatal("credential replacement retained stale check generation")
	}
	assertPeer(rotated)
	if err := c.nodes[1].Stop(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{NodeID: "b", DBPath: filepath.Join(directory, "b.sqlite"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop()
	if saved := reopened.current(m.ID); saved == nil || !equalJSON(saved.Steam, rotated.Steam) || saved.Revision != rotated.Revision {
		t.Fatal("reopen lost directory configuration")
	}
}

func TestSteamRuntimeProviderPendingAbsenceAndRecovery(t *testing.T) {
	var provider atomic.Int32
	provider.Store(429)
	var listed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if provider.Load() != 200 {
			w.WriteHeader(int(provider.Load()))
			_, _ = w.Write([]byte(`{"error":"private-provider-detail"}`))
			return
		}
		if listed.Load() {
			_, _ = w.Write([]byte(`{"response":{"servers":[{"addr":"127.0.0.9:27015","name":"private-provider-detail"}]}}`))
		} else {
			_, _ = w.Write([]byte(`{"response":{"servers":[]}}`))
		}
	}))
	defer server.Close()
	n := local(t, nil)
	hook := webhook(t)
	in := steamInput([]string{"a"})
	in.Enabled, in.WebhookURL = true, hook.URL
	in.Steam.APIURL, in.Steam.AllowInsecureAPI = server.URL, true
	add(t, n, in)
	pending := func() bool {
		views := getOverview(t, n).Monitors
		return len(views) == 1 && len(views[0].Observations) == 1 && views[0].Observations[0].State == "pending"
	}
	eventually(t, pending)
	if hook.count("down") != 0 || hook.count("recovery") != 0 {
		t.Fatal("provider quota failure became a game outage")
	}
	provider.Store(200)
	eventually(t, func() bool { return hook.count("down") == 1 })
	provider.Store(429)
	eventually(t, pending)
	if hook.count("recovery") != 0 {
		t.Fatal("provider uncertainty recovered the outage")
	}
	listed.Store(true)
	provider.Store(200)
	eventually(t, func() bool { return hook.count("recovery") == 1 })
	view := getOverview(t, n).Monitors[0]
	observation := view.Observations[0]
	if hook.count("down") != 1 || observation.LatencyAvailable == nil || *observation.LatencyAvailable || observation.LatencyMs != 0 || observation.StatusCode != 0 || strings.Contains(observation.Message, "private-") {
		t.Fatal("directory HTTP result invented game RTT/status or leaked metadata")
	}
}

func TestSteamRuntimeAPITimeoutPolicy(t *testing.T) {
	for _, test := range []struct {
		name          string
		failureIsDown bool
		state         string
	}{{"pending", false, "pending"}, {"down", true, "down"}} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
			defer server.Close()
			n, hook := local(t, nil), webhook(t)
			in := steamInput([]string{"a"})
			in.Enabled, in.WebhookURL, in.TimeoutMs = true, hook.URL, 75
			in.Steam.APIURL, in.Steam.AllowInsecureAPI, in.Steam.APIFailureIsDown = server.URL, true, test.failureIsDown
			add(t, n, in)
			eventually(t, func() bool {
				views := getOverview(t, n).Monitors
				// The API includes an Unknown placeholder before the first sample.
				return len(views) == 1 && len(views[0].Observations) == 1 && views[0].Observations[0].CheckedAt > 0
			})
			observation := getOverview(t, n).Monitors[0].Observations[0]
			if observation.State != test.state || observation.LatencyAvailable == nil || *observation.LatencyAvailable || strings.Contains(observation.Message, in.Steam.APIKey) {
				t.Fatal("scheduled API deadline ignored policy or invented RTT/leaked a credential", observation)
			}
			if test.failureIsDown {
				eventually(t, func() bool { return hook.count("down") == 1 })
			} else if hook.count("down") != 0 {
				t.Fatal("uncertain provider timeout sent a game outage")
			}
		})
	}
}

func TestSteamConfigurationSQLRollbackAndRetry(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := maintenanceNode(t, &clock, filepath.Join(t.TempDir(), "node.db"))
	w := maintenanceAPIRequest(n, "POST", "/api/monitors", steamInput([]string{"a"}), adminToken)
	var monitor Monitor
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &monitor) != nil {
		t.Fatal(w.Code)
	}
	before := n.heads[monitor.ID][0]
	count := len(n.revisionEdges)
	if _, err := n.store.db.Exec(`CREATE TEMP TRIGGER steam_fail BEFORE INSERT ON records WHEN NEW.category='heads' BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	options := *monitor.Steam
	options.SkipLatency = false
	monitor.Steam = &options
	w = maintenanceAPIRequest(n, "PUT", "/api/monitors/"+monitor.ID, monitor, adminToken)
	if w.Code != 503 {
		t.Fatal("failed persistence was accepted", w.Code)
	}
	stored, _, err := loadOne[[]Version](n.store, "heads", monitor.ID)
	if err != nil || !equalJSON(stored, []Version{before}) || !equalJSON(stored, n.heads[monitor.ID]) || len(n.revisionEdges) != count {
		t.Fatal("failed Steam transaction escaped rollback", err)
	}
	if _, err := n.store.db.Exec(`DROP TRIGGER steam_fail`); err != nil {
		t.Fatal(err)
	}
	if retry := maintenanceAPIRequest(n, "PUT", "/api/monitors/"+monitor.ID, monitor, adminToken); retry.Code != 200 {
		t.Fatal("Steam retry failed", retry.Code)
	}
}
