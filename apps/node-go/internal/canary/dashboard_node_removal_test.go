package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
)

func forceNodeFixture(t *testing.T, d *Dashboard, token string, node RegisteredNode) {
	t.Helper()
	w := dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+node.ID, token, "", map[string]any{"force": true, "confirmation": node.Label})
	if w.Code != 202 {
		t.Fatalf("queue removal: %d %s", w.Code, w.Body.String())
	}
}

func pumpNodeRemoval(t *testing.T, d *Dashboard, id string, replicate func()) {
	t.Helper()
	for range 12 {
		if _, ok := d.registry[id]; !ok {
			return
		}
		_, _ = d.finishEnrollment(context.Background(), id)
		for key := range d.fleet {
			d.finishFleet(context.Background(), key)
		}
		if replicate != nil {
			replicate()
		}
	}
	if row, ok := d.registry[id]; ok {
		t.Fatalf("removal did not finish: %s", row.Error)
	}
}

func TestForceNodeRemovalRequiresExactCurrentNameAndAdministrator(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(node)
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	for _, body := range []any{
		map[string]any{"force": true},
		map[string]any{"force": true, "confirmation": reg.Label + " "},
		map[string]any{"force": true, "confirmation": "not the node"},
		map[string]any{"force": "true", "confirmation": reg.Label},
		map[string]any{"force": true, "confirmation": reg.Label, "unknown": true},
		map[string]any{"confirmation": reg.Label},
	} {
		before := d.registry[reg.ID]
		w := dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+reg.ID, owner, "", body)
		if w.Code < 400 || !equalJSON(before, d.registry[reg.ID]) {
			t.Fatalf("invalid confirmation mutated registration: %d", w.Code)
		}
	}
	w := dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+reg.ID, "", "", map[string]any{"force": true, "confirmation": reg.Label})
	if w.Code != 401 {
		t.Fatal("unauthenticated force removal", w.Code)
	}
	forceNodeFixture(t, d, owner, reg)
	accepted := d.registry[reg.ID]
	if !accepted.ForceRemoving || d.publicNode(accepted, "admin").State != "removing" || len(node.accounts) != 4 {
		t.Fatal("acceptance did not retain credentials and defer remote revocation")
	}
	forceNodeFixture(t, d, owner, reg)
	if !equalJSON(accepted, d.registry[reg.ID]) {
		t.Fatal("repeated acceptance created another cleanup")
	}
	if w := dashboardHTTP(d, "GET", "/api/overview", owner, reg.ID, nil); w.Code != 409 {
		t.Fatal("ordinary reads continued during cleanup", w.Code)
	}
	if w := dashboardHTTP(d, "PATCH", "/api/dashboard/nodes/"+reg.ID, owner, "", map[string]any{"label": "Changed"}); w.Code != 409 {
		t.Fatal("node changed during cleanup", w.Code)
	}
}

func TestForceNodeRemovalKeepsIndependentSurvivorsAndCleansGroups(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	sa, sb := httptest.NewServer(a), httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	group := groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	w := dashboardHTTP(d, "POST", "/api/dashboard/node-groups", owner, "", map[string]any{"name": "Only retiring node", "registrationIds": []string{ra.ID}})
	var empty dashboardNodeGroup
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &empty) != nil {
		t.Fatal(w.Code)
	}
	row := fleetCreateFixture(t, d, owner, []string{ra.ID, rb.ID}, nil)
	d.finishFleet(context.Background(), row.ID)
	if d.fleet[row.ID].State != "applied" {
		t.Fatal(d.fleet[row.ID].Reason)
	}
	var survivor string
	for _, p := range d.fleet[row.ID].Placements {
		if p.RegistrationID == rb.ID {
			survivor = p.MonitorID
		}
	}
	if w := dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+ra.ID, owner, "", nil); w.Code != 409 {
		t.Fatal("legacy guard changed", w.Code)
	}
	forceNodeFixture(t, d, owner, ra)
	pumpNodeRemoval(t, d, ra.ID, nil)
	if current := b.current(survivor); current.Deleted || !slices.Equal(current.NodeIDs, []string{"node-b"}) {
		t.Fatal("surviving monitor changed identity or stopped")
	}
	for _, versions := range a.heads {
		if !versions[0].Monitor.Deleted {
			t.Fatal("retired node kept a managed check")
		}
	}
	remaining, ok, err := loadOne[dashboardNodeGroup](d.accounts.node.store, "dashboardNodeGroups", group.ID)
	if err != nil || !ok || !slices.Equal(remaining.RegistrationIDs, []string{rb.ID}) {
		t.Fatal("shared group did not keep its survivor")
	}
	if _, ok, err := loadOne[dashboardNodeGroup](d.accounts.node.store, "dashboardNodeGroups", empty.ID); err != nil || ok {
		t.Fatal("empty group was not retired")
	}
	if len(a.accounts) != 1 || len(b.accounts) != 4 {
		t.Fatal("revoked the wrong node's access")
	}
}

