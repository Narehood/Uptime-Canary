package availability_test

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"uptime-canary/apps/node-go/internal/availability"
)

func TestObservedTimeline(t *testing.T) {
	spans := []availability.Span{
		{StartMs: 0, EndMs: 10, State: availability.StateUp, Maintenance: true},
		{StartMs: 10, EndMs: 20, State: availability.StateDown},
		{StartMs: 25, EndMs: 35, State: availability.StatePending, Maintenance: true},
		{StartMs: 35, EndMs: 40, State: availability.StateUnknown, Maintenance: true},
		{StartMs: 45, EndMs: 60, State: availability.StateUp},
		{StartMs: 70, EndMs: 80, State: availability.StateDown, Maintenance: true},
	}
	percent := 60.0
	want := availability.Totals{
		StartMs: 5, EndMs: 55, UpMs: 15, DownMs: 10, PendingMs: 10,
		UnknownMs: 15, MaintenanceMs: 20, KnownMs: 25,
		AvailabilityPercent: &percent, CoveragePercent: 50,
	}
	got, err := availability.Summarize(5, 55, spans)
	if err != nil {
		t.Fatal(err)
	}
	assertTotals(t, got, want)
	buckets, err := availability.Buckets(5, 55, 10, spans)
	if err != nil {
		t.Fatal(err)
	}
	wantEdges := [][2]int64{{5, 10}, {10, 20}, {20, 30}, {30, 40}, {40, 50}, {50, 55}}
	if len(buckets) != len(wantEdges) {
		t.Fatalf("got %d buckets, want %d", len(buckets), len(wantEdges))
	}
	for i, edge := range wantEdges {
		assertTotals(t, buckets[i].Totals, oracle(edge[0], edge[1], spans))
	}
	assertPartition(t, want, buckets)
}

func TestUnknownPendingAndMaintenance(t *testing.T) {
	tests := []struct {
		name  string
		spans []availability.Span
		want  availability.Totals
	}{
		{
			name: "nil history",
			want: availability.Totals{StartMs: 10, EndMs: 30, UnknownMs: 20},
		},
		{
			name:  "empty history",
			spans: []availability.Span{},
			want:  availability.Totals{StartMs: 10, EndMs: 30, UnknownMs: 20},
		},
		{
			name:  "old up expires without extending maintenance",
			spans: []availability.Span{{StartMs: 0, EndMs: 10, State: availability.StateUp, Maintenance: true}},
			want:  availability.Totals{StartMs: 10, EndMs: 30, UnknownMs: 20},
		},
		{
			name:  "future up does not backfill",
			spans: []availability.Span{{StartMs: 30, EndMs: 40, State: availability.StateUp}},
			want:  availability.Totals{StartMs: 10, EndMs: 30, UnknownMs: 20},
		},
		{
			name:  "pending is not known",
			spans: []availability.Span{{StartMs: 10, EndMs: 30, State: availability.StatePending, Maintenance: true}},
			want:  availability.Totals{StartMs: 10, EndMs: 30, PendingMs: 20, MaintenanceMs: 20},
		},
		{
			name:  "observed unknown carries explicit maintenance only",
			spans: []availability.Span{{StartMs: 15, EndMs: 25, State: availability.StateUnknown, Maintenance: true}},
			want:  availability.Totals{StartMs: 10, EndMs: 30, UnknownMs: 20, MaintenanceMs: 10},
		},
		{
			name: "maintenance annotates up and down without excluding either",
			spans: []availability.Span{
				{StartMs: 10, EndMs: 20, State: availability.StateUp, Maintenance: true},
				{StartMs: 20, EndMs: 30, State: availability.StateDown, Maintenance: true},
			},
			want: availability.Totals{StartMs: 10, EndMs: 30, UpMs: 10, DownMs: 10, KnownMs: 20, MaintenanceMs: 20, AvailabilityPercent: floatPtr(50), CoveragePercent: 100},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := availability.Summarize(10, 30, tt.spans)
			if err != nil {
				t.Fatal(err)
			}
			assertTotals(t, got, tt.want)
			buckets, err := availability.Buckets(10, 30, 7, tt.spans)
			if err != nil {
				t.Fatal(err)
			}
			assertPartition(t, got, buckets)
		})
	}
}

