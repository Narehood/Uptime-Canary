package icmp

import (
	"errors"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

func pingExecutable() (string, error) {
	// Ask Windows, independent of PATH, working directory, and SystemRoot input.
	var directory [32768]uint16
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetSystemDirectoryW")
	n, _, _ := proc.Call(uintptr(unsafe.Pointer(&directory[0])), uintptr(len(directory)))
	if n == 0 || n >= uintptr(len(directory)) {
		return "", errors.New("system ping unavailable")
	}
	return filepath.Join(syscall.UTF16ToString(directory[:n]), "PING.EXE"), nil
}

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
