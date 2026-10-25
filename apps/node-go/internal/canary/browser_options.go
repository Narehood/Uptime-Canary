package canary

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"time"

	"uptime-canary/apps/node-go/internal/browsermonitor"
	"uptime-canary/apps/node-go/internal/jsonshape"
)

func validateBrowserMonitorJSON(raw []byte) error {
	bad := func() error { return fail(400, "Invalid browser monitor input") }
	if jsonshape.Validate(raw) != nil {
		return bad()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields["browser"] == nil || bytes.Equal(bytes.TrimSpace(fields["browser"]), []byte("null")) || !exactJSONFieldNames(fields, reflect.TypeFor[Monitor]()) {
		return bad()
	}
	return nil
}

func browserMonitorVersionJSON(raw []byte) error {
	if jsonshape.Validate(raw) != nil {
		return fail(400, "Invalid browser monitor version")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || !exactJSONFieldNames(fields, reflect.TypeFor[Version]()) {
		return fail(400, "Invalid browser monitor version")
	}
	return validateBrowserMonitorJSON(fields["monitor"])
}

func browserConfig(m MonitorInput) browsermonitor.Config {
	c := browsermonitor.Config{Target: m.Target, Timeout: time.Duration(m.TimeoutMs) * time.Millisecond}
	if m.Browser != nil {
		c.Options = *m.Browser
	}
	return c
}

func BrowserProbe(ctx context.Context, m Monitor) ProbeResult {
	r := browsermonitor.Check(ctx, browserConfig(m.MonitorInput))
	return ProbeResult{State: r.State, Message: r.Message, LatencyMs: r.LatencyMs, LatencyAvailable: Ptr(r.State != "pending" && r.LatencyMs > 0), StatusCode: r.StatusCode, Screenshot: r.Screenshot}
}
