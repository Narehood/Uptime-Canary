package canary

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type fleetAuthorityBody struct {
	reader io.Reader
	once   sync.Once
	change func()
}

func (body *fleetAuthorityBody) Read(b []byte) (int, error) {
	body.once.Do(body.change)
	return body.reader.Read(b)
}
func (*fleetAuthorityBody) Close() error { return nil }

func revokeFleetFixtureAccount(d *Dashboard, id string) {
	n := d.accounts.node
	n.mu.Lock()
	defer n.mu.Unlock()
	a := n.accounts[id]
	a.AuthVersion++
	n.accounts[id] = a
}

func TestDashboardFleetReauthorizesAfterBodyAndBeforeWorker(t *testing.T) {
	for _, path := range []string{"/api/dashboard/fleet/monitors", "/api/dashboard/node-groups"} {
		t.Run(path, func(t *testing.T) {
			d, owner, _ := dashboardRegistryFixture(t)
			node := dashboardRemoteFixture(t, "node-a")
			server := httptest.NewServer(node)
			defer server.Close()
			reg := enrollDashboardNode(t, d, owner, server.URL)
			actor, err := d.accounts.Authenticate(func() *http.Request {
				r := httptest.NewRequest("GET", "/api/auth/me", nil)
				r.Header.Set("Authorization", "Bearer "+owner)
				return r
			}())
			if err != nil {
				t.Fatal(err)
			}
			body := map[string]any{"name": "Revoked selection", "registrationIds": []string{reg.ID}}
			if strings.Contains(path, "/fleet/") {
				body = map[string]any{"requestId": placementFixtureID, "registrationIds": []string{reg.ID}, "monitor": placementFixtureInput()}
			}
			b, _ := json.Marshal(body)
			r := httptest.NewRequest("POST", path, nil)
			r.Body = &fleetAuthorityBody{reader: strings.NewReader(string(b)), change: func() { revokeFleetFixtureAccount(d, actor.ID) }}
			r.Header.Set("Authorization", "Bearer "+owner)
			w := httptest.NewRecorder()
			d.ServeHTTP(w, r)
			groups, _ := loadAll[dashboardNodeGroup](d.accounts.node.store, "dashboardNodeGroups")
			if w.Code != 401 || len(d.fleet) != 0 || len(groups) != 0 || len(node.heads) != 0 {
				t.Fatalf("revoked body committed a change: %d", w.Code)
			}
		})
	}
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(node)
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	row := fleetCreateFixture(t, d, owner, []string{reg.ID}, nil)
	revokeFleetFixtureAccount(d, row.ActorID)
	d.finishFleet(context.Background(), row.ID)
	if d.fleet[row.ID].State != "unauthorized" || len(node.heads) != 0 {
		t.Fatal("worker used revoked saved authority")
	}
	newOwner := dashboardPasswordLogin(t, d.accounts, "owner", dashboardPasswordFixture)
	row = d.fleet[row.ID]
	if w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors/"+row.ID+"/retry", newOwner, "", map[string]string{"revision": row.Revision}); w.Code != 202 {
		t.Fatal(w.Code)
	}
	d.finishFleet(context.Background(), row.ID)
	if d.fleet[row.ID].State != "applied" || len(node.heads) != 1 {
		t.Fatal("explicit current-authority retry did not finish")
	}
}

