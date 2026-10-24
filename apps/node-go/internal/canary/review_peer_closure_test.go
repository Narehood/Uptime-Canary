package canary

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
)

// Peer snapshots refine closure evidence without a background scheduler.
// Nodes never Start. Stores are private and receivers are loopback fixtures.

func peerClosureReviewSample(t *testing.T, n *Node, m Monitor, state string, sequence int64, run *int64) {
	t.Helper()
	n.mu.Lock()
	s := Sample{Observation: Observation{MonitorID: m.ID, NodeID: n.ID, State: state, CheckedAt: n.now(), Message: "peer closure fixture"}, Sequence: sequence, DownSince: run, Revision: m.Revision, CheckGeneration: m.CheckGeneration}
	key := m.ID + "/" + n.ID
	saved := n.dbPut("samples", key, s)
	if saved {
		n.samples[key] = s
		n.evaluate()
	}
	n.mu.Unlock()
	n.wg.Wait()
	if !saved || n.Error() != nil {
		t.Fatal("cannot persist/evaluate private sample", n.Error())
	}
}

// Freeze bytes at send time: later source updates cannot mutate a delayed page.
func peerClosureReviewWire(t *testing.T, n *Node) []byte {
	t.Helper()
	n.mu.Lock()
	s, err := n.snapshotPage("", "")
	if err == nil && s.Next != "" {
		err = fmt.Errorf("small fixture unexpectedly needs a second snapshot page")
	}
	var body []byte
	if err == nil {
		body, err = json.Marshal(s)
	}
	n.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func peerClosureReviewReceive(t *testing.T, n *Node, sender string, body []byte) {
	t.Helper()
	s, err := n.validateSnapshot(body, sender)
	if err != nil {
		t.Fatal("real peer decoder rejected fixture", err)
	}
	n.mu.Lock()
	err = n.mergeSnapshot(s)
	n.mu.Unlock()
	n.wg.Wait()
	if err != nil || n.Error() != nil {
		t.Fatal("peer merge/evaluation failed", err, n.Error())
	}
}

type peerClosureReviewFixture struct {
	n, peer       *Node
	clock         *atomic.Int64
	parent, child Monitor
	a, b          *receiver
	targetA       notificationTarget
	targetB       notificationTarget
	closed        Incident
	release       func()
	path          string
}

func newPeerClosureReviewFixture(t *testing.T, restart, frontier, earlierResolution bool) *peerClosureReviewFixture {
	t.Helper()
	f := &peerClosureReviewFixture{clock: &atomic.Int64{}, path: ":memory:"}
	base := maintenanceClock(11, 58)
	f.clock.Store(base)
	if restart {
		f.path = filepath.Join(t.TempDir(), "peer-closure.db")
	}
	f.n = maintenanceReviewNode(t, f.clock, f.path)
	f.peer = maintenanceReviewNode(t, f.clock, ":memory:", func(o *Options) {
		o.NodeID = "b"
		o.Peers = []Peer{{ID: "a", URL: "http://127.0.0.1:1"}}
	})
	f.a, f.b = webhook(t), webhook(t)
	f.b.status.Store(503)
	channels := []string{}
	for index, r := range []*receiver{f.a, f.b} {
		c, err := f.n.writeResource(resourceChannel, "", hookChannel(fmt.Sprintf("Peer closure route %d", index), r.URL), false)
		if err != nil {
			t.Fatal(err)
		}
		channels = append(channels, c.ID)
	}
	in := input([]string{"a"}, "http://127.0.0.1:1", "")
	in.Name, in.NotificationIDs = "Parent", channels
	f.parent = maintenanceReviewMonitor(t, f.n, in, nil)
	g, err := f.n.writeGroup(GroupInput{Name: "Dependency", NodeIDs: []string{"a", "b"}, OutageMonitorID: f.parent.ID, GraceMs: 0}, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	in.Name, in.GroupID, in.NodeIDs = "Child", g.ID, []string{"a", "b"}
	f.child = maintenanceReviewMonitor(t, f.n, in, nil)
	peerClosureReviewSample(t, f.n, f.parent, "down", 1, Ptr(int64(1)))
	if fullReviewCount(f.a, f.parent.ID, "down") != 1 || fullReviewCount(f.b, f.parent.ID, "down") != 1 {
		t.Fatal("fixture needs accepted parent A and failed parent B")
	}
	targets := f.n.notificationTargets(f.child)
	if len(targets) != 2 {
		t.Fatal("fixture needs two named destinations")
	}
	f.targetA, f.targetB = targets[0], targets[1]
	if group, _, _ := f.n.suppressionForTarget(f.child, f.targetA); group != g.ID {
		t.Fatal("accepted parent A does not cover child A")
	}
	if group, _, _ := f.n.suppressionForTarget(f.child, f.targetB); group != "" {
		t.Fatal("failed parent B incorrectly covers child B")
	}
	f.release = fullReviewFollowupBlock(f.n)
	t.Cleanup(f.release)
	releasePeer := fullReviewFollowupBlock(f.peer)
	t.Cleanup(releasePeer)
	peerClosureReviewReceive(t, f.peer, "a", peerClosureReviewWire(t, f.n))
	peerClosureReviewSample(t, f.n, f.child, "down", 1, Ptr(int64(1)))
	peerClosureReviewSample(t, f.peer, f.child, "down", 1, Ptr(int64(1)))
	peerClosureReviewReceive(t, f.peer, "a", peerClosureReviewWire(t, f.n))
	peerClosureReviewReceive(t, f.n, "b", peerClosureReviewWire(t, f.peer))
	opened := fullReviewControlEpisode(t, f.n, f.child)
	if opened.Closed != nil || !equalJSON(opened.Runs, map[string][]int64{"a": {1}, "b": {1}}) {
		t.Fatal("fixture did not reconcile both actual down observations", opened)
	}

	// Each observer is still seeing the other observer down. Capture each up-4
	// page before advancing local evidence, then deliver those delayed pages.
	f.clock.Store(base + 10)
	peerClosureReviewSample(t, f.n, f.child, "up", 4, nil)
	a4 := peerClosureReviewWire(t, f.n)
	peerClosureReviewSample(t, f.peer, f.child, "up", 4, nil)
	b4 := peerClosureReviewWire(t, f.peer)
	localSequence := int64(4)
	if frontier {
		localSequence = 5
		f.clock.Store(base + 11)
		peerClosureReviewSample(t, f.n, f.child, "up", 5, nil)
		peerClosureReviewSample(t, f.peer, f.child, "up", 5, nil)
	}
	peerResolved := base + 20
	if earlierResolution {
		peerResolved = base + 15
	}
	f.clock.Store(peerResolved)
	peerClosureReviewReceive(t, f.peer, "a", a4)
	f.clock.Store(base + 20)
	peerClosureReviewReceive(t, f.n, "b", b4)
	f.closed = fullReviewControlEpisode(t, f.n, f.child)
	peerClosed := fullReviewControlEpisode(t, f.peer, f.child)
	if !equalJSON(f.closed.Closed, map[string]int64{"a": localSequence, "b": 4}) ||
		!equalJSON(peerClosed.Closed, map[string]int64{"a": 4, "b": localSequence}) ||
		f.closed.ResolvedAt == nil || *f.closed.ResolvedAt != base+20 || peerClosed.ResolvedAt == nil || *peerClosed.ResolvedAt != peerResolved ||
		f.closed.OpenedAt != peerClosed.OpenedAt || !equalJSON(f.closed.Runs, peerClosed.Runs) {
		t.Fatal("actual peers did not produce the required independent closures", f.closed, peerClosed)
	}
	if !f.n.targetSuppressionComplete(f.closed, f.targetA.ID) || f.n.targetSuppressionComplete(f.closed, f.targetB.ID) ||
		!f.n.hasDeliveryIntent(f.closed, f.targetB.ID) || !f.n.hasLocalIncidentDelivery(f.closed, f.targetB.ID) || hasReceipt(f.n.forEpisode(f.closed), "down") {
		t.Fatal("fixture lacks terminal covered A and independently owed B")
	}
	return f
}

func peerClosureReviewCheckView(t *testing.T, f *peerClosureReviewFixture, want string) {
	t.Helper()
	i := fullReviewControlEpisode(t, f.n, f.child)
	found := false
	for _, a := range f.n.attempts {
		if a.MonitorID != f.child.ID || a.DestinationID != f.targetA.ID || a.Kind != "down" || !slices.Contains(episodeIDs(i), a.IncidentID) {
			continue
		}
		found = true
		v := f.n.attemptView(a)
		if v.State != want || v.Held || v.NextAttemptAt != 0 || want == "cancelled" && v.ErrorCode != completedDependency {
			t.Errorf("peer metadata refinement changed terminal route A: want=%s view=%+v", want, v)
		}
	}
	if !found {
		t.Fatal("fixture lost its persisted A attempt")
	}
}

func peerClosureReviewRestart(t *testing.T, f *peerClosureReviewFixture) {
	t.Helper()
	f.release()
	if err := f.n.Stop(); err != nil {
		t.Fatal(err)
	}
	f.n = maintenanceReviewNode(t, f.clock, f.path)
	f.release = fullReviewFollowupBlock(f.n)
	t.Cleanup(f.release)
}

func peerClosureReviewDrain(t *testing.T, f *peerClosureReviewFixture, wantA int) {
	t.Helper()
	f.b.status.Store(204)
	f.clock.Add(1) // Still before the failed parent B's one-second retry.
	peerClosureReviewSample(t, f.n, f.parent, "up", 2, nil)
	peerClosureReviewSample(t, f.n, f.child, "up", 6, nil)
	f.release()
	fullReviewControlFlush(f.n)
	if down, recovery := fullReviewCount(f.a, f.child.ID, "down"), fullReviewCount(f.a, f.child.ID, "recovery"); down != 0 || recovery != wantA {
		t.Errorf("covered A replayed stale work or lost receipt-backed recovery: down=%d recovery=%d wantRecovery=%d", down, recovery, wantA)
	}
	if down, recovery := fullReviewCount(f.b, f.child.ID, "down"), fullReviewCount(f.b, f.child.ID, "recovery"); down != 1 || recovery != 1 {
		t.Errorf("uncovered B lost its owed pair: down=%d recovery=%d", down, recovery)
	}
	if fullReviewCount(f.b, f.parent.ID, "down") != 1 {
		t.Fatal("parent B retried before the owed-child control completed")
	}
}

func TestReviewPeerClosureRefinementPreservesCompletion(t *testing.T) {
	for _, restart := range []bool{false, true} {
		for _, mode := range []string{"frontier", "receipt-precedence"} {
			t.Run(fmt.Sprintf("%s/restart=%v", mode, restart), func(t *testing.T) {
				f := newPeerClosureReviewFixture(t, restart, true, mode != "frontier")
				wantView, wantRecovery := "cancelled", 0
				if mode == "receipt-precedence" {
					// A real persisted peer acceptance outranks a local cancellation;
					// this node must send recovery even though it sent no child down.
					r := Receipt{NotificationView: NotificationView{ID: attemptIdentity(f.child.ID, f.closed.ID, "down", 0, f.targetA.ID), MonitorID: f.child.ID, IncidentID: f.closed.ID, Kind: "down", NodeID: "b", SentAt: f.clock.Load(), DestinationID: f.targetA.ID, ChannelID: f.targetA.ChannelID, Provider: f.targetA.Provider}}
					maintenanceReviewMerge(t, f.n, Snapshot{Receipts: []Receipt{r}})
					wantView, wantRecovery = "accepted", 1
				}
				peerClosureReviewReceive(t, f.n, "b", peerClosureReviewWire(t, f.peer))
				merged := fullReviewControlEpisode(t, f.n, f.child)
				wantSequence := int64(5)
				wantResolved := maintenanceClock(11, 58) + 15
				if mode == "frontier" {
					wantResolved += 5
				}
				if !equalJSON(merged.Closed, map[string]int64{"a": wantSequence, "b": wantSequence}) || merged.ResolvedAt == nil || *merged.ResolvedAt != wantResolved ||
					merged.OpenedAt != f.closed.OpenedAt || !equalJSON(merged.Runs, f.closed.Runs) {
					t.Fatal("validated peer closure did not refine only closure metadata", merged, f.closed)
				}
				peerClosureReviewCheckView(t, f, wantView)
				if restart {
					peerClosureReviewRestart(t, f)
					peerClosureReviewCheckView(t, f, wantView)
				}
				peerClosureReviewDrain(t, f, wantRecovery)
			})
		}
	}
}

func TestReviewPeerClosurePeerExtensionsInvalidateCompletion(t *testing.T) {
	for _, mode := range []string{"new-run", "earlier-opened"} {
		t.Run(mode, func(t *testing.T) {
			f := newPeerClosureReviewFixture(t, false, true, true)
			extended := combine(f.closed, f.closed)
			extended.EvidenceRoot = ""
			if mode == "new-run" {
				// A missed down/up inside the old closure frontier expands the run
				// evidence even though the recovery timestamp need not change.
				extended.Runs["a"] = append(extended.Runs["a"], 3)
			} else {
				extended.OpenedAt--
			}
			maintenanceReviewMerge(t, f.n, Snapshot{Incidents: []Incident{extended}})
			i := fullReviewControlEpisode(t, f.n, f.child)
			if mode == "new-run" && !slices.Contains(i.Runs["a"], int64(3)) || mode == "earlier-opened" && i.OpenedAt != f.closed.OpenedAt-1 {
				t.Fatal("peer extension did not reach the actual source projection", i)
			}
			if f.n.targetSuppressionComplete(i, f.targetA.ID) {
				t.Error("new outage evidence borrowed the old completion")
			}
			f.b.status.Store(204)
			f.clock.Add(1)
			peerClosureReviewSample(t, f.n, f.parent, "up", 2, nil)
			f.release()
			fullReviewControlFlush(f.n)
			if down, recovery := fullReviewCount(f.a, f.child.ID, "down"), fullReviewCount(f.a, f.child.ID, "recovery"); down != 1 || recovery != 1 {
				t.Errorf("extended lifetime lost owed A: down=%d recovery=%d", down, recovery)
			}
		})
	}
}

// Conservative guards that cannot all be expressed as same-source peer
// updates: combine takes the minimum resolution time and retains closure.
// A changed resolution time remains uncertain: a parent's delivery could have
// postdated the newly earlier boundary. Preserve the existing fail-open policy
// while ignoring vector-frontier refinement that adds no outage evidence.
func TestReviewPeerClosureResolutionChangeFailsOpen(t *testing.T) {
	for _, delta := range []int64{-1, 1} {
		t.Run(fmt.Sprint(delta), func(t *testing.T) {
			f := newPeerClosureReviewFixture(t, false, false, false)
			i := combine(f.closed, f.closed)
			i.ResolvedAt = Ptr(*i.ResolvedAt + delta)
			if f.n.targetSuppressionComplete(i, f.targetA.ID) {
				t.Fatal("changed resolution borrowed historical suppression without timing proof")
			}
			f.n.deliveryHold(f.child, i, f.targetA, false)
			if f.n.targetSuppressionComplete(i, f.targetA.ID) {
				t.Fatal("hold scheduling restored a completion for a changed resolution")
			}
		})
	}
}
