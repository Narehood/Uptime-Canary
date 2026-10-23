package canary

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"uptime-canary/apps/node-go/internal/checks"
)

// Review fixtures never start the node scheduler or issue a real probe. Only
// TestBadgeReviewPeerPublication uses HTTP, through private loopback servers.
func badgeReviewMonitor(t *testing.T, n *Node, id string) Monitor {
	t.Helper()
	in := input([]string{"a", "b"}, "http://private-target.invalid/private-credential", "")
	in.Name = "private-monitor-name-" + id
	in.HTTP = &checks.HTTPConfig{Method: "GET", Headers: map[string]string{"X-Secret": "private-header-secret"}, BearerToken: "private-bearer-secret"}
	m, err := n.writeMonitor(in, nil, id, false)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func badgeReviewSamples(n *Node, m Monitor, a, b string) {
	for i, state := range []string{a, b} {
		id := []string{"a", "b"}[i]
		n.samples[m.ID+"/"+id] = Sample{Observation: Observation{
			MonitorID: m.ID, NodeID: id, State: state, CheckedAt: n.now(),
			Message: "private-observation-message", LatencyMs: 123.456, StatusCode: 418,
			Certificate: &checks.CertificateSummary{NotBefore: time.UnixMilli(n.now()).Add(-time.Hour), NotAfter: time.UnixMilli(n.now()).Add(time.Hour), Verified: true},
		}, Revision: m.Revision, Sequence: 1, CheckGeneration: m.CheckGeneration}
		s := n.samples[m.ID+"/"+id]
		s.RawState = state
		if state == "down" {
			s.DownSince, s.ConsecutiveFailures = Ptr(int64(1)), 1
		}
		if state == "unknown" {
			s.LatencyMs, s.LatencyAvailable = 0, Ptr(false)
		}
		n.samples[m.ID+"/"+id] = s
	}
}

func badgeReviewSelection(slug, group, name string) string {
	return publicBadgePrefix + slug + ".svg?" + url.Values{"group": {group}, "monitor": {name}}.Encode()
}

func badgeReviewCheck(t *testing.T, w *httptest.ResponseRecorder, method string, code int, state string, maintenance bool) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("%s: want %d, got %d: %s", method, code, w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing public response policy: %v", w.Header())
	}
	read := method == http.MethodGet || method == http.MethodHead
	if (w.Header().Get("Access-Control-Allow-Origin") == "*") != read || w.Header().Get("Access-Control-Allow-Credentials") != "" || w.Header().Get("Access-Control-Allow-Methods") != "" || w.Header().Get("Access-Control-Allow-Headers") != "" {
		t.Fatalf("public route inherited management CORS: %v", w.Header())
	}
	wantType := "application/json; charset=utf-8"
	if code == http.StatusOK {
		wantType = "image/svg+xml; charset=utf-8"
		if w.Header().Get("Content-Security-Policy") != "default-src 'none'; style-src 'unsafe-inline'; sandbox" {
			t.Fatalf("unexpected SVG CSP: %v", w.Header())
		}
	}
	if w.Header().Get("Content-Type") != wantType {
		t.Fatalf("wrong content type: %v", w.Header())
	}
	if method == http.MethodHead {
		if w.Body.Len() != 0 {
			t.Fatal("HEAD wrote a body")
		}
		return
	}
	if code != http.StatusOK {
		if w.Body.String() != `{"error":"Not found"}` {
			t.Fatalf("failure was not static: %q", w.Body.String())
		}
		return
	}

	// XML and a fixed accessible title are the contract here; renderer geometry
	// and visual comparisons deliberately remain outside this review.
	wantTitle := "status: " + state
	if maintenance {
		wantTitle += ", maintenance"
	}
	d := xml.NewDecoder(strings.NewReader(w.Body.String()))
	title, titleCount, inTitle, svgCount := "", 0, false, 0
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal("invalid SVG XML", err)
		}
		switch v := token.(type) {
		case xml.StartElement:
			if v.Name.Space != "http://www.w3.org/2000/svg" || !slices.Contains([]string{"svg", "g", "rect", "path", "circle", "text", "title", "style"}, v.Name.Local) {
				t.Fatalf("unexpected SVG element: %v", v.Name)
			}
			if v.Name.Local == "svg" {
				svgCount++
			}
			if v.Name.Local == "title" {
				titleCount++
				inTitle = true
			}
			for _, a := range v.Attr {
				if strings.HasPrefix(strings.ToLower(a.Name.Local), "on") || slices.Contains([]string{"href", "src"}, a.Name.Local) {
					t.Fatalf("active SVG attribute: %v", a)
				}
			}
		case xml.EndElement:
			if v.Name.Local == "title" {
				inTitle = false
			}
		case xml.CharData:
			if inTitle {
				title += string(v)
			}
		case xml.Directive, xml.ProcInst:
			t.Fatal("unexpected XML directive or processing instruction")
		}
	}
	if title != wantTitle || titleCount != 1 || svgCount != 1 {
		t.Fatalf("want one SVG/title %q, got %q (%d SVG, %d title)", wantTitle, title, svgCount, titleCount)
	}
	for _, private := range []string{"private-", "monitorId", "nodeId", "revision", "observations", "latency", "statusCode", "certificate", "history", "@import", "url(", "%"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatalf("private data, history, percentage or external resource in SVG: %q", private)
		}
	}
}

