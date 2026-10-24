package canary

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
)

// json.Unmarshal alone accepts case aliases and duplicate keys. These small
// control-plane envelopes accept only exact, unique, non-null fields.
func dashboardJSON(b []byte, out any, fields ...string) error {
	bad := fail(400, "Invalid dashboard request")
	d := json.NewDecoder(bytes.NewReader(b))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return bad
	}
	seen := map[string]bool{}
	for d.More() {
		t, err = d.Token()
		key, ok := t.(string)
		if err != nil || !ok || seen[key] || !slices.Contains(fields, key) {
			return bad
		}
		seen[key] = true
		var v json.RawMessage
		if d.Decode(&v) != nil || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return bad
		}
	}
	if _, err = d.Token(); err != nil || d.Decode(new(any)) != io.EOF {
		return bad
	}
	if json.Unmarshal(b, out) != nil {
		return bad
	}
	return nil
}
