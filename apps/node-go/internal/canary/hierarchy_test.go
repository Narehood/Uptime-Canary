package canary

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"uptime-canary/apps/node-go/internal/rollup"
)

func hierarchyGroup(t *testing.T, n *Node, name, parent string) Group {
	t.Helper()
	w := maintenanceAPIRequest(n, "POST", "/api/groups", GroupInput{Name: name, NodeIDs: []string{"a"}, ParentID: parent}, adminToken)
	var g Group
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &g) != nil {
		t.Fatal("create group", w.Code, w.Body.String())
	}
	return g
}

func hierarchyMonitor(t *testing.T, n *Node, name, group string, enabled bool) Monitor {
	t.Helper()
	in := MonitorInput{Name: name, Type: "manual", Target: "manual", IntervalMs: 1000, TimeoutMs: 500, NodeIDs: []string{"a"}, Enabled: enabled, GroupID: group}
	w := maintenanceAPIRequest(n, "POST", "/api/monitors", in, adminToken)
	var m Monitor
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &m) != nil {
		t.Fatal("create monitor", w.Code, w.Body.String())
	}
	return m
}

func hierarchySample(t *testing.T, n *Node, m Monitor, state string, sequence int64) {
	t.Helper()
	s := Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: state, CheckedAt: n.now(), Message: "private fixture", LatencyAvailable: Ptr(false)}, Sequence: sequence, Revision: m.Revision, CheckGeneration: m.CheckGeneration}
	if state == "down" {
		s.DownSince = Ptr(sequence)
	}
	// These fixtures represent already committed local observations. Peer
	// application deliberately refuses to overwrite this node's own samples.
	if err := put(n.store.db, "samples", m.ID+"/a", s); err != nil {
		t.Fatal(err)
	}
	n.samples[m.ID+"/a"] = s
}

func hierarchyViews(t *testing.T, n *Node, token string) map[string]GroupView {
	t.Helper()
	w := maintenanceAPIRequest(n, "GET", "/api/groups", nil, token)
	var list []GroupView
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	out := map[string]GroupView{}
	for _, view := range list {
		out[view.Group.ID] = view
	}
	return out
}

func TestGroupHierarchyAPIStatesCountsFreshnessAndPrivacy(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := maintenanceNode(t, &clock, ":memory:")
	root := hierarchyGroup(t, n, "User selected root", "")
	child := hierarchyGroup(t, n, "User selected child", root.ID)
	empty := hierarchyGroup(t, n, "Empty", "")
	up := hierarchyMonitor(t, n, "Private service name", root.ID, true)
	down := hierarchyMonitor(t, n, "Private failing service", child.ID, true)
	paused := hierarchyMonitor(t, n, "Paused", child.ID, false)
	hierarchySample(t, n, up, "up", 1)
	hierarchySample(t, n, down, "down", 2)
	n.accounts["viewer"] = account{AccountView: AccountView{ID: "viewer", Name: "Viewer", Role: "viewer"}, Hash: digest(viewerToken)}
	views := hierarchyViews(t, n, viewerToken)
	r := views[root.ID]
	if r.HealthState != "down" || r.Health == nil || r.Health.LeafCount != 3 || r.Health.ActiveLeafCount != 2 || r.Health.UpCount != 1 || r.Health.DownCount != 1 || r.Health.PausedCount != 1 || r.Health.DirectChildCount != 2 || r.State != "unknown" {
		t.Fatalf("health confused with dependency state: %+v", r)
	}
	if c := views[child.ID]; c.Group.ParentID != root.ID || c.Health == nil || c.Health.DownCount != 1 || c.Health.PausedCount != 1 {
		t.Fatal("nested counts", c)
	}
	if views[empty.ID].HealthState != "pending" || views[empty.ID].Health.LeafCount != 0 {
		t.Fatal("empty group became healthy")
	}
	w := maintenanceAPIRequest(n, "GET", "/api/groups", nil, viewerToken)
	for _, secret := range []string{up.Name, down.Name, paused.Name, "private fixture", adminToken, clusterToken} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("group summary copied private monitor data")
		}
	}
	storeMaintenance(t, n, plannedWork())
	if r := hierarchyViews(t, n, adminToken)[root.ID]; r.Health == nil || !r.Health.Maintenance || r.Health.MaintenanceCount != 2 || r.HealthState != "down" {
		t.Fatal("maintenance changed health or excluded a failing monitor", r)
	}
	hierarchySample(t, n, down, "pending", 3)
	if r := hierarchyViews(t, n, adminToken)[root.ID]; r.HealthState != "pending" || r.Health.PendingCount != 1 {
		t.Fatal("pending lost", r)
	}
	clock.Add(4000)
	if r := hierarchyViews(t, n, adminToken)[root.ID]; r.HealthState != "unknown" || r.Health.UnknownCount != 2 || r.Health.PausedCount != 1 {
		t.Fatal("stale evidence became healthy", r)
	}
	// One known failure still outranks another monitor's unknown observations.
	hierarchySample(t, n, down, "down", 4)
	if r := hierarchyViews(t, n, adminToken)[root.ID]; r.HealthState != "down" || r.Health.UnknownCount != 1 || r.Health.DownCount != 1 {
		t.Fatal("known down lost behind stale sibling", r)
	}
	if len(n.episodes) != 0 || len(n.attempts) != 0 || len(n.receipts) != 0 {
		t.Fatal("health reads created alert coordination")
	}
}

