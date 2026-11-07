package canary

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestForceNodeRemovalCancelsUnstartedBlockedDeployment(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/validate") {
			w.Header().Set("X-Canary-Node-Id", node.ID)
			errorReply(w, fail(409, "private fixture rejects initial preflight"))
			return
		}
		node.ServeHTTP(w, r)
	}))
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	group := groupSyncFixture(t, d, owner, []string{reg.ID})
	row := fleetCreateFixture(t, d, owner, []string{reg.ID}, nil)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	plan, err := d.openFleet(row)
	if err != nil || row.State != "blocked" || plan.Prepared || len(row.Placements) != 0 {
		t.Fatal("fixture did not reproduce the blocked, never-started operation")
	}
	forceNodeFixture(t, d, owner, reg)
	pumpNodeRemoval(t, d, reg.ID, nil)
	if d.fleet[row.ID].State != "removed" {
		t.Fatal("unstarted last-runner definition was not retired")
	}
	if _, exists, err := loadOne[dashboardNodeGroup](d.accounts.node.store, "dashboardNodeGroups", group.ID); err != nil || exists {
		t.Fatal("empty group survived removal", err)
	}
	node.mu.Lock()
	defer node.mu.Unlock()
	if len(node.heads) != 0 || len(node.accounts) != 1 {
		t.Fatal("cancellation created a monitor or left delegated access")
	}
}

func TestForceNodeRemovalRetargetsUnstartedSharedDeploymentWithoutWaitingForOtherNode(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	sa := httptest.NewServer(a)
	sb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/validate") {
			w.Header().Set("X-Canary-Node-Id", b.ID)
			errorReply(w, fail(409, "survivor's unrelated preflight remains blocked"))
			return
		}
		b.ServeHTTP(w, r)
	}))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	row := fleetCreateFixture(t, d, owner, []string{ra.ID, rb.ID}, nil)
	d.finishFleet(context.Background(), row.ID)
	if d.fleet[row.ID].State != "blocked" {
		t.Fatal("fixture did not block before any assignment")
	}
	forceNodeFixture(t, d, owner, ra)
	pumpNodeRemoval(t, d, ra.ID, nil)
	row = d.fleet[row.ID]
	if !slices.Equal(row.RegistrationIDs, []string{rb.ID}) || row.State != "blocked" {
		t.Fatal("remaining desired monitor was lost or incorrectly reported applied")
	}
	d.mu.Lock()
	locked := d.fleetRemovalLocked(row)
	d.mu.Unlock()
	if locked {
		t.Fatal("surviving deployment remains locked after unrelated node removal")
	}
}

func TestForceNodeRemovalSupersedesBlockedUpdateAndKeepsSurvivor(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var reject atomic.Bool
	sa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reject.Load() && strings.HasSuffix(r.URL.Path, "/validate") {
			w.Header().Set("X-Canary-Node-Id", a.ID)
			errorReply(w, fail(409, "the departing node rejects an unsent update"))
			return
		}
		a.ServeHTTP(w, r)
	}))
	sb := httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	row := fleetCreateFixture(t, d, owner, []string{ra.ID, rb.ID}, nil)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	physical := ""
	for _, p := range row.Placements {
		if p.RegistrationID == rb.ID {
			physical = p.MonitorID
		}
	}
	plan, _ := d.openFleet(row)
	plan.Monitor.Name = "Queued change survives node removal"
	w := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{
		"revision": row.Revision, "registrationIds": row.RegistrationIDs, "monitor": plan.Monitor,
	})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	reject.Store(true)
	d.finishFleet(context.Background(), row.ID)
	if d.fleet[row.ID].State != "blocked" {
		t.Fatal("update was not blocked before it wrote")
	}
	forceNodeFixture(t, d, owner, ra)
	// Deletion has different validation semantics; allow the legitimate cleanup.
	reject.Store(false)
	pumpNodeRemoval(t, d, ra.ID, nil)
	if !slices.Equal(d.fleet[row.ID].RegistrationIDs, []string{rb.ID}) || d.fleet[row.ID].State != "applied" {
		t.Fatal("survivor deployment was lost")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if current := b.current(physical); current.Deleted || current.Name != plan.Monitor.Name {
		t.Fatal("survivor's physical monitor or queued changes were lost")
	}
}

