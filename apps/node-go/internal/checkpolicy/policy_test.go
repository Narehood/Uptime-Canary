package checkpolicy_test

import (
	"encoding/json"
	"strings"
	"testing"

	"uptime-canary/apps/node-go/internal/checkpolicy"
)

func config() checkpolicy.Config {
	return checkpolicy.Config{
		Generation:      "monitor/check-generation-1",
		Retries:         2,
		IntervalMs:      60_000,
		RetryIntervalMs: 1_000,
		TimeoutMs:       500,
	}
}

type step struct {
	raw      checkpolicy.Status
	want     checkpolicy.Status
	failures int
	interval int64
}

func runSteps(t *testing.T, cfg checkpolicy.Config, steps []step, restart bool) checkpolicy.State {
	t.Helper()
	var state checkpolicy.State
	for i, s := range steps {
		before := state
		next, got, err := checkpolicy.Evaluate(cfg, state, s.raw)
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		want := checkpolicy.Result{
			RawState: s.raw, EffectiveState: s.want,
			ConsecutiveFailures: s.failures, NextIntervalMs: s.interval,
		}
		if got != want {
			t.Fatalf("step %d: got %+v; want %+v", i, got, want)
		}
		wantState := checkpolicy.State{Generation: cfg.Generation, ConsecutiveFailures: s.failures}
		if next != wantState {
			t.Fatalf("step %d: state %+v; want %+v", i, next, wantState)
		}
		if state != before {
			t.Fatalf("step %d: input state changed", i)
		}
		// Replaying a completed observation against its original input is
		// deterministic; the caller must commit next only once.
		again, result, err := checkpolicy.Evaluate(cfg, before, s.raw)
		if err != nil || again != next || result != got {
			t.Fatalf("step %d: nondeterministic replay: %+v %+v %v", i, again, result, err)
		}
		if restart {
			data, err := json.Marshal(next)
			if err != nil {
				t.Fatal(err)
			}
			state = checkpolicy.State{}
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			if state != next {
				t.Fatalf("step %d: restart lost state", i)
			}
		} else {
			state = next
		}
	}
	return state
}

func TestTransitionsAndRestart(t *testing.T) {
	steps := []step{
		{checkpolicy.Up, checkpolicy.Up, 0, 60_000},
		{checkpolicy.Down, checkpolicy.Pending, 1, 1_000},
		{checkpolicy.Down, checkpolicy.Pending, 2, 1_000},
		{checkpolicy.Down, checkpolicy.Down, 3, 1_000},
		{checkpolicy.Down, checkpolicy.Down, 4, 1_000},
		{checkpolicy.Up, checkpolicy.Up, 0, 60_000},
		{checkpolicy.Down, checkpolicy.Pending, 1, 1_000},
		{checkpolicy.Up, checkpolicy.Up, 0, 60_000},
	}
	t.Run("continuous", func(t *testing.T) { runSteps(t, config(), steps, false) })
	t.Run("restart-after-every-observation", func(t *testing.T) { runSteps(t, config(), steps, true) })
}

func TestZeroRetriesStillUseRetryIntervalWhenDown(t *testing.T) {
	cfg := config()
	cfg.Retries = 0
	runSteps(t, cfg, []step{
		{checkpolicy.Down, checkpolicy.Down, 1, 1_000},
		{checkpolicy.Down, checkpolicy.Down, 2, 1_000},
		{checkpolicy.Pending, checkpolicy.Pending, 0, 1_000},
		{checkpolicy.Up, checkpolicy.Up, 0, 60_000},
	}, true)
}

