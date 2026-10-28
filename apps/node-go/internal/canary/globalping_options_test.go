package canary

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gp "uptime-canary/apps/node-go/internal/globalping"
)

func gpInput(ids []string) MonitorInput {
	return MonitorInput{Name: "Remote check", Type: "globalping", Target: "https://example.com/private-path?key=private-query", InternalOnly: true, IntervalMs: 1000, TimeoutMs: 900, NodeIDs: ids, Enabled: false,
		Globalping: &GlobalpingMonitorOptions{Mode: "http", Location: "private-location", Token: "private-provider-token", HTTP: &gp.HTTPOptions{Headers: map[string]string{"Authorization": "private-target-secret"}, Keyword: "healthy"}}}
}

func TestGlobalpingOptionsExactDecodeAndTypeValidation(t *testing.T) {
	valid := []string{
		`{"mode":"ping","location":"US","ping":{}}`,
		`{"mode":"http","location":"US","http":{"headers":{},"acceptedStatuses":[{"min":200,"max":299}],"jsonata":{"expression":"ok","operator":"==","expected":true}}}`,
		`{"mode":"dns","location":"US","dns":{"queryType":"TXT","allowEmptyAnswers":true}}`,
	}
	for _, value := range valid {
		var o GlobalpingMonitorOptions
		if err := json.Unmarshal([]byte(value), &o); err != nil {
			t.Fatal(err)
		}
	}
	invalid := []string{
		`null`, `{"mode":"ping","Mode":"http","location":"US","ping":{}}`,
		`{"mode":"ping","mode":"ping","location":"US","ping":{}}`,
		`{"mode":"ping","location":"US","ping":null}`,
		`{"mode":"ping","location":"US","ping":{"Packets":1}}`,
		`{"mode":"ping","location":"US","ping":{"packets":1,"packets":2}}`,
		`{"mode":"dns","location":"US","dns":{"unknown":true}}`,
		`{"mode":"http","location":"US","http":{"headers":{"X-Key":"a","x-key":"b"}}}`,
		`{"mode":"http","location":"US","http":{"headers":{"X-Key":"a","X-Key":"b"}}}`,
		`{"mode":"http","location":"US","http":{"headers":{"X-Key":null}}}`,
		`{"mode":"http","location":"US","http":{"jsonata":{"expression":"ok","Operator":"==","expected":true}}}`,
		`{"mode":"http","location":"US","http":{"acceptedStatuses":[{"min":200,"Min":299,"max":299}]}}`,
	}
	for _, value := range invalid {
		var o GlobalpingMonitorOptions
		if err := json.Unmarshal([]byte(value), &o); err == nil {
			t.Fatalf("accepted ambiguous options %s", value)
		}
	}
	in := gpInput([]string{"a"})
	if err := validateMonitor(&in, map[string]bool{"a": true}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"http", "ping", "manual", "push", "redis"} {
		copy := in
		copy.Type = kind
		if validateMonitor(&copy, map[string]bool{"a": true}) == nil {
			t.Fatal("accepted foreign options", kind)
		}
	}
	for _, edit := range []func(*MonitorInput){
		func(m *MonitorInput) { m.Globalping = nil },
		func(m *MonitorInput) { m.Ping = &PingMonitorOptions{} },
		func(m *MonitorInput) { m.ExpectedStatusCodes = []int{200} },
		func(m *MonitorInput) { m.Target = "https://user:password@example.com" },
	} {
		copy := in
		edit(&copy)
		if validateMonitor(&copy, map[string]bool{"a": true}) == nil {
			t.Fatal("accepted invalid Globalping monitor")
		}
	}
}