func TestDashboardFleetJournalFailureSendsNoRemoteWrite(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(node)
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	row := fleetCreateFixture(t, d, owner, []string{reg.ID}, nil)
	if _, err := d.accounts.node.store.db.Exec(`CREATE TRIGGER fleet_journal_failure BEFORE INSERT ON records WHEN NEW.category='dashboardFleet' BEGIN SELECT RAISE(ABORT,'private fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	d.finishFleet(context.Background(), row.ID)
	if len(node.heads) != 0 {
		t.Fatal("remote mutation preceded the prepared journal commit")
	}
	stored, ok, err := loadOne[dashboardFleetRecord](d.accounts.node.store, "dashboardFleet", row.ID)
	if err != nil || !ok || stored.Revision != row.Revision || !equalJSON(stored, d.fleet[row.ID]) {
		t.Fatal("failed journal commit published new state")
	}
}

func TestDashboardFleetFailedConfirmationSaveKeepsPublishedJournal(t *testing.T) {
	d, owner, options := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(node)
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	row := fleetCreateFixture(t, d, owner, []string{reg.ID}, nil)
	if _, err := d.accounts.node.store.db.Exec(`CREATE TRIGGER fleet_confirmation_failure BEFORE INSERT ON records
 WHEN NEW.category='dashboardFleet' AND json_extract(NEW.payload,'$.placements[0].state')='applied'
 BEGIN SELECT RAISE(ABORT,'private fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	d.finishFleet(context.Background(), row.ID)
	stored, ok, err := loadOne[dashboardFleetRecord](d.accounts.node.store, "dashboardFleet", row.ID)
	row = d.fleet[row.ID]
	if err != nil || !ok || row.State != "pending" || row.Placements[0].State != "syncing" || !equalJSON(stored, row) {
		t.Fatal("an uncommitted worker mutation leaked into the published journal")
	}
	if _, err = d.accounts.node.store.db.Exec(`DROP TRIGGER fleet_confirmation_failure`); err != nil {
		t.Fatal(err)
	}
	// auth.go's identityTransactionLocked faults the runtime on any persistence
	// error. Restore storage and restart before testing the original recovery claim.
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
	restarted.finishFleet(context.Background(), row.ID)
	if restarted.fleet[row.ID].State != "applied" || len(node.heads) != 1 {
		t.Fatal("the retained confirmation could not finish after journal recovery")
	}
}

func TestDashboardFleetHAReferenceMismatchPreflightWritesNothing(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := causalCapacityNode(t, "a", ":memory:"), causalCapacityNode(t, "b", ":memory:")
	sa, sb := httptest.NewServer(a), httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{"requestId": placementFixtureID, "registrationIds": []string{ra.ID, rb.ID}, "monitor": placementFixtureInput(), "references": map[string]any{rb.ID: map[string]any{"notificationIds": []string{"different-reference"}}}})
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	d.finishFleet(context.Background(), placementFixtureID)
	if d.fleet[placementFixtureID].State != "blocked" || len(a.heads) != 0 || len(b.heads) != 0 {
		t.Fatal("mismatched HA references were dropped or partially written")
	}
}

func TestDashboardFleetAdoptionTracksFormerRunners(t *testing.T) {
	for _, registerPeer := range []bool{false, true} {
		t.Run(map[bool]string{false: "unregistered runner refuses replacement", true: "former registered runner must confirm"}[registerPeer], func(t *testing.T) {
			d, owner, _ := dashboardRegistryFixture(t)
			a, b := causalCapacityNode(t, "a", ":memory:"), causalCapacityNode(t, "b", ":memory:")
			in := input([]string{"a", "b"}, "http://127.0.0.1:9", "")
			in.Enabled = false
			m, err := a.writeMonitor(in, nil, "former-runner", false)
			if err != nil {
				t.Fatal(err)
			}
			for _, page := range causalCapacityPages(t, a) {
				if err := b.applySnapshot(page); err != nil {
					t.Fatal(err)
				}
			}
			sa, sb := httptest.NewServer(a), httptest.NewServer(b)
			defer sa.Close()
			defer sb.Close()
			ra := enrollDashboardNode(t, d, owner, sa.URL)
			var rb RegisteredNode
			if registerPeer {
				rb = enrollDashboardNode(t, d, owner, sb.URL)
			}
			row := fleetCreateFixture(t, d, owner, []string{ra.ID}, &fleetSourceReference{ra.ID, m.ID, m.Revision})
			d.finishFleet(context.Background(), row.ID)
			row = d.fleet[row.ID]
			if !registerPeer {
				if row.State != "blocked" || a.current(m.ID).Revision != m.Revision {
					t.Fatal("unregistered runner silently lost its assignment")
				}
				return
			}
			if row.State != "pending" || !slices.Contains(row.Placements[0].RetiringRegistrationIDs, rb.ID) || !slices.Contains(b.current(m.ID).NodeIDs, "b") {
				t.Fatal("the adoption lost its former peer before confirmation")
			}
			for _, page := range causalCapacityPages(t, a) {
				if err := b.applySnapshot(page); err != nil {
					t.Fatal(err)
				}
			}
			d.finishFleet(context.Background(), row.ID)
			if d.fleet[row.ID].State != "applied" || slices.Contains(b.current(m.ID).NodeIDs, "b") {
				t.Fatal("former runner did not confirm the new assignment")
			}
		})
	}
}

func fleetCreateFixture(t *testing.T, d *Dashboard, owner string, ids []string, source *fleetSourceReference) dashboardFleetRecord {
	t.Helper()
	in := placementFixtureInput()
	in.NodeIDs = []string{"node-a"}
	in.Target = "http://127.0.0.1:9/private-fleet-target"
	request := map[string]any{"requestId": placementFixtureID, "registrationIds": ids, "monitor": in}
	if source != nil {
		request["source"] = source
	}
	w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", request)
	if w.Code != 202 {
		t.Fatalf("create fleet: %d %s", w.Code, w.Body.String())
	}
	return d.fleet[placementFixtureID]
}

func TestDashboardFleetIndependentNodesAndPublicRedaction(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	sa, sb := httptest.NewServer(a), httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	row := fleetCreateFixture(t, d, owner, []string{ra.ID, rb.ID}, nil)
	if strings.Contains(row.Private, "private-fleet-target") {
		t.Fatal("private config persisted without encryption")
	}
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "applied" || len(row.Placements) != 2 {
		t.Fatalf("independent placements not confirmed: %s %s", row.State, row.Reason)
	}
	if len(a.heads) != 1 || len(b.heads) != 1 {
		t.Fatal("independent deployment did not create exactly one monitor per node")
	}
	for _, node := range []*Node{a, b} {
		for _, heads := range node.heads {
			m := heads[0].Monitor
			if len(m.NodeIDs) != 1 || m.NodeIDs[0] != node.ID {
				t.Fatal("foreign registry IDs reached the node's peer catalogue")
			}
		}
	}
	if w := dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+row.Placements[0].RegistrationID, owner, "", nil); w.Code != 409 {
		t.Fatal("active placement anchor could be orphaned")
	}
	// A create whose response was lost recovers its logical ID instead of
	// producing another deployment or another private copy on either node.
	again := fleetCreateFixture(t, d, owner, []string{ra.ID, rb.ID}, nil)
	if again.ID != row.ID || len(d.fleet) != 1 {
		t.Fatal("duplicate create lost its request identity")
	}
	if w := dashboardHTTP(d, "POST", "/api/accounts", owner, "", map[string]string{"name": "fleet-viewer", "role": "viewer", "password": dashboardPasswordFixture}); w.Code != 201 {
		t.Fatal(w.Code)
	}
	viewer := dashboardPasswordLogin(t, d.accounts, "fleet-viewer", dashboardPasswordFixture)
	response := dashboardHTTP(d, "GET", "/api/dashboard/fleet/monitors", viewer, "", nil)
	if response.Code != 200 || strings.Contains(response.Body.String(), row.Private) || strings.Contains(response.Body.String(), "actorVersion") {
		t.Fatal("public fleet leaked its journal")
	}
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		path := "/api/dashboard/fleet/monitors"
		if method != "POST" {
			path += "/" + row.ID
		}
		if w := dashboardHTTP(d, method, path, viewer, "", map[string]string{}); w.Code != 403 {
			t.Fatal("viewer can change fleet")
		}
	}
}

