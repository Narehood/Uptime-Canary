package checks

import (
	"context"
	"time"
)

// Check performs one check. It never returns target URLs, raw network errors,
// response bodies, configured matchers, or authentication material.
func Check(ctx context.Context, c Config) (result Result) {
	start := time.Now()
	defer func() { result.LatencyMs = float64(time.Since(start).Microseconds()) / 1000 }()
	if err := Validate(c); err != nil {
		return down("Invalid configuration: " + err.Error())
	}
	if ctx == nil {
		return down("A context is required")
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	if ctx.Err() != nil {
		return down("Check cancelled or timed out")
	}
	switch c.Type {
	case HTTP:
		result = checkHTTP(ctx, c)
	case TCP, TLS:
		result = checkConnection(ctx, c)
	case DNS:
		result = checkDNS(ctx, c)
	case WebSocket:
		result = checkWebSocket(ctx, c)
	case SMTP:
		result = checkSMTP(ctx, c)
	}
	if ctx.Err() != nil && result.State != Pending {
		result.State = Down
		result.Message = "Check cancelled or timed out"
	}
	return result
}

func down(message string) Result { return Result{State: Down, Message: message} }
func up(message string) Result   { return Result{State: Up, Message: message} }
