package canary

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	gp "uptime-canary/apps/node-go/internal/globalping"
	"uptime-canary/apps/node-go/internal/jsonquery"
)

// This regression uses only the in-process management handler and an in-memory
// SQLite node. The monitor remains disabled; no provider/notification is called.
func TestGlobalpingReviewViewerNormalizedHTTPMode(t *testing.T) {
	for _, mode := range []string{"http", "HTTP", " http ", " HTTP "} {
		t.Run(mode, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(12, 1))
			n := maintenanceNode(t, &clock, ":memory:")
			in := gpInput([]string{"a"})
			in.Globalping.Mode = mode
			w := maintenanceAPIRequest(n, "POST", "/api/monitors", in, adminToken)
			var m Monitor
			if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &m) != nil {
				t.Fatalf("create: %d %s", w.Code, w.Body.String())
			}
			w = maintenanceAPIRequest(n, "POST", "/api/accounts", map[string]string{"name": "Review viewer", "role": "viewer", "token": viewerToken}, adminToken)
			if w.Code != 201 {
				t.Fatalf("viewer account: %d", w.Code)
			}
			for _, endpoint := range []string{"/api/overview", "/api/monitors", "/api/monitors/" + m.ID + "/conflicts"} {
				w = maintenanceAPIRequest(n, "GET", endpoint, nil, viewerToken)
				if w.Code != 200 {
					t.Fatalf("%s: %d", endpoint, w.Code)
				}
				if strings.Contains(w.Body.String(), "private-") {
					t.Errorf("%s leaks private target path/query for admitted mode %q", endpoint, mode)
				}
				if !strings.Contains(w.Body.String(), `"target":"https://example.com/"`) {
					t.Errorf("%s: expected origin-only target for admitted mode %q", endpoint, mode)
				}
				if strings.Contains(w.Body.String(), `"globalping":`) {
					t.Errorf("%s: private options leaked", endpoint)
				}
			}
		})
	}
}

func gpReviewCreate(t *testing.T, n *Node) Monitor {
	t.Helper()
	w := maintenanceAPIRequest(n, "POST", "/api/monitors", gpInput([]string{"a"}), adminToken)
	var m Monitor
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &m) != nil {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	return m
}

func gpReviewEdit(t *testing.T, n *Node, m Monitor) Monitor {
	t.Helper()
	w := maintenanceAPIRequest(n, "PUT", "/api/monitors/"+m.ID, m, adminToken)
	var out Monitor
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("edit: %d %s", w.Code, w.Body.String())
	}
	return out
}

