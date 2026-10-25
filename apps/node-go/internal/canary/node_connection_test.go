package canary

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"uptime-canary/apps/node-go/internal/nodeconnection"
)

type invitationFixture struct {
	nodeInvitation
	Token string `json:"token"`
}

// TempDir's existing leaf can be 0755 on Linux. Let the allocator create its
// own private child, as in the retained-origin fixtures; keep its 0700 check.
func connectionIdentityDirectory(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "identity")
}

func connectionInvitation(t *testing.T, d *Dashboard, owner, nodeID string) invitationFixture {
	t.Helper()
	w := dashboardHTTP(d, "POST", "/api/dashboard/invitations", owner, "", map[string]string{"label": "Private outbound node", "nodeId": nodeID})
	var v invitationFixture
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &v) != nil || len(v.Token) != 97 {
		t.Fatalf("invitation: %d", w.Code)
	}
	return v
}

func connectionRequest(d *Dashboard, route, token, id string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", route, strings.NewReader(string(b)))
	r.Header.Set("Authorization", "Bearer "+token)
	if id != "" {
		r.Header.Set("X-Canary-Registration", id)
	}
	w := httptest.NewRecorder()
	d.ConnectionHandler().ServeHTTP(w, r)
	return w
}

func claimFixture(d *Dashboard, invitation invitationFixture, nodeID, key string) *httptest.ResponseRecorder {
	return connectionRequest(d, "/connect/v1/enroll", invitation.Token, "", map[string]string{"nodeId": nodeID, "name": "Reported private name", "version": "fixture", "sourceCommit": "", "connectionToken": key, "adminToken": adminToken})
}

func TestNodeInvitationFirstConfirmationAndScopedRoleTransport(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	d.connectionEnabled = true
	server := httptest.NewTLSServer(d.ConnectionHandler())
	t.Cleanup(server.Close)
	d.connectionURL = server.URL
	invitation := connectionInvitation(t, d, owner, "outbound-a")
	n, err := New(Options{NodeID: "outbound-a", Host: "127.0.0.1", Port: Ptr(0), DBPath: ":memory:", IdentityDirectory: connectionIdentityDirectory(t), AdminToken: adminToken, ClusterToken: clusterToken, DashboardURL: server.URL, DashboardEnrollToken: invitation.Token})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	n.connector.transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	if err = n.Start(); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.invitations[invitation.ID].State == "connected" && d.connections.LastSeen(invitation.ID) != 0
	})
	n.mu.Lock()
	accounts := len(n.accounts)
	n.mu.Unlock()
	d.mu.Lock()
	rows := len(d.registry)
	fingerprint := d.invitations[invitation.ID].Node.Fingerprint
	d.mu.Unlock()
	if rows != 0 || accounts != 1 || fingerprint != n.ConnectionFingerprint() {
		t.Fatal("unconfirmed connection received management authority or fingerprint changed")
	}
	status := dashboardHTTP(d, "GET", "/api/dashboard/invitations/"+invitation.ID, owner, "", nil)
	if strings.Contains(status.Body.String(), invitation.Token) || strings.Contains(status.Body.String(), n.connector.identity.Secret) || strings.Contains(status.Body.String(), adminToken) {
		t.Fatal("invitation status exposed a secret")
	}
	confirmed := dashboardHTTP(d, "POST", "/api/dashboard/invitations/"+invitation.ID+"/confirm", owner, "", nil)
	if confirmed.Code != 201 {
		t.Fatalf("confirmation: %d %s", confirmed.Code, confirmed.Body.String())
	}
	d.mu.Lock()
	row := d.registry[invitation.ID]
	d.mu.Unlock()
	if row.Connection != "outbound" || row.URL != "" || row.State != "starting" {
		t.Fatalf("registration state: %s", row.State)
	}
	if dashboardHTTP(d, "DELETE", "/api/dashboard/invitations/"+row.ID, owner, "", nil).Code != 409 {
		t.Fatal("closing paired invitation removed registry")
	}
	if dashboardHTTP(d, "GET", "/api/overview", owner, row.ID, nil).Code != 200 {
		t.Fatal("management read failed through outbound transport")
	}
	n.mu.Lock()
	accounts = len(n.accounts)
	n.mu.Unlock()
	if accounts != 4 {
		t.Fatal("role bundle not installed exactly once")
	}
	if dashboardHTTP(d, "PATCH", "/api/dashboard/nodes/"+row.ID, owner, "", map[string]string{"label": "Renamed outbound"}).Code != 200 {
		t.Fatal("outbound rename failed")
	}
	if dashboardHTTP(d, "PATCH", "/api/dashboard/nodes/"+row.ID, owner, "", map[string]string{"url": "http://127.0.0.1:1"}).Code != 400 {
		t.Fatal("outbound address became editable")
	}
	for _, target := range []string{"/internal/snapshot", "/api/auth/login", "/api/accounts/other", "/api/dashboard-enrollments/" + strings.Repeat("f", 32), "//other/api/overview", "https://other/api/overview"} {
		if connectionNodePath("POST", target, row.ID) {
			t.Fatalf("unscoped management route accepted: %s", target)
		}
	}
	secrets, err := d.openSecrets(row)
	if err != nil {
		t.Fatal(err)
	}
	resp, _, err := d.nodeRequest(context.Background(), row, secrets.Viewer, "POST", "/api/monitors", []byte(`{}`))
	if err != nil || resp.StatusCode != 403 {
		t.Fatal("connection elevated viewer authority")
	}
	removed := dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+row.ID, owner, "", nil)
	if removed.Code != 204 {
		t.Fatalf("removal: %d %s", removed.Code, removed.Body.String())
	}
	n.mu.Lock()
	accounts = len(n.accounts)
	n.mu.Unlock()
	if accounts != 1 || d.connections.LastSeen(row.ID) != 0 {
		t.Fatal("removal retained delegated authority or session")
	}
}

