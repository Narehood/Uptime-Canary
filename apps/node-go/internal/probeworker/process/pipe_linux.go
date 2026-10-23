//go:build linux

package process

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func newOutputPipe() (*os.File, *os.File, error) { return os.Pipe() }

func pipeReadable(file *os.File) (bool, error) {
	raw, err := file.SyscallConn()
	if err != nil {
		return false, err
	}
	var ready bool
	var pollErr error
	err = raw.Control(func(fd uintptr) {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		_, pollErr = unix.Poll(fds, 0)
		if errors.Is(pollErr, unix.EINTR) {
			ready, pollErr = true, nil
			return
		}
		if fds[0].Revents&unix.POLLNVAL != 0 {
			pollErr = errProtocol
			return
		}
		ready = fds[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) != 0
	})
	if err != nil {
		return false, err
	}
	return ready, pollErr
}
