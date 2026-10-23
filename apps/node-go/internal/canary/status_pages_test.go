package canary

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"uptime-canary/apps/node-go/internal/checks"
)

func statusFixture(slug string) StatusPageInput {
	return StatusPageInput{Slug: slug, Title: "Service status", Description: "Operator public description", Enabled: true,
		Groups:        []StatusPageGroup{{ID: "public-services", Name: "Public services", Monitors: []StatusPageMonitor{{MonitorID: "selected-monitor", Name: "Public service", ShowCertificate: false}}}},
		Announcements: []StatusPageAnnouncement{}}
}

func statusTestNode(t *testing.T, clock *atomic.Int64, path string) *Node {
	t.Helper()
	n, err := New(Options{NodeID: "a", DBPath: path, AdminToken: adminToken, ClusterToken: clusterToken, Port: Ptr(0), Now: clock.Load,
		Peers: []Peer{{ID: "b", URL: "http://127.0.0.1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	return n
}

func statusCreate(t *testing.T, n *Node, in StatusPageInput, token string) StatusPage {
	t.Helper()
	w := maintenanceAPIRequest(n, "POST", "/api/status-pages", in, token)
	var out StatusPage
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("status create: %d %s", w.Code, w.Body.String())
	}
	return out
}

func statusPublic(t *testing.T, n *Node, slug string) (PublicStatusPage, *httptest.ResponseRecorder) {
	t.Helper()
	w := maintenanceAPIRequest(n, "GET", publicStatusPrefix+slug, nil, "")
	var out PublicStatusPage
	if w.Code == 200 && json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal("invalid public JSON")
	}
	return out, w
}

func statusEdit(in StatusPageInput, revision string, revisions []string) map[string]any {
	b, _ := json.Marshal(in)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if revision != "" {
		out["revision"] = revision
	}
	if revisions != nil {
		out["revisions"] = revisions
	}
	return out
}

func TestStatusPageStrictInputAndBounds(t *testing.T) {
	base, _ := json.Marshal(statusFixture("operations"))
	if _, err := parseStatusPage(base); err != nil {
		t.Fatal(err)
	}
	cases := []string{
		strings.Replace(string(base), `"enabled":true`, `"enabled":true,"enabled":false`, 1),
		strings.Replace(string(base), `"enabled":true`, `"Enabled":true`, 1),
		strings.Replace(string(base), `"enabled":true`, `"enabled":null`, 1),
		strings.Replace(string(base), `"enabled":true`, `"enabled":"true"`, 1),
		strings.Replace(string(base), `"enabled":true`, `"enabled":true,"css":"anything"`, 1),
		strings.Replace(string(base), `"monitorId":"selected-monitor"`, `"monitorId":"selected-monitor","monitorId":"other"`, 1),
		strings.Replace(string(base), `"monitorId":`, `"MonitorId":`, 1),
		strings.Replace(string(base), `"showCertificate":false`, `"showCertificate":null`, 1),
		strings.Replace(string(base), `"showCertificate":false`, `"showCertificate":false,"target":"secret"`, 1),
		strings.Replace(string(base), `"id":"public-services"`, `"id":"public-services","ID":"different"`, 1),
		strings.Replace(string(base), `"announcements":[]`, `"announcements":null`, 1),
		strings.Replace(string(base), `"announcements":[]`, `"announcements":[null]`, 1),
		strings.Replace(string(base), `"slug":"operations"`, `"slug":"Operations"`, 1),
		strings.Replace(string(base), `"slug":"operations"`, `"slug":"../operations"`, 1),
		strings.Replace(string(base), `"slug":"operations"`, `"slug":"-operations"`, 1),
		strings.Replace(string(base), `"description":"Operator public description"`, `"description":null`, 1),
		strings.TrimSuffix(string(base), "}") + `,"Revision":"tip"}`,
		strings.TrimSuffix(string(base), "}") + `,"revision":null}`,
		strings.TrimSuffix(string(base), "}") + `,"revisions":["tip","tip"]}`,
		string(base) + `{}`,
	}
	for i, raw := range cases {
		if _, err := parseStatusPage([]byte(raw)); err == nil {
			t.Errorf("ambiguous input %d accepted", i)
		}
	}
	for _, mutation := range []func(*StatusPageInput){
		func(in *StatusPageInput) { in.Groups = nil },
		func(in *StatusPageInput) { in.Groups[0].Monitors = nil },
		func(in *StatusPageInput) { in.Groups = append(in.Groups, in.Groups[0]) },
		func(in *StatusPageInput) {
			in.Groups[0].Monitors = append(in.Groups[0].Monitors, in.Groups[0].Monitors[0])
		},
		func(in *StatusPageInput) { in.Title = strings.Repeat("x", 129) },
		func(in *StatusPageInput) { in.Description = "control\x01" },
		func(in *StatusPageInput) { in.Slug = strings.Repeat("s", 65) },
	} {
		in := statusFixture("operations")
		mutation(&in)
		b, _ := json.Marshal(in)
		if _, err := parseStatusPage(b); err == nil {
			t.Error("invalid selection accepted")
		}
	}
	maximal := statusFixture(strings.Repeat("s", 64))
	maximal.Groups = []StatusPageGroup{}
	for g := range 16 {
		group := StatusPageGroup{ID: fmt.Sprintf("g%d", g), Name: "Chosen group", Monitors: []StatusPageMonitor{}}
		for m := range 4 {
			group.Monitors = append(group.Monitors, StatusPageMonitor{MonitorID: fmt.Sprintf("m%d-%d", g, m), Name: "Chosen public name"})
		}
		maximal.Groups = append(maximal.Groups, group)
	}
	for i := range 50 {
		maximal.Announcements = append(maximal.Announcements, StatusPageAnnouncement{ID: fmt.Sprintf("a%d", i), Title: "Manual update", Message: strings.Repeat("m", 2048), State: "investigating", CreatedAt: 1, UpdatedAt: 2})
	}
	b, _ := json.Marshal(maximal)
	if _, err := parseStatusPage(b); err != nil {
		t.Fatal("documented maximum rejected", err)
	}
	for _, mutation := range []func(*StatusPageInput){
		func(in *StatusPageInput) {
			in.Groups = append(in.Groups, StatusPageGroup{ID: "extra", Name: "extra", Monitors: []StatusPageMonitor{}})
		},
		func(in *StatusPageInput) {
			in.Groups[0].Monitors = append(in.Groups[0].Monitors, StatusPageMonitor{MonitorID: "extra", Name: "extra"})
		},
		func(in *StatusPageInput) { in.Announcements = append(in.Announcements, in.Announcements[0]) },
		func(in *StatusPageInput) { in.Announcements[0].State = "down" },
		func(in *StatusPageInput) { in.Announcements[0].UpdatedAt = 0 },
		func(in *StatusPageInput) { in.Announcements[0].Message = strings.Repeat("m", 2049) },
	} {
		var in StatusPageInput
		_ = json.Unmarshal(b, &in)
		mutation(&in)
		raw, _ := json.Marshal(in)
		if _, err := parseStatusPage(raw); err == nil {
			t.Error("maximum/announcement validation bypassed")
		}
	}
	for _, pair := range [][2]string{{`"pinned":false`, `"pinned":null`}, {`"pinned":false`, `"Pinned":true`}, {`"state":"investigating"`, `"state":"investigating","state":"resolved"`}} {
		if _, err := parseStatusPage([]byte(strings.Replace(string(b), pair[0], pair[1], 1))); err == nil {
			t.Error("ambiguous announcement accepted")
		}
	}
}

func TestStatusPageManagementRBACCRUDUniquenessAndRestart(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	path := filepath.Join(t.TempDir(), "status.db")
	n := statusTestNode(t, &clock, path)
	for _, role := range []struct{ role, token string }{{"viewer", viewerToken}, {"editor", editorToken}} {
		w := maintenanceAPIRequest(n, "POST", "/api/accounts", map[string]string{"name": role.role, "role": role.role, "token": role.token}, adminToken)
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	in := statusFixture("operations")
	for _, token := range []string{"", clusterToken} {
		if w := maintenanceAPIRequest(n, "GET", "/api/status-pages", nil, token); w.Code != 401 {
			t.Fatal("management must authenticate", w.Code)
		}
	}
	if w := maintenanceAPIRequest(n, "POST", "/api/status-pages", in, viewerToken); w.Code != 403 {
		t.Fatal("viewer published", w.Code)
	}
	page := statusCreate(t, n, in, editorToken)
	for _, suffix := range []string{"", "/" + page.ID, "/" + page.ID + "/conflicts"} {
		if w := maintenanceAPIRequest(n, "GET", "/api/status-pages"+suffix, nil, viewerToken); w.Code != 200 {
			t.Fatal("viewer cannot read status management", w.Code)
		}
	}
	for _, op := range []struct{ method, path string }{{"PUT", "/api/status-pages/" + page.ID}, {"DELETE", "/api/status-pages/" + page.ID}, {"POST", "/api/status-pages/" + page.ID + "/resolve"}} {
		if w := maintenanceAPIRequest(n, op.method, op.path, statusEdit(in, page.Revision, nil), viewerToken); w.Code != 403 {
			t.Fatal("viewer mutated publication", w.Code)
		}
	}
	if w := maintenanceAPIRequest(n, "POST", "/api/status-pages", in, adminToken); w.Code != 409 {
		t.Fatal("duplicate slug admitted", w.Code)
	}
	other := statusFixture("draft")
	other.Enabled = false
	draft := statusCreate(t, n, other, adminToken)
	if _, w := statusPublic(t, n, "draft"); w.Code != 404 {
		t.Fatal("draft published", w.Code)
	}
	other.Slug = in.Slug
	if w := maintenanceAPIRequest(n, "PUT", "/api/status-pages/"+draft.ID, statusEdit(other, draft.Revision, nil), adminToken); w.Code != 409 {
		t.Fatal("update stole slug", w.Code)
	}
	if w := maintenanceAPIRequest(n, "PUT", "/api/status-pages/"+page.ID, statusEdit(in, "stale", nil), editorToken); w.Code != 409 {
		t.Fatal("stale write accepted", w.Code)
	}
	in.Announcements = []StatusPageAnnouncement{{ID: "operator-update", Title: "Investigation", Message: "Operator-authored update\nPlain text only", State: "monitoring", CreatedAt: clock.Load(), UpdatedAt: clock.Load(), Pinned: true}}
	w := maintenanceAPIRequest(n, "PUT", "/api/status-pages/"+page.ID, statusEdit(in, page.Revision, nil), editorToken)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
		t.Fatal("edit", w.Code, w.Body.String())
	}
	pub, w := statusPublic(t, n, in.Slug)
	if w.Code != 200 || pub.State != "unknown" || !equalJSON(pub.Announcements, in.Announcements) {
		t.Fatal("missing monitor/announcements", w.Code, w.Body.String())
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = statusTestNode(t, &clock, path)
	pub, w = statusPublic(t, n, in.Slug)
	if w.Code != 200 || !equalJSON(pub.Announcements, in.Announcements) {
		t.Fatal("publication did not survive restart", w.Code)
	}
	for _, enabled := range []bool{false, true} {
		in.Enabled = enabled
		w = maintenanceAPIRequest(n, "PUT", "/api/status-pages/"+page.ID, statusEdit(in, page.Revision, nil), editorToken)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
			t.Fatal("publication toggle", w.Code, w.Body.String())
		}
		_, w = statusPublic(t, n, in.Slug)
		if enabled && w.Code != 200 || !enabled && w.Code != 404 {
			t.Fatal("publication toggle was not applied", w.Code)
		}
	}
	for _, raw := range []string{`{"revision":"` + page.Revision + `","revision":"stale"}`, `{"Revision":"` + page.Revision + `"}`, `{"revision":null}`, `{"revision":"` + page.Revision + `","enabled":true}`} {
		req := httptest.NewRequest("DELETE", "/api/status-pages/"+page.ID, strings.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+adminToken)
		bad := httptest.NewRecorder()
		n.ServeHTTP(bad, req)
		if bad.Code != 400 {
			t.Fatal("ambiguous delete envelope accepted", bad.Code)
		}
	}
	w = maintenanceAPIRequest(n, "DELETE", "/api/status-pages/"+page.ID, map[string]string{"revision": page.Revision}, adminToken)
	if w.Code != 204 {
		t.Fatal("delete", w.Code, w.Body.String())
	}
	if _, w = statusPublic(t, n, in.Slug); w.Code != 404 {
		t.Fatal("tombstone published", w.Code)
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = statusTestNode(t, &clock, path)
	if _, w = statusPublic(t, n, in.Slug); w.Code != 404 {
		t.Fatal("tombstone lost on restart", w.Code)
	}
	statusCreate(t, n, statusFixture(in.Slug), adminToken)
}

func TestStatusPagePublicRedactionStatesMaintenanceAndCertificates(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := statusTestNode(t, &clock, ":memory:")
	private := input([]string{"a", "b"}, "http://private-target.invalid/private-path?private-query=value", "")
	private.Name = "private-monitor-name"
	private.HTTP = &checks.HTTPConfig{Method: "GET", Headers: map[string]string{"X-Private": "private-header-value"}, BearerToken: "private-bearer-value"}
	m, err := n.writeMonitor(private, nil, "selected-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	unselected, err := n.writeMonitor(input([]string{"a"}, "http://private-other.invalid", ""), nil, "unselected-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	n.samples[unselected.ID+"/a"] = Sample{Observation: Observation{MonitorID: unselected.ID, NodeID: "a", State: "down", CheckedAt: clock.Load()}, Revision: unselected.Revision}
	cert := &checks.CertificateSummary{NotBefore: time.UnixMilli(clock.Load()).Add(-time.Hour), NotAfter: time.UnixMilli(clock.Load()).Add(time.Hour), Verified: true, DaysRemaining: 1}
	set := func(id, state string) {
		n.samples[m.ID+"/"+id] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: id, State: state, CheckedAt: clock.Load(), Message: "private-probe-message", LatencyMs: 19, StatusCode: 418, Certificate: cert}, Revision: m.Revision}
	}
	set("a", "up")
	set("b", "up")
	in := statusFixture("operations")
	page := statusCreate(t, n, in, adminToken)
	check := func(state string, maintenance, certificate bool) {
		t.Helper()
		p, w := statusPublic(t, n, in.Slug)
		if w.Code != 200 || p.State != state || p.Groups[0].State != state || p.Groups[0].Monitors[0].State != state || p.Maintenance != maintenance || p.Groups[0].Monitors[0].Maintenance != maintenance || (p.Groups[0].Monitors[0].Certificate != nil) != certificate {
			t.Fatalf("public state expected %s maintenance=%v certificate=%v: %d %s", state, maintenance, certificate, w.Code, w.Body.String())
		}
		for _, secret := range []string{"private-", "selected-monitor", "unselected-monitor", "nodeId", "monitorId", "revision", "observations", "statusCode", "latency", "history", "verified", "daysRemaining", "schedule", "maintenanceIds", "notification"} {
			if strings.Contains(w.Body.String(), secret) {
				t.Fatalf("private information leaked: %s", secret)
			}
		}
	}
	check("up", false, false)
	in.Groups[0].Monitors[0].ShowCertificate = true
	w := maintenanceAPIRequest(n, "PUT", "/api/status-pages/"+page.ID, statusEdit(in, page.Revision, nil), adminToken)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	check("up", false, true)
	delete(n.samples, m.ID+"/b")
	check("unknown", false, false)
	set("a", "down")
	check("down", false, false)
	set("b", "pending")
	check("down", false, true)
	set("a", "up")
	check("pending", false, true)
	set("b", "up")
	otherCert := *cert
	otherCert.NotAfter = otherCert.NotAfter.Add(time.Minute)
	s := n.samples[m.ID+"/b"]
	s.Certificate = &otherCert
	n.samples[m.ID+"/b"] = s
	check("up", false, false)
	set("b", "up")
	s = n.samples[m.ID+"/b"]
	s.Revision = "old-revision"
	n.samples[m.ID+"/b"] = s
	check("unknown", false, false)
	set("b", "up")
	clock.Add(10000)
	check("unknown", false, false)
	set("a", "down")
	set("b", "up")
	storeMaintenance(t, n, plannedWork())
	check("down", true, true)
	paused := m.MonitorInput
	paused.Enabled = false
	m, err = n.writeMonitor(paused, n.heads[m.ID], m.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	check("unknown", true, false)
	conflict := m
	conflict.Revision = "concurrent-monitor"
	if err = n.applySnapshot(Snapshot{Versions: []Version{{Monitor: conflict, Ancestors: []string{}}}}); err != nil {
		t.Fatal(err)
	}
	check("unknown", false, false)
	if _, err = n.writeMonitor(m.MonitorInput, n.heads[m.ID], m.ID, true); err != nil {
		t.Fatal(err)
	}
	check("unknown", false, false)
}

func TestStatusPagePublicWorkBudgetFailsClosedWithoutChangingRetainedUnion(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := statusTestNode(t, &clock, ":memory:")
	page := statusCreate(t, n, statusFixture("bounded"), adminToken)
	key := resourceKey(resourceStatusPage, page.ID)
	before := n.resources[key][0]
	// Exercise the read budget without admitting or persisting synthetic heads.
	n.resources[resourceKey(resourceChannel, "large-conflict")] = make([]ResourceVersion, publicStatusHeadBudget)
	if _, w := statusPublic(t, n, page.Slug); w.Code != 404 {
		t.Fatal("unbounded concurrent-head scan", w.Code)
	}
	delete(n.resources, resourceKey(resourceChannel, "large-conflict"))
	data := make([]byte, maxAPIBody)
	for i := range publicStatusByteBudget/maxAPIBody + 1 {
		r := Resource{Kind: resourceStatusPage, ID: fmt.Sprintf("scan-%d", i), Data: data}
		n.resources[resourceKey(resourceStatusPage, r.ID)] = []ResourceVersion{{Resource: r}}
	}
	if _, w := statusPublic(t, n, page.Slug); w.Code != 404 || !equalJSON(before, n.resources[key][0]) {
		t.Fatal("public read truncated the union or exceeded its byte budget", w.Code)
	}
}

func TestStatusPagePublicRouteFailsClosedAndCannotReflectOrWrite(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := statusTestNode(t, &clock, ":memory:")
	page := statusCreate(t, n, statusFixture("operations"), adminToken)
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"} {
		for _, path := range []string{"/public/status", "/public/status/", "/public/status/missing", "/public/status/operations", "/public/status/Operations", "/public/status/operations/extra", "/public/status/operations?token=private-query", "/public/status/operations?", "/public/status/%6fperations", "/public/status/" + strings.Repeat("x", 200)} {
			req := httptest.NewRequest(method, path, strings.NewReader(`{"message":"private-body-reflection"}`))
			req.Host = "private-host.invalid"
			req.Header.Set("Origin", "https://unlisted-origin.invalid")
			req.Header.Set("Authorization", "Bearer "+adminToken)
			w := httptest.NewRecorder()
			n.ServeHTTP(w, req)
			publicRead := method == "GET" || method == "HEAD"
			want := 404
			if publicRead && path == publicStatusPrefix+page.Slug {
				want = 200
			}
			if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Type") != "application/json; charset=utf-8" || (w.Header().Get("Access-Control-Allow-Origin") == "*") != publicRead || w.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Fatalf("public boundary %s %s: %d %v", method, path, w.Code, w.Header())
			}
			if method == "HEAD" && w.Body.Len() != 0 || strings.Contains(w.Body.String(), "private-") || want == 404 && method != "HEAD" && w.Body.String() != `{"error":"Not found"}` {
				t.Fatal("public reflection/existence disclosure", method, path, w.Body.String())
			}
		}
	}
	w := maintenanceAPIRequest(n, "GET", "/api/status-pages", nil, "")
	if w.Header().Get("Access-Control-Allow-Origin") == "*" || w.Code != 401 {
		t.Fatal("public CORS/auth leaked into management")
	}
	if n.currentResource(resourceStatusPage, page.ID).Revision != page.Revision {
		t.Fatal("public request changed resource")
	}
}

func TestStatusPageMissingProofAndOutOfOrderReferences(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := statusTestNode(t, &clock, ":memory:")
	in := statusFixture("delayed")
	b, _ := json.Marshal(in)
	r := Resource{Kind: resourceStatusPage, ID: "late-page", Revision: "late-page-revision", UpdatedAt: clock.Load(), Data: b}
	edge := newRevisionEdge(resourceStatusPage, r.ID, r.Revision, r, nil, "")
	v := ResourceVersion{Resource: r, Ancestors: edge.Parents, EdgeID: edge.ID}
	if err := n.applySnapshot(Snapshot{Resources: []ResourceVersion{v}}); err != nil {
		t.Fatal(err)
	}
	if _, w := statusPublic(t, n, in.Slug); w.Code != 404 {
		t.Fatal("missing page proof published", w.Code)
	}
	if w := maintenanceAPIRequest(n, "PUT", "/api/status-pages/"+r.ID, statusEdit(in, r.Revision, nil), adminToken); w.Code != 409 {
		t.Fatal("missing proof locally edited", w.Code)
	}
	if err := n.applySnapshot(Snapshot{RevisionEdges: []RevisionEdge{edge}}); err != nil {
		t.Fatal(err)
	}
	if p, w := statusPublic(t, n, in.Slug); w.Code != 200 || p.State != "unknown" {
		t.Fatal("page must retain not-yet-arrived monitor", w.Code, p.State)
	}
	m := Monitor{MonitorInput: input([]string{"a"}, "http://127.0.0.1:1", ""), ID: "selected-monitor", Revision: "delayed-monitor", UpdatedAt: clock.Load()}
	me := newRevisionEdge("monitor", m.ID, m.Revision, m, nil, m.Revision)
	if err := n.applySnapshot(Snapshot{Versions: []Version{{Monitor: m, Ancestors: me.Parents, EdgeID: me.ID}}}); err != nil {
		t.Fatal(err)
	}
	n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "up", CheckedAt: clock.Load()}, Revision: m.Revision}
	if p, _ := statusPublic(t, n, in.Slug); p.State != "unknown" {
		t.Fatal("missing monitor proof fabricated healthy")
	}
	if err := n.applySnapshot(Snapshot{RevisionEdges: []RevisionEdge{me}}); err != nil {
		t.Fatal(err)
	}
	if p, _ := statusPublic(t, n, in.Slug); p.State != "up" {
		t.Fatal("ready referenced monitor did not become public", p.State)
	}
}