func TestDashboardFleetAdoptsExistingAndKeepsOnePeerPlacement(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := causalCapacityNode(t, "a", ":memory:"), causalCapacityNode(t, "b", ":memory:")
	a.peerSeen["b"], b.peerSeen["a"] = 1, 1
	in := input([]string{"a", "b"}, "http://127.0.0.1:9", "")
	in.Enabled = false
	m, err := a.writeMonitor(in, nil, "existing-shared-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range causalCapacityPages(t, a) {
		if err = b.applySnapshot(page); err != nil {
			t.Fatal(err)
		}
	}
	sa, sb := httptest.NewServer(a), httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	row := fleetCreateFixture(t, d, owner, []string{ra.ID, rb.ID}, &fleetSourceReference{ra.ID, m.ID, m.Revision})
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	// Accepted fleet behavior: an anchor acknowledgement cannot confirm a peer
	// that has not received the new causal revision. Preserve the original final
	// assertions, and explicitly replicate before expecting applied.
	if row.State != "pending" || row.Placements[0].State != "syncing" {
		t.Fatalf("an unsynced HA member was marked complete: %s", row.State)
	}
	for _, page := range causalCapacityPages(t, a) {
		if err = b.applySnapshot(page); err != nil {
			t.Fatal(err)
		}
	}
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "applied" || len(row.Placements) != 1 || row.Placements[0].MonitorID != m.ID || len(a.heads) != 1 {
		t.Fatalf("existing HA monitor was duplicated: %s %s", row.State, row.Reason)
	}
	p := row.Placements[0]
	if len(p.NodeIDs) != 2 || p.Sequence != 1 || p.Revision == m.Revision {
		t.Fatal("adoption did not preserve identity with a new acknowledged revision")
	}
}