func TestNodeInvitationExpiryReplayIdentityAndCreatorRevocation(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	d.connectionEnabled, d.connectionURL = true, "https://connection.fixture.invalid"
	v := connectionInvitation(t, d, owner, "expected-node")
	key, _ := nodeconnection.RandomKey()
	if claimFixture(d, v, "wrong-node", key).Code != 409 {
		t.Fatal("wrong node ID claimed invitation")
	}
	if claimFixture(d, v, "expected-node", key).Code != 201 || claimFixture(d, v, "expected-node", key).Code != 201 {
		t.Fatal("same-device lost-answer claim was not idempotent")
	}
	other, _ := nodeconnection.RandomKey()
	if claimFixture(d, v, "expected-node", other).Code != 409 {
		t.Fatal("another device replaced pending claim")
	}
	d.mu.Lock()
	before := d.invitations[v.ID]
	d.mu.Unlock()
	if strings.Contains(before.Credentials, adminToken) || before.TokenHash == v.Token {
		t.Fatal("plaintext pairing authority persisted")
	}
	d.accounts.node.mu.Lock()
	a := d.accounts.node.accounts[before.CreatorID]
	a.AuthVersion++
	d.accounts.node.accounts[a.ID] = a
	d.accounts.node.mu.Unlock()
	if claimFixture(d, v, "expected-node", key).Code != 401 {
		t.Fatal("revoked invitation creator retained claim authority")
	}
	if connectionRequest(d, "/connect/v1/open", key, v.ID, map[string]string{"registrationId": v.ID, "nodeId": "expected-node"}).Code != 401 {
		t.Fatal("creator revocation retained pending session authority")
	}
	d.accounts.node.mu.Lock()
	a.AuthVersion--
	d.accounts.node.accounts[a.ID] = a
	d.accounts.node.mu.Unlock()
	d.mu.Lock()
	expired := d.invitations[v.ID]
	expired.ExpiresAt = time.Now().Add(-time.Second).UnixMilli()
	d.invitations[v.ID] = expired
	d.mu.Unlock()
	if claimFixture(d, v, "expected-node", key).Code != 401 || dashboardHTTP(d, "POST", "/api/dashboard/invitations/"+v.ID+"/confirm", owner, "", nil).Code != 409 {
		t.Fatal("expired invitation retained pairing authority")
	}
	if dashboardHTTP(d, "DELETE", "/api/dashboard/invitations/"+v.ID, owner, "", nil).Code != 204 {
		t.Fatal("expired invitation could not be cancelled")
	}
	if claimFixture(d, v, "expected-node", key).Code != 401 {
		t.Fatal("cancelled invitation replayed")
	}
}

