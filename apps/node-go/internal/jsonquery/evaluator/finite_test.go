package jsonata

import (
	"math"
	"strconv"
	"testing"
)

// These tests exercise only the bounded data walker. No expression compilation,
// evaluation, arbitrary binding, function registration or callback invocation occurs.
func TestFiniteResultScalarsAndContainers(t *testing.T) {
	for _, value := range []interface{}{
		nil, &JSONataNull{}, true, "private-value", float64(0), math.Copysign(0, -1),
		math.MaxFloat64, -math.MaxFloat64, float32(math.MaxFloat32), int64(-1), uint64(1),
		map[string]interface{}{"a": []interface{}{1.0, true, &JSONataArray{Data: []interface{}{2.0}}}},
		(*JSONataArray)(nil), JSONataArray{Data: []interface{}{3.0}},
	} {
		if err := rejectNonfiniteResult(value); err != nil {
			t.Fatalf("finite value of type %T rejected: %v", value, err)
		}
	}
	for _, nonfinite := range []interface{}{
		math.Inf(1), math.Inf(-1), math.NaN(), float32(math.Inf(1)), float32(math.NaN()),
	} {
		for _, value := range []interface{}{
			nonfinite,
			map[string]interface{}{"private-key": []interface{}{&JSONataArray{Data: []interface{}{nonfinite}}}},
			JSONataArray{Data: []interface{}{nonfinite}},
		} {
			requireFiniteRejection(t, value)
		}
	}
}

func TestFiniteResultDepthAndCycles(t *testing.T) {
	var value interface{} = true
	for range finiteResultMaxDepth {
		value = []interface{}{value}
	}
	if err := rejectNonfiniteResult(value); err != nil {
		t.Fatal("exact depth limit rejected")
	}
	requireFiniteRejection(t, []interface{}{value})

	object := map[string]interface{}{}
	object["self"] = object
	slice := make([]interface{}, 1)
	slice[0] = slice
	array := &JSONataArray{}
	array.Data = []interface{}{array}
	mixed := map[string]interface{}{}
	mixed["cycle"] = &JSONataArray{Data: []interface{}{[]interface{}{mixed}}}
	for _, cycle := range []interface{}{object, slice, array, mixed} {
		// Every cycle deterministically stops at the same fixed depth budget;
		// no host binding or cyclic expression input is used to construct it.
		requireFiniteRejection(t, cycle)
	}
}

func TestFiniteResultVisitBudget(t *testing.T) {
	exact := make([]interface{}, finiteResultMaxValues-1) // container also counts
	if err := rejectNonfiniteResult(exact); err != nil {
		t.Fatal("exact visit budget rejected")
	}
	requireFiniteRejection(t, make([]interface{}, finiteResultMaxValues))
	requireFiniteRejection(t, &JSONataArray{Data: make([]interface{}, finiteResultMaxValues)})

	// Shared references count each time. Local lengths fit, but the total walk
	// is 1 + 256 + 256*255 = 65,537 values, above the per-result budget.
	child := make([]interface{}, 255)
	shared := make([]interface{}, 256)
	for i := range shared {
		shared[i] = child
	}
	if err := rejectNonfiniteResult(shared[:255]); err != nil {
		t.Fatal("finite shared graph within budget rejected")
	}
	requireFiniteRejection(t, shared)

	// Exercise the object fast bound as well as aggregate nested accounting.
	object := make(map[string]interface{}, finiteResultMaxValues)
	for i := range finiteResultMaxValues {
		object[strconv.Itoa(i)] = nil
	}
	requireFiniteRejection(t, object)
}

func TestFiniteResultLeavesFunctionsOpaque(t *testing.T) {
	called := false
	callback := func(...interface{}) (interface{}, error) {
		called = true
		return math.Inf(1), nil
	}
	// The walker must not call functions or inspect captured input/environments.
	// These are helper values only, never evaluator host bindings.
	lambda := &Lambda{input: math.Inf(1), Implementation: callback}
	function := &Function{Implementation: callback}
	if err := rejectNonfiniteResult([]interface{}{callback, lambda, function}); err != nil || called {
		t.Fatal("walker invoked or inspected an opaque function")
	}
}

func requireFiniteRejection(t *testing.T, value interface{}) {
	t.Helper()
	err := rejectNonfiniteResult(value)
	if err != errFiniteResult || err.Error() != "JSON query result rejected by finite profile" {
		t.Fatal("expected the single fixed redacted profile error")
	}
}
