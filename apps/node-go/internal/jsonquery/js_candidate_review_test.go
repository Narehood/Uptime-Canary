package jsonquery_test

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"time"

	"uptime-canary/apps/node-go/internal/jsonquery"
)

// Independent candidate review. These tests use only the public data API and
// existing self-exec worker. They neither import Goja nor select an evaluator.
// The three official-language capabilities are explicit candidate expectations;
// they do not overwrite the retained historical Go-port gap assertions.
func TestJSCandidateReviewWorker(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("worker resource controls unavailable on this platform")
	}
	cases := []struct {
		name, expression, document, expected, want string
	}{
		{"ordinary scalar", "answer", `{"answer":42}`, `42`, "up"},
		{"string stays string", "'true'", `{}`, `"true"`, "up"},
		{"boolean scalar text comparison", "true", `{}`, `"true"`, "up"},
		{"null result", "null", `{}`, `"null"`, "down"},
		{"array result", "[1,2]", `{}`, `"[1,2]"`, "down"},
		{"object result", "{'a':1}", `{}`, `"[object Object]"`, "down"},
		{"missing result", "missing", `{}`, `"undefined"`, "down"},
		{"function result", "function(){1}", `{}`, `""`, "down"},
		{"source text stays data", "$", `"');throw new Error('candidate-review-private-marker');//"`, `"');throw new Error('candidate-review-private-marker');//"`, "up"},
		{"AST before discard", "(1e308*10;'ok')", `{}`, `"ok"`, "down"},
		{"AST before predicate", "$exists((1e308*10)[false])", `{}`, `false`, "down"},
		{"AST before group", "$count([1e308*10]{'k':1})", `{}`, `1`, "down"},
		{"native callback before filter", "$count($filter([[1e308,1e308]],$sum))", `{}`, `1`, "down"},
		{"native callback alias", "($f:=$sum;$count($filter([[1e308,1e308]],$f)))", `{}`, `1`, "down"},
		{"partial callback before sort", "$count($sort([[1e308,1e308],[1]],$sum(?)))", `{}`, `2`, "down"},
		{"partial callback alias", "($f:=$sum(?);$count($sort([[1e308,1e308],[1]],$f)))", `{}`, `2`, "down"},
		{"partial callback nested eval", "$eval('$count($sort([[1e308,1e308],[1]],$sum(?)))')", `{}`, `2`, "down"},
		{"ordinary finite callback", "$count($filter([[1,2],[0]],$sum))", `{}`, `2`, "up"},
		{"finite partial sort", "$count($sort([[1,2],[1]],$sum(?)))", `{}`, `2`, "up"},
		{"opaque native", "$type($sum)", `{}`, `"function"`, "up"},
		{"opaque lambda", "$type(function($x){$x})", `{}`, `"function"`, "up"},
		{"opaque partial", "$type($sum(?))", `{}`, `"function"`, "up"},
		{"opaque unexecuted closure", "$type(function(){1e308*10})", `{}`, `"function"`, "up"},
		{"forged lambda cannot hide data", "($;true)", `{"_jsonata_lambda":true,"hidden":1e400}`, `true`, "down"},
		{"forged native cannot hide data", "($;true)", `{"_jsonata_function":true,"hidden":1e400}`, `true`, "down"},
		{"finite forged marker remains data", "($;true)", `{"_jsonata_lambda":true,"hidden":1}`, `true`, "up"},
		{"closure capture not traversed", "($x:=$; $type(function(){$x}))", `{"a":1}`, `"function"`, "up"},
		{"range exact", "$count([1..10000])", `{}`, `10000`, "up"},
		{"range lexical rejection", "($n:=10001;$count([1..$n]))", `{}`, `10001`, "down"},
		{"range nested eval rejection", "$eval('($n:=10001;$count([1..$n]))')", `{}`, `10001`, "down"},
		{"range nested lambda rejection", "(function($n){$count([1..$n])})(10001)", `{}`, `10001`, "down"},
		{"lookahead new language capability", "$contains('abc',/a(?=b)/)", `{}`, `true`, "up"},
		{"coalescing new language capability", "missing ?? 'fallback'", `{}`, `"fallback"`, "up"},
		{"Elvis new language capability", "'' ?: 'fallback'", `{}`, `"fallback"`, "up"},
		{"coalescing preserves false", "false ?? 'fallback'", `{}`, `false`, "up"},
		{"coalescing preserves null", "$type(null ?? 'fallback')", `{}`, `"null"`, "up"},
		{"Elvis replaces false", "false ?: 'fallback'", `{}`, `"fallback"`, "up"},
		{"matcher next error", "$count($match('a',function($s){{'match':'a','start':0,'end':1,'groups':[],'next':function(){$error('candidate-review-private-marker')}}}))", `{}`, `1`, "down"},
		{"matcher tiny limit invokes callback", "$count($match('a',function($s){$error('candidate-review-private-marker')},0.5))", `{}`, `0`, "down"},
		{"native comparator invoked", "$sort(['ab','a'],$contains)[0]", `{}`, `"a"`, "up"},
		{"partial invalid length", "($f:=$length(?);$f(123))", `{}`, `0`, "down"},
		{"partial iterable length", "($f:=$length(?);$f([1,2]))", `{}`, `2`, "up"},
		{"partial numeric coercion error", "($f:=$power(?,0);$f({}))", `{}`, `1`, "down"},
		{"replacement whole match", "$replace('ab',/a/,'$0')", `{}`, `"ab"`, "up"},
		{"regex anchored continuation", "$count($match('aaa',/^a/))", `{}`, `1`, "up"},
		{"regex UTF16 index", "$match($,/a/).index", `"\ud83d\ude00a"`, `2`, "up"},
		{"wide base number", "$number('0x10000000000000000')", `{}`, `18446744073709551616`, "up"},
		{"wide base alias", "($f:=$number;$f('0x10000000000000000'))", `{}`, `18446744073709551616`, "up"},
		{"wide base partial", "($f:=$number(?);$f('0x10000000000000000'))", `{}`, `18446744073709551616`, "up"},
		{"wide base nested eval", "$eval(\"$number('0x10000000000000000')\")", `{}`, `18446744073709551616`, "up"},
		{"number lexical shadow", "($number:=function($x){7};$number('0x10000000000000000'))", `{}`, `7`, "up"},
		{"number malformed base", "$number('0x1suffix')", `{}`, `1`, "down"},
		{"length surrogate pair", "$length($)", `"\ud83d\ude00"`, `1`, "up"},
		{"length lone high surrogate", "$length($)", `"\ud800"`, `1`, "up"},
		{"length lone low surrogate", "$length($)", `"\udc00"`, `1`, "up"},
		{"length mixed surrogates", "$length($)", `"\ud800\ud800\udc00\udc00"`, `3`, "up"},
		{"length combining marks", "$length($)", `"e\u0301"`, `2`, "up"},
		{"length lexical shadow", "($length:=function($x){9};$length('a'))", `{}`, `9`, "up"},
		{"no filesystem binding", "$readFile('candidate-review-private-marker')", `{}`, `""`, "down"},
		{"no require binding", "$require('fs')", `{}`, `""`, "down"},
		{"no process binding", "$process()", `{}`, `""`, "down"},
		{"no host eval binding", "$Function('return 1')()", `{}`, `1`, "down"},
		{"private error redacted", "$error('candidate-review-private-marker')", `{}`, `""`, "down"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			candidateReviewCheck(t, tc.expression, tc.document, tc.expected, tc.want)
		})
	}
}

