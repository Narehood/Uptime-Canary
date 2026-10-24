package process

import (
	"testing"
	"time"
)

func TestPM2FramesPreserveOnlyFixedSummaries(t *testing.T) {
	for _, result := range []Result{
		{State: "up", Message: "PM2 process is online"},
		{State: "down", Message: "PM2 process is not online"},
		{State: "down", Message: "PM2 process was not found"},
		{State: "pending", Message: "PM2 status unavailable"},
	} {
		if !validTypedResult("pm2", result, time.Second) || safeMessage("pm2", result) != result.Message {
			t.Fatalf("fixed PM2 result refused: %+v", result)
		}
		changed := result
		changed.LatencyMs = 1
		if validTypedResult("pm2", changed, time.Second) {
			t.Fatal("PM2 accepted a fabricated RTT")
		}
	}
	for _, result := range []Result{
		{State: "up", Message: "PM2 status unavailable"},
		{State: "pending", Message: "private environment or daemon error"},
		{State: "down", Message: "PM2 status unavailable"},
		{State: "up", Message: "PM2 process is online\nprivate"},
	} {
		if validTypedResult("pm2", result, time.Second) {
			t.Fatal("PM2 worker acquired unsupported state or diagnostic authority")
		}
	}
	if validTypedResult("redis", Result{State: "pending", Message: "PM2 status unavailable"}, time.Second) {
		t.Fatal("PM2 Pending authority escaped to a network engine")
	}
}
