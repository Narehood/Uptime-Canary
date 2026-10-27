package canary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// The preceding node release has this exact placement envelope. Everything
// else uses its existing channel/tag and revision-fenced placement APIs.
func legacyFleetFixture(n *Node, posts *atomic.Int32, lose *atomic.Bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Canary-Node-Id", n.ID)
		if strings.HasPrefix(r.URL.Path, "/api/fleet-placements/") && r.Method != "GET" {
			body, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(strings.NewReader(string(body)))
			var command fleetPlacementCommand
			if err := dashboardJSON(body, &command, "sequence", "revision", "monitorId", "adopt", "monitor", "deleted"); err != nil {
				errorReply(w, err)
				return
			}
		}
		if r.URL.Path == "/api/notification-channels" && r.Method == "POST" {
			posts.Add(1)
			if lose != nil && lose.Swap(false) {
				recorder := httptest.NewRecorder()
				n.ServeHTTP(recorder, r)
				if recorder.Code != 201 {
					w.WriteHeader(recorder.Code)
					_, _ = w.Write(recorder.Body.Bytes())
					return
				}
				reply(w, 200, map[string]string{"error": "fixture dropped acknowledgement"})
				return
			}
		}
		n.ServeHTTP(w, r)
	})
}

func TestGroupMixedVersionCopiesAndRefreshesSettings(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var posts atomic.Int32
	sa, sb := httptest.NewServer(a), httptest.NewServer(legacyFleetFixture(b, &posts, nil))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	channel := groupChannelFixture(t, a)
	tag, err := a.writeResource(resourceTag, "", TagInput{Name: "Location", Color: "#336699"}, false)
	if err != nil {
		t.Fatal(err)
	}
	in := placementFixtureInput()
	in.Type, in.Target = "ping", "127.0.0.1"
	in.NotificationIDs = []string{channel.ID}
	in.Tags = []MonitorTag{{TagID: tag.ID, Value: "primary", Public: true}}
	original := groupMonitorFixture(t, a, in)
	group := groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	d.runGroupSyncs(context.Background())
	// Recover a preflight refusal saved by the previous dashboard build through
	// the group's Sync monitors action, without editing or recreating monitors.
	for _, blocked := range d.fleet {
		plan, err := d.openFleet(blocked)
		if err != nil {
			t.Fatal(err)
		}
		blocked.State, blocked.Reason = "blocked", "Previous node refused bundled settings"
		if err := d.saveFleet(nil, AccountView{}, blocked, plan); err != nil {
			t.Fatal(err)
		}
	}
	recovery := dashboardHTTP(d, "POST", "/api/dashboard/node-groups/"+group.ID+"/sync", owner, "", map[string]string{"revision": group.Revision})
	if recovery.Code != 202 || json.Unmarshal(recovery.Body.Bytes(), &group) != nil {
		t.Fatal(recovery.Code, recovery.Body.String())
	}
	finishGroupFixture(t, d)
	var row dashboardFleetRecord
	for _, row = range d.fleet {
	}
	if row.State != "applied" || len(a.heads) != 1 || len(b.heads) != 1 {
		t.Fatal("mixed versions failed to apply", row.State, row.Reason)
	}
	if a.current(original.ID) == nil || len(a.channelViews("admin")) != 1 {
		t.Fatal("native monitor or settings were replaced")
	}
	var copied *Monitor
	for _, p := range row.Placements {
		if p.RegistrationID == rb.ID {
			copied = b.current(p.MonitorID)
		}
	}
	if copied == nil || len(copied.NotificationIDs) != 1 || len(copied.Tags) != 1 || copied.Tags[0].Value != "primary" || !copied.Tags[0].Public {
		t.Fatal("copied settings missing")
	}
	copyID := copied.NotificationIDs[0]
	if b.currentResource(resourceChannel, copyID) == nil || posts.Load() != 1 {
		t.Fatal("older node did not receive its own channel")
	}
	w := dashboardHTTP(d, "POST", "/api/dashboard/node-groups/"+group.ID+"/sync", owner, "", map[string]string{"revision": group.Revision})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	finishGroupFixture(t, d)
	if posts.Load() != 1 || len(b.channelViews("admin")) != 1 {
		t.Fatal("repeat sync duplicated channels")
	}
	changed := channel.ChannelInput
	changed.Config.Webhook.BearerToken = "updated-private-fixture-token"
	if _, err = a.writeResource(resourceChannel, channel.ID, changed, false); err != nil {
		t.Fatal(err)
	}
	row = d.fleet[row.ID]
	row.RetryAt = 0
	plan, _ := d.openFleet(row)
	if err = d.saveFleet(nil, AccountView{}, row, plan); err != nil {
		t.Fatal(err)
	}
	d.refreshGroupPlacements(context.Background())
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "applied" || channelOf(*b.currentResource(resourceChannel, copyID), "admin").Config.Webhook.BearerToken != changed.Config.Webhook.BearerToken || posts.Load() != 1 {
		t.Fatal("source edit did not reach older node", row.State, row.Reason)
	}
	local := changed
	local.Name = "Direct node edit"
	if _, err = b.writeResource(resourceChannel, copyID, local, false); err != nil {
		t.Fatal(err)
	}
	changed.Name = "Another source edit"
	_, _ = a.writeResource(resourceChannel, channel.ID, changed, false)
	row.RetryAt = 0
	plan, _ = d.openFleet(row)
	_ = d.saveFleet(nil, AccountView{}, row, plan)
	d.refreshGroupPlacements(context.Background())
	d.finishFleet(context.Background(), row.ID)
	if d.fleet[row.ID].State != "blocked" || channelOf(*b.currentResource(resourceChannel, copyID), "admin").Name != local.Name {
		t.Fatal("sync overwrote a direct node edit")
	}
}

