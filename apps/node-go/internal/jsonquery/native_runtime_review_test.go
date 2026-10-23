package jsonquery

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// All expression evaluation in these review cases uses the existing self-exec
// worker and production limits. The controller only constructs request data.
func reviewNativeRuntime(t *testing.T, expression, document, expected, want string) {
	t.Helper()
	r := request(expression, expected)
	r.Document = json.RawMessage(document)
	if err := r.Validate(); err != nil {
		t.Fatal("invalid fixed review request")
	}
	got := Check(context.Background(), r)
	t.Logf("observed=%s want=%s", got.State, want)
	if got.State != want || got.LatencyMs <= 0 {
		t.Fatalf("state=%s want=%s public_message=%q latency=%g", got.State, want, got.Message, got.LatencyMs)
	}
	message := "service check succeeded"
	if want == "down" {
		message = "service check failed"
	}
	if got.Message != message {
		t.Fatal("worker diagnostic did not remain fixed and private")
	}
}

func TestNativeRuntimeReviewProcedures(t *testing.T) {
	supported(t)
	cases := []struct{ name, expression, expected, want string }{
		{"returned_closure", `($maker:=function($n){function($x){$x+$n}};$add:=$maker(2);$add(40))`, `42`, "up"},
		{"partial_lambda", `($add:=function($x,$y){$x+$y};$inc:=$add(?,1);$inc(41))`, `42`, "up"},
		{"composed_procedure", `($f:=function($x){$x+1};$g:=$f~>$f;$g(40))`, `42`, "up"},
		{"procedure_array", `($f:=[function($x){$x},$sum];$count($f))`, `2`, "up"},
		{"tail_call_procedure", `($f:=function($n,$a){$n=0?$a:$f($n-1,$a+1)};$f(20,22))`, `42`, "up"},
		{"regex_callable", `$type(/a/)`, `"function"`, "up"},
		{"eval_returns_closure", `($f:=$eval('function($x){$x+2}');$f(40))`, `42`, "up"},
		{"unexecuted_nonfinite_body_remains_opaque", `($maker:=function(){function(){1e308*10}};$type($maker()))`, `"function"`, "up"},
		{"invoked_nonfinite_body_fails_before_discard", `($maker:=function(){function(){1e308*10}};$f:=$maker();($f();true))`, `true`, "down"},
		{"finite_worker_after_procedure_error", `40+2`, `42`, "up"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reviewNativeRuntime(t, tc.expression, `{}`, tc.expected, tc.want)
		})
	}
}

func TestNativeRuntimeReviewNumericStrings(t *testing.T) {
	supported(t)
	cases := []struct{ name, expression, document, expected, want string }{
		{"integer_tie_down", `$number($)`, `"9007199254740993"`, `9007199254740992`, "up"},
		{"integer_tie_up", `$number($)`, `"9007199254740995"`, `9007199254740996`, "up"},
		{"rounds_to_max_finite", `$number($)`, `"1.7976931348623158e308"`, `1.7976931348623157e308`, "up"},
		{"decimal_overflow_before_discard", `($number($);true)`, `"1.7976931348623159e308"`, `true`, "down"},
		{"below_half_min_subnormal", `$number($)`, `"2.4703282292062327e-324"`, `0`, "up"},
		{"above_half_min_subnormal", `$number($)`, `"2.4703282292062328e-324"`, `5e-324`, "up"},
		{"negative_underflow_zero", `$number($)`, `"-1e-999"`, `0`, "up"},
		{"negative_zero_division_before_discard", `(1/$number($);true)`, `"-1e-999"`, `true`, "down"},
		{"partial_decimal_conversion", `($f:=$number(?);$f($))`, `"9007199254740995"`, `9007199254740996`, "up"},
		{"alias_decimal_conversion", `($f:=$number;$f($))`, `"1.25e+2"`, `125`, "up"},
		{"eval_decimal_conversion", `$eval('$number($)')`, `"1.25e+2"`, `125`, "up"},
		{"decimal_leading_space_still_invalid", `($number($);true)`, `" 2"`, `true`, "down"},
		{"decimal_trailing_space_still_invalid", `($number($);true)`, `"2 "`, `true`, "down"},
		{"valid_unicode_space_octal", `$number($)`, `"\u00a00o10"`, `8`, "up"},
		// The upstream octal alternative permits leading whitespace. BigInt
		// conversion trims it. These are valid numeric strings on the exact
		// UTF-8 byte boundary, so grammar rejection cannot mask the byte cap.
		{"two_byte_space_exact_numeric_cap", `$number($pad('0o10',-524290,'\u00a0'))`, `{}`, `8`, "up"},
		{"two_byte_space_above_numeric_cap", `($number($pad('0o10',-524291,'\u00a0'));true)`, `{}`, `true`, "down"},
		{"three_byte_space_exact_numeric_cap", `$number($pad('0o10',-349528,'\u2000'))`, `{}`, `8`, "up"},
		{"three_byte_space_above_numeric_cap", `($number($pad('0o10',-349529,'\u2000'));true)`, `{}`, `true`, "down"},
		{"finite_worker_after_numeric_errors", `$number('42')`, `{}`, `42`, "up"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reviewNativeRuntime(t, tc.expression, tc.document, tc.expected, tc.want)
		})
	}
}

func TestNativeRuntimeReviewBoundaryIsolation(t *testing.T) {
	supported(t)
	document := `{"values":[` + strings.Repeat("0,", 32765) + `0]}`
	// 1 object + 2 * (1 array + 32766 leaves) + 1 scalar = 65536.
	reviewNativeRuntime(t, `($a:=values;$count($keys({'left':$a,'right':$a,'tail':0})))`, document, `3`, "up")
	reviewNativeRuntime(t, `($a:=values;$count($keys({'left':$a,'right':$a,'tail':0,'extra':0})))`, document, `4`, "down")
	// Budgets apply afresh at each boundary; they are not cumulative per query.
	document = `{"values":[` + strings.Repeat("0,", 65534) + `0]}`
	reviewNativeRuntime(t, `($count(values);$count(values))`, document, `65535`, "up")
	reviewNativeRuntime(t, `40+2`, `{}`, `42`, "up")
}
