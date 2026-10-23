package canary

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"uptime-canary/apps/node-go/internal/globalping"
)

// Keep decoding at the management/peer boundary exact while the pure leaf
// remains usable directly by Go callers.
type GlobalpingMonitorOptions globalping.Options

func (o *GlobalpingMonitorOptions) UnmarshalJSON(b []byte) error {
	bad := func() error { return fail(400, "Invalid Globalping options") }
	fields, err := statusObject(b, "mode", "location", "apiUrl", "token", "allowInsecureApi", "apiCa", "probeTimeoutSeconds", "ipVersion", "ping", "http", "dns", "apiFailureIsDown")
	if err != nil {
		return bad()
	}
	for key, allowed := range map[string][]string{
		"ping": {"packets", "protocol", "port"},
		"http": {"method", "protocol", "headers", "acceptedStatuses", "keyword", "invertKeyword", "jsonata", "ignoreTls"},
		"dns":  {"queryType", "protocol", "port", "resolver", "regex", "allowEmptyAnswers"},
	} {
		if raw, exists := fields[key]; exists {
			parts, err := statusObject(raw, allowed...)
			if err != nil {
				return bad()
			}
			if query, exists := parts["jsonata"]; exists {
				if _, err := statusObject(query, "expression", "operator", "expected"); err != nil {
					return bad()
				}
			}
			if ranges, exists := parts["acceptedStatuses"]; exists {
				var values []json.RawMessage
				if json.Unmarshal(ranges, &values) != nil || len(values) > 32 {
					return bad()
				}
				for _, value := range values {
					if _, err := statusObject(value, "min", "max"); err != nil {
						return bad()
					}
				}
			}
			if headers, exists := parts["headers"]; exists {
				var values map[string]json.RawMessage
				if json.Unmarshal(headers, &values) != nil || len(values) > 32 {
					return bad()
				}
				keys := make([]string, 0, len(values))
				seen := map[string]bool{}
				for name := range values {
					folded := strings.ToLower(name)
					if seen[folded] {
						return bad()
					}
					seen[folded] = true
					keys = append(keys, name)
				}
				if _, err := statusObject(headers, keys...); err != nil {
					return bad()
				}
			}
		}
	}
	type plain GlobalpingMonitorOptions
	var value plain
	if json.Unmarshal(b, &value) != nil {
		return bad()
	}
	*o = GlobalpingMonitorOptions(value)
	return nil
}

func globalpingConfig(m MonitorInput) globalping.Config {
	c := globalping.Config{Target: m.Target, Timeout: time.Duration(m.TimeoutMs) * time.Millisecond}
	if m.Globalping != nil {
		c.Options = globalping.Options(*m.Globalping)
	}
	return c
}

func GlobalpingProbe(ctx context.Context, m Monitor) ProbeResult {
	r := globalping.Check(ctx, globalpingConfig(m.MonitorInput))
	return ProbeResult{State: r.State, LatencyMs: r.LatencyMs, LatencyAvailable: Ptr(r.LatencyAvailable), Message: r.Message, StatusCode: r.StatusCode}
}

func globalpingPublicTarget(m Monitor) string {
	if m.Type == "globalping" {
		// Publication must not depend on a configuration spelling that the
		// engine normalizes (for example, a whitespace-padded mode).
		if u, err := url.Parse(m.Target); err == nil && u.Host != "" &&
			(strings.EqualFold(u.Scheme, "http") || strings.EqualFold(u.Scheme, "https")) {
			return strings.ToLower(u.Scheme) + "://" + u.Host + "/"
		}
	}
	return m.Target
}
