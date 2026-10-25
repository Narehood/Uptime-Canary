package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

var dashboardKeyFixture = []byte("0123456789abcdef0123456789abcdef")

func dashboardHTTP(h http.Handler, method, path, token, selection string, body any) *httptest.ResponseRecorder {
	b := []byte{}
	if body != nil {
		b, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, strings.NewReader(string(b)))
	r.RemoteAddr = "192.0.2.9:1234"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if selection != "" {
		r.Header.Set("X-Canary-Registered-Node", selection)
	}
	r.Header.Set("Cookie", "sign-in-proxy=private-fixture-cookie")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func dashboardRegistryFixture(t *testing.T) (*Dashboard, string, DashboardAccountOptions) {
	t.Helper()
	a, o := dashboardAccountFixture(t)
	setupDashboardOwner(t, a, "owner")
	token := dashboardPasswordLogin(t, a, "owner", dashboardPasswordFixture)
	d, err := NewDashboard(DashboardOptions{Accounts: a, Key: dashboardKeyFixture})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d, token, o
}

func dashboardRemoteFixture(t *testing.T, id string) *Node {
	t.Helper()
	n, err := New(Options{NodeID: id, DBPath: ":memory:", AdminToken: adminToken, ClusterToken: clusterToken})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	return n
}

func verifyDashboardNode(t *testing.T, d *Dashboard, token, address string) nodeVerification {
	t.Helper()
	w := dashboardHTTP(d, "POST", "/api/dashboard/verify", token, "", map[string]string{"label": "Private fixture node", "url": address, "token": adminToken})
	var v nodeVerification
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &v) != nil || v.Ticket == "" {
		t.Fatalf("verify: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), adminToken) {
		t.Fatal("verification exposed enrollment credential")
	}
	return v
}

func enrollDashboardNode(t *testing.T, d *Dashboard, token, address string) RegisteredNode {
	t.Helper()
	v := verifyDashboardNode(t, d, token, address)
	w := dashboardHTTP(d, "POST", "/api/dashboard/nodes", token, "", map[string]string{"ticket": v.Ticket})
	var row RegisteredNode
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &row) != nil || row.ID == "" {
		t.Fatalf("enroll: %d %s", w.Code, w.Body.String())
	}
	return row
}

func TestDashboardRegistrySharedRolesUseNodeRedaction(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	n := dashboardRemoteFixture(t, "node-a")
	_, err := n.writeMonitor(input([]string{"node-a"}, "http://127.0.0.1:1", "http://127.0.0.1:2/private-notification-secret"), nil, "private-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	var leakedCookie atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			leakedCookie.Store(true)
		}
		w.Header().Set("Set-Cookie", "node-cookie=must-not-leak")
		n.ServeHTTP(w, r)
	}))
	defer server.Close()
	row := enrollDashboardNode(t, d, owner, server.URL)
	if len(n.accounts) != 4 {
		t.Fatalf("expected original admin and three delegated roles, got %d", len(n.accounts))
	}
	if w := dashboardHTTP(n, "PUT", "/api/accounts/"+dashboardDelegateID(row.ID, "viewer"), adminToken, "", map[string]string{"role": "admin"}); w.Code != 409 {
		t.Fatalf("a shared role delegation could be promoted between authorization and forwarding: %d", w.Code)
	}
	for _, role := range []string{"editor", "viewer"} {
		w := dashboardHTTP(d, "POST", "/api/accounts", owner, "", map[string]string{"name": role, "role": role, "password": dashboardPasswordFixture})
		if w.Code != 201 {
			t.Fatalf("create %s: %d %s", role, w.Code, w.Body.String())
		}
		token := dashboardPasswordLogin(t, d.accounts, role, dashboardPasswordFixture)
		list := dashboardHTTP(d, "GET", "/api/dashboard/nodes", token, "", nil)
		if list.Code != 200 || !strings.Contains(list.Body.String(), row.ID) || strings.Contains(list.Body.String(), server.URL) {
			t.Fatalf("shared role list: %d %s", list.Code, list.Body.String())
		}
		view := dashboardHTTP(d, "GET", "/api/overview", token, row.ID, nil)
		if view.Code != 200 || view.Header().Get("Set-Cookie") != "" {
			t.Fatalf("%s overview: %d %s", role, view.Code, view.Body.String())
		}
		if role == "viewer" && strings.Contains(view.Body.String(), "private-notification-secret") {
			t.Fatal("viewer received node secret")
		}
		if role == "editor" && !strings.Contains(view.Body.String(), "private-notification-secret") {
			t.Fatal("editor lost existing configuration authority")
		}
		if w = dashboardHTTP(d, "POST", "/api/dashboard/verify", token, "", map[string]string{}); w.Code != 403 {
			t.Fatalf("%s can enroll nodes: %d", role, w.Code)
		}
		if role == "viewer" && dashboardHTTP(d, "POST", "/api/monitors", token, row.ID, map[string]string{}).Code != 403 {
			t.Fatal("viewer mutated monitoring configuration")
		}
		// Account routing is dashboard-local even with a selection header.
		if w = dashboardHTTP(d, "GET", "/api/auth/me", token, row.ID, nil); !strings.Contains(w.Body.String(), `"name":"`+role+`"`) {
			t.Fatal("identity accidentally routed to delegated node account")
		}
	}
	if leakedCookie.Load() {
		t.Fatal("proxy cookie sent to private node")
	}
	for _, p := range []string{"/internal/snapshot", "/api/push/private-token", "/api/dashboard-enrollments/forbidden"} {
		if w := dashboardHTTP(d, "POST", p, owner, row.ID, map[string]string{}); w.Code != 404 {
			t.Fatalf("forwarded forbidden route %s: %d", p, w.Code)
		}
	}
}

