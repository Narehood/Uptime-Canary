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

func TestForceNodeRemovalPreservesUnstartedSourceSettings(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "native"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {

			d, owner, _ := dashboardRegistryFixture(t)
			a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
			sa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/validate") {
					w.Header().Set("X-Canary-Node-Id", a.ID)
					errorReply(w, fail(409, "source preflight is blocked before any monitor write"))
					return
				}
				a.ServeHTTP(w, r)
			}))
			var rejectB atomic.Bool
			sb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if rejectB.Load() && (strings.HasSuffix(r.URL.Path, "/validate") || legacy && r.Method == "POST" && r.URL.Path == "/api/notification-channels") {
					w.Header().Set("X-Canary-Node-Id", b.ID)
					errorReply(w, fail(409, "survivor preflight temporarily blocked"))
					return
				}
				if legacy && strings.HasPrefix(r.URL.Path, "/api/fleet-placements/") {
					w.Header().Set("X-Canary-Node-Id", b.ID)
					errorReply(w, fail(404, "fixture models a legacy node"))
					return
				}
				b.ServeHTTP(w, r)
			}))
			defer sa.Close()
			defer sb.Close()
			ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
			channel := groupChannelFixture(t, a)
			w := dashboardHTTP(a, "POST", "/api/tags", adminToken, "", TagInput{Name: "Frozen source tag", Color: "#123456"})
			var tag Tag
			if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &tag) != nil {
				t.Fatal(w.Code, w.Body.String())
			}
			in := placementFixtureInput()
			in.Enabled, in.NotificationIDs = false, []string{channel.ID}
			in.Tags = []MonitorTag{{TagID: tag.ID, Value: "retained"}}
			w = dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{
				"requestId": placementFixtureID, "registrationIds": []string{ra.ID, rb.ID}, "monitor": in,
				"syncNotifications": true, "notificationSourceRegistrationId": ra.ID,
			})
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			d.finishFleet(context.Background(), placementFixtureID)
			row := d.fleet[placementFixtureID]
			if row.State != "blocked" || len(row.Placements) != 0 {
				t.Fatal("fixture did not block before creating placements")
			}
			forceNodeFixture(t, d, owner, ra)
			rejectB.Store(true)
			pumpNodeRemoval(t, d, ra.ID, nil)
			row = d.fleet[row.ID]
			plan, err := d.openFleet(row)
			if err != nil || row.State != "blocked" || len(plan.Resources) != 2 {
				t.Fatal("pending survivor lost the durable settings snapshot", row.State, row.Reason)
			}
			plan.Monitor.Name = "Edited before frozen settings were installed"
			w = dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision, "registrationIds": row.RegistrationIDs, "monitor": plan.Monitor})
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			rejectB.Store(false)
			d.finishFleet(context.Background(), row.ID)
			row = d.fleet[row.ID]
			plan, err = d.openFleet(row)
			if err != nil || row.State != "applied" || plan.SyncNotifications || plan.NotificationSourceRegistrationID != "" || len(plan.Resources) != 0 {
				t.Fatal("source settings were lost or retained for future replay", row.State, row.Reason)
			}
			b.mu.Lock()
			monitor := b.current(row.Placements[0].MonitorID)
			channels, tags := b.channelViews("admin"), b.tagViews()
			b.mu.Unlock()
			if monitor == nil || len(monitor.NotificationIDs) != 1 || len(monitor.Tags) != 1 {
				t.Fatal("surviving monitor lost its notification/tag references")
			}
			if len(channels) != 1 || channels[0].ConfigConflict || channels[0].Channel.ID != monitor.NotificationIDs[0] || !equalJSON(channels[0].Channel.ChannelInput, channel.ChannelInput) {
				t.Fatal("survivor's copied channel settings differ")
			}
			if len(tags) != 1 || tags[0].ConfigConflict || tags[0].Tag.ID != monitor.Tags[0].TagID || !equalJSON(tags[0].Tag.TagInput, tag.TagInput) || monitor.Tags[0].Value != "retained" {
				t.Fatal("survivor's copied tag settings differ")
			}
			plan.Monitor.Name = "Edited after source removal"
			w = dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision, "registrationIds": row.RegistrationIDs, "monitor": plan.Monitor})
			if w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			d.finishFleet(context.Background(), row.ID)
			if d.fleet[row.ID].State != "applied" {
				t.Fatal("future edit lost the installed native/legacy references", d.fleet[row.ID].Reason)
			}
		})
	}
}

