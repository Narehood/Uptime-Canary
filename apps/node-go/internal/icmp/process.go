package icmp

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const outputLimit = 16 << 10

type execution struct {
	stdout   string
	stderr   string
	err      error
	exitCode int
	started  bool
	overflow bool
}

// Both streams share one bound. Overflow cancels the process, while Write still
// drains/discards without blocking or causing an unbounded allocation.
type outputBudget struct {
	mu        sync.Mutex
	remaining int
	overflow  bool
	cancel    context.CancelFunc
}

type outputStream struct {
	budget *outputBudget
	data   strings.Builder
}

func (w *outputStream) Write(p []byte) (int, error) {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	n := min(len(p), w.budget.remaining)
	w.data.Write(p[:n])
	w.budget.remaining -= n
	if n != len(p) {
		w.budget.overflow = true
		w.budget.cancel()
	}
	return len(p), nil
}

func execute(ctx context.Context, args []string) execution {
	path, err := pingExecutable()
	if err != nil {
		return execution{err: err, exitCode: -1}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	b := &outputBudget{remaining: outputLimit, cancel: cancel}
	out, diagnostic := &outputStream{budget: b}, &outputStream{budget: b}
	cmd := exec.CommandContext(ctx, path, args...)
	configureProcess(cmd)
	cmd.Env = pingEnvironment(os.Environ())
	cmd.Stdout, cmd.Stderr = out, diagnostic
	// Bound pipe-drain cleanup even if a broken utility keeps a handle open.
	cmd.WaitDelay = 100 * time.Millisecond
	if err := cmd.Start(); err != nil {
		return execution{err: err, exitCode: -1}
	}
	err = cmd.Wait()
	return execution{stdout: out.data.String(), stderr: diagnostic.data.String(), err: err,
		exitCode: cmd.ProcessState.ExitCode(), started: true, overflow: b.overflow}
}

func pingEnvironment(parent []string) []string {
	// Never give the child application credentials, loader hooks, proxy settings,
	// locale overrides, or arbitrary inherited environment values.
	env := []string{"LC_ALL=C", "LANG=C", "LANGUAGE=C"}
	for _, item := range parent {
		key, _, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		switch strings.ToUpper(key) {
		case "PATH", "SYSTEMROOT", "WINDIR":
			env = append(env, item)
		}
	}
	return env
}