func TestDashboardRegistryVerificationIsReadOnlyAndActorBound(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	n := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(n)
	defer server.Close()
	v := verifyDashboardNode(t, d, owner, server.URL)
	if len(n.accounts) != 1 || len(d.registry) != 0 {
		t.Fatal("verification acquired remote authority")
	}
	other := dashboardPasswordLogin(t, d.accounts, "owner", dashboardPasswordFixture)
	if w := dashboardHTTP(d, "POST", "/api/dashboard/nodes", other, "", map[string]string{"ticket": v.Ticket}); w.Code != 409 {
		t.Fatal("another session consumed verification ticket")
	}
	if w := dashboardHTTP(d, "DELETE", "/api/dashboard/verify/"+v.Ticket, owner, "", nil); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := dashboardHTTP(d, "POST", "/api/dashboard/nodes", owner, "", map[string]string{"ticket": v.Ticket}); w.Code != 409 {
		t.Fatal("canceled verification was enrolled")
	}
	if len(n.accounts) != 1 {
		t.Fatal("cancel left service accounts")
	}
	for _, address := range []string{server.URL + "/api", server.URL + "?x=1", strings.Replace(server.URL, "http://", "http://secret@", 1), "http://127.0.0.1:0", "http://127.0.0.1/#fragment"} {
		w := dashboardHTTP(d, "POST", "/api/dashboard/verify", owner, "", map[string]string{"label": "Fixture", "url": address, "token": adminToken})
		if w.Code != 400 {
			t.Fatalf("invalid address admitted %s: %d", address, w.Code)
		}
	}
}

func TestDashboardRegistryLostResponsesRetryWithoutOrphaning(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	n := dashboardRemoteFixture(t, "node-a")
	var losePost, loseDelete atomic.Bool
	losePost.Store(true)
	loseDelete.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/dashboard-enrollments/") && ((r.Method == "POST" && losePost.Swap(false)) || (r.Method == "DELETE" && loseDelete.Swap(false))) {
			recorder := httptest.NewRecorder()
			n.ServeHTTP(recorder, r)
			if recorder.Code != 201 && recorder.Code != 204 {
				t.Errorf("private fixture delegation failed: %d %s", recorder.Code, recorder.Body.String())
			}
			w.Header().Set("X-Canary-Node-Id", n.ID)
			errorReply(w, fail(503, "Private fixture dropped response"))
			return
		}
		n.ServeHTTP(w, r)
	}))
	defer server.Close()
	v := verifyDashboardNode(t, d, owner, server.URL)
	w := dashboardHTTP(d, "POST", "/api/dashboard/nodes", owner, "", map[string]string{"ticket": v.Ticket})
	var row RegisteredNode
	_ = json.Unmarshal(w.Body.Bytes(), &row)
	if w.Code != 202 || row.State != "enrolling" || len(n.accounts) != 4 {
		t.Fatalf("ambiguous enrollment untracked: %d %s", w.Code, w.Body.String())
	}
	w = dashboardHTTP(d, "POST", "/api/dashboard/nodes/"+row.ID+"/retry", owner, "", nil)
	if w.Code != 200 || len(n.accounts) != 4 {
		t.Fatalf("retry duplicated delegation: %d %s", w.Code, w.Body.String())
	}
	w = dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+row.ID, owner, "", nil)
	if w.Code != 202 || len(n.accounts) != 1 || len(d.registry) != 1 {
		t.Fatalf("ambiguous revocation claimed complete: %d %s", w.Code, w.Body.String())
	}
	w = dashboardHTTP(d, "POST", "/api/dashboard/nodes/"+row.ID+"/retry", owner, "", nil)
	if w.Code != 204 || len(d.registry) != 0 || len(n.accounts) != 1 {
		t.Fatalf("lost DELETE response not recoverable: %d %s", w.Code, w.Body.String())
	}
}

