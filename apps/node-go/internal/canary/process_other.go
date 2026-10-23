//go:build !windows

package canary

import "os/exec"

func hideProcess(cmd *exec.Cmd) {}
