package canary

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func groupSyncFixture(t *testing.T, d *Dashboard, token string, ids []string) dashboardNodeGroup {
	t.Helper()
	w := dashboardHTTP(d, "POST", "/api/dashboard/node-groups", token, "", map[string]any{"name": "Every location", "registrationIds": ids})
	var group dashboardNodeGroup
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &group) != nil {
		t.Fatalf("create group: %d %s", w.Code, w.Body.String())
	}
	return group
}
func finishGroupFixture(t *testing.T, d *Dashboard) {
	t.Helper()
	d.runGroupSyncs(context.Background())
	for id := range d.fleet {
		d.finishFleet(context.Background(), id)
	}
}
func groupMonitorFixture(t *testing.T, n *Node, in MonitorInput) Monitor {
	t.Helper()
	w := dashboardHTTP(n, "POST", "/api/monitors", adminToken, "", in)
	var monitor Monitor
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &monitor) != nil {
		t.Fatalf("fixture monitor: %d %s", w.Code, w.Body.String())
	}
	return monitor
}
func groupChannelFixture(t *testing.T, n *Node) Channel {
	t.Helper()
	w := dashboardHTTP(n, "POST", "/api/notification-channels", adminToken, "", hookChannel("Operations", "http://127.0.0.1:9/never-send"))
	var channel Channel
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &channel) != nil {
		t.Fatalf("fixture channel: %d %s", w.Code, w.Body.String())
	}
	return channel
}
func TestGroupSyncCollectsBothDirectionsAndCopiesChannels(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	sa, sb := httptest.NewServer(a), httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	channel := groupChannelFixture(t, a)
	in := placementFixtureInput()
	in.NotificationIDs = []string{channel.ID}
	in.Name = "Same name"
	first := groupMonitorFixture(t, a, in)
	in.NotificationIDs = nil
	in.NodeIDs = []string{"node-b"}
	second := groupMonitorFixture(t, b, in)
	group := groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	finishGroupFixture(t, d)
	if len(d.fleet) != 2 || len(a.heads) != 2 || len(b.heads) != 2 {
		t.Fatalf("union lost or duplicated monitors: %d %d %d", len(d.fleet), len(a.heads), len(b.heads))
	}
	if a.current(first.ID) == nil || b.current(second.ID) == nil {
		t.Fatal("adoption replaced an original monitor identity")
	}
	if len(a.channelViews("admin")) != 1 {
		t.Fatal("sync duplicated the source channel")
	}
	for _, row := range d.fleet {
		if row.State != "applied" || !slices.Contains(row.GroupIDs, group.ID) {
			t.Fatalf("group deployment: %s %s", row.State, row.Reason)
		}
		if row.Source.MonitorID == first.ID {
			for _, p := range row.Placements {
				node := a
				if p.RegistrationID == rb.ID {
					node = b
				}
				m := node.current(p.MonitorID)
				if len(m.NotificationIDs) != 1 {
					t.Fatal("notification selection lost")
				}
				resource := node.currentResource(resourceChannel, m.NotificationIDs[0])
				if resource == nil || channelOf(*resource, "admin").Config.Webhook.BearerToken != "fixture-only-bearer" {
					t.Fatal("notification settings did not reach a group member")
				}
			}
		}
	}
	w := dashboardHTTP(d, "POST", "/api/dashboard/node-groups/"+group.ID+"/sync", owner, "", map[string]string{"revision": group.Revision})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	finishGroupFixture(t, d)
	if len(d.fleet) != 2 || len(a.heads) != 2 || len(b.heads) != 2 {
		t.Fatal("repeated group sync duplicated monitors")
	}
	public := dashboardHTTP(d, "GET", "/api/dashboard/fleet/monitors", owner, "", nil)
	if strings.Contains(public.Body.String(), "fixture-only-bearer") {
		t.Fatal("resource credentials leaked through public fleet metadata")
	}
}

