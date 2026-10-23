package availability_test

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"reflect"
	"slices"
	"testing"

	"uptime-canary/apps/node-go/internal/availability"
)

// The review oracle starts with a complete labelled partition, including gaps.
// Its answers are differences of prefix measures on those cells. It never
// reads the generated Span slice, calls another production function, or uses
// the production span-intersection or bucket-count formulas.
// Labels: gap, up/down/pending/unknown, then those four with maintenance.
func reviewSpans(edges []int64, labels []byte) []availability.Span {
	states := [...]availability.State{availability.StateUp, availability.StateDown, availability.StatePending, availability.StateUnknown}
	var spans []availability.Span
	for i, label := range labels {
		if label == 0 {
			continue
		}
		spans = append(spans, availability.Span{
			StartMs: edges[i], EndMs: edges[i+1],
			State: states[(label-1)%4], Maintenance: label > 4,
		})
	}
	return spans
}

func reviewPrefix(at int64, edges []int64, labels []byte) [5]int64 {
	var measure [5]int64
	for i, label := range labels {
		if at <= edges[i] {
			break
		}
		length := edges[i+1] - edges[i]
		if at < edges[i+1] {
			length = at - edges[i]
		}
		state := 3 // Explicitly include missing history as unknown cells.
		if label != 0 {
			state = int((label - 1) % 4)
		}
		measure[state] += length
		if label > 4 {
			measure[4] += length
		}
	}
	return measure
}

func reviewRatio(numerator, denominator int64) float64 {
	// Both inputs are <= 2^53-1, so 100*numerator fits int64. Calculate
	// the reference in exact rational arithmetic before one float conversion.
	value, _ := new(big.Rat).SetFrac64(100*numerator, denominator).Float64()
	return value
}

func reviewExpected(start, end int64, edges []int64, labels []byte) availability.Totals {
	a, b := reviewPrefix(start, edges, labels), reviewPrefix(end, edges, labels)
	for i := range b {
		b[i] -= a[i]
	}
	want := availability.Totals{
		StartMs: start, EndMs: end, UpMs: b[0], DownMs: b[1],
		PendingMs: b[2], UnknownMs: b[3], MaintenanceMs: b[4],
		KnownMs: b[0] + b[1],
	}
	want.CoveragePercent = reviewRatio(want.KnownMs, end-start)
	if want.KnownMs != 0 {
		percent := reviewRatio(want.UpMs, want.KnownMs)
		want.AvailabilityPercent = &percent
	}
	return want
}

func reviewEqual(t *testing.T, got, want availability.Totals) {
	t.Helper()
	gp, wp := got.AvailabilityPercent, want.AvailabilityPercent
	gc, wc := got.CoveragePercent, want.CoveragePercent
	got.AvailabilityPercent, want.AvailabilityPercent = nil, nil
	got.CoveragePercent, want.CoveragePercent = 0, 0
	if got != want {
		t.Fatalf("integer totals differ: got %+v; want %+v", got, want)
	}
	if math.IsNaN(gc) || math.IsInf(gc, 0) || gc < 0 || gc > 100 || math.Abs(gc-wc) > 1e-12 {
		t.Fatalf("coverage = %.17g; exact rational reference %.17g", gc, wc)
	}
	if (gp == nil) != (wp == nil) {
		t.Fatalf("availability nil mismatch: got %v; want %v", gp, wp)
	}
	if gp != nil && (math.IsNaN(*gp) || math.IsInf(*gp, 0) || *gp < 0 || *gp > 100 || math.Abs(*gp-*wp) > 1e-12) {
		t.Fatalf("availability = %.17g; exact rational reference %.17g", *gp, *wp)
	}
}