func TestStatusPageSeventyOneHeadResolutionAPIAndRestart(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	path := filepath.Join(t.TempDir(), "status-joins.db")
	n := statusTestNode(t, &clock, path)
	in := statusFixture("many-heads")
	b, _ := json.Marshal(in)
	root := Resource{Kind: resourceStatusPage, ID: "joined-page", Revision: "page-root", UpdatedAt: clock.Load(), Data: b}
	snapshot := Snapshot{RevisionEdges: []RevisionEdge{newRevisionEdge(resourceStatusPage, root.ID, root.Revision, root, nil, "")}}
	revisions := []string{}
	for i := range 71 {
		r := root
		r.Revision = fmt.Sprintf("page-branch-%02d", i)
		e := newRevisionEdge(resourceStatusPage, r.ID, r.Revision, r, []string{root.Revision}, "")
		snapshot.RevisionEdges = append(snapshot.RevisionEdges, e)
		snapshot.Resources = append(snapshot.Resources, ResourceVersion{Resource: r, Ancestors: e.Parents, EdgeID: e.ID})
		revisions = append(revisions, r.Revision)
	}
	if err := n.applySnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if _, w := statusPublic(t, n, in.Slug); w.Code != 404 {
		t.Fatal("conflicting page published", w.Code)
	}
	for _, partial := range [][]string{revisions[:70], append(slices.Clone(revisions[:70]), revisions[0])} {
		if w := maintenanceAPIRequest(n, "POST", "/api/status-pages/"+root.ID+"/resolve", statusEdit(in, "", partial), adminToken); w.Code != 409 && w.Code != 400 {
			t.Fatal("partial or repeated heads resolved", w.Code)
		}
	}
	stale := slices.Clone(revisions)
	stale[len(stale)-1] = "unobserved-stale-head"
	if w := maintenanceAPIRequest(n, "POST", "/api/status-pages/"+root.ID+"/resolve", statusEdit(in, "", stale), adminToken); w.Code != 409 {
		t.Fatal("stale head set resolved", w.Code)
	}
	reserved := statusCreate(t, n, statusFixture("reserved"), adminToken)
	in.Slug = reserved.Slug
	if w := maintenanceAPIRequest(n, "POST", "/api/status-pages/"+root.ID+"/resolve", statusEdit(in, "", revisions), adminToken); w.Code != 409 {
		t.Fatal("resolution stole slug", w.Code)
	}
	in.Slug = "many-heads"
	w := maintenanceAPIRequest(n, "POST", "/api/status-pages/"+root.ID+"/resolve", statusEdit(in, "", revisions), adminToken)
	var page StatusPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil {
		t.Fatal("71-head resolution", w.Code, w.Body.String())
	}
	heads := n.resources[resourceKey(resourceStatusPage, page.ID)]
	if len(heads) != 1 || !n.resourceVersionReady(heads[0]) || len(heads[0].Ancestors) > maxClusterMembers {
		t.Fatal("resolution did not use complete bounded revision joins")
	}
	for _, rev := range revisions {
		if !n.graph.reaches(resourceStatusPage, page.ID, page.Revision, rev) {
			t.Fatal("resolution omitted head", rev)
		}
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = statusTestNode(t, &clock, path)
	if _, w = statusPublic(t, n, in.Slug); w.Code != 200 {
		t.Fatal("resolved proof failed restart", w.Code)
	}
	w = maintenanceAPIRequest(n, "DELETE", "/api/status-pages/"+page.ID, map[string]string{"revision": page.Revision}, adminToken)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := n.applySnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if _, w = statusPublic(t, n, in.Slug); w.Code != 404 {
		t.Fatal("late branch resurrected joined tombstone", w.Code)
	}
}

func TestStatusPageRealPeerPagesConflictsDuplicateSlugsAndTombstones(t *testing.T) {
	c := cluster(t, []string{"a", "b"}, func(o *Options) {
		// These pages carry ~200 KiB of JSON, unlike the small HA fixtures that
		// use a 60 ms peer deadline. Leave time for race-instrumented parsing,
		// proof validation and durable merge without changing production defaults.
		o.PeerTimeoutMs = Ptr(int64(2000))
		o.Probe = func(context.Context, Monitor) ProbeResult {
			return ProbeResult{State: "pending", Message: "private fixture"}
		}
	})
	a, b := c.nodes[0], c.nodes[1]
	c.split([]string{"a"}, []string{"b"})
	// More than one MiB of real replicated resources forces multiple peer pages.
	var first StatusPage
	for i := range 11 {
		in := statusFixture(fmt.Sprintf("peer-page-%02d", i))
		for j := range 50 {
			in.Announcements = append(in.Announcements, StatusPageAnnouncement{ID: fmt.Sprintf("notice-%d", j), Title: "Operator notice", Message: strings.Repeat("m", 2048), State: "identified", CreatedAt: 1, UpdatedAt: 2})
		}
		page := statusCreate(t, a, in, adminToken)
		if i == 0 {
			first = page
		}
	}
	c.heal()
	eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.statusPageViews()) == 11 && b.currentResource(resourceStatusPage, first.ID) != nil
	})
	if p, w := statusPublic(t, b, first.Slug); w.Code != 200 || len(p.Announcements) != 50 || p.State != "unknown" {
		t.Fatal("real peer publication missing", w.Code)
	}
	c.split([]string{"a"}, []string{"b"})
	for i, n := range c.nodes {
		in := first.StatusPageInput
		in.Title = fmt.Sprintf("Operator branch %d", i)
		if w := maintenanceAPIRequest(n, "PUT", "/api/status-pages/"+first.ID, statusEdit(in, first.Revision, nil), adminToken); w.Code != 200 {
			t.Fatal("partition edit", w.Code, w.Body.String())
		}
		statusCreate(t, n, statusFixture("concurrent-slug"), adminToken)
	}
	c.heal()
	eventually(t, func() bool {
		for _, n := range c.nodes {
			n.mu.Lock()
			ok := len(n.resources[resourceKey(resourceStatusPage, first.ID)]) == 2 && len(n.statusPageViews()) == 13
			n.mu.Unlock()
			if !ok {
				return false
			}
		}
		return true
	})
	for _, n := range c.nodes {
		for _, slug := range []string{first.Slug, "concurrent-slug"} {
			if _, w := statusPublic(t, n, slug); w.Code != 404 {
				t.Fatal("remote conflict chose arbitrary page", slug, w.Code)
			}
		}
	}
	w := maintenanceAPIRequest(a, "GET", "/api/status-pages/"+first.ID+"/conflicts", nil, adminToken)
	var candidates []StatusPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &candidates) != nil || len(candidates) != 2 {
		t.Fatal("conflicts", w.Code)
	}
	revs := []string{candidates[0].Revision, candidates[1].Revision}
	w = maintenanceAPIRequest(a, "POST", "/api/status-pages/"+first.ID+"/resolve", statusEdit(first.StatusPageInput, "", revs), adminToken)
	var resolved StatusPage
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &resolved) != nil {
		t.Fatal("peer resolution", w.Code, w.Body.String())
	}
	eventually(t, func() bool { _, w := statusPublic(t, b, first.Slug); return w.Code == 200 })
	if w = maintenanceAPIRequest(a, "DELETE", "/api/status-pages/"+first.ID, map[string]string{"revision": resolved.Revision}, adminToken); w.Code != 204 {
		t.Fatal("peer delete", w.Code)
	}
	eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		h := b.resources[resourceKey(resourceStatusPage, first.ID)]
		return len(h) == 1 && h[0].Resource.Deleted
	})
	if _, w = statusPublic(t, b, first.Slug); w.Code != 404 {
		t.Fatal("peer tombstone published", w.Code)
	}
}

