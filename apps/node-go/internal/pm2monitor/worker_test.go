package pm2monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	"uptime-canary/apps/node-go/internal/probeworker/process"
)

type workerFixtureRequest struct {
	Type string `json:"type"`
	Config
	Endpoint string `json:"fixtureEndpoint"`
}

// Only this test executable accepts a private endpoint seam. The release CLI
// dispatches the unchanged strict WorkerHandler, which rejects this field.
func TestMain(m *testing.M) {
	var request workerFixtureRequest
	valid := false
	if handled, code := process.Dispatch(os.Args[1:], process.Handler{
		Validate: func(raw []byte) error {
			valid = false
			fields, err := objectFields(raw, 8192, []string{"type", "name", "timeout", "options", "fixtureEndpoint"})
			if err != nil || len(fields) != 5 || json.Unmarshal(raw, &request) != nil || request.Endpoint == "" || len(request.Endpoint) > 256 {
				return errConfig
			}
			delete(fields, "fixtureEndpoint")
			base, err := json.Marshal(fields)
			if err != nil {
				return errConfig
			}
			handler := WorkerHandler()
			if handler.Validate(base) != nil {
				return errConfig
			}
			valid = true
			return nil
		},
		Check: func(ctx context.Context) process.Result {
			if !valid {
				return unavailable()
			}
			return check(ctx, request.Config, func(ctx context.Context, c Config) (net.Conn, error) {
				return workerFixtureDial(ctx, c, request.Endpoint)
			})
		},
	}); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func TestPM2RealBoundedWorker(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("native resource controls unavailable")
	}
	for _, item := range []struct{ name, status, state, message string }{
		{"online", "online", "up", "PM2 process is online"},
		{"stopped", "stopped", "down", "PM2 process is not online"},
		{"unknown", "future", "pending", "PM2 status unavailable"},
		{"malformed", "malformed", "pending", "PM2 status unavailable"},
	} {
		t.Run(item.name, func(t *testing.T) {
			c, endpoint, accept := workerFixtureListener(t)
			startWorkerFixture(t, accept, item.status, nil)
			r := workerResult(process.RunConfig(context.Background(), c.Timeout, workerFixtureRequest{Type, c, endpoint}))
			pm2ContractResult(t, r, item.state, item.message)
		})
	}
}

func TestPM2WorkerCancellationReleasesCapacity(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("native resource controls unavailable")
	}
	// Keep the other admission slot occupied through the replacement check.
	// Merely starting one replacement would not detect a leaked first slot.
	blocked, blockedEndpoint, blockedAccept := workerFixtureListener(t)
	blocked.Timeout = 10 * time.Second
	blockedReady := make(chan struct{})
	startWorkerFixture(t, blockedAccept, "stall", blockedReady)
	blockedCtx, stopBlocked := context.WithCancel(context.Background())
	blockedResult := make(chan process.Result, 1)
	go func() {
		blockedResult <- workerResult(process.RunConfig(blockedCtx, blocked.Timeout, workerFixtureRequest{Type, blocked, blockedEndpoint}))
	}()
	defer func() { stopBlocked(); <-blockedResult }()
	select {
	case <-blockedReady:
	case <-time.After(3 * time.Second):
		t.Fatal("other admission slot did not reach manager I/O")
	}
	c, endpoint, accept := workerFixtureListener(t)
	ready := make(chan struct{})
	done := startWorkerFixture(t, accept, "stall", ready)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan process.Result, 1)
	go func() {
		result <- workerResult(process.RunConfig(ctx, c.Timeout, workerFixtureRequest{Type, c, endpoint}))
	}()
	select {
	case <-ready:
	case r := <-result:
		t.Fatalf("worker ended before real manager I/O: %+v", r)
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not reach private manager")
	}
	if rejected := process.RunConfig(context.Background(), c.Timeout, workerFixtureRequest{Type, c, endpoint}); rejected.State != "pending" || rejected.Message != "service worker capacity unavailable" {
		t.Fatal("both real worker slots were not occupied", rejected)
	}
	cancel()
	select {
	case r := <-result:
		pm2ContractResult(t, r, "pending", "PM2 status unavailable")
	case <-time.After(time.Second):
		t.Fatal("worker cancellation did not finish")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("manager connection survived worker cancellation")
	}
	// Admission must be usable again after the cancelled process was reaped.
	next, endpoint, accept := workerFixtureListener(t)
	startWorkerFixture(t, accept, "online", nil)
	r := workerResult(process.RunConfig(context.Background(), next.Timeout, workerFixtureRequest{Type, next, endpoint}))
	pm2ContractResult(t, r, "up", "PM2 process is online")
}

func startWorkerFixture(t *testing.T, accept func() (net.Conn, error), status string, ready chan struct{}) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	var result error
	go func() { result = workerFixtureServe(accept, status, ready); close(done) }()
	// Registered after the native listener cleanup: join the fixture before
	// transferring or closing its raw server handle on any test failure path.
	t.Cleanup(func() {
		select {
		case <-done:
			if result != nil {
				t.Error("private worker fixture:", result)
			}
		case <-time.After(5 * time.Second):
			t.Error("private worker fixture did not stop")
		}
	})
	return done
}

func TestPM2ProductionHandlerRejectsFixtureEndpoint(t *testing.T) {
	config := pm2ContractConfig()
	raw, err := json.Marshal(workerFixtureRequest{Type, config, "private-test-only"})
	if err != nil {
		t.Fatal(err)
	}
	handler := WorkerHandler()
	if handler.Validate(raw) == nil {
		t.Fatal("private transport seam became a production option")
	}
}

func workerFixtureServe(accept func() (net.Conn, error), status string, ready chan struct{}) error {
	conn, err := accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	if conn.SetDeadline(time.Now().Add(4*time.Second)) != nil {
		return fmt.Errorf("fixture deadline unavailable")
	}
	previous := ""
	for _, method := range []string{"getVersion", "getMonitorData"} {
		id, err := pm2ContractReadCall(conn, method, previous)
		if err != nil {
			return err
		}
		previous = id
		body := `{"args":["6.0.8"]}`
		if method == "getMonitorData" {
			if ready != nil {
				close(ready)
			}
			if status == "stall" {
				return pm2ContractEnd(conn)
			}
			entry, _ := json.Marshal(map[string]any{"name": "fixture-app", "pm_id": 0, "pm2_env": map[string]any{
				"status": status, "namespace": "default", "env": map[string]string{"PRIVATE": "never-export-this"}}})
			body = pm2ContractList(string(entry))
			if status == "malformed" {
				body = `{"args":[[{"name":"fixture-app"}]]}`
			}
		}
		if _, err := conn.Write(pm2ContractReply(body, id)); err != nil {
			return err
		}
	}
	return pm2ContractEnd(conn)
}