func TestGroupMonitorFollowsMembershipAndSourceChannelChanges(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	sa, sb := httptest.NewServer(a), httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	channel := groupChannelFixture(t, a)
	group := groupSyncFixture(t, d, owner, []string{ra.ID})
	in := placementFixtureInput()
	in.NotificationIDs = []string{channel.ID}
	w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{"requestId": placementFixtureID, "groupIds": []string{group.ID}, "registrationIds": []string{}, "notificationSourceRegistrationId": ra.ID, "syncNotifications": true, "monitor": in})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), placementFixtureID)
	if row := d.fleet[placementFixtureID]; row.State != "applied" {
		t.Fatal(row.State, row.Reason)
	}
	w = dashboardHTTP(d, "PUT", "/api/dashboard/node-groups/"+group.ID, owner, "", map[string]any{"name": group.Name, "revision": group.Revision, "registrationIds": []string{ra.ID, rb.ID}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.refreshGroupPlacements(context.Background())
	d.finishFleet(context.Background(), placementFixtureID)
	row := d.fleet[placementFixtureID]
	if row.State != "applied" || len(row.RegistrationIDs) != 2 || len(b.heads) != 1 {
		t.Fatalf("new group member missed monitor: %s %s", row.State, row.Reason)
	}
	changed := channel.ChannelInput
	changed.Config.Webhook.BearerToken = "updated-fixture-only-bearer"
	if _, err := a.writeResource(resourceChannel, channel.ID, changed, false); err != nil {
		t.Fatal(err)
	}
	row.RetryAt = 0
	plan, _ := d.openFleet(row)
	if err := d.saveFleet(nil, AccountView{}, row, plan); err != nil {
		t.Fatal(err)
	}
	d.refreshGroupPlacements(context.Background())
	d.finishFleet(context.Background(), placementFixtureID)
	row = d.fleet[placementFixtureID]
	if row.State != "applied" {
		t.Fatal(row.State, row.Reason)
	}
	for _, p := range row.Placements {
		if p.RegistrationID == rb.ID {
			m := b.current(p.MonitorID)
			r := b.currentResource(resourceChannel, m.NotificationIDs[0])
			if channelOf(*r, "admin").Config.Webhook.BearerToken != "updated-fixture-only-bearer" {
				t.Fatal("source edit did not sync automatically")
			}
		}
	}
}

func TestFleetBundledChannelsDoNotOverwriteNodeEdits(t *testing.T) {
	n := placementFixtureNode(t, ":memory:")
	in := placementFixtureInput()
	in.NotificationIDs = []string{"fleet-" + placementFixtureID}
	data, _ := json.Marshal(hookChannel("Operations", "http://127.0.0.1:9/never-send"))
	command := placementFixtureCommand(t, 1, "", false)
	command.Monitor, _ = json.Marshal(in)
	command.Resources = []fleetResourceInput{{ID: in.NotificationIDs[0], Kind: resourceChannel, Data: data}}
	result := placementFixtureWrite(t, n, command, 201)
	local := hookChannel("Local edit", "http://127.0.0.1:9/never-send")
	if _, err := n.writeResource(resourceChannel, in.NotificationIDs[0], local, false); err != nil {
		t.Fatal(err)
	}
	command.Sequence = 2
	command.Revision = result.Revision
	command.Resources[0].Data, _ = json.Marshal(hookChannel("Source changed", "http://127.0.0.1:9/never-send"))
	placementFixtureWrite(t, n, command, 409)
	r := n.currentResource(resourceChannel, in.NotificationIDs[0])
	if channelOf(*r, "admin").Name != "Local edit" {
		t.Fatal("local edit was silently overwritten")
	}
}

func TestGroupSyncPreservesPinsAndExplicitNotificationSettings(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b, c := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b"), dashboardRemoteFixture(t, "node-c")
	sa, sb, sc := httptest.NewServer(a), httptest.NewServer(b), httptest.NewServer(c)
	defer sa.Close()
	defer sb.Close()
	defer sc.Close()
	ra, rb, rc := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL), enrollDashboardNode(t, d, owner, sc.URL)
	row := fleetCreateFixture(t, d, owner, []string{ra.ID, rc.ID}, nil)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	explicitPlan, _ := d.openFleet(row)
	explicit := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision, "registrationIds": row.RegistrationIDs, "monitor": explicitPlan.Monitor, "syncNotifications": false})
	if explicit.Code != 202 {
		t.Fatal(explicit.Code, explicit.Body.String())
	}
	d.finishFleet(context.Background(), row.ID)
	group := groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	finishGroupFixture(t, d)
	row = d.fleet[row.ID]
	plan, _ := d.openFleet(row)
	if row.State != "applied" || !slices.Contains(row.PinnedRegistrationIDs, rc.ID) || plan.SyncNotifications {
		t.Fatal("group adoption dropped pins or enabled explicitly disabled copying")
	}
	w := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision, "groupIds": row.GroupIDs, "registrationIds": []string{ra.ID, rb.ID}, "monitor": plan.Monitor, "syncNotifications": false})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if !slices.Contains(row.RegistrationIDs, rc.ID) || !slices.Contains(row.PinnedRegistrationIDs, rc.ID) {
		t.Fatal("ordinary group monitor edit retired a pinned runner")
	}
	w = dashboardHTTP(d, "PUT", "/api/dashboard/node-groups/"+group.ID, owner, "", map[string]any{"name": group.Name, "revision": group.Revision, "registrationIds": []string{ra.ID}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &group)
	d.refreshGroupPlacements(context.Background())
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "applied" || slices.Contains(row.RegistrationIDs, rb.ID) || !slices.Contains(row.RegistrationIDs, rc.ID) {
		t.Fatal("membership update did not distinguish group members and original pins")
	}
	w = dashboardHTTP(d, "DELETE", "/api/dashboard/node-groups/"+group.ID, owner, "", map[string]string{"revision": group.Revision})
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	row = d.fleet[row.ID]
	if len(row.GroupIDs) != 0 || len(row.RegistrationIDs) != 2 || row.State != "applied" {
		t.Fatal("group removal changed deployed monitors")
	}
}

