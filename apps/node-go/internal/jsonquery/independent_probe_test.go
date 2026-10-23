package jsonquery

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

// This release gate preserves the original independent JS observations. The local
// finite-intermediate profile additionally requires every non-control probe to
// fail closed, even where JS consumes/discards a nonfinite intermediate successfully.
// Evaluation still runs only in the guarded private worker.
func TestIndependentNonfiniteReleaseGate(t *testing.T) {
	supported(t)
	b, err := os.ReadFile("testdata/independent-nonfinite-probes.json")
	if err != nil {
		t.Fatal(err)
	}
	var probes []struct {
		fixture
		Control bool `json:"control"`
	}
	if json.Unmarshal(b, &probes) != nil || len(probes) != 34 {
		t.Fatal("independent probe fixture coverage missing")
	}
	falseUps, safeDifferences, matched, incomplete, closedRegressions := 0, 0, 0, 0, 0
	// These eight paths were confirmed false-UPs before the local guard repair.
	originalFalseUP := map[string]bool{
		"arithmetic overflow: type": false, "arithmetic overflow: boolean": false,
		"negative arithmetic overflow: type": false, "negative arithmetic overflow: boolean": false,
		"sum overflow: type": false, "sum overflow: boolean": false,
		"sum NaN: type": false, "sum NaN: boolean": false,
	}
	for _, p := range probes {
		t.Run(p.Name, func(t *testing.T) {
			r := request(p.Expression, string(p.Expected))
			r.Document, r.Query.Operator = p.Document, p.Operator
			got := Check(context.Background(), r)
			observation, _ := json.Marshal(struct{ Name, ReferenceState, GoState string }{p.Name, p.Reference.State, got.State})
			t.Logf("INDEPENDENT_NONFINITE %s", observation)
			if got.State != "up" && got.State != "down" || got.LatencyMs <= 0 {
				incomplete++
				t.Fatalf("independent probe did not complete: %+v", got)
			}
			if p.Control {
				if p.Reference.State != "up" || got.State != "up" {
					t.Error("ordinary finite/lexical control failed")
				}
			} else if got.State != "down" || got.Message != "service check failed" {
				t.Error("finite-intermediate profile requires bounded DOWN/error")
			}
			if _, known := originalFalseUP[p.Name]; known {
				originalFalseUP[p.Name] = true
				if p.Reference.State == "down" && got.State == "down" && got.Message == "service check failed" {
					closedRegressions++
				}
			}
			if got.State == "up" && p.Reference.State == "down" {
				falseUps++
				t.Error("release blocker: evaluator returned UP while pinned JS returned DOWN/error")
			} else if got.State != p.Reference.State {
				safeDifferences++
				t.Log("deliberate safe-DOWN finite-intermediate profile divergence")
			} else {
				matched++
			}
		})
	}
	for name, seen := range originalFalseUP {
		if !seen {
			t.Errorf("missing original false-UP regression: %s", name)
		}
	}
	if closedRegressions != 8 {
		t.Errorf("closed original false-UP regressions=%d, want 8", closedRegressions)
	}
	t.Logf("INDEPENDENT_NONFINITE_SUMMARY probes=%d matched=%d falseUP=%d safeProfileDifferences=%d incomplete=%d closedRegressions=%d", len(probes), matched, falseUps, safeDifferences, incomplete, closedRegressions)
}
