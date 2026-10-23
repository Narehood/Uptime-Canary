package jsvm

import (
	"github.com/dop251/goja"
	"testing"
)

// Benchmark the actual embedded production asset beside its canonical source.
var phaseWhitespace = runtimeLibrary

func BenchmarkR3TrustedBundleCompile(b *testing.B) {
	for name, source := range map[string]string{"unminified": library, "whitespace": phaseWhitespace} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := goja.Compile("trusted.js", source, false); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