func TestLegacyManagedMonitorAdoptsAutomaticCopyingOnGroupSync(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	sa, sb := httptest.NewServer(a), httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	channel := groupChannelFixture(t, a)
	in := placementFixtureInput()
	in.NotificationIDs = []string{channel.ID}
	w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{"requestId": placementFixtureID, "registrationIds": []string{ra.ID}, "monitor": in})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), placementFixtureID)
	public := dashboardHTTP(d, "GET", "/api/dashboard/fleet/monitors", owner, "", nil)
	if strings.Contains(public.Body.String(), "syncNotifications") {
		t.Fatal("a legacy absent preference was presented as an explicit Off choice")
	}
	group := groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	finishGroupFixture(t, d)
	row := d.fleet[placementFixtureID]
	plan, _ := d.openFleet(row)
	if row.State != "applied" || !plan.SyncNotifications || !plan.SyncNotificationsSet || plan.NotificationSourceRegistrationID != ra.ID {
		t.Fatalf("legacy monitor did not adopt automatic copying: %s %s", row.State, row.Reason)
	}
	for _, p := range row.Placements {
		if p.RegistrationID == rb.ID {
			monitor := b.current(p.MonitorID)
			if len(monitor.NotificationIDs) != 1 || b.currentResource(resourceChannel, monitor.NotificationIDs[0]) == nil {
				t.Fatal("legacy monitor's alerts did not reach the new group member")
			}
		}
	}
	// Older API clients omitting the new optional fields retain the saved group
	// and copying preference; an explicit [] or false remains a deliberate change.
	w = dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision, "registrationIds": row.RegistrationIDs, "monitor": plan.Monitor})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), row.ID)
	latest := d.fleet[row.ID]
	saved, _ := d.openFleet(latest)
	if !sameFleetIDs(latest.GroupIDs, row.GroupIDs) || !saved.SyncNotifications || !saved.SyncNotificationsSet {
		t.Fatal("an API edit erased the saved automatic group settings")
	}
	// An old preflight may already be blocked on the exact missing-channel case
	// that group copying fixes. No command was prepared or sent, so adoption can
	// upgrade that default without discarding an unknown write.
	in.Name = "Legacy adoption with a missing destination channel"
	native := groupMonitorFixture(t, a, in)
	blockedID := strings.Repeat("e", 32)
	w = dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{"requestId": blockedID, "registrationIds": []string{ra.ID, rb.ID}, "monitor": in, "source": fleetSourceReference{RegistrationID: ra.ID, MonitorID: native.ID, Revision: native.Revision}})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), blockedID)
	blocked := d.fleet[blockedID]
	blockedPlan, _ := d.openFleet(blocked)
	if blocked.State != "blocked" || blockedPlan.Prepared {
		t.Fatal("legacy missing-channel fixture did not preserve an unsent blocked plan")
	}
	w = dashboardHTTP(d, "POST", "/api/dashboard/node-groups/"+group.ID+"/sync", owner, "", map[string]string{"revision": group.Revision})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	finishGroupFixture(t, d)
	if row := d.fleet[blockedID]; row.State != "applied" {
		t.Fatalf("group sync could not upgrade a blocked legacy preflight: %s %s", row.State, row.Reason)
	}
}

