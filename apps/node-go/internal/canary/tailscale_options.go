package canary

import (
	"context"
	"time"

	"uptime-canary/apps/node-go/internal/tailscalemonitor"
)

func tailscaleConfig(m MonitorInput) tailscalemonitor.Config {
	return tailscalemonitor.Config{Name: m.Target, Timeout: time.Duration(m.TimeoutMs) * time.Millisecond}
}

func TailscaleProbe(ctx context.Context, m Monitor) ProbeResult {
	r := tailscalemonitor.Check(ctx, tailscaleConfig(m.MonitorInput))
	return ProbeResult{State: r.State, Message: r.Message, LatencyMs: r.LatencyMs, LatencyAvailable: Ptr(r.State == "up")}
}
