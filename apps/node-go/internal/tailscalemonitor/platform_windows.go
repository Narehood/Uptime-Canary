//go:build windows

package tailscalemonitor

import (
	"context"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

func platformRun(ctx context.Context, args []string) commandResult {
	path, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return commandResult{err: err}
	}
	return runInstalled(ctx, []string{filepath.Join(path, "Tailscale", "tailscale.exe")}, args)
}

func containCommand(cmd *exec.Cmd) func() {
	// Descendants inherit the existing worker job; never request breakaway.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return func() {}
}
