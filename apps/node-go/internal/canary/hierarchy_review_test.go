package canary

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"uptime-canary/apps/node-go/internal/rollup"
)

// Independent integration review. No production implementation or original
// assertion is replaced. Nodes are never started; requests use in-memory HTTP
// handlers and databases belong to this test's temporary directory.

func hierarchyReviewNode(t *testing.T, clock *atomic.Int64, id, path string) *Node {
	t.Helper()
	o := Options{NodeID: id, DBPath: path, Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: clock.Load, NotificationDelayMs: Ptr(int64(0))}
	for _, peer := range []string{"a", "b", "c"} {
		if peer != id {
			o.Peers = append(o.Peers, Peer{ID: peer, URL: "http://127.0.0.1:1"})
		}
	}
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	return n
}

func hierarchyReviewPage(t *testing.T, n *Node) Snapshot {
	t.Helper()
	s, err := n.snapshotPage("", "")
	if err != nil || s.Next != "" {
		t.Fatal("small review fixture must fit one page", err, s.Next)
	}
	return s
}

func hierarchyReviewPeer(t *testing.T, n *Node, sender string, s Snapshot) {
	t.Helper()
	s.Protocol, s.Node = peerProtocol, Identity{ID: sender, Name: "Review source"}
	if s.Versions == nil {
		s.Versions = []Version{}
	}
	if s.GroupVersions == nil {
		s.GroupVersions = []GroupVersion{}
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
	body, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/internal/snapshot", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+clusterToken)
	r.Header.Set("X-Canary-Node-Id", sender)
	w := httptest.NewRecorder()
	n.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("validated peer page: %d %s", w.Code, w.Body.Bytes())
	}
}

func hierarchyReviewUpdate(t *testing.T, n *Node, g Group, parent string) Group {
	t.Helper()
	g.ParentID = parent
	w := maintenanceAPIRequest(n, "PUT", "/api/groups/"+g.ID, g, adminToken)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &g) != nil {
		t.Fatal("update group", w.Code, w.Body.String())
	}
	return g
}

func hierarchyReviewHealth(t *testing.T, n *Node, unknown, usable []string) {
	t.Helper()
	views := hierarchyViews(t, n, adminToken)
	for _, id := range unknown {
		v, ok := views[id]
		if !ok || v.HealthState != "unknown" || v.Health != nil || v.HealthReason == "" {
			t.Fatalf("component must omit counts: %s %+v", id, v)
		}
	}
	for _, id := range usable {
		v, ok := views[id]
		if !ok || v.Health == nil || v.HealthReason != "" {
			t.Fatalf("unrelated/repaired component must remain evaluable: %s %+v", id, v)
		}
	}
}

