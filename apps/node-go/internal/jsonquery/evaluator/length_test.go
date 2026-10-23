package jsonata

import (
	"strings"
	"testing"
)

func TestStringCodepointCount(t *testing.T) {
	// Pure count helper only: no compilation, engine evaluation or host binding.
	for _, tc := range []struct {
		name, value string
		want        int
	}{
		{"ASCII", "canary", 6},
		{"multibyte", "é漢🙂", 3},
		{"combining", "e\u0301", 2},
		{"empty", "", 0},
		{"invalid byte retains range behavior", string([]byte{0xff, 'a'}), 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := countStringCodepoints(tc.value); got != tc.want {
				t.Fatalf("codepoints=%d want=%d", got, tc.want)
			}
		})
	}
}

func TestStringCodepointCountAllocation(t *testing.T) {
	value := strings.Repeat("a", 1<<20)
	count := 0
	allocations := testing.AllocsPerRun(5, func() {
		count = countStringCodepoints(value)
	})
	if count != len(value) || allocations != 0 {
		t.Fatalf("one-MiB count=%d allocations=%g", count, allocations)
	}
}

func BenchmarkStringCodepointCount(b *testing.B) {
	for _, tc := range []struct {
		name, value string
		want        int
	}{
		{"ASCII-1MiB", strings.Repeat("a", 1<<20), 1 << 20},
		{"multibyte", strings.Repeat("é漢🙂", 1<<16), 3 << 16},
		{"combining", strings.Repeat("e\u0301", 1<<16), 2 << 16},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.value)))
			for b.Loop() {
				if countStringCodepoints(tc.value) != tc.want {
					b.Fatal("codepoint count changed")
				}
			}
		})
	}
}
