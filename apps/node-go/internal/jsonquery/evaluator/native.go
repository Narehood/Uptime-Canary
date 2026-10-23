package jsonata

import (
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"
)

// This fixed internal dispatch lives on the immutable native definition header.
// Indirection also avoids an initialization cycle in upstream's builtin table.
func callNativeFunction(function *Function, args []interface{}) (interface{}, error) {
	native := *function
	if native.partialNative {
		native.signature = nil
	}
	focus := native.focus
	if focus == nil {
		focus = &Focus{}
	}
	return applyFunction(&native, args, focus.input, focus.environment)
}

// A native partial retains its full function header and explicit bindings.
// Undefined is a value at a known position, never an end-of-arguments marker.
// No generated source, closure inspection, reflected code or host binding is used.
type nativePartial struct {
	function   *Function
	bound      []interface{}
	positions  []int
	parameters []string
	input      interface{}
}

func newNativePartial(fn *Function, args []interface{}, input interface{}, environment *Frame) *Lambda {
	if fn.nativeParameters != nil && len(args) > len(fn.nativeParameters) {
		args = args[:len(fn.nativeParameters)] // reference native declaration ignores extra bound arguments
	}
	native := *fn
	native.partialNative = true
	partial := &nativePartial{function: &native, bound: append([]interface{}(nil), args...), input: input}
	// The reference's partial $eval frame exposes its actual native parameters.
	// Do not introduce generated locals into the environment $eval can inspect.
	if fn.nativeParameters != nil {
		environment = createFrame(environment)
		for i, name := range fn.nativeParameters {
			var value interface{}
			if i < len(args) {
				value = args[i]
			}
			if node, ok := value.(*ASTNode); ok && node.Type == "operator" && node.Value == "?" {
				value = nil
			}
			environment.bind(name, value)
		}
	}
	arguments := []interface{}{}
	for i, arg := range args {
		if node, ok := arg.(*ASTNode); ok && node.Type == "operator" && node.Value == "?" {
			name := "__native_partial_" + strconv.Itoa(len(partial.positions))
			if i < len(fn.nativeParameters) {
				name = fn.nativeParameters[i]
			}
			partial.positions = append(partial.positions, i)
			partial.parameters = append(partial.parameters, name)
			arguments = append(arguments, &ASTNode{Type: "variable", Value: name})
		}
	}
	procedure := &Lambda{
		JSONataLambda: true, input: input, environment: environment,
		arguments: arguments, body: partial, Arity: len(arguments),
	}
	procedure.apply = func(_ interface{}, args []interface{}) (interface{}, error) {
		return applyFunction(procedure, args, input, environment)
	}
	return procedure
}

func (p *nativePartial) invoke(environment *Frame) (interface{}, error) {
	args := append([]interface{}(nil), p.bound...)
	for i, position := range p.positions {
		args[position] = environment.lookup(p.parameters[i])
	}
	native := *p.function
	native.focus = &Focus{input: p.input, environment: environment}
	return functions.invokeFunction(&native, args)
}

// Pure JS primitive conversion used by the retained Math.pow implementation.
// Normal native calls still validate their signature first; native partials do
// not in the pinned reference. Numeric strings must not become false safe-DOWNs.
func nativeJSNumber(value interface{}) float64 {
	switch v := value.(type) {
	case nil:
		return math.NaN()
	case *JSONataNull, JSONataNull:
		return 0
	case bool:
		if v {
			return 1
		}
		return 0
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		s := strings.TrimFunc(v, func(r rune) bool { return unicode.IsSpace(r) && r != 0x85 || r == '\ufeff' })
		if s == "" {
			return 0
		}
		if s == "Infinity" || s == "+Infinity" {
			return math.Inf(1)
		}
		if s == "-Infinity" {
			return math.Inf(-1)
		}
		if len(s) > 2 && s[0] == '0' && strings.ContainsRune("xXoObB", rune(s[1])) {
			base := 16
			if s[1] == 'o' || s[1] == 'O' {
				base = 8
			}
			if s[1] == 'b' || s[1] == 'B' {
				base = 2
			}
			for _, digit := range s[2:] {
				if !(digit >= '0' && digit <= '9' && int(digit-'0') < base || base == 16 && (digit >= 'a' && digit <= 'f' || digit >= 'A' && digit <= 'F')) {
					return math.NaN()
				}
			}
			if n, ok := new(big.Int).SetString(s, 0); ok {
				f, _ := new(big.Float).SetInt(n).Float64()
				return f
			}
			return math.NaN()
		}
		// ParseFloat additionally accepts Inf and hex floats, which JS rejects.
		if strings.Contains(s, "Inf") || strings.ContainsAny(s, "xX_pP") {
			return math.NaN()
		}
		n, err := strconv.ParseFloat(s, 64)
		if err != nil {
			var parseError *strconv.NumError
			if !errors.As(err, &parseError) || parseError.Err != strconv.ErrRange {
				return math.NaN()
			}
		}
		return n
	case []interface{}:
		return nativeJSNumber(matchGroupText(v))
	case *JSONataArray:
		return nativeJSNumber(matchGroupText(v))
	default:
		return math.NaN()
	}
}

// JS sum uses addition rather than silently dropping nonnumeric array members.
// These are inert JSON primitives/containers; no valueOf/toString callback runs.
func nativeJSAdd(left, right interface{}) interface{} {
	primitive := func(value interface{}) interface{} {
		switch value.(type) {
		case map[string]interface{}, []interface{}, *JSONataArray, *Function, *Lambda:
			return matchGroupText(value)
		default:
			return value
		}
	}
	a, b := primitive(left), primitive(right)
	_, aString := a.(string)
	_, bString := b.(string)
	if aString || bString {
		text := func(value interface{}) string {
			if value == nil {
				return "undefined"
			}
			if n, ok := value.(float64); ok {
				if math.IsNaN(n) {
					return "NaN"
				}
				if math.IsInf(n, 1) {
					return "Infinity"
				}
				if math.IsInf(n, -1) {
					return "-Infinity"
				}
			}
			return matchGroupText(value)
		}
		return text(a) + text(b)
	}
	return nativeJSNumber(a) + nativeJSNumber(b)
}