func TestBadgeReviewPublishedSelection(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := statusTestNode(t, &clock, ":memory:")
	first := badgeReviewMonitor(t, n, "private-monitor-first")
	second := badgeReviewMonitor(t, n, "private-monitor-second")
	hidden := badgeReviewMonitor(t, n, "private-monitor-unpublished")
	badgeReviewSamples(n, first, "up", "up")
	badgeReviewSamples(n, second, "down", "down")
	badgeReviewSamples(n, hidden, "down", "down")
	name := `API & <edge> "日本" / + % = ?`
	in := statusFixture("selection")
	in.Groups = []StatusPageGroup{
		{ID: "public-first", Name: "Published section", Monitors: []StatusPageMonitor{{MonitorID: first.ID, Name: name, ShowCertificate: true, ShowTags: true}}},
		{ID: "public-second", Name: "Other published section", Monitors: []StatusPageMonitor{{MonitorID: second.ID, Name: name}}},
	}
	page := statusCreate(t, n, in, adminToken)
	pub, w := statusPublic(t, n, page.Slug)
	if w.Code != 200 || len(pub.Groups) != 2 || pub.Groups[0].Monitors[0].Certificate == nil {
		t.Fatal("public selection fixture", w.Code, w.Body.String())
	}
	for i, state := range []string{"up", "down"} {
		g := pub.Groups[i]
		path := badgeReviewSelection(pub.Slug, g.ID, g.Monitors[0].Name)
		w = maintenanceAPIRequest(n, "GET", path, nil, "")
		badgeReviewCheck(t, w, "GET", 200, state, false)
		if strings.Contains(w.Body.String(), name) || strings.Contains(w.Body.String(), g.ID) {
			t.Fatal("caller/public display text entered the fixed SVG")
		}
	}
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", publicBadgePrefix+page.Slug+".svg", nil, ""), "GET", 200, "down", false)
	for _, pair := range [][2]string{{"public-first", first.ID}, {"public-first", first.Name}, {"public-first", hidden.ID}, {first.ID, name}, {"public-first", strings.ToLower(name)}, {"public-first", name + " "}} {
		badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", badgeReviewSelection(page.Slug, pair[0], pair[1]), nil, adminToken), "GET", 404, "", false)
	}

	// Equal bytes are intentional publication when the operator explicitly
	// chooses a private value as the public display name.
	in.Groups[0].Monitors[0].Name = first.ID
	w = maintenanceAPIRequest(n, "PUT", "/api/status-pages/"+page.ID, statusEdit(in, page.Revision, nil), adminToken)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
		t.Fatal("rename fixture", w.Code, w.Body.String())
	}
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", badgeReviewSelection(page.Slug, "public-first", first.ID), nil, ""), "GET", 200, "up", false)
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", badgeReviewSelection(page.Slug, "public-first", name), nil, ""), "GET", 404, "", false)
	in.Groups[0].Monitors = append(in.Groups[0].Monitors, StatusPageMonitor{MonitorID: "missing-private-monitor", Name: first.ID})
	w = maintenanceAPIRequest(n, "PUT", "/api/status-pages/"+page.ID, statusEdit(in, page.Revision, nil), adminToken)
	if w.Code != 200 {
		t.Fatal("duplicate selection fixture", w.Code, w.Body.String())
	}
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", badgeReviewSelection(page.Slug, "public-first", first.ID), nil, ""), "GET", 404, "", false)
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", badgeReviewSelection(page.Slug, "public-second", name), nil, ""), "GET", 200, "down", false)
}

