package pm2monitor

import (
	"context"
	"encoding/json"
	"net"

	"uptime-canary/apps/node-go/internal/probeworker/process"
)

func unavailable() process.Result {
	return process.Result{State: "pending", Message: "PM2 status unavailable"}
}

// Check admits only the fixed worker protocol. Daemon communication never runs
// in the long-lived node process and has no direct fallback on worker failure.
func Check(ctx context.Context, c Config) process.Result {
	if Validate(c) != nil {
		return process.Result{State: "down", Message: errConfig.Error()}
	}
	r := process.RunConfig(ctx, c.Timeout, struct {
		Type string `json:"type"`
		Config
	}{Type, c})
	return workerResult(r)
}

func workerResult(r process.Result) process.Result {
	r.LatencyMs = 0 // A local-manager query is not a network latency sample.
	if r.State == "down" && (r.Message == "service check timed out" || r.Message == "service check canceled") {
		return unavailable()
	}
	return r
}

func CheckInWorker(ctx context.Context, c Config) process.Result { return check(ctx, c, dialManager) }

func check(ctx context.Context, c Config, dial func(context.Context, Config) (net.Conn, error)) process.Result {
	if Validate(c) != nil {
		return process.Result{State: "down", Message: errConfig.Error()}
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	if ctx.Err() != nil {
		return unavailable()
	}
	conn, err := dial(ctx, c)
	if err != nil || conn == nil {
		if conn != nil {
			conn.Close()
		}
		return unavailable()
	}
	defer conn.Close()
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = conn.Close(); close(closed) })
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil || ctx.Err() != nil {
		return unavailable()
	}
	version, err := call(conn, "getVersion", "canary-pm2:0", maxVersionReply)
	var reported string
	if err != nil || json.Unmarshal(version, &reported) != nil || reported != SupportedVersion || ctx.Err() != nil {
		return unavailable()
	}
	list, err := call(conn, "getMonitorData", "canary-pm2:1", maxMonitorReply)
	if err != nil {
		return unavailable()
	}
	state, message, err := processState(list, c)
	if err != nil || ctx.Err() != nil {
		return unavailable()
	}
	return process.Result{State: state, Message: message}
}

func WorkerHandler() process.Handler {
	var configuration Config
	valid := false
	return process.Handler{
		Validate: func(raw []byte) error {
			valid = false
			fields, err := objectFields(raw, 8192, []string{"type", "name", "timeout", "options"})
			if err != nil || len(fields) != 4 {
				return errConfig
			}
			var decoded struct {
				Type string `json:"type"`
				Config
			}
			if json.Unmarshal(raw, &decoded) != nil || decoded.Type != Type || Validate(decoded.Config) != nil {
				return errConfig
			}
			configuration, valid = decoded.Config, true
			return nil
		},
		Check: func(ctx context.Context) process.Result {
			if !valid {
				return process.Result{State: "down", Message: errConfig.Error()}
			}
			return CheckInWorker(ctx, configuration)
		},
	}
}
