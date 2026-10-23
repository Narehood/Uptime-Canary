package canary

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPagedRealPeersRetainHistoryAndSynchronizeNewState(t *testing.T) {
	var calls, largestRequest, largestResponse atomic.Int64
	var loseResponse atomic.Bool
	client := newHTTPClient()
	t.Cleanup(client.CloseIdleConnections)
	transport := doFunc(func(req *http.Request) (*http.Response, error) {
		calls.Add(1)
		body, err := readBounded(req.Body, maxBody)
		if err != nil {
			t.Error("Oversize request", err)
			return nil, err
		}
		largestRequest.Store(max(largestRequest.Load(), int64(len(body))))
		req.Body = io.NopCloser(bytes.NewReader(body))
		res, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, err = readBounded(res.Body, maxBody)
		res.Body.Close()
		if err != nil {
			t.Error("Oversize response", err)
			return nil, err
		}
		largestResponse.Store(max(largestResponse.Load(), int64(len(body))))
		if loseResponse.Swap(false) {
			return nil, fmt.Errorf("Lost response after peer committed page")
		}
		res.Body = io.NopCloser(bytes.NewReader(body))
		return res, nil
	})
	makePeer := func(id, other string) *Node {
		n, err := New(Options{NodeID: id, DBPath: filepath.Join(t.TempDir(), id+".db"), AdminToken: adminToken, ClusterToken: clusterToken, PeerClient: transport, Peers: []Peer{{ID: other, URL: "http://127.0.0.1:1"}}, PeerTimeoutMs: Ptr(int64(5000))})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = n.Stop() })
		return n
	}
	a, b := makePeer("a", "b"), makePeer("b", "a")
	// Real HTTP handlers and durable SQLite stores; drive bounded sync rounds
	// explicitly so an interrupted page and cross-round progress are observable.
	as, bs := httptest.NewServer(a), httptest.NewServer(b)
	t.Cleanup(as.Close)
	t.Cleanup(bs.Close)
	a.settings.Peers[0].URL, b.settings.Peers[0].URL = bs.URL, as.URL
	seedPagedHistory(t, a, true)
	seedPagedHistory(t, b, false)
	for _, n := range []*Node{a, b} {
		full, err := json.Marshal(fullSnapshotForTest(n))
		if err != nil || len(full) <= maxBody || len(n.receipts) != 5000 {
			t.Fatal("Fixture must exceed old full-snapshot ceiling with 5000 normal receipts", len(full), err)
		}
	}
	loseResponse.Store(true)
	a.syncPeers()
	if a.peerProgress["b"] != (syncProgress{}) {
		t.Fatal("Unacknowledged page advanced cursor", a.peerProgress)
	}
	converge := func(done func() bool) {
		t.Helper()
		for range 50 {
			a.syncPeers()
			if done() {
				return
			}
		}
		t.Fatalf("Bounded page rounds failed to converge: receipts %d/%d, progress %+v", len(a.receipts), len(b.receipts), a.peerProgress)
	}
	converge(func() bool {
		return len(a.receipts) == 10000 && len(b.receipts) == 10000 && len(b.heads) == 200 && len(b.groups) == 200 && len(a.episodes) == 100 && len(b.episodes) == 100
	})
	if calls.Load() <= syncPagesPerRound {
		t.Fatal("Fixture did not exercise cross-round pagination", calls.Load())
	}
	// A revision, group edit and new successful receipts still progress after
	// historical receipts have exceeded both the old byte and record ceilings.
	old := a.heads["monitor199"]
	edit := old[0].Monitor.MonitorInput
	edit.Name = "New revision after 10000 receipts"
	m, err := a.writeMonitor(edit, old, "monitor199", false)
	if err != nil {
		t.Fatal(err)
	}
	oldGroup := a.groups["group199"]
	groupEdit := oldGroup[0].Group.GroupInput
	groupEdit.Name = "New group revision after history backlog"
	g, err := a.writeGroup(groupEdit, oldGroup, "group199", false)
	if err != nil {
		t.Fatal(err)
	}
	later := episode("later-outage", map[string][]int64{"a": {201}})
	later.MonitorID, later.Revision, later.OpenedAt = "monitor000", "revision000", a.now()
	for index := range 100 {
		later.Predecessors = append(later.Predecessors, historyEpisodeID(index))
	}
	a.saveEpisode(later)
	late := b.episodes[historyEpisodeID(0)]
	late.ID, late.Aliases, late.Closed, late.ResolvedAt = "delayed-sibling", []string{"delayed-sibling"}, nil, nil
	late.Runs = map[string][]int64{"b": {1}}
	b.saveEpisode(late)
	newReceipt := Receipt{NotificationView{ID: "new-success-after-backlog", MonitorID: later.MonitorID, IncidentID: later.ID, Kind: "down", NodeID: "a", SentAt: a.now()}, 0}
	if !a.dbPut("receipts", newReceipt.ID, newReceipt) {
		t.Fatal("Persist new receipt")
	}
	a.receipts[newReceipt.ID] = newReceipt
	back := Receipt{NotificationView{ID: "new-peer-repeat-after-backlog", MonitorID: later.MonitorID, IncidentID: later.ID, Kind: "repeat", NodeID: "b", SentAt: b.now()}, 12345}
	if !b.dbPut("receipts", back.ID, back) {
		t.Fatal("Persist peer receipt")
	}
	b.receipts[back.ID] = back
	converge(func() bool {
		return b.current(m.ID) != nil && b.currentGroup(g.ID) != nil && b.current(m.ID).Revision == m.Revision && b.currentGroup(g.ID).Revision == g.Revision && b.receipts[newReceipt.ID].ID == newReceipt.ID && a.receipts[back.ID].ID == back.ID && len(a.episodes) == 101 && len(b.episodes) == 101 && slices.Contains(a.episodes[historyEpisodeID(0)].Aliases, "delayed-sibling") && slices.Contains(b.episodes[historyEpisodeID(0)].Aliases, "delayed-sibling")
	})
	for _, n := range []*Node{a, b} {
		if n.episodes[later.ID].Closed != nil || len(n.forEpisode(n.episodes[later.ID])) != 2 {
			t.Fatal("Historical closure or receipt silenced the newer outage", n.episodes[later.ID])
		}
		first := n.episodes[historyEpisodeID(0)]
		if first.Closed == nil || first.Closed["a"] != 2 || first.Closed["b"] != 2 {
			t.Fatal("Delayed old episode resurrected after pagination", first)
		}
		durable, err := loadAll[Receipt](n.store, "receipts")
		if err != nil || len(durable) != 10002 {
			t.Fatal("Successful-send history was discarded", len(durable), err)
		}
	}
	t.Logf("5000 durable receipts per node, 100 closed episodes, 200 monitors/groups: %d HTTP exchanges; largest request %d, response %d bytes", calls.Load(), largestRequest.Load(), largestResponse.Load())
}

