package canary

import (
	"context"
	"time"

	"uptime-canary/apps/node-go/internal/steam"
)

func steamConfig(m MonitorInput) steam.Config {
	c := steam.Config{Target: m.Target, Timeout: time.Duration(m.TimeoutMs) * time.Millisecond}
	if m.Steam != nil {
		c.Options = *m.Steam
	}
	return c
}

func SteamProbe(ctx context.Context, m Monitor) ProbeResult {
	r := steam.Check(ctx, steamConfig(m.MonitorInput))
	// No Steam API HTTP status or request duration is presented as the game's
	// protocol status or RTT. Optional ICMP is the only measured latency.
	return ProbeResult{State: r.State, Message: r.Message, LatencyMs: r.LatencyMs, LatencyAvailable: Ptr(r.LatencyAvailable)}
}