func gpReviewRoundTrip[T any](t *testing.T, v T) T {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err = json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGlobalpingReviewStrictNestedNullAliases(t *testing.T) {
	for _, raw := range []string{
		`{"mode":"http","location":"US","token":null,"http":{}}`,
		`{"mode":"http","location":"US","http":{"headers":null}}`,
		`{"mode":"http","location":"US","http":{"acceptedStatuses":null}}`,
		`{"mode":"http","location":"US","http":{"acceptedStatuses":[null]}}`,
		`{"mode":"http","location":"US","http":{"acceptedStatuses":[{"min":null,"max":299}]}}`,
		`{"mode":"http","location":"US","http":{"ignoreTls":null}}`,
		`{"mode":"http","location":"US","http":{"jsonata":null}}`,
		`{"mode":"http","location":"US","http":{"jsonata":{"expression":"ok","operator":"==","expected":null}}}`,
		`{"mode":"http","location":"US","http":{"headers":{"X-Review":"a","\u0058-Review":"b"}}}`,
		`{"mode":"http","location":"US","http":{"headers":{"Authorization":"a","authorization":"b"}}}`,
		`{"mode":"http","location":"US","http":{"acceptedStatuses":[{"min":200,"max":299,"\u006din":201}]}}`,
		`{"mode":"http","location":"US","http":{"jsonata":{"expression":"ok","operator":"==","Operator":"!=","expected":true}}}`,
	} {
		var options GlobalpingMonitorOptions
		if err := json.Unmarshal([]byte(raw), &options); err == nil {
			t.Errorf("admitted ambiguous/null nested options: %s", raw)
		}
	}
}

func TestGlobalpingReviewGenerationAndRollbackCancellation(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := maintenanceNode(t, &clock, ":memory:")
	m := gpReviewCreate(t, n)
	generation := checkGeneration(m)
	m.Name = "metadata rename"
	m = gpReviewEdit(t, n, m)
	if checkGeneration(m) != generation {
		t.Fatal("metadata edit retired semantic generation")
	}
	for _, edit := range []struct {
		name   string
		change func(*GlobalpingMonitorOptions)
	}{
		{"location", func(o *GlobalpingMonitorOptions) { o.Location = "review changed location" }},
		{"header map", func(o *GlobalpingMonitorOptions) { o.HTTP.Headers["Authorization"] = "review changed target token" }},
		{"status slice", func(o *GlobalpingMonitorOptions) { o.HTTP.AcceptedStatuses = []gp.StatusRange{{Min: 200, Max: 201}} }},
		{"query config", func(o *GlobalpingMonitorOptions) {
			o.HTTP.JSONata = &jsonquery.Config{Expression: "ok", Operator: jsonquery.Equal, Expected: json.RawMessage(`true`)}
		}},
	} {
		t.Run(edit.name, func(t *testing.T) {
			previous := gpReviewRoundTrip(t, n.heads[m.ID][0])
			beforeHash := contentID(previous.Monitor)
			draft := gpReviewRoundTrip(t, m)
			edit.change(draft.Globalping)
			updated := gpReviewEdit(t, n, draft)
			if checkGeneration(updated) == checkGeneration(m) {
				t.Fatal("semantic option change preserved generation")
			}
			if contentID(previous.Monitor) != beforeHash || !n.graph.matches(previous.EdgeID, "monitor", m.ID, previous.Monitor.Revision, previous.Monitor, previous.Ancestors) {
				t.Fatal("option edit changed previous immutable commitment")
			}
			if !n.monitorVersionReady(n.heads[m.ID][0]) {
				t.Fatal("new option payload not committed by revision DAG")
			}
			m = updated
		})
	}
	before := gpReviewRoundTrip(t, n.heads[m.ID])
	edgeCount := len(n.revisionEdges)
	var cancelled atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n.probing[m.ID] = func() { cancelled.Add(1); cancel() }
	n.nextProbe[m.ID] = 12345
	if _, err := n.store.db.Exec(`CREATE TEMP TRIGGER gp_review_abort BEFORE INSERT ON records WHEN NEW.category='heads' BEGIN SELECT RAISE(ABORT,'review rollback'); END`); err != nil {
		t.Fatal(err)
	}
	m.Globalping.Token = "review-replacement-provider-token"
	w := maintenanceAPIRequest(n, "PUT", "/api/monitors/"+m.ID, m, adminToken)
	if w.Code != 503 {
		t.Fatalf("rollback status: %d", w.Code)
	}
	stored, _, err := loadOne[[]Version](n.store, "heads", m.ID)
	if err != nil || !equalJSON(before, n.heads[m.ID]) || !equalJSON(before, stored) || len(n.revisionEdges) != edgeCount || cancelled.Load() != 0 || ctx.Err() != nil || n.nextProbe[m.ID] != 12345 {
		t.Fatal("failed SQL commit changed authority, probe schedule, or cancellation", err)
	}
	if _, err = n.store.db.Exec(`DROP TRIGGER gp_review_abort`); err != nil {
		t.Fatal(err)
	}
	m = gpReviewEdit(t, n, m)
	if cancelled.Load() != 1 || ctx.Err() != context.Canceled {
		t.Fatal("successful semantic commit failed to cancel old probe")
	}
}

func TestGlobalpingReviewPeerProofPagingTamperingRestart(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	source := maintenanceNode(t, &clock, ":memory:")
	m := gpReviewCreate(t, source)
	receiverOptions := Options{NodeID: "b", DBPath: filepath.Join(t.TempDir(), "peer.sqlite"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Peers: []Peer{{ID: "a", URL: "http://127.0.0.1:1"}}, Now: clock.Load}
	receiver, err := New(receiverOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = receiver.Stop() }()
	page, err := source.snapshotPage("", "")
	if err != nil {
		t.Fatal(err)
	}
	admit := func(s Snapshot) (Snapshot, error) {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		return receiver.validateSnapshot(b, "a")
	}
	old := gpReviewRoundTrip(t, page)
	old.Protocol = 11
	if _, err = admit(old); err == nil {
		t.Fatal("protocol 11 admitted Globalping configuration")
	}
	heads := gpReviewRoundTrip(t, page)
	heads.RevisionEdges = nil
	heads.RevisionClaims = nil
	parsed, err := admit(heads)
	if err != nil {
		t.Fatal(err)
	}
	if err = receiver.applySnapshot(parsed); err != nil {
		t.Fatal(err)
	}
	if receiver.current(m.ID) != nil || !receiver.monitorViews("admin")[0].ConfigConflict {
		t.Fatal("incomplete proof authorized a Globalping monitor")
	}
	proof := gpReviewRoundTrip(t, page)
	proof.Versions = []Version{}
	parsed, err = admit(proof)
	if err != nil {
		t.Fatal(err)
	}
	if err = receiver.applySnapshot(parsed); err != nil {
		t.Fatal(err)
	}
	saved := receiver.current(m.ID)
	if saved == nil || !equalJSON(saved.Globalping, m.Globalping) {
		t.Fatal("separate proof page did not restore exact Globalping options")
	}
	for _, field := range []string{"token", "location", "headers"} {
		t.Run(field, func(t *testing.T) {
			forged := gpReviewRoundTrip(t, page)
			o := forged.Versions[0].Monitor.Globalping
			switch field {
			case "token":
				o.Token = "tampered-provider-token"
			case "location":
				o.Location = "tampered location"
			case "headers":
				o.HTTP.Headers["Authorization"] = "tampered target token"
			}
			p, err := admit(forged)
			if err != nil {
				t.Fatal("tamper fixture must pass shape validation", err)
			}
			err = receiver.applySnapshot(p)
			if err == nil {
				t.Fatal("same-revision Globalping option substitution accepted")
			}
			if !equalJSON(receiver.current(m.ID).Globalping, m.Globalping) {
				t.Fatal("rejected substitution changed live options")
			}
		})
	}
	if err = receiver.Stop(); err != nil {
		t.Fatal(err)
	}
	receiver, err = New(receiverOptions)
	if err != nil {
		t.Fatal(err)
	}
	if saved = receiver.current(m.ID); saved == nil || !equalJSON(saved.Globalping, m.Globalping) {
		t.Fatal("restart lost admitted options/proof")
	}
}

func TestGlobalpingReviewPartitionedOptionsConflictResolution(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	nodes := map[string]*Node{}
	for _, id := range []string{"a", "b"} {
		peer := "a"
		if id == "a" {
			peer = "b"
		}
		n, err := New(Options{NodeID: id, DBPath: ":memory:", Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: clock.Load, Peers: []Peer{{ID: peer, URL: "http://127.0.0.1:1"}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = n.Stop() })
		nodes[id] = n
	}
	a, b := nodes["a"], nodes["b"]
	relay := func(from, to *Node) {
		t.Helper()
		page, err := from.snapshotPage("", "")
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := to.validateSnapshot(data, from.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = to.applySnapshot(parsed); err != nil {
			t.Fatal(err)
		}
	}
	base := gpReviewCreate(t, a)
	relay(a, b)
	left := gpReviewRoundTrip(t, base)
	left.Globalping.Location = "partition left location"
	left = gpReviewEdit(t, a, left)
	right := gpReviewRoundTrip(t, *b.current(base.ID))
	right.Globalping.Token = "partition-right-provider-token"
	right = gpReviewEdit(t, b, right)
	relay(a, b)
	relay(b, a)
	for _, n := range []*Node{a, b} {
		if n.current(base.ID) != nil || len(n.heads[base.ID]) != 2 || !n.monitorViews("viewer")[0].ConfigConflict {
			t.Fatal("concurrent Globalping semantics silently selected a winner")
		}
		for _, v := range n.heads[base.ID] {
			if !n.monitorVersionReady(v) {
				t.Fatal("conflicting options lost their payload proof")
			}
		}
	}
	raw, _ := json.Marshal(left)
	var choice map[string]any
	if err := json.Unmarshal(raw, &choice); err != nil {
		t.Fatal(err)
	}
	choice["name"] = "resolved remote check"
	choice["revisions"] = []string{left.Revision, right.Revision}
	w := maintenanceAPIRequest(a, "POST", "/api/monitors/"+base.ID+"/resolve", choice, adminToken)
	var resolved Monitor
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &resolved) != nil {
		t.Fatalf("resolution: %d %s", w.Code, w.Body.String())
	}
	if checkGeneration(resolved) == checkGeneration(left) || checkGeneration(resolved) == checkGeneration(right) {
		t.Fatal("semantic conflict resolution reused a branch generation")
	}
	relay(a, b)
	for _, n := range []*Node{a, b} {
		current := n.current(base.ID)
		if current == nil || current.Revision != resolved.Revision || !equalJSON(current.Globalping, left.Globalping) {
			t.Fatal("resolved options/proof did not converge")
		}
	}
}
