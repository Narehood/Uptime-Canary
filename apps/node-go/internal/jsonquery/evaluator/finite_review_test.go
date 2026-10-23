package jsonata

import (
	"math"
	"testing"
)

// These review tests inspect data only; no expression is parsed or evaluated and
// no callback is registered with the evaluator.
type profileReviewOpaque struct{ formatted *bool }

func (v profileReviewOpaque) String() string {
	*v.formatted = true
	return "review-private-value"
}

func TestFiniteReviewRedactionAndOpaqueValues(t *testing.T) {
	formatted := false
	opaque := profileReviewOpaque{formatted: &formatted}
	if err := rejectNonfiniteResult(map[string]interface{}{"review-private-key": opaque}); err != nil {
		t.Fatal("opaque value rejected")
	}
	value := map[string]interface{}{
		"review-private-key": []interface{}{opaque, JSONataArray{Data: []interface{}{float32(math.Inf(-1))}}},
	}
	err := rejectNonfiniteResult(value)
	if err != errFiniteResult || err.Error() != "JSON query result rejected by finite profile" || formatted {
		t.Fatal("rejection formatted an input or changed the fixed error")
	}
}

func TestFiniteReviewAliasesMutationAndFreshBudget(t *testing.T) {
	shared := map[string]interface{}{"n": 1.0}
	graph := []interface{}{shared, &JSONataArray{Data: []interface{}{shared}}, JSONataArray{Data: []interface{}{shared}}}
	if err := rejectNonfiniteResult(graph); err != nil {
		t.Fatal("finite aliases rejected")
	}
	shared["n"] = math.NaN()
	if rejectNonfiniteResult(graph) != errFiniteResult {
		t.Fatal("aliased mutation escaped a fresh walk")
	}
	shared["n"] = -math.MaxFloat64
	if err := rejectNonfiniteResult(graph); err != nil {
		t.Fatal("previous rejection contaminated the next result")
	}
	if rejectNonfiniteResult(make([]interface{}, 65536)) != errFiniteResult || rejectNonfiniteResult(true) != nil {
		t.Fatal("visit rejection contaminated a separate result budget")
	}
}

func TestFiniteReviewMixedDepthBoundary(t *testing.T) {
	var value interface{} = 1.0
	for i := 0; i < 128; i++ {
		switch i % 4 {
		case 0:
			value = []interface{}{value}
		case 1:
			value = map[string]interface{}{"a": value}
		case 2:
			value = &JSONataArray{Data: []interface{}{value}}
		case 3:
			value = JSONataArray{Data: []interface{}{value}}
		}
	}
	if rejectNonfiniteResult(value) != nil || rejectNonfiniteResult(map[string]interface{}{"a": value}) != errFiniteResult {
		t.Fatal("mixed containers changed the documented depth boundary")
	}
	cycle := &JSONataArray{}
	cycle.Data = []interface{}{map[string]interface{}{"a": JSONataArray{Data: []interface{}{cycle}}}}
	if rejectNonfiniteResult(cycle) != errFiniteResult {
		t.Fatal("mixed pointer/value cycle escaped rejection")
	}
}

func TestFiniteReviewRepeatedReferenceBudget(t *testing.T) {
	// root + 255 copies of (one container + 256 leaves) = 65,536.
	shared := make([]interface{}, 256)
	graph := make([]interface{}, 255)
	for i := range graph {
		graph[i] = &JSONataArray{Data: shared}
	}
	if err := rejectNonfiniteResult(graph); err != nil {
		t.Fatal("exact aggregate visit limit rejected")
	}
	graph = append(graph, nil)
	if rejectNonfiniteResult(graph) != errFiniteResult {
		t.Fatal("aggregate visit limit exempted shared references")
	}
}