func TestNodeInvitationConfirmationRollbackCreatesNoNodeAuthority(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	d.connectionEnabled, d.connectionURL = true, "https://connection.fixture.invalid"
	v := connectionInvitation(t, d, owner, "rollback-node")
	key, _ := nodeconnection.RandomKey()
	if claimFixture(d, v, "rollback-node", key).Code != 201 {
		t.Fatal("claim failed")
	}
	// Accepted durable-session gate: acknowledge the pending device before
	// exercising the same atomic confirmation rollback assertion.
	if connectionRequest(d, "/connect/v1/open", key, v.ID, map[string]string{"registrationId": v.ID, "nodeId": "rollback-node"}).Code != 200 {
		t.Fatal("device session failed")
	}
	n := d.accounts.node
	n.mu.Lock()
	_, err := n.store.db.Exec(`CREATE TRIGGER invitation_confirm_failure BEFORE INSERT ON records WHEN NEW.category='dashboardRegistry' BEGIN SELECT RAISE(ABORT,'private confirmation failure'); END`)
	n.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	w := dashboardHTTP(d, "POST", "/api/dashboard/invitations/"+v.ID+"/confirm", owner, "", nil)
	if w.Code < 500 {
		t.Fatalf("failed confirmation reported success: %d", w.Code)
	}
	d.mu.Lock()
	_, found := d.invitations[v.ID]
	count := len(d.registry)
	d.mu.Unlock()
	if !found || count != 0 {
		t.Fatal("failed transaction published a registration or consumed the invitation")
	}
	n.mu.Lock()
	_, saved, err := loadOne[registeredNodeRecord](n.store, "dashboardRegistry", v.ID)
	_, invitationSaved, secondErr := loadOne[nodeInvitationRecord](n.store, "dashboardInvitations", v.ID)
	n.mu.Unlock()
	if err != nil || secondErr != nil || saved || !invitationSaved {
		t.Fatal("confirmation rollback did not preserve durable categories")
	}
}

func TestNodeInvitationNeedsDurableDeviceSessionBeforeConfirmation(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	d.connectionEnabled, d.connectionURL = true, "https://connection.fixture.invalid"
	v := connectionInvitation(t, d, owner, "durable-device")
	key, _ := nodeconnection.RandomKey()
	if claimFixture(d, v, "durable-device", key).Code != 201 {
		t.Fatal("claim failed")
	}
	// A claim response can be lost, or the node's subsequent state publication
	// can fail. This point alone must not let the administrator consume it.
	w := dashboardHTTP(d, "GET", "/api/dashboard/invitations/"+v.ID, owner, "", nil)
	var status nodeInvitation
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || status.State != "waiting" {
		t.Fatal("claim was shown as ready before the device opened its durable session")
	}
	w = dashboardHTTP(d, "POST", "/api/dashboard/invitations/"+v.ID+"/confirm", owner, "", nil)
	if w.Code != 409 || len(d.registry) != 0 || len(d.invitations) != 1 {
		t.Fatal("unacknowledged claim was consumed into a registration")
	}
	w = connectionRequest(d, "/connect/v1/open", key, v.ID, map[string]string{"registrationId": v.ID, "nodeId": "durable-device"})
	if w.Code != 200 {
		t.Fatal("authenticated session was refused")
	}
	w = dashboardHTTP(d, "GET", "/api/dashboard/invitations/"+v.ID, owner, "", nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &status) != nil || status.State != "connected" {
		t.Fatal("durable session did not make the invitation ready")
	}
	if dashboardHTTP(d, "DELETE", "/api/dashboard/invitations/"+v.ID, owner, "", nil).Code != 204 || len(d.registry) != 0 {
		t.Fatal("unconfirmed durable session could not be cancelled without registry effects")
	}
}

func TestNodeInvitationLimitsAndAdministratorOnlyRoutes(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	d.connectionEnabled, d.connectionURL = true, "https://connection.fixture.invalid"
	w := dashboardHTTP(d, "POST", "/api/accounts", owner, "", map[string]string{"name": "invitation-viewer", "role": "viewer", "password": dashboardPasswordFixture})
	if w.Code != 201 {
		t.Fatal("viewer fixture could not be created")
	}
	viewer := dashboardPasswordLogin(t, d.accounts, "invitation-viewer", dashboardPasswordFixture)
	for _, route := range []string{"/api/dashboard/onboarding", "/api/dashboard/invitations", "/api/dashboard/invitations/" + strings.Repeat("a", 32)} {
		if dashboardHTTP(d, "GET", route, viewer, "", nil).Code != 403 {
			t.Fatal("nonadministrator could read onboarding secrets or topology")
		}
	}
	if dashboardHTTP(d, "POST", "/api/dashboard/invitations", viewer, "", map[string]string{"label": "forbidden", "nodeId": "forbidden"}).Code != 403 {
		t.Fatal("nonadministrator created invitation")
	}
	for i := range 16 {
		connectionInvitation(t, d, owner, "capacity-"+strings.Repeat("x", i+1))
	}
	w = dashboardHTTP(d, "POST", "/api/dashboard/invitations", owner, "", map[string]string{"label": "overflow", "nodeId": "overflow"})
	if w.Code != 409 || len(d.invitations) != 16 {
		t.Fatal("pending invitation bound exceeded")
	}
}

