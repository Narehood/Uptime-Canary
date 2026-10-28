package canary

import (
	"math"
	"reflect"
)

// Unchanged records are compared repeatedly while holding the state mutex.
// Avoid allocating two JSON encodings for these common positive comparisons.
// A false result always falls back to JSON: nil/empty collections, independently
// decoded pointers and JSON's string/number normalization retain their semantics.
func sameRecord(a, b any) bool {
	switch left := a.(type) {
	case attempt:
		right, ok := b.(attempt)
		return ok && left == right
	case NotificationIntent:
		right, ok := b.(NotificationIntent)
		return ok && left == right
	case Sample:
		right, ok := b.(Sample)
		// Go equality collapses signed zero; JSON preserves its sign.
		return ok && left == right && math.Float64bits(left.LatencyMs) == math.Float64bits(right.LatencyMs)
	case Incident:
		right, ok := b.(Incident)
		if !ok {
			return false
		}
		// Private proof is deliberately outside JSON equality. Callers which
		// need it still compare the uncertainty set separately.
		left.uncertainties, right.uncertainties = nil, nil
		return reflect.DeepEqual(left, right)
	}
	return false
}
