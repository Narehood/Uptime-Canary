package jsonquery

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	jsonata "uptime-canary/apps/node-go/internal/jsonquery/evaluator"

	"uptime-canary/apps/node-go/internal/probeworker/process"
)

const (
	MaxDepth          = 128
	MaxRange          = 10000
	MaxEvaluationTime = time.Second
)

// Check executes only through the fixed two-slot process runner. No evaluator
// compilation or execution happens in the parent and there is no fallback.
// The executable must route Type to WorkerHandler before ordinary startup.
func Check(ctx context.Context, r Request) process.Result {
	if r.Validate() != nil {
		return failed("invalid JSON query configuration")
	}
	return process.RunConfig(ctx, r.Timeout, r)
}

// WorkerHandler returns fresh request state for one dispatch. A root dynamic
// dispatcher may delegate its Validate and Check hooks after selecting Type.
// Only process.Dispatch may invoke this handler, after installing OS limits.
func WorkerHandler() process.Handler {
	var r Request
	valid := false
	return process.Handler{
		Validate: func(b []byte) error {
			valid = false
			r = Request{}
			if len(b) > 8<<20 {
				return errConfig
			}
			d := json.NewDecoder(bytes.NewReader(b))
			d.DisallowUnknownFields()
			if d.Decode(&r) != nil || !json.Valid(b) || r.Validate() != nil {
				return errConfig
			}
			valid = true
			return nil
		},
		Check: func(ctx context.Context) process.Result {
			if !valid {
				return failed("invalid JSON query configuration")
			}
			return CheckInWorker(ctx, r)
		},
	}
}

// CheckInWorker is exclusively the child-side execution hook. Never call it in
// a server, under Node.mu, or as a fallback for unavailable resource controls.
// Evaluator errors can contain credentials from the expression/document and
// must never be formatted, logged, returned or included in diagnostics.
func CheckInWorker(ctx context.Context, r Request) (result process.Result) {
	result = failed("JSON query evaluation failed")
	defer func() {
		if recover() != nil {
			result = failed("JSON query evaluation failed")
		}
	}()
	if r.Validate() != nil {
		return failed("invalid JSON query configuration")
	}
	if ctx.Err() != nil {
		return failed("JSON query interrupted")
	}
	e, err := jsonata.Compile(r.Query.Expression, false)
	if err != nil {
		return failed("JSON query compilation failed")
	}
	e.SetMaxDepth(MaxDepth)
	e.SetMaxRange(MaxRange)
	// The only native extension is this fixed pure compatibility implementation
	// of the standard builtin. It is local to this expression; nested $eval and
	// lexical scopes inherit it normally. No user-supplied binding is accepted.
	// The pinned registration code counts (nsb) as arity three rather than one,
	// breaking higher-order calls. x preserves one-argument/context metadata;
	// compatibleNumber itself strictly accepts only number/string/bool/undefined.
	if e.RegisterFunction("number", compatibleNumber, "<x-:n>") != nil {
		return failed("JSON query initialization failed")
	}
	limit := MaxEvaluationTime
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < limit {
		limit = time.Until(deadline)
	}
	if limit <= 0 || ctx.Err() != nil {
		return failed("JSON query interrupted")
	}
	// Advisory checks supplement the parent deadline, kill/reap and kernel cap.
	// They do not bound parsing, every builtin, or nested $eval compilation.
	e.SetMaxTime(max(1, int(limit/time.Millisecond)))
	output, err := e.Evaluate(r.Document, nil)
	if err != nil || ctx.Err() != nil {
		return failed("JSON query evaluation failed")
	}
	if len(output) > MaxDocumentBytes {
		return failed("JSON query result exceeds limit")
	}
	actual, ok := scalar(output)
	if !ok {
		return failed("JSON query must return a non-null scalar")
	}
	expected, _ := scalar(r.Query.Expected) // validated above
	matched, valid := compare(actual, r.Query.Operator, expected)
	if !valid {
		return failed("JSON query numeric comparison failed")
	}
	if !matched {
		return failed("JSON query condition did not match")
	}
	return process.Result{State: "up", Message: "JSON query condition matched"}
}

func failed(message string) process.Result {
	return process.Result{State: "down", Message: message}
}

func compare(actual any, op Operator, expected any) (bool, bool) {
	a, b := scalarText(actual), scalarText(expected)
	switch op {
	case Equal:
		return a == b, true
	case NotEqual:
		return a != b, true
	case Contains:
		return strings.Contains(a, b), true
	}
	x, okX := numericText(a)
	y, okY := numericText(b)
	if !okX || !okY {
		return false, false
	}
	switch op {
	case Greater:
		return x > y, true
	case GreaterEqual:
		return x >= y, true
	case Less:
		return x < y, true
	case LessEqual:
		return x <= y, true
	default:
		return false, false
	}
}

func scalarText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		if x == 0 {
			return "0" // JS Number.toString normalizes negative zero.
		}
		// encoding/json uses ECMAScript's fixed/exponent thresholds and removes
		// the leading exponent zero, unlike strconv's default g formatting.
		b, _ := json.Marshal(x)
		return string(b)
	default:
		return ""
	}
}

var decimalText = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?([Ee][-+]?[0-9]+)?$`)
var baseText = regexp.MustCompile(`^0[xX][0-9A-Fa-f]+|0[oO][0-7]+|0[bB][0-1]+$`)

// numericText follows the reference $number(string) grammar, not ParseFloat's
// wider grammar or JavaScript's Number coercion of whitespace/empty strings.
// Booleans have already become "true"/"false", which Kuma also rejects here.
func numericText(s string) (float64, bool) {
	n, ok := numberText(s)
	return n, ok && !math.IsInf(n, 0) && !math.IsNaN(n)
}

// numberText implements the pinned JS builtin's string branch. Its base-prefix
// regexp also accepts malformed numeric strings: Number(string) then returns
// NaN, or Infinity for sufficiently large valid base-prefixed integers. Both
// callers reject these internal conversion results: numericText enforces the
// outer comparator's finite boundary, and compatibleNumber deliberately fails
// closed before any nonfinite value can escape the compatibility builtin.
func numberText(s string) (float64, bool) {
	if decimalText.MatchString(s) {
		n, err := strconv.ParseFloat(s, 64)
		return n, (err == nil || n == 0) && !math.IsInf(n, 0) && !math.IsNaN(n)
	}
	// Preserve the reference's asymmetric base-prefix regexp before applying
	// Number(string)'s surrounding whitespace handling. In particular it allows
	// leading whitespace for octal/binary but not hexadecimal strings.
	if !baseText.MatchString(s) {
		return 0, false
	}
	s = strings.TrimFunc(s, ecmaSpace)
	if len(s) < 3 || s[0] != '0' {
		return math.NaN(), true
	}
	base := 0
	switch s[1] {
	case 'x', 'X':
		base = 16
	case 'o', 'O':
		base = 8
	case 'b', 'B':
		base = 2
	default:
		return math.NaN(), true
	}
	for _, c := range s[2:] {
		if !(c >= '0' && c <= '9' && int(c-'0') < base || base == 16 && (c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F')) {
			return math.NaN(), true
		}
	}
	i, ok := new(big.Int).SetString(s[2:], base)
	if !ok {
		return math.NaN(), true
	}
	n, _ := new(big.Float).SetInt(i).Float64()
	return n, true
}

func ecmaSpace(r rune) bool {
	return r >= 0x2000 && r <= 0x200a || strings.ContainsRune("\t\n\v\f\r \u00a0\u1680\u2028\u2029\u202f\u205f\u3000\ufeff", r)
}
