package canary

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"uptime-canary/apps/node-go/internal/origin"
)

// These review fixtures never Start a listener or use a real probe/provider.
func originRestoreReviewNode(t *testing.T, o Options) *Node {
	t.Helper()
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	n.nextSync = maxSafe
	t.Cleanup(func() { _ = n.Stop() })
	return n
}

func originRestoreReviewOptions(t *testing.T) Options {
	t.Helper()
	dir := t.TempDir()
	return Options{NodeID: "a", DBPath: filepath.Join(dir, "source.db"), IdentityDirectory: filepath.Join(dir, "identity"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken}
}

func originRestoreReviewMonitor(t *testing.T, n *Node, id string) Monitor {
	t.Helper()
	in := input([]string{"a"}, "http://127.0.0.1:1/", "")
	if err := validateMonitor(&in, n.known); err != nil {
		t.Fatal(err)
	}
	m, err := n.writeMonitor(in, nil, id, false)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func originRestoreReviewStage(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	backup, staged := filepath.Join(dir, "backup.db"), filepath.Join(dir, "staged.db")
	if _, err := BackupDatabase(context.Background(), source, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareDatabaseRestore(context.Background(), backup, staged); err != nil {
		t.Fatal(err)
	}
	return staged
}

func TestOriginRestoreReviewStopRetainsLockUntilProbeExits(t *testing.T) {
	o := originRestoreReviewOptions(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	o.Probe = func(context.Context, Monitor) ProbeResult {
		close(entered)
		<-release // Deliberately take time to stop after context cancellation.
		return ProbeResult{State: "down", Message: "private fixture"}
	}
	n := originRestoreReviewNode(t, o)
	m := originRestoreReviewMonitor(t, n, "stop-lock")
	n.tick()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("probe did not enter")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- n.Stop() }()
	select {
	case <-n.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not cancel runtime")
	}
	alias := o.IdentityDirectory
	if runtime.GOOS == "windows" {
		alias = strings.ToUpper(alias)
	}
	r, err := origin.Open(origin.Options{Directory: alias, NodeID: o.NodeID, RequireExisting: true})
	if r != nil {
		_ = r.Close()
	}
	if !errors.Is(err, origin.ErrLocked) {
		t.Fatal("shutdown released origin before writer stopped", err)
	}
	other := o
	other.DBPath = filepath.Join(filepath.Dir(o.DBPath), "other.db")
	other.IdentityDirectory = alias
	if duplicate, err := New(other); err == nil {
		_ = duplicate.Stop()
		t.Fatal("another database acquired the identity during shutdown")
	}
	once.Do(func() { close(release) })
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not finish after writer exit")
	}
	if len(n.samples) != 0 || len(n.probePolicies) != 0 || n.sequence != 0 {
		t.Fatal("canceled writer committed a result")
	}
	other.Probe = nil
	reopened := originRestoreReviewNode(t, other)
	seq, err := reopened.nextProbeSequence()
	if err != nil || seq <= origin.BlockSize {
		t.Fatal("new owner reused stopped owner's reservation", seq, err)
	}
	if _, exists := reopened.heads[m.ID]; exists {
		t.Fatal("independent database unexpectedly copied monitor state")
	}
}

func TestOriginRestoreReviewPendingRestartAndPolicyEdits(t *testing.T) {
	o := originRestoreReviewOptions(t)
	var now atomic.Int64
	now.Store(maintenanceClock(12, 0))
	raw := "down"
	o.Now = now.Load
	o.Probe = func(context.Context, Monitor) ProbeResult { return ProbeResult{State: raw, Message: "fixture"} }
	n := originRestoreReviewNode(t, o)
	m := originRestoreReviewMonitor(t, n, "pending-restart")
	in := m.MonitorInput
	in.Retries = 1
	var err error
	m, err = n.writeMonitor(in, n.heads[m.ID], m.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	tick := func() { now.Add(200); n.tick(); n.wg.Wait() }
	tick()
	tick()
	s := n.samples[m.ID+"/a"]
	if s.State != "down" || s.DownSince == nil || len(n.episodes) != 1 {
		t.Fatal("fixture did not confirm outage", s)
	}
	run := *s.DownSince
	var incident string
	for id := range n.episodes {
		incident = id
	}
	raw = "pending"
	tick()
	if p := n.probePolicies[m.ID]; p.State.ConsecutiveFailures != 0 || p.DownSince == nil || *p.DownSince != run {
		t.Fatal("service pending lost durable outage continuity", p)
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = originRestoreReviewNode(t, o)
	raw = "down"
	tick()
	if s := n.samples[m.ID+"/a"]; s.State != "pending" || s.ConsecutiveFailures != 1 || n.episodes[incident].ResolvedAt != nil {
		t.Fatal("restart treated retry pending as recovery", s)
	}
	oldGeneration := checkGeneration(m)
	in.IntervalMs, in.RetryIntervalMs = 200, Ptr(int64(100))
	m, err = n.writeMonitor(in, n.heads[m.ID], m.ID, false)
	if err != nil || checkGeneration(m) != oldGeneration {
		t.Fatal("interval-only edit changed generation", err)
	}
	tick()
	if s := n.samples[m.ID+"/a"]; s.State != "down" || s.ConsecutiveFailures != 2 || s.DownSince == nil || *s.DownSince != run || len(n.episodes) != 1 {
		t.Fatal("interval edit reset retry/outage evidence", s)
	}
	in.UpsideDown = true
	m, err = n.writeMonitor(in, n.heads[m.ID], m.ID, false)
	if err != nil || checkGeneration(m) == oldGeneration {
		t.Fatal("inversion edit retained old generation", err)
	}
	tick()
	if s := n.samples[m.ID+"/a"]; s.State != "up" || s.RawState != "down" || s.ConsecutiveFailures != 0 || n.probePolicies[m.ID].DownSince != nil {
		t.Fatal("new generation inherited old retry policy or inverted twice", s)
	}
	if i := n.episodes[incident]; i.RetiredBy != m.Revision || i.ResolvedAt == nil || hasReceipt(n.forEpisode(i), "recovery") {
		t.Fatal("semantic edit did not administratively retire old outage", i)
	}
}

func TestOriginRestoreReviewRejectsLateResultAfterIntervalEdit(t *testing.T) {
	o := originRestoreReviewOptions(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var calls atomic.Int64
	o.Probe = func(context.Context, Monitor) ProbeResult {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return ProbeResult{State: "down", Message: "fixture"}
	}
	n := originRestoreReviewNode(t, o)
	m := originRestoreReviewMonitor(t, n, "late-result")
	n.tick()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("probe did not enter")
	}
	in := m.MonitorInput
	in.IntervalMs = 200
	n.mu.Lock()
	edited, err := n.writeMonitor(in, n.heads[m.ID], m.ID, false)
	n.mu.Unlock()
	if err != nil || edited.Revision == m.Revision || checkGeneration(edited) != checkGeneration(m) {
		t.Fatal("fixture did not make a generation-preserving revision", err)
	}
	once.Do(func() { close(release) })
	n.wg.Wait()
	if len(n.samples) != 0 || len(n.probePolicies) != 0 || n.sequence != 0 {
		t.Fatal("late old-revision result advanced local policy/sequence")
	}
	history, err := n.store.history(m.ID, 10)
	if err != nil || len(history) != 0 {
		t.Fatal("late old-revision result wrote history", err)
	}
	n.tick()
	n.wg.Wait()
	if s := n.samples[m.ID+"/a"]; s.Revision != edited.Revision || s.ConsecutiveFailures != 1 || calls.Load() != 2 || n.Error() != nil {
		t.Fatal("current revision did not begin with one accepted failure", s, n.Error())
	}
}

func TestOriginRestoreReviewActivationPreservesConflictingHeads(t *testing.T) {
	for _, tombstone := range []bool{false, true} {
		t.Run(map[bool]string{false: "two-live", true: "live-and-deleted"}[tombstone], func(t *testing.T) {
			o := originRestoreReviewOptions(t)
			n := originRestoreReviewNode(t, o)
			m := originRestoreReviewMonitor(t, n, "conflicted")
			// Two independently committed children of the same observed root.
			// The local API correctly refuses an edit based on a stale vote set.
			branches := Snapshot{}
			for index, target := range []string{m.Target + "left", m.Target + "right"} {
				branch := m
				branch.Target, branch.Revision, branch.CheckGeneration = target, newID(), ""
				branch.Deleted = index == 1 && tombstone
				e := newRevisionEdge("monitor", m.ID, branch.Revision, branch, []string{m.Revision}, checkGeneration(branch))
				branches.Versions = append(branches.Versions, Version{Monitor: branch, Ancestors: e.Parents, EdgeID: e.ID})
				branches.RevisionEdges = append(branches.RevisionEdges, e)
			}
			if err := n.applySnapshot(branches); err != nil {
				t.Fatal(err)
			}
			before := append([]Version{}, n.heads[m.ID]...)
			if len(before) != 2 || n.current(m.ID) != nil {
				t.Fatal("fixture did not create conflicting heads")
			}
			staged := originRestoreReviewStage(t, o.DBPath)
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			result, err := ActivateRestoredDatabase(context.Background(), staged, o.IdentityDirectory)
			if err != nil || result.MonitorsReset != 1 {
				t.Fatal("conflict activation failed", result, err)
			}
			o.DBPath = staged
			restored := originRestoreReviewNode(t, o)
			if len(restored.heads[m.ID]) != 2 || restored.current(m.ID) != nil {
				t.Fatal("activation chose a conflict winner")
			}
			for _, old := range before {
				found := false
				for _, v := range restored.heads[m.ID] {
					if old.Monitor.Deleted {
						found = found || equalJSON(v, old)
					} else if v.Monitor.Target == old.Monitor.Target {
						e := restored.revisionEdges[v.EdgeID]
						found = len(e.Parents) == 1 && e.Parents[0] == old.Monitor.Revision && checkGeneration(v.Monitor) != checkGeneration(old.Monitor)
					}
				}
				if !found {
					t.Fatal("live head was not individually reset or tombstone changed")
				}
			}
		})
	}
}

func TestOriginRestoreReviewActivationCannotInventMissingParentProof(t *testing.T) {
	o := originRestoreReviewOptions(t)
	var probes atomic.Int64
	o.Probe = func(context.Context, Monitor) ProbeResult {
		probes.Add(1)
		return ProbeResult{State: "up", Message: "fixture"}
	}
	n := originRestoreReviewNode(t, o)
	in := input([]string{"a"}, "http://127.0.0.1:1/", "")
	if err := validateMonitor(&in, n.known); err != nil {
		t.Fatal(err)
	}
	root := Monitor{MonitorInput: in, ID: "partial", Revision: newID(), UpdatedAt: n.now()}
	parent := newRevisionEdge("monitor", root.ID, root.Revision, root, nil, checkGeneration(root))
	child := root
	child.Target, child.Revision = root.Target+"child", newID()
	edge := newRevisionEdge("monitor", child.ID, child.Revision, child, []string{root.Revision}, checkGeneration(child))
	v := Version{Monitor: child, Ancestors: edge.Parents, EdgeID: edge.ID}
	if err := n.applySnapshot(Snapshot{Versions: []Version{v}, RevisionEdges: []RevisionEdge{edge}}); err != nil {
		t.Fatal(err)
	}
	if n.current(child.ID) != nil {
		t.Fatal("fixture accepted incomplete parent proof")
	}
	staged := originRestoreReviewStage(t, o.DBPath)
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := ActivateRestoredDatabase(context.Background(), staged, o.IdentityDirectory); err != nil {
		t.Fatal(err)
	}
	o.DBPath = staged
	restored := originRestoreReviewNode(t, o)
	restored.tick()
	restored.wg.Wait()
	if restored.current(child.ID) != nil || probes.Load() != 0 || len(restored.samples) != 0 {
		t.Fatal("activation turned absent ancestry into runnable proof")
	}
	if err := restored.mergeSnapshot(Snapshot{RevisionEdges: []RevisionEdge{parent}}); err != nil {
		t.Fatal("delayed parent proof was rejected", err)
	}
	if restored.current(child.ID) == nil {
		t.Fatal("complete restored proof did not become runnable")
	}
	restored.tick()
	restored.wg.Wait()
	if probes.Load() != 1 || restored.Error() != nil {
		t.Fatal("restored generation did not run after proof arrived", restored.Error())
	}
}

func TestOriginRestoreReviewLaterPeerRevisionRemainsConflict(t *testing.T) {
	o := originRestoreReviewOptions(t)
	o.Peers = []Peer{{ID: "b", URL: "http://127.0.0.1:1"}}
	n := originRestoreReviewNode(t, o)
	m := originRestoreReviewMonitor(t, n, "remote-later")
	staged := originRestoreReviewStage(t, o.DBPath)
	in := m.MonitorInput
	in.Target += "later"
	later, err := n.writeMonitor(in, n.heads[m.ID], m.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	peer := originRestoreReviewNode(t, Options{NodeID: "b", DBPath: ":memory:", Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Peers: []Peer{{ID: "a", URL: "http://127.0.0.1:1"}}})
	transfer := func(from, to *Node) {
		t.Helper()
		cursor := ""
		for range 20 {
			page, err := from.snapshotPage(cursor, "")
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(page)
			if err != nil {
				t.Fatal(err)
			}
			validated, err := to.validateSnapshot(body, from.ID)
			if err != nil {
				t.Fatal("real peer page failed validation", err)
			}
			if err := to.mergeSnapshot(validated); err != nil {
				t.Fatal(err)
			}
			if page.Next == "" {
				return
			}
			cursor = page.Next
		}
		t.Fatal("small review peer sweep did not finish")
	}
	transfer(n, peer)
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := ActivateRestoredDatabase(context.Background(), staged, o.IdentityDirectory); err != nil {
		t.Fatal(err)
	}
	o.DBPath = staged
	restored := originRestoreReviewNode(t, o)
	reset := restored.current(m.ID)
	if reset == nil || checkGeneration(*reset) == checkGeneration(m) {
		t.Fatal("backup did not receive a fresh generation")
	}
	transfer(restored, peer)
	transfer(peer, restored)
	for _, node := range []*Node{peer, restored} {
		if len(node.heads[m.ID]) != 2 || node.current(m.ID) != nil {
			t.Fatal("restore discarded a later peer edit or chose a branch")
		}
		seen := map[string]bool{}
		for _, v := range node.heads[m.ID] {
			seen[v.Monitor.Revision] = true
		}
		if !seen[later.Revision] || !seen[reset.Revision] {
			t.Fatal("peer exchange lost one branch")
		}
	}
}

func TestOriginRestoreReviewActivationAuthRollbackAndReplayFence(t *testing.T) {
	o := originRestoreReviewOptions(t)
	n := originRestoreReviewNode(t, o)
	a, err := setPassword(n.accounts["bootstrap-admin"], "review fixture password")
	if err != nil {
		t.Fatal(err)
	}
	a.MFAEnabled, a.TOTPSecret, a.TOTPLastStep = true, "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP", 1
	a.PendingTOTP, a.PendingTOTPExpires = "pending-fixture", n.now()+600000
	if err := put(n.store.db, "accounts", a.ID, a); err != nil {
		t.Fatal(err)
	}
	if err := put(n.store.db, "sessions", "backed-up-session", loginSession{AccountID: a.ID, AuthVersion: a.AuthVersion, ExpiresAt: n.now() + 600000}); err != nil {
		t.Fatal(err)
	}
	staged := originRestoreReviewStage(t, o.DBPath)
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(staged)
	if err != nil {
		t.Fatal(err)
	}
	// Add a session after preparation to exercise activation's own deletion.
	if err := put(s.db, "sessions", "staged-session", loginSession{AccountID: a.ID, AuthVersion: a.AuthVersion, ExpiresAt: time.Now().UnixMilli() + 600000}); err != nil {
		_ = s.close()
		t.Fatal(err)
	}
	_, err = s.db.Exec(`CREATE TRIGGER review_reject_activation BEFORE UPDATE ON records WHEN NEW.category='settings' AND NEW.id='restoreQuarantined' AND NEW.payload='false' BEGIN SELECT RAISE(FAIL,'private review diagnostic'); END`)
	_ = s.close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ActivateRestoredDatabase(context.Background(), staged, o.IdentityDirectory); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("failed activation succeeded or leaked diagnostics", err)
	}
	s, err = openStore(staged)
	if err != nil {
		t.Fatal(err)
	}
	stored, _, accountErr := loadOne[account](s, "accounts", a.ID)
	sessions, sessionErr := loadAll[loginSession](s, "sessions")
	quarantined, _, quarantineErr := loadOne[bool](s, "settings", "restoreQuarantined")
	_, markerExists, markerErr := loadOne[RestoreActivation](s, "settings", "restoreActivation")
	if accountErr != nil || sessionErr != nil || quarantineErr != nil || markerErr != nil || !equalJSON(stored, a) || len(sessions) != 1 || !quarantined || markerExists {
		_ = s.close()
		t.Fatal("failed activation partially committed auth/quarantine state")
	}
	_, err = s.db.Exec("DROP TRIGGER review_reject_activation")
	_ = s.close()
	if err != nil {
		t.Fatal(err)
	}
	result, err := ActivateRestoredDatabase(context.Background(), staged, o.IdentityDirectory)
	if err != nil {
		t.Fatal(err)
	}
	var now atomic.Int64
	now.Store(result.ActivatedAt)
	o.DBPath, o.Now = staged, now.Load
	restored := originRestoreReviewNode(t, o)
	stored = restored.accounts[a.ID]
	if stored.AuthVersion != a.AuthVersion+1 || stored.TOTPSecret != a.TOTPSecret || stored.PendingTOTP != "" || stored.PendingTOTPExpires != 0 || len(restored.sessions) != 0 || stored.TOTPLastStep < result.ActivatedAt/30000+1 {
		t.Fatal("activation did not durably fence sessions and TOTP replay")
	}
	login := func(step int64) int {
		body, _ := json.Marshal(map[string]string{"name": a.Name, "password": "review fixture password", "code": totpCode(a.TOTPSecret, step)})
		return identityCall(restored, "POST", "/api/auth/login", string(body), "").Code
	}
	for delta := int64(-1); delta <= 1; delta++ {
		if code := login(result.ActivatedAt/30000 + delta); code != 401 {
			t.Fatal("restored current-window TOTP was accepted", delta, code)
		}
	}
	freshStep := stored.TOTPLastStep + 1
	now.Store(freshStep * 30000)
	if code := login(freshStep); code != 200 {
		t.Fatal("new TOTP step could not establish a session", code)
	}
	if code := login(freshStep); code != 401 {
		t.Fatal("accepted TOTP step could be replayed", code)
	}
}
