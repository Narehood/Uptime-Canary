package jsonquery

import (
	"context"
	"strings"
	"testing"
)

// Independent review regressions. Every expression runs in the production
// guarded self-exec worker selected by the existing TestMain.
func TestProfileReviewMatcherContinuation(t *testing.T) {
	supported(t)
	for _, tc := range []struct {
		name, continuation, want string
	}{
		{"finite exhausted continuation", "missing", "up"},
		{"arithmetic nonfinite continuation", "$boolean(1e308*10)", "down"},
		{"aggregate nonfinite continuation", "$type($sum([1e308,1e308]))", "down"},
		{"explicit continuation error", "$error('review-private-marker')", "down"},
	} {
		for _, converted := range []bool{false, true} {
			body, stage := tc.continuation, "first next/"
			matchCount, splitCount, replacement := "1", "2", `"xb"`
			if converted {
				body = `{"match":"b","start":1,"end":2,"groups":[],"next":function(){` + body + `}}`
				stage, matchCount, splitCount, replacement = "converted next/", "2", "3", `"xx"`
			}
			matcher := `function($s){ {"match":"a","start":0,"end":1,"groups":[],"next":function(){` + body + `}} }`
			for _, consumer := range []struct{ name, expression, expected string }{
				{"match", `$count($match("ab",` + matcher + `))`, matchCount},
				{"split", `$count($split("ab",` + matcher + `))`, splitCount},
				{"replace", `$replace("ab",` + matcher + `,"x")`, replacement},
			} {
				t.Run(stage+consumer.name+"/"+tc.name, func(t *testing.T) {
					got := Check(context.Background(), request(consumer.expression, consumer.expected))
					if strings.Contains(got.Message, "review-private-marker") {
						t.Fatal("worker leaked a private callback error")
					}
					if got.State != tc.want || got.LatencyMs <= 0 {
						t.Fatalf("continuation state=%s want=%s message=%q latency=%g", got.State, tc.want, got.Message, got.LatencyMs)
					}
				})
			}
		}
	}
}

func TestProfileReviewBoundaryControls(t *testing.T) {
	supported(t)
	for _, tc := range []struct {
		name, expression, expected, want string
	}{
		{"unexecuted branch", `false ? 1e308*10 : 7`, "7", "up"},
		{"unexecuted closure", `($f:=function(){1e308*10}; $type($f))`, `"function"`, "up"},
		{"unexecuted short circuit", `false and $boolean(1e308*10)`, "false", "up"},
		{"finite composition", `($f:=$sum ~> $string; $f([1,2]))`, `"3"`, "up"},
		{"finite partial native sort", `$sort([2,1],$power(?,?))[0]`, "1", "up"},
		{"finite nested shadow", `($number:=function($x){42}; $eval("$number('+2')"))`, "42", "up"},
		{"fresh builtin after shadow", `$number('+2')`, "2", "down"},
		{"finite nested alias", `($n:=$number; $eval("$n('0x10')"))`, "16", "up"},
		{"finite predicate group", `$lookup(($sum([1,2,3])[true]){'k':$},'k')`, "6", "up"},
		{"overflow composition", `($f:=$sum ~> $type; $f([1e308,1e308]))`, `"number"`, "down"},
		{"overflow partial HOF", `$count($filter([[1e308,1e308]],$sum(?)))`, "1", "down"},
		{"overflow single HOF", `$exists($single([[1e308,1e308]],$sum))`, "true", "down"},
		{"overflow sift HOF", `$exists($sift({'a':[1e308,1e308]},$sum))`, "true", "down"},
		{"overflow reduction", `$reduce([1e308,1e308],function($a,$b){$a+$b}) > 0`, "true", "down"},
		{"overflow nested eval alias", `($f:=$sum; $type($eval('$f([1e308,1e308])')))`, `"number"`, "down"},
		{"overflow unary", `$type(-(1e308*10))`, `"number"`, "down"},
		{"overflow group value", `$exists([{'k':'a','v':1e308},{'k':'a','v':1e308}]{k:$sum(v)})`, "true", "down"},
		{"overflow predicate discarded", `$exists((1e308*10)[false])`, "false", "down"},
		{"overflow ordered path", `$count([1,2]^(1e308*10))`, "2", "down"},
		{"overflow lambda sort", `$count($sort([1,2],function($a,$b){1e308*10}))`, "2", "down"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Check(context.Background(), request(tc.expression, tc.expected))
			if got.State != tc.want || got.LatencyMs <= 0 {
				t.Fatalf("boundary state=%s want=%s message=%q latency=%g", got.State, tc.want, got.Message, got.LatencyMs)
			}
		})
	}
}

func TestProfileReviewNativeSort(t *testing.T) {
	supported(t)
	for _, tc := range []struct {
		name, expression, expected, want string
	}{
		{"finite lambda comparator", `$sort([[3],[1]],function($a,$b){$sum($a)>$sum($b)})[0][0]`, "1", "up"},
		{"finite native comparator", `$sort(["ab","a"],$contains)[0]`, `"a"`, "up"},
		{"finite native comparator false UP", `$sort(["ab","a"],$contains)[0]`, `"ab"`, "down"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Check(context.Background(), request(tc.expression, tc.expected))
			if got.State != tc.want || got.LatencyMs <= 0 {
				t.Fatalf("sort state=%s want=%s message=%q latency=%g", got.State, tc.want, got.Message, got.LatencyMs)
			}
		})
	}
}
