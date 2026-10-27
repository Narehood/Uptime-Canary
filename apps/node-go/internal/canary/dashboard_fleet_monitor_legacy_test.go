package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"uptime-canary/apps/node-go/internal/nodeconnection"
)

// Main before PR #19 has no placement route or capability flag. Its ordinary
// monitor/channel/tag schemas are unchanged by #19 (checked against a4392c2).
func preFleetFixture(n *Node, creates, unsupported *atomic.Int32, lose, refuse *atomic.Bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Canary-Node-Id", n.ID)
		if strings.HasPrefix(r.URL.Path, "/api/fleet-placements/") {
			unsupported.Add(1)
			errorReply(w, fail(404, "Not found"))
			return
		}
		if r.URL.Path == "/api/auth/status" {
			recorder := httptest.NewRecorder()
			n.ServeHTTP(recorder, r)
			var status map[string]any
			_ = json.Unmarshal(recorder.Body.Bytes(), &status)
			delete(status, "fleetPlacements")
			delete(status, "fleetResourceSync")
			reply(w, 200, status)
			return
		}
		if r.URL.Path == "/api/monitors" && r.Method == "POST" {
			creates.Add(1)
			if refuse != nil && refuse.Load() {
				errorReply(w, fail(409, "Fixture refused before writing"))
				return
			}
			if lose != nil && lose.Swap(false) {
				recorder := httptest.NewRecorder()
				n.ServeHTTP(recorder, r)
				w.WriteHeader(recorder.Code)
				_, _ = w.Write([]byte("{"))
				return
			}
		}
		n.ServeHTTP(w, r)
	})
}

func fleetRowForOriginal(t *testing.T, d *Dashboard, id string) dashboardFleetRecord {
	t.Helper()
	for _, row := range d.fleet {
		if row.Source != nil && row.Source.MonitorID == id {
			return row
		}
	}
	t.Fatal("original monitor definition missing")
	return dashboardFleetRecord{}
}

func TestGroupPreFleetAPISyncsBothDirectionsAndOptsOutOfDefaults(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var creates, unsupported atomic.Int32
	sa, sb := httptest.NewServer(a), httptest.NewServer(preFleetFixture(b, &creates, &unsupported, nil, nil))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	channel := groupChannelFixture(t, a)
	defaultChannel := groupChannelFixture(t, b)
	def := defaultChannel.ChannelInput
	def.IsDefault = true
	_, _ = b.writeResource(resourceChannel, defaultChannel.ID, def, false)
	in := placementFixtureInput()
	in.NotificationIDs = []string{channel.ID}
	first := groupMonitorFixture(t, a, in)
	in.Name = "No notifications"
	in.NotificationIDs = nil
	second := groupMonitorFixture(t, a, in)
	in.NodeIDs = []string{b.ID}
	in.Name = "Old node's existing monitor"
	originalB, err := b.writeMonitor(in, nil, "original-on-old-node", false)
	if err != nil {
		t.Fatal(err)
	}
	groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	finishGroupFixture(t, d)
	if len(d.fleet) != 3 || len(a.monitorViews("admin")) != 3 || len(b.monitorViews("admin")) != 3 {
		for _, row := range d.fleet {
			t.Log(row.State, row.Reason)
		}
		t.Fatalf("old API lost or duplicated monitors: definitions=%d a=%d b=%d", len(d.fleet), len(a.monitorViews("admin")), len(b.monitorViews("admin")))
	}
	for _, row := range d.fleet {
		if row.State != "applied" {
			t.Fatal(row.State, row.Reason)
		}
	}
	if a.current(first.ID) == nil || a.current(second.ID) == nil || b.current(originalB.ID) == nil {
		t.Fatal("adoption changed an original monitor ID")
	}
	for _, p := range fleetRowForOriginal(t, d, second.ID).Placements {
		if p.RegistrationID == rb.ID && len(b.current(p.MonitorID).NotificationIDs) != 0 {
			t.Fatal("old POST silently enabled destination defaults")
		}
	}
	for _, p := range fleetRowForOriginal(t, d, first.ID).Placements {
		if p.RegistrationID == rb.ID {
			m := b.current(p.MonitorID)
			if len(m.NotificationIDs) != 1 || b.currentResource(resourceChannel, m.NotificationIDs[0]) == nil {
				t.Fatal("old monitor lost copied notifications")
			}
		}
	}
	if creates.Load() != 2 {
		t.Fatal("native adoption issued a create instead of preserving history")
	}
}