func TestQueryValidation(t *testing.T) {
	tests := []struct {
		name       string
		start, end int64
	}{
		{"negative start", -1, 1},
		{"negative end", -2, -1},
		{"empty", 5, 5},
		{"zero empty", 0, 0},
		{"reversed", 6, 5},
		{"past safe end", 0, availability.MaxTimeMs + 1},
		{"past safe start", availability.MaxTimeMs + 1, availability.MaxTimeMs + 2},
		{"int64 extremes", math.MinInt64, math.MaxInt64},
		{"largest end", 1, math.MaxInt64},
		{"largest empty", math.MaxInt64, math.MaxInt64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := availability.Summarize(tt.start, tt.end, nil)
			if err == nil || !reflect.DeepEqual(got, availability.Totals{}) {
				t.Fatalf("Summarize = %+v, %v; want zero result and error", got, err)
			}
			buckets, err := availability.Buckets(tt.start, tt.end, 1, nil)
			if err == nil || buckets != nil {
				t.Fatalf("Buckets = %+v, %v; want nil and error", buckets, err)
			}
		})
	}
}

func TestEverySpanValidatedEvenOutsideQuery(t *testing.T) {
	up := availability.StateUp
	tests := []struct {
		name  string
		spans []availability.Span
	}{
		{"negative start", []availability.Span{{StartMs: -1, EndMs: 1, State: up}}},
		{"negative end", []availability.Span{{StartMs: -2, EndMs: -1, State: up}}},
		{"zero length before query", []availability.Span{{StartMs: 1, EndMs: 1, State: up}}},
		{"reversed after query", []availability.Span{{StartMs: 40, EndMs: 30, State: up}}},
		{"past safe end", []availability.Span{{StartMs: 40, EndMs: availability.MaxTimeMs + 1, State: up}}},
		{"past safe start", []availability.Span{{StartMs: availability.MaxTimeMs + 1, EndMs: availability.MaxTimeMs + 2, State: up}}},
		{"int64 extremes", []availability.Span{{StartMs: math.MinInt64, EndMs: math.MaxInt64, State: up}}},
		{"empty state", []availability.Span{{StartMs: 0, EndMs: 1}}},
		{"uppercase state", []availability.Span{{StartMs: 0, EndMs: 1, State: "UP"}}},
		{"whitespace state", []availability.Span{{StartMs: 30, EndMs: 40, State: "up "}}},
		{"unsupported state", []availability.Span{{StartMs: 30, EndMs: 40, State: "healthy"}}},
		{"duplicates before query", []availability.Span{{StartMs: 0, EndMs: 5, State: up}, {StartMs: 0, EndMs: 5, State: up}}},
		{"overlap before query", []availability.Span{{StartMs: 0, EndMs: 5, State: up}, {StartMs: 4, EndMs: 6, State: availability.StateDown}}},
		{"overlap after query", []availability.Span{{StartMs: 30, EndMs: 40, State: up}, {StartMs: 35, EndMs: 45, State: up}}},
		{"unsorted disjoint", []availability.Span{{StartMs: 30, EndMs: 40, State: up}, {StartMs: 0, EndMs: 5, State: up}}},
		{"nested overlap", []availability.Span{{StartMs: 0, EndMs: 40, State: up}, {StartMs: 10, EndMs: 20, State: availability.StateUnknown}}},
		{"invalid tail after valid query span", []availability.Span{{StartMs: 10, EndMs: 20, State: up}, {StartMs: 30, EndMs: 40, State: "invalid"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := slices.Clone(tt.spans)
			got, err := availability.Summarize(10, 20, tt.spans)
			if err == nil || !reflect.DeepEqual(got, availability.Totals{}) {
				t.Fatalf("Summarize = %+v, %v; want zero result and error", got, err)
			}
			buckets, err := availability.Buckets(10, 20, 5, tt.spans)
			if err == nil || buckets != nil {
				t.Fatalf("Buckets = %+v, %v; want nil and error", buckets, err)
			}
			if !reflect.DeepEqual(tt.spans, before) {
				t.Fatal("validation mutated input")
			}
		})
	}
}

func TestStepAndBucketLimits(t *testing.T) {
	for _, step := range []int64{math.MinInt64, -1, 0, availability.MaxStepMs + 1, math.MaxInt64} {
		t.Run(fmt.Sprint(step), func(t *testing.T) {
			got, err := availability.Buckets(0, 1, step, nil)
			if err == nil || got != nil {
				t.Fatalf("invalid step accepted: %v, %v", got, err)
			}
		})
	}
	for _, step := range []int64{availability.MinStepMs, availability.MaxStepMs} {
		got, err := availability.Buckets(0, step, step, nil)
		if err != nil || len(got) != 1 || got[0].StartMs != 0 || got[0].EndMs != step {
			t.Fatalf("step boundary %d: %+v, %v", step, got, err)
		}
	}
	tests := []struct {
		name             string
		start, end, step int64
		wantCount        int
	}{
		{"single millisecond", 0, 1, 1, 1},
		{"exact aligned end", 0, 20, 10, 2},
		{"start on boundary", 10, 21, 10, 2},
		{"both partial", 9, 11, 10, 2},
		{"one partial", 1, 9, 10, 1},
		{"maximum count", 0, availability.MaxBuckets, 1, availability.MaxBuckets},
		{"too many", 0, availability.MaxBuckets + 1, 1, 0},
		{"partial edges fit", 1, 40951, 10, availability.MaxBuckets},
		{"partial edges exceed", 9, 40961, 10, 0},
		{"huge count rejected before allocation", 0, availability.MaxTimeMs, 1, 0},
		{"huge count at largest step", 0, availability.MaxTimeMs, availability.MaxStepMs, 0},
		{"safe maximum single millisecond", availability.MaxTimeMs - 1, availability.MaxTimeMs, 1, 1},
		{"safe maximum boundary addition", availability.MaxTimeMs - 3, availability.MaxTimeMs, availability.MaxStepMs, 1},
		{"maximum timestamp maximum count", availability.MaxTimeMs - availability.MaxBuckets, availability.MaxTimeMs, 1, availability.MaxBuckets},
		{"maximum timestamp excessive count", availability.MaxTimeMs - availability.MaxBuckets - 1, availability.MaxTimeMs, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := availability.Buckets(tt.start, tt.end, tt.step, nil)
			if tt.wantCount == 0 {
				if err == nil || got != nil || !strings.Contains(err.Error(), "buckets") {
					t.Fatalf("want bucket limit rejection; got len %d, %v", len(got), err)
				}
				return
			}
			if err != nil || len(got) != tt.wantCount {
				t.Fatalf("got len %d, %v; want %d", len(got), err, tt.wantCount)
			}
			for i, bucket := range got {
				if bucket.EndMs <= bucket.StartMs || bucket.UnknownMs != bucket.EndMs-bucket.StartMs {
					t.Fatalf("invalid bucket %d: %+v", i, bucket)
				}
				if i > 0 && (bucket.StartMs != got[i-1].EndMs || bucket.StartMs%tt.step != 0) {
					t.Fatalf("bucket %d is not contiguous and epoch aligned: %+v", i, bucket)
				}
			}
			if got[0].StartMs != tt.start || got[len(got)-1].EndMs != tt.end {
				t.Fatalf("edge buckets extend outside query: %+v", got)
			}
		})
	}
}