func TestGroupSyncRequestFencesOldJobAndRechecksAuthorization(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(node)
	defer server.Close()
	registration := enrollDashboardNode(t, d, owner, server.URL)
	group := groupSyncFixture(t, d, owner, []string{registration.ID})
	old, _, _ := loadOne[dashboardGroupSync](d.accounts.node.store, "dashboardGroupSyncs", group.ID)
	w := dashboardHTTP(d, "POST", "/api/dashboard/node-groups/"+group.ID+"/sync", owner, "", map[string]string{"revision": group.Revision})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.finishGroupSync(group, old, "applied", "")
	newJob, exists, _ := loadOne[dashboardGroupSync](d.accounts.node.store, "dashboardGroupSyncs", group.ID)
	if !exists || newJob.Revision == old.Revision {
		t.Fatal("old completion consumed a newer sync request")
	}
	revokeFleetFixtureAccount(d, newJob.ActorID)
	d.runGroupSyncs(context.Background())
	current, _, _ := loadOne[dashboardNodeGroup](d.accounts.node.store, "dashboardNodeGroups", group.ID)
	if current.SyncState != "blocked" || len(d.fleet) != 0 {
		t.Fatal("group sync used revoked account authority")
	}
}

func TestFleetResourcesAndMonitorRollbackTogether(t *testing.T) {
	n := placementFixtureNode(t, ":memory:")
	in := placementFixtureInput()
	in.NotificationIDs = []string{"fleet-" + placementFixtureID}
	command := placementFixtureCommand(t, 1, "", false)
	command.Monitor, _ = json.Marshal(in)
	data, _ := json.Marshal(hookChannel("Operations", "http://127.0.0.1:9/never-send"))
	command.Resources = []fleetResourceInput{{ID: in.NotificationIDs[0], Kind: resourceChannel, Data: data}}
	if _, err := n.store.db.Exec(`CREATE TRIGGER resource_failure BEFORE INSERT ON records WHEN NEW.category='fleetResourceReceipts' BEGIN SELECT RAISE(ABORT,'private rollback fixture'); END`); err != nil {
		t.Fatal(err)
	}
	placementFixtureWrite(t, n, command, 503)
	if len(n.heads) != 0 || n.currentResource(resourceChannel, in.NotificationIDs[0]) != nil {
		t.Fatal("failed resource receipt commit published a monitor or credentials")
	}
}

func TestNodeGroupRejectsDatabaseMembershipAndAuthorityEdits(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	sa, sb := httptest.NewServer(a), httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	group := groupSyncFixture(t, d, owner, []string{ra.ID})
	stored, _, _ := loadOne[dashboardNodeGroup](d.accounts.node.store, "dashboardNodeGroups", group.ID)
	job, _, _ := loadOne[dashboardGroupSync](d.accounts.node.store, "dashboardGroupSyncs", group.ID)
	stored.RegistrationIDs = append(stored.RegistrationIDs, rb.ID)
	if err := put(d.accounts.node.store.db, "dashboardNodeGroups", group.ID, stored); err != nil {
		t.Fatal(err)
	}
	d.runGroupSyncs(context.Background())
	d.mu.Lock()
	_, err := d.groupTargetsLocked([]string{group.ID})
	d.mu.Unlock()
	if err == nil || len(d.fleet) != 0 {
		t.Fatal("database edit rerouted a group or started a deployment")
	}
	stored.RegistrationIDs = []string{ra.ID}
	if err := put(d.accounts.node.store.db, "dashboardNodeGroups", group.ID, stored); err != nil {
		t.Fatal(err)
	}
	otherResponse := dashboardHTTP(d, "POST", "/api/dashboard/node-groups", owner, "", map[string]any{"name": "Another group", "registrationIds": []string{rb.ID}})
	if otherResponse.Code != 200 {
		t.Fatal(otherResponse.Code)
	}
	var other dashboardNodeGroup
	_ = json.Unmarshal(otherResponse.Body.Bytes(), &other)
	other, _, _ = loadOne[dashboardNodeGroup](d.accounts.node.store, "dashboardNodeGroups", other.ID)
	if err := put(d.accounts.node.store.db, "dashboardNodeGroups", group.ID, other); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	_, err = d.groupTargetsLocked([]string{group.ID})
	d.mu.Unlock()
	if err == nil {
		t.Fatal("a valid group signature was accepted under another storage identity")
	}
	if err := put(d.accounts.node.store.db, "dashboardNodeGroups", group.ID, stored); err != nil {
		t.Fatal(err)
	}
	job.ActorVersion++
	if err := put(d.accounts.node.store.db, "dashboardGroupSyncs", group.ID, job); err != nil {
		t.Fatal(err)
	}
	d.runGroupSyncs(context.Background())
	if len(d.fleet) != 0 {
		t.Fatal("database edit manufactured worker authority")
	}
	response := dashboardHTTP(d, "GET", "/api/dashboard/node-groups", owner, "", nil)
	if strings.Contains(response.Body.String(), "proof") {
		t.Fatal("internal group proof leaked into a public response")
	}
}

