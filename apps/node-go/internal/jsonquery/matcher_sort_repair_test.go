package jsonquery

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestMatcherSortRepairReference(t *testing.T) {
	supported(t)
	b, err := os.ReadFile("testdata/matcher-sort-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		fixture
		Classification string `json:"classification"`
	}
	if json.Unmarshal(b, &cases) != nil || len(cases) != 157 {
		t.Fatal("matcher/sort repair reference coverage missing")
	}
	matched, safeDown := 0, 0
	for _, f := range cases {
		t.Run(f.Name, func(t *testing.T) {
			r := request(f.Expression, string(f.Expected))
			r.Document, r.Query.Operator = f.Document, f.Operator
			got := Check(context.Background(), r)
			want := f.Reference.State
			switch f.Classification {
			case "reference":
				matched++
			case "safe-profile":
				if want != "up" {
					t.Fatal("safe-profile divergence must retain its JS UP reference")
				}
				want = "down"
				safeDown++
			default:
				t.Fatal("unclassified matcher/sort fixture")
			}
			if got.State != want || got.LatencyMs <= 0 || strings.Contains(got.Message, "private-marker") {
				t.Fatalf("matcher/sort state=%s want=%s message=%q latency=%g", got.State, want, got.Message, got.LatencyMs)
			}
			if want == "down" && got.Message != "service check failed" {
				t.Fatalf("expected fixed bounded failure: %+v", got)
			}
		})
	}
	if safeDown != 3 {
		t.Fatal("missing approved partial-native comparator profile regressions")
	}
	t.Logf("MATCHER_SORT_REPAIR_SUMMARY referenceOutcomes=%d deliberateSafeDown=%d", matched, safeDown)
}