func TestSpanLimit(t *testing.T) {
	spans := make([]availability.Span, availability.MaxSpans)
	for i := range spans {
		spans[i] = availability.Span{StartMs: int64(2 * i), EndMs: int64(2*i + 1), State: availability.StateUp}
	}
	end := int64(2 * availability.MaxSpans)
	got, err := availability.Summarize(0, end, spans)
	if err != nil || got.UpMs != availability.MaxSpans || got.UnknownMs != availability.MaxSpans {
		t.Fatalf("maximum spans: %+v, %v", got, err)
	}
	buckets, err := availability.Buckets(0, end, 64, spans)
	if err != nil {
		t.Fatal(err)
	}
	assertPartition(t, got, buckets)
	tooMany := append(spans, availability.Span{StartMs: end, EndMs: end + 1, State: availability.StateUp})
	if _, err := availability.Summarize(end+1, end+2, tooMany); err == nil || !strings.Contains(err.Error(), "spans") {
		t.Fatalf("out-of-query spans must count toward limit: %v", err)
	}
	if got, err := availability.Buckets(end+1, end+2, 1, tooMany); err == nil || got != nil || !strings.Contains(err.Error(), "spans") {
		t.Fatalf("out-of-query spans must count toward bucket input limit: len %d, %v", len(got), err)
	}
}