func TestDashboardFleetLostWriteResponseRetriesSavedCommand(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	var lose, refuse atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/fleet-placements/") && refuse.Swap(false) {
			// Model a recoverable refusal at the owned proxy. The next attempt
			// really commits on the node and may lose its response below.
			// nodeCall authenticates this identity header before trusting status.
			w.Header().Set("X-Canary-Node-Id", node.ID)
			errorReply(w, fail(409, "private fixture recoverable refusal"))
			return
		}
		if r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/fleet-placements/") && lose.Load() {
			capture := httptest.NewRecorder()
			node.ServeHTTP(capture, r)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
			return
		}
		node.ServeHTTP(w, r)
	}))
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	row := fleetCreateFixture(t, d, owner, []string{reg.ID}, nil)
	lose.Store(true)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	// A lost HTTP answer does not synchronize this test with the node's
	// handler. Inspect its state under the same lock used by ServeHTTP.
	node.mu.Lock()
	monitorCount := len(node.heads)
	node.mu.Unlock()
	if row.State != "pending" || monitorCount != 1 {
		t.Fatal("lost answer was not retained as an unknown saved outcome")
	}
	if w := dashboardHTTP(d, "DELETE", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]string{"revision": row.Revision}); w.Code != 409 {
		t.Fatal("an unconfirmed write was forgotten instead of resolved")
	}
	plan, err := d.openFleet(row)
	if err != nil {
		t.Fatal(err)
	}
	p := row.Placements[0]
	node.mu.Lock()
	observedRevision := node.current(p.MonitorID).Revision
	node.mu.Unlock()
	w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors/"+row.ID+"/reconcile", owner, "", map[string]any{
		"revision": row.Revision, "observedRevisions": map[string]string{p.ID: observedRevision},
	})
	after, err := d.openFleet(d.fleet[row.ID])
	if w.Code != 409 || err != nil || !equalJSON(plan, after) {
		t.Fatal("review discarded an unknown saved command instead of retaining exact replay")
	}
	var revision string
	node.mu.Lock()
	for _, heads := range node.heads {
		revision = heads[0].Monitor.Revision
	}
	node.mu.Unlock()
	lose.Store(false)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	node.mu.Lock()
	monitorCount = len(node.heads)
	input := node.current(p.MonitorID).MonitorInput
	node.mu.Unlock()
	if row.State != "applied" || monitorCount != 1 || row.Placements[0].Revision != revision {
		t.Fatal("retry duplicated or replaced an already committed monitor")
	}
	input.Name = "Update whose acknowledgement is lost"
	w = dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{
		"revision": row.Revision, "monitor": input, "registrationIds": row.RegistrationIDs,
	})
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	refuse.Store(true)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	plan, err = d.openFleet(row)
	if err != nil || row.State != "blocked" || !plan.Rejected[p.ID] {
		t.Fatal("the recoverable refusal was not retained")
	}
	w = dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors/"+row.ID+"/retry", owner, "", map[string]string{"revision": row.Revision})
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	lose.Store(true)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	plan, err = d.openFleet(row)
	if err != nil || row.Placements[0].Sequence != 1 || !plan.Prepared {
		t.Fatal("the unknown update was not retained")
	}
	if plan.Rejected[p.ID] || plan.Unsent[p.ID] {
		t.Fatal("a previous refusal stayed authoritative after another attempt")
	}
	input.Name = "Later direct edit must also survive exact replay"
	node.mu.Lock()
	local, err := node.writeMonitor(input, node.heads[p.MonitorID], p.MonitorID, false)
	node.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	w = dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors/"+row.ID+"/reconcile", owner, "", map[string]any{
		"revision": row.Revision, "observedRevisions": map[string]string{p.ID: local.Revision},
	})
	after, err = d.openFleet(d.fleet[row.ID])
	if w.Code != 409 || err != nil || !equalJSON(plan, after) {
		t.Fatal("review discarded an unknown update after a later direct edit")
	}
	if err = d.loadFleet(); err != nil {
		t.Fatal(err)
	}
	lose.Store(false)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	node.mu.Lock()
	observedRevision = node.current(p.MonitorID).Revision
	node.mu.Unlock()
	if row.Placements[0].Sequence != 2 || observedRevision != local.Revision {
		t.Fatal("unknown update did not recover its original receipt without overwriting the direct edit")
	}
}

func TestDashboardFleetDeleteUnpreparedUpdateRetiresExistingMonitors(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var offline atomic.Bool
	sa := httptest.NewServer(a)
	sb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			http.Error(w, "private fixture unavailable", 503)
			return
		}
		b.ServeHTTP(w, r)
	}))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	row := fleetCreateFixture(t, d, owner, []string{ra.ID}, nil)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	plan, err := d.openFleet(row)
	if err != nil {
		t.Fatal(err)
	}
	w := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision, "registrationIds": []string{ra.ID, rb.ID}, "monitor": plan.Monitor})
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	row = d.fleet[row.ID]
	plan, _ = d.openFleet(row)
	if fleetCanCancel(row, plan) {
		t.Fatal("an unapplied update concealed its already deployed monitors")
	}
	offline.Store(true)
	w = dashboardHTTP(d, "DELETE", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]string{"revision": row.Revision})
	if w.Code != 202 {
		t.Fatal("existing deployment was discarded as a cancellable new plan")
	}
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "removed" || len(row.Placements) != 1 || a.current(row.Placements[0].MonitorID) != nil || len(b.heads) != 0 {
		t.Fatalf("retirement needed an untouched new target or orphaned the old monitor: %s %s", row.State, row.Reason)
	}
}