func TestHierarchyReviewValidLocalEditsCyclicUnionRestartAndRepair(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	pathA, pathB := filepath.Join(t.TempDir(), "a.db"), filepath.Join(t.TempDir(), "b.db")
	a, b := hierarchyReviewNode(t, &clock, "a", pathA), hierarchyReviewNode(t, &clock, "b", pathB)
	left := hierarchyGroup(t, a, "Left", "")
	right := hierarchyGroup(t, a, "Right", "")
	child := hierarchyGroup(t, a, "Descendant", left.ID)
	other := hierarchyGroup(t, a, "Separate", "")
	m := hierarchyMonitor(t, a, "Child monitor", child.ID, true)
	ok := hierarchyMonitor(t, a, "Separate monitor", other.ID, true)
	hierarchySample(t, a, m, "up", 1)
	hierarchySample(t, a, ok, "up", 2)
	hierarchyReviewPeer(t, b, "a", hierarchyReviewPage(t, a))
	// Each edit passes ordinary authenticated local reference admission against
	// its partition's acyclic graph. Their union, on different IDs, is cyclic.
	left = hierarchyReviewUpdate(t, a, left, right.ID)
	right = hierarchyReviewUpdate(t, b, right, left.ID)
	pageA, pageB := hierarchyReviewPage(t, a), hierarchyReviewPage(t, b)
	hierarchyReviewPeer(t, a, "b", pageB)
	hierarchyReviewPeer(t, b, "a", pageA)
	for _, n := range []*Node{a, b} {
		hierarchyReviewHealth(t, n, []string{left.ID, right.ID, child.ID}, []string{other.ID})
		if n.currentGroup(left.ID).ParentID != right.ID || n.currentGroup(right.ID).ParentID != left.ID || n.current(m.ID) == nil {
			t.Fatal("valid replicated union was rewritten or rejected")
		}
		if hierarchyViews(t, n, adminToken)[other.ID].HealthState != "up" {
			t.Fatal("unrelated healthy group poisoned")
		}
	}
	if err := a.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := b.Stop(); err != nil {
		t.Fatal(err)
	}
	a, b = hierarchyReviewNode(t, &clock, "a", pathA), hierarchyReviewNode(t, &clock, "b", pathB)
	for _, n := range []*Node{a, b} {
		hierarchyReviewHealth(t, n, []string{left.ID, right.ID, child.ID}, []string{other.ID})
	}
	left = hierarchyReviewUpdate(t, a, *a.currentGroup(left.ID), "")
	hierarchyReviewPeer(t, b, "a", hierarchyReviewPage(t, a))
	for _, n := range []*Node{a, b} {
		hierarchyReviewHealth(t, n, nil, []string{left.ID, right.ID, child.ID, other.ID})
		if v := hierarchyViews(t, n, adminToken)[left.ID]; v.HealthState != "pending" || v.Health.LeafCount != 1 || v.Health.UpCount != 1 || v.Health.DirectChildCount != 2 {
			t.Fatal("empty sibling must remain a pending structural child", v)
		}
	}
}

func TestHierarchyReviewCandidateProofOrderingTombstoneAndPlacements(t *testing.T) {
	for _, firstProof := range []bool{false, true} {
		t.Run(fmt.Sprintf("proof-first=%v", firstProof), func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(12, 1))
			a := hierarchyReviewNode(t, &clock, "a", ":memory:")
			path := filepath.Join(t.TempDir(), "receiver.db")
			b := hierarchyReviewNode(t, &clock, "b", path)
			left := hierarchyGroup(t, a, "Left", "")
			right := hierarchyGroup(t, a, "Right", "")
			child := hierarchyGroup(t, a, "Child", left.ID)
			separate := hierarchyGroup(t, a, "Separate", "")
			m := hierarchyMonitor(t, a, "Member", child.ID, true)
			hierarchySample(t, a, m, "up", 1)
			hierarchyReviewPeer(t, b, "a", hierarchyReviewPage(t, a))
			child = hierarchyReviewUpdate(t, a, child, right.ID)
			page := hierarchyReviewPage(t, a)
			heads, proof := page, Snapshot{RevisionEdges: page.RevisionEdges, RevisionClaims: page.RevisionClaims, EvidenceLinks: page.EvidenceLinks}
			heads.RevisionEdges, heads.RevisionClaims, heads.EvidenceLinks = nil, nil, nil
			first, second := heads, proof
			if firstProof {
				first, second = proof, heads
			}
			hierarchyReviewPeer(t, b, "a", first)
			if !firstProof {
				hierarchyReviewHealth(t, b, []string{left.ID, right.ID, child.ID}, []string{separate.ID})
			}
			if err := b.Stop(); err != nil {
				t.Fatal(err)
			}
			b = hierarchyReviewNode(t, &clock, "b", path)
			hierarchyReviewPeer(t, b, "a", second)
			hierarchyReviewHealth(t, b, nil, []string{left.ID, right.ID, child.ID, separate.ID})
			if views := hierarchyViews(t, b, adminToken); views[right.ID].HealthState != "up" || views[left.ID].HealthState != "pending" {
				t.Fatal("late proof failed to move health")
			}

			// Valid concurrent monitor placements must invalidate every possible
			// component, including a placement whose candidate is paused.
			move := m
			move.GroupID = left.ID
			w := maintenanceAPIRequest(a, "PUT", "/api/monitors/"+m.ID, move, adminToken)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			move.GroupID, move.Enabled = right.ID, false
			w = maintenanceAPIRequest(b, "PUT", "/api/monitors/"+m.ID, move, adminToken)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			pa, pb := hierarchyReviewPage(t, a), hierarchyReviewPage(t, b)
			hierarchyReviewPeer(t, a, "b", pb)
			hierarchyReviewPeer(t, b, "a", pa)
			for _, n := range []*Node{a, b} {
				hierarchyReviewHealth(t, n, []string{left.ID, right.ID, child.ID}, []string{separate.ID})
				if len(n.heads[m.ID]) != 2 {
					t.Fatal("monitor conflict discarded")
				}
			}
			// Deleting a parent never detaches or deletes the still-live child.
			w = maintenanceAPIRequest(a, "DELETE", "/api/groups/"+right.ID, map[string]string{"revision": right.Revision}, adminToken)
			if w.Code != 204 {
				t.Fatal(w.Code, w.Body.String())
			}
			hierarchyReviewPeer(t, b, "a", hierarchyReviewPage(t, a))
			if b.currentGroup(right.ID) != nil || b.currentGroup(child.ID).ParentID != right.ID || len(b.heads[m.ID]) != 2 {
				t.Fatal("tombstone erased child references or candidates")
			}
			hierarchyReviewHealth(t, b, []string{left.ID, child.ID}, []string{separate.ID})
		})
	}
}