func TestForceNodeRemovalRetiresLastMonitorAndSurvivesRestart(t *testing.T) {
	d, owner, options := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(node)
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	row := fleetCreateFixture(t, d, owner, []string{reg.ID}, nil)
	d.finishFleet(context.Background(), row.ID)
	forceNodeFixture(t, d, owner, reg)
	d.Close()
	_ = d.accounts.Close()
	accounts, err := NewDashboardAccounts(options)
	if err != nil {
		t.Fatal(err)
	}
	defer accounts.Close()
	restarted, err := NewDashboard(DashboardOptions{Accounts: accounts, Key: dashboardKeyFixture})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	pumpNodeRemoval(t, restarted, reg.ID, nil)
	if restarted.fleet[row.ID].State != "removed" {
		t.Fatal("zero-runner monitor was not retired")
	}
	for _, versions := range node.heads {
		if !versions[0].Monitor.Deleted {
			t.Fatal("retired monitor was revived")
		}
	}
}

func TestForceNodeRemovalHandsSharedAnchorToSurvivingPeer(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := causalCapacityNode(t, "a", ":memory:"), causalCapacityNode(t, "b", ":memory:")
	a.peerSeen["b"], b.peerSeen["a"] = 1, 1
	in := input([]string{"a", "b"}, "http://127.0.0.1:9", "")
	in.Enabled = false
	m, err := a.writeMonitor(in, nil, "keep-shared-history", false)
	if err != nil {
		t.Fatal(err)
	}
	replicate := func() {
		for _, pair := range [][2]*Node{{a, b}, {b, a}} {
			for _, page := range causalCapacityPages(t, pair[0]) {
				if err := pair[1].applySnapshot(page); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	replicate()
	sa, sb := httptest.NewServer(a), httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	row := fleetCreateFixture(t, d, owner, []string{ra.ID, rb.ID}, &fleetSourceReference{ra.ID, m.ID, m.Revision})
	d.finishFleet(context.Background(), row.ID)
	replicate()
	d.finishFleet(context.Background(), row.ID)
	if d.fleet[row.ID].State != "applied" {
		t.Fatal(d.fleet[row.ID].Reason)
	}
	anchor := d.fleet[row.ID].Placements[0].RegistrationID
	reg := ra
	remainingID, remainingNode := rb.ID, "b"
	if anchor == rb.ID {
		reg, remainingID, remainingNode = rb, ra.ID, "a"
	}
	forceNodeFixture(t, d, owner, reg)
	pumpNodeRemoval(t, d, reg.ID, replicate)
	for _, n := range []*Node{a, b} {
		current := n.current(m.ID)
		if current.Deleted || !slices.Equal(current.NodeIDs, []string{remainingNode}) {
			t.Fatal("shared monitor was deleted or still has the removed runner")
		}
	}
	row = d.fleet[row.ID]
	for _, p := range row.Placements {
		if p.State != "removed" && (p.RegistrationID != remainingID || p.MonitorID != m.ID) {
			t.Fatal("management anchor or physical monitor identity was lost")
		}
	}
	// Future edits must work after the old registration and credentials are gone.
	plan, _ := d.openFleet(row)
	plan.Monitor.Name = "Still managed"
	w := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision, "registrationIds": []string{remainingID}, "monitor": plan.Monitor})
	if w.Code != 202 {
		t.Fatalf("survivor cannot be edited: %d %s", w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), row.ID)
	replicate()
	d.finishFleet(context.Background(), row.ID)
	if d.fleet[row.ID].State != "applied" {
		t.Fatal(d.fleet[row.ID].Reason)
	}
}

func TestForceNodeRemovalWaitsForOfflineNodeWithoutRevokingEarly(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	var offline atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			http.Error(w, "offline fixture", 503)
			return
		}
		node.ServeHTTP(w, r)
	}))
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	row := fleetCreateFixture(t, d, owner, []string{reg.ID}, nil)
	d.finishFleet(context.Background(), row.ID)
	forceNodeFixture(t, d, owner, reg)
	offline.Store(true)
	_, _ = d.finishEnrollment(context.Background(), reg.ID)
	d.finishFleet(context.Background(), row.ID)
	_, _ = d.finishEnrollment(context.Background(), reg.ID)
	if !d.registry[reg.ID].ForceRemoving || len(node.accounts) != 4 {
		t.Fatal("offline cleanup forgot its credentials or revoked before retirement")
	}
	offline.Store(false)
	pumpNodeRemoval(t, d, reg.ID, nil)
}

