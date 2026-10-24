// Package pm2monitor reads an existing local PM2 daemon in a guarded worker.
package pm2monitor

import (
	"bytes"
	"encoding/json"
	"errors"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const Type = "pm2"
const SupportedVersion = "6.0.8"

type Options struct {
	Transport string `json:"transport"`
	Home      string `json:"home,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type Config struct {
	Name    string        `json:"name"`
	Timeout time.Duration `json:"timeout"`
	Options Options       `json:"options"`
}

var errConfig = errors.New("invalid PM2 configuration")
var errReply = errors.New("invalid PM2 reply")

func exactText(value string, maximum int) bool {
	if value == "" || len(value) > maximum || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, ch := range value {
		if unicode.IsControl(ch) {
			return false
		}
	}
	return true
}

func validOptions(o Options) bool {
	if o.Namespace != "" && !exactText(o.Namespace, 256) {
		return false
	}
	switch o.Transport {
	case "unix":
		return exactText(o.Home, 107) && path.IsAbs(o.Home) && path.Clean(o.Home) == o.Home &&
			!strings.ContainsAny(o.Home, "\\$") && !strings.Contains(o.Home, "~") && len(path.Join(o.Home, "rpc.sock")) <= 107
	case "windows-pipe":
		return o.Home == ""
	default:
		return false
	}
}

func Validate(c Config) error {
	if !exactText(c.Name, 256) || c.Timeout <= 0 || c.Timeout > 2*time.Minute || !validOptions(c.Options) {
		return errConfig
	}
	return nil
}

// Decode exact keys once for API, peer, persisted configuration and worker input.
func (o *Options) UnmarshalJSON(raw []byte) error {
	fields, err := objectFields(raw, 4096, []string{"transport", "home", "namespace"})
	if err != nil || fields["transport"] == nil {
		return errConfig
	}
	type plain Options
	var value plain
	if json.Unmarshal(raw, &value) != nil || !validOptions(Options(value)) ||
		fields["namespace"] != nil && value.Namespace == "" || fields["home"] != nil && value.Transport == "windows-pipe" {
		return errConfig
	}
	*o = Options(value)
	return nil
}

func objectFields(raw []byte, maximum int, allowed []string) (map[string]json.RawMessage, error) {
	if len(raw) > maximum || strictJSON(raw) != nil {
		return nil, errConfig
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, errConfig
	}
	for key, value := range fields {
		known := false
		for _, candidate := range allowed {
			known = known || key == candidate
		}
		if !known || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errConfig
		}
	}
	return fields, nil
}
