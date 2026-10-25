package canary

import (
	"context"
	"time"

	"uptime-canary/apps/node-go/internal/gamedigmonitor"
)

func gamedigConfig(m MonitorInput) gamedigmonitor.Config {
	c := gamedigmonitor.Config{Target: m.Target, Timeout: time.Duration(m.TimeoutMs) * time.Millisecond}
	if m.GameDig != nil {
		c.Options = *m.GameDig
	}
	return c
}

func GameDigProbe(ctx context.Context, m Monitor) ProbeResult {
	r := gamedigmonitor.Check(ctx, gamedigConfig(m.MonitorInput))
	return ProbeResult{State: r.State, Message: r.Message, LatencyMs: r.LatencyMs, LatencyAvailable: Ptr(r.State == "up")}
}
