package canary

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

// A retained identity can restore the same snapshot repeatedly. Each restore
// and subsequent edit makes another genuine branch; static membership does not
// bound the number of these simultaneously observed tips.
func revisionJoinsSeed(t *testing.T, n *Node, kind string, count int, freshGenerations bool) []string {
	t.Helper()
	id := "join-" + kind
	rootRevision := "root-" + kind
	s := Snapshot{}
	tips := []string{}
	var monitor Monitor
	var group Group
	var resource Resource
	switch kind {
	case "monitor":
		in := input([]string{"a", "b"}, "http://127.0.0.1:1", "")
		in.Enabled = false
		if err := validateMonitor(&in, n.known); err != nil {
			t.Fatal(err)
		}
		monitor = Monitor{MonitorInput: in, ID: id, Revision: rootRevision, UpdatedAt: 10}
		s.RevisionEdges = append(s.RevisionEdges, newRevisionEdge(kind, id, rootRevision, monitor, nil, rootRevision))
	case "group":
		group = Group{GroupInput: GroupInput{Name: "Join group", NodeIDs: []string{"a", "b"}}, ID: id, Revision: rootRevision, UpdatedAt: 10}
		s.RevisionEdges = append(s.RevisionEdges, newRevisionEdge(kind, id, rootRevision, group, nil, ""))
	default:
		var data any
		if kind == resourceChannel {
			c := hookChannel("Join channel", "http://127.0.0.1:1/hook")
			c.Enabled = false
			data = c
		} else {
			m := plannedWork()
			m.Enabled = false
			data = m
		}
		b, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		resource = Resource{Kind: kind, ID: id, Revision: rootRevision, UpdatedAt: 10, Data: b}
		s.RevisionEdges = append(s.RevisionEdges, newRevisionEdge(kind, id, rootRevision, resource, nil, ""))
	}
	for index := range count {
		parent := rootRevision
		for stage := range 2 {
			rev := fmt.Sprintf("restore-%03d-edit-%d", index, stage)
			parents := []string{parent}
			var e RevisionEdge
			switch kind {
			case "monitor":
				m := monitor
				m.Revision, m.Name, m.UpdatedAt = rev, rev, int64(20+index*2+stage)
				m.CheckGeneration = rootRevision
				if freshGenerations {
					m.CheckGeneration = fmt.Sprintf("restore-%03d-edit-0", index)
					if stage == 0 {
						m.CheckGeneration = ""
					}
				}
				e = newRevisionEdge(kind, id, rev, m, parents, checkGeneration(m))
				if stage == 1 {
					s.Versions = append(s.Versions, Version{Monitor: m, Ancestors: e.Parents, EdgeID: e.ID})
				}
			case "group":
				g := group
				g.Revision, g.Name, g.UpdatedAt = rev, rev, int64(20+index*2+stage)
				e = newRevisionEdge(kind, id, rev, g, parents, "")
				if stage == 1 {
					s.GroupVersions = append(s.GroupVersions, GroupVersion{Group: g, Ancestors: e.Parents, EdgeID: e.ID})
				}
			default:
				r := resource
				r.Revision, r.UpdatedAt = rev, int64(20+index*2+stage)
				e = newRevisionEdge(kind, id, rev, r, parents, "")
				if stage == 1 {
					s.Resources = append(s.Resources, ResourceVersion{Resource: r, Ancestors: e.Parents, EdgeID: e.ID})
				}
			}
			s.RevisionEdges = append(s.RevisionEdges, e)
			parent = rev
		}
		tips = append(tips, parent)
	}
	if count == 0 {
		return tips
	}
	revisionJoinsApply(t, n, s)
	return tips
}

func revisionJoinsApply(t *testing.T, n *Node, s Snapshot) {
	t.Helper()
	if err := n.applySnapshot(cr2Decode(t, n, s)); err != nil {
		t.Fatal(err)
	}
}

