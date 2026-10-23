package jsonquery

import (
	"math"

	jsonata "uptime-canary/apps/node-go/internal/jsonquery/evaluator"
)

// compatibleNumber is the single whitelisted native compatibility function.
// It is installed only on a newly compiled expression in the guarded child,
// under the standard builtin name/signature. It is pure, accepts no host data
// or callbacks, and performs no I/O or global registration. The signature
// supplies the current context for an omitted argument. This function rejects
// containers itself because the pinned registration API miscounts union arity.
// Every numeric return must pass the finite check, including native numeric
// arguments. This deliberately rejects JS nonfinite intermediates before they
// reach the pinned evaluator's incompatible ordering/$type implementations.
// Errors deliberately contain neither the numeric text nor any argument value.
func compatibleNumber(args []interface{}) (interface{}, error) {
	if len(args) != 1 {
		return nil, &jsonata.JSONataError{Code: "T0410", Index: 1}
	}
	var n float64
	switch v := args[0].(type) {
	case nil:
		return nil, nil // undefined propagates; this is not explicit JSON null.
	case bool:
		if v {
			n = 1
		}
	case string:
		// Computed strings can exceed the request document limit. Bound this
		// native conversion too; the OS cap/deadline remain the hard controls.
		if len(v) > MaxDocumentBytes {
			return nil, &jsonata.JSONataError{Code: "D3030", Index: 1}
		}
		var ok bool
		n, ok = numberText(v)
		if !ok {
			return nil, &jsonata.JSONataError{Code: "D3030", Index: 1}
		}
	case float64:
		n = v
	case float32:
		n = float64(v)
	case int:
		n = float64(v)
	case int8:
		n = float64(v)
	case int16:
		n = float64(v)
	case int32:
		n = float64(v)
	case int64:
		n = float64(v)
	case uint:
		n = float64(v)
	case uint8:
		n = float64(v)
	case uint16:
		n = float64(v)
	case uint32:
		n = float64(v)
	case uint64:
		n = float64(v)
	default:
		return nil, &jsonata.JSONataError{Code: "T0410", Index: 1}
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, &jsonata.JSONataError{Code: "D3030", Index: 1}
	}
	return n, nil
}