func TestGroupPreFleetAPIEditsReconcilesAndRetiresSameID(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var creates, unsupported atomic.Int32
	sa, sb := httptest.NewServer(a), httptest.NewServer(preFleetFixture(b, &creates, &unsupported, nil, nil))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	original := groupMonitorFixture(t, a, placementFixtureInput())
	groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	finishGroupFixture(t, d)
	row := fleetRowForOriginal(t, d, original.ID)
	var placement dashboardFleetPlacement
	for _, p := range row.Placements {
		if p.RegistrationID == rb.ID {
			placement = p
		}
	}
	if row.State != "applied" || placement.MonitorID == "" {
		t.Fatal(row.State, row.Reason)
	}
	local := b.current(placement.MonitorID).MonitorInput
	local.Name = "Native edit to preserve"
	edited, err := b.writeMonitor(local, b.heads[placement.MonitorID], placement.MonitorID, false)
	if err != nil {
		t.Fatal(err)
	}
	plan, _ := d.openFleet(row)
	plan.Monitor.Name = "Dashboard edit"
	w := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision, "registrationIds": row.RegistrationIDs, "groupIds": row.GroupIDs, "monitor": plan.Monitor})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "blocked" || b.current(placement.MonitorID).Revision != edited.Revision {
		t.Fatal("old API overwrote a direct node edit")
	}
	w = dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors/"+row.ID+"/reconcile", owner, "", map[string]any{"revision": row.Revision, "observedRevisions": map[string]string{placement.ID: edited.Revision}})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "applied" || b.current(placement.MonitorID).Name != "Dashboard edit" || creates.Load() != 1 {
		t.Fatal("old edit did not keep its physical identity", row.State, row.Reason)
	}
	w = dashboardHTTP(d, "DELETE", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]string{"revision": row.Revision})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), row.ID)
	if d.fleet[row.ID].State != "removed" || b.current(placement.MonitorID) != nil || a.current(original.ID) != nil {
		t.Fatal("old copies were not retired")
	}
	if len(b.heads[placement.MonitorID]) != 1 || !b.heads[placement.MonitorID][0].Monitor.Deleted {
		t.Fatal("retirement replaced history instead of retaining its tombstone")
	}
}

func TestGroupPreFleetAPILostCreateReadsBackWithoutCoalescingExistingMonitor(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var creates, unsupported atomic.Int32
	var lose atomic.Bool
	lose.Store(true)
	sa, sb := httptest.NewServer(a), httptest.NewServer(preFleetFixture(b, &creates, &unsupported, &lose, nil))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	original := groupMonitorFixture(t, a, placementFixtureInput())
	group := groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	d.runGroupSyncs(context.Background())
	in := original.MonitorInput
	in.NodeIDs = []string{b.ID}
	unrelated, err := b.writeMonitor(in, nil, "unrelated-same-input", false)
	if err != nil {
		t.Fatal(err)
	}
	row := fleetRowForOriginal(t, d, original.ID)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "pending" || creates.Load() != 1 {
		t.Fatal("lost create did not retain its operation", row.State, row.Reason)
	}
	job, _, _ := loadOne[dashboardGroupSync](d.accounts.node.store, "dashboardGroupSyncs", group.ID)
	d.syncNodeGroup(context.Background(), group.ID, job)
	if len(d.fleet) != 1 {
		t.Fatal("collection adopted an unacknowledged copy as another monitor")
	}
	if err := d.loadFleet(); err != nil {
		t.Fatal(err)
	}
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "applied" || creates.Load() != 1 || len(b.monitorViews("admin")) != 2 || b.current(unrelated.ID).Revision != unrelated.Revision {
		t.Fatal("read-back repeated creation or coalesced an unrelated existing monitor", row.State, row.Reason)
	}
	for _, p := range row.Placements {
		if p.RegistrationID == rb.ID && p.MonitorID == unrelated.ID {
			t.Fatal("existing equal monitor was taken over")
		}
	}
}