func revisionJoinsResolve(t *testing.T, n *Node, kind string, semantic, deleted bool) Snapshot {
	t.Helper()
	id := "join-" + kind
	revision := "chosen-" + kind
	s := Snapshot{}
	var err error
	switch kind {
	case "monitor":
		m := n.heads[id][0].Monitor
		m.Revision, m.Name, m.UpdatedAt, m.Deleted = revision, "Chosen monitor", 1000, deleted
		if semantic {
			m.Target = "http://127.0.0.1:2"
		}
		if err = validateMonitor(&m.MonitorInput, n.known); err != nil {
			t.Fatal(err)
		}
		var v Version
		v, s.RevisionEdges, err = n.joinMonitorRevision(m, n.heads[id])
		s.Versions = []Version{v}
	case "group":
		g := n.groups[id][0].Group
		g.Revision, g.Name, g.UpdatedAt, g.Deleted = revision, "Chosen group", 1000, deleted
		var v GroupVersion
		v, s.RevisionEdges, err = n.joinGroupRevision(g, n.groups[id])
		s.GroupVersions = []GroupVersion{v}
	default:
		r := n.resources[resourceKey(kind, id)][0].Resource
		r.Revision, r.UpdatedAt, r.Deleted = revision, 1000, deleted
		var v ResourceVersion
		v, s.RevisionEdges, err = n.joinResourceRevision(r, n.resources[resourceKey(kind, id)])
		s.Resources = []ResourceVersion{v}
	}
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func revisionJoinsPayload(s Snapshot, e RevisionEdge) any {
	switch e.Kind {
	case "monitor":
		m := s.Versions[0].Monitor
		m.Revision, m.CheckGeneration = e.Revision, e.Generation
		if e.Generation == e.Revision {
			m.CheckGeneration = ""
		}
		return m
	case "group":
		g := s.GroupVersions[0].Group
		g.Revision = e.Revision
		return g
	default:
		r := s.Resources[0].Resource
		r.Revision = e.Revision
		return r
	}
}

func revisionJoinsCheck(t *testing.T, n *Node, s Snapshot, tips []string) {
	t.Helper()
	want := 1 + (max(0, len(tips)-maxClusterMembers)+maxClusterMembers-2)/(maxClusterMembers-1)
	if len(s.RevisionEdges) != want {
		t.Fatal("unexpected number of joins", len(s.RevisionEdges), want)
	}
	previous := ""
	for _, e := range s.RevisionEdges {
		if len(e.Parents) > maxClusterMembers || !slices.Equal(e.Parents, unique(e.Parents)) || e.ID != edgeID(e) || e.PayloadHash != contentID(revisionJoinsPayload(s, e)) {
			t.Fatal("join violates ordinary immutable edge contract", e)
		}
		if previous != "" && !slices.Contains(e.Parents, previous) {
			t.Fatal("join chain disconnected", e)
		}
		previous = e.Revision
	}
	last := s.RevisionEdges[len(s.RevisionEdges)-1]
	for _, tip := range tips {
		if !n.graph.reaches(last.Kind, last.EntityID, last.Revision, tip) {
			t.Fatal("observed tip omitted", tip)
		}
	}
	switch last.Kind {
	case "monitor":
		if len(n.heads[last.EntityID]) != 1 || !n.monitorVersionReady(n.heads[last.EntityID][0]) || !equalJSON(n.heads[last.EntityID][0], s.Versions[0]) {
			t.Fatal("monitor resolution is not the chosen ready head")
		}
	case "group":
		if len(n.groups[last.EntityID]) != 1 || !n.groupVersionReady(n.groups[last.EntityID][0]) || !equalJSON(n.groups[last.EntityID][0], s.GroupVersions[0]) {
			t.Fatal("group resolution is not the chosen ready head")
		}
	default:
		h := n.resources[resourceKey(last.Kind, last.EntityID)]
		if len(h) != 1 || !n.resourceVersionReady(h[0]) || !equalJSON(h[0], s.Resources[0]) {
			t.Fatal("resource resolution is not the chosen ready head")
		}
	}
}

func TestRevisionJoinsPreserveOrResetMonitorGenerationAcrossRestoredBranches(t *testing.T) {
	for _, mode := range []string{"metadata", "semantic", "restored-generations", "tombstone"} {
		t.Run(mode, func(t *testing.T) {
			n := cr2New(t, cr2Options(t, false))
			tips := revisionJoinsSeed(t, n, "monitor", 71, mode == "restored-generations")
			before := cr2State(n)
			s := revisionJoinsResolve(t, n, "monitor", mode == "semantic", mode == "tombstone")
			if cr2State(n) != before {
				t.Fatal("constructing joins mutated the node")
			}
			generation := "root-monitor"
			if mode != "metadata" {
				generation = s.RevisionEdges[0].Revision
			}
			if checkGeneration(s.Versions[0].Monitor) != generation {
				t.Fatal("wrong chosen generation", s.Versions[0].Monitor)
			}
			for index, e := range s.RevisionEdges {
				if e.Generation != generation || mode != "metadata" && index == 0 && revisionJoinsPayload(s, e).(Monitor).CheckGeneration != "" {
					t.Fatal("new generation did not begin at the first join", e)
				}
			}
			revisionJoinsApply(t, n, s)
			revisionJoinsCheck(t, n, s, tips)
		})
	}
}

func TestRevisionJoinsFanInBoundariesKeepSmallResolutionContract(t *testing.T) {
	for _, count := range []int{0, 1, 31, 32, 33, 63, 64, 71, 100} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			n := cr2New(t, cr2Options(t, false))
			tips := revisionJoinsSeed(t, n, "group", count, false)
			g := Group{GroupInput: GroupInput{Name: "Chosen group", NodeIDs: []string{"a", "b"}}, ID: "join-group", Revision: "chosen-group", UpdatedAt: 1000}
			v, edges, err := n.joinGroupRevision(g, n.groups[g.ID])
			if err != nil {
				t.Fatal(err)
			}
			s := Snapshot{GroupVersions: []GroupVersion{v}, RevisionEdges: edges}
			if count <= maxClusterMembers && (len(edges) != 1 || !slices.Equal(v.Ancestors, unique(tips)) || v.EdgeID != newRevisionEdge("group", g.ID, g.Revision, g, tips, "").ID) {
				t.Fatal("small-head resolution contract changed")
			}
			revisionJoinsApply(t, n, s)
			revisionJoinsCheck(t, n, s, tips)
		})
	}
	for _, mode := range []string{"new", "metadata", "semantic", "tombstone"} {
		t.Run("monitor-"+mode, func(t *testing.T) {
			n := cr2New(t, cr2Options(t, false))
			in := input([]string{"a", "b"}, "http://127.0.0.1:1", "")
			in.Enabled = false
			chosen := Monitor{MonitorInput: in, ID: "join-monitor", Revision: "small-chosen", UpdatedAt: 1000}
			parents := []string{}
			if mode != "new" {
				parents = revisionJoinsSeed(t, n, "monitor", 1, false)
			}
			if mode == "semantic" {
				chosen.Target = "http://127.0.0.1:2"
			}
			chosen.Deleted = mode == "tombstone"
			if err := validateMonitor(&chosen.MonitorInput, n.known); err != nil {
				t.Fatal(err)
			}
			v, edges, err := n.joinMonitorRevision(chosen, n.heads[chosen.ID])
			if err != nil {
				t.Fatal(err)
			}
			if mode == "metadata" {
				chosen.CheckGeneration = "root-monitor"
			}
			want := newRevisionEdge("monitor", chosen.ID, chosen.Revision, chosen, parents, checkGeneration(chosen))
			if len(edges) != 1 || !equalJSON(v.Monitor, chosen) || !equalJSON(edges[0], want) {
				t.Fatal("small monitor lifecycle contract changed", v, edges)
			}
		})
	}
}

