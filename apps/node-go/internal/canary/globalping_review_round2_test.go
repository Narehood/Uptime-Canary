package canary

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gp "uptime-canary/apps/node-go/internal/globalping"
	"uptime-canary/apps/node-go/internal/jsonquery"
)

// Publication is safe even before a head has options or usable revision proof.
// These direct boundary cases deliberately include shapes admission rejects.
func TestGlobalpingReviewRound2OriginBoundary(t *testing.T) {
	for _, tc := range []struct{ name, kind, target, want string }{
		{"mixed scheme", "globalping", "HtTpS://Example.COM:8443/private-path?private-query=x", "https://Example.COM:8443/"},
		{"IPv6", "globalping", "HTTP://[2001:db8::1]:8080/private%2Fpath?private-query=%2B", "http://[2001:db8::1]:8080/"},
		{"default port", "globalping", "https://example.com:443/private-path?private-query=x", "https://example.com:443/"},
		{"userinfo defense", "globalping", "https://private-user:private-password@example.com/private-path?private-query=x", "https://example.com/"},
		{"ping host", "globalping", "example.com", "example.com"},
		{"DNS host", "globalping", "_service.example.com", "_service.example.com"},
		{"other monitor", "http", "https://example.com/private-path?private-query=x", "https://example.com/private-path?private-query=x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []string{"http", "HTTP", " http ", " HTTP ", "future-mode", "<nil>"} {
				m := Monitor{MonitorInput: MonitorInput{Type: tc.kind, Target: tc.target, NodeIDs: []string{"a"}}}
				if mode != "<nil>" {
					m.Globalping = &GlobalpingMonitorOptions{Mode: mode, Location: "private-location", Token: "private-token"}
				}
				before := contentID(m)
				v := publicMonitor(m, "viewer")
				if v.Target != tc.want || v.Globalping != nil {
					t.Errorf("mode %q: viewer target %q, options present %v", mode, v.Target, v.Globalping != nil)
				}
				for _, role := range []string{"admin", "editor"} {
					if !equalJSON(publicMonitor(m, role), m) {
						t.Errorf("mode %q: changed %s configuration", mode, role)
					}
				}
				if contentID(m) != before {
					t.Fatal("publication mutated its source")
				}
			}
		})
	}
}

func gpR2Accounts(t *testing.T, n *Node) {
	t.Helper()
	for _, a := range []struct{ role, token string }{{"viewer", viewerToken}, {"editor", editorToken}} {
		w := maintenanceAPIRequest(n, "POST", "/api/accounts", map[string]string{"name": "R2 " + a.role, "role": a.role, "token": a.token}, adminToken)
		if w.Code != 201 {
			t.Fatalf("create %s: %d", a.role, w.Code)
		}
	}
}

func gpR2Management(t *testing.T, n *Node, id string, wantConflict bool) {
	t.Helper()
	before := contentID(n.heads[id])
	for _, a := range []struct{ role, token string }{{"viewer", viewerToken}, {"admin", adminToken}, {"editor", editorToken}} {
		for _, endpoint := range []string{"/api/overview", "/api/monitors", "/api/monitors/" + id + "/conflicts"} {
			w := maintenanceAPIRequest(n, "GET", endpoint, nil, a.token)
			if w.Code != 200 {
				t.Fatalf("%s %s: %d", a.role, endpoint, w.Code)
			}
			var monitors []Monitor
			if strings.HasSuffix(endpoint, "/conflicts") {
				if err := json.Unmarshal(w.Body.Bytes(), &monitors); err != nil {
					t.Fatal(err)
				}
				if len(monitors) != len(n.heads[id]) {
					t.Fatal("conflicts response lost a head")
				}
			} else {
				var views []MonitorView
				if endpoint == "/api/overview" {
					var overview Overview
					if err := json.Unmarshal(w.Body.Bytes(), &overview); err != nil {
						t.Fatal(err)
					}
					views = overview.Monitors
				} else if err := json.Unmarshal(w.Body.Bytes(), &views); err != nil {
					t.Fatal(err)
				}
				if len(views) != 1 || views[0].ConfigConflict != wantConflict {
					t.Fatalf("%s: wrong readiness/conflict", endpoint)
				}
				monitors = []Monitor{views[0].Monitor}
			}
			for _, m := range monitors {
				var raw *Monitor
				for _, v := range n.heads[id] {
					if v.Monitor.Revision == m.Revision {
						copy := v.Monitor
						raw = &copy
					}
				}
				if raw == nil {
					t.Fatal("response has unknown revision")
				}
				if a.role == "viewer" {
					u, err := url.Parse(raw.Target)
					if err != nil || m.Target != strings.ToLower(u.Scheme)+"://"+u.Host+"/" || m.Globalping != nil {
						t.Fatalf("%s: private target/options escaped viewer boundary", endpoint)
					}
				} else if !equalJSON(m, *raw) {
					t.Fatalf("%s: %s lost raw configuration", endpoint, a.role)
				}
			}
			if a.role == "viewer" && (strings.Contains(w.Body.String(), "private-") || strings.Contains(w.Body.String(), "BEGIN CERTIFICATE") || strings.Contains(w.Body.String(), `"globalping":`)) {
				t.Fatalf("%s: private options/path/query leaked", endpoint)
			}
		}
	}
	if contentID(n.heads[id]) != before {
		t.Fatal("management reads changed immutable heads")
	}
}

