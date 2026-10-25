package probeworker

import (
	"bytes"
	"context"
	"encoding/json"

	"uptime-canary/apps/node-go/internal/jsonquery"
	"uptime-canary/apps/node-go/internal/pm2monitor"
	"uptime-canary/apps/node-go/internal/probeworker/process"
	"uptime-canary/apps/node-go/internal/services"
	"uptime-canary/apps/node-go/internal/systemservice"
	"uptime-canary/apps/node-go/internal/tailscalemonitor"
)

// Dispatch runs before CLI parsing, database access, or listener startup.
// CheckInWorker executes locally only after verified hard controls.
func Dispatch(args []string) (handled bool, exitCode int) {
	var c services.Config
	var selected process.Handler
	return process.Dispatch(args, process.Handler{
		Validate: func(b []byte) error {
			selected = process.Handler{}
			var header struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(b, &header); err != nil {
				return err
			}
			if header.Type == jsonquery.Type {
				selected = jsonquery.WorkerHandler()
				return selected.Validate(b)
			}
			if header.Type == systemservice.Type {
				selected = systemservice.WorkerHandler()
				return selected.Validate(b)
			}
			if header.Type == pm2monitor.Type {
				selected = pm2monitor.WorkerHandler()
				return selected.Validate(b)
			}
			if header.Type == tailscalemonitor.Type {
				selected = tailscalemonitor.WorkerHandler()
				return selected.Validate(b)
			}
			d := json.NewDecoder(bytes.NewReader(b))
			d.DisallowUnknownFields()
			if err := d.Decode(&c); err != nil {
				return err
			}
			return c.Validate()
		},
		Check: func(ctx context.Context) process.Result {
			if selected.Check != nil {
				return selected.Check(ctx)
			}
			return process.Result(services.CheckInWorker(ctx, c))
		},
	})
}
