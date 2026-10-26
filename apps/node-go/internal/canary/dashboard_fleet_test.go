package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

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
	var lose atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	if row.State != "pending" || len(node.heads) != 1 {
		t.Fatal("lost answer was not retained as an unknown saved outcome")
	}
	var revision string
	for _, heads := range node.heads {
		revision = heads[0].Monitor.Revision
	}
	lose.Store(false)
	d.finishFleet(context.Background(), row.ID)
	row = d.fleet[row.ID]
	if row.State != "applied" || len(node.heads) != 1 || row.Placements[0].Revision != revision {
		t.Fatal("retry duplicated or replaced an already committed monitor")
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
	w = dashboardHTTP(reloaded, "DELETE", "/api/dashboard/fleet/monitors/"+row.ID, owner, "", map[string]string{"revision": row.Revision})
	if w.Code != 202 {
		t.Fatal(w.Code)
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