func TestBadgeReviewEvidenceAndMaintenance(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := statusTestNode(t, &clock, ":memory:")
	m := badgeReviewMonitor(t, n, "selected-monitor")
	page := statusCreate(t, n, statusFixture("evidence"), adminToken)
	base := publicBadgePrefix + page.Slug + ".svg"
	selection := badgeReviewSelection(page.Slug, page.Groups[0].ID, page.Groups[0].Monitors[0].Name)
	for _, tc := range []struct{ a, b, want string }{
		{"up", "up", "up"}, {"up", "pending", "pending"}, {"pending", "up", "pending"},
		{"up", "unknown", "unknown"}, {"pending", "unknown", "unknown"},
		{"down", "unknown", "down"}, {"pending", "down", "down"}, {"up", "<script>private-state</script>", "unknown"},
	} {
		badgeReviewSamples(n, m, tc.a, tc.b)
		for _, path := range []string{base, selection} {
			badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", path, nil, ""), "GET", 200, tc.want, false)
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func()
	}{
		{"missing", func() { delete(n.samples, m.ID+"/b") }},
		{"stale", func() { s := n.samples[m.ID+"/b"]; s.CheckedAt -= 86400000; n.samples[m.ID+"/b"] = s }},
		{"future", func() { s := n.samples[m.ID+"/b"]; s.CheckedAt++; n.samples[m.ID+"/b"] = s }},
		{"old revision", func() { s := n.samples[m.ID+"/b"]; s.Revision = "private-obsolete-revision"; n.samples[m.ID+"/b"] = s }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			badgeReviewSamples(n, m, "up", "up")
			tc.mutate()
			badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", selection, nil, ""), "GET", 200, "unknown", false)
		})
	}
	badgeReviewSamples(n, m, "down", "up")
	work := plannedWork()
	work.Name = "private-maintenance-name"
	storeMaintenance(t, n, work)
	for _, path := range []string{base, selection} {
		badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", path, nil, ""), "GET", 200, "down", true)
	}
	clock.Store(maintenanceClock(12, 11))
	badgeReviewSamples(n, m, "down", "up")
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", selection, nil, ""), "GET", 200, "down", false)
	paused := m.MonitorInput
	paused.Enabled = false
	if _, err := n.writeMonitor(paused, n.heads[m.ID], m.ID, false); err != nil {
		t.Fatal(err)
	}
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", selection, nil, ""), "GET", 200, "unknown", false)
}

func TestBadgeReviewAggregateScope(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := statusTestNode(t, &clock, ":memory:")
	first := badgeReviewMonitor(t, n, "selected-monitor")
	second := badgeReviewMonitor(t, n, "private-second-selected")
	hidden := badgeReviewMonitor(t, n, "private-hidden-down")
	badgeReviewSamples(n, first, "up", "up")
	badgeReviewSamples(n, second, "up", "up")
	badgeReviewSamples(n, hidden, "down", "down")
	in := statusFixture("aggregate")
	in.Groups = append(in.Groups, StatusPageGroup{ID: "public-other", Name: "Other", Monitors: []StatusPageMonitor{{MonitorID: second.ID, Name: "Other public service"}}})
	page := statusCreate(t, n, in, adminToken)
	base := publicBadgePrefix + page.Slug + ".svg"
	work := plannedWork()
	work.AllMonitors, work.MonitorIDs = false, []string{hidden.ID}
	storeMaintenance(t, n, work)
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", base, nil, ""), "GET", 200, "up", false)
	work.MonitorIDs = []string{first.ID}
	storeMaintenance(t, n, work)
	badgeReviewSamples(n, first, "down", "up")
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", base, nil, ""), "GET", 200, "down", true)
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", badgeReviewSelection(page.Slug, "public-services", "Public service"), nil, ""), "GET", 200, "down", true)
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", badgeReviewSelection(page.Slug, "public-other", "Other public service"), nil, ""), "GET", 200, "up", false)

	empty := statusFixture("empty")
	empty.Groups = []StatusPageGroup{}
	statusCreate(t, n, empty, adminToken)
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", publicBadgePrefix+empty.Slug+".svg", nil, ""), "GET", 200, "unknown", false)
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", badgeReviewSelection(empty.Slug, "public-services", "Public service"), nil, ""), "GET", 404, "", false)
}