func TestForceNodeRemovalCancelsPreparedButWhollyUnsentLastRunner(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	node := dashboardRemoteFixture(t, "node-a")
	var refuse atomic.Bool
	var writes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refuse.Load() && r.URL.Path == "/api/auth/me" {
			w.Header().Set("X-Canary-Node-Id", node.ID)
			errorReply(w, fail(401, "pre-write role check refused"))
			return
		}
		if r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/fleet-placements/") {
			writes.Add(1)
		}
		node.ServeHTTP(w, r)
		if strings.HasSuffix(r.URL.Path, "/validate") {
			refuse.Store(true)
		}
	}))
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	row := fleetCreateFixture(t, d, owner, []string{reg.ID}, nil)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	plan, err := d.openFleet(row)
	if err != nil || !plan.Prepared || len(row.Placements) != 1 || !plan.Unsent[row.Placements[0].ID] || writes.Load() != 0 {
		t.Fatal("fixture did not stop at the durable pre-send boundary")
	}
	forceNodeFixture(t, d, owner, reg)
	refuse.Store(false)
	pumpNodeRemoval(t, d, reg.ID, nil)
	node.mu.Lock()
	defer node.mu.Unlock()
	if writes.Load() != 0 || len(node.heads) != 0 || len(node.accounts) != 1 || d.fleet[row.ID].State != "removed" {
		t.Fatal("wholly unsent removal manufactured a monitor or left access")
	}
}

func TestForceNodeRemovalReauthorizesPendingRefusedEditor(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	if w := dashboardHTTP(d, "POST", "/api/accounts", owner, "", map[string]string{"name": "editor", "role": "editor", "password": dashboardPasswordFixture}); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	editor := dashboardPasswordLogin(t, d.accounts, "editor", dashboardPasswordFixture)
	node := dashboardRemoteFixture(t, "node-a")
	var refuse atomic.Bool
	var editorAuth atomic.Value
	editorAuth.Store("")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if refuse.Load() && r.URL.Path == "/api/auth/me" && r.Header.Get("Authorization") == editorAuth.Load().(string) {
			w.Header().Set("X-Canary-Node-Id", node.ID)
			errorReply(w, fail(401, "editor delegation refused"))
			return
		}
		node.ServeHTTP(w, r)
		if strings.HasSuffix(r.URL.Path, "/validate") {
			refuse.Store(true)
		}
	}))
	defer server.Close()
	reg := enrollDashboardNode(t, d, owner, server.URL)
	secret, err := d.openSecrets(d.registry[reg.ID])
	if err != nil {
		t.Fatal(err)
	}
	editorAuth.Store("Bearer " + secret.token("editor"))
	row := fleetCreateFixture(t, d, owner, []string{reg.ID}, nil)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "applied" {
		t.Fatal(row.Reason)
	}
	plan, _ := d.openFleet(row)
	plan.Monitor.Name = "Pending editor update"
	refuse.Store(false)
	w := dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, editor, "", map[string]any{"revision": row.Revision, "registrationIds": row.RegistrationIDs, "monitor": plan.Monitor})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	plan, err = d.openFleet(row)
	if err != nil || row.State != "pending" || row.ActorRole != "editor" || !d.fleetAuthorized(row) || !plan.Prepared {
		t.Fatal("fixture did not retain a locally authorized pending editor command")
	}
	before, _ := fleetCommandBytes(plan, row.Placements[0])
	forceNodeFixture(t, d, owner, reg)
	_, _ = d.finishEnrollment(context.Background(), reg.ID)
	resumed := d.fleet[row.ID]
	retained, _ := d.openFleet(resumed)
	after, _ := fleetCommandBytes(retained, resumed.Placements[0])
	if resumed.ActorRole != "admin" || string(before) != string(after) {
		t.Fatal("pending editor command was not reauthorized intact")
	}
	pumpNodeRemoval(t, d, reg.ID, nil)
	if d.fleet[row.ID].State != "removed" {
		t.Fatal("refused editor credential still blocks removal")
	}
}

