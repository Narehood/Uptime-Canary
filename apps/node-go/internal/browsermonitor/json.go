package browsermonitor

import (
	"bytes"
	"encoding/json"

	"uptime-canary/apps/node-go/internal/jsonshape"
)

func (o *Options) UnmarshalJSON(raw []byte) error {
	if jsonshape.Validate(raw) != nil {
		return errProtocol
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields["endpoint"] == nil {
		return errProtocol
	}
	for key, value := range fields {
		if key != "endpoint" && key != "bearerToken" && key != "screenshot" && key != "delayMs" || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errProtocol
		}
	}
	type plain Options
	var out plain
	if json.Unmarshal(raw, &out) != nil {
		return errProtocol
	}
	*o = Options(out)
	return nil
}