func TestGroupMixedVersionLostResourceAcknowledgementIsReadBack(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var posts atomic.Int32
	var lose atomic.Bool
	lose.Store(true)
	sa, sb := httptest.NewServer(a), httptest.NewServer(legacyFleetFixture(b, &posts, &lose))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	channel := groupChannelFixture(t, a)
	in := placementFixtureInput()
	in.NotificationIDs = []string{channel.ID}
	original := groupMonitorFixture(t, a, in)
	groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	finishGroupFixture(t, d)
	var row dashboardFleetRecord
	for _, row = range d.fleet {
	}
	if row.State != "pending" || posts.Load() != 1 || len(b.heads) != 0 || a.current(original.ID).Revision != original.Revision {
		t.Fatal("uncertain resource create changed monitors or lost its intent", row.State, row.Reason)
	}
	if err := d.loadFleet(); err != nil {
		t.Fatal(err)
	}
	d.finishFleet(context.Background(), row.ID)
	if row = d.fleet[row.ID]; row.State != "applied" || posts.Load() != 1 || len(b.channelViews("admin")) != 1 {
		t.Fatal("lost creation was repeated instead of read back", row.State, row.Reason)
	}
	view := dashboardHTTP(d, "GET", "/api/dashboard/fleet/monitors", owner, "", nil)
	if strings.Contains(view.Body.String(), "fixture-only-bearer") {
		t.Fatal("copied channel credentials leaked into fleet metadata")
	}
}

func TestLegacyFleetEnvelopeSupportsNewNodes(t *testing.T) {
	n := placementFixtureNode(t, ":memory:")
	command := placementFixtureCommand(t, 1, "", false)
	body, _ := json.Marshal(command)
	if strings.Contains(string(body), "resources") {
		t.Fatal("older commands unexpectedly gained resource fields")
	}
	placementFixtureWrite(t, n, command, 201)
}

