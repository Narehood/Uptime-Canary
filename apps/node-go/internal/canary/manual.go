package canary

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
)

func (m *ManualMonitorOptions) UnmarshalJSON(b []byte) error {
	bad := func() error { return fail(400, "Invalid manual options") }
	d := json.NewDecoder(bytes.NewReader(b))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return bad()
	}
	fields := map[string]string{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return bad()
		}
		key, ok := token.(string)
		if !ok || key != "state" && key != "message" {
			return bad()
		}
		if _, exists := fields[key]; exists {
			return bad()
		}
		value, err := d.Token()
		if err != nil {
			return bad()
		}
		text, ok := value.(string)
		if !ok {
			return bad()
		}
		fields[key] = text
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return bad()
	}
	if _, err := d.Token(); err != io.EOF {
		return bad()
	}
	*m = ManualMonitorOptions{State: fields["state"], Message: fields["message"]}
	return nil
}

func manualProbe(ctx context.Context, m Monitor) ProbeResult {
	if ctx.Err() != nil {
		return ProbeResult{State: "pending", LatencyAvailable: Ptr(false), Message: "Manual status evaluation canceled"}
	}
	state, message := "pending", "Manual monitoring: no status set"
	if m.Manual != nil {
		if m.Manual.State != "" {
			state = m.Manual.State
		}
		if m.Manual.Message != "" {
			message = m.Manual.Message
		} else if m.Manual.State != "" {
			message = "Manual status: " + state
		}
	}
	return ProbeResult{State: state, LatencyAvailable: Ptr(false), Message: message}
}