func TestGroupPreFleetAPIDefiniteRefusalCanBeEdited(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var creates, unsupported atomic.Int32
	var refuse atomic.Bool
	refuse.Store(true)
	sa, sb := httptest.NewServer(a), httptest.NewServer(preFleetFixture(b, &creates, &unsupported, nil, &refuse))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	original := groupMonitorFixture(t, a, placementFixtureInput())
	groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	finishGroupFixture(t, d)
	row := fleetRowForOriginal(t, d, original.ID)
	plan, _ := d.openFleet(row)
	if row.State != "blocked" || plan.Prepared || len(b.heads) != 0 {
		t.Fatal("definite refusal became an uneditable unknown command", row.State, row.Reason)
	}
	refuse.Store(false)
	plan.Monitor.Name = "Corrected monitor"
	w := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision, "registrationIds": row.RegistrationIDs, "groupIds": row.GroupIDs, "monitor": plan.Monitor})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), row.ID)
	if d.fleet[row.ID].State != "applied" || creates.Load() != 2 || len(b.heads) != 1 {
		t.Fatal("corrected old-node placement did not recover", d.fleet[row.ID].Reason)
	}
}

func TestGroupPreFleetOutboundDoesNotProbeUnsupportedWireRoutes(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	n := dashboardRemoteFixture(t, "old-outbound")
	var creates, unsupported atomic.Int32
	legacy := preFleetFixture(n, &creates, &unsupported, nil, nil)
	server := httptest.NewServer(legacy)
	defer server.Close()
	registration := enrollDashboardNode(t, d, owner, server.URL)
	row := d.registry[registration.ID]
	secret, err := d.openSecrets(row)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := nodeconnection.RandomKey()
	row.Connection, row.URL, row.ConnectionHash = "outbound", "", digest(key)
	row.Credentials, err = d.sealSecrets(row, secret)
	if err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	err = d.saveRegistry(row)
	d.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	epoch, err := d.connections.Open(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			frame, err := d.connections.Poll(ctx, row.ID, epoch)
			if err != nil {
				return
			}
			if frame == nil {
				continue
			}
			r := httptest.NewRequest(frame.Method, frame.Path, strings.NewReader(string(frame.Body)))
			r.Header.Set("Authorization", frame.Authorization)
			r.Header.Set("Content-Type", frame.ContentType)
			w := httptest.NewRecorder()
			legacy.ServeHTTP(w, r)
			_ = d.connections.Complete(row.ID, epoch, nodeconnection.Response{ID: frame.ID, NodeID: n.ID, Status: w.Code, ContentType: w.Header().Get("Content-Type"), Body: w.Body.Bytes()})
		}
	}()
	t.Cleanup(func() { cancel(); d.connections.Close(); <-done })
	in := placementFixtureInput()
	in.NodeIDs = []string{n.ID}
	original := groupMonitorFixture(t, n, in)
	groupSyncFixture(t, d, owner, []string{row.ID})
	finishGroupFixture(t, d)
	managed := fleetRowForOriginal(t, d, original.ID)
	if managed.State != "applied" || unsupported.Load() != 0 || creates.Load() != 0 {
		t.Fatal("old outbound connection was sent a new wire route", managed.State, managed.Reason)
	}
	plan, _ := d.openFleet(managed)
	plan.Monitor.Name = "Edited with notification copying off"
	w := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+managed.ID, owner, "", map[string]any{"revision": managed.Revision, "registrationIds": managed.RegistrationIDs, "groupIds": managed.GroupIDs, "monitor": plan.Monitor, "syncNotifications": false})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), managed.ID)
	managed = d.fleet[managed.ID]
	if managed.State != "applied" || unsupported.Load() != 0 || n.current(original.ID).Name != plan.Monitor.Name {
		t.Fatal("saving the copying preference forgot the older outbound API", managed.State, managed.Reason)
	}
}

