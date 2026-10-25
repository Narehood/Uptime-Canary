package tailscalemonitor

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	"uptime-canary/apps/node-go/internal/probeworker/process"
)

type fixtureRequest struct {
	Type string `json:"type"`
	Config
	Fixture string `json:"fixture"`
	Ready   string `json:"ready,omitempty"`
}

// The test binary alone can substitute its own tiny client. The production
// handler still rejects fixture, ready, command, socket and environment fields.
func fixtureWorkerDispatch() bool {
	var request fixtureRequest
	valid := false
	if handled, code := process.Dispatch(os.Args[1:], process.Handler{
		Validate: func(raw []byte) error {
			valid = false
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &request) != nil || json.Unmarshal(raw, &fields) != nil {
				return errConfig
			}
			if request.Fixture != "pong" && request.Fixture != "timeout" && request.Fixture != "flood" && request.Fixture != "stall" {
				return errConfig
			}
			if request.Ready != "" {
				host, _, err := net.SplitHostPort(request.Ready)
				if err != nil || host != "127.0.0.1" {
					return errConfig
				}
			}
			delete(fields, "fixture")
			delete(fields, "ready")
			base, err := json.Marshal(fields)
			if err != nil || WorkerHandler().Validate(base) != nil {
				return errConfig
			}
			valid = true
			return nil
		},
		Check: func(ctx context.Context) process.Result {
			if !valid {
				return unavailable()
			}
			return check(ctx, request.Config, func(ctx context.Context, _ []string) commandResult {
				path, err := os.Executable()
				if err != nil {
					return commandResult{err: err}
				}
				args := []string{"--tailscale-client-fixture", request.Fixture}
				if request.Ready != "" {
					args = append(args, request.Ready)
				}
				return runCommand(ctx, path, args)
			})
		},
	}); handled {
		os.Exit(code)
	}
	return false
}

func requireWorkerPlatform(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("native worker hard controls require Linux or Windows")
	}
}

func TestActualWorkerAndClientBoundary(t *testing.T) {
	requireWorkerPlatform(t)
	for _, item := range []struct{ fixture, state, message string }{
		{"pong", "up", Replied}, {"timeout", "down", NoReply}, {"flood", "pending", Unavailable},
	} {
		t.Run(item.fixture, func(t *testing.T) {
			request := fixtureRequest{Type: Type, Config: Config{Name: "private-fixture-peer", Timeout: 3 * time.Second}, Fixture: item.fixture}
			r := workerResult(process.RunConfig(context.Background(), request.Timeout, request))
			if r.State != item.state || r.Message != item.message || item.fixture == "pong" && r.LatencyMs != 2.5 {
				t.Fatalf("native worker/client boundary misclassified its fixed result: %+v", r)
			}
		})
	}
	raw, _ := json.Marshal(fixtureRequest{Type: Type, Config: Config{Name: "private-peer", Timeout: time.Second}, Fixture: "pong"})
	if WorkerHandler().Validate(raw) == nil {
		t.Fatal("private client seam became a production option")
	}
}

type blockedWorker struct {
	result process.Result
	done   chan struct{}
	cancel context.CancelFunc
	client net.Conn
}

func startBlockedWorker(t *testing.T) *blockedWorker {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if listener.SetDeadline(time.Now().Add(3*time.Second)) != nil {
		t.Fatal("private readiness deadline unavailable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &blockedWorker{done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(w.done)
		c := Config{Name: "private-fixture-peer", Timeout: 10 * time.Second}
		w.result = workerResult(process.RunConfig(ctx, c.Timeout, fixtureRequest{Type: Type, Config: c, Fixture: "stall", Ready: listener.Addr().String()}))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-w.done:
		case <-time.After(time.Second):
			t.Error("private worker was not joined")
		}
		if w.client != nil {
			w.client.Close()
		}
	})
	w.client, err = listener.Accept()
	if err != nil {
		t.Fatal("private client did not start", err)
	}
	if w.client.SetReadDeadline(time.Now().Add(3*time.Second)) != nil {
		t.Fatal("private client read deadline unavailable")
	}
	var ready [1]byte
	if _, err := io.ReadFull(w.client, ready[:]); err != nil || ready[0] != 'R' {
		t.Fatal("private client readiness missing", err)
	}
	return w
}

func TestCancellationKillsClientAndReleasesOccupiedSlot(t *testing.T) {
	requireWorkerPlatform(t)
	first, other := startBlockedWorker(t), startBlockedWorker(t)
	c := Config{Name: "private-fixture-peer", Timeout: 3 * time.Second}
	if r := process.RunConfig(context.Background(), c.Timeout, fixtureRequest{Type: Type, Config: c, Fixture: "pong"}); r.State != "pending" || r.Message != "service worker capacity unavailable" {
		t.Fatal("both worker slots were not occupied", r)
	}
	first.cancel()
	select {
	case <-first.done:
		if first.result.State != "pending" {
			t.Fatal("canceled worker became an outage", first.result)
		}
	case <-time.After(time.Second):
		t.Fatal("worker/client cancellation did not finish")
	}
	first.client.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	// Abrupt Windows job termination closes TCP with WSAECONNRESET; Linux
	// can report EOF. A deadline/timeout is deliberately not acceptance.
	if _, err := first.client.Read(b[:]); !peerClosed(err) {
		t.Fatal("client survived its stopped worker", err)
	}
	select {
	case <-other.done:
		t.Fatal("unrelated occupied worker was also stopped")
	default:
	}
	r := workerResult(process.RunConfig(context.Background(), c.Timeout, fixtureRequest{Type: Type, Config: c, Fixture: "pong"}))
	if r.State != "up" || r.Message != Replied {
		t.Fatal("canceled slot was not reusable while the other slot stayed occupied", r)
	}
}
