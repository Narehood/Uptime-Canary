package rollup_test

import (
	"fmt"
	"testing"

	"uptime-canary/apps/node-go/internal/rollup"
)

func benchmarkForest(size int, deep bool) ([]rollup.Node, []rollup.Observation) {
	nodes := make([]rollup.Node, size)
	observations := make([]rollup.Observation, 0, size)
	for i := range nodes {
		parent := ""
		if i > 0 {
			parent = "0"
			if deep {
				parent = nodes[i-1].ID
			}
		}
		group := i == 0 || deep && i < size-1
		nodes[i] = rollup.Node{ID: fmt.Sprint(i), ParentID: parent, Group: group, Enabled: true}
		if !group {
			observations = append(observations, rollup.Observation{ID: nodes[i].ID, State: rollup.Up, Fresh: true})
		}
	}
	return nodes, observations
}

func BenchmarkCompile(b *testing.B) {
	for _, size := range []int{1_000, 10_000, rollup.MaxNodes} {
		for _, deep := range []bool{false, true} {
			b.Run(fmt.Sprintf("nodes=%d/deep=%t", size, deep), func(b *testing.B) {
				nodes, _ := benchmarkForest(size, deep)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := rollup.Compile(nodes); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkEvaluate(b *testing.B) {
	for _, size := range []int{1_000, 10_000, rollup.MaxNodes} {
		for _, deep := range []bool{false, true} {
			b.Run(fmt.Sprintf("nodes=%d/deep=%t", size, deep), func(b *testing.B) {
				nodes, observations := benchmarkForest(size, deep)
				p, err := rollup.Compile(nodes)
				if err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := p.Evaluate(observations); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
