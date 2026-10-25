package process

import (
	"math"
	"testing"
	"time"
)

func TestTailscaleClosedResultContract(t *testing.T) {
	for _, r := range []Result{
		{State: "up", Message: "Tailscale peer replied", LatencyMs: 2.5},
		{State: "down", Message: "Tailscale peer did not reply"},
		{State: "pending", Message: "Tailscale peer status unavailable"},
	} {
		if !validTypedResult("tailscale-ping", r, time.Second) || safeMessage("tailscale-ping", r) != r.Message {
			t.Fatal("valid fixed Tailscale result refused", r)
		}
		changed := r
		changed.Message += " private peer diagnostic"
		if validTypedResult("tailscale-ping", changed, time.Second) {
			t.Fatal("client diagnostic entered worker protocol")
		}
	}
	for _, r := range []Result{
		{State: "up", Message: "Tailscale peer status unavailable"},
		{State: "pending", Message: "Tailscale peer status unavailable", LatencyMs: 1},
		{State: "down", Message: "Tailscale peer did not reply", LatencyMs: 1},
		{State: "up", Message: "Tailscale peer replied", LatencyMs: math.Inf(1)},
		{State: "up", Message: "Tailscale peer replied", LatencyMs: -1},
		{State: "up", Message: "Tailscale peer replied", LatencyMs: 1001},
		{State: "pending", Message: "PM2 status unavailable"},
	} {
		if validTypedResult("tailscale-ping", r, time.Second) {
			t.Fatal("unsupported Tailscale worker result admitted", r)
		}
	}
	if validTypedResult("redis", Result{State: "pending", Message: "Tailscale peer status unavailable"}, time.Second) {
		t.Fatal("local-engine pending leaked into existing network engines")
	}
}
