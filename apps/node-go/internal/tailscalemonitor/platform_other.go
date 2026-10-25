//go:build !linux && !windows

package tailscalemonitor

import (
	"context"
	"errors"
	"os/exec"
)

func platformRun(context.Context, []string) commandResult {
	return commandResult{err: errors.New("Tailscale check platform unavailable")}
}

func containCommand(*exec.Cmd) func() { return func() {} }