func TestHierarchyReviewStrictParentInputRBACResolutionAndAdmission(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	a, b := hierarchyReviewNode(t, &clock, "a", ":memory:"), hierarchyReviewNode(t, &clock, "b", ":memory:")
	root := hierarchyGroup(t, a, "Root", "")
	child := hierarchyGroup(t, a, "Child", root.ID)
	for _, extra := range []string{
		`"parentId":null`, `"parentId":true`, `"parentId":0`, `"parentId":[]`, `"parentId":{}`,
		`"ParentId":""`, `"PARENTID":""`, `"parentId":"","parentId":""`, `"parentId":"","parent\u0049d":""`,
		`"parentId":"with space"`, `"parentId":"../root"`, `"parentId":"constructor"`, `"unexpected":0`,
	} {
		body := `{"name":"Strict","nodeIds":["a"],"graceMs":0,` + extra + `}`
		r := httptest.NewRequest("POST", "/api/groups", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+adminToken)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("ambiguous exact parent input admitted: %s => %d", body, w.Code)
		}
	}
	for _, token := range []string{viewerToken, editorToken} {
		role := "viewer"
		if token == editorToken {
			role = "editor"
		}
		a.accounts[role] = account{AccountView: AccountView{ID: role, Name: role, Role: role}, Hash: digest(token)}
	}
	for _, op := range []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/groups", GroupInput{Name: "Denied", NodeIDs: []string{"a"}}},
		{"PUT", "/api/groups/" + child.ID, child},
		{"DELETE", "/api/groups/" + child.ID, map[string]string{"revision": child.Revision}},
		{"POST", "/api/groups/" + child.ID + "/resolve", child},
	} {
		if w := maintenanceAPIRequest(a, op.method, op.path, op.body, viewerToken); w.Code != 403 {
			t.Fatal("viewer write admitted", op.method, op.path, w.Code)
		}
		if w := maintenanceAPIRequest(a, op.method, op.path, op.body, ""); w.Code != 401 {
			t.Fatal("unauthenticated hierarchy access", w.Code)
		}
	}
	// A valid escaped spelling still denotes exactly the documented key.
	if g, err := parseGroup([]byte(`{"name":"Escaped","nodeIds":["a"],"graceMs":0,"parent\u0049d":""}`), a.known); err != nil || g.ParentID != "" {
		t.Fatal("valid exact JSON key escape rejected", err)
	}
	hierarchyReviewPeer(t, b, "a", hierarchyReviewPage(t, a))
	child = hierarchyReviewUpdate(t, a, child, "")
	other := *b.currentGroup(child.ID)
	other.Name = "Concurrent edit"
	w := maintenanceAPIRequest(b, "PUT", "/api/groups/"+child.ID, other, adminToken)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	hierarchyReviewPeer(t, a, "b", hierarchyReviewPage(t, b))
	if len(a.groups[child.ID]) != 2 {
		t.Fatal("setup did not create genuine conflict")
	}
	if w := maintenanceAPIRequest(a, "POST", "/api/groups", GroupInput{Name: "Invalid parent", NodeIDs: []string{"a"}, ParentID: child.ID}, adminToken); w.Code != 400 {
		t.Fatal("conflicted parent locally admitted", w.Code)
	}
	revisions := []string{}
	for _, v := range a.groups[child.ID] {
		revisions = append(revisions, v.Group.Revision)
	}
	resolution := map[string]any{"name": "Resolved", "nodeIds": []string{"a"}, "graceMs": 0, "parentId": root.ID, "revisions": revisions[:1]}
	if w := maintenanceAPIRequest(a, "POST", "/api/groups/"+child.ID+"/resolve", resolution, editorToken); w.Code != 409 {
		t.Fatal("partial head-set resolution admitted", w.Code)
	}
	resolution["revisions"] = revisions
	resolution["parentId"] = child.ID
	if w := maintenanceAPIRequest(a, "POST", "/api/groups/"+child.ID+"/resolve", resolution, editorToken); w.Code != 400 {
		t.Fatal("cyclic resolution admitted", w.Code)
	}
	resolution["parentId"] = root.ID
	if w := maintenanceAPIRequest(a, "POST", "/api/groups/"+child.ID+"/resolve", resolution, editorToken); w.Code != 200 {
		t.Fatal("valid editor resolution rejected", w.Code, w.Body.String())
	}
	if a.currentGroup(child.ID) == nil || a.currentGroup(child.ID).ParentID != root.ID {
		t.Fatal("resolution failed to commit exact parent")
	}
}

