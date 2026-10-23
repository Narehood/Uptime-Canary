package canary

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"testing"
)

func causalCapacityOptions(id, path string) Options {
	other := "a"
	if id == "a" {
		other = "b"
	}
	return Options{NodeID: id, DBPath: path, Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Peers: []Peer{{ID: other, URL: "http://127.0.0.1:1"}}}
}

func causalCapacityNode(t *testing.T, id, path string) *Node {
	t.Helper()
	n, err := New(causalCapacityOptions(id, path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	return n
}

func causalCapacityPages(t *testing.T, n *Node) []Snapshot {
	t.Helper()
	pages := []Snapshot{}
	for cursor := ""; ; {
		p, err := n.snapshotPage(cursor, "")
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(p)
		if err != nil || len(b) > maxBody {
			t.Fatal("unbounded page", len(b), err)
		}
		count := len(p.Versions) + len(p.GroupVersions) + len(p.Resources) + len(p.Intents) + len(p.Observations) + len(p.Incidents) + len(p.Receipts) + len(p.RevisionEdges) + len(p.RevisionClaims) + len(p.IncidentFacts) + len(p.EvidenceLinks)
		if count > pageRecords {
			t.Fatal("unbounded record count", count)
		}
		if _, err = n.validateSnapshot(b, n.ID); err != nil {
			t.Fatal("self-produced page invalid", err)
		}
		pages = append(pages, p)
		if p.Next == "" {
			return pages
		}
		if p.Next <= cursor {
			t.Fatal("cursor did not advance")
		}
		cursor = p.Next
	}
}

func TestCausalCapacityMissingRevisionPagesRetainMoreThan32Candidates(t *testing.T) {
	a, b := causalCapacityNode(t, "a", ":memory:"), causalCapacityNode(t, "b", ":memory:")
	in := input([]string{"a", "b"}, "http://127.0.0.1:1", "")
	in.Enabled = false
	m, err := a.writeMonitor(in, nil, "causal-capacity-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	root := a.heads[m.ID][0]
	if err = b.mergeSnapshot(Snapshot{Versions: []Version{root}, RevisionEdges: []RevisionEdge{a.revisionEdges[root.EdgeID]}}); err != nil {
		t.Fatal(err)
	}
	old := Incident{ID: "old-generation-episode", Aliases: []string{"old-generation-episode"}, MonitorID: m.ID, MonitorName: m.Name, Revision: m.Revision, OpenedAt: a.now(), Runs: map[string][]int64{"a": {1}}, Predecessors: []string{}}
	b.saveEpisode(old)
	versions := []Version{}
	for edit := range 70 {
		in.Name = fmt.Sprintf("Metadata edit %d", edit)
		m, err = a.writeMonitor(in, a.heads[m.ID], m.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		versions = append(versions, a.heads[m.ID][0])
	}
	in.Target = "http://127.0.0.1:2"
	m, err = a.writeMonitor(in, a.heads[m.ID], m.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	versions = append(versions, a.heads[m.ID][0])
	// Each head is valid but all intermediate edges are delayed. Apparent
	// incomparability must not impose a permanent 32-candidate receive limit.
	for _, v := range versions {
		if err = b.mergeSnapshot(Snapshot{Versions: []Version{v}}); err != nil {
			t.Fatal("valid unresolved head rejected", err)
		}
	}
	if len(b.heads[m.ID]) != len(versions)+1 || b.current(m.ID) != nil || b.episodes[old.ID].Closed != nil {
		t.Fatal("missing proof discarded a candidate or retired old generation")
	}
	edges := slices.Collect(maps.Values(a.revisionEdges))
	slices.Reverse(edges)
	for _, e := range edges {
		if len(e.Parents) > maxClusterMembers {
			t.Fatal("unbounded edge")
		}
		if err = b.mergeSnapshot(Snapshot{RevisionEdges: []RevisionEdge{e}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(b.heads[m.ID]) != 1 || b.current(m.ID) == nil || b.current(m.ID).Revision != m.Revision || b.episodes[old.ID].RetiredBy != m.Revision {
		t.Fatal("late proofs failed domination or old-generation retirement")
	}
	if err = b.mergeSnapshot(Snapshot{Versions: []Version{root}}); err != nil || len(b.heads[m.ID]) != 1 {
		t.Fatal("old root replay resurrected a stale head", err)
	}
	changed := root
	changed.Monitor.Name = "collision after domination"
	if err = b.mergeSnapshot(Snapshot{Versions: []Version{changed}}); err == nil {
		t.Fatal("forgot payload digest after pruning old head")
	}
}

func TestCausalCapacityCompleteEvidenceSurvivesReversePagesAliasesAndRestart(t *testing.T) {
	a := causalCapacityNode(t, "a", ":memory:")
	path := filepath.Join(t.TempDir(), "receiver.db")
	b := causalCapacityNode(t, "b", path)
	i := Incident{ID: "z-original", Aliases: []string{"z-original"}, MonitorID: "retained-monitor", MonitorName: "Large causal history", Revision: "generation", OpenedAt: 100, Runs: map[string][]int64{"a": {}, "b": {1}}, Predecessors: []string{}}
	for index := range 1005 {
		i.Runs["a"] = append(i.Runs["a"], int64(index+1))
	}
	for index := range 65 {
		i.Aliases = append(i.Aliases, fmt.Sprintf("z-related-%05d", index))
	}
	for index := range 10001 {
		i.Predecessors = append(i.Predecessors, fmt.Sprintf("prior-%05d", index))
	}
	a.saveEpisode(i)
	if a.fault != nil {
		t.Fatal(a.fault)
	}
	pages := causalCapacityPages(t, a)
	if len(pages) < 2 {
		t.Fatal("fixture did not page evidence")
	}
	// Deliver the summary first and prove its inline suffix cannot become an
	// authoritative episode before older run/predecessor evidence arrives.
	summary := a.incidentSources[i.ID]
	if err := b.mergeSnapshot(Snapshot{Incidents: []Incident{summary}}); err != nil {
		t.Fatal(err)
	}
	if len(b.episodes) != 0 || len(b.pendingIncidents) != 1 {
		t.Fatal("incomplete summary acquired causal authority")
	}
	if err := b.Stop(); err != nil {
		t.Fatal(err)
	}
	var err error
	b, err = New(causalCapacityOptions("b", path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Stop() })
	if len(b.episodes) != 0 || len(b.pendingIncidents) != 1 {
		t.Fatal("pending evidence did not survive restart")
	}
	slices.Reverse(pages)
	for _, page := range pages {
		raw, _ := json.Marshal(page)
		validated, e := b.validateSnapshot(raw, "a")
		if e != nil {
			t.Fatal(e)
		}
		if e = b.mergeSnapshot(validated); e != nil {
			t.Fatal(e)
		}
	}
	check := func() {
		t.Helper()
		got := b.episodes[i.ID]
		if len(b.pendingIncidents) != 0 || len(got.Runs["a"]) != 1005 || len(got.Aliases) != 66 || len(got.Predecessors) != 10001 {
			t.Fatal("complete evidence lost in hydration", len(got.Runs["a"]), len(got.Aliases), len(got.Predecessors))
		}
		stored, ok, e := loadOne[Incident](b.store, "incidents", i.ID)
		if e != nil || !ok || stored.EvidenceRoot == "" || len(stored.Runs["a"]) > inlineIncidentRuns || len(stored.Aliases) > inlineIncidentIDs || len(stored.Predecessors) > inlineIncidentIDs {
			t.Fatal("unbounded durable summary", e)
		}
		hydrated, complete, e := hydrateIncident(stored, b.incidentFacts, b.evidenceLinks)
		if e != nil || !complete || !equalJSON(hydrated.Runs, got.Runs) || !slices.Equal(hydrated.Predecessors, got.Predecessors) {
			t.Fatal("durable summary cannot recover its complete evidence", e)
		}
	}
	check()
	receipt := Receipt{NotificationView: NotificationView{ID: "retained-receipt", MonitorID: i.MonitorID, IncidentID: i.ID, Kind: "down", NodeID: "a", SentAt: 101}}
	alias := i
	alias.ID = "a-canonical"
	alias.Aliases = []string{alias.ID}
	alias.Runs = map[string][]int64{"b": {1}}
	if err = b.mergeSnapshot(Snapshot{Incidents: []Incident{alias}, Receipts: []Receipt{receipt}}); err != nil {
		t.Fatal(err)
	}
	if len(b.episodes) != 1 || len(b.episodes[alias.ID].Runs["a"]) != 1005 || len(b.forEpisode(b.episodes[alias.ID])) != 1 {
		t.Fatal("alias transition hid old evidence or receipt")
	}
	factCount, linkCount := len(b.incidentFacts), len(b.evidenceLinks)
	if err = b.Stop(); err != nil {
		t.Fatal(err)
	}
	b, err = New(causalCapacityOptions("b", path))
	if err != nil {
		t.Fatal(err)
	}
	if len(b.incidentFacts) != factCount || len(b.evidenceLinks) != linkCount || len(b.episodes) != 1 || len(b.episodes[alias.ID].Runs["a"]) != 1005 || len(b.forEpisode(b.episodes[alias.ID])) != 1 {
		t.Fatal("restart lost original facts, alias projection, or receipt")
	}
}

func TestCausalCapacityFactAndProjectionTransactionRollback(t *testing.T) {
	n := causalCapacityNode(t, "a", ":memory:")
	i := Incident{ID: "atomic-facts", Aliases: []string{"atomic-facts"}, MonitorID: "monitor", MonitorName: "Atomic facts", Revision: "generation", OpenedAt: 100, Runs: map[string][]int64{"a": {1}}, Predecessors: []string{}}
	n.saveEpisode(i)
	beforeFacts, beforeLinks := len(n.incidentFacts), len(n.evidenceLinks)
	_, err := n.store.db.Exec(`CREATE TRIGGER causal_projection_failure BEFORE INSERT ON records WHEN NEW.category='incidents' BEGIN SELECT RAISE(FAIL,'injected projection failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	i.Runs = map[string][]int64{"a": {1, 3}}
	receipt := Receipt{NotificationView: NotificationView{ID: "atomic-receipt", MonitorID: i.MonitorID, IncidentID: i.ID, Kind: "down", NodeID: "b", SentAt: 100}}
	page := Snapshot{Incidents: []Incident{i}, Receipts: []Receipt{receipt}}
	if err = n.mergeSnapshot(page); err == nil {
		t.Fatal("failed projection acknowledged")
	}
	if len(n.incidentFacts) != beforeFacts || len(n.evidenceLinks) != beforeLinks || len(n.episodes[i.ID].Runs["a"]) != 1 || len(n.receipts) != 0 {
		t.Fatal("failed transaction published facts or authority")
	}
	for _, category := range []string{"incidentFacts", "evidenceLinks", "receipts"} {
		rows, e := loadAll[json.RawMessage](n.store, category)
		if e != nil {
			t.Fatal(e)
		}
		want := beforeFacts
		if category == "evidenceLinks" {
			want = beforeLinks
		}
		if category == "receipts" {
			want = 0
		}
		if len(rows) != want {
			t.Fatal("failed transaction persisted facts", category, len(rows), want)
		}
	}
	var indexed int
	if err = n.store.db.QueryRow(`SELECT COUNT(*) FROM incident_fact_index`).Scan(&indexed); err != nil || indexed != beforeFacts {
		t.Fatal("failed transaction changed SQL index", indexed, err)
	}
	if _, err = n.store.db.Exec(`DROP TRIGGER causal_projection_failure`); err != nil {
		t.Fatal(err)
	}
	if err = n.mergeSnapshot(page); err != nil || len(n.episodes[i.ID].Runs["a"]) != 2 {
		t.Fatal("identical retry did not complete", err)
	}
	durable, err := loadAll[Receipt](n.store, "receipts")
	if err != nil || len(durable) != 1 {
		t.Fatal("successful receipt did not commit", err)
	}
}

func TestCausalCapacityLegacyDatabaseExtractsExactFactsAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	n := causalCapacityNode(t, "a", path)
	in := input([]string{"a"}, "http://127.0.0.1:1", "")
	in.Enabled = false
	m, err := n.writeMonitor(in, nil, "legacy-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	i := Incident{ID: "legacy-incident", Aliases: []string{"legacy-incident"}, MonitorID: m.ID, MonitorName: m.Name, Revision: m.Revision, OpenedAt: 100, Runs: map[string][]int64{"a": {}}, Predecessors: []string{}}
	for index := range 1001 {
		i.Runs["a"] = append(i.Runs["a"], int64(index+1))
	}
	v := Version{Monitor: m, Ancestors: []string{}}
	for index := range 10017 {
		v.Ancestors = append(v.Ancestors, fmt.Sprintf("legacy-revision-%05d", index))
	}
	// Seed the old prototype's actual record domains; New performs the upgrade.
	if err = put(n.store.db, "heads", m.ID, []Version{v}); err != nil {
		t.Fatal(err)
	}
	if err = put(n.store.db, "incidents", i.ID, i); err != nil {
		t.Fatal(err)
	}
	if _, err = n.store.db.Exec(`DELETE FROM records WHERE category IN ('revisionEdges','revisionClaims','incidentFacts','evidenceLinks','incidentSources','pendingIncidents'); DELETE FROM revision_index; DELETE FROM revision_parents; DELETE FROM incident_fact_index; CREATE TRIGGER legacy_upgrade_failure BEFORE INSERT ON records WHEN NEW.category='incidents' BEGIN SELECT RAISE(FAIL,'injected migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = n.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err = New(causalCapacityOptions("a", path)); err == nil {
		t.Fatal("failed root migration started node")
	}
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	stored, _, err := loadOne[[]Version](s, "heads", m.ID)
	if err != nil || len(stored[0].Ancestors) != 10017 || stored[0].EdgeID != "" {
		t.Fatal("failed upgrade altered legacy head", err)
	}
	facts, err := loadAll[IncidentFact](s, "incidentFacts")
	if err != nil || len(facts) != 0 {
		t.Fatal("failed upgrade committed facts", err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER legacy_upgrade_failure`); err != nil {
		t.Fatal(err)
	}
	if err = s.close(); err != nil {
		t.Fatal(err)
	}
	n, err = New(causalCapacityOptions("a", path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	if len(n.revisionClaims) != 10017 || len(n.episodes[i.ID].Runs["a"]) != 1001 || len(n.heads[m.ID][0].Ancestors) != 0 || n.heads[m.ID][0].EdgeID == "" {
		t.Fatal("legacy migration did not retain complete causal facts")
	}
	for _, ancestor := range v.Ancestors {
		if !n.graph.reaches("monitor", m.ID, m.Revision, ancestor) {
			t.Fatal("legacy reachability lost", ancestor)
		}
	}
	for _, page := range causalCapacityPages(t, n) {
		for _, head := range page.Versions {
			if len(head.Ancestors) > maxClusterMembers {
				t.Fatal("legacy producer still emits cumulative ancestry")
			}
		}
	}
}

func TestCausalCapacityFreshEvaluationBeforeOlderBoundaryPage(t *testing.T) {
	n := causalCapacityNode(t, "a", filepath.Join(t.TempDir(), "causal-order.db"))
	in := input([]string{"a", "b"}, "http://127.0.0.1:1", "http://127.0.0.1:1/hook")
	m, err := n.writeMonitor(in, nil, "causal-order-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	// Occupy delivery workers: this control inspects authority without I/O.
	for range cap(n.notificationWorkers) {
		n.notificationWorkers <- struct{}{}
	}
	prior := Incident{ID: "z-prior", Aliases: []string{"z-prior"}, MonitorID: m.ID, MonitorName: m.Name, Revision: m.Revision, OpenedAt: n.now() - 10000, Runs: map[string][]int64{"a": {1}}, Closed: map[string]int64{"a": 2, "b": 2}, ResolvedAt: Ptr(n.now() - 9000), Predecessors: []string{}}
	sibling := prior
	sibling.ID, sibling.Aliases = "b-sibling", []string{"b-sibling"}
	sibling.Runs = map[string][]int64{"b": {1}}
	sibling.Closed, sibling.ResolvedAt = nil, nil
	current := prior
	current.ID, current.Aliases = "c-current", []string{"c-current"}
	current.OpenedAt = n.now() - 1000
	current.Closed, current.ResolvedAt = nil, nil
	current.Runs = map[string][]int64{"a": {}}
	current.Predecessors = []string{prior.ID}
	for run := int64(3); run <= 1003; run++ {
		current.Runs["a"] = append(current.Runs["a"], run)
	}
	facts, links := map[string]IncidentFact{}, map[string]EvidenceLink{}
	prior = boundedIncident(prepareIncident(prior, "", facts, links))
	sibling = boundedIncident(prepareIncident(sibling, "", facts, links))
	current = boundedIncident(prepareIncident(current, "", facts, links))
	receipt := Receipt{NotificationView: NotificationView{ID: "old-success", MonitorID: m.ID, IncidentID: sibling.ID, Kind: "down", NodeID: "b", SentAt: prior.OpenedAt + 100}}
	if err = n.mergeSnapshot(Snapshot{Incidents: []Incident{sibling, current}, Receipts: []Receipt{receipt}, Intents: []NotificationIntent{deliveryIntent(m.ID, sibling.ID, "")}}); err != nil {
		t.Fatal(err)
	}
	n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: n.now(), Message: "current outage"}, Revision: m.Revision, Sequence: 1003, DownSince: Ptr(int64(1003))}
	n.samples[m.ID+"/b"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "b", State: "up", CheckedAt: n.now(), Message: "healthy"}, Revision: m.Revision, Sequence: 1004}
	if n.current(m.ID) == nil || len(n.episodes) != 0 {
		t.Fatal("incomplete history disabled known configuration or acquired historical authority")
	}
	factList, linkList := slices.Collect(maps.Values(facts)), slices.Collect(maps.Values(links))
	for len(factList) > 0 {
		count := min(pageRecords, len(factList))
		if err = n.mergeSnapshot(Snapshot{IncidentFacts: factList[:count]}); err != nil {
			t.Fatal(err)
		}
		factList = factList[count:]
	}
	for len(linkList) > 0 {
		count := min(pageRecords, len(linkList))
		if err = n.mergeSnapshot(Snapshot{EvidenceLinks: linkList[:count]}); err != nil {
			t.Fatal(err)
		}
		linkList = linkList[count:]
	}
	assertIndependent := func() {
		t.Helper()
		var got Incident
		for _, i := range n.episodes {
			if slices.Contains(episodeIDs(i), current.ID) {
				got = i
			}
		}
		if got.ID == "" || got.Closed != nil || len(got.Runs["a"]) != 1001 || slices.Contains(episodeIDs(got), sibling.ID) || len(n.forEpisode(got)) != 0 {
			t.Fatal("old receipt acquired successor authority before its boundary", got)
		}
		fresh := false
		for _, i := range n.episodes {
			if localBranch(i, n.ID, downWitness(n.fresh(m), n.ID)) && n.hasDeliveryIntent(i, "") && len(n.forEpisode(i)) == 0 {
				fresh = true
			}
		}
		if !fresh {
			t.Fatal("fresh local source lost delivery authority while history was interrupted")
		}
	}
	for range 8 {
		n.evaluate()
		assertIndependent()
	}
	// Only now supply the older closed boundary. It may reconcile the old
	// sibling but must not change the successor's identity or receipt set.
	if err = n.mergeSnapshot(Snapshot{Incidents: []Incident{prior}}); err != nil {
		t.Fatal(err)
	}
	assertIndependent()
	for range cap(n.notificationWorkers) {
		<-n.notificationWorkers
	}
}
