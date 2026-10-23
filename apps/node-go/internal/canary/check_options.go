package canary

import (
	"context"
	"time"
	"uptime-canary/apps/node-go/internal/checks"
)

func activeCheckConfig(m MonitorInput) checks.Config {
	c := checks.Config{Type: checks.Type(m.Type), Target: m.Target, Timeout: time.Duration(m.TimeoutMs) * time.Millisecond, HTTP: m.HTTP, DNS: m.DNS, WebSocket: m.WebSocket, SMTP: m.SMTP}
	if m.TLS != nil {
		c.TLS = &checks.TLSConfig{ServerName: m.TLS.ServerName, CAPEM: m.TLS.CAPEM, ClientCertPEM: m.TLS.ClientCertPEM, ClientKeyPEM: m.TLS.ClientKeyPEM, MinValidity: time.Duration(m.TLS.MinValidityDays * float64(24*time.Hour)), IgnoreVerification: m.TLS.IgnoreVerification}
	}
	if m.Type == "http" && len(m.ExpectedStatusCodes) > 0 {
		o := checks.HTTPConfig{}
		if c.HTTP != nil {
			o = *c.HTTP
		}
		if len(o.StatusRanges) == 0 {
			for _, code := range m.ExpectedStatusCodes {
				o.StatusRanges = append(o.StatusRanges, checks.StatusRange{Min: code, Max: code})
			}
		}
		c.HTTP = &o
	}
	return c
}

func ActiveProbe(ctx context.Context, m Monitor) ProbeResult {
	r := checks.Check(ctx, activeCheckConfig(m.MonitorInput))
	return ProbeResult{State: r.State, LatencyMs: r.LatencyMs, Message: r.Message, StatusCode: r.StatusCode, Certificate: r.Certificate}
}