func TestStatusPageLocalCapacityCountsTombstonesWithoutCappingPartitionUnion(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := statusTestNode(t, &clock, ":memory:")
	for i := range maxStatusPages {
		in := statusFixture(fmt.Sprintf("retained-%d", i))
		b, _ := json.Marshal(in)
		r := Resource{Kind: resourceStatusPage, ID: fmt.Sprintf("retained-%d", i), Revision: fmt.Sprintf("revision-%d", i), UpdatedAt: clock.Load(), Deleted: true, Data: b}
		if err := n.applySnapshot(Snapshot{Resources: []ResourceVersion{{Resource: r, Ancestors: []string{}}}}); err != nil {
			t.Fatal(err)
		}
	}
	if w := maintenanceAPIRequest(n, "POST", "/api/status-pages", statusFixture("overflow"), adminToken); w.Code != 409 {
		t.Fatal("local admission ignored retained IDs", w.Code)
	}
	in := statusFixture("partition-union")
	b, _ := json.Marshal(in)
	r := Resource{Kind: resourceStatusPage, ID: "partition-extra", Revision: "partition-extra-revision", UpdatedAt: clock.Load(), Data: b}
	if err := n.applySnapshot(Snapshot{Resources: []ResourceVersion{{Resource: r, Ancestors: []string{}}}}); err != nil {
		t.Fatal("valid partition union rejected at local admission limit", err)
	}
	if _, w := statusPublic(t, n, in.Slug); w.Code != 200 {
		t.Fatal("partition union page unavailable", w.Code)
	}
}
