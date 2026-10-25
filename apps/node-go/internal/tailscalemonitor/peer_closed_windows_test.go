//go:build windows

package tailscalemonitor

import (
	"errors"
	"io"
	"syscall"
)

func peerClosed(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.WSAECONNRESET)
}
