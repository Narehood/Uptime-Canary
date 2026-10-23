//go:build linux

package systemservice

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func init() {
	if len(os.Args) == 3 && os.Args[1] == "--system-service-fixture-parent" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		querySystemd(ctx, "fixture", os.Args[2])
		os.Exit(0)
	}
}

func TestSystemServiceSystemdExactPropertiesAndStates(t *testing.T) {
	for _, test := range []struct {
		load, active string
		exit         int
		expected     serviceState
	}{
		{"loaded", "active", 0, running}, {"masked", "active", 0, running}, {"loaded", "reloading", 0, running},
		{"loaded", "inactive", 0, stopped}, {"loaded", "failed", 0, stopped}, {"loaded", "activating", 0, stopped},
		{"loaded", "deactivating", 0, stopped}, {"loaded", "maintenance", 0, stopped},
		{"not-found", "inactive", 0, missing}, {"not-found", "inactive", 1, missing},
		{"loaded", "active", 1, unknown}, {"loaded", "future-state", 0, unknown}, {"error", "active", 0, unknown},
	} {
		raw := []byte("LoadState=" + test.load + "\nActiveState=" + test.active + "\n")
		if result := systemdState(raw, test.exit); result != test.expected {
			t.Fatal(test, result)
		}
	}
	for _, invalid := range []string{"", "LoadState=loaded\n", "ActiveState=active\n", "LoadState=loaded\r\nActiveState=active\r\n",
		"LoadState=loaded\nActiveState=active\nActiveState=inactive\n", "LoadState=loaded\nActiveState=active\nDescription=private\n",
		"LoadState=loaded\nActiveState=active\x00\n", strings.Repeat("x", 4097)} {
		if systemdState([]byte(invalid), 0) != unknown {
			t.Fatalf("malformed property output accepted: %q", invalid)
		}
	}
}

func systemctlFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "systemctl-fixture")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSystemServiceSystemdUsesLiteralArgumentsMinimalEnvironmentAndBoundedOutput(t *testing.T) {
	t.Setenv("DBUS_SYSTEM_BUS_ADDRESS", "fixture-must-not-be-inherited")
	path := systemctlFixture(t, `[ "$#" -eq 6 ] || exit 9
[ "$1" = show ] && [ "$2" = --no-pager ] && [ "$3" = --all ] || exit 9
[ "$4" = --property=LoadState,ActiveState ] && [ "$5" = -- ] || exit 9
[ "$6" = fixture@one.service ] || exit 9
[ -z "${DBUS_SYSTEM_BUS_ADDRESS+x}" ] || exit 9
printf 'LoadState=loaded\nActiveState=active\n'`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if result := querySystemd(ctx, "fixture@one", path); result != running {
		t.Fatal(result)
	}
	missingPath := systemctlFixture(t, `printf 'LoadState=not-found\nActiveState=inactive\n'; exit 1`)
	if result := querySystemd(ctx, "fixture", missingPath); result != missing {
		t.Fatal(result)
	}
	excessive := systemctlFixture(t, `i=0; while [ "$i" -lt 1000 ]; do printf 'xxxxxxxxxxxxxxxx\n'; i=$((i+1)); done`)
	if result := querySystemd(ctx, "fixture", excessive); result != unknown {
		t.Fatal("excessive output accepted", result)
	}
	if result := querySystemd(ctx, "fixture", filepath.Join(t.TempDir(), "absent")); result != unknown {
		t.Fatal(result)
	}
}

func TestSystemServiceSystemdDeadlineTerminatesOwnHelper(t *testing.T) {
	path := systemctlFixture(t, `while :; do :; done`)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	if result := querySystemd(ctx, "fixture", path); result != unknown {
		t.Fatal(result)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatal("helper deadline was not enforced")
	}
}

func TestSystemServiceSystemdHelperStopsWhenItsWorkerDies(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "helper.pid")
	quoted := "'" + strings.ReplaceAll(pidFile, "'", "'\\''") + "'"
	path := systemctlFixture(t, `printf '%s\n' "$$" > `+quoted+"\nwhile :; do :; done")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(executable, "--system-service-fixture-parent", path)
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Process.Kill(); _ = parent.Wait() }()
	var pid int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
			if pid > 0 {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pid <= 0 {
		t.Fatal("private helper never started")
	}
	// Reclaim only this fixture's helper if a future mutation removes Pdeathsig.
	defer func() {
		raw, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
		if strings.Contains(string(raw), path) {
			if helper, err := os.FindProcess(pid); err == nil {
				_ = helper.Kill()
			}
		}
	}()
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
		// A killed orphan may briefly await the OS init's reaper. It must never
		// continue running after the worker's creating thread has exited.
		_, status, found := strings.Cut(string(raw), ") ")
		if os.IsNotExist(err) || found && strings.HasPrefix(status, "Z ") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("helper continued executing after worker death")
}