func TestForceNodeRemovalJournalFailureDoesNotPublishAcceptance(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(node)
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	before := d.registry[reg.ID]
	if _, err := d.accounts.node.store.db.Exec(`CREATE TRIGGER removal_journal_failure BEFORE INSERT ON records WHEN NEW.category='dashboardRegistry' BEGIN SELECT RAISE(ABORT,'private fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	w := dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+reg.ID, owner, "", map[string]any{"force": true, "confirmation": reg.Label})
	if w.Code < 400 || !equalJSON(before, d.registry[reg.ID]) || len(node.accounts) != 4 {
		t.Fatal("failed acceptance changed state or sent revocation")
	}
}

func TestForceNodeRemovalCleanupTransactionIsAtomic(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(node)
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	group := groupSyncFixture(t, d, owner, []string{reg.ID})
	row := fleetCreateFixture(t, d, owner, []string{reg.ID}, nil)
	d.finishFleet(context.Background(), row.ID)
	before := copyFleet(d.fleet[row.ID])
	forceNodeFixture(t, d, owner, reg)
	if _, err := d.accounts.node.store.db.Exec(`CREATE TRIGGER cleanup_journal_failure BEFORE INSERT ON records WHEN NEW.category='dashboardFleet' BEGIN SELECT RAISE(ABORT,'private fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, _ = d.finishEnrollment(context.Background(), reg.ID)
	stored, ok, err := loadOne[dashboardNodeGroup](d.accounts.node.store, "dashboardNodeGroups", group.ID)
	if err != nil || !ok || !slices.Equal(stored.RegistrationIDs, []string{reg.ID}) || !equalJSON(before, d.fleet[row.ID]) {
		t.Fatal("failed cleanup published partial membership changes")
	}
	if node.current(before.Placements[0].MonitorID).Deleted || len(node.accounts) != 4 {
		t.Fatal("cleanup sent remote writes before its transaction committed")
	}
}

func TestForceNodeRemovalRevokedAuthorityNeedsExplicitRetry(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(node)
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	forceNodeFixture(t, d, owner, reg)
	secret, _ := d.openSecrets(d.registry[reg.ID])
	revokeFleetFixtureAccount(d, secret.Removal.ActorID)
	if _, err := d.finishEnrollment(context.Background(), reg.ID); err == nil || !d.registry[reg.ID].ForceRemoving || len(node.accounts) != 4 {
		t.Fatal("revoked authority continued cleanup")
	}
	newOwner := dashboardPasswordLogin(t, d.accounts, "owner", dashboardPasswordFixture)
	w := dashboardHTTP(d, "POST", "/api/dashboard/nodes/"+reg.ID+"/retry", newOwner, "", nil)
	if w.Code != 200 && w.Code != 202 {
		t.Fatalf("renew cleanup: %d %s", w.Code, w.Body.String())
	}
	pumpNodeRemoval(t, d, reg.ID, nil)
}

func TestForceNodeRemovalPreservesCopiedNotificationSettings(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	sa, sb := httptest.NewServer(a), httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	channel := groupChannelFixture(t, a)
	in := placementFixtureInput()
	in.NotificationIDs = []string{channel.ID}
	w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{"requestId": placementFixtureID, "registrationIds": []string{ra.ID, rb.ID}, "monitor": in, "syncNotifications": true, "notificationSourceRegistrationId": ra.ID})
	if w.Code != 202 {
		t.Fatalf("create copied settings: %d %s", w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), placementFixtureID)
	row := d.fleet[placementFixtureID]
	if row.State != "applied" {
		t.Fatal(row.Reason)
	}
	monitorID := ""
	for _, p := range row.Placements {
		if p.RegistrationID == rb.ID {
			monitorID = p.MonitorID
		}
	}
	before := slices.Clone(b.current(monitorID).NotificationIDs)
	forceNodeFixture(t, d, owner, ra)
	pumpNodeRemoval(t, d, ra.ID, nil)
	if !slices.Equal(before, b.current(monitorID).NotificationIDs) {
		t.Fatal("source removal changed survivor delivery settings")
	}
	plan, err := d.openFleet(d.fleet[placementFixtureID])
	if err != nil || plan.SyncNotifications || plan.NotificationSourceRegistrationID != "" {
		t.Fatal("monitor still follows the retired settings source")
	}
	public, err := d.publicFleet(d.fleet[placementFixtureID], "admin")
	encoded, _ := json.Marshal(public)
	if err != nil || len(encoded) == 0 {
		t.Fatal("surviving deployment unreadable")
	}
}

func TestForceNodeRemovalOfflineJobDoesNotBlockIndependentRemoval(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var offline atomic.Bool
	sa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			http.Error(w, "offline fixture", 503)
			return
		}
		a.ServeHTTP(w, r)
	}))
	sb := httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	row := fleetCreateFixture(t, d, owner, []string{ra.ID}, nil)
	d.finishFleet(context.Background(), row.ID)
	forceNodeFixture(t, d, owner, ra)
	offline.Store(true)
	_, _ = d.finishEnrollment(context.Background(), ra.ID)
	d.finishFleet(context.Background(), row.ID)
	forceNodeFixture(t, d, owner, rb)
	pumpNodeRemoval(t, d, rb.ID, nil)
	if !d.registry[ra.ID].ForceRemoving || len(a.accounts) != 4 || len(b.accounts) != 1 {
		t.Fatal("an unrelated offline cleanup blocked or changed the second removal")
	}
}
