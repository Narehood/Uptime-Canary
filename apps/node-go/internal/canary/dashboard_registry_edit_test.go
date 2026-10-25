package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDashboardRegistryEditPreservesRegistrationAndChecksIdentity(t *testing.T) {
	dashboard, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "registered-node")
	oldServer := httptest.NewServer(node)
	row := enrollDashboardNode(t, dashboard, owner, oldServer.URL)
	before := dashboard.registry[row.ID]
	oldServer.Close()
	newServer := httptest.NewServer(node)
	defer newServer.Close()
	path := "/api/dashboard/nodes/" + row.ID
	response := dashboardHTTP(dashboard, "PATCH", path, owner, "", map[string]string{"label": "Moved private node", "url": newServer.URL})
	if response.Code != 200 {
		t.Fatalf("address edit: %d %s", response.Code, response.Body.String())
	}
	after := dashboard.registry[row.ID]
	secret, err := dashboard.openSecrets(after)
	oldSecret, oldError := dashboard.openSecrets(before)
	if err != nil || oldError != nil || secret != oldSecret || after.NodeID != before.NodeID || after.URL != newServer.URL || after.ID != before.ID || after.Credentials == before.Credentials || len(node.accounts) != 4 {
		t.Fatal("address edit replaced registration, roles or identity")
	}
	wrong := dashboardRemoteFixture(t, "different-node")
	wrongServer := httptest.NewServer(wrong)
	defer wrongServer.Close()
	response = dashboardHTTP(dashboard, "PATCH", path, owner, "", map[string]string{"label": "Wrong identity", "url": wrongServer.URL})
	if response.Code != 502 || dashboard.registry[row.ID].URL != newServer.URL || dashboard.registry[row.ID].Credentials != after.Credentials {
		t.Fatal("different identity replaced the registered route")
	}
	newServer.Close()
	response = dashboardHTTP(dashboard, "PATCH", path, owner, "", map[string]string{"label": "Renamed while unavailable"})
	if response.Code != 200 || dashboard.registry[row.ID].Label != "Renamed while unavailable" {
		t.Fatal("label edit required a live remote node")
	}
}

func TestDashboardRegistryEditCannotOverwriteConcurrentChanges(t *testing.T) {
	dashboard, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "edit-race")
	oldServer := httptest.NewServer(node)
	defer oldServer.Close()
	row := enrollDashboardNode(t, dashboard, owner, oldServer.URL)
	entered, release := make(chan struct{}), make(chan struct{})
	var blocked atomic.Bool
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	newServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/auth/me" && blocked.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		node.ServeHTTP(writer, request)
	}))
	defer newServer.Close()
	defer unblock()
	path := "/api/dashboard/nodes/" + row.ID
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- dashboardHTTP(dashboard, "PATCH", path, owner, "", map[string]string{"label": "First edit", "url": newServer.URL})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("address verification did not enter fixture")
	}
	response := dashboardHTTP(dashboard, "PATCH", path, owner, "", map[string]string{"label": "Concurrent label"})
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	unblock()
	select {
	case response = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("address edit did not finish")
	}
	if response.Code != 409 || dashboard.registry[row.ID].Label != "Concurrent label" || dashboard.registry[row.ID].URL != oldServer.URL {
		t.Fatal("late verification overwrote a newer registration edit")
	}
}

