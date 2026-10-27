package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGroupNativeAdoptionPreservesOriginalOutsideRunner(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, c := causalCapacityNode(t, "a", ":memory:"), causalCapacityNode(t, "b", ":memory:")
	b := dashboardRemoteFixture(t, "independent-b")
	a.peerSeen[c.ID], c.peerSeen[a.ID] = 1, 1
	in := input([]string{a.ID, c.ID}, "http://127.0.0.1:9", "")
	in.Enabled = false
	native, err := a.writeMonitor(in, nil, "outside-native-runner", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range causalCapacityPages(t, a) {
		if err = c.applySnapshot(page); err != nil {
			t.Fatal(err)
		}
	}
	sa, sb, sc := httptest.NewServer(a), httptest.NewServer(b), httptest.NewServer(c)
	defer sa.Close()
	defer sb.Close()
	defer sc.Close()
	ra, rb, rc := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL), enrollDashboardNode(t, d, owner, sc.URL)
	groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	finishGroupFixture(t, d)
	var row dashboardFleetRecord
	for _, definition := range d.fleet {
		if definition.Source != nil && definition.Source.MonitorID == native.ID {
			row = definition
		}
	}
	if !slices.Contains(row.PinnedRegistrationIDs, rc.ID) || !slices.Contains(row.RegistrationIDs, rc.ID) {
		t.Fatal("adoption removed an original runner outside the group")
	}
	for _, page := range causalCapacityPages(t, a) {
		if err = c.applySnapshot(page); err != nil {
			t.Fatal(err)
		}
	}
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "applied" {
		t.Fatal(row.State, row.Reason)
	}
	if got := c.current(native.ID); got == nil || !slices.Contains(got.NodeIDs, c.ID) {
		t.Fatal("the original outside runner stopped checking its preserved monitor")
	}
}

func TestFleetManualToGroupEditPreservesOutsideAssignment(t *testing.T) {
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
	plan, _ := d.openFleet(row)
	group := groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	w := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision, "groupIds": []string{group.ID}, "registrationIds": group.RegistrationIDs, "monitor": plan.Monitor})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	row = d.fleet[row.ID]
	if !slices.Contains(row.PinnedRegistrationIDs, rc.ID) || !slices.Contains(row.RegistrationIDs, rc.ID) {
		t.Fatal("an edit from individual nodes to a group removed its outside assignment")
	}
	d.finishFleet(context.Background(), row.ID)
	if row = d.fleet[row.ID]; row.State != "applied" {
		t.Fatal(row.State, row.Reason)
	}
}

