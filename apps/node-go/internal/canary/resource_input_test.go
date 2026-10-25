package canary

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResourceSettingsCannotBeSilentlyDiscarded(t *testing.T) {
	for _, resource := range []struct {
		name  string
		input any
		parse func([]byte) error
	}{
		{"channel", hookChannel("Fixture", "http://127.0.0.1:1/hook"), func(raw []byte) error { _, err := parseChannel(raw); return err }},
		{"maintenance", plannedWork(), func(raw []byte) error { _, err := parseMaintenance(raw); return err }},
	} {
		t.Run(resource.name, func(t *testing.T) {
			base, _ := json.Marshal(resource.input)
			if err := resource.parse(base); err != nil {
				t.Fatal("valid resource refused", err)
			}
			// A submitted view may carry the current resource's metadata.
			metadata := string(base[:len(base)-1]) + `,"id":"fixture","revision":"r1","revisions":["r1"],"updatedAt":1,"deleted":false}`
			if err := resource.parse([]byte(metadata)); err != nil {
				t.Fatal("exact metadata refused", err)
			}
			for _, suffix := range []string{
				`,"futureSetting":true}`, `,"Enabled":false}`, `,"enabled":false}`,
				`,"futureSetting":null}`, `,"revision":null}`, `,"name":"\ud800"}`,
			} {
				if resource.parse(append(append([]byte{}, base[:len(base)-1]...), []byte(suffix)...)) == nil {
					t.Fatal("unsupported or ambiguous setting was acknowledged", suffix)
				}
			}
			// Duplicate nested keys must not disappear during decoding either.
			nested := string(base)
			if resource.name == "channel" {
				nested = strings.Replace(nested, `"kind":"webhook"`, `"kind":"webhook","kind":"webhook"`, 1)
			} else {
				nested = strings.Replace(nested, `"timezone":"UTC"`, `"timezone":"UTC","timezone":"UTC"`, 1)
			}
			if nested == string(base) || resource.parse([]byte(nested)) == nil {
				t.Fatal("duplicate nested setting was acknowledged")
			}
		})
	}
}