func TestDashboardCredentialRotationRevokesOldRolesAndRecoversLostAnswer(t *testing.T) {
	dashboard, owner, options := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "rotate-node")
	var lose atomic.Bool
	lose.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == "PATCH" && strings.HasPrefix(request.URL.Path, "/api/dashboard-enrollments/") && lose.Swap(false) {
			response := httptest.NewRecorder()
			node.ServeHTTP(response, request)
			if response.Code != 200 {
				t.Errorf("rotation failed inside fixture: %d %s", response.Code, response.Body.String())
			}
			writer.Header().Set("X-Canary-Node-Id", node.ID)
			errorReply(writer, fail(503, "Private fixture dropped response"))
			return
		}
		node.ServeHTTP(writer, request)
	}))
	defer server.Close()
	row := enrollDashboardNode(t, dashboard, owner, server.URL)
	before, err := dashboard.openSecrets(dashboard.registry[row.ID])
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/dashboard/nodes/" + row.ID
	response := dashboardHTTP(dashboard, "POST", path+"/rotate", owner, "", nil)
	if response.Code != 202 || dashboard.registry[row.ID].State != "rotating" {
		t.Fatalf("ambiguous rotation was not retained: %d %s", response.Code, response.Body.String())
	}
	desired, err := dashboard.openSecrets(dashboard.registry[row.ID])
	if err != nil {
		t.Fatal(err)
	}
	if desired.EnrollmentToken != before.Admin || desired.Admin == before.Admin || desired.Editor == before.Editor || desired.Viewer == before.Viewer {
		t.Fatal("desired credentials were not journaled separately")
	}
	for _, role := range dashboardRoles {
		if result := identityCall(node, "GET", "/api/auth/me", "", before.token(role)); result.Code != 401 {
			t.Fatal("old role credential remained valid")
		}
		if result := identityCall(node, "GET", "/api/auth/me", "", desired.token(role)); result.Code != 200 {
			t.Fatal("new role credential was not installed")
		}
	}
	if response := dashboardHTTP(dashboard, "GET", "/api/overview", owner, row.ID, nil); response.Code != 409 {
		t.Fatal("private proxy used an unconfirmed role bundle")
	}
	if len(node.accounts) != 4 {
		t.Fatal("rotation created extra node accounts")
	}
	dashboard.Close()
	dashboard.accounts.Close()
	accounts, err := NewDashboardAccounts(options)
	if err != nil {
		t.Fatal(err)
	}
	defer accounts.Close()
	reopened, err := NewDashboard(DashboardOptions{Accounts: accounts, Key: dashboardKeyFixture})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.registry[row.ID].State != "rotating" {
		t.Fatal("restart discarded the pending credential journal")
	}
	response = dashboardHTTP(reopened, "POST", path+"/retry", owner, "", nil)
	if response.Code != 200 {
		t.Fatalf("lost answer retry failed: %d %s", response.Code, response.Body.String())
	}
	confirmed, err := reopened.openSecrets(reopened.registry[row.ID])
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Admin != desired.Admin || confirmed.Editor != desired.Editor || confirmed.Viewer != desired.Viewer || confirmed.EnrollmentToken != "" || reopened.registry[row.ID].URL != row.URL {
		t.Fatal("retry rotated twice or retained the old administrator")
	}
	if _, err := reopened.checkNode(context.Background(), row.ID); err != nil {
		t.Fatal(err)
	}
	if response := dashboardHTTP(reopened, "GET", "/api/overview", owner, row.ID, nil); response.Code != 200 {
		t.Fatal("confirmed rotation broke monitoring")
	}
	for _, value := range []string{before.Admin, before.Editor, before.Viewer, desired.Admin, desired.Editor, desired.Viewer} {
		if strings.Contains(response.Body.String(), value) {
			t.Fatal("rotation exposed a role credential")
		}
	}
}

func TestDashboardCredentialRotationAtomicNodeRollback(t *testing.T) {
	dashboard, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "rotate-rollback")
	server := httptest.NewServer(node)
	defer server.Close()
	row := enrollDashboardNode(t, dashboard, owner, server.URL)
	before, _ := dashboard.openSecrets(dashboard.registry[row.ID])
	if _, err := node.store.db.Exec(`CREATE TRIGGER rotate_failure BEFORE INSERT ON records WHEN NEW.category='accounts' AND NEW.id LIKE '%-editor' BEGIN SELECT RAISE(FAIL,'private rotation rollback'); END`); err != nil {
		t.Fatal(err)
	}
	updated := dashboardRoleTokens{NodeID: node.ID, Admin: "new-admin-rotation-fixture-0123456789", Editor: "new-editor-rotation-fixture-0123456789", Viewer: "new-viewer-rotation-fixture-0123456789"}
	raw, _ := json.Marshal(updated)
	response := identityCall(node, "PATCH", "/api/dashboard-enrollments/"+row.ID, string(raw), before.Admin)
	if response.Code != 503 {
		t.Fatalf("failed role transaction was acknowledged: %d %s", response.Code, response.Body.String())
	}
	for _, role := range dashboardRoles {
		nodeAccount := node.accounts[dashboardDelegateID(row.ID, role)]
		stored, found, err := loadOne[account](node.store, "accounts", nodeAccount.ID)
		if err != nil || !found || !matches(nodeAccount.Hash, before.token(role)) || !matches(stored.Hash, before.token(role)) || matches(nodeAccount.Hash, updated.token(role)) {
			t.Fatal("failed rotation published or persisted a partial bundle")
		}
	}
}