func reviewCheck(t *testing.T, start, end, step int64, edges []int64, labels []byte) {
	t.Helper()
	spans := reviewSpans(edges, labels)
	before := slices.Clone(spans)
	want := reviewExpected(start, end, edges, labels)
	got, err := availability.Summarize(start, end, spans)
	if err != nil {
		t.Fatal(err)
	}
	reviewEqual(t, got, want)
	buckets, err := availability.Buckets(start, end, step, spans)
	if err != nil {
		t.Fatal(err)
	}
	// Enumerate epoch lattice boundaries with arbitrary-precision arithmetic.
	// Do not derive the expected count from either production's count formula
	// or from its returned bucket endpoints.
	width := big.NewInt(step)
	next := new(big.Int).Quo(big.NewInt(start), width)
	next.Add(next, big.NewInt(1)).Mul(next, width)
	var sum [6]int64
	index, cursor := 0, start
	for cursor < end {
		stop := end
		if next.Cmp(big.NewInt(end)) < 0 {
			stop = next.Int64()
		}
		if index >= len(buckets) {
			t.Fatalf("missing expected bucket [%d,%d)", cursor, stop)
		}
		bucket := buckets[index].Totals
		reviewEqual(t, bucket, reviewExpected(cursor, stop, edges, labels))
		for j, value := range [6]int64{bucket.UpMs, bucket.DownMs, bucket.PendingMs, bucket.UnknownMs, bucket.MaintenanceMs, bucket.KnownMs} {
			sum[j] += value
		}
		cursor = stop
		index++
		next.Add(next, width)
	}
	if index != len(buckets) {
		t.Fatalf("unexpected extra buckets: got %d; want %d", len(buckets), index)
	}
	if sum != [6]int64{want.UpMs, want.DownMs, want.PendingMs, want.UnknownMs, want.MaintenanceMs, want.KnownMs} {
		t.Fatalf("bucket durations do not reproduce independently measured summary: %v", sum)
	}
	if !slices.Equal(spans, before) {
		t.Fatal("caller input changed")
	}
}

func TestReviewExhaustiveLabelledCells(t *testing.T) {
	// Exhaust all 9^4 combinations. Unequal cell widths make event-counting
	// errors observable; clip both ends and split cells at epoch boundaries.
	for pattern := 0; pattern < 9*9*9*9; pattern++ {
		labels := make([]byte, 4)
		for i, code := 0, pattern; i < len(labels); i, code = i+1, code/9 {
			labels[i] = byte(code % 9)
		}
		reviewCheck(t, 1, 16, 5, []int64{0, 2, 7, 13, 17}, labels)
	}
}

func TestReviewWeightedCellsAtSafeTimestampEdges(t *testing.T) {
	rng := rand.New(rand.NewSource(923746150))
	for trial := 0; trial < 240; trial++ {
		const cells = 24
		scale := []int64{1, 17, 1 << 20, availability.MaxStepMs}[trial%4]
		edges := make([]int64, cells+1)
		labels := make([]byte, cells)
		for i := range labels {
			edges[i+1] = edges[i] + int64(1+rng.Intn(32))*scale
			labels[i] = byte(rng.Intn(9))
		}
		// Include exact 0, arbitrary nonaligned origins, and the safe maximum.
		origin := []int64{0, 123456789, availability.MaxTimeMs - edges[cells]}[trial%3]
		for i := range edges {
			edges[i] += origin
		}
		start := edges[0] + int64(rng.Intn(17))
		end := edges[cells] - int64(rng.Intn(17))
		reviewCheck(t, start, end, scale, edges, labels)
	}
	// Full timestamp domain: each duration and the percentage numerator stay
	// exact; milliseconds are deliberately uneven and include a huge gap.
	edges := []int64{0, 1, 1 << 31, 1 << 32, 1 << 48, availability.MaxTimeMs - 2, availability.MaxTimeMs - 1, availability.MaxTimeMs}
	labels := []byte{5, 2, 7, 8, 0, 1, 6}
	got, err := availability.Summarize(0, availability.MaxTimeMs, reviewSpans(edges, labels))
	if err != nil {
		t.Fatal(err)
	}
	reviewEqual(t, got, reviewExpected(0, availability.MaxTimeMs, edges, labels))
}

