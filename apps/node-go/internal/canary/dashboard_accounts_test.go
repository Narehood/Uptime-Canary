package canary

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const dashboardSetupFixture = "dashboard-first-owner-private-fixture-000000000000"
const dashboardPasswordFixture = "private dashboard password"

func dashboardAccountFixture(t *testing.T) (*DashboardAccounts, DashboardAccountOptions) {
	t.Helper()
	o := DashboardAccountOptions{Directory: t.TempDir(), SetupToken: dashboardSetupFixture,
		CORSOrigins: []string{"https://dashboard.example.test"}}
	d, err := NewDashboardAccounts(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d, o
}

func dashboardAccountRequest(d *DashboardAccounts, method, path, token string, body any) *httptest.ResponseRecorder {
	var raw string
	if body != nil {
		b, _ := json.Marshal(body)
		raw = string(b)
	}
	r := httptest.NewRequest(method, path, strings.NewReader(raw))
	r.RemoteAddr = "192.0.2.1:12345"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	return w
}

func setupDashboardOwner(t *testing.T, d *DashboardAccounts, name string) AccountView {
	t.Helper()
	w := dashboardAccountRequest(d, "POST", "/api/dashboard/setup", "", map[string]string{
		"name": name, "password": dashboardPasswordFixture, "code": dashboardSetupFixture})
	var owner AccountView
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &owner) != nil {
		t.Fatalf("owner setup: %d %s", w.Code, w.Body.String())
	}
	return owner
}

func dashboardPasswordLogin(t *testing.T, d *DashboardAccounts, name, password string) string {
	t.Helper()
	w := dashboardAccountRequest(d, "POST", "/api/auth/login", "", map[string]string{"name": name, "password": password})
	var value struct{ Token string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &value) != nil || value.Token == "" {
		t.Fatalf("dashboard login: %d %s", w.Code, w.Body.String())
	}
	return value.Token
}