func TestGroupHierarchyLocalReferencesStrictInputAndRBAC(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := maintenanceNode(t, &clock, ":memory:")
	root := hierarchyGroup(t, n, "Root", "")
	child := hierarchyGroup(t, n, "Child", root.ID)
	root.ParentID = child.ID
	if w := maintenanceAPIRequest(n, "PUT", "/api/groups/"+root.ID, root, adminToken); w.Code != 400 {
		t.Fatal("cycle accepted", w.Code, w.Body.String())
	}
	for _, parent := range []string{root.ID, "not-arrived", "bad/identifier"} {
		changed := root
		changed.ParentID = parent
		if w := maintenanceAPIRequest(n, "PUT", "/api/groups/"+root.ID, changed, adminToken); w.Code != 400 {
			t.Fatal("invalid parent accepted", parent, w.Code)
		}
	}
	for _, extra := range []string{`"parentId":null`, `"ParentID":""`, `"parentId":"","parentId":""`, `"parentId":false`, `"futureEnvelope":{}`} {
		body := `{"name":"Strict","nodeIds":["a"],"graceMs":0,` + extra + `}`
		r := httptest.NewRequest("POST", "/api/groups", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+adminToken)
		w := httptest.NewRecorder()
		n.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal("ambiguous group input accepted", body, w.Code)
		}
	}
	n.accounts["viewer"] = account{AccountView: AccountView{ID: "viewer", Name: "Viewer", Role: "viewer"}, Hash: digest(viewerToken)}
	if w := maintenanceAPIRequest(n, "PUT", "/api/groups/"+child.ID, child, viewerToken); w.Code != 403 {
		t.Fatal("viewer altered parent", w.Code)
	}
	// A root reparenting repairs an inherited invalid chain, instead of forcing
	// the operator to delete descendants or discard any revision evidence.
	child.ParentID = ""
	if w := maintenanceAPIRequest(n, "PUT", "/api/groups/"+child.ID, child, adminToken); w.Code != 200 {
		t.Fatal("valid root reparent rejected", w.Code, w.Body.String())
	}
}