func TestDashboardFleetHAOfflineRemovalStaysVisibleUntilPeerConfirms(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := causalCapacityNode(t, "a", ":memory:"), causalCapacityNode(t, "b", ":memory:")
	in := input([]string{"a", "b"}, "http://127.0.0.1:9", "")
	in.Enabled = false
	m, err := a.writeMonitor(in, nil, "remove-from-peer", false)
	if err != nil {
		t.Fatal(err)
	}
	syncPeer := func() {
		for _, page := range causalCapacityPages(t, a) {
			if err := b.applySnapshot(page); err != nil {
				t.Fatal(err)
			}
		}
	}
	syncPeer()
	var offline atomic.Bool
	sa := httptest.NewServer(a)
	sb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			http.Error(w, "private fixture unavailable", 503)
			return
		}
		b.ServeHTTP(w, r)
	}))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	row := fleetCreateFixture(t, d, owner, []string{ra.ID, rb.ID}, &fleetSourceReference{ra.ID, m.ID, m.Revision})
	d.finishFleet(context.Background(), row.ID)
	syncPeer()
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "applied" {
		t.Fatal(row.Reason)
	}
	plan, _ := d.openFleet(row)
	offline.Store(true)
	w := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision, "registrationIds": []string{ra.ID}, "monitor": plan.Monitor})
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "pending" || row.Placements[0].State != "syncing" || !slices.Contains(row.Placements[0].RetiringRegistrationIDs, rb.ID) || slices.Contains(a.current(m.ID).NodeIDs, "b") || !slices.Contains(b.current(m.ID).NodeIDs, "b") {
		t.Fatalf("offline removal was lost or blocked the anchor's stop command: %s %s", row.State, row.Reason)
	}
	if w := dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+rb.ID, owner, "", nil); w.Code != 409 {
		t.Fatal("a retiring peer registration could be orphaned")
	}
	// A later full deletion must retain the already retiring peer too. Its old
	// monitor remains visible there until causal replication resumes.
	w = dashboardHTTP(d, "DELETE", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]string{"revision": row.Revision})
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "pending" || a.current(m.ID) != nil || b.current(m.ID) == nil || !slices.Contains(row.Placements[0].RetiringRegistrationIDs, rb.ID) {
		t.Fatal("offline peer deletion was falsely complete")
	}
	if repeated := dashboardHTTP(d, "DELETE", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]string{"revision": row.Revision}); repeated.Code != 202 || !equalJSON(row, d.fleet[row.ID]) {
		t.Fatal("repeated removal replaced an acknowledged deletion while a peer was offline")
	}
	syncPeer()
	offline.Store(false)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "removed" || b.current(m.ID) != nil || len(row.Placements[0].RetiringRegistrationIDs) != 0 || row.Placements[0].Sequence != 3 {
		t.Fatal("peer confirmation did not finish the original command sequence")
	}
}

func TestDashboardFleetSourceAdoptionIsNotRepeatedAfterRetirement(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	sa, sb := httptest.NewServer(a), httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	m, err := a.writeMonitor(placementFixtureInput(), nil, "one-time-source", false)
	if err != nil {
		t.Fatal(err)
	}
	row := fleetCreateFixture(t, d, owner, []string{ra.ID, rb.ID}, &fleetSourceReference{ra.ID, m.ID, m.Revision})
	d.finishFleet(context.Background(), row.ID)
	for _, ids := range [][]string{{rb.ID}, {ra.ID, rb.ID}} {
		row = d.fleet[row.ID]
		plan, _ := d.openFleet(row)
		w := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision, "registrationIds": ids, "monitor": plan.Monitor})
		if w.Code != 202 {
			t.Fatal(w.Code)
		}
		d.finishFleet(context.Background(), row.ID)
		if d.fleet[row.ID].State != "applied" {
			t.Fatal(d.fleet[row.ID].Reason)
		}
	}
	row = d.fleet[row.ID]
	if !row.SourceCompleted || a.current(m.ID) != nil || !a.heads[m.ID][0].Monitor.Deleted {
		t.Fatal("reselection resurrected the retired original monitor")
	}
	for _, p := range row.Placements {
		if p.RegistrationID == ra.ID && (p.MonitorID == m.ID || a.current(p.MonitorID) == nil) {
			t.Fatal("reselection failed to use a fresh physical identity")
		}
	}
}

func TestDashboardFleetPreflightDoesNotDropLocalReferences(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(node)
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	in := placementFixtureInput()
	in.NotificationIDs = []string{"not-on-this-node"}
	w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{"requestId": placementFixtureID, "monitor": in, "registrationIds": []string{reg.ID}})
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	d.finishFleet(context.Background(), placementFixtureID)
	if len(node.heads) != 0 || d.fleet[placementFixtureID].State != "blocked" {
		t.Fatal("missing channel was silently dropped or partially deployed")
	}
}

func TestDashboardNodeGroupsRolesAndRevisionChecks(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(node)
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	w := dashboardHTTP(d, "POST", "/api/dashboard/node-groups", owner, "", map[string]any{"name": "All locations", "registrationIds": []string{reg.ID}})
	var group dashboardNodeGroup
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &group) != nil {
		t.Fatalf("group create: %d", w.Code)
	}
	again := dashboardHTTP(d, "POST", "/api/dashboard/node-groups", owner, "", map[string]any{"name": "All locations", "registrationIds": []string{reg.ID}})
	var recovered dashboardNodeGroup
	if again.Code != 200 || json.Unmarshal(again.Body.Bytes(), &recovered) != nil || recovered.ID != group.ID || recovered.Revision != group.Revision {
		t.Fatal("a retried group create duplicated or revised its saved selection")
	}
	if w = dashboardHTTP(d, "PUT", "/api/dashboard/node-groups/"+group.ID, owner, "", map[string]any{"name": "Renamed", "registrationIds": []string{reg.ID}, "revision": "stale"}); w.Code != 409 {
		t.Fatal("stale group save accepted")
	}
	if w = dashboardHTTP(d, "PUT", "/api/dashboard/node-groups/"+group.ID, owner, "", map[string]any{"name": "Renamed", "registrationIds": []string{reg.ID}, "revision": group.Revision}); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if len(node.groups) != 0 {
		t.Fatal("dashboard placement group changed a node's outage dependencies")
	}
	for _, raw := range []string{`{"name":null}`, `{"Name":"alias"}`, `{"name":"a","name":"b"}`, `{"registrationIds":["missing"],"name":"bad"}`} {
		r := httptest.NewRequest("POST", "/api/dashboard/node-groups", strings.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+owner)
		out := httptest.NewRecorder()
		d.ServeHTTP(out, r)
		if out.Code != 400 {
			t.Fatal("invalid group accepted")
		}
	}
}