func TestGroupChannelCopiedFromAnotherMemberKeepsItsOriginalSource(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	sa, sb := httptest.NewServer(a), httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	channel := groupChannelFixture(t, a)
	in := placementFixtureInput()
	in.NotificationIDs = []string{channel.ID}
	group := groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	makeDefinition := func(id, source string, monitor MonitorInput) {
		t.Helper()
		w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{"requestId": id, "groupIds": []string{group.ID}, "registrationIds": []string{}, "notificationSourceRegistrationId": source, "syncNotifications": true, "monitor": monitor})
		if w.Code != 202 {
			t.Fatal(w.Code, w.Body.String())
		}
		d.finishFleet(context.Background(), id)
		if row := d.fleet[id]; row.State != "applied" {
			t.Fatal(row.State, row.Reason)
		}
	}
	makeDefinition(placementFixtureID, ra.ID, in)
	var copyID string
	for _, p := range d.fleet[placementFixtureID].Placements {
		if p.RegistrationID == rb.ID {
			copyID = b.current(p.MonitorID).NotificationIDs[0]
		}
	}
	in.Name = "Created from another group member"
	in.NotificationIDs = []string{copyID}
	second := strings.Repeat("f", 32)
	makeDefinition(second, rb.ID, in)
	if len(a.channelViews("admin")) != 1 {
		t.Fatal("a copied default channel created a duplicate on its original source")
	}
	updated := channel.ChannelInput
	updated.Config.Webhook.BearerToken = "changed-original-fixture-bearer"
	if _, err := a.writeResource(resourceChannel, channel.ID, updated, false); err != nil {
		t.Fatal(err)
	}
	d.refreshGroupPlacements(context.Background())
	for _, id := range []string{placementFixtureID, second} {
		d.finishFleet(context.Background(), id)
		if row := d.fleet[id]; row.State != "applied" {
			t.Fatal(row.State, row.Reason)
		}
	}
	for _, row := range d.fleet {
		for _, p := range row.Placements {
			node := a
			if p.RegistrationID == rb.ID {
				node = b
			}
			m := node.current(p.MonitorID)
			resource := node.currentResource(resourceChannel, m.NotificationIDs[0])
			if resource == nil || channelOf(*resource, "admin").Config.Webhook.BearerToken != "changed-original-fixture-bearer" {
				t.Fatal("an older destination copy replaced the original channel edit")
			}
		}
	}
}

func TestLegacyNodeGroupMigrationCannotResignActiveMembership(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(node)
	defer server.Close()
	registration := enrollDashboardNode(t, d, owner, server.URL)
	group := groupSyncFixture(t, d, owner, []string{registration.ID})
	group.Proof = ""
	group.SyncState = ""
	if err := put(d.accounts.node.store.db, "dashboardNodeGroups", group.ID, group); err != nil {
		t.Fatal(err)
	}
	if err := remove(d.accounts.node.store.db, "dashboardGroupSyncs", group.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.loadGroupAuthority(); err != nil {
		t.Fatal("legacy inert selection did not migrate", err)
	}
	signed, _, _ := loadOne[dashboardNodeGroup](d.accounts.node.store, "dashboardNodeGroups", group.ID)
	if d.verifyNodeGroup(signed) != nil {
		t.Fatal("legacy migration did not authenticate membership")
	}
	row := fleetCreateFixture(t, d, owner, []string{registration.ID}, nil)
	plan, _ := d.openFleet(row)
	row.GroupIDs = []string{group.ID}
	if err := d.saveFleet(nil, AccountView{}, row, plan); err != nil {
		t.Fatal(err)
	}
	signed.Proof = ""
	if err := put(d.accounts.node.store.db, "dashboardNodeGroups", group.ID, signed); err != nil {
		t.Fatal(err)
	}
	if d.loadGroupAuthority() == nil {
		t.Fatal("removing an active membership proof bypassed authentication through migration")
	}
}