func TestForceNodeRemovalRechecksWorkerProgressDuringSettingsSnapshot(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b")
	var advance atomic.Bool
	sa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/notification-channels" && advance.Swap(false) {
			// A real worker can finish while cleanup reads the settings snapshot.
			// Its receipt progress does not change the desired-plan revision.
			d.finishFleet(context.Background(), placementFixtureID)
		}
		a.ServeHTTP(w, r)
	}))
	sb := httptest.NewServer(b)
	defer sa.Close()
	defer sb.Close()
	ra, rb := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL)
	channel := groupChannelFixture(t, a)
	in := placementFixtureInput()
	in.Enabled, in.NotificationIDs = false, []string{channel.ID}
	w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{
		"requestId": placementFixtureID, "registrationIds": []string{ra.ID, rb.ID}, "monitor": in,
		"syncNotifications": true, "notificationSourceRegistrationId": ra.ID,
	})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	forceNodeFixture(t, d, owner, ra)
	advance.Store(true)
	if _, err := d.finishEnrollment(context.Background(), ra.ID); err == nil {
		t.Fatal("cleanup overwrote worker progress observed during its snapshot")
	}
	row := d.fleet[placementFixtureID]
	secret, _ := d.openSecrets(d.registry[ra.ID])
	if row.State != "applied" || len(row.Placements) != 2 || secret.Removal.Phase != "queued" {
		t.Fatal("worker receipts were lost before cleanup retried")
	}
	pumpNodeRemoval(t, d, ra.ID, nil)
	row = d.fleet[row.ID]
	if row.State != "applied" || !slices.Equal(row.RegistrationIDs, []string{rb.ID}) {
		t.Fatal("cleanup did not safely resume from the worker's committed progress")
	}
}

func TestForceNodeRemovalKeepsSettingsForUnplacedSharedExpansion(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a, b, c := dashboardRemoteFixture(t, "node-a"), dashboardRemoteFixture(t, "node-b"), dashboardRemoteFixture(t, "node-c")
	var reject atomic.Bool
	sa := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reject.Load() && strings.HasSuffix(r.URL.Path, "/validate") {
			w.Header().Set("X-Canary-Node-Id", a.ID)
			errorReply(w, fail(409, "pending expansion blocked on departing source"))
			return
		}
		a.ServeHTTP(w, r)
	}))
	sb, sc := httptest.NewServer(b), httptest.NewServer(c)
	defer sa.Close()
	defer sb.Close()
	defer sc.Close()
	ra, rb, rc := enrollDashboardNode(t, d, owner, sa.URL), enrollDashboardNode(t, d, owner, sb.URL), enrollDashboardNode(t, d, owner, sc.URL)
	channel := groupChannelFixture(t, a)
	in := placementFixtureInput()
	in.Enabled, in.NotificationIDs = false, []string{channel.ID}
	w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{"requestId": placementFixtureID, "registrationIds": []string{ra.ID, rb.ID}, "monitor": in, "syncNotifications": true, "notificationSourceRegistrationId": ra.ID})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), placementFixtureID)
	row := d.fleet[placementFixtureID]
	if row.State != "applied" {
		t.Fatal(row.Reason)
	}
	updated := channel.ChannelInput
	updated.Name = "Fresh source settings for the new target only"
	raw, _ := json.Marshal(updated)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	body["revision"] = channel.Revision
	changed := dashboardHTTP(a, "PUT", "/api/notification-channels/"+channel.ID, adminToken, "", body)
	if changed.Code != 200 {
		t.Fatal(changed.Code, changed.Body.String())
	}
	physical := ""
	for _, p := range row.Placements {
		if p.RegistrationID == rb.ID {
			physical = p.MonitorID
		}
	}
	w = dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision, "registrationIds": []string{ra.ID, rb.ID, rc.ID}, "monitor": in})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	reject.Store(true)
	d.finishFleet(context.Background(), row.ID)
	if d.fleet[row.ID].State != "blocked" {
		t.Fatal("expansion did not block before its new writes")
	}
	forceNodeFixture(t, d, owner, ra)
	reject.Store(false)
	pumpNodeRemoval(t, d, ra.ID, nil)
	row = d.fleet[row.ID]
	if row.State != "applied" || !sameFleetIDs(row.RegistrationIDs, []string{rb.ID, rc.ID}) {
		t.Fatal(row.State, row.Reason)
	}
	for _, n := range []*Node{b, c} {
		n.mu.Lock()
		views := n.channelViews("admin")
		if len(views) == 1 {
			wanted := channel.ChannelInput
			if n == c {
				wanted = updated
			}
			if !equalJSON(views[0].Channel.ChannelInput, wanted) {
				t.Error("frozen settings replayed on an existing survivor or were lost for a new target")
			}
		}
		if len(n.heads) != 1 || len(views) != 1 {
			t.Error("expansion lost its copied settings or duplicated a monitor")
		}
		for id := range n.heads {
			m := n.current(id)
			if m == nil || len(m.NotificationIDs) != 1 || n == b && id != physical {
				t.Error("surviving physical monitor/settings changed")
			}
		}
		n.mu.Unlock()
	}
}

