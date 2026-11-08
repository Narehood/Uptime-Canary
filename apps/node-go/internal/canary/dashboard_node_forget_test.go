package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uptime-canary/apps/node-go/internal/nodetls"
)

func forgetNodeFixture(t *testing.T, d *Dashboard, owner string, node RegisteredNode) {
	t.Helper()
	w := dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+node.ID, owner, "", map[string]any{"force": true, "forget": true, "confirmation": node.Label})
	if w.Code != 202 {
		t.Fatal("local removal was not accepted", w.Code, w.Body.String())
	}
}

func forgetFleetFixture(t *testing.T, d *Dashboard, owner string, ids []string, _ *fleetSourceReference) dashboardFleetRecord {
	t.Helper()
	id := strings.ReplaceAll(newID(), "-", "")
	in := placementFixtureInput()
	in.Enabled = false
	w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{"requestId": id, "registrationIds": ids, "monitor": in})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	return d.fleet[id]
}

func TestForgetAlreadyPendingOfflineNodeSurvivesRestartAndKeepsSharedMonitor(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var offline atomic.Bool
	var calls atomic.Int32
	sa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
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
	group := groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	shared := forgetFleetFixture(t, d, owner, []string{ra.ID, rb.ID}, nil)
	solo := forgetFleetFixture(t, d, owner, []string{ra.ID}, nil)
	d.finishFleet(context.Background(), shared.ID)
	d.finishFleet(context.Background(), solo.ID)
	physical := ""
	for _, p := range d.fleet[shared.ID].Placements {
		if p.RegistrationID == rb.ID {
			physical = p.MonitorID
		}
	}
	if physical == "" {
		t.Fatal("shared fixture has no survivor")
	}
	forceNodeFixture(t, d, owner, ra)
	offline.Store(true)
	_, _ = d.finishEnrollment(context.Background(), ra.ID)
	d.finishFleet(context.Background(), shared.ID)
	d.finishFleet(context.Background(), solo.ID)
	_, err := d.finishEnrollment(context.Background(), ra.ID)
	if err == nil || !strings.Contains(d.registry[ra.ID].Error, "A selected node cannot be read yet") {
		t.Fatal("fixture did not reproduce the already-pending failure", d.registry[ra.ID].Error)
	}
	d.busy[ra.ID], d.fleetBusy[shared.ID] = true, true
	beforeCalls := calls.Load()
	forgetNodeFixture(t, d, owner, ra)
	if calls.Load() != beforeCalls || d.forgottenNodes[ra.ID].ID == "" {
		t.Fatal("acceptance contacted the offline node or lost its journal")
	}
	if err := d.finishForgottenNode(context.Background(), ra.ID); err == nil {
		t.Fatal("local removal raced a running worker")
	}
	delete(d.busy, ra.ID)
	if err := d.finishForgottenNode(context.Background(), ra.ID); err == nil || !strings.Contains(err.Error(), "Waiting for the current monitor request") {
		t.Fatal("local removal did not wait for the monitor worker", err)
	}
	if _, ok := d.registry[ra.ID]; !ok {
		t.Fatal("busy monitor allowed removal")
	}
	delete(d.fleetBusy, shared.ID)
	d.Close()
	restarted, err := NewDashboard(DashboardOptions{Accounts: d.accounts, Key: dashboardKeyFixture})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	d = restarted
	if err := d.finishForgottenNode(context.Background(), ra.ID); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != beforeCalls {
		t.Fatal("local cleanup tried to read the offline node")
	}
	if _, ok := d.registry[ra.ID]; ok {
		t.Fatal("pending node stayed in the registry")
	}
	if d.fleet[solo.ID].State != "removed" || !slices.Equal(d.fleet[shared.ID].RegistrationIDs, []string{rb.ID}) {
		t.Fatal("local monitor references survived removal")
	}
	g, ok, err := loadOne[dashboardNodeGroup](d.accounts.node.store, "dashboardNodeGroups", group.ID)
	if err != nil || !ok || !slices.Equal(g.RegistrationIDs, []string{rb.ID}) {
		t.Fatal("group membership survived removal", err)
	}
	for range 4 {
		d.finishFleet(context.Background(), shared.ID)
	}
	if d.fleet[shared.ID].State != "applied" || b.current(physical) == nil || b.current(physical).Deleted {
		t.Fatal("shared survivor changed identity or stopped", d.fleet[shared.ID].Reason)
	}
	if len(a.accounts) != 4 || len(d.forgottenNodes) != 1 {
		t.Fatal("unreachable revocation was falsely confirmed")
	}
	if err := d.finishForgottenNode(context.Background(), ra.ID); err == nil {
		t.Fatal("offline revocation reported success")
	}
	offline.Store(false)
	if err := d.finishForgottenNode(context.Background(), ra.ID); err != nil {
		t.Fatal("background revocation failed after reconnection", err)
	}
	if len(a.accounts) != 1 || len(d.forgottenNodes) != 0 {
		t.Fatal("revocation did not finish")
	}
}