func TestNodeConnectionRejectsAmbiguousJSONAndRepeatedCredentials(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	d.connectionEnabled, d.connectionURL = true, "https://connection.fixture.invalid"
	v := connectionInvitation(t, d, owner, "exact-node")
	key, _ := nodeconnection.RandomKey()
	for _, body := range []string{
		`{"nodeId":"exact-node","nodeId":"other"}`,
		`{"NodeId":"exact-node"}`,
		`{"nodeId":"exact-node","name":"broken\ud800"}`,
		`{"nodeId":"exact-node","extra":true}`,
	} {
		r := httptest.NewRequest("POST", "/connect/v1/enroll", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+v.Token)
		w := httptest.NewRecorder()
		d.ConnectionHandler().ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("ambiguous claim admitted: %d", w.Code)
		}
	}
	if claimFixture(d, v, "exact-node", key).Code != 201 {
		t.Fatal("valid claim failed after invalid requests")
	}
	for _, header := range []string{"Authorization", "X-Canary-Registration"} {
		body := &forbiddenConnectionRead{}
		r := httptest.NewRequest("POST", "/connect/v1/open", body)
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("X-Canary-Registration", v.ID)
		r.Header.Add(header, "extra")
		w := httptest.NewRecorder()
		d.ConnectionHandler().ServeHTTP(w, r)
		if w.Code != 401 || body.touched {
			t.Fatal("ambiguous credential reached body parser")
		}
	}
}

type forbiddenConnectionRead struct{ touched bool }

func (r *forbiddenConnectionRead) Read([]byte) (int, error) { r.touched = true; return 0, io.EOF }

func TestNodeConnectionDedicatedListenerAuthenticatesBeforeBody(t *testing.T) {
	d, _, _ := dashboardRegistryFixture(t)
	d.connectionEnabled, d.connectionURL = true, "https://connection.fixture.invalid"
	for _, route := range []string{"/", "/api/auth/status", "/api/accounts", "/api/overview", "/internal/snapshot", "/connect/v1/unknown"} {
		w := dashboardHTTP(d.ConnectionHandler(), "GET", route, "", "", nil)
		if w.Code != 404 {
			t.Fatalf("connection-only listener exposed %s: %d", route, w.Code)
		}
	}
	for _, route := range []string{"/connect/v1/enroll", "/connect/v1/result", "/connect/v1/open", "/connect/v1/poll"} {
		body := &forbiddenConnectionRead{}
		r := httptest.NewRequest("POST", route, body)
		w := httptest.NewRecorder()
		d.ConnectionHandler().ServeHTTP(w, r)
		if w.Code != 401 || body.touched {
			t.Fatal("unauthenticated protocol request read its body")
		}
	}
	r := httptest.NewRequest("POST", "/connect/v1/open", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://browser.invalid")
	w := httptest.NewRecorder()
	d.ConnectionHandler().ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("browser origin admitted to node-only listener")
	}
}

func TestNodeConnectorIdentityPinsOriginAndSurvivesRestartWithoutInvitation(t *testing.T) {
	dir := connectionIdentityDirectory(t)
	opts := Options{NodeID: "retained-node", DBPath: ":memory:", IdentityDirectory: dir, AdminToken: adminToken, ClusterToken: clusterToken, DashboardURL: "https://connection.fixture.invalid", DashboardEnrollToken: strings.Repeat("a", 32) + "." + strings.Repeat("b", 64)}
	n, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	first := n.ConnectionFingerprint()
	state := n.connector.identity
	state.RegistrationID = strings.Repeat("a", 32)
	data, _ := json.Marshal(state)
	if err = n.store.origin.SaveConnectionState(n.connector.saved, data); err != nil {
		t.Fatal(err)
	}
	_ = n.Stop()
	opts.DashboardEnrollToken = ""
	n, err = New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if n.ConnectionFingerprint() != first || n.connector.invitation != "" {
		t.Fatal("restart replaced device credential or needed consumed invitation")
	}
	_ = n.Stop()
	opts.DashboardURL = "https://other.fixture.invalid"
	if n, err = New(opts); err == nil {
		_ = n.Stop()
		t.Fatal("retained identity followed another origin")
	}
	opts.DashboardURL = "http://connection.fixture.invalid"
	if n, err = New(opts); err == nil {
		_ = n.Stop()
		t.Fatal("plaintext dashboard connection admitted")
	}
}