func TestUpsideDownBeforeRetries(t *testing.T) {
	cfg := config()
	cfg.UpsideDown = true
	runSteps(t, cfg, []step{
		{checkpolicy.Down, checkpolicy.Up, 0, 60_000},
		{checkpolicy.Up, checkpolicy.Pending, 1, 1_000},
		{checkpolicy.Up, checkpolicy.Pending, 2, 1_000},
		{checkpolicy.Up, checkpolicy.Down, 3, 1_000},
		{checkpolicy.Up, checkpolicy.Down, 4, 1_000},
		{checkpolicy.Down, checkpolicy.Up, 0, 60_000},
		{checkpolicy.Up, checkpolicy.Pending, 1, 1_000},
		{checkpolicy.Pending, checkpolicy.Pending, 0, 1_000},
		{checkpolicy.Up, checkpolicy.Pending, 1, 1_000},
	}, true)
	cfg.Retries = 0
	runSteps(t, cfg, []step{
		{checkpolicy.Up, checkpolicy.Down, 1, 1_000},
		{checkpolicy.Down, checkpolicy.Up, 0, 60_000},
	}, false)
}

func TestServicePendingBreaksStreakWithoutRecovery(t *testing.T) {
	for _, inverted := range []bool{false, true} {
		name, failure := "normal", checkpolicy.Down
		if inverted {
			name, failure = "inverted", checkpolicy.Up
		}
		t.Run(name, func(t *testing.T) {
			cfg := config()
			cfg.UpsideDown = inverted
			runSteps(t, cfg, []step{
				{checkpolicy.Pending, checkpolicy.Pending, 0, 1_000},
				{failure, checkpolicy.Pending, 1, 1_000},
				{checkpolicy.Pending, checkpolicy.Pending, 0, 1_000},
				{failure, checkpolicy.Pending, 1, 1_000},
				{failure, checkpolicy.Pending, 2, 1_000},
				{failure, checkpolicy.Down, 3, 1_000},
				{checkpolicy.Pending, checkpolicy.Pending, 0, 1_000},
				{checkpolicy.Pending, checkpolicy.Pending, 0, 1_000},
				{failure, checkpolicy.Pending, 1, 1_000},
			}, true)
		})
	}
}

func TestRestoreAtRetryBoundary(t *testing.T) {
	var state checkpolicy.State
	if err := json.Unmarshal([]byte(`{"generation":"monitor/check-generation-1","consecutiveFailures":2}`), &state); err != nil {
		t.Fatal(err)
	}
	next, got, err := checkpolicy.Evaluate(config(), state, checkpolicy.Down)
	if err != nil || got.EffectiveState != checkpolicy.Down || got.ConsecutiveFailures != 3 || next.ConsecutiveFailures != 3 {
		t.Fatalf("restored final retry must become down, got %+v %+v %v", next, got, err)
	}
}

func TestGenerationChangeDiscardsOldEvidence(t *testing.T) {
	old := checkpolicy.State{Generation: "monitor/old-target", ConsecutiveFailures: 101}
	cfg := config()
	next, got, err := checkpolicy.Evaluate(cfg, old, checkpolicy.Down)
	if err != nil || next.Generation != cfg.Generation || got.EffectiveState != checkpolicy.Pending || got.ConsecutiveFailures != 1 {
		t.Fatalf("new target inherited old failures: %+v %+v %v", next, got, err)
	}

	// The caller changes generation for policy semantics as well as targets.
	cfg.Generation = "monitor/inverted-policy"
	cfg.UpsideDown = true
	cfg.Retries = 1
	next, got, err = checkpolicy.Evaluate(cfg, next, checkpolicy.Up)
	if err != nil || next.Generation != cfg.Generation || got.EffectiveState != checkpolicy.Pending || got.ConsecutiveFailures != 1 {
		t.Fatalf("inversion edit inherited old failures: %+v %+v %v", next, got, err)
	}
}

