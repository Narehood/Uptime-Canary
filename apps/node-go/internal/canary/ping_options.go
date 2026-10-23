package canary

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"time"
	"uptime-canary/apps/node-go/internal/icmp"
)

func decodeMonitorOptions(b []byte, out any, allowed ...string) error {
	bad := func() error { return fail(400, "Invalid monitor options") }
	d := json.NewDecoder(bytes.NewReader(b))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return bad()
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || !slices.Contains(allowed, key) || seen[key] {
			return bad()
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(value, []byte("null")) {
			return bad()
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return bad()
	}
	if _, err := d.Token(); err != io.EOF {
		return bad()
	}
	if json.Unmarshal(b, out) != nil {
		return bad()
	}
	return nil
}

func (p *PingMonitorOptions) UnmarshalJSON(b []byte) error {
	type plain PingMonitorOptions
	var out plain
	if err := decodeMonitorOptions(b, &out, "count", "size", "family"); err != nil {
		return err
	}
	*p = PingMonitorOptions(out)
	return nil
}

func pingConfig(m MonitorInput) icmp.Config {
	c := icmp.Config{Target: m.Target, Timeout: time.Duration(m.TimeoutMs) * time.Millisecond}
	if m.Ping != nil {
		c.Count, c.Size, c.Family = m.Ping.Count, m.Ping.Size, m.Ping.Family
	}
	return c
}

func validatePing(m MonitorInput) error {
	if err := icmp.Validate(pingConfig(m)); err != nil {
		return fail(400, err.Error())
	}
	return nil
}
