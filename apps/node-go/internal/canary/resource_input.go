package canary

import (
	"bytes"
	"encoding/json"
	"slices"

	"uptime-canary/apps/node-go/internal/jsonshape"
)

// Reject unsupported settings rather than acknowledge an edit that silently
// drops them. Exact top-level keys also prevent encoding/json case aliases
// from selecting a different value than the revision/required-field checks.
func strictResourceObject(raw []byte, allowed ...string) (map[string]json.RawMessage, error) {
	if jsonshape.Validate(raw) != nil {
		return nil, fail(400, "Invalid resource input")
	}
	fields, err := jsonObject(raw)
	if err != nil {
		return nil, err
	}
	for key, value := range fields {
		if !slices.Contains(allowed, key) || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fail(400, "Invalid resource input")
		}
	}
	return fields, nil
}