// This reference uses explicit undirected adjacency and a component walk,
// then enumerates each requested subtree. It neither uses union-find nor calls
// Compile/Evaluate, and never folds a production child's rollup into a parent.
func hierarchyReviewReference(n *Node, views []GroupView, monitors []MonitorView) map[string]*rollup.Result {
	groups, adjacent, invalid := map[string]Group{}, map[string][]string{}, map[string]bool{}
	for _, v := range views {
		groups[v.Group.ID] = v.Group
	}
	for id := range groups {
		heads := n.groups[id]
		invalid[id] = len(heads) != 1 || heads[0].EdgeID != ""
		for _, h := range heads {
			if h.Group.Deleted || h.Group.ParentID == "" {
				continue
			}
			if _, ok := groups[h.Group.ParentID]; !ok {
				invalid[id] = true
				continue
			}
			adjacent[id] = append(adjacent[id], h.Group.ParentID)
			adjacent[h.Group.ParentID] = append(adjacent[h.Group.ParentID], id)
		}
	}
	for _, m := range monitors {
		if !m.ConfigConflict {
			continue
		}
		for _, h := range n.heads[m.Monitor.ID] {
			if !h.Monitor.Deleted {
				invalid[h.Monitor.GroupID] = true
			}
		}
	}
	for start := range groups {
		seen := map[string]bool{}
		for id := start; id != ""; id = groups[id].ParentID {
			if seen[id] {
				invalid[start] = true
				break
			}
			seen[id] = true
			if _, ok := groups[id]; !ok {
				break
			}
		}
	}
	out := map[string]*rollup.Result{}
	for start := range groups {
		component, queue, bad := map[string]bool{}, []string{start}, false
		for len(queue) != 0 {
			id := queue[0]
			queue = queue[1:]
			if component[id] {
				continue
			}
			component[id], bad = true, bad || invalid[id]
			queue = append(queue, adjacent[id]...)
		}
		if bad {
			out[start] = nil
			continue
		}
		subtree := map[string]bool{start: true}
		for changed := true; changed; {
			changed = false
			for id, g := range groups {
				if !subtree[id] && g.ParentID != "" && subtree[g.ParentID] {
					subtree[id], changed = true, true
				}
			}
		}
		r := &rollup.Result{State: rollup.Up, DescendantCount: len(subtree) - 1}
		activeChildren := map[string]int{}
		for id := range subtree {
			if id != start {
				activeChildren[groups[id].ParentID]++
			}
			if groups[id].ParentID == start {
				r.DirectChildCount++
				r.ActiveChildCount++
			}
		}
		rank := map[rollup.State]int{rollup.Up: 0, rollup.Pending: 1, rollup.Unknown: 2, rollup.Down: 3}
		for _, v := range monitors {
			m := v.Monitor
			if v.ConfigConflict || !subtree[m.GroupID] {
				continue
			}
			r.LeafCount++
			r.DescendantCount++
			if m.GroupID == start {
				r.DirectChildCount++
			}
			if !m.Enabled {
				r.PausedCount++
				continue
			}
			activeChildren[m.GroupID]++
			if m.GroupID == start {
				r.ActiveChildCount++
			}
			r.ActiveLeafCount++
			switch v.State {
			case "up":
				r.UpCount++
			case "down":
				r.DownCount++
			case "pending":
				r.PendingCount++
			default:
				r.UnknownCount++
			}
			if rank[rollup.State(v.State)] > rank[r.State] {
				r.State = rollup.State(v.State)
			}
			if len(v.MaintenanceIDs) != 0 {
				r.MaintenanceCount++
			}
		}
		for id := range subtree {
			if activeChildren[id] == 0 && rank[r.State] < rank[rollup.Pending] {
				r.State = rollup.Pending
			}
		}
		r.Maintenance = r.MaintenanceCount != 0
		out[start] = r
	}
	return out
}

