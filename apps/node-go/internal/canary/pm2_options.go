package canary

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"uptime-canary/apps/node-go/internal/pm2monitor"
)

func validatePM2MonitorJSON(raw []byte) error {
	bad := func() error { return fail(400, "Invalid PM2 monitor input") }
	if pm2monitor.ValidateJSON(raw) != nil {
		return bad()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields["pm2"] == nil || bytes.Equal(bytes.TrimSpace(fields["pm2"]), []byte("null")) {
		return bad()
	}
	// Reject case aliases before they can override exact typed fields. Other
	// monitor families retain their existing management admission rules.
	if !exactJSONFieldNames(fields, reflect.TypeFor[Monitor]()) {
		return bad()
	}
	return nil
}

func exactJSONFieldNames(fields map[string]json.RawMessage, typ reflect.Type) bool {
	for key := range fields {
		for _, field := range reflect.VisibleFields(typ) {
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name != "" && name != "-" && key != name && strings.EqualFold(key, name) {
				return false
			}
		}
	}
	return true
}

func pm2MonitorVersionJSON(raw []byte) error {
	if pm2monitor.ValidateJSON(raw) != nil {
		return fail(400, "Invalid PM2 monitor version")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return fail(400, "Invalid PM2 monitor version")
	}
	if !exactJSONFieldNames(fields, reflect.TypeFor[Version]()) {
		return fail(400, "Invalid PM2 monitor version")
	}
	return validatePM2MonitorJSON(fields["monitor"])
}

func pm2Config(m MonitorInput) pm2monitor.Config {
	c := pm2monitor.Config{Name: m.Target, Timeout: time.Duration(m.TimeoutMs) * time.Millisecond}
	if m.PM2 != nil {
		c.Options = *m.PM2
	}
	return c
}

func PM2Probe(ctx context.Context, m Monitor) ProbeResult {
	r := pm2monitor.Check(ctx, pm2Config(m.MonitorInput))
	return ProbeResult{State: r.State, Message: r.Message, LatencyAvailable: Ptr(false)}
}
