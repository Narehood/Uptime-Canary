package notifyhttp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestFullReviewConfigEscapedRoundTrip(t *testing.T) {
	for _, char := range []string{"a", "&"} {
		t.Run(char, func(t *testing.T) {
			c := fixtureConfig(Ntfy, "https://example.test/"+strings.Repeat(char, 1800))
			c.Ntfy.Token = ""
			c.Ntfy.Username, c.Ntfy.Password = "fixture", strings.Repeat(char, 512)
			for i := 0; i < 16; i++ {
				c.Ntfy.Tags = append(c.Ntfy.Tags, strings.Repeat(char, 64))
			}
			if err := Validate(c); err != nil {
				t.Fatalf("bounded input rejected before round trip: %v", err)
			}
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			var got Config
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("valid native configuration rejected after encoding (%d bytes): %v", len(data), err)
			}
			if !reflect.DeepEqual(got, c) || Validate(got) != nil {
				t.Fatal("native round trip changed bounded fields")
			}
		})
	}
}

func TestFullReviewConfigStillRejectsOversizedInput(t *testing.T) {
	c := fixtureConfig(Ntfy, "https://example.test")
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var got Config
	// encoding/json strips whitespace outside a value before UnmarshalJSON.
	// Preserve the original raw-method boundary and test wrapper admission with
	// whitespace inside the object, where it remains part of the encoded value.
	if err := got.UnmarshalJSON(append(data, []byte(strings.Repeat(" ", MaxConfigBytes+1))...)); err != errConfig {
		t.Fatal("oversized raw input did not return the fixed error")
	}
	oversized := append(append(data[:len(data)-1:len(data)-1], []byte(strings.Repeat(" ", MaxConfigBytes+1))...), '}')
	if err := json.Unmarshal(oversized, &got); err != errConfig {
		t.Fatal("oversized but syntactically valid JSON did not return the fixed error")
	}
	for _, tc := range []struct {
		name   string
		change func(*NtfyConfig)
	}{
		{"password", func(p *NtfyConfig) {
			p.Token = ""
			p.Username = "fixture"
			p.Password = strings.Repeat("&", 513)
		}},
		{"tag-size", func(p *NtfyConfig) { p.Tags = []string{strings.Repeat("&", 65)} }},
		{"tag-count", func(p *NtfyConfig) { p.Tags = strings.Split(strings.Repeat("a,", 16)+"a", ",") }},
		{"url", func(p *NtfyConfig) { p.BaseURL = "https://example.test/" + strings.Repeat("&", MaxURLBytes) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixtureConfig(Ntfy, "https://example.test")
			tc.change(c.Ntfy)
			if Validate(c) != errConfig {
				t.Fatal("decoded field limit was removed")
			}
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			if json.Unmarshal(data, &got) != errConfig {
				t.Fatal("encoded input bypassed its decoded field limit")
			}
		})
	}
}