func TestDashboardRegistryEncryptionRestartAndWrongKey(t *testing.T) {
	d, owner, options := dashboardRegistryFixture(t)
	n := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(n)
	defer server.Close()
	row := enrollDashboardNode(t, d, owner, server.URL)
	saved := d.registry[row.ID]
	secret, err := d.openSecrets(saved)
	if err != nil {
		t.Fatal(err)
	}
	var payload string
	if err = d.accounts.node.store.db.QueryRow(`SELECT payload FROM records WHERE category='dashboardRegistry' AND id=?`, row.ID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{adminToken, secret.Admin, secret.Editor, secret.Viewer} {
		if strings.Contains(payload, value) {
			t.Fatal("registry contains plaintext node credential")
		}
	}
	tampered := saved
	tampered.URL = "http://127.0.0.1:1"
	if _, err = d.openSecrets(tampered); err == nil {
		t.Fatal("encrypted credential could be reassigned to another destination")
	}
	d.Close()
	_ = d.accounts.Close()
	a, err := NewDashboardAccounts(options)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err = NewDashboard(DashboardOptions{Accounts: a, Key: []byte("abcdef0123456789abcdef0123456789")}); err == nil {
		t.Fatal("wrong master key admitted")
	}
	restarted, err := NewDashboard(DashboardOptions{Accounts: a, Key: dashboardKeyFixture})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	w := dashboardHTTP(restarted, "GET", "/api/overview", owner, row.ID, nil)
	if w.Code != 200 {
		t.Fatalf("registry/session did not survive restart: %d %s", w.Code, w.Body.String())
	}
}

func TestDashboardRegistryRejectsElevatedOrReplacedDelegation(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	n := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(n)
	defer server.Close()
	row := enrollDashboardNode(t, d, owner, server.URL)
	_ = dashboardHTTP(d, "POST", "/api/accounts", owner, "", map[string]string{"name": "reader", "role": "viewer", "password": dashboardPasswordFixture})
	reader := dashboardPasswordLogin(t, d.accounts, "reader", dashboardPasswordFixture)
	n.mu.Lock()
	a := n.accounts[dashboardDelegateID(row.ID, "viewer")]
	a.Role = "admin"
	n.accounts[a.ID] = a
	n.mu.Unlock()
	w := dashboardHTTP(d, "GET", "/api/monitors", reader, row.ID, nil)
	if w.Code != 502 {
		t.Fatalf("elevated viewer delegation exposed data: %d %s", w.Code, w.Body.String())
	}
	// A node identity mismatch is rejected before its mutation handler runs.
	r := httptest.NewRequest("POST", "/api/monitors", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer "+adminToken)
	r.Header.Set("X-Canary-Expected-Node", "node-b")
	response := httptest.NewRecorder()
	n.ServeHTTP(response, r)
	if response.Code != 409 || len(n.heads) != 0 {
		t.Fatal("different-node request reached mutation handler")
	}
}

func TestDashboardRegistryUnavailableNodesDoNotBlockAccountsOrAssets(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	h := DashboardHandler(d, fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("private fixture dashboard assets")}})
	for _, p := range []string{"/", "/nodes", "/canary-config.js", "/healthz", "/readyz", "/api/auth/me", "/api/overview", "/api/dashboard/nodes"} {
		w := dashboardHTTP(h, "GET", p, owner, "", nil)
		if w.Code != 200 {
			t.Fatalf("zero-node path %s: %d %s", p, w.Code, w.Body.String())
		}
	}
	if w := dashboardHTTP(h, "GET", "/internal/snapshot", owner, "", nil); w.Code != 404 {
		t.Fatal("assets exposed peer endpoint")
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.localURL, d.localToken = "http://127.0.0.1:1", adminToken
	d.ensureLocalNode(ctx)
	if len(d.registry) != 1 {
		t.Fatal("unavailable bundled local node was not added")
	}
	var id string
	for key, row := range d.registry {
		id = key
		if !row.Local || row.State != "enrolling" {
			t.Fatal("incorrect local starting state")
		}
	}
	if w := dashboardHTTP(h, "DELETE", "/api/dashboard/nodes/"+id, owner, "", nil); w.Code != 409 {
		t.Fatal("local node removed through UI")
	}
	cancel()
	if w := dashboardHTTP(h, "GET", "/api/auth/me", owner, "", nil); w.Code != 200 {
		t.Fatal("unavailable local node blocked dashboard account")
	}
}

func TestDashboardRegistryBackgroundLifecycleHasOneJoin(t *testing.T) {
	d, _, _ := dashboardRegistryFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	first := d.Start(ctx)
	if second := d.Start(context.Background()); first != second {
		cancel()
		t.Fatal("a second start returned a false completion channel")
	}
	cancel()
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("dashboard poll workers were not joined")
	}
}