func TestGroupHierarchyConflictComponentsAndRemoteCyclesFailUnknown(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := maintenanceNode(t, &clock, ":memory:")
	root := hierarchyGroup(t, n, "Root", "")
	child := hierarchyGroup(t, n, "Child", root.ID)
	other := hierarchyGroup(t, n, "Unrelated", "")
	m := hierarchyMonitor(t, n, "Child monitor", child.ID, true)
	ok := hierarchyMonitor(t, n, "Other monitor", other.ID, true)
	hierarchySample(t, n, m, "up", 1)
	hierarchySample(t, n, ok, "up", 2)
	old := n.groups[child.ID][0]
	changed := old.Group
	changed.Revision, changed.ParentID = "parallel-parent", other.ID
	v := GroupVersion{Group: changed, Ancestors: []string{}}
	if err := n.applySnapshot(Snapshot{GroupVersions: []GroupVersion{v}}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{root.ID, child.ID, other.ID} {
		if view := hierarchyViews(t, n, adminToken)[id]; view.HealthState != "unknown" || view.Health != nil {
			t.Fatal("ambiguous parent selected a healthy component", id, view)
		}
	}
	// Resolve to the original parent using every current head.
	in := old.Group.GroupInput
	resolved, err := n.writeGroup(in, n.groups[child.ID], child.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if views := hierarchyViews(t, n, adminToken); views[root.ID].HealthState != "up" || views[other.ID].HealthState != "up" {
		t.Fatal("resolution did not restore independent summaries")
	}
	// Individually valid concurrent edits can form a cycle after replication.
	root.ParentID = resolved.ID
	if _, err := n.writeGroup(root.GroupInput, n.groups[root.ID], root.ID, false); err != nil {
		t.Fatal(err)
	}
	views := hierarchyViews(t, n, adminToken)
	if views[root.ID].HealthState != "unknown" || views[child.ID].HealthState != "unknown" || views[other.ID].HealthState != "up" || views[other.ID].Health == nil {
		t.Fatal("cycle poisoned unrelated component or appeared healthy", views)
	}
	if n.current(m.ID) == nil || n.current(ok.ID) == nil || len(n.groups[root.ID]) != 1 || len(n.groups[child.ID]) != 1 {
		t.Fatal("projection altered executable monitors or retained configuration")
	}
}

func hierarchyApplyPeer(t *testing.T, n *Node, s Snapshot) {
	t.Helper()
	s.Protocol, s.Node = peerProtocol, Identity{ID: "a", Name: "Private source"}
	if s.GroupVersions == nil {
		s.GroupVersions = []GroupVersion{}
	}
	if s.Resources == nil {
		s.Resources = []ResourceVersion{}
	}
	if s.Intents == nil {
		s.Intents = []NotificationIntent{}
	}
	if s.Versions == nil {
		s.Versions = []Version{}
	}
	if s.Observations == nil {
		s.Observations = []Sample{}
	}
	if s.Incidents == nil {
		s.Incidents = []Incident{}
	}
	if s.Receipts == nil {
		s.Receipts = []Receipt{}
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	checked, err := n.validateSnapshot(b, "a")
	if err != nil {
		t.Fatal("peer validation", err)
	}
	if err = n.applySnapshot(checked); err != nil {
		t.Fatal("peer application", err)
	}
}

func TestGroupHierarchyParentProofOrderingAndRestart(t *testing.T) {
	for _, order := range []string{"heads-first", "edges-first"} {
		t.Run(order, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(12, 1))
			source := maintenanceNode(t, &clock, ":memory:")
			root := hierarchyGroup(t, source, "Root", "")
			child := hierarchyGroup(t, source, "Child", root.ID)
			m := hierarchyMonitor(t, source, "Monitor", child.ID, true)
			hierarchySample(t, source, m, "up", 1)
			page, err := source.snapshotPage("", "")
			if err != nil || page.Next != "" {
				t.Fatal("fixture must fit one page", err)
			}
			heads, edges := page, Snapshot{RevisionEdges: page.RevisionEdges, RevisionClaims: page.RevisionClaims}
			heads.RevisionEdges, heads.RevisionClaims = nil, nil
			path := filepath.Join(t.TempDir(), "peer.db")
			open := func() *Node {
				n, err := New(Options{NodeID: "b", DBPath: path, Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: clock.Load, Peers: []Peer{{ID: "a", URL: "http://127.0.0.1:1"}}})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = n.Stop() })
				return n
			}
			n := open()
			first, second := heads, edges
			if order == "edges-first" {
				first, second = edges, heads
			}
			hierarchyApplyPeer(t, n, first)
			if order == "heads-first" {
				for _, v := range hierarchyViews(t, n, adminToken) {
					if v.HealthState != "unknown" || v.Health != nil {
						t.Fatal("missing proof became healthy", v)
					}
				}
			}
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = open()
			hierarchyApplyPeer(t, n, second)
			views := hierarchyViews(t, n, adminToken)
			if len(views) != 2 || views[root.ID].HealthState != "up" || views[root.ID].Health.LeafCount != 1 || views[child.ID].Group.ParentID != root.ID {
				t.Fatal("proof completion did not hydrate hierarchy", views)
			}
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = open()
			if view := hierarchyViews(t, n, adminToken)[root.ID]; view.HealthState != "up" || view.Health.UpCount != 1 {
				t.Fatal("restart lost hierarchy", view)
			}
		})
	}
}

