package canary

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOriginReservationsOutliveDatabasePathsAndProbeFailures(t *testing.T) {
	dir := t.TempDir()
	o := Options{NodeID: "a", DBPath: filepath.Join(dir, "one.db"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken}
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Stop()
	other := o
	other.DBPath = filepath.Join(dir, "two.db")
	if duplicate, err := New(other); err == nil {
		_ = duplicate.Stop()
		t.Fatal("same retained identity could execute from two different databases")
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	next, err := New(other)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Stop()
	seq, err := next.nextProbeSequence()
	if err != nil || seq <= 65536 {
		t.Fatal("a new database reused the old reservation", seq, err)
	}
	if err := next.store.origin.Close(); err != nil {
		t.Fatal(err)
	}
	var now atomic.Int64
	now.Store(maintenanceClock(12, 0))
	next.now = now.Load
	next.nextSync = maxSafe
	next.probe = func(context.Context, Monitor) ProbeResult { return ProbeResult{State: "up", Message: "fixture"} }
	m, err := next.writeMonitor(input([]string{"a"}, "http://127.0.0.1:1", ""), nil, "origin-fault", false)
	if err != nil {
		t.Fatal(err)
	}
	next.tick()
	next.wg.Wait()
	if next.Error() == nil || len(next.samples) != 0 || len(next.probePolicies) != 0 {
		t.Fatal("failed reservation published local work", next.Error())
	}
	history, err := next.store.history(m.ID, 10)
	if err != nil || len(history) != 0 {
		t.Fatal("failed reservation added history", err)
	}
}

func TestRestoreActivationRequiresRetainedIdentityAndResetsContinuity(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Now().UnixMilli() - 1000)
	raw := "down"
	r := webhook(t)
	dir := t.TempDir()
	o := Options{NodeID: "a", DBPath: filepath.Join(dir, "source.db"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: now.Load, NotificationDelayMs: Ptr(int64(0)), Probe: func(context.Context, Monitor) ProbeResult { return ProbeResult{State: raw, Message: "fixture"} }}
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Stop()
	n.nextSync = maxSafe
	in := input([]string{"a"}, "http://127.0.0.1:1/", r.URL)
	if err := validateMonitor(&in, n.known); err != nil {
		t.Fatal(err)
	}
	m, err := n.writeMonitor(in, nil, "restored-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	tick := func(node *Node) { now.Add(100); node.tick(); node.wg.Wait() }
	tick(n)
	if r.count("down") != 1 {
		t.Fatal("missing initial outage")
	}
	oldIncident := r.all()[0].IncidentID
	backup := filepath.Join(t.TempDir(), "backup.db")
	if _, err := BackupDatabase(context.Background(), o.DBPath, backup); err != nil {
		t.Fatal(err)
	}
	raw = "up"
	tick(n)
	raw = "down"
	tick(n)
	lastSequence := n.sequence
	observer, err := New(Options{NodeID: "b", DBPath: ":memory:", Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: now.Load, Peers: []Peer{{ID: "a", URL: "http://127.0.0.1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Stop()
	transfer := func(source *Node) {
		t.Helper()
		cursor := ""
		for pageCount := 0; pageCount < 100; pageCount++ {
			page, pageErr := source.snapshotPage(cursor, "")
			if pageErr != nil {
				t.Fatal(pageErr)
			}
			b, _ := json.Marshal(page)
			validated, validationErr := observer.validateSnapshot(b, "a")
			if validationErr != nil {
				t.Fatal("self-produced restore evidence rejected", validationErr)
			}
			if err := observer.mergeSnapshot(validated); err != nil {
				t.Fatal("peer merge", cursor, err)
			}
			if page.Next == "" {
				return
			}
			cursor = page.Next
		}
		t.Fatal("restore fixture peer sweep did not finish")
	}
	transfer(n)
	if r.count("down") != 2 || r.count("recovery") != 1 {
		t.Fatal("fixture did not make a later outage")
	}
	staged := filepath.Join(t.TempDir(), "restored.db")
	if _, err := PrepareDatabaseRestore(context.Background(), backup, staged); err != nil {
		t.Fatal(err)
	}
	identityDirectory := filepath.Join(dir, ".canary-identities")
	if _, err := ActivateRestoredDatabase(context.Background(), staged, identityDirectory); err == nil {
		t.Fatal("activation ignored the running origin owner")
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := ActivateRestoredDatabase(context.Background(), staged, ""); err == nil {
		t.Fatal("activation bootstrapped missing retained identity state")
	}
	result, err := ActivateRestoredDatabase(context.Background(), staged, identityDirectory)
	if err != nil || result.MonitorsReset != 1 || result.SequenceFloor <= lastSequence {
		t.Fatal("activation did not establish a new origin floor", result, err)
	}
	o.DBPath, o.IdentityDirectory = staged, identityDirectory
	restored, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Stop()
	restored.nextSync = maxSafe
	current := restored.current(m.ID)
	if current == nil || checkGeneration(*current) == checkGeneration(m) || current.ID != m.ID || current.Target != m.Target {
		t.Fatal("activation failed to reset only continuity", current)
	}
	if prior := restored.episodes[oldIncident]; prior.RetiredBy == "" || prior.ResolvedAt == nil {
		t.Fatal("backup's open outage was not administratively retired")
	}
	tick(restored)
	if restored.sequence <= result.SequenceFloor || r.count("down") != 3 || r.count("recovery") != 1 {
		t.Fatal("historical receipt suppressed fresh restored monitoring", restored.sequence, r.count("down"), r.count("recovery"))
	}
	if r.all()[len(r.all())-1].IncidentID == oldIncident {
		t.Fatal("restore reused the backed-up incident identity")
	}
	transfer(restored)
	if got := observer.current(m.ID); got == nil || got.Revision != current.Revision || observer.samples[m.ID+"/a"].Sequence != restored.sequence {
		t.Fatal("peer could not accept restored monitoring generation and origin", got)
	}
}

func TestRestoreActivationCommitFailureKeepsQuarantine(t *testing.T) {
	dir := t.TempDir()
	o := Options{NodeID: "a", DBPath: filepath.Join(dir, "source.db"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken}
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Stop()
	backup := filepath.Join(dir, "backup.db")
	if _, err := BackupDatabase(context.Background(), o.DBPath, backup); err != nil {
		t.Fatal(err)
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "staged.db")
	if _, err := PrepareDatabaseRestore(context.Background(), backup, staged); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(staged)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`CREATE TRIGGER block_activation BEFORE UPDATE ON records WHEN NEW.category='settings' AND NEW.id='restoreQuarantined' AND NEW.payload='false' BEGIN SELECT RAISE(FAIL,'private activation diagnostic'); END`)
	_ = s.close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ActivateRestoredDatabase(context.Background(), staged, ""); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("partial activation was acknowledged or leaked diagnostics", err)
	}
	o.DBPath = staged
	if unsafe, err := New(o); err == nil {
		_ = unsafe.Stop()
		t.Fatal("failed activation released quarantine")
	}
	s, err = openStore(staged)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec("DROP TRIGGER block_activation")
	_ = s.close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ActivateRestoredDatabase(context.Background(), staged, ""); err != nil {
		t.Fatal("safe activation retry failed", err)
	}
	active, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer active.Stop()
}