func TestMaxSafeArithmetic(t *testing.T) {
	end := availability.MaxTimeMs
	spans := []availability.Span{
		{StartMs: 0, EndMs: end - 1, State: availability.StateUp, Maintenance: true},
		{StartMs: end - 1, EndMs: end, State: availability.StateDown, Maintenance: true},
	}
	got, err := availability.Summarize(0, end, spans)
	if err != nil {
		t.Fatal(err)
	}
	wantPercent := 100 * float64(end-1) / float64(end)
	assertTotals(t, got, availability.Totals{
		StartMs: 0, EndMs: end, UpMs: end - 1, DownMs: 1, KnownMs: end,
		MaintenanceMs: end, AvailabilityPercent: &wantPercent, CoveragePercent: 100,
	})
	buckets, err := availability.Buckets(end-3, end, 2, spans)
	if err != nil {
		t.Fatal(err)
	}
	clipped, err := availability.Summarize(end-3, end, spans)
	if err != nil {
		t.Fatal(err)
	}
	assertPartition(t, clipped, buckets)
	if len(buckets) != 2 || buckets[0].StartMs != end-3 || buckets[0].EndMs != end-1 || buckets[1].EndMs != end {
		t.Fatalf("unexpected maximum-time boundaries: %+v", buckets)
	}
	for _, state := range []availability.State{availability.StateUp, availability.StateDown, availability.StatePending, availability.StateUnknown} {
		got, err := availability.Summarize(0, end, []availability.Span{{StartMs: 0, EndMs: end, State: state, Maintenance: true}})
		if err != nil {
			t.Fatal(err)
		}
		assertInvariants(t, got)
		if got.MaintenanceMs != end {
			t.Fatalf("maintenance overflow for %s: %+v", state, got)
		}
	}
}

func TestClippingAndPartitionIdentity(t *testing.T) {
	spans := []availability.Span{
		{StartMs: 0, EndMs: 7, State: availability.StateUp},
		{StartMs: 7, EndMs: 19, State: availability.StateDown, Maintenance: true},
		{StartMs: 22, EndMs: 29, State: availability.StatePending},
		{StartMs: 29, EndMs: 41, State: availability.StateUnknown, Maintenance: true},
		{StartMs: 44, EndMs: 80, State: availability.StateUp, Maintenance: true},
	}
	full, err := availability.Summarize(0, 90, spans)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range [][2]int64{{0, 90}, {5, 65}, {7, 19}, {19, 22}, {30, 40}, {79, 81}, {81, 90}} {
		start, end := query[0], query[1]
		got, err := availability.Summarize(start, end, spans)
		if err != nil {
			t.Fatal(err)
		}
		assertTotals(t, got, oracle(start, end, spans))
		var clipped []availability.Span
		for _, span := range spans {
			if span.EndMs <= start || span.StartMs >= end {
				continue
			}
			span.StartMs = max(span.StartMs, start)
			span.EndMs = min(span.EndMs, end)
			clipped = append(clipped, span)
		}
		gotClipped, err := availability.Summarize(start, end, clipped)
		if err != nil {
			t.Fatal(err)
		}
		assertTotals(t, got, gotClipped)
		var parts []availability.Bucket
		for _, edge := range [][2]int64{{0, start}, {start, end}, {end, 90}} {
			if edge[0] < edge[1] {
				part, err := availability.Summarize(edge[0], edge[1], spans)
				if err != nil {
					t.Fatal(err)
				}
				parts = append(parts, availability.Bucket{Totals: part})
			}
		}
		assertPartition(t, full, parts)
		for _, step := range []int64{1, 7, 19, 100} {
			buckets, err := availability.Buckets(start, end, step, spans)
			if err != nil {
				t.Fatal(err)
			}
			assertPartition(t, got, buckets)
		}
	}
}