func TestGroupCopiedChannelRetainsOriginAfterOriginalDefinitionChanges(t *testing.T) {
	for _, change := range []string{"remove", "reselect"} {
		t.Run(change, func(t *testing.T) {
			d, owner, _ := dashboardRegistryFixture(t)
			a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
			sa, sb := httptest.NewServer(a), httptest.NewServer(b)
			defer sa.Close()
			defer sb.Close()
			ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
			channel := groupChannelFixture(t, a)
			in := placementFixtureInput()
			in.NotificationIDs = []string{channel.ID}
			makeDefinition := func(id, source string, monitor MonitorInput) {
				t.Helper()
				w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{"requestId": id, "registrationIds": []string{ra.ID, rb.ID}, "monitor": monitor, "syncNotifications": true, "notificationSourceRegistrationId": source})
				if w.Code != 202 {
					t.Fatal(w.Code, w.Body.String())
				}
				d.finishFleet(context.Background(), id)
				if row := d.fleet[id]; row.State != "applied" {
					t.Fatal(row.State, row.Reason)
				}
			}
			makeDefinition(placementFixtureID, ra.ID, in)
			var copied string
			for _, p := range d.fleet[placementFixtureID].Placements {
				if p.RegistrationID == rb.ID {
					copied = b.current(p.MonitorID).NotificationIDs[0]
				}
			}
			second := strings.Repeat("f", 32)
			in.Name = "Origin consumer"
			in.NotificationIDs = []string{copied}
			makeDefinition(second, rb.ID, in)
			first := d.fleet[placementFixtureID]
			firstPlan, _ := d.openFleet(first)
			if change == "remove" {
				w := dashboardHTTP(d, "DELETE", "/api/dashboard/fleet/monitors/"+first.ID, owner, "", map[string]string{"revision": first.Revision})
				if w.Code != 202 {
					t.Fatal(w.Code, w.Body.String())
				}
			} else {
				firstPlan.Monitor.NotificationIDs = []string{}
				w := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+first.ID, owner, "", map[string]any{"revision": first.Revision, "registrationIds": first.RegistrationIDs, "monitor": firstPlan.Monitor})
				if w.Code != 202 {
					t.Fatal(w.Code, w.Body.String())
				}
			}
			d.finishFleet(context.Background(), first.ID)
			// Renaming the consumer must retain its origin too, rather than relying
			// on a mapping that happened to be present before its edit.
			consumer := d.fleet[second]
			consumerPlan, _ := d.openFleet(consumer)
			consumerPlan.Monitor.Name = "Renamed origin consumer"
			w := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+second, owner, "", map[string]any{"revision": consumer.Revision, "registrationIds": consumer.RegistrationIDs, "monitor": consumerPlan.Monitor})
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			d.finishFleet(context.Background(), second)
			third := strings.Repeat("e", 32)
			in.Name = "Later origin consumer"
			makeDefinition(third, rb.ID, in)
			updated := channel.ChannelInput
			updated.Config.Webhook.BearerToken = "new-original-fixture-value"
			if _, err := a.writeResource(resourceChannel, channel.ID, updated, false); err != nil {
				t.Fatal(err)
			}
			d.refreshGroupPlacements(context.Background())
			d.finishFleet(context.Background(), second)
			d.finishFleet(context.Background(), third)
			for _, p := range append(slices.Clone(d.fleet[second].Placements), d.fleet[third].Placements...) {
				node := a
				if p.RegistrationID == rb.ID {
					node = b
				}
				m := node.current(p.MonitorID)
				resource := node.currentResource(resourceChannel, m.NotificationIDs[0])
				if resource == nil || channelOf(*resource, "admin").Config.Webhook.BearerToken != "new-original-fixture-value" {
					t.Fatal("the surviving consumer lost its original channel source")
				}
			}
		})
	}
}

func TestGroupSyncWaitsForMembershipBeforeReportingApplied(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var refuse atomic.Bool
	sa := httptest.NewServer(a)
	sb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refuse.Load() && r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/api/fleet-placements/") && strings.HasSuffix(r.URL.Path, "/validate") {
			w.Header().Set("X-Canary-Node-Id", b.ID)
			w.WriteHeader(409)
			return
		}
		b.ServeHTTP(w, r)
	}))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	groupMonitorFixture(t, a, placementFixtureInput())
	group := groupSyncFixture(t, d, owner, []string{ra.ID})
	finishGroupFixture(t, d)
	job, _, _ := loadOne[dashboardGroupSync](d.accounts.node.store, "dashboardGroupSyncs", group.ID)
	d.syncNodeGroup(context.Background(), group.ID, job)
	w := dashboardHTTP(d, "PUT", "/api/dashboard/node-groups/"+group.ID, owner, "", map[string]any{"revision": group.Revision, "name": group.Name, "registrationIds": []string{ra.ID, rb.ID}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &group)
	d.runGroupSyncs(context.Background())
	current, _, _ := loadOne[dashboardNodeGroup](d.accounts.node.store, "dashboardNodeGroups", group.ID)
	job, exists, _ := loadOne[dashboardGroupSync](d.accounts.node.store, "dashboardGroupSyncs", group.ID)
	if current.SyncState != "pending" || !exists {
		t.Fatal("group reported applied before its new member received the monitors")
	}
	d.refreshGroupPlacements(context.Background())
	refuse.Store(true)
	for id := range d.fleet {
		d.finishFleet(context.Background(), id)
	}
	d.syncNodeGroup(context.Background(), group.ID, job)
	current, _, _ = loadOne[dashboardNodeGroup](d.accounts.node.store, "dashboardNodeGroups", group.ID)
	if current.SyncState != "blocked" || current.SyncReason == "" {
		t.Fatalf("group hid its new member's preflight failure: state=%s reason=%s", current.SyncState, current.SyncReason)
	}
}