func TestForceNodeRemovalFinalizesLegacyHAReferencesForEveryMember(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	a := dashboardRemoteFixture(t, "node-a")
	b, c := causalCapacityNode(t, "a", ":memory:"), causalCapacityNode(t, "b", ":memory:")
	b.peerSeen[c.ID], c.peerSeen[b.ID] = 1, 1
	replicate := func() {
		for _, pair := range [][2]*Node{{b, c}, {c, b}} {
			for _, page := range causalCapacityPages(t, pair[0]) {
				if err := pair[1].applySnapshot(page); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/validate") {
			w.Header().Set("X-Canary-Node-Id", a.ID)
			errorReply(w, fail(409, "source blocks initial placement"))
			return
		}
		a.ServeHTTP(w, r)
	}))
	legacy := func(node *Node) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/fleet-placements/") {
				w.Header().Set("X-Canary-Node-Id", node.ID)
				errorReply(w, fail(404, "legacy placement routes unavailable"))
				return
			}
			node.ServeHTTP(w, r)
		}))
	}
	sb, sc := legacy(b), legacy(c)
	defer source.Close()
	defer sb.Close()
	defer sc.Close()
	ra, rb, rc := enrollDashboardNode(t, d, owner, source.URL), enrollDashboardNode(t, d, owner, sb.URL), enrollDashboardNode(t, d, owner, sc.URL)
	channel := groupChannelFixture(t, a)
	in := placementFixtureInput()
	in.Enabled, in.NotificationIDs = false, []string{channel.ID}
	w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{"requestId": placementFixtureID, "registrationIds": []string{ra.ID, rb.ID, rc.ID}, "monitor": in, "syncNotifications": true, "notificationSourceRegistrationId": ra.ID})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	d.finishFleet(context.Background(), placementFixtureID)
	if d.fleet[placementFixtureID].State != "blocked" {
		t.Fatal("fixture did not block the unstarted HA deployment")
	}
	forceNodeFixture(t, d, owner, ra)
	pumpNodeRemoval(t, d, ra.ID, replicate)
	settle := func() {
		for range 12 {
			d.finishFleet(context.Background(), placementFixtureID)
			replicate()
			if d.fleet[placementFixtureID].State == "applied" {
				return
			}
		}
		t.Fatal(d.fleet[placementFixtureID].Reason)
	}
	settle()
	row := d.fleet[placementFixtureID]
	plan, err := d.openFleet(row)
	if err != nil || len(row.Placements) != 1 || len(row.Placements[0].RegistrationIDs) != 2 || len(plan.Resources) != 0 || len(plan.RemovalResourceTargets) != 0 {
		t.Fatal("legacy HA snapshot did not finish", err)
	}
	left, right := plan.References[rb.ID].NotificationIDs, plan.References[rc.ID].NotificationIDs
	if left == nil || right == nil || !slices.Equal(*left, *right) || len(*left) != 1 {
		t.Fatal("legacy HA members retained different installed reference IDs")
	}
	plan.Monitor.Name = "Ordinary edit after legacy HA snapshot"
	w = dashboardHTTP(d, "PUT", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]any{"revision": row.Revision, "registrationIds": row.RegistrationIDs, "monitor": plan.Monitor})
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	settle()
	for _, node := range []*Node{b, c} {
		node.mu.Lock()
		m := node.current(row.Placements[0].MonitorID)
		if m == nil || m.Name != plan.Monitor.Name || !slices.Equal(m.NotificationIDs, *left) {
			t.Error("ordinary edit lost the installed HA settings")
		}
		node.mu.Unlock()
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
			versions := node.heads[row.Placements[0].MonitorID]
			if len(node.heads) != 1 || len(versions) != 1 || !versions[0].Monitor.Deleted || len(node.accounts) != 1 {
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