func TestIntervalEditsRetainFailureEvidence(t *testing.T) {
	cfg := config()
	state := checkpolicy.State{Generation: cfg.Generation, ConsecutiveFailures: 1}
	cfg.IntervalMs, cfg.RetryIntervalMs = 120_000, 2_000
	next, got, err := checkpolicy.Evaluate(cfg, state, checkpolicy.Down)
	if err != nil || next.ConsecutiveFailures != 2 || got.EffectiveState != checkpolicy.Pending || got.NextIntervalMs != 2_000 {
		t.Fatalf("interval edit reset or invented evidence: %+v %+v %v", next, got, err)
	}
	cfg.IntervalMs, cfg.RetryIntervalMs = 30_000, 750
	next, got, err = checkpolicy.Evaluate(cfg, next, checkpolicy.Down)
	if err != nil || next.ConsecutiveFailures != 3 || got.EffectiveState != checkpolicy.Down || got.NextIntervalMs != 750 {
		t.Fatalf("interval edit at confirmation: %+v %+v %v", next, got, err)
	}
	cfg.IntervalMs, cfg.RetryIntervalMs = 90_000, 1_500
	next, got, err = checkpolicy.Evaluate(cfg, next, checkpolicy.Down)
	if err != nil || next.ConsecutiveFailures != 4 || got.EffectiveState != checkpolicy.Down || got.NextIntervalMs != 1_500 {
		t.Fatalf("interval edit while down: %+v %+v %v", next, got, err)
	}
	_, got, err = checkpolicy.Evaluate(cfg, next, checkpolicy.Up)
	if err != nil || got.ConsecutiveFailures != 0 || got.EffectiveState != checkpolicy.Up || got.NextIntervalMs != 90_000 {
		t.Fatalf("successful check did not adopt new normal interval: %+v %v", got, err)
	}
}

func TestEveryRetryThresholdAndSaturation(t *testing.T) {
	for retries := 0; retries <= checkpolicy.MaxRetries; retries++ {
		cfg := config()
		cfg.Retries = retries
		var state checkpolicy.State
		for failure := 1; failure <= retries; failure++ {
			next, got, err := checkpolicy.Evaluate(cfg, state, checkpolicy.Down)
			if err != nil || got.EffectiveState != checkpolicy.Pending || got.ConsecutiveFailures != failure {
				t.Fatalf("retries=%d failure=%d must be pending: %+v %v", retries, failure, got, err)
			}
			state = next
		}
		next, got, err := checkpolicy.Evaluate(cfg, state, checkpolicy.Down)
		if err != nil || got.EffectiveState != checkpolicy.Down || got.ConsecutiveFailures != retries+1 {
			t.Fatalf("retries=%d must confirm on failure %d: %+v %v", retries, retries+1, got, err)
		}
		state = next
		for failure := retries + 2; failure <= 1_000; failure++ {
			next, got, err = checkpolicy.Evaluate(cfg, state, checkpolicy.Down)
			if err != nil || got.EffectiveState != checkpolicy.Down || got.NextIntervalMs != cfg.RetryIntervalMs || got.ConsecutiveFailures < state.ConsecutiveFailures || got.ConsecutiveFailures > 101 {
				t.Fatalf("retries=%d continued down became unsafe: %+v %v", retries, got, err)
			}
			state = next
		}
		if state.ConsecutiveFailures != 101 {
			t.Fatalf("retries=%d streak did not saturate at 101: %+v", retries, state)
		}
		_, got, err = checkpolicy.Evaluate(cfg, state, checkpolicy.Up)
		if err != nil || got.EffectiveState != checkpolicy.Up || got.ConsecutiveFailures != 0 {
			t.Fatalf("retries=%d saturated streak did not recover: %+v %v", retries, got, err)
		}
	}
}

