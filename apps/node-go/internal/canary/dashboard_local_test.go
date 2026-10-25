package canary

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func configuredDashboardFixture(t *testing.T, old *Dashboard, address, token string) *Dashboard {
	t.Helper()
	d, err := NewDashboard(DashboardOptions{Accounts: old.accounts, Key: dashboardKeyFixture, LocalNodeURL: address, LocalNodeToken: token})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d
}

func onlyDashboardRecord(t *testing.T, d *Dashboard) registeredNodeRecord {
	t.Helper()
	if len(d.registry) != 1 {
		t.Fatalf("expected one registration, got %d", len(d.registry))
	}
	for _, row := range d.registry {
		return row
	}
	panic("unreachable")
}

func TestDashboardLocalConfigurationRepairsPendingEnrollment(t *testing.T) {
	for _, changed := range []string{"token", "url"} {
		t.Run(changed, func(t *testing.T) {
			d, _, _ := dashboardRegistryFixture(t)
			n := dashboardRemoteFixture(t, "local-node")
			server := httptest.NewServer(n)
			defer server.Close()
			address, token := server.URL, "wrong-private-enrollment-token-1234567890"
			if changed == "url" {
				address, token = "http://127.0.0.1:1", adminToken
			}
			d = configuredDashboardFixture(t, d, address, token)
			d.ensureLocalNode(context.Background())
			before := onlyDashboardRecord(t, d)
			prior, err := d.openSecrets(before)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = d.finishEnrollment(context.Background(), before.ID); err == nil || len(n.accounts) != 1 {
				t.Fatal("invalid initial configuration unexpectedly provisioned the node")
			}
			d = configuredDashboardFixture(t, d, server.URL, adminToken)
			d.ensureLocalNode(context.Background())
			after := onlyDashboardRecord(t, d)
			secret, err := d.openSecrets(after)
			if err != nil || after.ID != before.ID || after.URL != server.URL || secret.EnrollmentToken != adminToken || secret.dashboardRoleTokens != prior.dashboardRoleTokens {
				t.Fatal("corrected configuration did not durably reseal the pending registration without changing its role credentials")
			}
			if _, err = d.finishEnrollment(context.Background(), after.ID); err != nil || len(n.accounts) != 4 {
				t.Fatalf("corrected configuration could not enroll: %v", err)
			}
			d = configuredDashboardFixture(t, d, server.URL, adminToken)
			row := onlyDashboardRecord(t, d)
			if row.ID != before.ID || row.NodeID != n.ID {
				t.Fatal("corrected identity did not survive restart")
			}
		})
	}
}

func TestDashboardLocalDisabledConfigurationAllowsPendingRemoval(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	d = configuredDashboardFixture(t, d, "http://127.0.0.1:1", adminToken)
	d.ensureLocalNode(context.Background())
	row := onlyDashboardRecord(t, d)
	d = configuredDashboardFixture(t, d, "", "")
	if d.publicNode(row, "admin").Local {
		t.Fatal("disabled local enrollment still hides the removal control")
	}
	w := dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+row.ID, owner, "", nil)
	if w.Code != 204 || len(d.registry) != 0 {
		t.Fatalf("unproved placeholder could not be removed without reaching a node: %d %s", w.Code, w.Body.String())
	}
	d = configuredDashboardFixture(t, d, "", "")
	if len(d.registry) != 0 {
		t.Fatal("removed placeholder returned after restart")
	}
}

func TestDashboardLocalMovedConfigurationRetainsRevocation(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	n := dashboardRemoteFixture(t, "old-local-node")
	server := httptest.NewServer(n)
	defer server.Close()
	d = configuredDashboardFixture(t, d, server.URL, adminToken)
	d.ensureLocalNode(context.Background())
	row := onlyDashboardRecord(t, d)
	if _, err := d.finishEnrollment(context.Background(), row.ID); err != nil {
		t.Fatal(err)
	}
	if w := dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+row.ID, owner, "", nil); w.Code != 409 {
		t.Fatal("still-configured local node became removable")
	}
	d = configuredDashboardFixture(t, d, "http://127.0.0.1:1", adminToken)
	d.ensureLocalNode(context.Background())
	row = onlyDashboardRecord(t, d)
	if row.URL != server.URL || d.publicNode(row, "admin").Local {
		t.Fatal("changed configuration either retargeted provisioned credentials or hid removal")
	}
	if w := dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+row.ID, owner, "", nil); w.Code != 204 || len(n.accounts) != 1 {
		t.Fatalf("old local role bundle was not revoked before removal: %d %s", w.Code, w.Body.String())
	}
	d.ensureLocalNode(context.Background())
	if row := onlyDashboardRecord(t, d); row.URL != d.localURL || !row.Local || row.NodeID != "pending" {
		t.Fatal("replacement local configuration was not enrolled after old registration removal")
	}
}