func TestBadgeReviewMonitorAuthorityRemainsUnknown(t *testing.T) {
	for _, name := range []string{"missing", "disabled", "no assigned nodes", "conflict", "missing proof", "deleted"} {
		t.Run(name, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(12, 1))
			n := statusTestNode(t, &clock, ":memory:")
			page := statusCreate(t, n, statusFixture("monitor-authority"), adminToken)
			if name != "missing" {
				m := badgeReviewMonitor(t, n, "selected-monitor")
				badgeReviewSamples(n, m, "up", "up")
				switch name {
				case "disabled", "no assigned nodes":
					in := m.MonitorInput
					if name == "disabled" {
						in.Enabled = false
					} else {
						in.NodeIDs = []string{}
					}
					if _, err := n.writeMonitor(in, n.heads[m.ID], m.ID, false); err != nil {
						t.Fatal(err)
					}
				case "conflict":
					n.heads[m.ID] = append(n.heads[m.ID], n.heads[m.ID][0])
				case "missing proof":
					n.heads[m.ID][0].EdgeID = "private-unavailable-monitor-proof"
				case "deleted":
					if _, err := n.writeMonitor(m.MonitorInput, n.heads[m.ID], m.ID, true); err != nil {
						t.Fatal(err)
					}
				}
			}
			pub, w := statusPublic(t, n, page.Slug)
			if w.Code != 200 || pub.State != "unknown" || pub.Groups[0].Monitors[0].State != "unknown" {
				t.Fatal("monitor fixture disagreed with existing public projection", w.Code, w.Body.String())
			}
			for _, path := range []string{publicBadgePrefix + page.Slug + ".svg", badgeReviewSelection(page.Slug, pub.Groups[0].ID, pub.Groups[0].Monitors[0].Name)} {
				badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", path, nil, adminToken), "GET", 200, "unknown", false)
			}
		})
	}
}

type badgeReviewUnreadBody struct{}

func (badgeReviewUnreadBody) Read([]byte) (int, error) { panic("badge read a request body") }
func (badgeReviewUnreadBody) Close() error             { return nil }

func TestBadgeReviewHTTPBoundary(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := statusTestNode(t, &clock, ":memory:")
	page := statusCreate(t, n, statusFixture("http-boundary"), adminToken)
	base := publicBadgePrefix + page.Slug + ".svg"
	selection := badgeReviewSelection(page.Slug, "public-services", "Public service")
	badPaths := []string{
		"/public/badge", "/public/badge/", base + "/", base + "/extra", base + ".svg",
		"/public/badge/../http-boundary.svg", "/public/badge//http-boundary.svg",
		"/public/badge/-bad.svg", "/public/badge/bad-.svg", "/public/badge/HTTP-boundary.svg",
		"/public/badge/%68ttp-boundary.svg", "/public/%62adge/http-boundary.svg", "/public/badge/http-boundary%2esvg",
		"/public/badge/missing.svg", base + "?", base + "?group=&monitor=Public+service",
		base + "?group=public-services&monitor=", base + "?group=public-services", base + "?monitor=Public+service",
		selection + "&group=public-services", selection + "&%6donitor=Public+service", selection + "&revision=private-revision",
		selection + "&token=private-token", selection + "&monitorId=selected-monitor", selection + "&id=selected-monitor",
		base + "?Group=public-services&monitor=Public+service", selection + "&private-unknown",
		base + "?group=public-services;private=x&monitor=Public+service", base + "?group=public-services&monitor=%ZZ",
		base + "?group=public-services&monitor=%ff", base + "?" + strings.Repeat("x", 4097),
	}
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"} {
		for _, path := range append([]string{base, selection}, badPaths...) {
			r := httptest.NewRequest(method, path, nil)
			r.Body = badgeReviewUnreadBody{}
			r.ContentLength = int64(maxBody * 100)
			r.Host = "private-request-host.invalid"
			r.Header.Set("Origin", "https://private-unlisted-origin.invalid")
			r.Header.Set("Authorization", "Bearer "+adminToken)
			r.Header.Set("If-None-Match", "*")
			r.Header.Set("If-Modified-Since", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
			w := httptest.NewRecorder()
			n.ServeHTTP(w, r)
			code := 404
			if (method == "GET" || method == "HEAD") && (path == base || path == selection) {
				code = 200
			}
			badgeReviewCheck(t, w, method, code, "unknown", false)
		}
	}
	get := maintenanceAPIRequest(n, "GET", selection, nil, "")
	head := maintenanceAPIRequest(n, "HEAD", selection, nil, "")
	if !reflect.DeepEqual(get.Header(), head.Header()) {
		t.Fatal("GET and HEAD response policies differ")
	}
	for _, token := range []string{"", "invalid-private-token", adminToken, clusterToken} {
		badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", selection, nil, token), "GET", 200, "unknown", false)
	}
	for _, path := range []string{"/api/status-pages", "/api/monitors"} {
		w := maintenanceAPIRequest(n, "GET", path, nil, "")
		if w.Code != 401 || w.Header().Get("Access-Control-Allow-Origin") == "*" {
			t.Fatal("badge authority spread into management", path, w.Code)
		}
	}
	for _, slug := range []string{strings.Repeat("s", 64), "0", "a-b"} {
		p := statusCreate(t, n, statusFixture(slug), adminToken)
		badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", publicBadgePrefix+p.Slug+".svg", nil, ""), "GET", 200, "unknown", false)
	}
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", publicBadgePrefix+strings.Repeat("s", 65)+".svg", nil, ""), "GET", 404, "", false)
}