func TestGroupMixedVersionMonitorsShareCopiedChannelUpdates(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var posts atomic.Int32
	sa, sb := httptest.NewServer(a), httptest.NewServer(legacyFleetFixture(b, &posts, nil))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	channel := groupChannelFixture(t, a)
	in := placementFixtureInput()
	in.NotificationIDs = []string{channel.ID}
	groupMonitorFixture(t, a, in)
	in.Name = "Another original monitor"
	groupMonitorFixture(t, a, in)
	group := groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	finishGroupFixture(t, d)
	if len(d.fleet) != 2 || len(b.heads) != 2 || posts.Load() != 1 {
		t.Fatal("shared channel was duplicated")
	}
	copyID := b.channelViews("admin")[0].Channel.ID
	changed := channel.ChannelInput
	changed.Name = "Renamed source channel"
	if _, err := a.writeResource(resourceChannel, channel.ID, changed, false); err != nil {
		t.Fatal(err)
	}
	// A new consumer reaches the old destination before either original
	// consumer refreshes. Its receipt must reuse and update the existing ID.
	in.Name = "New consumer after source edit"
	third := groupMonitorFixture(t, a, in)
	w := dashboardHTTP(d, "POST", "/api/dashboard/node-groups/"+group.ID+"/sync", owner, "", map[string]string{"revision": group.Revision})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.runGroupSyncs(context.Background())
	newConsumer := fleetRowForOriginal(t, d, third.ID)
	d.finishFleet(context.Background(), newConsumer.ID)
	if d.fleet[newConsumer.ID].State != "applied" || posts.Load() != 1 || len(b.channelViews("admin")) != 1 || b.channelViews("admin")[0].Channel.ID != copyID || b.channelViews("admin")[0].Channel.Name != changed.Name {
		t.Fatal("new consumer duplicated the canonical copy before existing consumers refreshed", d.fleet[newConsumer.ID].Reason)
	}
	for _, row := range d.fleet {
		row.RetryAt = 0
		plan, _ := d.openFleet(row)
		if err := d.saveFleet(nil, AccountView{}, row, plan); err != nil {
			t.Fatal(err)
		}
	}
	d.refreshGroupPlacements(context.Background())
	for id := range d.fleet {
		d.finishFleet(context.Background(), id)
	}
	for _, row := range d.fleet {
		if row.State != "applied" {
			t.Fatal(row.State, row.Reason)
		}
	}
	channels := b.channelViews("admin")
	if posts.Load() != 1 || len(channels) != 1 || channels[0].Channel.Name != changed.Name {
		t.Fatal("consumers duplicated a shared channel when its source changed")
	}
}

