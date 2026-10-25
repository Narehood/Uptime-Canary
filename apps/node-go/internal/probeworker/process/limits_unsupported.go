//go:build !linux && !windows

package process

import "os/exec"

func prepareProcess(*exec.Cmd) (processGuard, error) { return nil, errProtocol }
func applyChildLimits() error                        { return errProtocol }

func prepareTypedProcess(cmd *exec.Cmd, _ string) (processGuard, error) { return prepareProcess(cmd) }