func TestBadgeReviewPublicationAuthority(t *testing.T) {
	for _, name := range []string{"draft", "deleted", "conflict", "missing proof", "duplicate", "draft duplicate", "unready duplicate", "fault", "stopped"} {
		t.Run(name, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(12, 1))
			n := statusTestNode(t, &clock, ":memory:")
			in := statusFixture("authority")
			page := statusCreate(t, n, in, adminToken)
			key := resourceKey(resourceStatusPage, page.ID)
			switch name {
			case "draft":
				in.Enabled = false
				if w := maintenanceAPIRequest(n, "PUT", "/api/status-pages/"+page.ID, statusEdit(in, page.Revision, nil), adminToken); w.Code != 200 {
					t.Fatal(w.Code)
				}
			case "deleted":
				if w := maintenanceAPIRequest(n, "DELETE", "/api/status-pages/"+page.ID, map[string]string{"revision": page.Revision}, adminToken); w.Code != 204 {
					t.Fatal(w.Code)
				}
			case "conflict":
				n.resources[key] = append(n.resources[key], n.resources[key][0])
			case "missing proof":
				n.resources[key][0].EdgeID = "private-unavailable-proof"
			case "duplicate", "draft duplicate", "unready duplicate":
				in.Enabled = name != "draft duplicate"
				other, err := n.writeResource(resourceStatusPage, "private-other-page", in, false)
				if err != nil {
					t.Fatal(err)
				}
				if name == "unready duplicate" {
					n.resources[resourceKey(resourceStatusPage, other.ID)][0].EdgeID = "private-unavailable-proof"
				}
			case "fault":
				n.fault = errors.New("private-persistence-detail")
			case "stopped":
				if err := n.Stop(); err != nil {
					t.Fatal(err)
				}
			}
			for _, token := range []string{"", adminToken, clusterToken, "private-invalid-token"} {
				for _, path := range []string{publicBadgePrefix + page.Slug + ".svg", badgeReviewSelection(page.Slug, "public-services", "Public service")} {
					for _, method := range []string{"GET", "HEAD"} {
						badgeReviewCheck(t, maintenanceAPIRequest(n, method, path, nil, token), method, 404, "", false)
					}
				}
			}
		})
	}
}

