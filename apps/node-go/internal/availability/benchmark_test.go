package availability_test

import (
	"fmt"
	"testing"

	"uptime-canary/apps/node-go/internal/availability"
)

func BenchmarkSummarize(b *testing.B) {
	for _, count := range []int{0, 100, availability.MaxSpans} {
		b.Run(fmt.Sprintf("spans_%d", count), func(b *testing.B) {
			spans := benchmarkSpans(count)
			end := int64(2*count + 1)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, err := availability.Summarize(0, end, spans)
				if err != nil || got.KnownMs != int64(count) {
					b.Fatalf("unexpected summary: %+v, %v", got, err)
				}
			}
		})
	}
	b.Run("maximum_timestamp_gap", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			got, err := availability.Summarize(0, availability.MaxTimeMs, nil)
			if err != nil || got.UnknownMs != availability.MaxTimeMs {
				b.Fatalf("unexpected summary: %+v, %v", got, err)
			}
		}
	})
}

func BenchmarkBuckets(b *testing.B) {
	b.Run("maximum_spans", func(b *testing.B) {
		spans := benchmarkSpans(availability.MaxSpans)
		end := int64(2 * availability.MaxSpans)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			got, err := availability.Buckets(0, end, 64, spans)
			if err != nil || len(got) != 3125 {
				b.Fatalf("unexpected buckets: len %d, %v", len(got), err)
			}
		}
	})
	b.Run("maximum_buckets_crossing_span", func(b *testing.B) {
		end := int64(availability.MaxBuckets) * availability.MaxStepMs
		spans := []availability.Span{{StartMs: 0, EndMs: end, State: availability.StateUp, Maintenance: true}}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			got, err := availability.Buckets(0, end, availability.MaxStepMs, spans)
			if err != nil || len(got) != availability.MaxBuckets {
				b.Fatalf("unexpected buckets: len %d, %v", len(got), err)
			}
		}
	})
}

func benchmarkSpans(count int) []availability.Span {
	spans := make([]availability.Span, count)
	for i := range spans {
		state := availability.StateUp
		if i%3 == 0 {
			state = availability.StateDown
		}
		spans[i] = availability.Span{StartMs: int64(2 * i), EndMs: int64(2*i + 1), State: state, Maintenance: i%5 == 0}
	}
	return spans
}