func TestDashboardFleetReconciliationUsesOnlyReviewedCurrentRevision(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(node)
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	row := fleetCreateFixture(t, d, owner, []string{reg.ID}, nil)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	p := row.Placements[0]
	input := node.current(p.MonitorID).MonitorInput
	input.Name = "Direct edit to preserve until reviewed"
	local, err := node.writeMonitor(input, node.heads[p.MonitorID], p.MonitorID, false)
	if err != nil {
		t.Fatal(err)
	}
	request := func(revision string) {
		row = d.fleet[row.ID]
		w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors/"+row.ID+"/reconcile", owner, "", map[string]any{"revision": row.Revision, "observedRevisions": map[string]string{p.ID: revision}})
		if w.Code != 202 {
			t.Fatalf("review: %d %s", w.Code, w.Body.String())
		}
	}
	request(p.Revision)
	d.finishFleet(context.Background(), row.ID)
	if d.fleet[row.ID].State != "blocked" || node.current(p.MonitorID).Revision != local.Revision {
		t.Fatal("reconciliation overwrote a revision the user had not reviewed")
	}
	request(local.Revision)
	input.Name = "Another edit after the review"
	later, err := node.writeMonitor(input, node.heads[p.MonitorID], p.MonitorID, false)
	if err != nil {
		t.Fatal(err)
	}
	d.finishFleet(context.Background(), row.ID)
	if d.fleet[row.ID].State != "blocked" || node.current(p.MonitorID).Revision != later.Revision {
		t.Fatal("a node edit racing the reviewed snapshot was overwritten")
	}
	request(later.Revision)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "applied" || row.Placements[0].Sequence != 2 || node.current(p.MonitorID).Name == input.Name {
		t.Fatalf("explicit current review did not restore the saved template: %s %s", row.State, row.Reason)
	}
	// A later native deletion can be retired globally without rewriting its
	// tombstone or requiring a live monitor snapshot that cannot exist.
	deleted, err := node.writeMonitor(node.current(p.MonitorID).MonitorInput, node.heads[p.MonitorID], p.MonitorID, true)
	if err != nil {
		t.Fatal(err)
	}
	w := dashboardHTTP(d, "DELETE", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]string{"revision": row.Revision})
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "removed" || row.Placements[0].Revision != deleted.Revision || row.Placements[0].Sequence != 3 {
		t.Fatal("a native deletion left the global record permanently stuck")
	}
}