func TestForgetNodeRequiresExactNameAndAdministrator(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	s := httptest.NewServer(node)
	defer s.Close()
	reg := enrollDashboardNode(t, d, owner, s.URL)
	forceNodeFixture(t, d, owner, reg)
	for _, confirmation := range []string{"", reg.Label + " ", "wrong"} {
		w := dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+reg.ID, owner, "", map[string]any{"force": true, "forget": true, "confirmation": confirmation})
		if w.Code < 400 || len(d.forgottenNodes) != 0 {
			t.Fatal("invalid name queued local deletion")
		}
	}
	w := dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+reg.ID, "", "", map[string]any{"force": true, "forget": true, "confirmation": reg.Label})
	if w.Code != 401 || len(d.forgottenNodes) != 0 {
		t.Fatal("anonymous local deletion accepted")
	}
	forgetNodeFixture(t, d, owner, reg)
	first := d.forgottenNodes[reg.ID]
	forgetNodeFixture(t, d, owner, reg)
	second, _ := d.openSecrets(d.forgottenNodes[reg.ID])
	prior, _ := d.openSecrets(first)
	if !equalJSON(prior, second) || len(d.forgottenNodes) != 1 {
		t.Fatal("repeat confirmation duplicated the cleanup")
	}
}

func TestForgetNodeLocalCleanupRollsBackAtomically(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	s := httptest.NewServer(node)
	defer s.Close()
	reg := enrollDashboardNode(t, d, owner, s.URL)
	g := groupSyncFixture(t, d, owner, []string{reg.ID})
	fleet := forgetFleetFixture(t, d, owner, []string{reg.ID}, nil)
	d.finishFleet(context.Background(), fleet.ID)
	forceNodeFixture(t, d, owner, reg)
	forgetNodeFixture(t, d, owner, reg)
	before := copyFleet(d.fleet[fleet.ID])
	if _, err := d.accounts.node.store.db.Exec(`CREATE TRIGGER forget_failure BEFORE INSERT ON records WHEN NEW.category='dashboardFleet' BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if err := d.finishForgottenNode(context.Background(), reg.ID); err == nil {
		t.Fatal("fixture failed to reject transaction")
	}
	if _, ok := d.registry[reg.ID]; !ok || !equalJSON(before, d.fleet[fleet.ID]) {
		t.Fatal("failed transaction published partial removal")
	}
	if _, ok, err := loadOne[dashboardNodeGroup](d.accounts.node.store, "dashboardNodeGroups", g.ID); err != nil || !ok {
		t.Fatal("failed transaction lost the group")
	}
}

func TestForgetOfflineHAAnchorKeepsPhysicalMonitorOnPeer(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := causalCapacityNode(t, "a", ":memory:"), causalCapacityNode(t, "b", ":memory:")
	a.peerSeen[b.ID], b.peerSeen[a.ID] = 1, 1
	var offlineA, offlineB atomic.Bool
	serve := func(n *Node, offline *atomic.Bool) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if offline.Load() {
				http.Error(w, "offline", 503)
				return
			}
			n.ServeHTTP(w, r)
		})
	}
	sa, sb := httptest.NewServer(serve(a, &offlineA)), httptest.NewServer(serve(b, &offlineB))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	replicate := func() {
		for _, pair := range [][2]*Node{{a, b}, {b, a}} {
			for _, page := range causalCapacityPages(t, pair[0]) {
				if err := pair[1].applySnapshot(page); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	fleet := forgetFleetFixture(t, d, owner, []string{ra.ID, rb.ID}, nil)
	for range 4 {
		d.finishFleet(context.Background(), fleet.ID)
		replicate()
	}
	row := d.fleet[fleet.ID]
	if row.State != "applied" || len(row.Placements) != 1 {
		t.Fatal("HA fixture not applied", row.Reason)
	}
	anchor, survivor := ra, rb
	remaining := b
	if row.Placements[0].RegistrationID == rb.ID {
		anchor, survivor, remaining = rb, ra, a
	}
	forceNodeFixture(t, d, owner, anchor)
	if anchor.ID == ra.ID {
		offlineA.Store(true)
	} else {
		offlineB.Store(true)
	}
	_, _ = d.finishEnrollment(context.Background(), anchor.ID)
	forgetNodeFixture(t, d, owner, anchor)
	if err := d.finishForgottenNode(context.Background(), anchor.ID); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		d.finishFleet(context.Background(), fleet.ID)
	}
	kept := d.fleet[fleet.ID]
	if kept.State != "applied" || !slices.Equal(kept.RegistrationIDs, []string{survivor.ID}) || len(kept.Placements) != 1 || kept.Placements[0].MonitorID != row.Placements[0].MonitorID {
		t.Fatal("offline HA handoff lost identity", kept.State, kept.Reason)
	}
	m := remaining.current(row.Placements[0].MonitorID)
	if m == nil || !slices.Equal(m.NodeIDs, []string{remaining.ID}) {
		t.Fatal("survivor assignment was not updated")
	}
	plan, _ := d.openFleet(kept)
	plan.Monitor.Name = "Editable after offline removal"
	w := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+kept.ID, owner, "", map[string]any{"revision": kept.Revision, "registrationIds": kept.RegistrationIDs, "monitor": plan.Monitor})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), kept.ID)
	if d.fleet[kept.ID].State != "applied" {
		t.Fatal(d.fleet[kept.ID].Reason)
	}
	encoded, _ := json.Marshal(d.publicNode(d.forgottenNodes[anchor.ID], "viewer"))
	if strings.Contains(string(encoded), adminToken) {
		t.Fatal("revocation journal leaked credentials")
	}
}

func TestForgetPreservesExactUnknownSurvivorCommandBeforeRetargeting(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var lose atomic.Bool
	sa := httptest.NewServer(a)
	sb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/fleet-placements/") && lose.Swap(false) {
			recorder := httptest.NewRecorder()
			b.ServeHTTP(recorder, r)
			if recorder.Code != 200 && recorder.Code != 201 {
				t.Error("fixture write failed", recorder.Code, recorder.Body.String())
			}
			w.Header().Set("X-Canary-Node-Id", b.ID)
			errorReply(w, fail(503, "fixture lost successful acknowledgement"))
			return
		}
		b.ServeHTTP(w, r)
	}))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	channel := groupChannelFixture(t, a)
	in := placementFixtureInput()
	in.Enabled = false
	in.NotificationIDs = []string{channel.ID}
	id := strings.ReplaceAll(newID(), "-", "")
	w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{"requestId": id, "registrationIds": []string{ra.ID, rb.ID}, "monitor": in, "syncNotifications": true, "notificationSourceRegistrationId": ra.ID})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	lose.Store(true)
	d.finishFleet(context.Background(), id)
	row := d.fleet[id]
	plan, _ := d.openFleet(row)
	var pending dashboardFleetPlacement
	for _, p := range row.Placements {
		if p.RegistrationID == rb.ID {
			pending = p
		}
	}
	before, err := fleetCommandBytes(plan, pending)
	if err != nil || !plan.Prepared || plan.Unsent[pending.ID] || pending.State != "pending" {
		t.Fatal("fixture did not leave an unknown survivor command")
	}
	forceNodeFixture(t, d, owner, ra)
	forgetNodeFixture(t, d, owner, ra)
	if err := d.finishForgottenNode(context.Background(), ra.ID); err != nil {
		t.Fatal(err)
	}
	row = d.fleet[id]
	plan, _ = d.openFleet(row)
	after, err := fleetCommandBytes(plan, row.Placements[0])
	if err != nil || string(before) != string(after) || !plan.RetargetAfterReplay {
		t.Fatal("local removal rewrote an unknown survivor command")
	}
	b.mu.Lock()
	editedInput := b.current(row.Placements[0].MonitorID).MonitorInput
	editedInput.Name = "Direct node edit after the lost acknowledgement"
	edited, err := b.writeMonitor(editedInput, b.heads[row.Placements[0].MonitorID], row.Placements[0].MonitorID, false)
	b.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	d.finishFleet(context.Background(), id) // Resolve the original receipt, without overwriting that edit.
	row = d.fleet[id]
	plan, _ = d.openFleet(row)
	if !plan.RetargetAfterReplay || row.Placements[0].Sequence != 1 {
		t.Fatal("fixture did not retain replay mode after receipt confirmation")
	}
	for _, method := range []string{"PUT", "DELETE"} {
		blocked := dashboardHTTP(d, method, "/api/dashboard/fleet/monitors/"+id, owner, "", map[string]any{"revision": row.Revision, "registrationIds": row.RegistrationIDs, "monitor": plan.Monitor})
		if blocked.Code != 409 {
			t.Fatal("syncing replay allowed loss of deferred work", method, blocked.Code, blocked.Body.String())
		}
	}
	w = dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors/"+id+"/reconcile", owner, "", map[string]any{"revision": row.Revision, "observedRevisions": map[string]string{row.Placements[0].ID: edited.Revision}})
	if w.Code != 202 {
		t.Fatal("reviewed survivor reconciliation refused", w.Code, w.Body.String())
	}
	plan, _ = d.openFleet(d.fleet[id])
	if plan.RetargetAfterReplay || plan.Prepared || len(plan.Commands) != 0 {
		t.Fatal("reviewed retarget retained frozen replay mode")
	}
	for range 4 {
		d.finishFleet(context.Background(), id)
	}
	if d.fleet[id].State != "applied" || len(b.heads) != 1 {
		t.Fatal("saved survivor command did not resolve without duplication", d.fleet[id].Reason)
	}
}

func TestForgetLostAcknowledgementHAHandoffCanReviewSurvivorRevision(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := causalCapacityNode(t, "a", ":memory:"), causalCapacityNode(t, "b", ":memory:")
	a.peerSeen[b.ID], b.peerSeen[a.ID] = 1, 1
	var lose, offlineA, offlineB atomic.Bool
	serve := func(n *Node, offline *atomic.Bool) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if offline.Load() {
				http.Error(w, "offline", 503)
				return
			}
			if r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/fleet-placements/") && lose.Swap(false) {
				recorder := httptest.NewRecorder()
				n.ServeHTTP(recorder, r)
				if recorder.Code != 201 && recorder.Code != 200 {
					t.Error(recorder.Code, recorder.Body.String())
				}
				w.Header().Set("X-Canary-Node-Id", n.ID)
				errorReply(w, fail(503, "lost acknowledgement"))
				return
			}
			n.ServeHTTP(w, r)
		})
	}
	sa, sb := httptest.NewServer(serve(a, &offlineA)), httptest.NewServer(serve(b, &offlineB))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	fleet := forgetFleetFixture(t, d, owner, []string{ra.ID, rb.ID}, nil)
	lose.Store(true)
	d.finishFleet(context.Background(), fleet.ID)
	before := d.fleet[fleet.ID]
	if len(before.Placements) != 1 || before.Placements[0].Sequence != 0 || before.State != "pending" {
		t.Fatal("lost-acknowledgement HA fixture did not leave unknown work")
	}
	anchor, survivor, peer := ra, rb, b
	if before.Placements[0].RegistrationID == rb.ID {
		anchor, survivor, peer = rb, ra, a
	}
	for _, pair := range [][2]*Node{{a, b}, {b, a}} {
		for _, page := range causalCapacityPages(t, pair[0]) {
			if err := pair[1].applySnapshot(page); err != nil {
				t.Fatal(err)
			}
		}
	}
	if anchor.ID == ra.ID {
		offlineA.Store(true)
	} else {
		offlineB.Store(true)
	}
	forceNodeFixture(t, d, owner, anchor)
	forgetNodeFixture(t, d, owner, anchor)
	if err := d.finishForgottenNode(context.Background(), anchor.ID); err != nil {
		t.Fatal(err)
	}
	d.finishFleet(context.Background(), fleet.ID)
	row := d.fleet[fleet.ID]
	if row.State != "blocked" {
		t.Fatal("stale handoff did not require reviewed revision", row.State, row.Reason)
	}
	peer.mu.Lock()
	current := *peer.current(before.Placements[0].MonitorID)
	peer.mu.Unlock()
	w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors/"+row.ID+"/reconcile", owner, "", map[string]any{"revision": row.Revision, "observedRevisions": map[string]string{row.Placements[0].ID: current.Revision}})
	if w.Code != 202 {
		t.Fatal("handoff cannot review its actual survivor revision", w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "applied" || !slices.Equal(row.RegistrationIDs, []string{survivor.ID}) || row.Placements[0].MonitorID != current.ID || len(peer.heads) != 1 {
		t.Fatal("reviewed handoff lost its physical monitor", row.State, row.Reason)
	}
}

func TestForgetOutboundRevocationUsesOnlyAuthenticatedJournal(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	d.connectionEnabled = true
	server := httptest.NewTLSServer(d.ConnectionHandler())
	defer server.Close()
	d.connectionURL = server.URL
	d.connectionTLSFingerprint = nodetls.CertificateFingerprint(server.Certificate())
	invitation := connectionInvitation(t, d, owner, "forget-outbound", server.URL)
	n, err := New(Options{NodeID: "forget-outbound", Host: "127.0.0.1", Port: Ptr(0), DBPath: ":memory:", IdentityDirectory: connectionIdentityDirectory(t), AdminToken: adminToken, ClusterToken: clusterToken, DashboardURL: server.URL, DashboardTLSFingerprint: invitation.ConnectionTLSFingerprint, DashboardEnrollToken: invitation.Token})
	if err != nil {
		t.Fatal(err)
	}
	defer n.Stop()
	if err = n.Start(); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.invitations[invitation.ID].State == "connected" && d.connections.LastSeen(invitation.ID) != 0
	})
	w := dashboardHTTP(d, "POST", "/api/dashboard/invitations/"+invitation.ID+"/confirm", owner, "", nil)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	reg := d.registry[invitation.ID].RegisteredNode
	forceNodeFixture(t, d, owner, reg)
	forgetNodeFixture(t, d, owner, reg)
	if err = d.finishForgottenNode(context.Background(), reg.ID); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	row := d.forgottenNodes[reg.ID]
	if d.outboundCallAllowedLocked(row, "GET", "/api/overview") || d.outboundCallAllowedLocked(row, "PATCH", "/api/dashboard-enrollments/"+row.ID) || d.outboundCallAllowedLocked(row, "DELETE", "/api/dashboard-enrollments/wrong") {
		t.Error("forgotten journal admitted ordinary management")
	}
	if !d.outboundCallAllowedLocked(row, "DELETE", "/api/dashboard-enrollments/"+row.ID) {
		t.Error("forgotten journal refused its exact DELETE")
	}
	d.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = d.finishForgottenNode(ctx, reg.ID); err != nil {
		t.Fatal("real outbound revocation after registry removal failed", err)
	}
	n.mu.Lock()
	accounts := len(n.accounts)
	n.mu.Unlock()
	if accounts != 1 || len(d.forgottenNodes) != 0 || d.nodeCallSlots[reg.ID] != nil {
		t.Fatal("outbound delegation, call slot or cleanup journal survived confirmed revocation")
	}
}

func TestForgetRenewalAtCapacityDoesNotAdmitNewJobs(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "capacity-node")
	server := httptest.NewServer(node)
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	forceNodeFixture(t, d, owner, reg)
	forgetNodeFixture(t, d, owner, reg)
	saved := d.forgottenNodes[reg.ID]
	secret, err := d.openSecrets(saved)
	if err != nil {
		t.Fatal(err)
	}
	for len(d.forgottenNodes) < 1024 {
		copy := saved
		copy.ID = strings.ReplaceAll(newID(), "-", "")
		copy.Credentials, err = d.sealSecrets(copy, secret)
		if err != nil {
			t.Fatal(err)
		}
		d.forgottenNodes[copy.ID] = copy
	}
	revokeFleetFixtureAccount(d, secret.Removal.ActorID)
	owner = dashboardPasswordLogin(t, d.accounts, "owner", dashboardPasswordFixture)
	forgetNodeFixture(t, d, owner, reg)
	renewed, err := d.openSecrets(d.forgottenNodes[reg.ID])
	if err != nil || renewed.Removal.ActorVersion == secret.Removal.ActorVersion || len(d.forgottenNodes) != 1024 {
		t.Fatal("existing revocation could not renew authority at capacity", err)
	}
	// The cap still refuses a different, valid queued registration.
	otherNode := dashboardRemoteFixture(t, "capacity-other-node")
	otherServer := httptest.NewServer(otherNode)
	defer otherServer.Close()
	other := enrollDashboardNode(t, d, owner, otherServer.URL)
	forceNodeFixture(t, d, owner, other)
	w := dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+other.ID, owner, "", map[string]any{"force": true, "forget": true, "confirmation": other.Label})
	if w.Code != 409 || len(d.forgottenNodes) != 1024 {
		t.Fatal("capacity admitted another job", w.Code, w.Body.String())
	}
}

func TestForgetDetachedRevocationCanBeDiscoveredAndReauthorized(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "reauthorize-node")
	server := httptest.NewServer(node)
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	forceNodeFixture(t, d, owner, reg)
	forgetNodeFixture(t, d, owner, reg)
	list := func(token string) []pendingNodeRevocation {
		t.Helper()
		w := dashboardHTTP(d, "GET", "/api/dashboard/node-revocations", token, "", nil)
		var result []pendingNodeRevocation
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal("revocations unavailable", w.Code, w.Body.String())
		}
		// Even administrators receive no addresses, credentials or archived commands.
		var raw []map[string]any
		if json.Unmarshal(w.Body.Bytes(), &raw) != nil {
			t.Fatal("invalid list")
		}
		for _, item := range raw {
			if len(item) != 4 || item["id"] == nil || item["label"] == nil || item["nodeId"] == nil || item["authorizationRequired"] == nil {
				t.Fatal("private journal fields exposed", item)
			}
		}
		return result
	}
	if len(list(owner)) != 0 {
		t.Fatal("queued registration duplicated in detached list")
	}
	if err := d.finishForgottenNode(context.Background(), reg.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.registry[reg.ID]; ok {
		t.Fatal("local registration did not detach")
	}
	before := list(owner)
	if len(before) != 1 || before[0].ID != reg.ID || before[0].Label != reg.Label || before[0].NodeID != node.ID || before[0].AuthorizationRequired {
		t.Fatal("detached cleanup was undiscoverable", before)
	}
	secret, err := d.openSecrets(d.forgottenNodes[reg.ID])
	if err != nil {
		t.Fatal(err)
	}
	revokeFleetFixtureAccount(d, secret.Removal.ActorID)
	if err := d.finishForgottenNode(context.Background(), reg.ID); err == nil || len(node.accounts) != 4 {
		t.Fatal("revocation ignored changed authority")
	}
	owner = dashboardPasswordLogin(t, d.accounts, "owner", dashboardPasswordFixture)
	if current := list(owner); len(current) != 1 || !current[0].AuthorizationRequired {
		t.Fatal("renewal need was hidden", current)
	}
	if w := dashboardHTTP(d, "GET", "/api/dashboard/node-revocations", "", "", nil); w.Code != 401 {
		t.Fatal("anonymous revocation list", w.Code)
	}
	if w := dashboardHTTP(d, "POST", "/api/accounts", owner, "", map[string]string{"name": "revocation-reader", "role": "viewer", "password": dashboardPasswordFixture}); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	viewer := dashboardPasswordLogin(t, d.accounts, "revocation-reader", dashboardPasswordFixture)
	if w := dashboardHTTP(d, "GET", "/api/dashboard/node-revocations", viewer, "", nil); w.Code != 403 {
		t.Fatal("non-administrator discovered private revocations", w.Code)
	}
	if w := dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+reg.ID, owner, "", map[string]any{"force": true, "forget": true, "confirmation": "wrong name"}); w.Code != 409 {
		t.Fatal("renewal skipped exact name", w.Code)
	}
	forgetNodeFixture(t, d, owner, reg)
	if current := list(owner); len(current) != 1 || current[0].AuthorizationRequired {
		t.Fatal("renewal did not resume authority", current)
	}
	if err := d.finishForgottenNode(context.Background(), reg.ID); err != nil {
		t.Fatal("reauthorized DELETE failed", err)
	}
	if len(node.accounts) != 1 || len(list(owner)) != 0 {
		t.Fatal("confirmed revocation did not erase credentials")
	}
	if _, ok, err := loadOne[registeredNodeRecord](d.accounts.node.store, "dashboardForgottenNodes", reg.ID); err != nil || ok {
		t.Fatal("durable revocation journal survived confirmation", err)
	}
}

func TestForgetLegacyUnsentReplayCanRetryWithCompletePayload(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var creates, unsupported atomic.Int32
	var refuse atomic.Bool
	refuse.Store(true)
	sa, sb := httptest.NewServer(a), httptest.NewServer(preFleetFixture(b, &creates, &unsupported, nil, &refuse))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	fleet := forgetFleetFixture(t, d, owner, []string{ra.ID, rb.ID}, nil)
	row := d.fleet[fleet.ID]
	plan, _ := d.openFleet(row)
	if err := d.prepareFleet(context.Background(), &row, &plan); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	err := d.saveFleet(nil, AccountView{}, row, plan)
	d.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	forceNodeFixture(t, d, owner, ra)
	forgetNodeFixture(t, d, owner, ra)
	if err := d.finishForgottenNode(context.Background(), ra.ID); err != nil {
		t.Fatal(err)
	}
	plan, _ = d.openFleet(d.fleet[fleet.ID])
	if !plan.RetargetAfterReplay {
		t.Fatal("fixture did not retain unsent replay")
	}
	d.finishFleet(context.Background(), fleet.ID)
	row = d.fleet[fleet.ID]
	plan, _ = d.openFleet(row)
	if row.State != "blocked" || plan.Prepared || plan.RetargetAfterReplay {
		t.Fatal("safe legacy reset left replay active", row.State, row.Reason)
	}
	refuse.Store(false)
	w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors/"+row.ID+"/retry", owner, "", map[string]any{"revision": row.Revision})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	for range 3 {
		d.finishFleet(context.Background(), row.ID)
	}
	if d.fleet[row.ID].State != "applied" || len(b.heads) != 1 || creates.Load() != 2 {
		t.Fatal("legacy retry lost its monitor payload", d.fleet[row.ID].Reason)
	}
}

func TestForgetSecondDetachmentPreservesDeferredResourceScope(t *testing.T) {
	for _, acknowledged := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "acknowledged"}[acknowledged], func(t *testing.T) {
			d, owner, _ := dashboardRegistryFixture(t)
			a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
			sa, sb := httptest.NewServer(a), httptest.NewServer(b)
			defer sa.Close()
			defer sb.Close()
			ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
			fleet := forgetFleetFixture(t, d, owner, []string{ra.ID, rb.ID}, nil)
			d.finishFleet(context.Background(), fleet.ID)
			row := d.fleet[fleet.ID]
			plan, _ := d.openFleet(row)
			for _, p := range row.Placements {
				body, err := fleetCommandBytes(plan, p)
				if err != nil {
					t.Fatal(err)
				}
				var command fleetPlacementCommand
				if err := json.Unmarshal(body, &command); err != nil {
					t.Fatal(err)
				}
				plan.Commands[p.ID] = command
			}
			// A prior detachment deferred this scope while an unrelated write resolves.
			plan.RetargetAfterReplay = true
			plan.ForgetResourceTargets = []string{"only-new-target"}
			plan.Resources = []fleetResourceInput{{Kind: resourceChannel, ID: "frozen-channel"}}
			if !acknowledged {
				for i := range row.Placements {
					if row.Placements[i].RegistrationID == rb.ID {
						row.Placements[i].State = "pending"
					}
				}
			}
			row.State = "pending"
			d.mu.Lock()
			err := d.saveFleet(nil, AccountView{}, row, plan)
			d.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			forceNodeFixture(t, d, owner, ra)
			forgetNodeFixture(t, d, owner, ra)
			if err := d.finishForgottenNode(context.Background(), ra.ID); err != nil {
				t.Fatal(err)
			}
			row = d.fleet[fleet.ID]
			plan, _ = d.openFleet(row)
			if plan.RetargetAfterReplay {
				resumeForgetRetarget(&row, &plan)
			}
			if !slices.Equal(plan.RemovalResourceTargets, []string{"only-new-target"}) || len(fleetResourcesForPlacement(plan, row.Placements[0])) != 0 {
				t.Fatal("second removal broadened frozen settings installation", plan.RemovalResourceTargets)
			}
		})
	}
}

func TestForgetInvalidatesInFlightRefreshForChangedGroups(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var hold atomic.Bool
	var once, releaseOnce sync.Once
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	sa := httptest.NewServer(a)
	defer sa.Close()
	sb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hold.Load() && r.Method == "GET" && r.URL.Path == "/api/overview" {
			once.Do(func() { close(entered) })
			<-release
		}
		b.ServeHTTP(w, r)
	}))
	defer sb.Close()
	defer unblock()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	fleet := forgetFleetFixture(t, d, owner, []string{rb.ID}, nil)
	d.finishFleet(context.Background(), fleet.ID)
	group := groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	row := d.fleet[fleet.ID]
	plan, _ := d.openFleet(row)
	row.GroupIDs = []string{group.ID}
	channel := groupChannelFixture(t, b)
	plan.Monitor.NotificationIDs = []string{channel.ID}
	plan.SyncNotifications, plan.SyncNotificationsSet, plan.NotificationSourceRegistrationID = true, true, rb.ID
	d.mu.Lock()
	err := d.saveFleet(nil, AccountView{}, row, plan)
	d.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	before := d.fleet[row.ID].Revision
	forceNodeFixture(t, d, owner, ra)
	forgetNodeFixture(t, d, owner, ra)
	hold.Store(true)
	go func() { d.refreshGroupPlacements(context.Background()); close(done) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not reach its saved-target read")
	}
	if err := d.finishForgottenNode(context.Background(), ra.ID); err != nil {
		t.Fatal(err)
	}
	unblock()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh failed to finish")
	}
	current := d.fleet[row.ID]
	if current.Revision == before || slices.Contains(current.RegistrationIDs, ra.ID) {
		t.Fatal("stale group refresh restored forgotten registration")
	}
}

func TestForgetHandoffsRetainDeletionAndRetiringPeerCleanup(t *testing.T) {
	for _, mode := range []string{"delete-before-handoff", "retiring-peer", "lost-adoption-ack"} {
		t.Run(mode, func(t *testing.T) {
			d, owner, _ := dashboardRegistryFixture(t)
			a, b := causalCapacityNode(t, "a", ":memory:"), causalCapacityNode(t, "b", ":memory:")
			a.peerSeen[b.ID], b.peerSeen[a.ID] = 1, 1
			var offlineA, offlineB, loseAdoption atomic.Bool
			serve := func(n *Node, offline *atomic.Bool) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if offline.Load() {
						http.Error(w, "offline", 503)
						return
					}
					if r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/fleet-placements/") && loseAdoption.Swap(false) {
						recorder := httptest.NewRecorder()
						n.ServeHTTP(recorder, r)
						if recorder.Code != 200 && recorder.Code != 201 {
							t.Error("fixture adoption failed", recorder.Code, recorder.Body.String())
						}
						w.Header().Set("X-Canary-Node-Id", n.ID)
						errorReply(w, fail(503, "fixture lost adoption acknowledgement"))
						return
					}
					n.ServeHTTP(w, r)
				})
			}
			sa, sb := httptest.NewServer(serve(a, &offlineA)), httptest.NewServer(serve(b, &offlineB))
			defer sa.Close()
			defer sb.Close()
			ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
			replicate := func() {
				for _, pair := range [][2]*Node{{a, b}, {b, a}} {
					for _, page := range causalCapacityPages(t, pair[0]) {
						if err := pair[1].applySnapshot(page); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			fleet := forgetFleetFixture(t, d, owner, []string{ra.ID, rb.ID}, nil)
			for range 4 {
				d.finishFleet(context.Background(), fleet.ID)
				replicate()
			}
			row := d.fleet[fleet.ID]
			if row.State != "applied" || len(row.Placements) != 1 {
				t.Fatal("HA fixture not applied", row.Reason)
			}
			anchor, survivor := ra, rb
			remaining := b
			if row.Placements[0].RegistrationID == rb.ID {
				anchor, survivor, remaining = rb, ra, a
			}
			if mode == "retiring-peer" {
				w := dashboardHTTP(d, "DELETE", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision})
				if w.Code != 202 {
					t.Fatal(w.Code, w.Body.String())
				}
				retiring := d.fleet[row.ID]
				plan, _ := d.openFleet(retiring)
				if err := d.prepareFleet(context.Background(), &retiring, &plan); err != nil {
					t.Fatal(err)
				}
				d.mu.Lock()
				err := d.saveFleet(nil, AccountView{}, retiring, plan)
				d.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			forceNodeFixture(t, d, owner, anchor)
			if anchor.ID == ra.ID {
				offlineA.Store(true)
			} else {
				offlineB.Store(true)
			}
			_, _ = d.finishEnrollment(context.Background(), anchor.ID)
			forgetNodeFixture(t, d, owner, anchor)
			if err := d.finishForgottenNode(context.Background(), anchor.ID); err != nil {
				t.Fatal(err)
			}

			if mode != "retiring-peer" {
				pending := d.fleet[row.ID]
				w := dashboardHTTP(d, "DELETE", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": pending.Revision})
				if w.Code != 202 {
					t.Fatal("existing physical handoff was cancelled without deleting its monitor", w.Code, w.Body.String())
				}
			}
			loseAdoption.Store(mode == "lost-adoption-ack")
			d.finishFleet(context.Background(), row.ID)
			if mode == "lost-adoption-ack" {
				pending := d.fleet[row.ID]
				plan, _ := d.openFleet(pending)
				before, err := fleetCommandBytes(plan, pending.Placements[0])
				if err != nil || !plan.Prepared || pending.Placements[0].Sequence != 0 {
					t.Fatal("lost adoption was not journaled", err)
				}
				d.Close()
				restarted, err := NewDashboard(DashboardOptions{Accounts: d.accounts, Key: dashboardKeyFixture})
				if err != nil {
					t.Fatal(err)
				}
				defer restarted.Close()
				d = restarted
				plan, _ = d.openFleet(d.fleet[row.ID])
				after, err := fleetCommandBytes(plan, d.fleet[row.ID].Placements[0])
				if err != nil || string(before) != string(after) {
					t.Fatal("restart changed the saved adoption", err)
				}
				d.finishFleet(context.Background(), row.ID)
			}
			adopted := d.fleet[row.ID]
			m := remaining.current(row.Placements[0].MonitorID)
			if adopted.State != "pending" || len(adopted.Placements) != 1 || adopted.Placements[0].Sequence != 1 || m == nil || m.Deleted {
				t.Fatal("handoff did not confirm before retirement", adopted.State, adopted.Reason)
			}
			for range 4 {
				d.finishFleet(context.Background(), row.ID)
			}
			m = remaining.current(row.Placements[0].MonitorID)
			if d.fleet[row.ID].State != "removed" || m == nil || !m.Deleted || len(remaining.heads) != 1 {
				t.Fatal("survivor monitor survived deletion or retiring cleanup", d.fleet[row.ID].State, d.fleet[row.ID].Reason)
			}
			if _, ok := d.registry[survivor.ID]; !ok {
				t.Fatal("survivor registration was removed")
			}
		})
	}
}