func TestArbitraryEpochPeriods(t *testing.T) {
	spans := []availability.Span{
		{StartMs: 0, EndMs: 14, State: availability.StateUp, Maintenance: true},
		{StartMs: 14, EndMs: 25, State: availability.StateDown},
		{StartMs: 29, EndMs: 43, State: availability.StatePending},
		{StartMs: 43, EndMs: 70, State: availability.StateUnknown, Maintenance: true},
	}
	for _, offset := range []int64{0, 1, 12345, 1701234567890, availability.MaxTimeMs - 100} {
		shifted := slices.Clone(spans)
		for i := range shifted {
			shifted[i].StartMs += offset
			shifted[i].EndMs += offset
		}
		got, err := availability.Summarize(offset+3, offset+75, shifted)
		if err != nil {
			t.Fatal(err)
		}
		want := oracle(3, 75, spans)
		want.StartMs += offset
		want.EndMs += offset
		assertTotals(t, got, want)
		buckets, err := availability.Buckets(offset+3, offset+75, 17, shifted)
		if err != nil {
			t.Fatal(err)
		}
		for i, bucket := range buckets {
			want := oracle(bucket.StartMs-offset, bucket.EndMs-offset, spans)
			want.StartMs += offset
			want.EndMs += offset
			assertTotals(t, bucket.Totals, want)
			if i > 0 && bucket.StartMs%17 != 0 {
				t.Fatalf("bucket not aligned to epoch: %+v", bucket)
			}
		}
		assertPartition(t, got, buckets)
	}
}

func TestRandomSmallTimelineAgainstIndependentOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(772139))
	states := []availability.State{availability.StateUp, availability.StateDown, availability.StatePending, availability.StateUnknown}
	for trial := 0; trial < 500; trial++ {
		spans := make([]availability.Span, 0, 32)
		var cursor int64
		for i, n := 0, rng.Intn(33); i < n; i++ {
			cursor += int64(rng.Intn(4))
			end := cursor + int64(rng.Intn(7)+1)
			spans = append(spans, availability.Span{StartMs: cursor, EndMs: end, State: states[rng.Intn(4)], Maintenance: rng.Intn(2) == 0})
			cursor = end
		}
		start := int64(rng.Intn(200))
		end := start + int64(1+rng.Intn(80))
		step := int64(1 + rng.Intn(40))
		checkSmallTimeline(t, start, end, step, spans)
	}
}