func TestNodeConnectorReplacementInvitationPreservesDeviceOnFailedClaim(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	d.connectionEnabled = true
	var refuse atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refuse.Load() {
			errorReply(w, fail(401, "private fixture refused"))
			return
		}
		d.ConnectionHandler().ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	d.connectionURL = server.URL
	first := connectionInvitation(t, d, owner, "replace-node")
	opts := Options{NodeID: "replace-node", DBPath: ":memory:", IdentityDirectory: connectionIdentityDirectory(t), AdminToken: adminToken, ClusterToken: clusterToken, DashboardURL: server.URL, DashboardEnrollToken: first.Token}
	n, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	n.connector.transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	if err = n.connector.claim(context.Background(), n); err != nil {
		n.Stop()
		t.Fatal(err)
	}
	fingerprint := n.ConnectionFingerprint()
	before, err := n.store.origin.ConnectionState()
	if err != nil {
		n.Stop()
		t.Fatal(err)
	}
	_ = n.Stop()
	if dashboardHTTP(d, "DELETE", "/api/dashboard/invitations/"+first.ID, owner, "", nil).Code != 204 {
		t.Fatal("unconfirmed invitation cancellation failed")
	}
	second := connectionInvitation(t, d, owner, "replace-node")
	opts.DashboardEnrollToken = second.Token
	n, err = New(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	n.connector.transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	refuse.Store(true)
	if err = n.connector.claim(context.Background(), n); err == nil {
		t.Fatal("refused replacement claim succeeded")
	}
	after, _ := n.store.origin.ConnectionState()
	if string(before) != string(after) || n.connector.identity.RegistrationID != first.ID {
		t.Fatal("failed claim overwrote the previous device identity")
	}
	refuse.Store(false)
	if err = n.connector.claim(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	if n.connector.identity.RegistrationID != second.ID || n.ConnectionFingerprint() != fingerprint {
		t.Fatal("replacement invitation discarded the pinned device")
	}
}

func TestNodeConnectionDualListenerShutdownAndFailureAreJoined(t *testing.T) {
	for _, broken := range []bool{false, true} {
		first, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		second, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			first.Close()
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		if broken {
			first.Close()
		}
		servers := []*http.Server{{Handler: http.NotFoundHandler()}, {Handler: http.NotFoundHandler()}}
		done := make(chan error, 1)
		go func() { done <- serveDashboardListeners(ctx, servers, []net.Listener{first, second}) }()
		if !broken {
			client := &http.Client{Timeout: time.Second}
			for _, listener := range []net.Listener{first, second} {
				eventually(t, func() bool {
					r, e := client.Get("http://" + listener.Addr().String() + "/")
					if e != nil {
						return false
					}
					r.Body.Close()
					return r.StatusCode == 404
				})
			}
		}
		if !broken {
			cancel()
		}
		select {
		case err := <-done:
			if broken && err == nil {
				t.Fatal("listener failure was reported as clean shutdown")
			}
		case <-time.After(6 * time.Second):
			first.Close()
			second.Close()
			t.Fatal("dual listeners did not join")
		}
		cancel()
		for _, listener := range []net.Listener{first, second} {
			c, e := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
			if e == nil {
				c.Close()
				t.Fatal("a listener survived shutdown")
			}
		}
	}
}

func TestNodeConnectorRefusesRedirectWithoutFollowingOrForwardingCredential(t *testing.T) {
	var followed atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/connect/v1/open" {
			w.Header().Set("Location", "/must-not-follow")
			w.WriteHeader(302)
			return
		}
		followed.Store(true)
		reply(w, 200, map[string]any{"ok": true})
	}))
	defer server.Close()
	n, err := New(Options{NodeID: "redirect-node", DBPath: ":memory:", IdentityDirectory: connectionIdentityDirectory(t), AdminToken: adminToken, ClusterToken: clusterToken, DashboardURL: server.URL, DashboardEnrollToken: strings.Repeat("a", 32) + "." + strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Stop()
	n.connector.transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	status, _, err := n.connector.post(context.Background(), "/connect/v1/open", n.connector.identity.Secret, map[string]string{}, 4096)
	if status != 302 || err == nil || followed.Load() {
		t.Fatal("redirect was followed by the node connector")
	}
	if n.connector.transport.Proxy != nil || n.connector.client.Jar != nil {
		t.Fatal("ambient proxy or cookie authority enabled")
	}
}