func TestGlobalpingOptionsPeerPersistenceGenerationAndViewer(t *testing.T) {
	dir := t.TempDir()
	c := cluster(t, []string{"a", "b"}, func(o *Options) { o.DBPath = filepath.Join(dir, o.NodeID+".sqlite") })
	n := c.nodes[0]
	m := add(t, n, gpInput([]string{"a", "b"}))
	assertPeer := func(expected Monitor) {
		t.Helper()
		eventually(t, func() bool {
			peer := c.nodes[1]
			peer.mu.Lock()
			defer peer.mu.Unlock()
			saved := peer.current(m.ID)
			return saved != nil && equalJSON(saved.Globalping, expected.Globalping) && saved.Revision == expected.Revision
		})
	}
	assertPeer(m)
	if code, _ := api(t, n, "/api/accounts", "POST", map[string]string{"name": "Viewer", "role": "viewer", "token": viewerToken}, adminToken); code != 201 {
		t.Fatal(code)
	}
	for _, endpoint := range []string{"/api/overview", "/api/monitors"} {
		code, data := api(t, n, endpoint, "GET", nil, viewerToken)
		if code != 200 || strings.Contains(string(data), "private-") || strings.Contains(string(data), "globalping\":{") {
			t.Fatal("viewer received private measurement options", endpoint, code)
		}
	}
	edit := func(current Monitor) Monitor {
		t.Helper()
		code, data := api(t, n, "/api/monitors/"+m.ID, "PUT", current, adminToken)
		var result Monitor
		if code != 200 || json.Unmarshal(data, &result) != nil {
			t.Fatal("edit", code, string(data))
		}
		return result
	}
	m.Name = "Remote renamed"
	meta := edit(m)
	if checkGeneration(meta) != checkGeneration(m) || !equalJSON(meta.Globalping, m.Globalping) {
		t.Fatal("rename changed measurement generation/options")
	}
	options := *meta.Globalping
	options.Token = "private-rotated-token"
	meta.Globalping = &options
	rotated := edit(meta)
	if checkGeneration(rotated) == checkGeneration(m) {
		t.Fatal("credential edit reused stale generation")
	}
	assertPeer(rotated)
	if err := c.nodes[1].Stop(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{NodeID: "b", DBPath: filepath.Join(dir, "b.sqlite"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop()
	if saved := reopened.current(m.ID); saved == nil || !equalJSON(saved.Globalping, rotated.Globalping) || saved.Revision != rotated.Revision {
		t.Fatal("restart lost options")
	}
}

func TestGlobalpingRuntimePendingDownRecoveryAndRemoteLatency(t *testing.T) {
	var apiStatus atomic.Int32
	apiStatus.Store(429)
	var targetStatus atomic.Int32
	targetStatus.Store(503)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if apiStatus.Load() != 200 {
			w.WriteHeader(int(apiStatus.Load()))
			_, _ = w.Write([]byte(`{"error":"private-provider-error"}`))
			return
		}
		if r.Method == "POST" {
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"id":"local_measurement","probesCount":1}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "local_measurement", "type": "http", "target": "example.com", "createdAt": time.Now().UTC().Format(time.RFC3339Nano), "status": "finished", "probesCount": 1,
			"results": []any{map[string]any{"result": map[string]any{"status": "finished", "statusCode": targetStatus.Load(), "rawBody": "healthy", "tls": map[string]bool{"authorized": true}, "timings": map[string]any{"total": 7.25}, "truncated": false}}}})
	}))
	defer server.Close()
	hook := webhook(t)
	n := local(t, nil)
	in := gpInput([]string{"a"})
	in.Enabled = true
	in.WebhookURL = hook.URL
	in.Globalping.APIURL = server.URL
	in.Globalping.AllowInsecureAPI = true
	m := add(t, n, in)
	eventually(t, func() bool {
		v := getOverview(t, n)
		return len(v.Monitors) == 1 && len(v.Monitors[0].Observations) == 1 && v.Monitors[0].Observations[0].State == "pending"
	})
	if hook.count("down") != 0 || hook.count("recovery") != 0 {
		t.Fatal("provider quota failure created incident delivery")
	}
	apiStatus.Store(200)
	eventually(t, func() bool { return hook.count("down") == 1 })
	view := getOverview(t, n).Monitors[0]
	if view.Monitor.ID != m.ID || len(view.Observations) != 1 || view.Observations[0].LatencyAvailable == nil || !*view.Observations[0].LatencyAvailable || view.Observations[0].LatencyMs != 7.25 || strings.Contains(view.Observations[0].Message, "private-") {
		t.Fatal("remote measurement lost latency/privacy", view)
	}
	targetStatus.Store(200)
	eventually(t, func() bool { return hook.count("recovery") == 1 })
	if hook.count("down") != 1 {
		t.Fatal("connected single node repeated original down")
	}
}

func TestGlobalpingOptionsSQLRollback(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := maintenanceNode(t, &clock, filepath.Join(t.TempDir(), "node.db"))
	w := maintenanceAPIRequest(n, "POST", "/api/monitors", gpInput([]string{"a"}), adminToken)
	var m Monitor
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &m) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	before := n.heads[m.ID][0]
	count := len(n.revisionEdges)
	if _, err := n.store.db.Exec(`CREATE TEMP TRIGGER globalping_fail BEFORE INSERT ON records WHEN NEW.category='heads' BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	copy := *m.Globalping
	copy.Location = "new location"
	m.Globalping = &copy
	if w = maintenanceAPIRequest(n, "PUT", "/api/monitors/"+m.ID, m, adminToken); w.Code != 503 {
		t.Fatal("accepted failed persistence", w.Code)
	}
	stored, _, err := loadOne[[]Version](n.store, "heads", m.ID)
	if err != nil || !equalJSON(stored, []Version{before}) || !equalJSON(stored, n.heads[m.ID]) || len(n.revisionEdges) != count {
		t.Fatal("failed options transaction escaped rollback", err)
	}
	if _, err := n.store.db.Exec(`DROP TRIGGER globalping_fail`); err != nil {
		t.Fatal(err)
	}
	if w = maintenanceAPIRequest(n, "PUT", "/api/monitors/"+m.ID, m, adminToken); w.Code != 200 {
		t.Fatal("retry", w.Code, w.Body.String())
	}
}