func TestGroupNativeOutsideReferencesAreVerifiedBeforePlacement(t *testing.T) {
	for _, assigned := range []bool{true, false} {
		t.Run(map[bool]string{true: "original runner", false: "unrelated registration"}[assigned], func(t *testing.T) {
			d, owner, _ := dashboardRegistryFixture(t)
			a, c := causalCapacityNode(t, "a", ":memory:"), causalCapacityNode(t, "b", ":memory:")
			b := dashboardRemoteFixture(t, "independent-b")
			a.peerSeen[c.ID], c.peerSeen[a.ID] = 1, 1
			nodes := []string{a.ID}
			if assigned {
				nodes = append(nodes, c.ID)
			}
			in := input(nodes, "http://127.0.0.1:9", "")
			in.Enabled = false
			native, err := a.writeMonitor(in, nil, "native-reference-adoption", false)
			if err != nil {
				t.Fatal(err)
			}
			for _, page := range causalCapacityPages(t, a) {
				if err = c.applySnapshot(page); err != nil {
					t.Fatal(err)
				}
			}
			sa, sb, sc := httptest.NewServer(a), httptest.NewServer(b), httptest.NewServer(c)
			defer sa.Close()
			defer sb.Close()
			defer sc.Close()
			ra, rb, rc := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL), enrollDashboardNode(t, d, owner, sc.URL)
			group := groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
			body := map[string]any{"requestId": placementFixtureID, "groupIds": []string{group.ID}, "registrationIds": []string{ra.ID, rb.ID, rc.ID}, "monitor": native.MonitorInput,
				"source":     fleetSourceReference{RegistrationID: ra.ID, MonitorID: native.ID, Revision: native.Revision},
				"references": map[string]any{rc.ID: map[string]any{"notificationIds": []string{}, "groupId": "", "tags": []MonitorTag{}}}}
			w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", body)
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			d.finishFleet(context.Background(), placementFixtureID)
			row := d.fleet[placementFixtureID]
			if !assigned {
				if row.State != "blocked" || !strings.Contains(row.Reason, "References must name selected registrations") {
					t.Fatal("unverified outside references were not refused", row.State, row.Reason)
				}
				if a.current(native.ID).Revision != native.Revision || len(b.heads) != 0 {
					t.Fatal("invalid outside references changed a monitoring node")
				}
				return
			}
			for _, page := range causalCapacityPages(t, a) {
				if err = c.applySnapshot(page); err != nil {
					t.Fatal(err)
				}
			}
			d.finishFleet(context.Background(), placementFixtureID)
			row = d.fleet[placementFixtureID]
			if row.State != "applied" || !slices.Contains(row.PinnedRegistrationIDs, rc.ID) {
				t.Fatal("original runner override did not apply", row.State, row.Reason)
			}
			plan, err := d.openFleet(row)
			if err != nil {
				t.Fatal(err)
			}
			ref := plan.References[rc.ID]
			if ref.NotificationIDs == nil || len(*ref.NotificationIDs) != 0 || ref.GroupID == nil || *ref.GroupID != "" || ref.Tags == nil || len(*ref.Tags) != 0 {
				t.Fatal("explicit matching overrides were lost")
			}
			if w = dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", body); w.Code != 202 {
				t.Fatal("verified native pins changed create replay", w.Code, w.Body.String())
			}
		})
	}
}

func TestGroupCreateReplayEmptyGroupsAfterReload(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	server := httptest.NewServer(node)
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	body := map[string]any{"requestId": placementFixtureID, "registrationIds": []string{reg.ID}, "groupIds": []string{}, "monitor": placementFixtureInput()}
	if w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", body); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), placementFixtureID)
	if err := d.loadFleet(); err != nil {
		t.Fatal(err)
	}
	if w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", body); w.Code != 202 {
		t.Fatal("empty groups broke exact create replay after reload", w.Code, w.Body.String())
	}
	if len(node.heads) != 1 {
		t.Fatal("replay duplicated its monitor")
	}
}