func TestDashboardLocalConfigurationAdoptsManualRegistration(t *testing.T) {
	for _, alias := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-address", true: "verified-alias"}[alias], func(t *testing.T) {
			d, owner, _ := dashboardRegistryFixture(t)
			n := dashboardRemoteFixture(t, "manual-node")
			server := httptest.NewServer(n)
			defer server.Close()
			row := enrollDashboardNode(t, d, owner, server.URL)
			before, err := d.openSecrets(d.registry[row.ID])
			if err != nil {
				t.Fatal(err)
			}
			address := server.URL
			if alias {
				other := httptest.NewServer(http.HandlerFunc(n.ServeHTTP))
				defer other.Close()
				address = other.URL
			}
			d = configuredDashboardFixture(t, d, address, adminToken)
			d.ensureLocalNode(context.Background())
			for id, candidate := range d.registry {
				if candidate.NodeID == "pending" {
					if _, err := d.finishEnrollment(context.Background(), id); err != nil {
						t.Fatalf("existing identity was not adopted: %v", err)
					}
				}
			}
			adopted := onlyDashboardRecord(t, d)
			after, err := d.openSecrets(adopted)
			if err != nil || adopted.ID != row.ID || !adopted.Local || adopted.URL != address || after != before || len(n.accounts) != 4 {
				t.Fatal("manual registration adoption changed its identity or provisioned another role bundle")
			}
			if w := dashboardHTTP(d, "GET", "/api/overview", owner, row.ID, nil); w.Code != 200 {
				t.Fatalf("adopted registration cannot read node: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestDashboardLocalConfigurationWaitsForBusyOperationAndCommit(t *testing.T) {
	d, _, _ := dashboardRegistryFixture(t)
	d = configuredDashboardFixture(t, d, "http://127.0.0.1:1", "original-private-fixture-enrollment-token-123456789")
	d.ensureLocalNode(context.Background())
	before := onlyDashboardRecord(t, d)
	d.busy[before.ID] = true
	d.localToken = adminToken
	d.ensureLocalNode(context.Background())
	if d.registry[before.ID] != before {
		t.Fatal("configuration changed during an owned operation")
	}
	delete(d.busy, before.ID)
	if _, err := d.accounts.node.store.db.Exec(`CREATE TRIGGER local_reseal_failure BEFORE INSERT ON records WHEN NEW.category='dashboardRegistry' BEGIN SELECT RAISE(ABORT, 'private fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	d.ensureLocalNode(context.Background())
	persisted, found, err := loadOne[registeredNodeRecord](d.accounts.node.store, "dashboardRegistry", before.ID)
	if err != nil || !found || persisted != before || d.registry[before.ID] != before {
		t.Fatal("failed reseal transaction changed published or stored credentials")
	}
}

func TestDashboardLocalConfigurationRefreshesProvedEnrollment(t *testing.T) {
	d, _, _ := dashboardRegistryFixture(t)
	n := dashboardRemoteFixture(t, "proved-local-node")
	server := httptest.NewServer(n)
	defer server.Close()
	d = configuredDashboardFixture(t, d, server.URL, adminToken)
	d.ensureLocalNode(context.Background())
	row := onlyDashboardRecord(t, d)
	secret, err := d.openSecrets(row)
	if err != nil {
		t.Fatal(err)
	}
	row, secret, err = d.identifyLocal(context.Background(), row, secret)
	if err != nil || row.NodeID != n.ID {
		t.Fatal("private fixture identity discovery failed")
	}
	secret.EnrollmentToken = "expired-private-fixture-enrollment-token-123456789"
	row.Credentials, err = d.sealSecrets(row, secret)
	if err != nil || d.saveRegistry(row) != nil {
		t.Fatal("cannot prepare interrupted proved enrollment")
	}
	d = configuredDashboardFixture(t, d, server.URL, adminToken)
	d.ensureLocalNode(context.Background())
	if _, err = d.finishEnrollment(context.Background(), row.ID); err != nil || len(n.accounts) != 4 {
		t.Fatalf("corrected credential could not finish proved enrollment: %v", err)
	}
}

func TestDashboardLocalDisabledProvisionedNodeStillRequiresRevocation(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	// A real node can be called pending: only the private proof flag permits
	// deleting an undiscovered placeholder without contacting its node.
	n := dashboardRemoteFixture(t, "pending")
	var unavailable atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if unavailable.Load() {
			w.Header().Set("X-Canary-Node-Id", n.ID)
			w.WriteHeader(503)
			return
		}
		n.ServeHTTP(w, r)
	}))
	defer server.Close()
	d = configuredDashboardFixture(t, d, server.URL, adminToken)
	d.ensureLocalNode(context.Background())
	row := onlyDashboardRecord(t, d)
	if _, err := d.finishEnrollment(context.Background(), row.ID); err != nil {
		t.Fatal(err)
	}
	d = configuredDashboardFixture(t, d, "", "")
	unavailable.Store(true)
	w := dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+row.ID, owner, "", nil)
	if w.Code != 202 || onlyDashboardRecord(t, d).State != "removing" || len(n.accounts) != 4 {
		t.Fatal("disabled local configuration bypassed durable remote revocation")
	}
	unavailable.Store(false)
	w = dashboardHTTP(d, "POST", "/api/dashboard/nodes/"+row.ID+"/retry", owner, "", nil)
	if w.Code != 204 || len(d.registry) != 0 || len(n.accounts) != 1 {
		t.Fatal("retired local registration could not finish remote revocation")
	}
}

func TestDashboardLocalAliasAdoptionRequiresEveryRoleAndAtomicCommit(t *testing.T) {
	for _, failure := range []string{"viewer-refused", "commit-failed"} {
		t.Run(failure, func(t *testing.T) {
			d, owner, _ := dashboardRegistryFixture(t)
			n := dashboardRemoteFixture(t, "alias-local-node")
			server := httptest.NewServer(n)
			defer server.Close()
			registered := enrollDashboardNode(t, d, owner, server.URL)
			original := d.registry[registered.ID]
			secret, err := d.openSecrets(original)
			if err != nil {
				t.Fatal(err)
			}
			alias := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if failure == "viewer-refused" && bearer(r) == secret.Viewer {
					w.Header().Set("X-Canary-Node-Id", n.ID)
					w.WriteHeader(401)
					return
				}
				n.ServeHTTP(w, r)
			}))
			defer alias.Close()
			d = configuredDashboardFixture(t, d, alias.URL, adminToken)
			d.ensureLocalNode(context.Background())
			var pending registeredNodeRecord
			for _, row := range d.registry {
				if row.NodeID == "pending" {
					pending = row
				}
			}
			if failure == "commit-failed" {
				if _, err := d.accounts.node.store.db.Exec(`CREATE TRIGGER local_adoption_failure BEFORE DELETE ON records WHEN OLD.category='dashboardRegistry' BEGIN SELECT RAISE(ABORT, 'private fixture failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := d.finishEnrollment(context.Background(), pending.ID); err == nil {
				t.Fatal("unproved or uncommitted alias was adopted")
			}
			current := d.registry[original.ID]
			persisted, err := loadAll[registeredNodeRecord](d.accounts.node.store, "dashboardRegistry")
			if err != nil || len(d.registry) != 2 || len(persisted) != 2 || current.URL != original.URL || current.Local || current.Credentials != original.Credentials || persisted[original.ID].URL != original.URL || len(n.accounts) != 4 {
				t.Fatal("failed alias adoption changed the registration or orphaned its role bundle")
			}
		})
	}
}