func TestGlobalpingReviewRound2PrivacyUnreadyConflictPublicationRestart(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	nodes := map[string]*Node{}
	options := map[string]Options{}
	for _, id := range []string{"a", "b"} {
		peer := "a"
		if id == "a" {
			peer = "b"
		}
		o := Options{NodeID: id, DBPath: filepath.Join(t.TempDir(), "node.sqlite"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: clock.Load, Peers: []Peer{{ID: peer, URL: "http://127.0.0.1:1"}}}
		n, err := New(o)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = n.Stop() })
		nodes[id], options[id] = n, o
		gpR2Accounts(t, n)
	}
	a, b := nodes["a"], nodes["b"]
	in := gpInput([]string{"a", "b"})
	in.Target = "HTTPS://[2001:db8::1]:8443/private%2Fpath?private-query=%2B"
	in.Globalping.Mode = " HTTP "
	in.Globalping.APIURL = "https://private-provider.example/private-api"
	_, in.Globalping.APICA, _ = oauthQueryReviewTLS(t)
	in.Globalping.HTTP.JSONata = &jsonquery.Config{Expression: `"private-expression" = "private-expression"`, Operator: jsonquery.Equal, Expected: json.RawMessage(`true`)}
	w := maintenanceAPIRequest(a, "POST", "/api/monitors", in, adminToken)
	var m Monitor
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &m) != nil {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	gpR2Management(t, a, m.ID, false)
	if _, w := statusPublic(t, a, "round2"); w.Code != 404 {
		t.Fatal("monitor existence published an unconfigured status page")
	}
	page, err := a.snapshotPage("", "")
	if err != nil {
		t.Fatal(err)
	}
	apply := func(to *Node, from string, page Snapshot) {
		t.Helper()
		data, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := to.validateSnapshot(data, from)
		if err != nil {
			t.Fatal(err)
		}
		if err := to.applySnapshot(parsed); err != nil {
			t.Fatal(err)
		}
	}
	heads := gpReviewRoundTrip(t, page)
	heads.RevisionEdges, heads.RevisionClaims = nil, nil
	apply(b, "a", heads)
	if b.current(m.ID) != nil {
		t.Fatal("head without proof became checkable")
	}
	gpR2Management(t, b, m.ID, true)
	publication := statusFixture("round2")
	publication.Groups[0].Monitors[0].MonitorID = m.ID
	if w := maintenanceAPIRequest(b, "POST", "/api/status-pages", publication, viewerToken); w.Code != 403 {
		t.Fatal("viewer could publish")
	}
	statusCreate(t, b, publication, editorToken)
	publicCheck := func() {
		t.Helper()
		p, w := statusPublic(t, b, "round2")
		if w.Code != 200 || p.State != "unknown" || len(p.Groups) != 1 || len(p.Groups[0].Monitors) != 1 || p.Groups[0].Monitors[0].Name != "Public service" || strings.Contains(w.Body.String(), "private-") || strings.Contains(w.Body.String(), "BEGIN CERTIFICATE") || strings.Contains(w.Body.String(), `"target":`) || strings.Contains(w.Body.String(), `"globalping":`) {
			t.Fatal("public selection disclosed configuration or fabricated a usable monitor")
		}
	}
	publicCheck()
	apply(b, "a", page)
	gpR2Management(t, b, m.ID, false)
	publicCheck()
	left := gpReviewRoundTrip(t, m)
	left.Target = "https://[2001:db8::1]:8443/private-left?private-query=left"
	gpReviewEdit(t, a, left)
	right := gpReviewRoundTrip(t, *b.current(m.ID))
	right.Target = "http://Example.COM:8080/private-right?private-query=right"
	right.Globalping.Mode = " http "
	gpReviewEdit(t, b, right)
	page, err = a.snapshotPage("", "")
	if err != nil {
		t.Fatal(err)
	}
	apply(b, "a", page)
	if len(b.heads[m.ID]) != 2 || b.current(m.ID) != nil {
		t.Fatal("independent changes failed to remain a conflict")
	}
	gpR2Management(t, b, m.ID, true)
	publicCheck()
	before := contentID(b.heads[m.ID])
	if err := b.Stop(); err != nil {
		t.Fatal(err)
	}
	b, err = New(options["b"])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Stop() })
	if contentID(b.heads[m.ID]) != before {
		t.Fatal("restart changed private conflicting payloads")
	}
	gpR2Management(t, b, m.ID, true)
	publicCheck()
}

