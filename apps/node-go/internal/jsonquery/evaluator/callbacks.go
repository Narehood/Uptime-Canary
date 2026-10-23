package jsonata

// Matcher continuations use .apply in the pinned reference, rather than the
// general expression invocation path. Raw native/partial objects lack that JS
// method; normal DSL lambdas and expression argument wrappers have it. Track
// only that distinction, without looking inside captured state or function code.
func matcherContinuationCallable(value interface{}) bool {
	switch fn := value.(type) {
	case *Lambda:
		return fn != nil && fn.matcherCallable
	case *Function:
		return fn != nil && fn.matcherCallable
	case func() (interface{}, error):
		return true // fixed native regex continuation
	default:
		return isFunction(value)
	}
}

// comparatorTruthy follows the host JS conditional used by reference sort,
// rather than JSONata's $boolean builtin: empty arrays/objects are truthy, null
// is false. invokeFunction has already rejected nonfinite returned numbers.
// Opaque values are never formatted, reflected, traversed or invoked here.
func comparatorTruthy(value interface{}) bool {
	switch v := value.(type) {
	case nil, *JSONataNull, JSONataNull:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0
	case float32:
		return v != 0
	case int:
		return v != 0
	case int8:
		return v != 0
	case int16:
		return v != 0
	case int32:
		return v != 0
	case int64:
		return v != 0
	case uint:
		return v != 0
	case uint8:
		return v != 0
	case uint16:
		return v != 0
	case uint32:
		return v != 0
	case uint64:
		return v != 0
	default:
		return true
	}
}
