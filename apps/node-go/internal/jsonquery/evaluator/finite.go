package jsonata

import (
	"errors"
	"math"
)

const (
	finiteResultMaxDepth  = 128
	finiteResultMaxValues = 65536
)

var errFiniteResult = errors.New("JSON query result rejected by finite profile")

// rejectNonfiniteResult implements the local finite-intermediate profile. Each
// result has its own depth/value budget. Only JSON-shaped containers are walked;
// functions, lexical environments and other opaque engine values are not inspected.
// No reflection, pointer cache, host callbacks or user bindings are involved.
// Cycles exhaust the depth budget (at most 129 edges); repeated references count
// again. This bounds cyclic/shared graphs without skipping data after alias mutation.
func rejectNonfiniteResult(value interface{}) error {
	remaining := finiteResultMaxValues
	return walkFiniteResult(value, 0, &remaining)
}

func walkFiniteResult(value interface{}, depth int, remaining *int) error {
	if depth > finiteResultMaxDepth || *remaining == 0 {
		return errFiniteResult
	}
	*remaining -= 1
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return errFiniteResult
		}
	case float32:
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return errFiniteResult
		}
	case map[string]interface{}:
		if len(v) > *remaining {
			return errFiniteResult
		}
		for _, child := range v {
			if err := walkFiniteResult(child, depth+1, remaining); err != nil {
				return err
			}
		}
	case []interface{}:
		return walkFiniteItems(v, depth, remaining)
	case *JSONataArray:
		if v != nil {
			return walkFiniteItems(v.Data, depth, remaining)
		}
	case JSONataArray:
		return walkFiniteItems(v.Data, depth, remaining)
	}
	return nil
}

func walkFiniteItems(items []interface{}, depth int, remaining *int) error {
	if len(items) > *remaining {
		return errFiniteResult
	}
	for _, child := range items {
		if err := walkFiniteResult(child, depth+1, remaining); err != nil {
			return err
		}
	}
	return nil
}
