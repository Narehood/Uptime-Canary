package pm2monitor

import "uptime-canary/apps/node-go/internal/jsonshape"

// ValidateJSON preserves exact-key and Unicode admission when a PM2 name is
// embedded in a larger management or peer record before typed decoding.
func ValidateJSON(raw []byte) error { return strictJSON(raw) }

func strictJSON(raw []byte) error {
	if jsonshape.Validate(raw) != nil {
		return errReply
	}
	return nil
}