func TestHierarchyReviewIndependentProjectionOracleAndCandidateOrders(t *testing.T) {
	rng := rand.New(rand.NewSource(203617161))
	ids := []string{"same", "group:same", "monitor:same", "Other", "other", "isolated"}
	for trial := 0; trial < 4096; trial++ {
		n := &Node{groups: map[string][]GroupVersion{}, heads: map[string][]Version{}}
		groups, monitors := []GroupView{}, []MonitorView{}
		parents := append([]string{"", "missing"}, ids[:5]...)
		for i, id := range ids {
			count := 1
			if i != 5 && rng.Intn(5) == 0 {
				count = 2 + rng.Intn(2)
			}
			for j := 0; j < count; j++ {
				g := Group{ID: id, Revision: fmt.Sprintf("r%d", j), GroupInput: GroupInput{ParentID: parents[rng.Intn(len(parents))]}}
				v := GroupVersion{Group: g}
				if i == 5 {
					v.Group.ParentID = ""
				} else {
					v.Group.Deleted = rng.Intn(7) == 0
					if rng.Intn(11) == 0 {
						v.EdgeID = "missing-proof"
					}
				}
				n.groups[id] = append(n.groups[id], v)
			}
		}
		for i := 0; i < 9; i++ {
			id := fmt.Sprintf("m%d", i)
			if i < len(ids) {
				id = ids[i]
			} // Valid cross-domain ID collisions.
			count := 1
			if i < 8 && rng.Intn(4) == 0 {
				count = 2 + rng.Intn(2)
			}
			for j := 0; j < count; j++ {
				m := Monitor{ID: id, Revision: fmt.Sprintf("m-r%d", j), MonitorInput: MonitorInput{GroupID: parents[rng.Intn(len(parents))], Enabled: rng.Intn(3) != 0}}
				v := Version{Monitor: m}
				if i == 8 {
					v.Monitor.GroupID = "isolated"
					v.Monitor.Enabled = true
				} else {
					v.Monitor.Deleted = rng.Intn(7) == 0
					if rng.Intn(11) == 0 {
						v.EdgeID = "missing-proof"
					}
				}
				n.heads[id] = append(n.heads[id], v)
			}
		}
		monitorIDs := make([]string, 0, len(n.heads))
		for id := range n.heads {
			monitorIDs = append(monitorIDs, id)
		}
		slices.Sort(monitorIDs)
		for order := 0; order < 3; order++ {
			groups, monitors = groups[:0], monitors[:0]
			for _, id := range ids {
				heads := n.groups[id]
				if order != 0 {
					slices.Reverse(heads)
				}
				for _, h := range heads {
					if !h.Group.Deleted {
						groups = append(groups, GroupView{Group: h.Group, ConfigConflict: len(heads) != 1 || heads[0].EdgeID != ""})
						break
					}
				}
			}
			for _, monitorID := range monitorIDs {
				heads := n.heads[monitorID]
				if order != 0 {
					slices.Reverse(heads)
				}
				for _, h := range heads {
					if !h.Monitor.Deleted {
						v := MonitorView{Monitor: h.Monitor, ConfigConflict: len(heads) != 1 || heads[0].EdgeID != "", State: []string{"up", "down", "pending", "unknown"}[rng.Intn(4)]}
						if rng.Intn(2) == 0 {
							v.MaintenanceIDs = []string{"private-maintenance-id"}
						}
						monitors = append(monitors, v)
						break
					}
				}
			}
			rng.Shuffle(len(groups), func(i, j int) { groups[i], groups[j] = groups[j], groups[i] })
			rng.Shuffle(len(monitors), func(i, j int) { monitors[i], monitors[j] = monitors[j], monitors[i] })
			expected := hierarchyReviewReference(n, groups, monitors)
			before, _ := json.Marshal([]any{n.groups, n.heads, monitors})
			n.projectGroupHealth(groups, monitors)
			after, _ := json.Marshal([]any{n.groups, n.heads, monitors})
			if !bytes.Equal(before, after) {
				t.Fatal("projection mutated configuration or monitor views")
			}
			for _, g := range groups {
				want := expected[g.Group.ID]
				if want == nil {
					if g.HealthState != "unknown" || g.Health != nil || g.HealthReason == "" {
						t.Fatalf("trial=%d order=%d invalid component %+v", trial, order, g)
					}
					continue
				}
				if !reflect.DeepEqual(g.Health, want) || g.HealthState != string(want.State) || g.HealthReason != "" {
					t.Fatalf("trial=%d order=%d group=%s got=%+v want=%+v", trial, order, g.Group.ID, g, want)
				}
			}
		}
	}
}