func TestForceNodeRemovalResumesExactSavedCommandUnderCurrentAuthority(t *testing.T) {
	for _, outcome := range []string{"rejected", "unknown", "unauthorized"} {
		t.Run(outcome, func(t *testing.T) {
			d, owner, _ := dashboardRegistryFixture(t)
			node := dashboardRemoteFixture(t, "node-a")
			var intercept atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if intercept.Load() && r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/fleet-placements/") {
					if outcome != "rejected" {
						node.ServeHTTP(httptest.NewRecorder(), r)
					}
					w.Header().Set("X-Canary-Node-Id", node.ID)
					status := 503
					if outcome == "rejected" {
						status = 409
					}
					errorReply(w, fail(status, "private fixture withholds acknowledgement"))
					return
				}
				node.ServeHTTP(w, r)
			}))
			defer server.Close()
			reg := enrollDashboardNode(t, d, owner, server.URL)
			row := fleetCreateFixture(t, d, owner, []string{reg.ID}, nil)
			intercept.Store(true)
			d.finishFleet(context.Background(), row.ID)
			row = d.fleet[row.ID]
			plan, err := d.openFleet(row)
			if err != nil || !plan.Prepared || fleetWritesConfirmed(row, plan) {
				t.Fatal("fixture did not retain the exact unresolved command")
			}
			before, err := fleetCommandBytes(plan, row.Placements[0])
			if err != nil {
				t.Fatal(err)
			}
			if outcome == "unauthorized" {
				revokeFleetFixtureAccount(d, row.ActorID)
				owner = dashboardPasswordLogin(t, d.accounts, "owner", dashboardPasswordFixture)
				d.finishFleet(context.Background(), row.ID)
				if d.fleet[row.ID].State != "unauthorized" {
					t.Fatal("fixture did not stop its old authorization")
				}
			}
			forceNodeFixture(t, d, owner, reg)
			_, _ = d.finishEnrollment(context.Background(), reg.ID)
			resumed := d.fleet[row.ID]
			retained, err := d.openFleet(resumed)
			if err != nil || resumed.State != "pending" || !retained.Prepared {
				t.Fatal("removal did not resume the pending command")
			}
			after, err := fleetCommandBytes(retained, resumed.Placements[0])
			if err != nil || string(after) != string(before) || !equalJSON(plan.Commands, retained.Commands) {
				t.Fatal("removal replaced an unresolved command")
			}
			node.mu.Lock()
			accounts := len(node.accounts)
			node.mu.Unlock()
			if accounts != 4 {
				t.Fatal("access revoked before resolving the saved command")
			}
			intercept.Store(false)
			pumpNodeRemoval(t, d, reg.ID, nil)
			node.mu.Lock()
			defer node.mu.Unlock()
			if len(node.heads) != 1 || !node.current(row.Placements[0].MonitorID).Deleted || len(node.accounts) != 1 {
				t.Fatal("cleanup duplicated a monitor or failed retirement/revocation")
			}
		})
	}
}

func TestForceNodeRemovalPreservesAlreadyQueuedMonitorDeletion(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	sa, sb := httptest.NewServer(a), httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	row := fleetCreateFixture(t, d, owner, []string{ra.ID, rb.ID}, nil)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	w := dashboardHTTP(d, "DELETE", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	forceNodeFixture(t, d, owner, ra)
	pumpNodeRemoval(t, d, ra.ID, nil)
	if d.fleet[row.ID].State != "removed" {
		t.Fatal("removal reversed an already queued monitor deletion")
	}
	for _, node := range []*Node{a, b} {
		node.mu.Lock()
		for _, versions := range node.heads {
			if !versions[0].Monitor.Deleted {
				t.Error("an intentionally retired monitor was revived")
			}
		}
		node.mu.Unlock()
	}
}

func TestForceNodeRemovalPreservesPendingAdoptionOnSurvivingSource(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	in := placementFixtureInput()
	in.NodeIDs, in.Enabled = []string{"node-b"}, false
	original, err := b.writeMonitor(in, nil, "surviving-native-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	sa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/validate") {
			w.Header().Set("X-Canary-Node-Id", a.ID)
			errorReply(w, fail(409, "departing node cannot accept the initial adoption"))
			return
		}
		a.ServeHTTP(w, r)
	}))
	sb := httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	row := fleetCreateFixture(t, d, owner, []string{ra.ID, rb.ID}, &fleetSourceReference{rb.ID, original.ID, original.Revision})
	d.finishFleet(context.Background(), row.ID)
	if d.fleet[row.ID].State != "blocked" {
		t.Fatal("fixture did not block the unstarted adoption")
	}
	forceNodeFixture(t, d, owner, ra)
	pumpNodeRemoval(t, d, ra.ID, nil)
	row = d.fleet[row.ID]
	if row.State != "applied" || len(row.Placements) != 1 || row.Placements[0].MonitorID != original.ID {
		t.Fatal("survivor's original monitor was not adopted in place", row.State, row.Reason)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.heads) != 1 || b.current(original.ID).Deleted {
		t.Fatal("node removal duplicated or deleted the surviving native monitor")
	}
}
