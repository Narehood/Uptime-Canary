package canary

import (
	"bytes"
	"encoding/json"
	"reflect"

	"uptime-canary/apps/node-go/internal/jsonshape"
)

func typedMonitorJSON(raw []byte, field string) error {
	bad := func() error { return fail(400, "Invalid typed monitor input") }
	if jsonshape.Validate(raw) != nil {
		return bad()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields[field] == nil || bytes.Equal(bytes.TrimSpace(fields[field]), []byte("null")) || !exactJSONFieldNames(fields, reflect.TypeFor[Monitor]()) {
		return bad()
	}
	return nil
}

func typedMonitorVersionJSON(raw []byte, field string) error {
	if jsonshape.Validate(raw) != nil {
		return fail(400, "Invalid typed monitor version")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || !exactJSONFieldNames(fields, reflect.TypeFor[Version]()) {
		return fail(400, "Invalid typed monitor version")
	}
	return typedMonitorJSON(fields["monitor"], field)
}