func TestReviewOutsideQueryValidationAndNoPartialResults(t *testing.T) {
	good := availability.Span{StartMs: 50, EndMs: 60, State: availability.StateDown, Maintenance: true}
	tails := [][]availability.Span{
		{{StartMs: 80, EndMs: 90, State: "UP"}},
		{{StartMs: 80, EndMs: 90, State: "up\x00"}},
		{{StartMs: 80, EndMs: 90, State: "unknown "}},
		{{StartMs: 80, EndMs: 90, State: availability.StateUp}, {StartMs: 89, EndMs: 95, State: availability.StateUnknown}},
		{{StartMs: 90, EndMs: 95, State: availability.StateUp}, {StartMs: 80, EndMs: 85, State: availability.StateDown}},
		{{StartMs: availability.MaxTimeMs, EndMs: math.MaxInt64, State: availability.StateUp}},
		{{StartMs: math.MaxInt64, EndMs: math.MinInt64, State: availability.StateDown}},
	}
	for i, tail := range tails {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			spans := append([]availability.Span{good}, tail...)
			before := slices.Clone(spans)
			got, err := availability.Summarize(50, 60, spans)
			if err == nil || got != (availability.Totals{}) {
				t.Fatalf("invalid out-of-query tail returned %+v, %v", got, err)
			}
			buckets, err := availability.Buckets(50, 60, 3, spans)
			if err == nil || buckets != nil {
				t.Fatalf("invalid out-of-query tail returned buckets %v, %v", buckets, err)
			}
			if !slices.Equal(spans, before) {
				t.Fatal("rejection mutated caller input")
			}
		})
	}
}

func TestReviewCombinedLimitsAndPointerOwnership(t *testing.T) {
	step := availability.MaxStepMs
	base := (availability.MaxTimeMs/step - availability.MaxBuckets - 1) * step
	start, end := base+1, base+int64(availability.MaxBuckets-1)*step+1
	spans := make([]availability.Span, availability.MaxSpans)
	for i := 0; i < len(spans)-1; i++ {
		spans[i] = availability.Span{StartMs: int64(2 * i), EndMs: int64(2*i + 1), State: availability.StateDown}
	}
	spans[len(spans)-1] = availability.Span{StartMs: base, EndMs: availability.MaxTimeMs, State: availability.StateUp, Maintenance: true}
	before := slices.Clone(spans)
	buckets, err := availability.Buckets(start, end, step, spans)
	if err != nil || len(buckets) != availability.MaxBuckets {
		t.Fatalf("simultaneous inclusive limits: count %d, error %v", len(buckets), err)
	}
	owned := make(map[*float64]bool, len(buckets))
	for _, bucket := range buckets {
		length := bucket.EndMs - bucket.StartMs
		percent := 100.0
		reviewEqual(t, bucket.Totals, availability.Totals{
			StartMs: bucket.StartMs, EndMs: bucket.EndMs, UpMs: length,
			MaintenanceMs: length, KnownMs: length,
			AvailabilityPercent: &percent, CoveragePercent: 100,
		})
		if owned[bucket.AvailabilityPercent] {
			t.Fatal("two buckets share a mutable percentage pointer")
		}
		owned[bucket.AvailabilityPercent] = true
	}
	first, err := availability.Summarize(start, end, spans)
	if err != nil {
		t.Fatal(err)
	}
	second, err := availability.Summarize(start, end, spans)
	if err != nil {
		t.Fatal(err)
	}
	if first.AvailabilityPercent == second.AvailabilityPercent || owned[first.AvailabilityPercent] || owned[second.AvailabilityPercent] {
		t.Fatal("summary percentage aliases another result")
	}
	*first.AvailabilityPercent = -11
	for i := range buckets {
		*buckets[i].AvailabilityPercent = float64(i) // Each write must stay local.
	}
	for i, bucket := range buckets {
		if *bucket.AvailabilityPercent != float64(i) {
			t.Fatalf("bucket %d changed when writing another percentage", i)
		}
	}
	if *second.AvailabilityPercent != 100 || !slices.Equal(spans, before) {
		t.Fatal("mutating results changed another call or its input")
	}
	// An extra clipped edge exceeds the cap, even though duration/step rounds
	// down to MaxBuckets. No partial result may escape.
	tooFar := base + int64(availability.MaxBuckets)*step + 1
	if result, err := availability.Buckets(start, tooFar, step, spans); err == nil || result != nil {
		t.Fatalf("4097 epoch buckets accepted: count %d, error %v", len(result), err)
	}
	tooMany := append(slices.Clone(spans), availability.Span{})
	if got, err := availability.Summarize(start, end, tooMany); err == nil || got != (availability.Totals{}) {
		t.Fatalf("100001 supplied spans accepted: %+v, %v", got, err)
	}
	if result, err := availability.Buckets(start, end, step, tooMany); err == nil || result != nil {
		t.Fatalf("100001 supplied spans accepted by Buckets: count %d, error %v", len(result), err)
	}
}

