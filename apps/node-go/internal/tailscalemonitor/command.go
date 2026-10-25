package tailscalemonitor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

type boundedOutput struct {
	// Do not embed Buffer: its promoted ReadFrom would let io.Copy bypass Write.
	buffer   bytes.Buffer
	cancel   context.CancelFunc
	overflow bool
}

func (b *boundedOutput) Write(raw []byte) (int, error) {
	if len(raw) > 4096-b.buffer.Len() {
		b.overflow = true
		b.cancel()
		return 0, errors.New("client output exceeds limit")
	}
	return b.buffer.Write(raw)
}

func runInstalled(ctx context.Context, paths []string, args []string) commandResult {
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		return runCommand(ctx, path, args)
	}
	return commandResult{err: errors.New("Tailscale client unavailable")}
}

// runCommand consumes and reaps the one fixed client process before returning.
// Its inherited resource controls are the disposable worker's original limits.
func runCommand(ctx context.Context, path string, args []string) commandResult {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout, stderr := &boundedOutput{cancel: cancel}, &boundedOutput{cancel: cancel}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"LC_ALL=C", "LANG=C", "NO_COLOR=1"}
	if root := os.Getenv("SystemRoot"); root != "" {
		cmd.Env = append(cmd.Env, "SystemRoot="+root)
	}
	cmd.WaitDelay = 250 * time.Millisecond
	release := containCommand(cmd)
	defer release()
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	exit := -1
	if cmd.ProcessState != nil {
		exit = cmd.ProcessState.ExitCode()
	}
	var exited *exec.ExitError
	if errors.As(err, &exited) {
		err = nil // An ordinary exit is admitted only by the exact output parser.
	}
	if ctx.Err() != nil || stdout.overflow || stderr.overflow {
		err = errors.New("Tailscale client interrupted")
	}
	return commandResult{stdout.buffer.Bytes(), stderr.buffer.Bytes(), exit, err}
}
