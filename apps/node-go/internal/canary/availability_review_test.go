package canary

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"uptime-canary/apps/node-go/internal/availability"
)

// Review fixtures use disposable SQLite files and an in-process handler. They
// never Start a listener, load environment files, or contact probe/peer services.
func archiveReviewNode(t *testing.T, clock *atomic.Int64, path, identity string) *Node {
	t.Helper()
	n, err := New(Options{NodeID: "a", DBPath: path, IdentityDirectory: identity,
		AdminToken: adminToken, ClusterToken: clusterToken, Port: Ptr(0), Now: clock.Load,
		Peers: []Peer{{ID: "b", URL: "http://127.0.0.1:1"}},
		Probe: func(context.Context, Monitor) ProbeResult { return ProbeResult{State: "up"} }})
	if err != nil {
		t.Fatal(err)
	}
	n.nextSync = maxSafe
	t.Cleanup(func() { _ = n.Stop() })
	return n
}

func archiveReviewMonitor(t *testing.T, n *Node, id string) Monitor {
	t.Helper()
	in := MonitorInput{Name: "Private archive review", Type: "manual", Enabled: true,
		InternalOnly: true, IntervalMs: 100, TimeoutMs: 70, NodeIDs: []string{"a", "b"}, Manual: &ManualMonitorOptions{State: "up"}}
	if err := validateMonitor(&in, n.known); err != nil {
		t.Fatal(err)
	}
	m, err := n.writeMonitor(in, nil, id, false)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func archiveReviewDrive(t *testing.T, n *Node) {
	t.Helper()
	n.mu.Lock()
	n.nextSync = maxSafe
	for id := range n.heads {
		n.nextProbe[id] = 0
	}
	n.mu.Unlock()
	n.tick()
	n.wg.Wait()
	if err := n.Error(); err != nil {
		t.Fatal(err)
	}
}

func archiveReviewRequest(n *Node, id, raw, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/api/monitors/"+id+"/availability?"+raw, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	n.ServeHTTP(w, r)
	return w
}

func archiveReviewRead(t *testing.T, n *Node, id string, from, to, step int64) availabilityHistoryResult {
	t.Helper()
	v, err := n.availabilityHistory(id, url.Values{"from": {strconv.FormatInt(from, 10)},
		"to": {strconv.FormatInt(to, 10)}, "stepMs": {strconv.FormatInt(step, 10)}})
	if err != nil {
		t.Fatal(err)
	}
	return v.(availabilityHistoryResult)
}

func archiveReviewWrite(t *testing.T, n *Node, f func(dbExec) error) {
	t.Helper()
	tx, err := n.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = f(tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

type archiveReviewCell struct {
	state       string
	maintenance bool
}

// This oracle stores labelled milliseconds, not spans, SQL, coalescing rules,
// or returned bucket edges. Every expected duration is counted from those cells.
func archiveReviewCount(t *testing.T, got availability.Totals, cells []archiveReviewCell, from, to int64) {
	t.Helper()
	var up, down, pending, unknown, held int64
	for i := from; i < to; i++ {
		switch cells[i].state {
		case "up":
			up++
		case "down":
			down++
		case "pending":
			pending++
		default:
			unknown++
		}
		if cells[i].maintenance {
			held++
		}
	}
	if got.StartMs != from || got.EndMs != to || got.UpMs != up || got.DownMs != down ||
		got.PendingMs != pending || got.UnknownMs != unknown || got.MaintenanceMs != held || got.KnownMs != up+down {
		t.Fatalf("SQL projection [%d,%d): got %+v; independent cells up=%d down=%d pending=%d unknown=%d held=%d", from, to, got, up, down, pending, unknown, held)
	}
	coverage := 100 * float64(up+down) / float64(to-from)
	if math.IsNaN(got.CoveragePercent) || math.Abs(got.CoveragePercent-coverage) > 1e-10 {
		t.Fatalf("coverage denominator: %+v", got)
	}
	if up+down == 0 {
		if got.AvailabilityPercent != nil {
			t.Fatal("unsupported time acquired availability", got)
		}
	} else if got.AvailabilityPercent == nil || math.IsNaN(*got.AvailabilityPercent) ||
		math.Abs(*got.AvailabilityPercent-100*float64(up)/float64(up+down)) > 1e-10 {
		t.Fatal("known-time denominator", got)
	}
}

func TestAvailabilityStorageReviewLabelledTimelineSQLProjection(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "cells.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	n := &Node{ID: "a", store: s, now: func() int64 { return 4096 }}
	rng := rand.New(rand.NewSource(0x519e))
	for monitor := range 3 {
		m := Monitor{ID: fmt.Sprintf("cells-%d", monitor), Revision: "r0", CheckGeneration: "g0",
			MonitorInput: MonitorInput{Enabled: true, NodeIDs: []string{"a"}}}
		cells := make([]archiveReviewCell, 4096)
		hi, sequence := int64(0), int64(-1)
		var latest Sample
		var deadline int64
		for event := range 90 {
			at := max(int64(0), int64(event*19+rng.Intn(151)-75))
			if event%11 == 0 {
				archiveReviewWrite(t, n, func(e dbExec) error { return closeLocalAvailability(e, m.ID, at) })
				// Closing a never-observed monitor creates no watermark.
				if sequence >= 0 {
					hi = max(hi, at)
					clear(cells[hi:])
				}
			} else {
				seq := int64(event)
				if event%7 == 0 {
					seq = sequence
				} // equal-sequence replay has no authority
				if event%17 == 0 {
					m.Revision, m.CheckGeneration = fmt.Sprint("r", event), fmt.Sprint("g", event)
				}
				state := []string{"up", "down", "pending", "unknown"}[rng.Intn(4)]
				end, held := at+int64(rng.Intn(301)), rng.Intn(2) == 0
				sample := Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", CheckedAt: at, State: state},
					Revision: m.Revision, CheckGeneration: m.CheckGeneration, Sequence: seq}
				archiveReviewWrite(t, n, func(e dbExec) error { return recordLocalAvailability(e, m, sample, end, held) })
				if seq > sequence {
					hi = max(hi, at)
					clear(cells[hi:])
					for i := hi; i < end; i++ {
						cells[i] = archiveReviewCell{state, held}
					}
					sequence, latest, deadline = seq, sample, end
				}
			}
			if sequence >= 0 {
				var high, checked, until, seq int64
				var rev, gen string
				if err := s.db.QueryRow(`SELECT high_water,checked_at,valid_until,sequence,revision,generation FROM availability_heads WHERE monitor_id=?`, m.ID).
					Scan(&high, &checked, &until, &seq, &rev, &gen); err != nil {
					t.Fatal(err)
				}
				if high != hi || checked != latest.CheckedAt || until != deadline || seq != sequence || rev != latest.Revision || gen != latest.CheckGeneration {
					t.Fatalf("original provenance changed at event %d: %d %d %d %d %s %s", event, high, checked, until, seq, rev, gen)
				}
			}
			from := int64(rng.Intn(500))
			to, step := int64(2300), int64(137)
			out := archiveReviewRead(t, n, m.ID, from, to, step)
			archiveReviewCount(t, out.Summary, cells, from, to)
			index := 0
			for start := from; start < to; {
				end := start + 1
				for end < to && end%step != 0 {
					end++
				}
				if index >= len(out.Buckets) {
					t.Fatal("missing epoch bucket")
				}
				archiveReviewCount(t, out.Buckets[index].Totals, cells, start, end)
				start, index = end, index+1
			}
			if index != len(out.Buckets) {
				t.Fatal("extra epoch bucket")
			}
		}
	}
}

// A logical database dump discovers every user table, including archive and
// causal indexes. Sorted encoded rows make rollback comparisons order-free.
func archiveReviewDump(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, name := range names {
		rows, err := db.Query(`SELECT * FROM "` + strings.ReplaceAll(name, `"`, `""`) + `"`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		out[name] = []string{}
		for rows.Next() {
			values, pointers := make([]any, len(columns)), make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			out[name] = append(out[name], string(b))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(out[name])
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAvailabilityStorageReviewRawPruneFailureRollsBackRealSample(t *testing.T) {
	for _, phase := range []string{"insert", "count-prune", "age-prune"} {
		t.Run(phase, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(10000)
			n := archiveReviewNode(t, &clock, filepath.Join(t.TempDir(), "rollback.db"), "")
			m := archiveReviewMonitor(t, n, "late-failure")
			n.historyLimit = 1
			archiveReviewDrive(t, n)
			before := archiveReviewDump(t, n.store.db)
			previous, policy, sequence := n.samples[m.ID+"/a"], n.probePolicies[m.ID], n.sequence
			ddl := `CREATE TRIGGER archive_review_fail BEFORE INSERT ON history BEGIN SELECT RAISE(ABORT,'private review diagnostic'); END`
			if phase != "insert" {
				ddl = `CREATE TRIGGER archive_review_fail BEFORE DELETE ON history BEGIN SELECT RAISE(ABORT,'private review diagnostic'); END`
				if phase == "age-prune" {
					n.historyLimit = 100
					n.historyRetention = time.Second
					clock.Add(2000)
				}
			}
			if _, err := n.store.db.Exec(ddl); err != nil {
				t.Fatal(err)
			}
			clock.Add(100)
			n.nextProbe[m.ID] = 0
			n.tick()
			n.wg.Wait()
			if n.Error() == nil {
				t.Fatal("injected late SQL failure did not fault runtime")
			}
			if !reflect.DeepEqual(n.samples[m.ID+"/a"], previous) || !reflect.DeepEqual(n.probePolicies[m.ID], policy) || n.sequence != sequence {
				t.Fatal("failed sample published staged memory")
			}
			if after := archiveReviewDump(t, n.store.db); after != before {
				t.Fatal("sample, policy, sequence, archive, raw history, or causal index escaped rollback")
			}
		})
	}
}

func TestAvailabilityStorageReviewCommitFailureRollsBack(t *testing.T) {
	for _, action := range []string{"sample", "configuration"} {
		t.Run(action, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(10000)
			n := archiveReviewNode(t, &clock, ":memory:", "")
			m := archiveReviewMonitor(t, n, "commit-failure")
			archiveReviewDrive(t, n)
			_, err := n.store.db.Exec(`PRAGMA foreign_keys=ON;
CREATE TABLE archive_review_parent(id INTEGER PRIMARY KEY);
CREATE TABLE archive_review_child(parent INTEGER REFERENCES archive_review_parent(id) DEFERRABLE INITIALLY DEFERRED);`)
			if err != nil {
				t.Fatal(err)
			}
			// Prove the fixture fails specifically on COMMIT, after a successful
			// statement. The cached SQLite driver must leave the connection clean.
			tx, err := n.store.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(`INSERT INTO archive_review_child VALUES(1)`); err != nil {
				tx.Rollback()
				t.Fatal("fixture failed before commit", err)
			}
			if err := tx.Commit(); err == nil {
				t.Fatal("fixture did not fail commit")
			}
			ddl := `CREATE TRIGGER archive_review_commit_fail AFTER INSERT ON history BEGIN INSERT INTO archive_review_child VALUES(1); END`
			if action == "configuration" {
				ddl = `CREATE TRIGGER archive_review_commit_fail AFTER UPDATE ON availability_heads WHEN NEW.closed=1 BEGIN INSERT INTO archive_review_child VALUES(1); END`
			}
			if _, err := n.store.db.Exec(ddl); err != nil {
				t.Fatal(err)
			}
			before := archiveReviewDump(t, n.store.db)
			previous, policy, sequence := n.samples[m.ID+"/a"], n.probePolicies[m.ID], n.sequence
			clock.Add(100)
			if action == "sample" {
				n.nextProbe[m.ID] = 0
				n.tick()
				n.wg.Wait()
				if n.Error() == nil {
					t.Fatal("failed sample COMMIT did not fault runtime")
				}
			} else {
				in := m.MonitorInput
				in.Enabled = false
				if _, err := n.writeMonitor(in, n.heads[m.ID], m.ID, false); err == nil {
					t.Fatal("configuration COMMIT unexpectedly succeeded")
				}
				if current := n.current(m.ID); current == nil || current.Revision != m.Revision || !current.Enabled {
					t.Fatal("failed configuration COMMIT published memory")
				}
			}
			if archiveReviewDump(t, n.store.db) != before {
				t.Fatal("COMMIT failure left sample/config/archive changes on pooled connection")
			}
			if !reflect.DeepEqual(n.samples[m.ID+"/a"], previous) || !reflect.DeepEqual(n.probePolicies[m.ID], policy) || n.sequence != sequence {
				t.Fatal("failed COMMIT published local state")
			}
			probe, err := n.store.db.Begin()
			if err != nil {
				t.Fatal("failed COMMIT poisoned pooled connection", err)
			}
			if err := probe.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAvailabilityStorageReviewConfigurationClosures(t *testing.T) {
	for _, change := range []string{"metadata", "disable", "delete", "assignment", "conflict", "unready", "group", "maintenance"} {
		t.Run(change, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(12, 1))
			n := archiveReviewNode(t, &clock, ":memory:", "")
			m := archiveReviewMonitor(t, n, "config-close")
			other := archiveReviewMonitor(t, n, "other-local")
			archiveReviewDrive(t, n)
			base := clock.Load()
			clock.Add(47)
			in := m.MonitorInput
			var err error
			switch change {
			case "metadata":
				in.Name = "Edited metadata"
				_, err = n.writeMonitor(in, n.heads[m.ID], m.ID, false)
			case "disable":
				in.Enabled = false
				_, err = n.writeMonitor(in, n.heads[m.ID], m.ID, false)
			case "delete":
				_, err = n.writeMonitor(in, n.heads[m.ID], m.ID, true)
			case "assignment":
				in.NodeIDs = []string{"b"}
				_, err = n.writeMonitor(in, n.heads[m.ID], m.ID, false)
			case "conflict":
				candidate := m
				candidate.Revision, candidate.CheckGeneration, candidate.Name, candidate.UpdatedAt = newID(), "", "Concurrent independent root", clock.Load()
				edge := newRevisionEdge("monitor", m.ID, candidate.Revision, candidate, nil, checkGeneration(candidate))
				err = n.applySnapshot(Snapshot{Versions: []Version{{Monitor: candidate, EdgeID: edge.ID, Ancestors: edge.Parents}}, RevisionEdges: []RevisionEdge{edge}})
			case "unready":
				candidate := m
				candidate.Revision, candidate.UpdatedAt = newID(), clock.Load()
				edge := newRevisionEdge("monitor", m.ID, candidate.Revision, candidate, []string{m.Revision, "missing-parent"}, checkGeneration(candidate))
				err = n.applySnapshot(Snapshot{Versions: []Version{{Monitor: candidate, EdgeID: edge.ID, Ancestors: edge.Parents}}, RevisionEdges: []RevisionEdge{edge}})
			case "group":
				_, err = n.writeGroup(GroupInput{Name: "New group", NodeIDs: []string{"a", "b"}}, nil, "review-group", false)
			case "maintenance":
				storeMaintenance(t, n, plannedWork())
			}
			if err != nil {
				t.Fatal(err)
			}
			if change == "conflict" && len(n.heads[m.ID]) != 2 {
				t.Fatal("fixture did not create conflict")
			}
			if change == "unready" && n.current(m.ID) != nil {
				t.Fatal("fixture did not create unresolved proof")
			}
			clock.Add(53)
			out := archiveReviewRead(t, n, m.ID, base, clock.Load(), 37)
			if out.Summary.UpMs != 47 || out.Summary.UnknownMs != 53 {
				t.Fatal("configuration left an extendable head", change, out.Summary)
			}
			otherOut := archiveReviewRead(t, n, other.ID, base, clock.Load(), 37)
			want := int64(100)
			if change == "group" || change == "maintenance" {
				want = 47
			}
			if otherOut.Summary.UpMs != want {
				t.Fatal("global annotation closure", change, otherOut.Summary)
			}
			// Fresh peer registers cannot resume a local head, even on a ready revision.
			if current := n.current(m.ID); current != nil {
				peer := Sample{Observation: Observation{MonitorID: m.ID, NodeID: "b", CheckedAt: clock.Load(), State: "up"}, Revision: current.Revision, CheckGeneration: current.CheckGeneration, Sequence: 99}
				if err := n.applySnapshot(Snapshot{Observations: []Sample{peer}}); err != nil {
					t.Fatal(err)
				}
				if got := archiveReviewRead(t, n, m.ID, base, clock.Load(), 37); got.Summary.UpMs != 47 {
					t.Fatal("peer register created local history", got.Summary)
				}
			}
		})
	}
}

func TestAvailabilityStorageReviewFreshnessExclusiveDeadline(t *testing.T) {
	var clock atomic.Int64
	clock.Store(1000)
	n := archiveReviewNode(t, &clock, ":memory:", "")
	m := archiveReviewMonitor(t, n, "policy")
	for _, state := range []string{"up", "down", "pending", "unknown"} {
		for _, tc := range []struct{ interval, retry, timeout, sync, want int64 }{
			{1000, 2000, 10, 50, 3001}, {100, 170, 800, 50, 1601}, {100, 170, 70, 1000, 3001},
		} {
			m.IntervalMs, m.RetryIntervalMs, m.TimeoutMs, n.settings.SyncIntervalMs = tc.interval, Ptr(tc.retry), tc.timeout, tc.sync
			want := tc.want
			if tc.interval == 1000 && state != "up" {
				want = 6001
			}
			s := Sample{Observation: Observation{State: state, CheckedAt: 1000}}
			until, held := n.localAvailabilityPolicy(m, s)
			if until != 1000+want || held {
				t.Fatal("inclusive freshness was not converted exactly once", state, until, held)
			}
		}
	}
}

func TestAvailabilityStorageReviewAnnotationProofCompletionClosesAllHeads(t *testing.T) {
	for _, kind := range []string{"group", resourceMaintenance} {
		t.Run(kind, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(12, 1))
			n := archiveReviewNode(t, &clock, ":memory:", "")
			first := archiveReviewMonitor(t, n, "proof-first")
			second := archiveReviewMonitor(t, n, "proof-second")
			parent, revision := newID(), newID()
			var parentEdge RevisionEdge
			var snapshot Snapshot
			if kind == "group" {
				g := Group{ID: "pending-group", Revision: parent, UpdatedAt: clock.Load(), GroupInput: GroupInput{Name: "Pending group", NodeIDs: []string{"a", "b"}}}
				parentEdge = newRevisionEdge(kind, g.ID, parent, g, nil, "")
				g.Revision = revision
				e := newRevisionEdge(kind, g.ID, revision, g, []string{parent}, "")
				snapshot = Snapshot{GroupVersions: []GroupVersion{{Group: g, Ancestors: e.Parents, EdgeID: e.ID}}, RevisionEdges: []RevisionEdge{e}}
			} else {
				in := plannedWork()
				data, err := json.Marshal(in)
				if err != nil {
					t.Fatal(err)
				}
				prepared, err := n.prepareMaintenanceRequest(context.Background(), data)
				if err != nil {
					t.Fatal(err)
				}
				r := Resource{Kind: kind, ID: "pending-maintenance", Revision: parent, UpdatedAt: clock.Load(), Data: data}
				parentEdge = newRevisionEdge(kind, r.ID, parent, r, nil, "")
				r.Revision = revision
				e := newRevisionEdge(kind, r.ID, revision, r, []string{parent}, "")
				snapshot = Snapshot{Resources: []ResourceVersion{{Resource: r, Ancestors: e.Parents, EdgeID: e.ID}}, RevisionEdges: []RevisionEdge{e}, preparedMaintenance: prepared}
			}
			if err := n.applySnapshot(snapshot); err != nil {
				t.Fatal(err)
			}
			if kind == "group" && n.currentGroup("pending-group") != nil || kind == resourceMaintenance && n.currentResource(kind, "pending-maintenance") != nil {
				t.Fatal("fixture prematurely completed parent proof")
			}
			archiveReviewDrive(t, n)
			base := clock.Load()
			clock.Add(47)
			before := archiveReviewDump(t, n.store.db)
			// Failure in one head must roll back the proof and every head closure.
			if _, err := n.store.db.Exec(`CREATE TRIGGER archive_review_proof_failure BEFORE UPDATE ON availability_heads WHEN NEW.closed=1 AND NEW.monitor_id='proof-second' BEGIN SELECT RAISE(ABORT,'private proof fixture'); END`); err != nil {
				t.Fatal(err)
			}
			if err := n.applySnapshot(Snapshot{RevisionEdges: []RevisionEdge{parentEdge}}); err == nil {
				t.Fatal("proof committed despite failed archive closure")
			}
			if archiveReviewDump(t, n.store.db) != before {
				t.Fatal("annotation proof transaction partially committed")
			}
			if _, present := n.revisionEdges[parentEdge.ID]; present {
				t.Fatal("failed proof published graph")
			}
			if _, err := n.store.db.Exec(`DROP TRIGGER archive_review_proof_failure`); err != nil {
				t.Fatal(err)
			}
			if err := n.applySnapshot(Snapshot{RevisionEdges: []RevisionEdge{parentEdge}}); err != nil {
				t.Fatal(err)
			}
			clock.Add(53)
			for _, m := range []Monitor{first, second} {
				out := archiveReviewRead(t, n, m.ID, base, clock.Load(), 300000)
				if out.Summary.UpMs != 47 || out.Summary.UnknownMs != 53 || out.Summary.MaintenanceMs != 0 {
					t.Fatal("proof-only completion carried old evidence across annotation change", out.Summary)
				}
			}
		})
	}
}

func TestAvailabilityStorageReviewRetentionPointerAndReadCutoff(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "retention.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	var clock atomic.Int64
	clock.Store(1000)
	n := &Node{ID: "a", store: s, now: clock.Load}
	m := Monitor{ID: "crossing", Revision: "r", CheckGeneration: "g", MonitorInput: MonitorInput{Enabled: true, NodeIDs: []string{"a"}}}
	// A deliberately long supplied horizon tests retention's moving primary key
	// and pointer without waiting 90 days or modifying any production constant.
	record := func(at, end, seq int64) {
		sample := Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", CheckedAt: at, State: "up"}, Revision: m.Revision, CheckGeneration: m.CheckGeneration, Sequence: seq}
		archiveReviewWrite(t, n, func(e dbExec) error { return recordLocalAvailability(e, m, sample, end, true) })
	}
	record(100, availabilityRetentionMs+5000, 1)
	record(availabilityRetentionMs+1000, availabilityRetentionMs+6000, 2)
	var start, end, pointer int64
	if err := s.db.QueryRow(`SELECT start_ms,end_ms FROM availability_spans WHERE monitor_id=?`, m.ID).Scan(&start, &end); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT span_start FROM availability_heads WHERE monitor_id=?`, m.ID).Scan(&pointer); err != nil {
		t.Fatal(err)
	}
	if start != 1000 || pointer != start || end != availabilityRetentionMs+6000 {
		t.Fatal("crossing run pointer not repaired", start, end, pointer)
	}
	record(availabilityRetentionMs+2000, availabilityRetentionMs+7000, 3)
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*),MIN(start_ms),MAX(end_ms) FROM availability_spans WHERE monitor_id=?`, m.ID).Scan(&count, &start, &end); err != nil {
		t.Fatal(err)
	}
	if count != 1 || start != 2000 || end != availabilityRetentionMs+7000 {
		t.Fatal("retention damaged continuing run", count, start, end)
	}
	clock.Store(availabilityRetentionMs + 3000)
	out := archiveReviewRead(t, n, m.ID, 0, 5000, 333)
	if out.Summary.UpMs != 2000 || out.Summary.UnknownMs != 3000 || out.Summary.MaintenanceMs != 2000 || out.RetainedFrom == nil || *out.RetainedFrom != 3000 {
		t.Fatal("lazy idle read cutoff leaked expired coverage", out)
	}
	archiveReviewWrite(t, n, func(e dbExec) error { return closeLocalAvailability(e, m.ID, availabilityRetentionMs+4000) })
	// Read at an earlier clock after durable pruning cannot resurrect the cut.
	clock.Store(availabilityRetentionMs + 2500)
	out = archiveReviewRead(t, n, m.ID, 0, 5000, 333)
	if out.Summary.UpMs != 1000 || out.Summary.UnknownMs != 4000 || out.RetainedFrom == nil || *out.RetainedFrom != 4000 {
		t.Fatal("backward clock resurrected pruned cells", out)
	}
}

func TestAvailabilityStorageReviewMalformedRawQueryRejected(t *testing.T) {
	var clock atomic.Int64
	clock.Store(10000)
	n := archiveReviewNode(t, &clock, ":memory:", "")
	m := archiveReviewMonitor(t, n, "raw-query")
	for _, raw := range []string{
		"from=%zz", "from=10;to=20", "stepMs=%zz", "from=0&to=10000&stepMs=100&from=%zz", "from=0&to=10000&stepMs=100&unexpected=%zz",
	} {
		t.Run(raw, func(t *testing.T) {
			w := archiveReviewRequest(n, m.ID, raw, adminToken)
			if w.Code != 400 {
				t.Fatalf("malformed raw availability query must be 400, got %d", w.Code)
			}
		})
	}
}

func TestAvailabilityStorageReviewBackupRestoreClosesHead(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli() - 1000)
	dir := t.TempDir()
	source, identity := filepath.Join(dir, "source.db"), filepath.Join(dir, "identity")
	n := archiveReviewNode(t, &clock, source, identity)
	n.settings.SyncIntervalMs = 60000
	m := archiveReviewMonitor(t, n, "restore-local")
	archiveReviewDrive(t, n)
	base := clock.Load()
	original := n.samples[m.ID+"/a"]
	var beforeHead, beforeSpans string
	if err := n.store.db.QueryRow(`SELECT json_object('hi',high_water,'at',checked_at,'end',valid_until,'rev',revision,'gen',generation,'seq',sequence,'closed',closed,'start',span_start) FROM availability_heads WHERE monitor_id=?`, m.ID).Scan(&beforeHead); err != nil {
		t.Fatal(err)
	}
	if err := n.store.db.QueryRow(`SELECT json_group_array(json_array(monitor_id,start_ms,end_ms,state,maintenance)) FROM availability_spans`).Scan(&beforeSpans); err != nil {
		t.Fatal(err)
	}
	backup, restored := filepath.Join(dir, "backup.db"), filepath.Join(dir, "restored.db")
	if _, err := BackupDatabase(context.Background(), source, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareDatabaseRestore(context.Background(), backup, restored); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{backup, restored} {
		db, err := sql.Open("sqlite", sqliteFileURI(path)+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		var head, spans string
		err = db.QueryRow(`SELECT json_object('hi',high_water,'at',checked_at,'end',valid_until,'rev',revision,'gen',generation,'seq',sequence,'closed',closed,'start',span_start) FROM availability_heads WHERE monitor_id=?`, m.ID).Scan(&head)
		if err == nil {
			err = db.QueryRow(`SELECT json_group_array(json_array(monitor_id,start_ms,end_ms,state,maintenance)) FROM availability_spans`).Scan(&spans)
		}
		db.Close()
		if err != nil || head != beforeHead || spans != beforeSpans {
			t.Fatal("whole-SQLite copy changed archive", err, path)
		}
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	if unsafe, err := New(Options{DBPath: restored, ClusterToken: clusterToken}); err == nil {
		unsafe.Stop()
		t.Fatal("quarantined restore started")
	}
	activation, err := ActivateRestoredDatabase(context.Background(), restored, identity)
	if err != nil {
		t.Fatal(err)
	}
	clock.Store(activation.ActivatedAt + 1000)
	n = archiveReviewNode(t, &clock, restored, identity)
	var hi, checked, until, sequence int64
	var closed int
	var rev, gen string
	if err := n.store.db.QueryRow(`SELECT high_water,checked_at,valid_until,sequence,closed,revision,generation FROM availability_heads WHERE monitor_id=?`, m.ID).
		Scan(&hi, &checked, &until, &sequence, &closed, &rev, &gen); err != nil {
		t.Fatal(err)
	}
	if closed != 1 || checked != base || until != base+180001 || sequence != original.Sequence || rev != original.Revision || gen != original.CheckGeneration || hi < base || hi > activation.ActivatedAt {
		t.Fatal("activation lost original provenance or failed to close head", hi, checked, until, sequence, closed, rev, gen)
	}
	out := archiveReviewRead(t, n, m.ID, base, clock.Load(), 300000)
	if out.Summary.UpMs != hi-base || out.Summary.UnknownMs != clock.Load()-hi {
		t.Fatal("restore reset projected coverage", out.Summary)
	}
	current := n.current(m.ID)
	if current == nil || current.Revision == original.Revision || checkGeneration(*current) == sampleGeneration(original) {
		t.Fatal("activation did not reset monitor generation")
	}
	archiveReviewDrive(t, n)
	if n.samples[m.ID+"/a"].Sequence <= activation.SequenceFloor {
		t.Fatal("restored sample reused origin sequence")
	}
	clock.Add(100)
	resumed := archiveReviewRead(t, n, m.ID, hi, clock.Load(), 300000)
	if resumed.Summary.UpMs != 100 || resumed.Summary.UnknownMs != activation.ActivatedAt+1000-hi {
		t.Fatal("fresh restore sample backfilled missing time", resumed.Summary)
	}
}

func TestAvailabilityStorageReviewRuntimeBackwardClockAcrossRestart(t *testing.T) {
	var clock atomic.Int64
	clock.Store(10000)
	dir := t.TempDir()
	path, identity := filepath.Join(dir, "clock.db"), filepath.Join(dir, "identity")
	n := archiveReviewNode(t, &clock, path, identity)
	m := archiveReviewMonitor(t, n, "clock-local")
	archiveReviewDrive(t, n)
	clock.Store(11000)
	archiveReviewDrive(t, n)
	clock.Store(10800)
	archiveReviewDrive(t, n)
	var high, checked, until int64
	if err := n.store.db.QueryRow(`SELECT high_water,checked_at,valid_until FROM availability_heads WHERE monitor_id=?`, m.ID).Scan(&high, &checked, &until); err != nil {
		t.Fatal(err)
	}
	if high != 11000 || checked != 10800 || until != 13801 {
		t.Fatal("runtime moved original deadline with clock clamp", high, checked, until)
	}
	clock.Store(10500)
	in := m.MonitorInput
	in.Name = "Clock closure"
	if _, err := n.writeMonitor(in, n.heads[m.ID], m.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	clock.Store(5000)
	n = archiveReviewNode(t, &clock, path, identity)
	archiveReviewDrive(t, n)
	if err := n.store.db.QueryRow(`SELECT high_water,checked_at,valid_until FROM availability_heads WHERE monitor_id=?`, m.ID).Scan(&high, &checked, &until); err != nil {
		t.Fatal(err)
	}
	if high != 11000 || checked != 5000 || until != 8001 {
		t.Fatal("restart lost durable watermark or shifted expired original deadline", high, checked, until)
	}
	var count int
	var start, end int64
	if err := n.store.db.QueryRow(`SELECT COUNT(*),MIN(start_ms),MAX(end_ms) FROM availability_spans WHERE monitor_id=?`, m.ID).Scan(&count, &start, &end); err != nil {
		t.Fatal(err)
	}
	if count != 1 || start != 10000 || end != 11000 {
		t.Fatal("expired backward sample rewrote known history", count, start, end)
	}
	clock.Store(5100)
	if out := archiveReviewRead(t, n, m.ID, 5000, 5100, 100); out.Summary.KnownMs != 0 || out.Summary.UnknownMs != 100 || out.RetainedFrom != nil {
		t.Fatal("read exposed future head after backward restart", out)
	}
	clock.Store(12000)
	archiveReviewDrive(t, n)
	clock.Store(12100)
	out := archiveReviewRead(t, n, m.ID, 10000, 12100, 157)
	if out.Summary.UpMs != 1100 || out.Summary.UnknownMs != 1000 {
		t.Fatal("clock catch-up sample filled closure gap", out.Summary)
	}
}
