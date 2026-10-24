package steam

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSteamOptionsEncodedRoundTripBounds(t *testing.T) {
	for _, ca := range []struct{ name, value string }{
		{"unicode-separator", strings.Repeat(string(rune(0x2028)), 32768)},
		{"maximum-escaped-whitespace", strings.Repeat(string(rune(0x0b)), 128<<10)},
	} {
		t.Run(ca.name, func(t *testing.T) {
			base := "https://api.example.test/"
			config := Config{Target: "127.0.0.1:27015", Timeout: time.Second, Options: Options{
				APIKey: strings.Repeat("<&>", 86)[:256], APICA: ca.value, APIURL: base + strings.Repeat("&", 2048-len(base)),
			}}
			if err := Validate(config); err != nil {
				t.Fatal("decoded field bounds admit this configuration", err)
			}
			raw, err := json.Marshal(config.Options)
			if err != nil || len(raw) <= 140<<10 || len(raw) > 1<<20 {
				t.Fatalf("fixture must exercise encoded expansion within one MiB: bytes=%d err=%v", len(raw), err)
			}
			var decoded Options
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatal("node-generated JSON rejected for an admitted configuration", err)
			}
			if !reflect.DeepEqual(decoded, config.Options) || Validate(Config{Target: config.Target, Timeout: config.Timeout, Options: decoded}) != nil {
				t.Fatal("encoded round trip changed options or validity")
			}
		})
	}
	var decoded Options
	// encoding/json strips whitespace outside the value before calling a
	// custom decoder. Keep this oversized padding inside the valid object.
	oversized := append([]byte{'{'}, bytes.Repeat([]byte{' '}, 1<<20)...)
	oversized = append(oversized, []byte(`"apiKey":"fixture"}`)...)
	if json.Unmarshal(oversized, &decoded) == nil {
		t.Fatal("encoded input ceiling was removed")
	}
	if Validate(Config{Target: "127.0.0.1:27015", Timeout: time.Second, Options: Options{
		APIKey: "fixture", APICA: strings.Repeat(" ", (128<<10)+1),
	}}) == nil {
		t.Fatal("decoded CA byte ceiling was removed")
	}
}