func TestReviewFlatJSONExactIntegersAndNull(t *testing.T) {
	end := availability.MaxTimeMs
	edges := []int64{end - 4, end - 3, end - 2, end - 1, end}
	labels := []byte{5, 6, 7, 8}
	spans := reviewSpans(edges, labels)
	buckets, err := availability.Buckets(end-4, end, 1, spans)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := availability.Summarize(end-4, end, spans)
	if err != nil {
		t.Fatal(err)
	}
	values := []availability.Totals{summary}
	for _, bucket := range buckets {
		values = append(values, bucket.Totals)
	}
	for _, value := range values {
		for _, output := range []any{value, availability.Bucket{Totals: value}} {
			encoded, err := json.Marshal(output)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			if len(fields) != 10 {
				t.Fatalf("expected exactly ten flat fields: %s", encoded)
			}
			integers := map[string]int64{
				"startMs": value.StartMs, "endMs": value.EndMs,
				"upMs": value.UpMs, "downMs": value.DownMs,
				"pendingMs": value.PendingMs, "unknownMs": value.UnknownMs,
				"maintenanceMs": value.MaintenanceMs, "knownMs": value.KnownMs,
			}
			for key, want := range integers {
				if string(fields[key]) != fmt.Sprint(want) {
					t.Fatalf("%s lost its exact integer value in %s", key, encoded)
				}
			}
			if _, ok := fields["coveragePercent"]; !ok {
				t.Fatalf("missing coveragePercent in %s", encoded)
			}
			percent, ok := fields["availabilityPercent"]
			if !ok || (string(percent) == "null") != (value.KnownMs == 0) {
				t.Fatalf("availability must be present and null iff no known time: %s", encoded)
			}
			var decoded availability.Totals
			if err := json.Unmarshal(encoded, &decoded); err != nil || !reflect.DeepEqual(decoded, value) {
				t.Fatalf("JSON roundtrip differs: %+v, %v", decoded, err)
			}
		}
	}
}

func FuzzReviewWeightedCells(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{255, 255, 255, 255, 255, 255, 255, 255, 3, 5, 31, 6, 2, 7, 0, 8, 5, 0})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 5, 2, 4, 3, 3, 4})
	f.Fuzz(func(t *testing.T, input []byte) {
		// The decoder bounds oracle and production workloads independently:
		// <=24 cells/spans, <=768 nominal buckets, <=~2.5e13 ms wide.
		var data [64]byte
		copy(data[:], input)
		scale := []int64{1, 17, 1 << 20, availability.MaxStepMs}[data[8]%4]
		count := 1 + int(data[9]%24)
		edges := make([]int64, count+1)
		labels := make([]byte, count)
		for i := range labels {
			edges[i+1] = edges[i] + (1+int64(data[10+2*i]%32))*scale
			labels[i] = data[11+2*i] % 9
		}
		room := availability.MaxTimeMs - edges[count]
		origin := int64(binary.LittleEndian.Uint64(data[:8]) % uint64(room+1))
		if data[63]&1 != 0 {
			origin = room // Frequently finish at the safe maximum, not just near it.
		}
		for i := range edges {
			edges[i] += origin
		}
		start, end := edges[0], edges[count]
		if end-start > 2 {
			start += int64(data[60]) % (end - start - 1)
			end -= int64(data[61]) % (end - start)
		}
		reviewCheck(t, start, end, scale, edges, labels)
	})
}
