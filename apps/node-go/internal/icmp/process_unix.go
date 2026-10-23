//go:build !windows

package icmp

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
)

func pingExecutable() (string, error) {
	paths := []string{"/bin/ping", "/usr/bin/ping"}
	if runtime.GOOS == "darwin" {
		paths = []string{"/sbin/ping"}
	}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return path, nil
		}
	}
	return "", errors.New("system ping unavailable")
}

func configureProcess(cmd *exec.Cmd) {}