func TestRevisionJoinsAllDomainsPagedReverseInterruptedRestartAndCollision(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			a := cr2New(t, cr2Options(t, false))
			kinds := []string{"monitor", "group", resourceChannel, resourceMaintenance}
			resolutions := map[string]Snapshot{}
			tips := map[string][]string{}
			for _, kind := range kinds {
				tips[kind] = revisionJoinsSeed(t, a, kind, 100, false)
				s := revisionJoinsResolve(t, a, kind, kind == "monitor", false)
				revisionJoinsApply(t, a, s)
				revisionJoinsCheck(t, a, s, tips[kind])
				resolutions[kind] = s
			}
			pages := causalCapacityPages(t, a)
			if len(pages) < 2 {
				t.Fatal("restored branches failed to exercise actual producer paging")
			}
			o := cr2Options(t, true)
			b := cr2New(t, o)
			// The final heads arrive first. No intermediate chosen payload
			// records are ever sent, only independently paged edge proofs.
			for _, s := range resolutions {
				s.RevisionEdges = nil
				revisionJoinsApply(t, b, s)
			}
			if b.current("join-monitor") != nil || b.currentGroup("join-group") != nil || b.currentResource(resourceChannel, "join-"+resourceChannel) != nil || b.currentResource(resourceMaintenance, "join-"+resourceMaintenance) != nil {
				t.Fatal("missing intermediate proofs authorized a configuration")
			}
			if err := b.Stop(); err != nil {
				t.Fatal(err)
			}
			b = cr2New(t, o)
			if b.current("join-monitor") != nil {
				t.Fatal("restart treated missing proofs as complete")
			}
			if reverse {
				slices.Reverse(pages)
			}
			// Keep a middle join absent through the complete sweep. It is
			// indispensable even if every other ancestor and the final head
			// has arrived; restart must preserve that lack of readiness.
			missing := resolutions["monitor"].RevisionEdges[1]
			for _, page := range pages {
				page.RevisionEdges = slices.DeleteFunc(append([]RevisionEdge{}, page.RevisionEdges...), func(e RevisionEdge) bool { return e.ID == missing.ID })
				revisionJoinsApply(t, b, page)
			}
			if b.current("join-monitor") != nil || b.monitorVersionReady(b.heads["join-monitor"][0]) {
				t.Fatal("later join bypassed missing middle proof")
			}
			if err := b.Stop(); err != nil {
				t.Fatal(err)
			}
			b = cr2New(t, o)
			if b.current("join-monitor") != nil {
				t.Fatal("restart bypassed missing middle proof")
			}
			revisionJoinsApply(t, b, Snapshot{RevisionEdges: []RevisionEdge{missing}})
			for _, kind := range kinds {
				revisionJoinsCheck(t, b, resolutions[kind], tips[kind])
			}
			if !equalJSON(a.revisionEdges, b.revisionEdges) {
				t.Fatal("full immutable edge set changed during paging/restart")
			}
			// A dominated intermediate payload remains immutable even though
			// it has never been a transmitted/stored mutable head record.
			m := revisionJoinsPayload(resolutions["monitor"], missing).(Monitor)
			m.Name = "Forged intermediate payload"
			if err := b.applySnapshot(Snapshot{Versions: []Version{{Monitor: m, Ancestors: missing.Parents, EdgeID: missing.ID}}}); err == nil {
				t.Fatal("intermediate revision forgot its immutable payload digest")
			}
			if err := b.Stop(); err != nil {
				t.Fatal(err)
			}
			b = cr2New(t, o)
			for _, kind := range kinds {
				revisionJoinsCheck(t, b, resolutions[kind], tips[kind])
			}
		})
	}
}