func TestValidateConfigBounds(t *testing.T) {
	const maxInt64 = int64(1<<63 - 1)
	const minInt64 = int64(-1 << 63)
	tests := []struct {
		name string
		edit func(*checkpolicy.Config)
		ok   bool
	}{
		{"normal", func(*checkpolicy.Config) {}, true},
		{"zero-retries", func(c *checkpolicy.Config) { c.Retries = 0 }, true},
		{"max-retries", func(c *checkpolicy.Config) { c.Retries = 100 }, true},
		{"negative-retries", func(c *checkpolicy.Config) { c.Retries = -1 }, false},
		{"too-many-retries", func(c *checkpolicy.Config) { c.Retries = 101 }, false},
		{"extreme-retries", func(c *checkpolicy.Config) { c.Retries = int(^uint(0) >> 1) }, false},
		{"min-intervals", func(c *checkpolicy.Config) { c.IntervalMs, c.RetryIntervalMs, c.TimeoutMs = 100, 100, 25 }, true},
		{"max-intervals", func(c *checkpolicy.Config) {
			c.IntervalMs, c.RetryIntervalMs, c.TimeoutMs = 86_400_000, 86_400_000, 120_000
		}, true},
		{"equal-timeout", func(c *checkpolicy.Config) { c.IntervalMs, c.RetryIntervalMs, c.TimeoutMs = 1_000, 1_000, 1_000 }, true},
		{"timeout-at-min-interval", func(c *checkpolicy.Config) { c.IntervalMs, c.RetryIntervalMs, c.TimeoutMs = 100, 100, 100 }, true},
		{"retry-longer-than-normal", func(c *checkpolicy.Config) { c.RetryIntervalMs = 90_000 }, true},
		{"missing-generation", func(c *checkpolicy.Config) { c.Generation = "" }, false},
		{"max-generation", func(c *checkpolicy.Config) { c.Generation = strings.Repeat("x", 256) }, true},
		{"long-generation", func(c *checkpolicy.Config) { c.Generation = strings.Repeat("x", 257) }, false},
		{"max-generation-unicode", func(c *checkpolicy.Config) { c.Generation = strings.Repeat("é", 128) }, true},
		{"long-generation-unicode", func(c *checkpolicy.Config) { c.Generation = strings.Repeat("é", 129) }, false},
		{"invalid-utf8-generation", func(c *checkpolicy.Config) { c.Generation = string([]byte{0xff}) }, false},
	}
	for _, field := range []string{"interval", "retryInterval"} {
		for _, value := range []int64{minInt64, -1, 0, 99, 86_400_001, maxInt64} {
			c := config()
			if field == "interval" {
				c.IntervalMs = value
			} else {
				c.RetryIntervalMs = value
			}
			if err := checkpolicy.Validate(c); err == nil {
				t.Errorf("accepted %s=%d", field, value)
			}
		}
	}
	for _, value := range []int64{minInt64, -1, 0, 24, 120_001, maxInt64} {
		c := config()
		c.IntervalMs, c.RetryIntervalMs, c.TimeoutMs = 86_400_000, 86_400_000, value
		if err := checkpolicy.Validate(c); err == nil {
			t.Errorf("accepted timeout=%d", value)
		}
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := config()
			tc.edit(&c)
			before := c
			err := checkpolicy.Validate(c)
			if (err == nil) != tc.ok {
				t.Fatalf("Validate()=%v; valid=%v", err, tc.ok)
			}
			if c != before {
				t.Fatal("Validate mutated config")
			}
		})
	}
	for _, zeroRetries := range []bool{false, true} {
		for _, shortRetry := range []bool{false, true} {
			c := config()
			if zeroRetries {
				c.Retries = 0
			}
			if shortRetry {
				c.RetryIntervalMs = 499
			} else {
				c.IntervalMs = 499
			}
			if err := checkpolicy.Validate(c); err == nil {
				t.Fatalf("accepted timeout exceeding an interval: %+v", c)
			}
		}
	}
}

