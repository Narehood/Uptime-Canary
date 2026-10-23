package canary

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestManualStatusPersistsReplicatesAndRecoversSameIncident(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 0))
	r := webhook(t)
	o := Options{NodeID: "a", DBPath: filepath.Join(t.TempDir(), "manual.db"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: clock.Load, NotificationDelayMs: Ptr(int64(0)), Peers: []Peer{{ID: "b", URL: "http://127.0.0.1:1"}}}
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	server := httptest.NewServer(n)
	defer server.Close()
	n.url = server.URL
	in := input([]string{"a"}, "", r.URL)
	in.Type = "manual"
	status, b := api(t, n, "/api/monitors", "POST", in, adminToken)
	if status != 201 {
		t.Fatalf("manual creation: %d %s", status, b)
	}
	var m Monitor
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Target != "manual" {
		t.Fatal("manual target not canonical")
	}
	partitionDrive(t, n)
	if s := n.samples[m.ID+"/a"]; s.State != "pending" || len(n.episodes) != 0 {
		t.Fatal("unset manual status is not pending", s)
	}
	change := func(state string, token string) int {
		body, _ := json.Marshal(m.MonitorInput)
		fields := map[string]any{}
		_ = json.Unmarshal(body, &fields)
		fields["revision"], fields["manual"] = m.Revision, ManualMonitorOptions{State: state, Message: "Operator set " + state}
		status, b := api(t, n, "/api/monitors/"+m.ID, "PUT", fields, token)
		if status == 200 {
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatal(err)
			}
		}
		return status
	}
	if status, b := api(t, n, "/api/accounts", "POST", map[string]string{"name": "manual-viewer", "role": "viewer", "token": viewerToken}, adminToken); status != 201 {
		t.Fatalf("viewer fixture: %d %s", status, b)
	}
	if change("down", viewerToken) != 403 {
		t.Fatal("viewer changed manual status")
	}
	generation := checkGeneration(m)
	if change("down", adminToken) != 200 {
		t.Fatal("cannot set down")
	}
	partitionDrive(t, n)
	if r.count("down") != 1 || checkGeneration(m) != generation {
		t.Fatal("manual status edit reset continuity or did not notify")
	}
	incident := r.all()[0].IncidentID
	if change("pending", adminToken) != 200 {
		t.Fatal("cannot set pending")
	}
	partitionDrive(t, n)
	if r.count("recovery") != 0 {
		t.Fatal("manual pending invented recovery")
	}
	if change("up", adminToken) != 200 {
		t.Fatal("cannot set up")
	}
	partitionDrive(t, n)
	if r.count("recovery") != 1 || r.all()[len(r.all())-1].IncidentID != incident {
		t.Fatal("manual recovery did not close same incident", r.all())
	}
	peerOptions := o
	peerOptions.NodeID, peerOptions.DBPath, peerOptions.Peers = "b", ":memory:", []Peer{{ID: "a", URL: "http://127.0.0.1:1"}}
	peer, err := New(peerOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Stop()
	for _, page := range causalCapacityPages(t, n) {
		revisionJoinsApply(t, peer, page)
	}
	if got := peer.current(m.ID); got == nil || got.Manual == nil || got.Manual.State != "up" || checkGeneration(*got) != generation {
		t.Fatal("manual setting did not replicate")
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n, err = New(o)
	if err != nil {
		t.Fatal(err)
	}
	clock.Add(100)
	partitionDrive(t, n)
	history, err := n.store.history(m.ID, 10)
	if err != nil || len(history) != 5 || history[0].State != "up" || n.current(m.ID).Manual.State != "up" || r.count("down") != 1 || r.count("recovery") != 1 {
		t.Fatal("manual restart changed status/history or replayed", history, err)
	}
}

func TestManualValidationRejectsAmbiguousOptions(t *testing.T) {
	in := input([]string{"a"}, "", "")
	in.Type = "manual"
	b, _ := json.Marshal(in)
	base := strings.TrimSuffix(string(b), "}")
	for _, raw := range []string{`null`, `{"state":null}`, `{"state":"unknown"}`, `{"State":"down"}`, `{"state":"up","state":"down"}`, `{"state":1}`, `{"message":["text"]}`, `{"secret":"hidden"}`, `{"message":"` + strings.Repeat("x", 257) + `"}`} {
		if _, err := parseMonitor([]byte(base+`,"manual":`+raw+`}`), map[string]bool{"a": true}); err == nil {
			t.Fatal("invalid manual options accepted", raw)
		}
	}
	in.Manual = &ManualMonitorOptions{State: "up"}
	in.Type, in.Target = "http", "http://127.0.0.1:1/"
	if err := validateMonitor(&in, map[string]bool{"a": true}); err == nil {
		t.Fatal("manual options attached to network check")
	}
}
