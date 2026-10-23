package jsonata

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// Preserve fractional limits until the consumer compares its counter. Native
// String.split's separate ToUint32 conversion stays in its string-only branch.
func matcherLimit(args []interface{}, index int) (*float64, error) {
	if len(args) <= index || args[index] == nil {
		return nil, nil
	}
	var n float64
	switch value := args[index].(type) {
	case float64:
		n = value
	case int:
		n = float64(value)
	default:
		return nil, createJSONataError("T0410", nil)
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, errFiniteResult
	}
	return &n, nil
}

// This fixed internal regex continuation is an observable DSL function. It is
// never registered as a binding and ignores the reference's undefined argument.
func matcherNextFunction(next func() (interface{}, error)) *Function {
	return &Function{
		JSONataFunction: true,
		matcherCallable: true,
		Implementation: JSONataFunc(func([]interface{}) (interface{}, error) {
			return next()
		}),
	}
}

// Reference replacement concatenates group values; undefined positions are
// empty, while JSON scalars and containers retain JS primitive conversion.
// Group trees have already passed the bounded finite-result walker.
func matchGroupText(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case *JSONataNull, JSONataNull:
		return "null"
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		if v == 0 {
			return "0"
		}
		b, _ := json.Marshal(v)
		return string(b)
	case int:
		return strconv.Itoa(v)
	case []interface{}:
		parts := make([]string, len(v))
		for i, item := range v {
			if _, null := item.(*JSONataNull); !null {
				parts[i] = matchGroupText(item)
			}
		}
		return strings.Join(parts, ",")
	case *JSONataArray:
		return matchGroupText(v.Data)
	default:
		return "[object Object]"
	}
}