func TestDashboardFleetReconcilesPreparedUpdateAfterDirectEdit(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	var editBeforeWrite atomic.Bool
	var edited Monitor
	nodes := map[string]*Node{}
	register := func(id string) string {
		node := dashboardRemoteFixture(t, id)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/fleet-placements/") && editBeforeWrite.Swap(false) {
				id := "fleet-" + strings.TrimPrefix(r.URL.Path, "/api/fleet-placements/")
				input := node.current(id).MonitorInput
				input.Name = "Direct edit after preflight"
				var err error
				edited, err = node.writeMonitor(input, node.heads[id], id, false)
				if err != nil {
					t.Error(err)
				}
			}
			node.ServeHTTP(w, r)
		}))
		t.Cleanup(server.Close)
		reg := enrollDashboardNode(t, d, owner, server.URL)
		nodes[reg.ID] = node
		return reg.ID
	}
	row := fleetCreateFixture(t, d, owner, []string{register("node-a"), register("node-b")}, nil)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	p := row.Placements[0]
	node := nodes[p.RegistrationID]
	added := register("node-c")
	input := node.current(p.MonitorID).MonitorInput
	input.Name = "Saved dashboard update"
	w := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{
		"revision": row.Revision, "monitor": input, "registrationIds": append(slices.Clone(row.RegistrationIDs), added),
	})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	editBeforeWrite.Store(true)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	plan, err := d.openFleet(row)
	if err != nil || row.State != "blocked" || !plan.Prepared || !plan.Rejected[p.ID] || row.Placements[0].Sequence != p.Sequence {
		t.Fatal("the refused prepared command was not retained")
	}
	if len(row.Placements) != 3 || len(plan.Unsent) != 2 {
		t.Fatal("later existing and new destinations were not retained as unsent")
	}
	// Retry still sends exactly the rejected command and cannot overwrite the edit.
	w = dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors/"+row.ID+"/retry", owner, "", map[string]string{"revision": row.Revision})
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	d.finishFleet(context.Background(), row.ID)
	if node.current(p.MonitorID).Revision != edited.Revision {
		t.Fatal("an ordinary retry overwrote a direct edit")
	}
	row = d.fleet[row.ID]
	for _, revisions := range []map[string]string{{}, {p.ID: p.Revision}} {
		w = dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors/"+row.ID+"/reconcile", owner, "", map[string]any{"revision": row.Revision, "observedRevisions": revisions})
		if w.Code != 409 {
			t.Fatal("a rejected command was replaced without a different reviewed revision")
		}
	}
	// The refusal survives a restart, and the worker verifies the previous node
	// receipt before preparing sequence 2 against the reviewed current head.
	if err = d.loadFleet(); err != nil {
		t.Fatal(err)
	}
	observed := map[string]string{}
	for _, placement := range row.Placements {
		if placement.Sequence > 0 {
			observed[placement.ID] = nodes[placement.RegistrationID].current(placement.MonitorID).Revision
		}
	}
	w = dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors/"+row.ID+"/reconcile", owner, "", map[string]any{
		"revision": row.Revision, "observedRevisions": observed,
	})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "applied" || row.Placements[0].Sequence != p.Sequence+1 || node.current(p.MonitorID).Name != input.Name || len(node.heads) != 1 {
		t.Fatal("explicit review did not recover the refused prepared update")
	}
	for _, placement := range row.Placements {
		if placement.State != "applied" || nodes[placement.RegistrationID].current(placement.MonitorID).Name != input.Name || len(nodes[placement.RegistrationID].heads) != 1 {
			t.Fatal("recovery lost or duplicated an unsent destination")
		}
	}
	w = dashboardHTTP(d, "DELETE", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]string{"revision": row.Revision})
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	d.finishFleet(context.Background(), row.ID)
	if d.fleet[row.ID].State != "removed" || node.current(p.MonitorID) != nil {
		t.Fatal("recovered placement could not be retired")
	}
}

func TestDashboardFleetReferenceChoicesAndAuthenticatedRouting(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	sa, sb := httptest.NewServer(a), httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	tags := []Tag{}
	for _, node := range []*Node{a, b} {
		w := dashboardHTTP(node, "POST", "/api/tags", adminToken, "", TagInput{Name: "Location", Color: "#123456"})
		var tag Tag
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &tag) != nil {
			t.Fatal("tag fixture refused")
		}
		tags = append(tags, tag)
	}
	in := placementFixtureInput()
	in.Tags = []MonitorTag{{TagID: tags[0].ID, Value: "original-value"}}
	w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{"requestId": placementFixtureID, "monitor": in, "registrationIds": []string{ra.ID, rb.ID}, "references": map[string]any{rb.ID: map[string]any{"tags": []MonitorTag{{TagID: tags[1].ID, Value: "destination-private-value"}}}}})
	if w.Code != 202 {
		t.Fatalf("reference plan: %d %s", w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), placementFixtureID)
	row := d.fleet[placementFixtureID]
	if row.State != "applied" {
		t.Fatalf("mapped references failed: %s", row.Reason)
	}
	for index, node := range []*Node{a, b} {
		for _, heads := range node.heads {
			if heads[0].Monitor.Tags[0].TagID != tags[index].ID {
				t.Fatal("node-local references were not respected")
			}
		}
	}
	viewer, err := d.publicFleet(row, "viewer")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(viewer)
	if strings.Contains(string(data), "destination-private-value") || strings.Contains(row.Private, "destination-private-value") {
		t.Fatal("private reference mapping leaked")
	}
	for _, mutate := range []func(*dashboardFleetRecord){
		func(r *dashboardFleetRecord) { r.RegistrationIDs = []string{rb.ID} },
		// Anchors sort by random registration ID. Always change the addressable
		// registration, rather than occasionally assigning its existing value.
		func(r *dashboardFleetRecord) {
			if r.Placements[0].RegistrationID == rb.ID {
				r.Placements[0].RegistrationID = ra.ID
			} else {
				r.Placements[0].RegistrationID = rb.ID
			}
		},
		func(r *dashboardFleetRecord) { r.ActorRole = "viewer" },
		func(r *dashboardFleetRecord) { r.State = "pending" },
	} {
		tampered := copyFleet(row)
		mutate(&tampered)
		if equalJSON(tampered, row) {
			t.Fatal("routing test did not change the record")
		}
		if _, err = d.openFleet(tampered); err == nil {
			t.Fatal("encrypted plan accepted changed routing, ownership or state")
		}
	}
}

