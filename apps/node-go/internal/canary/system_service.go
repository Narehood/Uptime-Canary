package canary

import (
	"context"
	"time"

	"uptime-canary/apps/node-go/internal/systemservice"
)

func systemServiceConfig(m MonitorInput) systemservice.Config {
	return systemservice.Config{Name: m.Target, Timeout: time.Duration(m.TimeoutMs) * time.Millisecond}
}

func SystemServiceProbe(ctx context.Context, m Monitor) ProbeResult {
	result := systemservice.Check(ctx, systemServiceConfig(m.MonitorInput))
	return ProbeResult{State: result.State, Message: result.Message, LatencyAvailable: Ptr(false)}
}