func TestDashboardDelegationAtomicRollbackAndStrictAdmission(t *testing.T) {
	n := dashboardRemoteFixture(t, "node-a")
	id := "0123456789abcdef0123456789abcdef"
	in := dashboardRoleTokens{NodeID: n.ID, Admin: "admin-delegation-private-fixture-123456789", Editor: "editor-delegation-private-fixture-123456789", Viewer: "viewer-delegation-private-fixture-123456789"}
	if _, err := n.store.db.Exec(`CREATE TRIGGER delegation_failure BEFORE INSERT ON records WHEN NEW.category='accounts' AND NEW.id LIKE '%-editor' BEGIN SELECT RAISE(ABORT, 'private fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	w := dashboardHTTP(n, "POST", "/api/dashboard-enrollments/"+id, adminToken, "", in)
	if w.Code != 503 || len(n.accounts) != 1 {
		t.Fatalf("partial role bundle published: %d %s", w.Code, w.Body.String())
	}
	accounts, err := loadAll[account](n.store, "accounts")
	if err != nil || len(accounts) != 1 {
		t.Fatal("partial role bundle persisted")
	}
	var decoded dashboardRoleTokens
	for _, raw := range []string{`{"nodeId":"a","NodeId":"b"}`, `{"admin":"a","admin":"b"}`, `{"viewer":null}`, `{"surprise":true}`} {
		if dashboardJSON([]byte(raw), &decoded, "nodeId", "admin", "editor", "viewer") == nil {
			t.Fatalf("ambiguous delegation JSON admitted: %s", raw)
		}
	}
}

func TestDashboardRegistryPublicBadgesPreservePublicationAndQueryGates(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	n := dashboardRemoteFixture(t, "node-a")
	if _, err := n.writeMonitor(input([]string{n.ID}, "http://127.0.0.1:1/private-target", ""), nil, "selected-monitor", false); err != nil {
		t.Fatal(err)
	}
	statusCreate(t, n, statusFixture("services"), adminToken)
	var publicBearer atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/public/") && (r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "") {
			publicBearer.Store(true)
		}
		n.ServeHTTP(w, r)
	}))
	defer server.Close()
	row := enrollDashboardNode(t, d, owner, server.URL)
	query := "?monitor=Public+service&registration=" + row.ID + "&group=public-services"
	for _, method := range []string{"GET", "HEAD"} {
		r := httptest.NewRequest(method, "/public/badge/services.svg"+query, nil)
		r.Header.Set("Origin", "https://external-status.example.test")
		r.Header.Set("Authorization", "Bearer "+owner)
		r.Header.Set("Cookie", "proxy-cookie=private-fixture")
		w := httptest.NewRecorder()
		d.ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("Content-Type") != "image/svg+xml; charset=utf-8" || w.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("public %s: %d %s", method, w.Code, w.Body.String())
		}
		if method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("public HEAD returned a body")
		}
		if strings.Contains(w.Body.String(), "private-target") {
			t.Fatal("badge exposed private monitoring data")
		}
	}
	for _, bad := range []string{query + "&registration=" + row.ID, query + ";poison=true", query + "&group=duplicate", query + "&credential=secret"} {
		if w := dashboardHTTP(d, "GET", "/public/badge/services.svg"+bad, "", "", nil); w.Code != 404 {
			t.Fatalf("ambiguous public query admitted: %d", w.Code)
		}
	}
	if publicBearer.Load() {
		t.Fatal("public projection used a dashboard or management credential")
	}
}