func TestDashboardFleetUpdatesRetiresAndReloadsJournal(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(node)
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	row := fleetCreateFixture(t, d, owner, []string{reg.ID}, nil)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	plan, err := d.openFleet(row)
	if err != nil {
		t.Fatal(err)
	}
	plan.Monitor.Name = "Edited globally"
	w := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision, "monitor": plan.Monitor, "registrationIds": row.RegistrationIDs})
	if w.Code != 202 {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	// A new dashboard instance must reconstruct its operation from the retained
	// encrypted journal rather than a closure, browser state or command cache.
	reloaded, err := NewDashboard(DashboardOptions{Accounts: d.accounts, Key: dashboardKeyFixture})
	if err != nil {
		t.Fatal(err)
	}
	defer reloaded.Close()
	reloaded.finishFleet(context.Background(), row.ID)
	row = reloaded.fleet[row.ID]
	if row.State != "applied" || row.Placements[0].Sequence != 2 || node.current(row.Placements[0].MonitorID).Name != "Edited globally" {
		t.Fatal("saved update did not survive journal reload")
	}
	if wrongRoute := dashboardHTTP(reloaded, "DELETE", "/api/dashboard/fleet/monitors/"+row.ID+"/retry", owner, "", map[string]string{"revision": row.Revision}); wrongRoute.Code != 404 || !equalJSON(row, reloaded.fleet[row.ID]) {
		t.Fatal("DELETE on a retry subroute changed the deployment")
	}
	w = dashboardHTTP(reloaded, "DELETE", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]string{"revision": row.Revision})
	if w.Code != 202 {
		t.Fatal(w.Code)
	}
	queued := reloaded.fleet[row.ID]
	if repeated := dashboardHTTP(reloaded, "DELETE", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]string{"revision": queued.Revision}); repeated.Code != 202 || !equalJSON(queued, reloaded.fleet[row.ID]) {
		t.Fatal("repeated queued deletion changed its saved identity")
	}
	reloaded.finishFleet(context.Background(), row.ID)
	row = reloaded.fleet[row.ID]
	if row.State != "removed" || node.current(row.Placements[0].MonitorID) != nil {
		t.Fatalf("global deletion not confirmed: %s %s", row.State, row.Reason)
	}
	if w = dashboardHTTP(reloaded, "DELETE", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]string{"revision": row.Revision}); w.Code != 204 {
		t.Fatal("confirmed deletion was not idempotent")
	}
}

func TestDashboardFleetPreparationFailureKeepsCommittedLayout(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	sa, sb := httptest.NewServer(a), httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	row := fleetCreateFixture(t, d, owner, []string{ra.ID}, nil)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	placements := copyFleet(row).Placements
	plan, err := d.openFleet(row)
	if err != nil {
		t.Fatal(err)
	}
	request := func(refs []string) {
		row = d.fleet[row.ID]
		w := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{
			"revision": row.Revision, "registrationIds": []string{ra.ID, rb.ID}, "monitor": plan.Monitor,
			"references": map[string]any{rb.ID: map[string]any{"notificationIds": refs}},
		})
		if w.Code != 202 {
			t.Fatal(w.Code)
		}
	}
	request([]string{"not-on-this-node"})
	before, err := d.openFleet(d.fleet[row.ID])
	if err != nil {
		t.Fatal(err)
	}
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	after, err := d.openFleet(row)
	if err != nil || row.State != "blocked" || !equalJSON(placements, row.Placements) || !equalJSON(before, after) ||
		a.current(placements[0].MonitorID).Revision != placements[0].Revision || len(b.heads) != 0 {
		t.Fatal("failed preflight published candidate placements or changed the previous plan")
	}
	request([]string{})
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "applied" || len(row.Placements) != 2 || len(a.heads) != 1 || len(b.heads) != 1 || row.Placements[0].ID != placements[0].ID {
		t.Fatal("corrected preflight could not retain the existing copy and add its destination")
	}
}

func TestDashboardFleetPreparationCancellationKeepsInputs(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(node)
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	row := copyFleet(fleetCreateFixture(t, d, owner, []string{reg.ID}, nil))
	plan, err := d.openFleet(row)
	if err != nil {
		t.Fatal(err)
	}
	beforeRow, beforePlan := copyFleet(row), plan
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = d.prepareFleet(ctx, &row, &plan); !errors.Is(err, context.Canceled) || !equalJSON(row, beforeRow) || !equalJSON(plan, beforePlan) || len(node.heads) != 0 {
		t.Fatal("cancelled preparation changed its inputs or did not preserve cancellation", err)
	}
}

func TestDashboardFleetCancelledPreflightLeavesExistingMonitorAlone(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(node)
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	m, err := node.writeMonitor(placementFixtureInput(), nil, "keep-existing", false)
	if err != nil {
		t.Fatal(err)
	}
	row := fleetCreateFixture(t, d, owner, []string{reg.ID}, &fleetSourceReference{reg.ID, m.ID, m.Revision})
	w := dashboardHTTP(d, "DELETE", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]string{"revision": row.Revision})
	if w.Code != 204 || d.fleet[row.ID].State != "removed" || d.fleet[row.ID].Source != nil || node.current(m.ID).Revision != m.Revision {
		t.Fatal("cancelling a preparation changed or hid an existing monitor")
	}
}