func TestGroupPreFleetAPICreatesWaitForEarlierUnknownOperation(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var creates, unsupported atomic.Int32
	var waiting atomic.Bool
	waiting.Store(true)
	legacy := preFleetFixture(b, &creates, &unsupported, nil, nil)
	sa := httptest.NewServer(a)
	sb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if waiting.Load() && r.Method == "POST" && r.URL.Path == "/api/monitors" {
			creates.Add(1)
			w.Header().Set("X-Canary-Node-Id", b.ID)
			errorReply(w, fail(502, "Unknown create fixture"))
			return
		}
		legacy.ServeHTTP(w, r)
	}))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	in := placementFixtureInput()
	groupMonitorFixture(t, a, in)
	in.Name = "Second queued copy"
	groupMonitorFixture(t, a, in)
	groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	finishGroupFixture(t, d)
	if creates.Load() != 1 || len(b.heads) != 0 {
		t.Fatal("another old-node create overtook an uncertain operation")
	}
	var pending MonitorInput
	for _, row := range d.fleet {
		plan, _ := d.openFleet(row)
		for _, p := range row.Placements {
			if p.RegistrationID == rb.ID && plan.LegacyMonitorAttempts[p.ID].Sent {
				body, _ := fleetCommandBytes(plan, p)
				var cmd fleetPlacementCommand
				_ = json.Unmarshal(body, &cmd)
				_ = json.Unmarshal(cmd.Monitor, &pending)
			}
		}
	}
	if pending.Name == "" {
		t.Fatal("unknown create intent missing")
	}
	if _, err := b.writeMonitor(pending, nil, "late-confirmed-old-copy", false); err != nil {
		t.Fatal(err)
	}
	waiting.Store(false)
	for range 2 {
		for id := range d.fleet {
			d.finishFleet(context.Background(), id)
		}
	}
	for _, row := range d.fleet {
		if row.State != "applied" {
			t.Fatal(row.State, row.Reason)
		}
	}
	if creates.Load() != 2 || len(b.monitorViews("admin")) != 2 {
		t.Fatal("late confirmation duplicated or lost a queued copy")
	}
}

func TestGroupPreFleetAPIPreservesSharedHAIdentityAndWaitsForPeer(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := causalCapacityNode(t, "a", ":memory:"), causalCapacityNode(t, "b", ":memory:")
	a.peerSeen[b.ID], b.peerSeen[a.ID] = 1, 1
	in := input([]string{a.ID, b.ID}, "http://127.0.0.1:9", "")
	in.Enabled = false
	original, err := a.writeMonitor(in, nil, "old-ha-original", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range causalCapacityPages(t, a) {
		if err := b.applySnapshot(page); err != nil {
			t.Fatal(err)
		}
	}
	var creates, unsupported atomic.Int32
	var oldA, oldB atomic.Bool
	serve := func(n *Node, old *atomic.Bool) http.Handler {
		legacy := preFleetFixture(n, &creates, &unsupported, nil, nil)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if old.Load() {
				legacy.ServeHTTP(w, r)
			} else {
				n.ServeHTTP(w, r)
			}
		})
	}
	sa, sb := httptest.NewServer(serve(a, &oldA)), httptest.NewServer(serve(b, &oldB))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	// Collection chooses the first registration, whose ID is generated during
	// enrollment. Make that anchor the old node regardless of random ID order.
	oldA.Store(ra.ID < rb.ID)
	oldB.Store(rb.ID < ra.ID)
	groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	finishGroupFixture(t, d)
	row := fleetRowForOriginal(t, d, original.ID)
	if row.State != "pending" || len(row.Placements) != 1 || creates.Load() != 0 {
		t.Fatal("HA adoption replaced its root or claimed peer confirmation", row.State, row.Reason)
	}
	plan, _ := d.openFleet(row)
	if !plan.LegacyMonitors[row.Placements[0].RegistrationID] {
		t.Fatal("fixture did not exercise the old API anchor")
	}
	anchor, peer := a, b
	if row.Placements[0].RegistrationID == rb.ID {
		anchor, peer = b, a
	}
	for _, page := range causalCapacityPages(t, anchor) {
		if err := peer.applySnapshot(page); err != nil {
			t.Fatal(err)
		}
	}
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "applied" || a.current(original.ID) == nil || b.current(original.ID) == nil || len(a.heads) != 1 || len(b.heads) != 1 {
		t.Fatal("old API failed to keep its shared HA identity", row.State, row.Reason)
	}
}
