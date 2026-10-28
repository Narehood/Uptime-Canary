package canary

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"uptime-canary/apps/node-go/internal/availability"
)

func availabilityFixture(t *testing.T, path string, now int64) *Node {
	t.Helper()
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.close() })
	if err = initializeAvailabilityStore(s.db); err != nil {
		t.Fatal(err)
	}
	return &Node{ID: "a", store: s, now: func() int64 { return now }}
}

func availabilityMonitor() Monitor {
	return Monitor{ID: "monitor", Revision: "r1", MonitorInput: MonitorInput{Enabled: true, NodeIDs: []string{"a"}}}
}

func availabilitySample(m Monitor, at, seq int64, state string) Sample {
	return Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", CheckedAt: at, State: state},
		Revision: m.Revision, CheckGeneration: m.CheckGeneration, Sequence: seq}
}

func availabilityWrite(t *testing.T, n *Node, f func(dbExec) error) {
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

func availabilityRecord(t *testing.T, n *Node, m Monitor, at, end, seq int64, state string, maintenance bool) {
	t.Helper()
	availabilityWrite(t, n, func(e dbExec) error {
		return recordLocalAvailability(e, m, availabilitySample(m, at, seq, state), end, maintenance)
	})
}

func availabilityRead(t *testing.T, n *Node, id string, from, to, step int64) availabilityHistoryResult {
	t.Helper()
	v, err := n.availabilityHistory(id, url.Values{"from": {strconv.FormatInt(from, 10)}, "to": {strconv.FormatInt(to, 10)}, "stepMs": {strconv.FormatInt(step, 10)}})
	if err != nil {
		t.Fatal(err)
	}
	return v.(availabilityHistoryResult)
}

func availabilityStoredSpans(t *testing.T, n *Node, id string) []availability.Span {
	t.Helper()
	rows, err := n.store.db.Query(`SELECT start_ms,end_ms,state,maintenance FROM availability_spans WHERE monitor_id=? ORDER BY start_ms`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []availability.Span
	for rows.Next() {
		var span availability.Span
		if err = rows.Scan(&span.StartMs, &span.EndMs, &span.State, &span.Maintenance); err != nil {
			t.Fatal(err)
		}
		out = append(out, span)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAvailabilityHistoryExactRunsGapsStatesAndClipping(t *testing.T) {
	n := availabilityFixture(t, ":memory:", 1000)
	m := availabilityMonitor()
	availabilityRecord(t, n, m, 100, 180, 1, "up", false)
	availabilityRecord(t, n, m, 150, 220, 2, "up", false)
	availabilityRecord(t, n, m, 200, 260, 3, "down", true)
	availabilityRecord(t, n, m, 230, 250, 4, "pending", true)
	availabilityRecord(t, n, m, 270, 300, 5, "unknown", true)
	availabilityRecord(t, n, m, 300, 320, 6, "up", false)
	want := []availability.Span{
		{StartMs: 100, EndMs: 200, State: "up"},
		{StartMs: 200, EndMs: 230, State: "down", Maintenance: true},
		{StartMs: 230, EndMs: 250, State: "pending", Maintenance: true},
		{StartMs: 270, EndMs: 300, State: "unknown", Maintenance: true},
		{StartMs: 300, EndMs: 320, State: "up"},
	}
	if got := availabilityStoredSpans(t, n, m.ID); !reflect.DeepEqual(got, want) {
		t.Fatalf("runs: got %+v, want %+v", got, want)
	}
	out := availabilityRead(t, n, m.ID, 125, 310, 50)
	s := out.Summary
	if s.UpMs != 85 || s.DownMs != 30 || s.PendingMs != 20 || s.UnknownMs != 50 || s.KnownMs != 115 || s.MaintenanceMs != 80 {
		t.Fatalf("clipped durations: %+v", s)
	}
	if s.AvailabilityPercent == nil || *s.AvailabilityPercent != 100*(float64(85)/115) || s.CoveragePercent != 100*(float64(115)/185) {
		t.Fatalf("percentages: %+v", s)
	}
	var up, down, pending, unknown, maintenance int64
	for _, b := range out.Buckets {
		up += b.UpMs
		down += b.DownMs
		pending += b.PendingMs
		unknown += b.UnknownMs
		maintenance += b.MaintenanceMs
	}
	if up != 85 || down != 30 || pending != 20 || unknown != 50 || maintenance != 80 || out.Buckets[0].StartMs != 125 || out.Buckets[len(out.Buckets)-1].EndMs != 310 {
		t.Fatalf("bucket accounting: %+v", out.Buckets)
	}
	if out.RetainedFrom == nil || *out.RetainedFrom != 100 || out.RecordLimit != 100000 || out.RetentionMs != 7776000000 {
		t.Fatalf("retention metadata: %+v", out)
	}
}

func TestAvailabilityHistoryMaintenanceChangesBreakRunsWithoutExclusion(t *testing.T) {
	n := availabilityFixture(t, ":memory:", 1000)
	m := availabilityMonitor()
	availabilityRecord(t, n, m, 100, 200, 1, "up", false)
	availabilityRecord(t, n, m, 150, 250, 2, "up", true)
	availabilityRecord(t, n, m, 250, 300, 3, "up", false)
	availabilityRecord(t, n, m, 350, 400, 4, "up", false)
	if got := availabilityStoredSpans(t, n, m.ID); len(got) != 4 {
		t.Fatalf("annotation transition or gap coalesced: %+v", got)
	}
	s := availabilityRead(t, n, m.ID, 100, 400, 100).Summary
	if s.UpMs != 250 || s.UnknownMs != 50 || s.MaintenanceMs != 100 || s.KnownMs != 250 || s.AvailabilityPercent == nil || *s.AvailabilityPercent != 100 {
		t.Fatal(s)
	}
}

func TestAvailabilityHistoryOriginalDeadlineAndRevisionClosure(t *testing.T) {
	n := availabilityFixture(t, ":memory:", 1000)
	m := availabilityMonitor()
	availabilityRecord(t, n, m, 100, 500, 1, "up", false)
	availabilityWrite(t, n, func(e dbExec) error { return closeLocalAvailability(e, m.ID, 200) })
	// Root's actual transition wiring owns selecting the closure;
	// exercise the direct hook rather than mirroring that selection.
	m.IntervalMs, m.TimeoutMs = 999999, 999999
	m.Revision, m.CheckGeneration = "r2", "g2"
	availabilityRecord(t, n, m, 300, 340, 2, "up", false)
	got := availabilityStoredSpans(t, n, m.ID)
	if len(got) != 2 || got[0].EndMs != 200 || got[1].StartMs != 300 || got[1].EndMs != 340 {
		t.Fatalf("closure/gap/deadline: %+v", got)
	}
	var checked, deadline, sequence int64
	var revision, generation string
	if err := n.store.db.QueryRow(`SELECT checked_at,valid_until,revision,generation,sequence FROM availability_heads WHERE monitor_id=?`, m.ID).
		Scan(&checked, &deadline, &revision, &generation, &sequence); err != nil {
		t.Fatal(err)
	}
	if checked != 300 || deadline != 340 || revision != "r2" || generation != "g2" || sequence != 2 {
		t.Fatal(checked, deadline, revision, generation, sequence)
	}
}

func TestAvailabilityHistoryRollbackClockClosureAndReplayAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "availability.db")
	n := availabilityFixture(t, path, 1000)
	m := availabilityMonitor()
	availabilityRecord(t, n, m, 100, 500, 1, "up", false)
	availabilityRecord(t, n, m, 200, 500, 2, "up", false)
	// The backward-clock down event starts at 200, retaining [100,200)
	// exactly. Its deadline remains 240, not 200+(240-150).
	availabilityRecord(t, n, m, 150, 240, 3, "down", false)
	got := availabilityStoredSpans(t, n, m.ID)
	if len(got) != 2 || got[0].EndMs != 200 || got[1].StartMs != 200 || got[1].EndMs != 240 {
		t.Fatal(got)
	}
	availabilityWrite(t, n, func(e dbExec) error { return closeLocalAvailability(e, m.ID, 180) })
	availabilityWrite(t, n, func(e dbExec) error { return closeLocalAvailability(e, m.ID, 200) })
	availabilityRecord(t, n, m, 150, 500, 3, "down", false) // replay cannot reopen
	availabilityRecord(t, n, m, 140, 190, 4, "up", false)   // entire deadline behind high-water
	availabilityWrite(t, n, func(e dbExec) error { return closeLocalAvailability(e, m.ID, 300) })
	if err := n.store.close(); err != nil {
		t.Fatal(err)
	}
	n = availabilityFixture(t, path, 1000)
	availabilityRecord(t, n, m, 250, 350, 5, "up", false)
	got = availabilityStoredSpans(t, n, m.ID)
	if len(got) != 2 || got[0].StartMs != 100 || got[0].EndMs != 200 || got[1].StartMs != 300 || got[1].EndMs != 350 {
		t.Fatalf("durable high-water or closure lost: %+v", got)
	}
	var highWater, checked, deadline int64
	if err := n.store.db.QueryRow(`SELECT high_water,checked_at,valid_until FROM availability_heads WHERE monitor_id=?`, m.ID).Scan(&highWater, &checked, &deadline); err != nil {
		t.Fatal(err)
	}
	if highWater != 300 || checked != 250 || deadline != 350 {
		t.Fatal(highWater, checked, deadline)
	}
	// Queries are capped at the CURRENT clock even if a durable future head exists.
	n.now = func() int64 { return 325 }
	if s := availabilityRead(t, n, m.ID, 100, 325, 100).Summary; s.UpMs != 125 || s.UnknownMs != 100 {
		t.Fatal(s)
	}
}

func TestAvailabilityHistorySameClockAndNeverObservedClosure(t *testing.T) {
	n := availabilityFixture(t, ":memory:", 1000)
	availabilityWrite(t, n, func(e dbExec) error { return closeLocalAvailability(e, "never-observed", 300) })
	var count int
	if err := n.store.db.QueryRow(`SELECT COUNT(*) FROM availability_heads`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	m := availabilityMonitor()
	availabilityRecord(t, n, m, 100, 200, 1, "up", false)
	availabilityRecord(t, n, m, 100, 200, 2, "down", false)
	availabilityWrite(t, n, func(e dbExec) error { return closeLocalAvailability(e, m.ID, 100) })
	availabilityWrite(t, n, func(e dbExec) error { return closeLocalAvailability(e, m.ID, 100) })
	availabilityRecord(t, n, m, 100, 200, 2, "down", false)
	if got := availabilityStoredSpans(t, n, m.ID); len(got) != 0 {
		t.Fatalf("zero-duration or invalidated head reopened: %+v", got)
	}
	var highWater int64
	var closed int
	if err := n.store.db.QueryRow(`SELECT high_water,closed FROM availability_heads WHERE monitor_id=?`, m.ID).Scan(&highWater, &closed); err != nil || highWater != 100 || closed != 1 {
		t.Fatal(highWater, closed, err)
	}
}

func TestAvailabilityHistoryIndependentOfRawPruningAndOldDatabase(t *testing.T) {
	n := availabilityFixture(t, filepath.Join(t.TempDir(), "old.db"), 1000)
	if _, err := n.store.db.Exec(`DROP TABLE availability_spans; DROP TABLE availability_heads`); err != nil {
		t.Fatal(err)
	}
	m := availabilityMonitor()
	old := availabilitySample(m, 10, 1, "up")
	availabilityWrite(t, n, func(e dbExec) error {
		if err := put(e, "samples", m.ID+"/a", old); err != nil {
			return err
		}
		return appendHistory(e, old.Observation, 1000, 0)
	})
	// Schema initialization/reinitialization never infers coverage from old data.
	if err := initializeAvailabilityStore(n.store.db); err != nil {
		t.Fatal(err)
	}
	s := availabilityRead(t, n, m.ID, 0, 100, 10)
	if s.Summary.UnknownMs != 100 || s.Summary.KnownMs != 0 || s.Summary.AvailabilityPercent != nil || s.RetainedFrom != nil {
		t.Fatal(s)
	}
	for i := int64(1); i <= 5; i++ {
		observation := availabilitySample(m, 100+i*10, i+1, "up")
		availabilityWrite(t, n, func(e dbExec) error {
			if err := recordLocalAvailability(e, m, observation, observation.CheckedAt+15, false); err != nil {
				return err
			}
			return appendHistory(e, observation.Observation, 2, 0)
		})
	}
	if history, err := n.store.history(m.ID, 1000); err != nil || len(history) != 2 {
		t.Fatal(history, err)
	}
	if got := availabilityStoredSpans(t, n, m.ID); len(got) != 1 || got[0].StartMs != 110 || got[0].EndMs != 165 {
		t.Fatal(got)
	}
	if _, err := n.store.db.Exec(`DELETE FROM history; DELETE FROM records WHERE category='samples'`); err != nil {
		t.Fatal(err)
	}
	if s := availabilityRead(t, n, m.ID, 100, 200, 50).Summary; s.UpMs != 55 || s.UnknownMs != 45 {
		t.Fatal(s)
	}
}

func TestAvailabilityHistoryRetentionClipsAndIdleQueryExpires(t *testing.T) {
	base := availabilityRetentionMs + 1000
	n := availabilityFixture(t, ":memory:", base+200)
	m := availabilityMonitor()
	availabilityRecord(t, n, m, 0, 2000, 1, "up", true)
	other := m
	other.ID = "other"
	availabilityRecord(t, n, other, 0, 2000, 1, "down", false)
	availabilityRecord(t, n, m, base, base+100, 2, "down", false)
	got := availabilityStoredSpans(t, n, m.ID)
	if len(got) != 2 || got[0].StartMs != 1000 || got[0].EndMs != 2000 {
		t.Fatalf("retention must clip crossing coverage: %+v", got)
	}
	if got := availabilityStoredSpans(t, n, other.ID); len(got) != 1 || got[0].StartMs != 0 {
		t.Fatalf("pruned another monitor: %+v", got)
	}
	out := availabilityRead(t, n, m.ID, 200, 2200, 1000)
	if out.Summary.UpMs != 800 || out.Summary.UnknownMs != 1200 || out.RetainedFrom == nil || *out.RetainedFrom != 1200 {
		t.Fatal(out)
	}
	n.now = func() int64 { return base + availabilityRetentionMs + 100 }
	out = availabilityRead(t, n, m.ID, base, base+200, 100)
	if out.Summary.KnownMs != 0 || out.Summary.UnknownMs != 200 || out.RetainedFrom != nil {
		t.Fatalf("idle expiry must not expose old coverage: %+v", out)
	}
}

func TestAvailabilityHistoryRecordLimitKeepsNewestAndReportsUnknown(t *testing.T) {
	n := availabilityFixture(t, ":memory:", 300000)
	m := availabilityMonitor()
	availabilityRecord(t, n, m, 0, 1, 0, "up", false)
	// Bulk private fixture simulates 100000 disjoint observations without
	// making this resource-bound test execute 100000 sample transactions.
	if _, err := n.store.db.Exec(`WITH RECURSIVE numbers(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM numbers WHERE x<99999)
 INSERT INTO availability_spans SELECT 'monitor',x*2,x*2+1,CASE WHEN x%2=0 THEN 'up' ELSE 'down' END,0 FROM numbers`); err != nil {
		t.Fatal(err)
	}
	availabilityRecord(t, n, m, 200000, 200001, 100000, "down", false)
	var count, first int64
	if err := n.store.db.QueryRow(`SELECT COUNT(*),MIN(start_ms) FROM availability_spans WHERE monitor_id=?`, m.ID).Scan(&count, &first); err != nil || count != 100000 || first != 2 {
		t.Fatal(count, first, err)
	}
	out := availabilityRead(t, n, m.ID, 0, 200002, 100)
	if out.Summary.KnownMs != 100000 || out.Summary.UnknownMs != 100002 || out.RetainedFrom == nil || *out.RetainedFrom != 2 {
		t.Fatalf("record pruning fabricated coverage: %+v", out.Summary)
	}
	if _, err := n.store.db.Exec(`INSERT INTO availability_spans VALUES('monitor',200001,200002,'up',0)`); err != nil {
		t.Fatal(err)
	}
	_, err := n.availabilityHistory(m.ID, url.Values{"from": {"0"}, "to": {"200002"}, "stepMs": {"100"}})
	var api *apiError
	if !errors.As(err, &api) || api.Status != 503 {
		t.Fatalf("oversized stored result must fail before over-allocation: %v", err)
	}
}

type availabilityFailExec struct {
	e        dbExec
	contains string
	hit      bool
}

func (f *availabilityFailExec) Exec(query string, args ...any) (sql.Result, error) {
	if strings.Contains(query, f.contains) {
		f.hit = true
		return nil, errors.New("private injected database diagnostic")
	}
	return f.e.Exec(query, args...)
}

func availabilitySnapshot(t *testing.T, n *Node) string {
	t.Helper()
	var tables []json.RawMessage
	for _, query := range []string{
		`SELECT json_group_array(json_object('id',monitor_id,'hi',high_water,'at',checked_at,'end',valid_until,'rev',revision,'gen',generation,'seq',sequence,'state',state,'maintenance',maintenance,'closed',closed,'start',span_start)) FROM availability_heads`,
		`SELECT json_group_array(json_object('id',monitor_id,'start',start_ms,'end',end_ms,'state',state,'maintenance',maintenance)) FROM availability_spans ORDER BY monitor_id,start_ms`,
		`SELECT json_group_array(json_object('category',category,'id',id,'payload',payload)) FROM records ORDER BY category,id`,
		`SELECT json_group_array(json_object('id',id,'monitor',monitor_id,'at',checked_at,'payload',payload)) FROM history ORDER BY id`,
	} {
		var value string
		if err := n.store.db.QueryRow(query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, json.RawMessage(value))
	}
	b, err := json.Marshal(tables)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestAvailabilityHistoryCoordinatedTransactionRollback(t *testing.T) {
	for _, closure := range []bool{false, true} {
		phases := []string{"UPDATE availability_spans SET end_ms", "DELETE FROM availability_spans WHERE monitor_id=?\n AND start_ms=", "UPDATE availability_heads SET", "AND end_ms<=", "UPDATE availability_spans SET start_ms", "AND start_ms<COALESCE", "SET span_start=\n (SELECT"}
		if !closure {
			phases = append(phases, "INSERT INTO availability_spans")
		}
		for _, phase := range phases {
			t.Run(strconv.FormatBool(closure)+"/"+phase, func(t *testing.T) {
				n := availabilityFixture(t, ":memory:", availabilityRetentionMs+500)
				m := availabilityMonitor()
				availabilityRecord(t, n, m, 0, 300, 1, "up", false)
				before := availabilitySnapshot(t, n)
				tx, err := n.store.db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				// These are the same transaction participants root coordinates.
				s := availabilitySample(m, availabilityRetentionMs+100, 2, "down")
				for _, item := range []struct {
					category, key string
					value         any
				}{
					{"samples", m.ID + "/a", s}, {"probePolicies", m.ID, map[string]int{"failures": 1}}, {"settings", "sequence", int64(2)},
				} {
					if err := put(tx, item.category, item.key, item.value); err != nil {
						t.Fatal(err)
					}
				}
				if err := appendHistory(tx, s.Observation, 1, 0); err != nil {
					t.Fatal(err)
				}
				inject := &availabilityFailExec{e: tx, contains: phase}
				if closure {
					err = closeLocalAvailability(inject, m.ID, s.CheckedAt)
				} else {
					err = recordLocalAvailability(inject, m, s, s.CheckedAt+100, false)
				}
				if err == nil || !inject.hit {
					t.Fatalf("failure phase not reached: %v, hit %v", err, inject.hit)
				}
				if err = tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				if after := availabilitySnapshot(t, n); after != before {
					t.Fatalf("partial transaction survived\nbefore: %s\nafter: %s", before, after)
				}
			})
		}
	}
}

func TestAvailabilityHistoryQueryValidationDefaultsAndErrors(t *testing.T) {
	n := availabilityFixture(t, ":memory:", 200000000)
	for _, raw := range []string{
		"from=1&from=2", "to=3&to=3", "stepMs=1&stepMs=1", "limit=1", "from=", "from=-1", "from=+1", "from=1.0", "from=1e2", "from=%201", "from=9007199254740992", "from=999999999999999999999999", "from=1&to=1", "from=2&to=1", "to=200000001", "from=0&to=1&stepMs=0", "from=0&to=1&stepMs=31536000001", "from=0&to=4097&stepMs=1", "from=1&to=40961&stepMs=10", "from=0&to=7776000001&stepMs=31536000000",
	} {
		t.Run(raw, func(t *testing.T) {
			query, err := url.ParseQuery(raw)
			if err != nil {
				t.Fatal(err)
			}
			_, err = n.availabilityHistory("absent", query)
			var api *apiError
			if !errors.As(err, &api) || api.Status != 400 {
				t.Fatalf("expected 400, got %v", err)
			}
		})
	}
	v, err := n.availabilityHistory("absent", nil)
	if err != nil {
		t.Fatal(err)
	}
	out := v.(availabilityHistoryResult)
	if out.From != 200000000-86400000 || out.To != 200000000 || out.StepMs != 300000 || out.Summary.UnknownMs != 86400000 || out.Summary.AvailabilityPercent != nil {
		t.Fatal(out)
	}
	encoded, err := json.Marshal(out)
	if err != nil || !strings.Contains(string(encoded), `"availabilityPercent":null`) || !strings.Contains(string(encoded), `"retainedFrom":null`) || strings.Contains(string(encoded), "latency") {
		t.Fatal(string(encoded), err)
	}
	if _, err = n.availabilityHistory("absent", url.Values{"from": {"0"}, "to": {"4096"}, "stepMs": {"1"}}); err != nil {
		t.Fatal("4096 buckets rejected", err)
	}
	// Any storage diagnostic must be replaced by the stable API error.
	if _, err = n.store.db.Exec(`DROP TABLE availability_spans`); err != nil {
		t.Fatal(err)
	}
	_, err = n.availabilityHistory("absent", nil)
	var api *apiError
	if !errors.As(err, &api) || api.Status != 503 || api.Message != "Availability history is unavailable" {
		t.Fatal(err)
	}
	if _, err = n.store.db.Exec(`CREATE TABLE availability_spans(private TEXT)`); err != nil {
		t.Fatal(err)
	}
	// A closed database also exercises sanitized startup-schema errors.
	if err = n.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = initializeAvailabilityStore(n.store.db); err == nil || err.Error() != "Cannot initialize availability history" {
		t.Fatal(err)
	}
}

func TestAvailabilityHistoryNewSchemaAndAuthenticatedRoute(t *testing.T) {
	n, err := New(Options{NodeID: "a", DBPath: filepath.Join(t.TempDir(), "new.db"),
		AdminToken: adminToken, ClusterToken: clusterToken, Now: func() int64 { return 1000 }})
	if err != nil {
		t.Fatal(err)
	}
	// No Start, listening server, scheduler, probes or live delivery is needed.
	t.Cleanup(func() { _ = n.Stop() })
	m, err := n.writeMonitor(MonitorInput{Name: "Observed history", Type: "manual", InternalOnly: true, IntervalMs: 100, TimeoutMs: 70,
		Enabled: true, NodeIDs: []string{"a"}, Manual: &ManualMonitorOptions{State: "up"}}, nil, "local-history", false)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err = n.store.db.QueryRow(`SELECT COUNT(*) FROM availability_heads`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("New must initialize schema without manufacturing observed heads: %d %v", count, err)
	}
	availabilityRecord(t, n, m, 100, 501, 1, "up", false)
	path := "/api/monitors/" + m.ID + "/availability?from=0&to=1000&stepMs=100"
	request := func(path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		n.ServeHTTP(response, req)
		return response
	}
	for _, token := range []string{"", clusterToken} {
		if got := request(path, token); got.Code != 401 {
			t.Fatal(got.Code, got.Body.String())
		}
	}
	response := request(path, adminToken)
	var out availabilityHistoryResult
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &out) != nil || out.Summary.UpMs != 401 || out.Summary.UnknownMs != 599 || out.NodeID != "a" {
		t.Fatal(response.Code, response.Body.String())
	}
	if got := request(path+"&stepMs=100", adminToken); got.Code != 400 {
		t.Fatal(got.Code, got.Body.String())
	}
	if _, err = n.store.db.Exec(`DROP TABLE availability_spans`); err != nil {
		t.Fatal(err)
	}
	if got := request(path, adminToken); got.Code != 503 || strings.Contains(got.Body.String(), "SQL") || strings.Contains(got.Body.String(), "new.db") {
		t.Fatal(got.Code, got.Body.String())
	}
}