func TestDashboardAccountsSetupWithoutAnyNode(t *testing.T) {
	d, _ := dashboardAccountFixture(t)
	if d.node.started || len(d.node.settings.Peers) != 0 || len(d.node.heads) != 0 {
		t.Fatal("dashboard identity started or acquired monitoring work")
	}
	status := dashboardAccountRequest(d, "GET", "/api/auth/status", "", nil)
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"setupRequired":true`) || strings.Contains(status.Body.String(), dashboardSetupFixture) {
		t.Fatalf("initial status: %d %s", status.Code, status.Body.String())
	}
	owner := setupDashboardOwner(t, d, "owner")
	if owner.Role != "admin" || !owner.PasswordEnabled || len(d.node.accounts) != 1 {
		t.Fatalf("owner state: %+v", owner)
	}
	if _, exists := d.node.accounts["bootstrap-admin"]; exists {
		t.Fatal("hidden bootstrap authority survived owner creation")
	}
	if w := dashboardAccountRequest(d, "GET", "/api/auth/me", dashboardServiceToken("dashboard-identity-bootstrap", dashboardSetupFixture), nil); w.Code != 401 {
		t.Fatalf("bootstrap bearer survived owner creation: %d", w.Code)
	}
	token := dashboardPasswordLogin(t, d, "owner", dashboardPasswordFixture)
	me := dashboardAccountRequest(d, "GET", "/api/auth/me", token, nil)
	if me.Code != 200 || !strings.Contains(me.Body.String(), owner.ID) {
		t.Fatalf("owner identity: %d %s", me.Code, me.Body.String())
	}
	for _, route := range []string{"/api/overview", "/api/monitors", "/api/push", "/internal/snapshot", "/healthz"} {
		if w := dashboardAccountRequest(d, "GET", route, token, nil); w.Code != 404 {
			t.Fatalf("identity facade exposed %s: %d", route, w.Code)
		}
	}
}

func TestDashboardAccountsSetupRequiresPrivateProofAndExactFields(t *testing.T) {
	d, _ := dashboardAccountFixture(t)
	for _, body := range []map[string]string{
		{"name": "owner", "password": dashboardPasswordFixture},
		{"name": "owner", "password": dashboardPasswordFixture, "code": "wrong"},
		{"name": "owner", "password": "short", "code": dashboardSetupFixture},
		{"name": "owner", "password": dashboardPasswordFixture, "code": dashboardSetupFixture, "role": "admin"},
	} {
		if w := dashboardAccountRequest(d, "POST", "/api/dashboard/setup", "", body); w.Code != 400 && w.Code != 401 {
			t.Fatalf("invalid setup admitted: %d", w.Code)
		}
		if d.Initialized() {
			t.Fatal("invalid setup initialized the dashboard")
		}
	}
	r := httptest.NewRequest("POST", "/api/dashboard/setup", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://unrelated.example.test")
	w := httptest.NewRecorder()
	d.ServeHTTP(w, r)
	if w.Code != 403 || d.Initialized() {
		t.Fatal("unrelated browser origin reached first-owner setup")
	}
}

func TestDashboardAccountsConcurrentOwnerAndRestart(t *testing.T) {
	d, options := dashboardAccountFixture(t)
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := dashboardAccountRequest(d, "POST", "/api/dashboard/setup", "", map[string]string{
				"name": "owner", "password": dashboardPasswordFixture, "code": dashboardSetupFixture})
			results <- w.Code
		}()
	}
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for code := range results {
		counts[code]++
	}
	if counts[201] != 1 || counts[409] != 1 {
		t.Fatalf("concurrent owner claims: %v", counts)
	}
	token := dashboardPasswordLogin(t, d, "owner", dashboardPasswordFixture)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewDashboardAccounts(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if !restarted.Initialized() || dashboardAccountRequest(restarted, "GET", "/api/auth/me", token, nil).Code != 200 {
		t.Fatal("dashboard owner/session did not survive its private database restart")
	}
	if w := dashboardAccountRequest(restarted, "POST", "/api/dashboard/setup", "", map[string]string{
		"name": "second", "password": dashboardPasswordFixture, "code": dashboardSetupFixture}); w.Code != 409 {
		t.Fatalf("setup code reused after restart: %d", w.Code)
	}
}

func TestDashboardAccountsRolesLogoutAndLastAdministrator(t *testing.T) {
	d, _ := dashboardAccountFixture(t)
	owner := setupDashboardOwner(t, d, "owner")
	token := dashboardPasswordLogin(t, d, "owner", dashboardPasswordFixture)
	w := dashboardAccountRequest(d, "POST", "/api/accounts", token, map[string]string{
		"name": "reader", "role": "viewer", "password": dashboardPasswordFixture})
	if w.Code != 201 {
		t.Fatalf("dashboard account creation: %d %s", w.Code, w.Body.String())
	}
	reader := dashboardPasswordLogin(t, d, "reader", dashboardPasswordFixture)
	if w := dashboardAccountRequest(d, "POST", "/api/accounts", reader, map[string]string{
		"name": "intruder", "role": "admin", "password": dashboardPasswordFixture}); w.Code != 403 {
		t.Fatalf("viewer created an account: %d", w.Code)
	}
	if w := dashboardAccountRequest(d, "DELETE", "/api/accounts/"+owner.ID, token, nil); w.Code != 409 {
		t.Fatalf("last dashboard administrator removed: %d", w.Code)
	}
	if w := dashboardAccountRequest(d, "POST", "/api/auth/logout", reader, nil); w.Code != 204 {
		t.Fatalf("dashboard logout: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/api/auth/me", nil)
	r.Header.Set("Authorization", "Bearer "+reader)
	if _, err := d.Authenticate(r); err == nil {
		t.Fatal("revoked dashboard session authenticated")
	}
}

func TestDashboardAccountsSetupRollbackIsNotPublished(t *testing.T) {
	d, options := dashboardAccountFixture(t)
	_, err := d.node.store.db.Exec(`CREATE TRIGGER dashboard_owner_failure BEFORE INSERT ON records WHEN NEW.category = 'settings' AND NEW.id = 'dashboardOwnerInitialized' BEGIN SELECT RAISE(ABORT, 'private fixture failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	w := dashboardAccountRequest(d, "POST", "/api/dashboard/setup", "", map[string]string{
		"name": "owner", "password": dashboardPasswordFixture, "code": dashboardSetupFixture})
	if w.Code != http.StatusServiceUnavailable || d.Initialized() || len(d.node.accounts) != 1 {
		t.Fatalf("failed owner transaction published: %d", w.Code)
	}
	if _, err := d.node.store.db.Exec(`DROP TRIGGER dashboard_owner_failure`); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	restarted, err := NewDashboardAccounts(options)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	setupDashboardOwner(t, restarted, "owner")
}

func TestDashboardAccountsInitializedDatabaseNeverReopensBootstrap(t *testing.T) {
	d, options := dashboardAccountFixture(t)
	setupDashboardOwner(t, d, "owner")
	// Corrupt only a private fixture to model an incomplete restoration. Generic
	// node startup can bootstrap an empty account table; a dashboard already
	// marked initialized must refuse that state instead of exposing the token.
	if _, err := d.node.store.db.Exec(`DELETE FROM records WHERE category='accounts'`); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	if reopened, err := NewDashboardAccounts(options); err == nil {
		_ = reopened.Close()
		t.Fatal("initialized account database reopened bootstrap authority")
	}
}