func TestHierarchyReviewProjectionCapacityBoundaryAndRecovery(t *testing.T) {
	n := &Node{groups: map[string][]GroupVersion{}}
	groups := []GroupView{{Group: Group{ID: "same"}}}
	n.groups["same"] = []GroupVersion{{Group: groups[0].Group}}
	monitors := make([]MonitorView, rollup.MaxNodes-1)
	for i := range monitors {
		monitors[i] = MonitorView{Monitor: Monitor{ID: fmt.Sprintf("m%d", i), MonitorInput: MonitorInput{GroupID: "same", Enabled: true}}, State: "up"}
	}
	monitors[0].Monitor.ID = "same"
	n.projectGroupHealth(groups, monitors)
	if v := groups[0]; v.HealthState != "up" || v.Health == nil || v.Health.UpCount != rollup.MaxNodes-1 {
		t.Fatal("inclusive combined cap or cross-domain identity failed", v)
	}
	monitors = append(monitors, MonitorView{Monitor: Monitor{ID: "overflow", MonitorInput: MonitorInput{GroupID: "same", Enabled: true}}, State: "down"})
	n.projectGroupHealth(groups, monitors)
	if v := groups[0]; v.HealthState != "unknown" || v.Health != nil || !strings.Contains(v.HealthReason, "capacity") {
		t.Fatal("overflow reused prior healthy counts", v)
	}
	if len(monitors) != rollup.MaxNodes || len(n.groups) != 1 {
		t.Fatal("overflow trimmed source union")
	}
	n.projectGroupHealth(groups, monitors[:1])
	if v := groups[0]; v.HealthState != "up" || v.Health == nil || v.Health.UpCount != 1 {
		t.Fatal("later small projection reused stale overflow result", v)
	}
}

