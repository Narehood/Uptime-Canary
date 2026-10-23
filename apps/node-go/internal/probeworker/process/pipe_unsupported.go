//go:build !linux && !windows

package process

import "os"

func newOutputPipe() (*os.File, *os.File, error) { return nil, nil, errProtocol }

func pipeReadable(*os.File) (bool, error) { return false, errProtocol }