func badgeReviewStoreDigest(t *testing.T, n *Node) [32]byte {
	t.Helper()
	rows, err := n.store.db.Query("SELECT category,id,payload FROM records ORDER BY category,id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		var category, id, payload string
		if err := rows.Scan(&category, &id, &payload); err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(h, "%d:%s%d:%s%d:%s", len(category), category, len(id), id, len(payload), payload)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return [32]byte(h.Sum(nil))
}

func TestBadgeReviewReadBudgetBoundariesPreserveStore(t *testing.T) {
	for _, budget := range []string{"heads", "JSON bytes"} {
		t.Run(budget, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(12, 1))
			n := statusTestNode(t, &clock, ":memory:")
			page := statusCreate(t, n, statusFixture("budget"), adminToken)
			durableBefore := badgeReviewStoreDigest(t, n)
			var changesBefore int
			if err := n.store.db.QueryRow("SELECT total_changes()").Scan(&changesBefore); err != nil {
				t.Fatal(err)
			}
			extraKey := resourceKey(resourceChannel, "private-budget-filler")
			if budget == "heads" {
				n.resources[extraKey] = make([]ResourceVersion, publicStatusHeadBudget-1)
			} else {
				// Only the read view is synthetic. Keep each unrelated JSON blob
				// within its existing input ceiling; never persist/admit padding.
				remaining := publicStatusByteBudget - len(n.resources[resourceKey(resourceStatusPage, page.ID)][0].Resource.Data)
				for i := 0; remaining > 0; i++ {
					size := min(remaining, maxAPIBody)
					data := []byte(`{}` + strings.Repeat(" ", size-2))
					key := resourceKey(resourceStatusPage, fmt.Sprintf("private-padding-%d", i))
					n.resources[key] = []ResourceVersion{{Resource: Resource{Kind: resourceStatusPage, ID: fmt.Sprintf("private-padding-%d", i), Data: data}}}
					extraKey = key
					remaining -= size
				}
			}
			paths := []string{publicBadgePrefix + page.Slug + ".svg", badgeReviewSelection(page.Slug, "public-services", "Public service")}
			for _, path := range paths {
				badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", path, nil, ""), "GET", 200, "unknown", false)
			}
			if budget == "heads" {
				n.resources[extraKey] = append(n.resources[extraKey], ResourceVersion{})
			} else {
				n.resources[extraKey][0].Resource.Data = append(n.resources[extraKey][0].Resource.Data, ' ')
			}
			before := make(map[string][]ResourceVersion, len(n.resources))
			for key, heads := range n.resources {
				before[key] = slices.Clone(heads)
				for i := range before[key] {
					before[key][i].Resource.Data = bytes.Clone(heads[i].Resource.Data)
				}
			}
			for _, path := range paths {
				for _, method := range []string{"GET", "HEAD"} {
					badgeReviewCheck(t, maintenanceAPIRequest(n, method, path, nil, adminToken), method, 404, "", false)
				}
			}
			var changesAfter int
			if err := n.store.db.QueryRow("SELECT total_changes()").Scan(&changesAfter); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, n.resources) || badgeReviewStoreDigest(t, n) != durableBefore || changesBefore != changesAfter {
				t.Fatal("public read changed retained heads, payloads or durable records")
			}
		})
	}
}

func TestBadgeReviewReadsNeedNoSQLConnection(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := statusTestNode(t, &clock, ":memory:")
	m := badgeReviewMonitor(t, n, "selected-monitor")
	badgeReviewSamples(n, m, "down", "up")
	page := statusCreate(t, n, statusFixture("no-history"), adminToken)
	storeMaintenance(t, n, plannedWork())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, err := n.store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// This holds the only SQL connection. A query for history or any other
	// table would block; release it on failure so cleanup can still finish.
	done := make(chan []*httptest.ResponseRecorder, 1)
	go func() {
		out := []*httptest.ResponseRecorder{}
		for _, path := range []string{publicBadgePrefix + page.Slug + ".svg", badgeReviewSelection(page.Slug, "public-services", "Public service")} {
			out = append(out, maintenanceAPIRequest(n, "GET", path, nil, ""))
		}
		done <- out
	}()
	select {
	case out := <-done:
		for _, w := range out {
			badgeReviewCheck(t, w, "GET", 200, "down", true)
		}
	case <-ctx.Done():
		_ = conn.Close()
		<-done
		t.Fatal("public badge needed a SQL connection")
	}
}

