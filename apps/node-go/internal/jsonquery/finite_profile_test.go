package jsonquery

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestFiniteIntermediateProfile(t *testing.T) {
	supported(t)
	b, err := os.ReadFile("testdata/finite-profile-fixtures.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		fixture
		Profile string `json:"profile"`
	}
	if json.Unmarshal(b, &cases) != nil || len(cases) != 40 {
		t.Fatal("finite profile reference coverage missing")
	}
	controls, referenceDown, safeDown := 0, 0, 0
	for _, f := range cases {
		t.Run(f.Name, func(t *testing.T) {
			r := request(f.Expression, string(f.Expected))
			r.Document, r.Query.Operator = f.Document, f.Operator
			got := Check(context.Background(), r)
			if got.LatencyMs <= 0 {
				t.Fatalf("worker did not complete: %+v", got)
			}
			switch f.Profile {
			case "finite-control":
				controls++
				if f.Reference.State != "up" || got.State != "up" {
					t.Fatalf("finite/lexical/HOF control failed: %+v", got)
				}
			case "nonfinite", "visit-budget", "depth-budget":
				if got.State != "down" || got.Message != "service check failed" {
					t.Fatalf("profile violation must return bounded DOWN/error: %+v", got)
				}
				if f.Reference.State == "down" {
					referenceDown++
				} else if f.Reference.State == "up" {
					safeDown++
					t.Log("deliberate safe-DOWN profile divergence; pinned JS is UP")
				} else {
					t.Fatal("invalid JS reference state")
				}
			default:
				t.Fatal("unclassified profile fixture")
			}
		})
	}
	t.Logf("FINITE_PROFILE_SUMMARY controls=%d rejectedReferenceDown=%d deliberateSafeDown=%d", controls, referenceDown, safeDown)
}
