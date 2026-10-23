package checkpolicy_test

import (
	"testing"
	"uptime-canary/apps/node-go/internal/checkpolicy"
)

func TestUnavailableMeasurementRemainsUnknownUnderInversion(t *testing.T) {
	for _, inverted := range []bool{false, true} {
		c := config()
		c.UpsideDown = inverted
		s := checkpolicy.State{Generation: c.Generation, ConsecutiveFailures: 3}
		next, result, err := checkpolicy.Evaluate(c, s, checkpolicy.Unknown)
		if err != nil || next.ConsecutiveFailures != 0 || result.RawState != checkpolicy.Unknown || result.EffectiveState != checkpolicy.Unknown || result.NextIntervalMs != c.RetryIntervalMs {
			t.Fatal("unknown invented success/failure or kept stale retry evidence", next, result, err)
		}
	}
}