func TestGroupMixedVersionPreservesLocalRollbackWhileAnotherConsumerLags(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var posts atomic.Int32
	sa, sb := httptest.NewServer(a), httptest.NewServer(legacyFleetFixture(b, &posts, nil))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	channel := groupChannelFixture(t, a)
	in := placementFixtureInput()
	in.NotificationIDs = []string{channel.ID}
	groupMonitorFixture(t, a, in)
	in.Name = "Second consumer"
	groupMonitorFixture(t, a, in)
	groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	finishGroupFixture(t, d)
	ids := []string{}
	for id := range d.fleet {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	if len(ids) != 2 || len(b.channelViews("admin")) != 1 {
		t.Fatal("shared channel fixture failed")
	}
	localID := b.channelViews("admin")[0].Channel.ID
	changed := channel.ChannelInput
	changed.Name = "Source's new configuration"
	if _, err := a.writeResource(resourceChannel, channel.ID, changed, false); err != nil {
		t.Fatal(err)
	}
	for _, row := range d.fleet {
		row.RetryAt = 0
		plan, _ := d.openFleet(row)
		if err := d.saveFleet(nil, AccountView{}, row, plan); err != nil {
			t.Fatal(err)
		}
	}
	d.refreshGroupPlacements(context.Background())
	d.finishFleet(context.Background(), ids[0])
	if d.fleet[ids[0]].State != "applied" || b.channelViews("admin")[0].Channel.Name != changed.Name {
		t.Fatal("first consumer did not update its shared channel")
	}
	// A user deliberately restores the old configuration before the other
	// consumer catches up. Its stale hash is not evidence of current ownership.
	rolledBack, err := b.writeResource(resourceChannel, localID, channel.ChannelInput, false)
	if err != nil {
		t.Fatal(err)
	}
	d.finishFleet(context.Background(), ids[1])
	if d.fleet[ids[1]].State != "blocked" || b.currentResource(resourceChannel, localID).Revision != rolledBack.Revision {
		t.Fatal("a lagging consumer overwrote a user's channel rollback")
	}
}

func TestGroupMixedVersionConsumersShareUnconfirmedCreateIntent(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var posts atomic.Int32
	legacy := legacyFleetFixture(b, &posts, nil)
	sa := httptest.NewServer(a)
	sb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/notification-channels" && r.Method == "POST" {
			posts.Add(1)
			w.Header().Set("X-Canary-Node-Id", b.ID)
			reply(w, 502, map[string]string{"error": "fixture cannot confirm creation"})
			return
		}
		legacy.ServeHTTP(w, r)
	}))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	channel := groupChannelFixture(t, a)
	in := placementFixtureInput()
	in.NotificationIDs = []string{channel.ID}
	groupMonitorFixture(t, a, in)
	in.Name = "Second waiting consumer"
	groupMonitorFixture(t, a, in)
	groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	finishGroupFixture(t, d)
	for _, row := range d.fleet {
		if row.State != "pending" {
			t.Fatal("unconfirmed settings did not stay pending", row.State, row.Reason)
		}
	}
	if posts.Load() != 1 || len(b.heads) != 0 {
		t.Fatal("another consumer repeated an unconfirmed create")
	}
	if err := d.loadFleet(); err != nil {
		t.Fatal(err)
	}
	for id := range d.fleet {
		d.finishFleet(context.Background(), id)
	}
	if posts.Load() != 1 {
		t.Fatal("restart forgot the shared creation intent")
	}
	// A delayed first write becomes visible. All consumers recover by reading it.
	if _, err := b.writeResource(resourceChannel, "", channel.ChannelInput, false); err != nil {
		t.Fatal(err)
	}
	for id := range d.fleet {
		d.finishFleet(context.Background(), id)
	}
	for _, row := range d.fleet {
		if row.State != "applied" {
			t.Fatal("delayed settings did not recover", row.State, row.Reason)
		}
	}
	if posts.Load() != 1 || len(b.heads) != 2 || len(b.channelViews("admin")) != 1 {
		t.Fatal("recovery duplicated resources or monitors")
	}
}

func TestGroupMixedVersionKeepsDistinctEqualSettings(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var posts atomic.Int32
	sa, sb := httptest.NewServer(a), httptest.NewServer(legacyFleetFixture(b, &posts, nil))
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	first, second := groupChannelFixture(t, a), groupChannelFixture(t, a)
	tag1, err := a.writeResource(resourceTag, "", TagInput{Name: "Equal tag", Color: "#336699"}, false)
	if err != nil {
		t.Fatal(err)
	}
	tag2, err := a.writeResource(resourceTag, "", TagInput{Name: "Equal tag", Color: "#336699"}, false)
	if err != nil {
		t.Fatal(err)
	}
	in := placementFixtureInput()
	in.NotificationIDs = []string{first.ID, second.ID}
	in.Tags = []MonitorTag{{TagID: tag1.ID, Value: "one"}, {TagID: tag2.ID, Value: "two", Public: true}}
	groupMonitorFixture(t, a, in)
	groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
	finishGroupFixture(t, d)
	for _, row := range d.fleet {
		if row.State != "applied" {
			t.Fatal("equal settings were collapsed", row.State, row.Reason)
		}
		for _, p := range row.Placements {
			if p.RegistrationID == rb.ID {
				m := b.current(p.MonitorID)
				if len(m.NotificationIDs) != 2 || m.NotificationIDs[0] == m.NotificationIDs[1] || len(m.Tags) != 2 || m.Tags[0].TagID == m.Tags[1].TagID {
					t.Fatal("distinct selections became duplicate IDs")
				}
				if m.Tags[0].Value != "one" || m.Tags[1].Value != "two" || !m.Tags[1].Public {
					t.Fatal("distinct tag annotations were lost")
				}
			}
		}
	}
	if posts.Load() != 2 || len(b.channelViews("admin")) != 2 || len(b.tagViews()) != 2 {
		t.Fatal("equal source resources did not keep separate identities")
	}
}

