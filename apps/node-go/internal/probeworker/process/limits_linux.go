//go:build linux

package process

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

type linuxGuard struct{}

func prepareProcess(cmd *exec.Cmd) (processGuard, error) {
	// Linux ties Pdeathsig to the creating OS thread, not just the process.
	// Keep that thread alive until the child is reaped.
	runtime.LockOSThread()
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	return linuxGuard{}, nil
}
func (linuxGuard) attach(*os.Process) error { return nil }
func (linuxGuard) close()                   { runtime.UnlockOSThread() }

func applyChildLimits() error {
	var u unix.Utsname
	if unix.Uname(&u) != nil {
		return errProtocol
	}
	release := make([]byte, 0, len(u.Release))
	for _, b := range u.Release {
		if b == 0 {
			break
		}
		release = append(release, byte(b))
	}
	var major, minor int
	if n, _ := fmt.Sscanf(string(release), "%d.%d", &major, &minor); n != 2 || major < 4 || major == 4 && minor < 7 {
		return errProtocol
	}
	var signal int32
	// Pass the pointer directly to the syscall intrinsic so Go keeps it valid
	// across a possible stack growth (a uintptr-only wrapper would lose it).
	_, _, errno := syscall.Syscall6(syscall.SYS_PRCTL, unix.PR_GET_PDEATHSIG, uintptr(unsafe.Pointer(&signal)), 0, 0, 0, 0)
	if errno != 0 || signal != int32(syscall.SIGKILL) {
		return errProtocol
	}
	var old unix.Rlimit
	if unix.Getrlimit(unix.RLIMIT_DATA, &old) != nil {
		return errProtocol
	}
	limit := uint64(hardMemoryBytes)
	if old.Max < limit {
		limit = old.Max
	}
	if old.Cur < limit {
		limit = old.Cur
	}
	if limit == 0 {
		return errProtocol
	}
	if unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{}) != nil {
		return errProtocol
	}
	if unix.Setrlimit(unix.RLIMIT_DATA, &unix.Rlimit{Cur: limit, Max: limit}) != nil {
		return errProtocol
	}
	var got unix.Rlimit
	if unix.Getrlimit(unix.RLIMIT_DATA, &got) != nil || got.Cur != limit || got.Max != limit {
		return errProtocol
	}
	return nil
}

func prepareTypedProcess(cmd *exec.Cmd, _ string) (processGuard, error) { return prepareProcess(cmd) }