func historyEpisodeID(index int) string {
	return fmt.Sprintf("11111111-1111-4111-8111-%012d", index)
}

func fullSnapshotForTest(n *Node) Snapshot {
	s := Snapshot{Protocol: 1, Node: Identity{n.ID, n.Name}, Versions: []Version{}, GroupVersions: []GroupVersion{}, Observations: []Sample{}, Incidents: []Incident{}, Receipts: []Receipt{}}
	for _, heads := range n.heads {
		s.Versions = append(s.Versions, heads...)
	}
	for _, heads := range n.groups {
		s.GroupVersions = append(s.GroupVersions, heads...)
	}
	for _, o := range n.samples {
		s.Observations = append(s.Observations, o)
	}
	for _, i := range n.episodes {
		s.Incidents = append(s.Incidents, i)
	}
	for _, r := range n.receipts {
		s.Receipts = append(s.Receipts, r)
	}
	return s
}

func seedPagedHistory(t *testing.T, n *Node, configuration bool) {
	t.Helper()
	tx, err := n.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	write := func(category, id string, value any) {
		t.Helper()
		if err := put(tx, category, id, value); err != nil {
			t.Fatal(err)
		}
	}
	if configuration {
		// Exercise individual monitor/group revisions above the soft page target,
		// as well as aggregate configuration spread over several pages.
		largeAncestors := []string{}
		for index := range 8000 {
			largeAncestors = append(largeAncestors, historyEpisodeID(index))
		}
		for index := range 200 {
			id := fmt.Sprintf("monitor%03d", index)
			in := input([]string{"a", "b"}, "https://example.com/"+strings.Repeat("path", 400), "https://hooks.example.com/"+strings.Repeat("x", 1600))
			in.Enabled = false
			m := Monitor{MonitorInput: in, ID: id, Revision: fmt.Sprintf("revision%03d", index), UpdatedAt: n.now()}
			n.heads[id] = []Version{{Monitor: m, Ancestors: []string{"older-revision"}}}
			if index == 0 {
				n.heads[id][0].Ancestors = largeAncestors
			}
			write("heads", id, n.heads[id])
			id = fmt.Sprintf("group%03d", index)
			g := Group{GroupInput: GroupInput{Name: strings.Repeat("g", 128), NodeIDs: []string{"a", "b"}, OutageMonitorID: "monitor000", GraceMs: 1000}, ID: id, Revision: fmt.Sprintf("group-revision%03d", index), UpdatedAt: n.now()}
			n.groups[id] = []GroupVersion{{Group: g, Ancestors: []string{"older-group-revision"}}}
			if index == 0 {
				n.groups[id][0].Ancestors = largeAncestors
			}
			write("groupHeads", id, n.groups[id])
		}
	}
	predecessors := []string{}
	for index := range 100 {
		seq := int64(index*2 + 1)
		i := episode(historyEpisodeID(index), map[string][]int64{"a": {seq}, "b": {seq}})
		i.MonitorID, i.Revision = "monitor000", "revision000"
		i.OpenedAt = int64(1000 + index*100000)
		i.Closed, i.ResolvedAt = map[string]int64{"a": seq + 1, "b": seq + 1}, Ptr(i.OpenedAt+50000)
		i.Predecessors = append([]string{}, predecessors...)
		predecessors = append(predecessors, i.ID)
		n.episodes[i.ID] = i
		write("incidents", i.ID, i)
		for slot := range 50 {
			kind := "repeat"
			if slot == 0 {
				kind = "down"
			} else if slot == 49 {
				kind = "recovery"
			}
			r := Receipt{NotificationView{ID: digest(fmt.Sprintf("%s/%s/%d", n.ID, i.ID, slot)), MonitorID: i.MonitorID, IncidentID: i.ID, Kind: kind, NodeID: n.ID, SentAt: i.OpenedAt + int64(slot*1000)}, int64(slot)}
			n.receipts[r.ID] = r
			write("receipts", r.ID, r)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
