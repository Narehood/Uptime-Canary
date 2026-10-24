//go:build windows

// Source-only candidate: root may overlay this into internal/pm2monitor.
// Requires newWindowsPipeConn(h windows.Handle) (net.Conn, error).
// That PRIVATE helper consumes h on every outcome and admits only pollable
// overlapped pipe handles. Production must verify the peer BEFORE calling it.
// These tests exercise transport mechanics, not account authentication.
package pm2monitor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func candidatePM2Pipe(t *testing.T, overlapped bool) windows.Handle {
	t.Helper()
	// This name is exclusively a private fixture. Production has no path option.
	name, err := windows.UTF16PtrFromString(fmt.Sprintf(
		`\\.\pipe\canary-pm2-fixture-%d-%d`, os.Getpid(), time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	server, err := windows.CreateNamedPipe(name,
		windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_OVERLAPPED|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		1, 128, 128, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(server) })
	flags := uint32(windows.SECURITY_SQOS_PRESENT | windows.SECURITY_IDENTIFICATION)
	if overlapped {
		flags |= windows.FILE_FLAG_OVERLAPPED
	}
	// A client can connect between CreateNamedPipe and ConnectNamedPipe.
	// The server stays silent, with no outstanding native OVERLAPPED structure.
	client, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE,
		0, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		t.Fatal(err)
	}
	return client // ownership immediately passes to newWindowsPipeConn
}

func TestPM2WindowsCandidateDeadline(t *testing.T) {
	conn, err := newWindowsPipeConn(candidatePM2Pipe(t, true))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = conn.SetReadDeadline(time.Now().Add(40 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	n, err := conn.Read(b[:])
	if n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("silent pipe read = (%d, %v), want deadline", n, err)
	}
}

func TestPM2WindowsCandidateCloseCancellation(t *testing.T) {
	conn, err := newWindowsPipeConn(candidatePM2Pipe(t, true))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// A backstop also bounds a failed Close cancellation implementation.
	if err = conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = conn.Close()
		close(closed)
	})
	defer stop()
	started := make(chan struct{})
	readDone := make(chan error, 1)
	go func() {
		close(started)
		var b [1]byte
		_, readErr := conn.Read(b[:])
		readDone <- readErr
	}()
	<-started
	cancel()
	select {
	case err = <-readDone:
		if err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
			t.Errorf("read after cancellation = %v, want closed pipe", err)
		}
	case <-time.After(time.Second):
		t.Error("read did not return promptly after Close")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Error("context Close callback did not finish")
	}
}

func TestPM2WindowsCandidateRejectSynchronous(t *testing.T) {
	h := candidatePM2Pipe(t, false)
	conn, err := newWindowsPipeConn(h)
	if conn != nil {
		_ = conn.Close()
		t.Fatal("synchronous pipe was admitted")
	}
	if err == nil {
		t.Fatal("synchronous pipe rejection returned no error")
	}
	var flags uint32
	if err = windows.GetNamedPipeInfo(h, &flags, nil, nil, nil); !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
		t.Fatalf("rejected handle was not closed: %v", err)
	}
}
