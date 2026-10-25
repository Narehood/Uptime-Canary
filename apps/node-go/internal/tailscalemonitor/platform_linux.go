//go:build linux

package tailscalemonitor

import (
	"context"
	"os/exec"
	"runtime"
	"syscall"
)

func platformRun(ctx context.Context, args []string) commandResult {
	return runInstalled(ctx, []string{"/usr/bin/tailscale", "/usr/local/bin/tailscale"}, args)
}

func containCommand(cmd *exec.Cmd) func() {
	// Keep the creating thread alive until Wait. Forced worker death must kill
	// the client even when no Go cancellation callback or defer can execute.
	runtime.LockOSThread()
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	return runtime.UnlockOSThread
}