func TestGlobalpingReviewRound2ViewerReadsPreserveActualRequest(t *testing.T) {
	var posts, gets atomic.Int32
	type creation struct {
		Type, Target string
		Limit        int
		Locations    []struct{ Magic string }
		Options      struct {
			Protocol string
			Port     int
			Request  struct {
				Method, Path, Query string
				Headers             map[string]string
			}
		} `json:"measurementOptions"`
	}
	captured := make(chan creation, 1)
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer private-provider-token" {
			t.Error("provider credential namespace changed")
		}
		if r.Method == "POST" {
			posts.Add(1)
			var posted creation
			if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
				t.Error(err)
			}
			select {
			case captured <- posted:
			default:
				t.Error("unexpected duplicate POST")
			}
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"id":"round2_measurement","probesCount":1}`))
			return
		}
		gets.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "round2_measurement", "type": "http", "target": "Example.COM", "createdAt": stamp, "status": "finished", "probesCount": 1,
			"results": []any{map[string]any{"result": map[string]any{"status": "finished", "statusCode": 200, "rawBody": "healthy", "tls": map[string]bool{"authorized": true}, "timings": map[string]any{"total": 7.25}, "truncated": false}}}})
	}))
	pair, ca, _ := oauthQueryReviewTLS(t)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := maintenanceNode(t, &clock, ":memory:")
	gpR2Accounts(t, n)
	in := gpInput([]string{"a"})
	in.Target = "HTTPS://Example.COM:8443/private%2Fpath?private-query=token%2Bkey&x=%2f"
	in.Globalping.Mode = " HTTP "
	in.Globalping.APIURL, in.Globalping.APICA = server.URL, ca
	w := maintenanceAPIRequest(n, "POST", "/api/monitors", in, adminToken)
	var m Monitor
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &m) != nil {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	before := contentID(n.heads[m.ID])
	gpR2Management(t, n, m.ID, false)
	if posts.Load() != 0 || gets.Load() != 0 {
		t.Fatal("management reads contacted provider")
	}
	got := n.probe(context.Background(), *n.current(m.ID))
	if got.State != gp.Up || got.LatencyAvailable == nil || !*got.LatencyAvailable || got.LatencyMs != 7.25 || posts.Load() != 1 || gets.Load() != 1 {
		t.Fatalf("remote result: %+v posts=%d gets=%d", got, posts.Load(), gets.Load())
	}
	posted := <-captured
	if posted.Type != "http" || posted.Target != "Example.COM" || posted.Limit != 1 || len(posted.Locations) != 1 || posted.Locations[0].Magic != in.Globalping.Location || posted.Options.Protocol != "HTTPS" || posted.Options.Port != 8443 || posted.Options.Request.Method != "GET" || posted.Options.Request.Path != "/private%2Fpath" || posted.Options.Request.Query != "private-query=token%2Bkey&x=%2f" || posted.Options.Request.Headers["Authorization"] != "private-target-secret" {
		t.Fatal("publication changed actual private remote request")
	}
	if contentID(n.heads[m.ID]) != before || !equalJSON(n.current(m.ID).MonitorInput, in) {
		t.Fatal("read/check mutated original target/options")
	}
}