func TestGroupHierarchySQLRollbackReparentAndTombstone(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := maintenanceNode(t, &clock, filepath.Join(t.TempDir(), "node.db"))
	left := hierarchyGroup(t, n, "Left", "")
	right := hierarchyGroup(t, n, "Right", "")
	child := hierarchyGroup(t, n, "Child", left.ID)
	m := hierarchyMonitor(t, n, "Monitor", child.ID, true)
	hierarchySample(t, n, m, "down", 1)
	old := n.groups[child.ID][0]
	edges := len(n.revisionEdges)
	child.ParentID = right.ID
	if _, err := n.store.db.Exec(`CREATE TEMP TRIGGER hierarchy_fail BEFORE INSERT ON records WHEN NEW.category='groupHeads' BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if w := maintenanceAPIRequest(n, "PUT", "/api/groups/"+child.ID, child, adminToken); w.Code != 503 {
		t.Fatal("failed transaction accepted", w.Code)
	}
	stored, _, err := loadOne[[]GroupVersion](n.store, "groupHeads", child.ID)
	if err != nil || !equalJSON(stored, []GroupVersion{old}) || !equalJSON(n.groups[child.ID], stored) || len(n.revisionEdges) != edges {
		t.Fatal("reparent escaped rollback", err)
	}
	if views := hierarchyViews(t, n, adminToken); views[left.ID].HealthState != "down" || views[right.ID].HealthState != "pending" {
		t.Fatal("failed reparent altered summary")
	}
	if _, err := n.store.db.Exec(`DROP TRIGGER hierarchy_fail`); err != nil {
		t.Fatal(err)
	}
	if w := maintenanceAPIRequest(n, "PUT", "/api/groups/"+child.ID, child, adminToken); w.Code != 200 {
		t.Fatal("retry failed", w.Code, w.Body.String())
	}
	if views := hierarchyViews(t, n, adminToken); views[left.ID].HealthState != "pending" || views[right.ID].HealthState != "down" {
		t.Fatal("reparent did not move exact health")
	}
	if w := maintenanceAPIRequest(n, "DELETE", "/api/groups/"+right.ID, map[string]string{"revision": right.Revision}, adminToken); w.Code != 204 {
		t.Fatal("parent deletion failed", w.Code)
	}
	if view := hierarchyViews(t, n, adminToken)[child.ID]; view.HealthState != "unknown" || view.Health != nil {
		t.Fatal("deleted parent made child hierarchy healthy", view)
	}
}

func TestGroupHierarchyCapacityIsProjectionOnly(t *testing.T) {
	n := &Node{}
	views := make([]GroupView, rollup.MaxNodes+1)
	for i := range views {
		views[i].Group.ID = fmt.Sprintf("g%d", i)
	}
	n.projectGroupHealth(views, nil)
	for _, view := range views {
		if view.HealthState != "unknown" || view.Health != nil || view.HealthReason == "" {
			t.Fatal("over-capacity projection fabricated health")
		}
	}
	if len(views) != rollup.MaxNodes+1 || len(n.groups) != 0 || len(n.heads) != 0 {
		t.Fatal("projection modified source configuration")
	}
}

func TestGroupHierarchyDoesNotInheritNotificationHolds(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := maintenanceNode(t, &clock, ":memory:")
	sink := webhook(t)
	root := hierarchyGroup(t, n, "Root", "")
	child := hierarchyGroup(t, n, "Child", root.ID)
	parentMonitor := hierarchyMonitor(t, n, "Dependency", root.ID, true)
	member := hierarchyMonitor(t, n, "Nested member", child.ID, true)
	for _, m := range []*Monitor{&parentMonitor, &member} {
		m.WebhookURL = sink.URL
		updated, err := n.writeMonitor(m.MonitorInput, n.heads[m.ID], m.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		*m = updated
	}
	root.OutageMonitorID = parentMonitor.ID
	if w := maintenanceAPIRequest(n, "PUT", "/api/groups/"+root.ID, root, adminToken); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	hierarchySample(t, n, parentMonitor, "down", 1)
	hierarchySample(t, n, member, "down", 2)
	n.mu.Lock()
	n.evaluate()
	n.mu.Unlock()
	n.wg.Wait()
	if sink.count("down") != 2 {
		t.Fatal("hierarchy silently suppressed a nested destination", sink.all())
	}
	n.mu.Lock()
	group, _, _ := n.suppression(member)
	views := n.groupViews()
	n.mu.Unlock()
	if group != "" {
		t.Fatal("nested member inherited parent suppression")
	}
	for _, view := range views {
		if view.HealthState != "down" || view.Health == nil || view.Health.DownCount == 0 {
			t.Fatal("successful notification hid failing health", view)
		}
		if view.Group.ID == root.ID && !view.SuppressionActive {
			t.Fatal("direct dependency delivery was not established for the control")
		}
		if view.Group.ID == child.ID && view.SuppressionActive {
			t.Fatal("child inherited alert policy")
		}
	}
}
