//go:build windows

package pm2monitor

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func workerFixtureListener(t *testing.T) (Config, string, func() (net.Conn, error)) {
	t.Helper()
	endpoint := fmt.Sprintf(`\\.\pipe\canary-pm2-worker-%d-%d`, os.Getpid(), time.Now().UnixNano())
	name, err := windows.UTF16PtrFromString(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid.String()
	sd, err := windows.SecurityDescriptorFromString("O:" + sid + "D:(A;;GA;;;" + sid + ")(A;;GA;;;SY)(A;;GA;;;BA)")
	if err != nil {
		t.Fatal(err)
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	handle, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS, 1, 8192, 8192, 0, &attributes)
	if err != nil {
		t.Fatal(err)
	}
	var connection net.Conn
	t.Cleanup(func() {
		if connection != nil {
			connection.Close()
		} else if handle != windows.InvalidHandle {
			windows.CloseHandle(handle)
		}
	})
	accept := func() (net.Conn, error) {
		event, err := windows.CreateEvent(nil, 1, 0, nil)
		if err != nil {
			return nil, err
		}
		defer windows.CloseHandle(event)
		overlap := windows.Overlapped{HEvent: event}
		err = windows.ConnectNamedPipe(handle, &overlap)
		if err == windows.ERROR_IO_PENDING {
			result, waitErr := windows.WaitForSingleObject(event, 1500)
			var transferred uint32
			if waitErr != nil || result != windows.WAIT_OBJECT_0 {
				windows.CancelIoEx(handle, &overlap)
				_ = windows.GetOverlappedResult(handle, &overlap, &transferred, true)
				return nil, os.ErrDeadlineExceeded
			}
			err = windows.GetOverlappedResult(handle, &overlap, &transferred, false)
		}
		if err != nil && err != windows.ERROR_PIPE_CONNECTED {
			return nil, err
		}
		connection, err = newWindowsPipeConn(handle)
		handle = windows.InvalidHandle // newWindowsPipeConn consumed it, even on error.
		return connection, err
	}
	return Config{Name: "fixture-app", Timeout: 2 * time.Second, Options: Options{Transport: "windows-pipe"}}, endpoint, accept
}

func workerFixtureDial(ctx context.Context, c Config, endpoint string) (net.Conn, error) {
	if !strings.HasPrefix(endpoint, `\\.\pipe\canary-pm2-worker-`) {
		return nil, errConfig
	}
	return dialWindowsPipe(ctx, c, endpoint)
}