func TestRevisionJoinsAtomicFailureRetryAndRestart(t *testing.T) {
	for _, kind := range []string{"monitor", "group", resourceChannel, resourceMaintenance} {
		t.Run(kind, func(t *testing.T) {
			o := cr2Options(t, true)
			o.DBPath = filepath.Join(t.TempDir(), "join-atomic.db")
			n := cr2New(t, o)
			tips := revisionJoinsSeed(t, n, kind, 71, false)
			s := revisionJoinsResolve(t, n, kind, kind == "monitor", false)
			before := cr2State(n)
			stored := revisionJoinsStored(t, n)
			trigger := fmt.Sprintf(`CREATE TEMP TRIGGER reject_join BEFORE INSERT ON records WHEN NEW.category='revisionEdges' AND NEW.id='%s' BEGIN SELECT RAISE(ABORT,'join rollback fixture'); END`, s.RevisionEdges[1].ID)
			if _, err := n.store.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			if err := n.applySnapshot(s); err == nil || cr2State(n) != before || revisionJoinsStored(t, n) != stored {
				t.Fatal("failed fan-in exposed a partial edge/head/index transaction", err)
			}
			if _, err := n.store.db.Exec(`DROP TRIGGER reject_join`); err != nil {
				t.Fatal(err)
			}
			revisionJoinsApply(t, n, s)
			revisionJoinsCheck(t, n, s, tips)
			stored = revisionJoinsStored(t, n)
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			revisionJoinsCheck(t, n, s, tips)
			if revisionJoinsStored(t, n) != stored {
				t.Fatal("restart changed committed resolution or parent indexes")
			}
		})
	}
}

