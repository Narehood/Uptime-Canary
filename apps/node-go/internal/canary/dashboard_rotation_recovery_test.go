package canary

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func replaceDelegateHash(t *testing.T, node *Node, registration string) {
	t.Helper()
	node.mu.Lock()
	defer node.mu.Unlock()
	key := dashboardDelegateID(registration, "admin")
	value := node.accounts[key]
	value.Hash = digest("Private fixture independently replaced administrator credential")
	if err := put(node.store.db, "accounts", key, value); err != nil {
		t.Fatal(err)
	}
	node.accounts[key] = value
}

func TestDashboardRotationRefusedAuthorityDoesNotPublishJournal(t *testing.T) {
	dashboard, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "refused-rotation")
	var patches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" {
			patches.Add(1)
		}
		node.ServeHTTP(w, r)
	}))
	defer server.Close()
	row := enrollDashboardNode(t, dashboard, owner, server.URL)
	before := dashboard.registry[row.ID]
	replaceDelegateHash(t, node, row.ID)
	response := dashboardHTTP(dashboard, "POST", "/api/dashboard/nodes/"+row.ID+"/rotate", owner, "", nil)
	after := dashboard.registry[row.ID]
	if response.Code != 502 || after.Credentials != before.Credentials || after.State != before.State || patches.Load() != 0 {
		t.Fatalf("known refused authority created an unfinishable rotation: status=%d state=%s patches=%d", response.Code, after.State, patches.Load())
	}
}

func TestDashboardRotationMissingDelegationDoesNotPublishJournal(t *testing.T) {
	dashboard, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "incomplete-rotation")
	server := httptest.NewServer(node)
	defer server.Close()
	row := enrollDashboardNode(t, dashboard, owner, server.URL)
	before := dashboard.registry[row.ID]
	// A restored node can lack an enrollment that the dashboard still retains.
	// Recovery authority must not invent a replacement for a missing/revoked row.
	key := dashboardDelegateID(row.ID, "viewer")
	node.mu.Lock()
	err := remove(node.store.db, "accounts", key)
	delete(node.accounts, key)
	node.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	response := dashboardHTTP(dashboard, "POST", "/api/dashboard/nodes/"+row.ID+"/rotate", owner, "", map[string]string{"token": adminToken})
	after := dashboard.registry[row.ID]
	if response.Code != 409 || after.Credentials != before.Credentials || after.State != before.State || len(node.accounts) != 3 {
		t.Fatalf("missing delegation created an unfinishable or revived bundle: status=%d state=%s", response.Code, after.State)
	}
}

func TestDashboardRotationLateAuthorityLossCanRecoverSameJournal(t *testing.T) {
	dashboard, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "late-rotation")
	var revoked atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/api/dashboard-enrollments/") && revoked.CompareAndSwap(false, true) {
			replaceDelegateHash(t, node, strings.TrimPrefix(r.URL.Path, "/api/dashboard-enrollments/"))
		}
		node.ServeHTTP(w, r)
	}))
	defer server.Close()
	row := enrollDashboardNode(t, dashboard, owner, server.URL)
	path := "/api/dashboard/nodes/" + row.ID
	response := dashboardHTTP(dashboard, "POST", path+"/rotate", owner, "", nil)
	if response.Code != 202 || dashboard.registry[row.ID].State != "rotating" {
		t.Fatalf("late authority loss discarded the durable journal: %d", response.Code)
	}
	desired, err := dashboard.openSecrets(dashboard.registry[row.ID])
	if err != nil {
		t.Fatal(err)
	}
	before := dashboard.registry[row.ID]
	response = dashboardHTTP(dashboard, "POST", path+"/retry", owner, "", map[string]string{"token": clusterToken})
	if response.Code != 502 || dashboard.registry[row.ID].Credentials != before.Credentials {
		t.Fatal("a refused recovery credential replaced the journal")
	}
	response = dashboardHTTP(dashboard, "POST", path+"/retry", owner, "", map[string]string{"token": adminToken})
	if response.Code != 200 {
		t.Fatalf("valid administrator could not finish the pending update: %d", response.Code)
	}
	confirmed, err := dashboard.openSecrets(dashboard.registry[row.ID])
	if err != nil || confirmed.Admin != desired.Admin || confirmed.Editor != desired.Editor || confirmed.Viewer != desired.Viewer || confirmed.EnrollmentToken != "" || dashboard.registry[row.ID].State != "starting" || len(node.accounts) != 4 {
		t.Fatal("recovery replaced the desired bundle, registration or account set")
	}
	for _, role := range dashboardRoles {
		if result := identityCall(node, "GET", "/api/auth/me", "", desired.token(role)); result.Code != 200 {
			t.Fatal("recovery did not install the existing desired role bundle")
		}
	}
}

