//go:build windows

package process

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var peekNamedPipe = windows.NewLazySystemDLL("kernel32.dll").NewProc("PeekNamedPipe")

// os.Pipe uses synchronous anonymous handles on Windows. PeekNamedPipe can
// block behind their pending ReadFile. Use an overlapped parent reader and a
// normal child writer; os.NewFile detects the reader's asynchronous mode.
func newOutputPipe() (*os.File, *os.File, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, nil, err
	}
	name, err := windows.UTF16PtrFromString(`\\.\pipe\canary-probe-output-` + hex.EncodeToString(token[:]))
	if err != nil {
		return nil, nil, err
	}
	r, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_INBOUND|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 4096, 4096, 0, nil)
	if err != nil {
		return nil, nil, err
	}
	w, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		_ = windows.CloseHandle(r)
		return nil, nil, err
	}
	// CreateFile has connected the sole client already. No pending connect
	// operation, OVERLAPPED buffer, extra goroutine, or filesystem file exists.
	return os.NewFile(uintptr(r), "probe-output-reader"), os.NewFile(uintptr(w), "probe-output-writer"), nil
}

func pipeReadable(file *os.File) (bool, error) {
	raw, err := file.SyscallConn()
	if err != nil {
		return false, err
	}
	var ready bool
	var peekErr error
	var available uint32
	var pin runtime.Pinner
	pin.Pin(&available)
	defer pin.Unpin()
	err = raw.Control(func(fd uintptr) {
		// Our output pipes use asynchronous readers, so peeking does not wait
		// behind the collector's pending ReadFile. Never copy payload data.
		ok, _, callErr := peekNamedPipe.Call(fd, 0, 0, 0, uintptr(unsafe.Pointer(&available)), 0)
		if ok != 0 {
			ready = available > 0
			return
		}
		if errors.Is(callErr, windows.ERROR_BROKEN_PIPE) {
			ready = true
			return
		}
		peekErr = callErr
	})
	if err != nil {
		return false, err
	}
	return ready, peekErr
}