func TestHierarchyReviewAllHealthEnableMaintenanceCombinations(t *testing.T) {
	states := []string{"up", "down", "pending", "unknown"}
	for empty := 0; empty < 2; empty++ {
		groups := []GroupView{{Group: Group{ID: "same"}}, {Group: Group{ID: "monitor:same", GroupInput: GroupInput{ParentID: "same"}}}}
		if empty != 0 {
			groups = append(groups, GroupView{Group: Group{ID: "empty", GroupInput: GroupInput{ParentID: "monitor:same"}}})
		}
		n := &Node{groups: map[string][]GroupVersion{}}
		for _, g := range groups {
			n.groups[g.Group.ID] = []GroupVersion{{Group: g.Group}}
		}
		for evidence := 0; evidence < 64; evidence++ {
			for enabled := 0; enabled < 8; enabled++ {
				for maintenance := 0; maintenance < 8; maintenance++ {
					monitors := make([]MonitorView, 3)
					code := evidence
					for i := range monitors {
						group := "same"
						if i != 0 {
							group = "monitor:same"
						}
						monitors[i] = MonitorView{Monitor: Monitor{ID: []string{"same", "monitor:same", "group:same"}[i], MonitorInput: MonitorInput{GroupID: group, Enabled: enabled&(1<<i) != 0}}, State: states[code%4]}
						code /= 4
						if maintenance&(1<<i) != 0 {
							monitors[i].MaintenanceIDs = []string{"annotation"}
						}
					}
					want := hierarchyReviewReference(n, groups, monitors)
					n.projectGroupHealth(groups, monitors)
					for _, g := range groups {
						if !reflect.DeepEqual(g.Health, want[g.Group.ID]) || g.HealthState != string(want[g.Group.ID].State) {
							t.Fatalf("empty=%d evidence=%d enabled=%d maintenance=%d group=%s got=%+v want=%+v", empty, evidence, enabled, maintenance, g.Group.ID, g.Health, want[g.Group.ID])
						}
					}
				}
			}
		}
	}
}

func TestHierarchyReviewLocalAdmissionAndValidReplicaUnionAreSeparate(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	a, b := hierarchyReviewNode(t, &clock, "a", ":memory:"), hierarchyReviewNode(t, &clock, "b", ":memory:")
	for _, n := range []*Node{a, b} {
		parent := ""
		for i := 0; i < maxGroups; i++ {
			g := hierarchyGroup(t, n, fmt.Sprintf("%s-%03d", n.ID, i), parent)
			parent = g.ID
		}
		w := maintenanceAPIRequest(n, "POST", "/api/groups", GroupInput{Name: "Above local admission", NodeIDs: []string{"a"}}, adminToken)
		if w.Code != 409 || len(n.groups) != maxGroups {
			t.Fatal("local admission boundary changed", w.Code, len(n.groups))
		}
	}
	for _, direction := range [][2]*Node{{a, b}, {b, a}} {
		source, receiver := direction[0], direction[1]
		cursor := ""
		for pageNumber := 0; ; pageNumber++ {
			if pageNumber >= 16 {
				t.Fatal("review sweep exceeded bounded pagination")
			}
			page, err := source.snapshotPage(cursor, "")
			if err != nil {
				t.Fatal(err)
			}
			hierarchyReviewPeer(t, receiver, source.ID, page)
			if page.Next == "" {
				break
			}
			if page.Next <= cursor {
				t.Fatal("peer cursor failed to advance")
			}
			cursor = page.Next
		}
	}
	for _, n := range []*Node{a, b} {
		if len(n.groups) != 2*maxGroups {
			t.Fatal("valid union trimmed to local admission", len(n.groups))
		}
		views := hierarchyViews(t, n, adminToken)
		if len(views) != 2*maxGroups {
			t.Fatal("projection hid valid union")
		}
		for _, v := range views {
			if v.HealthState != "pending" || v.Health == nil || v.Health.LeafCount != 0 || v.ConfigConflict {
				t.Fatal("valid replicated forest failed evaluation", v)
			}
		}
	}
}

