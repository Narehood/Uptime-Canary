// Package systemservice reads the local OS service manager in a guarded worker.
package systemservice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"uptime-canary/apps/node-go/internal/probeworker/process"
)

const Type = "system-service"

type Config struct {
	Name    string        `json:"name"`
	Timeout time.Duration `json:"timeout"`
}

var errConfig = errors.New("invalid system service configuration")

func (c Config) Validate() error {
	if len(c.Name) == 0 || len(c.Name) > 256 || c.Timeout < 0 || c.Timeout > 2*time.Minute {
		return errConfig
	}
	for i, ch := range []byte(c.Name) {
		alpha := ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_'
		if !alpha && (i == 0 || ch != '.' && ch != '-' && ch != '@') {
			return errConfig
		}
	}
	return nil
}

type request struct {
	Type string `json:"type"`
	Config
}

// Check never calls a service manager in the node process. Resource-control,
// startup and capacity failures remain Pending; no direct fallback is allowed.
func Check(ctx context.Context, c Config) process.Result {
	if c.Validate() != nil {
		return process.Result{State: "down", Message: errConfig.Error()}
	}
	if c.Timeout == 0 {
		c.Timeout = 5 * time.Second
	}
	result := process.RunConfig(ctx, c.Timeout, request{Type: Type, Config: c})
	// A stalled local manager proves no service state. The shared runner's
	// engine-timeout result is definitive for network probes, but not here.
	if result.State == "down" && (result.Message == "service check timed out" || result.Message == "service check canceled") {
		return process.Result{State: "pending", Message: "Service status unavailable"}
	}
	return result
}

func decodeRequest(raw []byte) (request, error) {
	var result request
	if len(raw) > 2048 {
		return result, errConfig
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return result, errConfig
	}
	seen := make(map[string]bool, 3)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || key != "type" && key != "name" && key != "timeout" {
			return result, errConfig
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return result, errConfig
		}
		switch key {
		case "type":
			err = json.Unmarshal(value, &result.Type)
		case "name":
			err = json.Unmarshal(value, &result.Name)
		case "timeout":
			err = json.Unmarshal(value, &result.Timeout)
		}
		if err != nil {
			return result, errConfig
		}
	}
	last, err := decoder.Token()
	if err != nil || last != json.Delim('}') || len(seen) != 3 || result.Type != Type || result.Timeout <= 0 || result.Config.Validate() != nil {
		return result, errConfig
	}
	if _, err := decoder.Token(); err != io.EOF {
		return result, errConfig
	}
	return result, nil
}

// WorkerHandler is selected only after the process runner has installed its
// OS limits. Each instance has private request state; failed validation clears it.
func WorkerHandler() process.Handler {
	var configuration Config
	valid := false
	return process.Handler{
		Validate: func(raw []byte) error {
			valid = false
			decoded, err := decodeRequest(raw)
			if err != nil {
				return err
			}
			configuration = decoded.Config
			valid = true
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

type serviceState uint8

const (
	unknown serviceState = iota
	running
	stopped
	missing
)

// CheckInWorker is exclusively a child-side hook, never a node-side fallback.
func CheckInWorker(ctx context.Context, c Config) process.Result {
	return check(ctx, c, platformQuery)
}

func check(ctx context.Context, c Config, query func(context.Context, string) serviceState) process.Result {
	if c.Validate() != nil {
		return process.Result{State: "down", Message: errConfig.Error()}
	}
	if c.Timeout == 0 {
		c.Timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	result := process.Result{State: "pending", Message: "Service status unavailable"}
	if ctx.Err() != nil {
		return result
	}
	state := query(ctx, c.Name)
	if ctx.Err() != nil {
		return result
	}
	switch state {
	case running:
		result.State, result.Message = "up", "Service is running"
	case stopped:
		result.State, result.Message = "down", "Service is not running"
	case missing:
		result.State, result.Message = "down", "Service is not installed"
	}
	return result
}