func TestGroupNotificationSourceRemovalAndRotation(t *testing.T) {
	for _, mode := range []string{"empty edit", "removed source", "rotating source"} {
		t.Run(mode, func(t *testing.T) {
			d, owner, _ := dashboardRegistryFixture(t)
			a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
			sa, sb := httptest.NewServer(a), httptest.NewServer(b)
			defer sa.Close()
			defer sb.Close()
			ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
			in := placementFixtureInput()
			if mode != "empty edit" {
				in.NotificationIDs = []string{groupChannelFixture(t, a).ID}
			}
			w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{"requestId": placementFixtureID, "registrationIds": []string{rb.ID}, "monitor": in, "syncNotifications": true, "notificationSourceRegistrationId": ra.ID})
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			if mode == "empty edit" {
				d.finishFleet(context.Background(), placementFixtureID)
			}
			if mode == "rotating source" {
				row := d.registry[ra.ID]
				row.State = "rotating"
				d.registry[ra.ID] = row
			} else if w = dashboardHTTP(d, "DELETE", "/api/dashboard/nodes/"+ra.ID, owner, "", nil); w.Code != 204 {
				t.Fatal("source removal", w.Code, w.Body.String())
			}
			if mode == "empty edit" {
				row := d.fleet[placementFixtureID]
				in.Name = "Edited without notification resources"
				w = dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision, "registrationIds": row.RegistrationIDs, "monitor": in})
				if w.Code != 202 {
					t.Fatal("unused removed source blocked edit", w.Code, w.Body.String())
				}
			}
			d.finishFleet(context.Background(), placementFixtureID)
			row := d.fleet[placementFixtureID]
			switch mode {
			case "empty edit":
				if row.State != "applied" {
					t.Fatal(row.State, row.Reason)
				}
			case "removed source":
				if row.State != "blocked" || len(b.heads) != 0 {
					t.Fatal("a removed source should require explicit replacement before node writes", row.State, row.Reason)
				}
			case "rotating source":
				if row.State != "pending" || len(b.heads) != 0 {
					t.Fatal("temporary rotation must remain retryable", row.State, row.Reason)
				}
			}
		})
	}
}

func TestGroupSyncReauthorizesAppliedMonitor(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "add member", true: "replace members"}[replace], func(t *testing.T) {
			d, owner, _ := dashboardRegistryFixture(t)
			a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
			sa, sb := httptest.NewServer(a), httptest.NewServer(b)
			defer sa.Close()
			defer sb.Close()
			ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
			channel := groupChannelFixture(t, a)
			in := placementFixtureInput()
			in.NotificationIDs = []string{channel.ID}
			native := groupMonitorFixture(t, a, in)
			group := groupSyncFixture(t, d, owner, []string{ra.ID})
			finishGroupFixture(t, d)
			var before dashboardFleetRecord
			for _, row := range d.fleet {
				before = row
			}
			if before.State != "applied" {
				t.Fatal(before.State, before.Reason)
			}
			revokeFleetFixtureAccount(d, before.ActorID)
			freshOwner := dashboardPasswordLogin(t, d.accounts, "owner", dashboardPasswordFixture)
			members := []string{ra.ID, rb.ID}
			if replace {
				members = []string{rb.ID}
			}
			w := dashboardHTTP(d, "PUT", "/api/dashboard/node-groups/"+group.ID, freshOwner, "", map[string]any{"revision": group.Revision, "name": group.Name, "registrationIds": members})
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			d.runGroupSyncs(context.Background())
			row := d.fleet[before.ID]
			if !d.fleetAuthorized(row) || row.Revision == before.Revision {
				t.Fatal("explicit current-authority group sync did not rebind an idle applied monitor")
			}
			updated := channel.ChannelInput
			updated.Config.Webhook.BearerToken = "rebound-fixture-value"
			if _, err := a.writeResource(resourceChannel, channel.ID, updated, false); err != nil {
				t.Fatal(err)
			}
			d.refreshGroupPlacements(context.Background())
			d.finishFleet(context.Background(), row.ID)
			row = d.fleet[row.ID]
			if row.State != "applied" || !sameFleetIDs(row.RegistrationIDs, members) {
				t.Fatal("reauthorized monitor missed changed membership", row.State, row.Reason)
			}
			for _, placement := range row.Placements {
				if placement.RegistrationID == rb.ID {
					monitor := b.current(placement.MonitorID)
					resource := b.currentResource(resourceChannel, monitor.NotificationIDs[0])
					if channelOf(*resource, "admin").Config.Webhook.BearerToken != "rebound-fixture-value" {
						t.Fatal("reauthorized monitor missed source channel updates")
					}
				}
			}
			if replace && a.current(native.ID) != nil {
				t.Fatal("the former group member did not retire its monitor")
			}
			job, exists, err := loadOne[dashboardGroupSync](d.accounts.node.store, "dashboardGroupSyncs", group.ID)
			if err != nil || !exists {
				t.Fatal("membership sync job missing", err)
			}
			d.syncNodeGroup(context.Background(), group.ID, job)
			current, _, _ := loadOne[dashboardNodeGroup](d.accounts.node.store, "dashboardNodeGroups", group.ID)
			if current.SyncState != "applied" {
				t.Fatal("reauthorized group did not confirm sync", current.SyncState, current.SyncReason)
			}
		})
	}
}