func revisionJoinsStored(t *testing.T, n *Node) string {
	t.Helper()
	state := []any{}
	for _, query := range []string{`SELECT category,id,payload FROM records ORDER BY category,id`, `SELECT entity,revision,edge_id,payload_hash FROM revision_index ORDER BY entity,revision`, `SELECT entity,revision,parent FROM revision_parents ORDER BY entity,revision,parent`} {
		rows, err := n.store.db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			for index := range values {
				values[index] = new(string)
			}
			if err = rows.Scan(values...); err != nil {
				t.Fatal(err)
			}
			state = append(state, values)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return contentID(state)
}

func TestRevisionJoinsRequireExactCompleteCurrentHeads(t *testing.T) {
	n := cr2New(t, cr2Options(t, false))
	revisionJoinsSeed(t, n, "monitor", 71, false)
	id := "join-monitor"
	previous := append([]Version{}, n.heads[id]...)
	chosen := previous[0].Monitor
	chosen.Revision, chosen.UpdatedAt = "chosen-monitor", 1000
	before := cr2State(n)
	badSets := [][]Version{previous[:70], append(append([]Version{}, previous...), previous[0])}
	wrongScope := append([]Version{}, previous...)
	wrongScope[0].Monitor.ID = "different-monitor"
	badSets = append(badSets, wrongScope)
	wrongPayload := append([]Version{}, previous...)
	wrongPayload[0].Monitor.Name = "Same revision different payload"
	badSets = append(badSets, wrongPayload)
	for _, parents := range badSets {
		if _, _, err := n.joinMonitorRevision(chosen, parents); err == nil {
			t.Fatal("resolution accepted missing, duplicate, wrong-scope or changed vote")
		}
	}
	missing := n.revisionEdges[previous[0].EdgeID]
	delete(n.revisionEdges, missing.ID)
	var err error
	n.graph, err = buildRevisionGraph(n.revisionEdges, n.revisionClaims, n.evidenceLinks)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = n.joinMonitorRevision(chosen, previous); err == nil {
		t.Fatal("resolution accepted a parent with missing proof")
	}
	n.revisionEdges[missing.ID] = missing
	n.graph, err = buildRevisionGraph(n.revisionEdges, n.revisionClaims, n.evidenceLinks)
	if err != nil {
		t.Fatal(err)
	}
	chosen.Revision = "root-monitor"
	if _, _, err = n.joinMonitorRevision(chosen, previous); err == nil {
		t.Fatal("new join reused a dominated immutable revision")
	}
	if cr2State(n) != before {
		t.Fatal("rejected join mutated configuration or facts")
	}
}