func TestGroupMixedVersionNativeReuseKeepsIndependentSource(t *testing.T) {
	for _, version := range []string{"placements", "pre-fleet"} {
		t.Run(version, func(t *testing.T) {
			d, owner, _ := dashboardRegistryFixture(t)
			a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
			var creates, unsupported atomic.Int32
			var handler http.Handler = legacyFleetFixture(b, &creates, nil)
			if version == "pre-fleet" {
				handler = preFleetFixture(b, &creates, &unsupported, nil, nil)
			}
			sa, sb := httptest.NewServer(a), httptest.NewServer(handler)
			defer sa.Close()
			defer sb.Close()
			ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
			channelA, channelB := groupChannelFixture(t, a), groupChannelFixture(t, b)
			tagInput := TagInput{Name: "Independent equal tag", Color: "#336699"}
			tagA, err := a.writeResource(resourceTag, "", tagInput, false)
			if err != nil {
				t.Fatal(err)
			}
			tagB, err := b.writeResource(resourceTag, "", tagInput, false)
			if err != nil {
				t.Fatal(err)
			}
			in := placementFixtureInput()
			in.NotificationIDs = []string{channelA.ID}
			in.Tags = []MonitorTag{{TagID: tagA.ID}}
			groupMonitorFixture(t, a, in)
			group := groupSyncFixture(t, d, owner, []string{ra.ID, rb.ID})
			finishGroupFixture(t, d)
			if len(b.channelViews("admin")) != 1 || len(b.tagViews()) != 1 {
				t.Fatal("initial copy did not reuse equal native settings")
			}
			in.Name = "B's independent settings consumer"
			in.NodeIDs = []string{b.ID}
			in.NotificationIDs = []string{channelB.ID}
			in.Tags = []MonitorTag{{TagID: tagB.ID}}
			w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{"requestId": placementFixtureID, "groupIds": []string{group.ID}, "monitor": in, "syncNotifications": true, "notificationSourceRegistrationId": rb.ID})
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			d.finishFleet(context.Background(), placementFixtureID)
			if row := d.fleet[placementFixtureID]; row.State != "applied" {
				t.Fatal(row.State, row.Reason)
			}
			changed := channelA.ChannelInput
			changed.Name = "A-only channel change"
			if _, err := a.writeResource(resourceChannel, channelA.ID, changed, false); err != nil {
				t.Fatal(err)
			}
			changedTag := tagInput
			changedTag.Name = "A-only tag change"
			if _, err := a.writeResource(resourceTag, tagA.ID, changedTag, false); err != nil {
				t.Fatal(err)
			}
			for _, row := range d.fleet {
				row.RetryAt = 0
				plan, _ := d.openFleet(row)
				if err := d.saveFleet(nil, AccountView{}, row, plan); err != nil {
					t.Fatal(err)
				}
			}
			d.refreshGroupPlacements(context.Background())
			for id := range d.fleet {
				d.finishFleet(context.Background(), id)
			}
			if b.currentResource(resourceChannel, channelB.ID).Revision != channelB.Revision || b.currentResource(resourceTag, tagB.ID).Revision != tagB.Revision {
				t.Fatal("sync changed B's independent native settings")
			}
			consumer := d.fleet[placementFixtureID]
			if consumer.State != "applied" {
				t.Fatal(consumer.State, consumer.Reason)
			}
			for _, p := range consumer.Placements {
				node := a
				if p.RegistrationID == rb.ID {
					node = b
				}
				monitor := node.current(p.MonitorID)
				channel := node.currentResource(resourceChannel, monitor.NotificationIDs[0])
				tag := node.currentResource(resourceTag, monitor.Tags[0].TagID)
				if !equalJSON(channelOf(*channel, "admin").ChannelInput, channelB.ChannelInput) || !equalJSON(tagOf(*tag).TagInput, tagInput) || p.RegistrationID == rb.ID && (monitor.NotificationIDs[0] != channelB.ID || monitor.Tags[0].TagID != tagB.ID) {
					t.Fatal("B's native consumer followed A's copied provenance")
				}
			}
		})
	}
}
