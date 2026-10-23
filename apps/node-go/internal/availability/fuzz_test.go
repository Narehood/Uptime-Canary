package availability_test

import (
	"encoding/binary"
	"math"
	"slices"
	"testing"

	"uptime-canary/apps/node-go/internal/availability"
)

func FuzzObservedTimeline(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 0, 1, 0, 0, 0, 0, 0})
	f.Add([]byte{3, 70, 7, 0, 15, 0, 9, 1, 7, 0, 3, 2, 3, 3, 7, 3})
	f.Add([]byte{255, 255, 255, 255, 255, 255, 255, 255})
	f.Fuzz(func(t *testing.T, input []byte) {
		// At most 64 spans, a 512 ms window, and 512 buckets. The oracle
		// intentionally scans time, so bound its workload independently.
		if len(input) > 260 {
			input = input[:260]
		}
		var header [4]byte
		copy(header[:], input)
		start := int64(header[0])
		end := start + 1 + int64(header[1])
		step := 1 + int64(header[2]%32)
		var spans []availability.Span
		cursor := int64(header[3] % 8)
		states := [...]availability.State{availability.StateUp, availability.StateDown, availability.StatePending, availability.StateUnknown}
		for i := 4; i+3 < len(input); i += 4 {
			cursor += int64(input[i] % 4)
			next := cursor + 1 + int64(input[i+1]%8)
			spans = append(spans, availability.Span{
				StartMs: cursor, EndMs: next, State: states[input[i+2]%4], Maintenance: input[i+3]%2 != 0,
			})
			cursor = next
		}
		checkSmallTimeline(t, start, end, step, spans)
	})
}

func FuzzInvalidInputs(f *testing.F) {
	encode := func(values ...int64) []byte {
		out := make([]byte, len(values)*8)
		for i, value := range values {
			binary.LittleEndian.PutUint64(out[i*8:], uint64(value))
		}
		return out
	}
	f.Add(encode(0, 10, 1, 0, 10, 0))
	f.Add(encode(0, availability.MaxTimeMs, math.MaxInt64))
	f.Add(encode(math.MinInt64, math.MaxInt64, 1))
	f.Add(encode(10, 20, 5, 0, 5, 0, 0, 5, 0))
	f.Add(encode(availability.MaxTimeMs-1, availability.MaxTimeMs, 1, 0, 1, 17))
	f.Fuzz(func(t *testing.T, input []byte) {
		// Three header integers and at most 16 raw spans; raw invalid times
		// are never fed into the per-millisecond reference oracle.
		if len(input) > 24+16*24 {
			input = input[:24+16*24]
		}
		read := func(offset int) int64 {
			var raw [8]byte
			if offset < len(input) {
				copy(raw[:], input[offset:])
			}
			return int64(binary.LittleEndian.Uint64(raw[:]))
		}
		start, end, step := read(0), read(8), read(16)
		var spans []availability.Span
		states := [...]availability.State{availability.StateUp, availability.StateDown, availability.StatePending, availability.StateUnknown, "invalid"}
		for i := 24; i+23 < len(input); i += 24 {
			code := uint64(read(i + 16))
			spans = append(spans, availability.Span{StartMs: read(i), EndMs: read(i + 8), State: states[code%5], Maintenance: code&8 != 0})
		}
		before := slices.Clone(spans)
		valid := start >= 0 && start < end && end <= availability.MaxTimeMs
		for i, span := range spans {
			valid = valid && span.StartMs >= 0 && span.StartMs < span.EndMs && span.EndMs <= availability.MaxTimeMs
			valid = valid && (span.State == availability.StateUp || span.State == availability.StateDown || span.State == availability.StatePending || span.State == availability.StateUnknown)
			if i > 0 {
				valid = valid && spans[i-1].EndMs <= span.StartMs
			}
		}
		summary, err := availability.Summarize(start, end, spans)
		if (err == nil) != valid {
			t.Fatalf("validation differs for query [%d,%d), spans %+v: %v", start, end, spans, err)
		}
		if err == nil {
			assertInvariants(t, summary)
		}
		validBuckets := valid && step >= availability.MinStepMs && step <= availability.MaxStepMs
		if validBuckets {
			// Count without production's quotient-difference formula. All
			// additions here fit int64 because validated times are <= 2^53-1.
			firstEnd := start - start%step + step
			count := int64(1)
			if end > firstEnd {
				count += (end - firstEnd + step - 1) / step
			}
			validBuckets = count <= availability.MaxBuckets
		}
		buckets, err := availability.Buckets(start, end, step, spans)
		if (err == nil) != validBuckets {
			t.Fatalf("bucket validation differs for [%d,%d), step %d: %v", start, end, step, err)
		}
		if err == nil {
			assertPartition(t, summary, buckets)
		} else if buckets != nil {
			t.Fatal("partial result on error")
		}
		if !slices.Equal(spans, before) {
			t.Fatal("input mutated")
		}
	})
}