func TestJSONContract(t *testing.T) {
	got, err := availability.Summarize(0, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"startMs":0,"endMs":10,"upMs":0,"downMs":0,"pendingMs":0,"unknownMs":10,"maintenanceMs":0,"knownMs":0,"availabilityPercent":null,"coveragePercent":0}`
	for _, value := range []any{got, availability.Bucket{Totals: got}} {
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != want {
			t.Fatalf("JSON = %s, %v; want %s", encoded, err, want)
		}
	}
	span := availability.Span{StartMs: 0, EndMs: 10, State: availability.StateUp, Maintenance: true}
	encoded, err := json.Marshal(span)
	if err != nil || string(encoded) != `{"startMs":0,"endMs":10,"state":"up","maintenance":true}` {
		t.Fatalf("span JSON = %s, %v", encoded, err)
	}
	known, err := availability.Summarize(0, 10, []availability.Span{span})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(known)
	if err != nil || !strings.Contains(string(encoded), `"availabilityPercent":100`) {
		t.Fatalf("known availability JSON = %s, %v", encoded, err)
	}
}

func TestInputsAndResultsAreIndependent(t *testing.T) {
	spans := []availability.Span{{StartMs: 0, EndMs: 40, State: availability.StateUp, Maintenance: true}}
	before := slices.Clone(spans)
	first, err := availability.Buckets(0, 40, 10, spans)
	if err != nil {
		t.Fatal(err)
	}
	second, err := availability.Buckets(0, 40, 10, spans)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := availability.Summarize(0, 40, spans)
	if err != nil {
		t.Fatal(err)
	}
	*first[0].AvailabilityPercent = -1
	first[0].UpMs = -1
	*summary.AvailabilityPercent = -2
	if *first[1].AvailabilityPercent != 100 || second[0].UpMs != 10 || *second[0].AvailabilityPercent != 100 {
		t.Fatal("results share mutable state")
	}
	if !reflect.DeepEqual(spans, before) {
		t.Fatal("functions changed caller spans")
	}
	spans[0].State = availability.StateDown
	if *second[0].AvailabilityPercent != 100 || second[0].UpMs != 10 {
		t.Fatal("result retained input storage")
	}
}

func TestConcurrentReadOnlyCalls(t *testing.T) {
	spans := []availability.Span{
		{StartMs: 0, EndMs: 14, State: availability.StateUp, Maintenance: true},
		{StartMs: 14, EndMs: 25, State: availability.StateDown},
		{StartMs: 29, EndMs: 43, State: availability.StatePending},
		{StartMs: 43, EndMs: 70, State: availability.StateUnknown, Maintenance: true},
	}
	before := slices.Clone(spans)
	want, err := availability.Summarize(3, 80, spans)
	if err != nil {
		t.Fatal(err)
	}
	wantBuckets, err := availability.Buckets(3, 80, 9, spans)
	if err != nil {
		t.Fatal(err)
	}
	errors := make(chan error, 8)
	var workers sync.WaitGroup
	for worker := 0; worker < cap(errors); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for iteration := 0; iteration < 30; iteration++ {
				got, err := availability.Summarize(3, 80, spans)
				if err != nil || !reflect.DeepEqual(got, want) {
					errors <- fmt.Errorf("concurrent summary differs: %+v, %v", got, err)
					return
				}
				buckets, err := availability.Buckets(3, 80, 9, spans)
				if err != nil || !reflect.DeepEqual(buckets, wantBuckets) {
					errors <- fmt.Errorf("concurrent buckets differ: %+v, %v", buckets, err)
					return
				}
				*buckets[0].AvailabilityPercent = -1
			}
		}()
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if !reflect.DeepEqual(spans, before) {
		t.Fatal("concurrent calls changed caller spans")
	}
}

// oracle deliberately counts each integer millisecond independently, scanning
// the original spans each time. Only small, bounded test timelines use it.
func oracle(start, end int64, spans []availability.Span) availability.Totals {
	got := availability.Totals{StartMs: start, EndMs: end}
	for instant := start; instant < end; instant++ {
		state := availability.StateUnknown
		maintenance := false
		for _, span := range spans {
			if instant >= span.StartMs && instant < span.EndMs {
				state, maintenance = span.State, span.Maintenance
				break
			}
		}
		switch state {
		case availability.StateUp:
			got.UpMs++
		case availability.StateDown:
			got.DownMs++
		case availability.StatePending:
			got.PendingMs++
		case availability.StateUnknown:
			got.UnknownMs++
		}
		if maintenance {
			got.MaintenanceMs++
		}
	}
	got.KnownMs = got.UpMs + got.DownMs
	if got.KnownMs > 0 {
		got.AvailabilityPercent = floatPtr(100 * float64(got.UpMs) / float64(got.KnownMs))
	}
	got.CoveragePercent = 100 * float64(got.KnownMs) / float64(end-start)
	return got
}

func checkSmallTimeline(t *testing.T, start, end, step int64, spans []availability.Span) {
	t.Helper()
	before := slices.Clone(spans)
	got, err := availability.Summarize(start, end, spans)
	if err != nil {
		t.Fatal(err)
	}
	assertTotals(t, got, oracle(start, end, spans))
	buckets, err := availability.Buckets(start, end, step, spans)
	if err != nil {
		t.Fatal(err)
	}
	// Find boundaries one millisecond at a time, independently of the
	// production division/count arithmetic. Test windows are <= 512 ms.
	index, bucketStart := 0, start
	for instant := start + 1; instant <= end; instant++ {
		if instant != end && instant%step != 0 {
			continue
		}
		if index >= len(buckets) {
			t.Fatal("missing bucket")
		}
		assertTotals(t, buckets[index].Totals, oracle(bucketStart, instant, spans))
		bucketStart = instant
		index++
	}
	if index != len(buckets) {
		t.Fatalf("got %d buckets, want %d", len(buckets), index)
	}
	assertPartition(t, got, buckets)
	if !reflect.DeepEqual(spans, before) {
		t.Fatal("input mutated")
	}
}

func assertTotals(t *testing.T, got, want availability.Totals) {
	t.Helper()
	assertInvariants(t, got)
	gotPercent, wantPercent := got.AvailabilityPercent, want.AvailabilityPercent
	got.AvailabilityPercent, want.AvailabilityPercent = nil, nil
	gotCoverage, wantCoverage := got.CoveragePercent, want.CoveragePercent
	got.CoveragePercent, want.CoveragePercent = 0, 0
	if got != want || math.Abs(gotCoverage-wantCoverage) > 1e-12 {
		t.Fatalf("totals = %+v (coverage %v), want %+v (coverage %v)", got, gotCoverage, want, wantCoverage)
	}
	if (gotPercent == nil) != (wantPercent == nil) {
		t.Fatalf("availability nil mismatch: got %v, want %v", gotPercent, wantPercent)
	}
	if gotPercent != nil && math.Abs(*gotPercent-*wantPercent) > 1e-12 {
		t.Fatalf("availability = %v, want %v", *gotPercent, *wantPercent)
	}
}

func assertInvariants(t *testing.T, got availability.Totals) {
	t.Helper()
	window := got.EndMs - got.StartMs
	if window <= 0 || got.UpMs < 0 || got.DownMs < 0 || got.PendingMs < 0 || got.UnknownMs < 0 || got.MaintenanceMs < 0 || got.MaintenanceMs > window {
		t.Fatalf("invalid duration: %+v", got)
	}
	if got.UpMs+got.DownMs+got.PendingMs+got.UnknownMs != window || got.KnownMs != got.UpMs+got.DownMs {
		t.Fatalf("accounting invariant failed: %+v", got)
	}
	if math.IsNaN(got.CoveragePercent) || math.IsInf(got.CoveragePercent, 0) || got.CoveragePercent < 0 || got.CoveragePercent > 100 {
		t.Fatalf("invalid coverage: %+v", got)
	}
	if (got.AvailabilityPercent == nil) != (got.KnownMs == 0) {
		t.Fatalf("availability denominator invariant failed: %+v", got)
	}
	if got.AvailabilityPercent != nil && (math.IsNaN(*got.AvailabilityPercent) || math.IsInf(*got.AvailabilityPercent, 0) || *got.AvailabilityPercent < 0 || *got.AvailabilityPercent > 100) {
		t.Fatalf("invalid availability: %+v", got)
	}
}

func assertPartition(t *testing.T, want availability.Totals, buckets []availability.Bucket) {
	t.Helper()
	if len(buckets) == 0 {
		t.Fatal("empty partition")
	}
	sum := availability.Totals{StartMs: want.StartMs, EndMs: want.EndMs}
	cursor := want.StartMs
	for _, bucket := range buckets {
		assertInvariants(t, bucket.Totals)
		if bucket.StartMs != cursor {
			t.Fatalf("partition gap: start %d, want %d", bucket.StartMs, cursor)
		}
		cursor = bucket.EndMs
		sum.UpMs += bucket.UpMs
		sum.DownMs += bucket.DownMs
		sum.PendingMs += bucket.PendingMs
		sum.UnknownMs += bucket.UnknownMs
		sum.MaintenanceMs += bucket.MaintenanceMs
		sum.KnownMs += bucket.KnownMs
	}
	if cursor != want.EndMs {
		t.Fatalf("partition end %d, want %d", cursor, want.EndMs)
	}
	if sum.KnownMs > 0 {
		sum.AvailabilityPercent = floatPtr(100 * float64(sum.UpMs) / float64(sum.KnownMs))
	}
	sum.CoveragePercent = 100 * float64(sum.KnownMs) / float64(sum.EndMs-sum.StartMs)
	assertTotals(t, sum, want)
}

func floatPtr(value float64) *float64 { return &value }