func TestBadgeReviewSQLiteRestart(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	path := filepath.Join(t.TempDir(), "badge-review.db")
	n := statusTestNode(t, &clock, path)
	m := badgeReviewMonitor(t, n, "selected-monitor")
	badgeReviewSamples(n, m, "down", "up")
	for _, id := range []string{"a", "b"} {
		if !n.dbPut("samples", m.ID+"/"+id, n.samples[m.ID+"/"+id]) {
			t.Fatal("persist sample fixture")
		}
	}
	page := statusCreate(t, n, statusFixture("restart"), adminToken)
	storeMaintenance(t, n, plannedWork())
	selection := badgeReviewSelection(page.Slug, "public-services", "Public service")
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", selection, nil, ""), "GET", 200, "down", true)
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = statusTestNode(t, &clock, path)
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", selection, nil, ""), "GET", 200, "down", true)
	clock.Store(maintenanceClock(12, 11))
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", selection, nil, ""), "GET", 200, "unknown", false)
	if w := maintenanceAPIRequest(n, "DELETE", "/api/status-pages/"+page.ID, map[string]string{"revision": page.Revision}, adminToken); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = statusTestNode(t, &clock, path)
	badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", selection, nil, adminToken), "GET", 404, "", false)
}

func TestBadgeReviewPeerPublication(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	newPeer := func(id, other string) *Node {
		n, err := New(Options{NodeID: id, DBPath: filepath.Join(t.TempDir(), id+".db"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: clock.Load,
			PeerTimeoutMs: Ptr(int64(2000)), Peers: []Peer{{ID: other, URL: "http://127.0.0.1:1"}}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = n.Stop() })
		return n
	}
	a, b := newPeer("a", "b"), newPeer("b", "a")
	server := httptest.NewServer(b)
	t.Cleanup(server.Close)
	peer := Peer{ID: "b", URL: server.URL}
	exchange := func() {
		t.Helper()
		for round := range 8 {
			more := a.exchangePage(peer)
			if a.peerSeen["b"] != clock.Load() {
				t.Fatal("loopback peer exchange was not acknowledged", round)
			}
			if !more {
				return
			}
		}
		t.Fatal("private peer fixture did not converge in eight pages")
	}
	m := badgeReviewMonitor(t, a, "selected-monitor")
	badgeReviewSamples(a, m, "down", "up")
	page := statusCreate(t, a, statusFixture("peer-badge"), adminToken)
	selection := badgeReviewSelection(page.Slug, "public-services", "Public service")
	badgeReviewCheck(t, maintenanceAPIRequest(b, "GET", selection, nil, ""), "GET", 404, "", false)
	exchange()
	badgeReviewCheck(t, maintenanceAPIRequest(b, "GET", selection, nil, ""), "GET", 200, "down", false)

	// Partitioned edits of the same published page must both survive; neither
	// the SVG route nor credentials may pick a branch on the healed node.
	for i, n := range []*Node{a, b} {
		in := page.StatusPageInput
		in.Title = fmt.Sprintf("Published branch %d", i)
		if w := maintenanceAPIRequest(n, "PUT", "/api/status-pages/"+page.ID, statusEdit(in, page.Revision, nil), adminToken); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		statusCreate(t, n, statusFixture("peer-duplicate"), adminToken)
	}
	exchange()
	for _, n := range []*Node{a, b} {
		badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", selection, nil, adminToken), "GET", 404, "", false)
		badgeReviewCheck(t, maintenanceAPIRequest(n, "GET", publicBadgePrefix+"peer-duplicate.svg", nil, ""), "GET", 404, "", false)
	}
	w := maintenanceAPIRequest(a, "GET", "/api/status-pages/"+page.ID+"/conflicts", nil, adminToken)
	var conflicts []StatusPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &conflicts) != nil || len(conflicts) != 2 {
		t.Fatal("peer conflicts fixture", w.Code, w.Body.String())
	}
	revs := []string{conflicts[0].Revision, conflicts[1].Revision}
	w = maintenanceAPIRequest(a, "POST", "/api/status-pages/"+page.ID+"/resolve", statusEdit(page.StatusPageInput, "", revs), adminToken)
	var resolved StatusPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &resolved) != nil {
		t.Fatal("peer resolution fixture", w.Code, w.Body.String())
	}
	exchange()
	badgeReviewCheck(t, maintenanceAPIRequest(b, "GET", selection, nil, ""), "GET", 200, "down", false)
	if w := maintenanceAPIRequest(a, "DELETE", "/api/status-pages/"+page.ID, map[string]string{"revision": resolved.Revision}, adminToken); w.Code != 204 {
		t.Fatal(w.Code)
	}
	exchange()
	badgeReviewCheck(t, maintenanceAPIRequest(b, "GET", selection, nil, adminToken), "GET", 404, "", false)
}
