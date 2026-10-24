//go:build linux

package systemservice

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
)

func platformQuery(ctx context.Context, name string) serviceState {
	for _, path := range []string{"/usr/bin/systemctl", "/bin/systemctl"} {
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return querySystemd(ctx, name, path)
		}
	}
	return unknown
}

func querySystemd(ctx context.Context, name, executable string) serviceState {
	if !strings.HasSuffix(name, ".service") {
		name += ".service"
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "show", "--no-pager", "--all", "--property=LoadState,ActiveState", "--", name)
	// Linux parent-death signals belong to the creating OS thread. Keep that
	// thread alive through Wait; a forcibly stopped worker must also stop its
	// helper even when it cannot run context cancellation or deferred cleanup.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	command.Env = []string{"LC_ALL=C", "LANG=C", "SYSTEMD_COLORS=0"}
	command.Dir = "/"
	reader, writer, err := os.Pipe()
	if err != nil {
		return unknown
	}
	defer reader.Close()
	command.Stdout = writer
	// Stderr is /dev/null: service names, paths and manager diagnostics never
	// become observations. The deadline bounds a noisy or unavailable helper.
	if err := command.Start(); err != nil {
		writer.Close()
		return unknown
	}
	writer.Close()
	type output struct {
		raw []byte
		err error
	}
	done := make(chan output, 1)
	go func() {
		raw, err := io.ReadAll(io.LimitReader(reader, 4097))
		if len(raw) > 4096 || err != nil {
			cancel()
		}
		done <- output{raw, err}
	}()
	err = command.Wait()
	var captured output
	select {
	case captured = <-done:
	case <-ctx.Done():
		reader.Close()
		<-done
		return unknown
	}
	if ctx.Err() != nil || captured.err != nil || len(captured.raw) > 4096 {
		return unknown
	}
	exit := 0
	if err != nil {
		var exited *exec.ExitError
		if !errors.As(err, &exited) {
			return unknown
		}
		exit = exited.ExitCode()
	}
	return systemdState(captured.raw, exit)
}

func systemdState(raw []byte, exit int) serviceState {
	if len(raw) == 0 || len(raw) > 4096 || strings.ContainsAny(string(raw), "\x00\r") {
		return unknown
	}
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found || key != "LoadState" && key != "ActiveState" {
			return unknown
		}
		if _, duplicate := fields[key]; duplicate {
			return unknown
		}
		fields[key] = value
	}
	if len(fields) != 2 {
		return unknown
	}
	if fields["LoadState"] == "not-found" && fields["ActiveState"] == "inactive" && (exit == 0 || exit == 1) {
		return missing
	}
	if exit != 0 || fields["LoadState"] != "loaded" && fields["LoadState"] != "masked" {
		return unknown
	}
	switch fields["ActiveState"] {
	case "active", "reloading":
		return running
	case "inactive", "failed", "activating", "deactivating", "maintenance":
		return stopped
	default:
		return unknown
	}
}
