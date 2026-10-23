package canary

import (
	"net/url"
	"path/filepath"
	"testing"
)

// This deliberately uses a file-backed SQLite database at the documented
// per-monitor span limit. Setup is outside the timed section. Measurements
// describe this workload and machine, not a request-latency guarantee.
func benchmarkAvailabilityStore(b *testing.B) *Node {
	b.Helper()
	s, err := openStore(filepath.Join(b.TempDir(), "archive.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := s.close(); err != nil {
			b.Error(err)
		}
	})
	n := &Node{ID: "a", store: s, now: func() int64 { return 1000000 }}
	m := availabilityMonitor()
	tx, err := s.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	if err = recordLocalAvailability(tx, m, availabilitySample(m, 0, 1, "up"), 1, false); err != nil {
		b.Fatal(err)
	}
	_, err = tx.Exec(`WITH RECURSIVE numbers(x) AS
 (VALUES(1) UNION ALL SELECT x+1 FROM numbers WHERE x<99999)
 INSERT INTO availability_spans
 SELECT 'monitor',x*2,x*2+1,CASE WHEN x%2=0 THEN 'up' ELSE 'down' END,0 FROM numbers`)
	if err != nil {
		b.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		b.Fatal(err)
	}
	return n
}

func BenchmarkAvailabilityHistoryRetainedLimit(b *testing.B) {
	for _, sample := range []struct {
		name  string
		from  string
		known int64
	}{
		{"Full100000Spans", "0", 100000},
		{"Recent100Spans", "199800", 100},
	} {
		b.Run(sample.name, func(b *testing.B) {
			n := benchmarkAvailabilityStore(b)
			query := url.Values{"from": {sample.from}, "to": {"200000"}, "stepMs": {"100"}}
			b.ReportAllocs()
			b.ResetTimer()
			var result any
			var err error
			for i := 0; i < b.N; i++ {
				result, err = n.availabilityHistory("monitor", query)
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if got := result.(availabilityHistoryResult).Summary.KnownMs; got != sample.known {
				b.Fatalf("known duration: got %d, want %d", got, sample.known)
			}
		})
	}
}

func BenchmarkAvailabilityAppendAtRetainedLimit(b *testing.B) {
	n := benchmarkAvailabilityStore(b)
	m := availabilityMonitor()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		at := int64(200000 + i*2)
		tx, err := n.store.db.Begin()
		if err != nil {
			b.Fatal(err)
		}
		err = recordLocalAvailability(tx, m, availabilitySample(m, at, int64(i+2), "down"), at+1, false)
		if err != nil {
			tx.Rollback()
			b.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	var count int
	if err := n.store.db.QueryRow(`SELECT COUNT(*) FROM availability_spans WHERE monitor_id=?`, m.ID).Scan(&count); err != nil {
		b.Fatal(err)
	}
	if count != availabilityRecordLimit {
		b.Fatalf("retained %d spans, want %d", count, availabilityRecordLimit)
	}
}