func TestHierarchyReviewConcurrentReadsAndReparent(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := hierarchyReviewNode(t, &clock, "a", ":memory:")
	left := hierarchyGroup(t, n, "Left", "")
	right := hierarchyGroup(t, n, "Right", "")
	child := hierarchyGroup(t, n, "Child", left.ID)
	m := hierarchyMonitor(t, n, "Member", child.ID, true)
	hierarchySample(t, n, m, "up", 1)
	childID := child.ID
	var workers sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 40 {
				w := maintenanceAPIRequest(n, "GET", "/api/overview", nil, adminToken)
				var o Overview
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &o) != nil {
					t.Error("concurrent overview failed", w.Code)
					return
				}
				up, leaves := 0, 0
				for _, g := range o.Groups {
					if g.Group.ID == childID {
						continue
					}
					if g.Health == nil {
						t.Error("valid concurrent hierarchy lost counts")
						return
					}
					up += g.Health.UpCount
					leaves += g.Health.LeafCount
				}
				if up != 1 || leaves != 1 {
					t.Error("overview mixed multiple hierarchy revisions", up, leaves)
					return
				}
			}
		}()
	}
	for i := 0; i < 30; i++ {
		parent := left.ID
		if i%2 == 0 {
			parent = right.ID
		}
		child = hierarchyReviewUpdate(t, n, child, parent)
	}
	workers.Wait()
}

func TestHierarchyReviewDirectMaintenanceAndReadOnlyOverview(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := hierarchyReviewNode(t, &clock, "a", ":memory:")
	root := hierarchyGroup(t, n, "Root", "")
	child := hierarchyGroup(t, n, "Child", root.ID)
	direct := hierarchyMonitor(t, n, "Direct private member", root.ID, true)
	nested := hierarchyMonitor(t, n, "Nested private member", child.ID, true)
	hierarchySample(t, n, direct, "down", 1)
	hierarchySample(t, n, nested, "down", 2)
	work := plannedWork()
	work.AllMonitors, work.GroupIDs = false, []string{root.ID}
	storeMaintenance(t, n, work)
	n.accounts["viewer"] = account{AccountView: AccountView{ID: "viewer", Name: "Viewer", Role: "viewer"}, Hash: digest(viewerToken)}
	logical := func() []byte {
		b, err := json.Marshal([]any{n.groups, n.heads, n.samples, n.episodes, n.intents, n.attempts, n.receipts, n.revisionEdges, n.revisionClaims, n.incidentFacts, n.resources, n.nextProbe})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	durable := func() string {
		rows, err := n.store.db.Query(`SELECT category,id,payload FROM records ORDER BY category,id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out strings.Builder
		for rows.Next() {
			var category, id, payload string
			if err := rows.Scan(&category, &id, &payload); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&out, "%q %q %q\n", category, id, payload)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		var history int
		if err := n.store.db.QueryRow(`SELECT count(*) FROM history`).Scan(&history); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&out, "history=%d", history)
		return out.String()
	}
	// Prime only derived maintenance/reachability caches before comparison.
	_ = n.overview(n.accounts["viewer"])
	before, stored := logical(), durable()
	for range 8 {
		for _, path := range []string{"/api/groups", "/api/overview"} {
			w := maintenanceAPIRequest(n, "GET", path, nil, viewerToken)
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			if path == "/api/groups" {
				for _, secret := range []string{direct.Name, nested.Name, "private fixture", adminToken, clusterToken} {
					if strings.Contains(w.Body.String(), secret) {
						t.Fatal("group projection copied private detail", secret)
					}
				}
			}
		}
	}
	if !bytes.Equal(before, logical()) || stored != durable() {
		t.Fatal("health reads changed logical state, persisted records, or raw history")
	}
	o := n.overview(n.accounts["viewer"])
	for _, m := range o.Monitors {
		if (m.Monitor.ID == direct.ID) != (len(m.MaintenanceIDs) > 0) {
			t.Fatal("maintenance inherited through parent", m)
		}
	}
	for _, g := range o.Groups {
		want := 0
		if g.Group.ID == root.ID {
			want = 1
		}
		if g.Health == nil || g.HealthState != "down" || g.Health.MaintenanceCount != want || g.SuppressionActive {
			t.Fatal("maintenance rewrote health/dependency policy", g)
		}
	}
}