func TestValidatePersistedState(t *testing.T) {
	tests := []struct {
		state checkpolicy.State
		ok    bool
	}{
		{checkpolicy.State{}, true},
		{checkpolicy.State{Generation: "g", ConsecutiveFailures: 0}, true},
		{checkpolicy.State{Generation: "g", ConsecutiveFailures: 101}, true},
		{checkpolicy.State{Generation: "g", ConsecutiveFailures: -1}, false},
		{checkpolicy.State{Generation: "g", ConsecutiveFailures: 102}, false},
		{checkpolicy.State{Generation: "", ConsecutiveFailures: 1}, false},
		{checkpolicy.State{Generation: strings.Repeat("x", 257)}, false},
		{checkpolicy.State{Generation: string([]byte{0xff})}, false},
	}
	for _, tc := range tests {
		if err := tc.state.Validate(); (err == nil) != tc.ok {
			t.Errorf("state %+v: err=%v; valid=%v", tc.state, err, tc.ok)
		}
	}
}

func TestErrorsDoNotConsumeEvidenceOrFabricateResults(t *testing.T) {
	for _, inverted := range []bool{false, true} {
		cfg := config()
		cfg.UpsideDown = inverted
		state := checkpolicy.State{Generation: cfg.Generation, ConsecutiveFailures: 2}
		for _, raw := range []checkpolicy.Status{"", "stale", "UP", " down", "maintenance"} {
			next, got, err := checkpolicy.Evaluate(cfg, state, raw)
			if err == nil || next != state || got != (checkpolicy.Result{}) {
				t.Fatalf("invalid observation %q inverted=%v consumed evidence: %+v %+v %v", raw, inverted, next, got, err)
			}
		}
		badConfig := cfg
		badConfig.TimeoutMs = 0
		next, got, err := checkpolicy.Evaluate(badConfig, state, checkpolicy.Up)
		if err == nil || next != state || got != (checkpolicy.Result{}) {
			t.Fatalf("invalid config changed state: %+v %+v %v", next, got, err)
		}
		for _, generation := range []string{cfg.Generation, "superseded-generation"} {
			badState := checkpolicy.State{Generation: generation, ConsecutiveFailures: 102}
			next, got, err := checkpolicy.Evaluate(cfg, badState, checkpolicy.Up)
			if err == nil || next != badState || got != (checkpolicy.Result{}) {
				t.Fatalf("invalid persisted state silently repaired: %+v %+v %v", next, got, err)
			}
		}
	}
}

func TestJSONContract(t *testing.T) {
	fixtures := []struct {
		value any
		json  string
	}{
		{config(), `{"generation":"monitor/check-generation-1","retries":2,"intervalMs":60000,"retryIntervalMs":1000,"timeoutMs":500,"upsideDown":false}`},
		{checkpolicy.State{Generation: "g", ConsecutiveFailures: 2}, `{"generation":"g","consecutiveFailures":2}`},
		{checkpolicy.State{}, `{"generation":"","consecutiveFailures":0}`},
		{checkpolicy.Result{RawState: checkpolicy.Down, EffectiveState: checkpolicy.Pending, ConsecutiveFailures: 1, NextIntervalMs: 1_000}, `{"rawState":"down","effectiveState":"pending","consecutiveFailures":1,"nextIntervalMs":1000}`},
	}
	for _, fixture := range fixtures {
		data, err := json.Marshal(fixture.value)
		if err != nil || string(data) != fixture.json {
			t.Fatalf("JSON: got %s %v; want %s", data, err, fixture.json)
		}
	}
	var restoredConfig checkpolicy.Config
	if err := json.Unmarshal([]byte(fixtures[0].json), &restoredConfig); err != nil || restoredConfig != config() {
		t.Fatalf("config round trip: %+v %v", restoredConfig, err)
	}
	for _, data := range []string{
		`{"generation":"g","consecutiveFailures":1.5}`,
		`{"generation":"g","consecutiveFailures":9223372036854775808}`,
		`{"generation":"g","consecutiveFailures":-1}`,
		`{"generation":"g","consecutiveFailures":102}`,
	} {
		var restored checkpolicy.State
		err := json.Unmarshal([]byte(data), &restored)
		if err == nil {
			err = restored.Validate()
		}
		if err == nil {
			t.Fatalf("accepted invalid serialized state: %s", data)
		}
	}
}