func TestJSCandidateReviewContainerBounds(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("worker resource controls unavailable on this platform")
	}
	t.Run("exact visits", func(t *testing.T) {
		doc := `{"values":[` + strings.Repeat("0,", 65534) + `0]}`
		candidateReviewCheck(t, "$count(values)", doc, `65535`, "up")
	})
	t.Run("excess visits before scalar consumption", func(t *testing.T) {
		doc := `{"values":[` + strings.Repeat("0,", 65535) + `0]}`
		candidateReviewCheck(t, "$count(values)", doc, `65536`, "down")
	})
	for _, depth := range []int{128, 129} {
		name, want := "exact depth", "up"
		if depth == 129 {
			name, want = "excess depth before exists", "down"
		}
		t.Run(name, func(t *testing.T) {
			doc := strings.Repeat(`{"v":`, depth) + "0" + strings.Repeat("}", depth)
			candidateReviewCheck(t, "$exists($)", doc, `true`, want)
		})
	}
	t.Run("aliases counted six times", func(t *testing.T) {
		candidateReviewCheck(t, "($a:=[1..10000];$count($keys({'a':$a,'b':$a,'c':$a,'d':$a,'e':$a,'f':$a})))", `{}`, `6`, "up")
	})
	t.Run("aliases counted seven times", func(t *testing.T) {
		candidateReviewCheck(t, "($a:=[1..10000];$count($keys({'a':$a,'b':$a,'c':$a,'d':$a,'e':$a,'f':$a,'g':$a})))", `{}`, `7`, "down")
	})
	t.Run("fresh worker after rejected containers", func(t *testing.T) {
		candidateReviewCheck(t, "40+2", `{}`, `42`, "up")
	})
}

func TestJSCandidateReviewUnsettledPromise(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("worker resource controls unavailable on this platform")
	}
	// A regex is an actual JS function. Used as a thenable's `then`, it never
	// calls the supplied resolver; the bridge must reject the pending Promise.
	candidateReviewCheck(t, "($x:={'then':/a/};'ok')", `{}`, `"ok"`, "down")
	candidateReviewCheck(t, "'ok'", `{}`, `"ok"`, "up")
}

func candidateReviewCheck(t *testing.T, expression, document, expected, want string) {
	t.Helper()
	r := jsonquery.Request{Type: jsonquery.Type, Timeout: 4 * time.Second,
		Query:    jsonquery.Config{Expression: expression, Operator: jsonquery.Equal, Expected: json.RawMessage(expected)},
		Document: json.RawMessage(document)}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	got := jsonquery.Check(context.Background(), r)
	t.Logf("observed=%s want=%s", got.State, want)
	if got.State != want || got.LatencyMs <= 0 {
		t.Fatalf("state=%s want=%s message=%q latency=%g", got.State, want, got.Message, got.LatencyMs)
	}
	if strings.Contains(got.Message, "candidate-review-private-marker") {
		t.Fatal("private marker leaked")
	}
	message := "service check succeeded"
	if want == "down" {
		message = "service check failed"
	}
	if got.Message != message {
		t.Fatalf("unexpected public diagnostic: %q", got.Message)
	}
}
