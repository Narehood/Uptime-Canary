package jsonquery

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestRound6RepairReference(t *testing.T) {
	supported(t)
	b, err := os.ReadFile("testdata/round6-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		fixture
		Mode string `json:"mode"`
	}
	if json.Unmarshal(b, &cases) != nil || len(cases) != 97 {
		t.Fatal("R6 fixture coverage missing")
	}
	for _, f := range cases {
		t.Run(f.Name, func(t *testing.T) {
			r := request(f.Expression, string(f.Expected))
			r.Document, r.Query.Operator = f.Document, f.Operator
			got := Check(context.Background(), r)
			want := f.Reference.State
			if f.Mode == "safe-profile" {
				if want != "up" {
					t.Fatal("profile divergence lost JS UP observation")
				}
				want = "down"
			} else if f.Mode != "up" && f.Mode != "error" {
				t.Fatal("unclassified R6 fixture")
			}
			if got.State != want || got.LatencyMs <= 0 || strings.Contains(got.Message, "private-marker") {
				t.Fatalf("R6 state=%s want=%s message=%q latency=%g", got.State, want, got.Message, got.LatencyMs)
			}
			if want == "down" && got.Message != "service check failed" {
				t.Fatalf("expected fixed failure: %+v", got)
			}
		})
	}
}