func stagePendingRotation(t *testing.T, dashboard *Dashboard, id string) registeredNodeRecord {
	t.Helper()
	dashboard.mu.Lock()
	defer dashboard.mu.Unlock()
	row := dashboard.registry[id]
	secret, err := dashboard.openSecrets(row)
	if err != nil {
		t.Fatal(err)
	}
	secret.EnrollmentToken = secret.Admin
	secret.Admin = digest("private pending administrator " + id)
	secret.Editor = digest("private pending editor " + id)
	secret.Viewer = digest("private pending viewer " + id)
	row.State = "rotating"
	row.Credentials, err = dashboard.sealSecrets(row, secret)
	if err == nil {
		err = dashboard.saveRegistry(row)
	}
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestDashboardRotationRecoveryCommitFailureCannotReachNodeMutation(t *testing.T) {
	dashboard, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "recovery-commit-failure")
	var patches atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" {
			patches.Add(1)
		}
		node.ServeHTTP(w, r)
	}))
	defer server.Close()
	row := enrollDashboardNode(t, dashboard, owner, server.URL)
	before := stagePendingRotation(t, dashboard, row.ID)
	if _, err := dashboard.accounts.node.store.db.Exec(`CREATE TRIGGER recovery_commit_failure BEFORE INSERT ON records WHEN NEW.category='dashboardRegistry' BEGIN SELECT RAISE(FAIL,'private recovery commit failure'); END`); err != nil {
		t.Fatal(err)
	}
	response := dashboardHTTP(dashboard, "POST", "/api/dashboard/nodes/"+row.ID+"/retry", owner, "", map[string]string{"token": adminToken})
	if response.Code != 503 || patches.Load() != 0 || dashboard.registry[row.ID].Credentials != before.Credentials || dashboard.registry[row.ID].State != "rotating" {
		t.Fatal("failed recovery journal write changed credentials or attempted a remote mutation")
	}
}

func TestDashboardRotationRetryRejectsAmbiguousRecoveryBeforeNetwork(t *testing.T) {
	dashboard, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "recovery-admission")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		node.ServeHTTP(w, r)
	}))
	defer server.Close()
	row := enrollDashboardNode(t, dashboard, owner, server.URL)
	before := stagePendingRotation(t, dashboard, row.ID)
	requests.Store(0)
	for _, body := range []string{`null`, `[]`, `{"token":null}`, `{"Token":"unused"}`, `{"token":"short"}`,
		`{"token":"` + adminToken + `","token":"` + adminToken + `"}`, `{"token":"` + adminToken + `","extra":false}`,
		`{"token":"` + strings.Repeat("x", 5000) + `"}`} {
		request := httptest.NewRequest("POST", "/api/dashboard/nodes/"+row.ID+"/retry", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+owner)
		response := httptest.NewRecorder()
		dashboard.ServeHTTP(response, request)
		if response.Code != 400 && response.Code != 413 {
			t.Error("ambiguous recovery body was accepted", response.Code)
		}
	}
	if requests.Load() != 0 || dashboard.registry[row.ID].Credentials != before.Credentials || dashboard.registry[row.ID].State != "rotating" {
		t.Fatal("invalid recovery body reached the node or changed the durable journal")
	}
}
