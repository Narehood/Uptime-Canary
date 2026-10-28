package canary

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"strings"
	"testing"
)

func browserInput() MonitorInput {
	return MonitorInput{Name: "Browser fixture", Type: "real-browser", Target: "https://fixture.example/", InternalOnly: true, IntervalMs: 1000, TimeoutMs: 500, NodeIDs: []string{"a"}, Enabled: false, Browser: &BrowserMonitorOptions{Endpoint: "http://127.0.0.1:9222", BearerToken: "private-browser-token", Screenshot: true}}
}

func TestBrowserMonitorExactAdmissionGenerationAndPrivacy(t *testing.T) {
	in := browserInput()
	raw, _ := json.Marshal(in)
	if _, err := parseMonitor(raw, map[string]bool{"a": true}); err != nil {
		t.Fatal("valid browser monitor refused", err)
	}
	for _, setting := range []string{`null`, `{}`, `{"endpoint":"http://127.0.0.1:9222","Endpoint":"http://127.0.0.1:1"}`, `{"endpoint":"http://127.0.0.1:9222","screenshot":null}`, `{"endpoint":"http://127.0.0.1:9222","command":"chrome"}`, `{"endpoint":"http://127.0.0.1:9222","delayMs":500}`} {
		var fields map[string]json.RawMessage
		json.Unmarshal(raw, &fields)
		fields["browser"] = json.RawMessage(setting)
		bad, _ := json.Marshal(fields)
		if _, err := parseMonitor(bad, map[string]bool{"a": true}); err == nil {
			t.Fatal("invalid browser settings admitted")
		}
	}
	for _, key := range []string{"type", "browser", "target", "enabled", "deleted", "checkGeneration"} {
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
			t.Fatal("peer browser metadata alias admitted")
		}
	}
	n := local(t, nil)
	m := add(t, n, in)
	public := publicMonitor(m, "viewer")
	if public.Browser != nil {
		t.Fatal("viewer received browser endpoint or credential")
	}
	metadata := m
	metadata.Name = "Renamed"
	if !sameCheck(m.MonitorInput, metadata.MonitorInput) {
		t.Fatal("browser rename changed measurement generation")
	}
	for _, change := range []func(*BrowserMonitorOptions){func(o *BrowserMonitorOptions) { o.Endpoint = "http://127.0.0.1:9223" }, func(o *BrowserMonitorOptions) { o.BearerToken = "rotated-token" }, func(o *BrowserMonitorOptions) { o.DelayMs = 1 }, func(o *BrowserMonitorOptions) { o.Screenshot = false }} {
		other := *m.Browser
		change(&other)
		metadata.Browser = &other
		if sameCheck(m.MonitorInput, metadata.MonitorInput) {
			t.Fatal("browser semantic edit retained generation")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := n.probe(ctx, m); r.State != "pending" || r.LatencyAvailable == nil || *r.LatencyAvailable || len(r.Screenshot) != 0 {
		t.Fatal("canceled browser invented an observation", r.State)
	}
}

func TestBrowserScreenshotsArePrivateLocalBoundedAndTransactional(t *testing.T) {
	n := local(t, nil)
	m := add(t, n, browserInput())
	var output bytes.Buffer
	if err := png.Encode(&output, image.NewRGBA(image.Rect(0, 0, 1024, 576))); err != nil {
		t.Fatal(err)
	}
	s := Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "up", CheckedAt: n.now()}, Revision: m.Revision, CheckGeneration: checkGeneration(m), Sequence: 1}
	n.mu.Lock()
	tx, err := n.store.db.Begin()
	if err != nil {
		n.mu.Unlock()
		t.Fatal(err)
	}
	err = saveBrowserScreenshot(tx, m, s, output.Bytes())
	if err == nil {
		err = tx.Commit()
	} else {
		tx.Rollback()
	}
	n.samples[m.ID+"/a"] = s
	n.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	status, body := api(t, n, "/api/monitors/"+m.ID+"/screenshot", "GET", nil, adminToken)
	var capture BrowserScreenshot
	if status != 200 || json.Unmarshal(body, &capture) != nil || !capture.Current || !bytes.Equal(capture.Data, output.Bytes()) || capture.Generation != checkGeneration(m) {
		t.Fatal("screenshot lost source/time identity", status)
	}
	if status, _ := api(t, n, "/api/accounts", "POST", map[string]string{"name": "Viewer", "role": "viewer", "token": viewerToken}, adminToken); status != 201 {
		t.Fatal(status)
	}
	for _, token := range []string{viewerToken, ""} {
		status, _ := api(t, n, "/api/monitors/"+m.ID+"/screenshot", "GET", nil, token)
		if status != 403 && status != 401 {
			t.Fatal("private screenshot exposed", status)
		}
	}
	status, body = api(t, n, "/api/overview", "GET", nil, viewerToken)
	if status != 200 || bytes.Contains(body, []byte("private-browser-token")) || bytes.Contains(body, []byte("image/png")) {
		t.Fatal("overview exposed private screenshot or browser profile")
	}
	n.mu.Lock()
	newer := s
	newer.Sequence++
	newer.State = "pending"
	n.samples[m.ID+"/a"] = newer
	n.mu.Unlock()
	status, body = api(t, n, "/api/monitors/"+m.ID+"/screenshot", "GET", nil, adminToken)
	json.Unmarshal(body, &capture)
	if status != 200 || capture.Current {
		t.Fatal("old screenshot presented as latest observation")
	}
	// An uncommitted replacement must never become visible.
	n.mu.Lock()
	tx, err = n.store.db.Begin()
	if err == nil {
		newer.CheckedAt++
		err = saveBrowserScreenshot(tx, m, newer, output.Bytes())
		tx.Rollback()
	}
	n.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	status, body = api(t, n, "/api/monitors/"+m.ID+"/screenshot", "GET", nil, adminToken)
	json.Unmarshal(body, &capture)
	if status != 200 || capture.CheckedAt != s.CheckedAt {
		t.Fatal("rolled-back capture published")
	}
	options := *m.Browser
	options.Endpoint = "http://127.0.0.1:9223"
	m.Browser = &options
	if status, _ := api(t, n, "/api/monitors/"+m.ID, "PUT", m, adminToken); status != 200 {
		t.Fatal(status)
	}
	if status, _ := api(t, n, "/api/monitors/"+m.ID+"/screenshot", "GET", nil, adminToken); status != 404 {
		t.Fatal("screenshot crossed measurement generation", status)
	}
	n.mu.Lock()
	var count int
	err = n.store.db.QueryRow(`SELECT COUNT(*) FROM browser_screenshots`).Scan(&count)
	n.mu.Unlock()
	if err != nil || count != 0 {
		t.Fatal("superseded screenshot retained")
	}
}
