//go:build linux

// Source-only candidate: root may overlay this into internal/pm2monitor.
// Uses only the agreed dialManager(ctx context.Context, c Config) API.
// Root must supply a short private TMPDIR if its normal scratch path is long.
// Same-process fixtures cover positive UID admission, not cross-account denial.
package pm2monitor

import (
	"context"
	"errors"
	"net"
	"os"
	"path"
	"testing"
	"time"
)

func candidatePM2Unix(t *testing.T) Config {
	t.Helper()
	home, err := os.MkdirTemp("", "cpm2-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(home); err != nil {
			t.Error(err)
		}
	})
	endpoint := path.Join(home, "rpc.sock")
	if len(endpoint) > 107 {
		t.Fatal("fixture needs a shorter private TMPDIR; socket path exceeds 107 bytes")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: endpoint, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	// The listening socket's same-process peer credentials are available even
	// before Accept. Leave the queued connection silent; create no goroutines.
	return Config{Name: "fixture", Timeout: time.Second,
		Options: Options{Transport: "unix", Home: home}}
}

func TestPM2UnixCandidateDeadline(t *testing.T) {
	c := candidatePM2Unix(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := dialManager(ctx, c)
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
		t.Fatalf("silent socket read = (%d, %v), want deadline", n, err)
	}
}

func TestPM2UnixCandidateCanceledDial(t *testing.T) {
	c := candidatePM2Unix(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn, err := dialManager(ctx, c)
	if conn != nil {
		_ = conn.Close()
		t.Fatal("canceled dial returned a connection")
	}
	if err == nil {
		t.Fatal("canceled dial returned no error")
	}
}

func TestPM2UnixCandidateTransportMismatch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c := Config{Name: "fixture", Timeout: time.Second,
		Options: Options{Transport: "windows-pipe"}}
	conn, err := dialManager(ctx, c)
	if conn != nil {
		_ = conn.Close()
		t.Fatal("Linux admitted the Windows transport")
	}
	if err == nil {
		t.Fatal("transport mismatch returned no error")
	}
}
