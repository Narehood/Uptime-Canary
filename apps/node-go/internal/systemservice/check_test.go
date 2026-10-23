package systemservice

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"uptime-canary/apps/node-go/internal/probeworker/process"
)

// Child fixtures use the real protocol/limits and production state classifier,
// with a private synthetic manager. No real OS service is queried or changed.
func TestMain(m *testing.M) {
	handler := WorkerHandler()
	validate := handler.Validate
	var configuration Config
	handler.Validate = func(raw []byte) error {
		if err := validate(raw); err != nil {
			return err
		}
		decoded, err := decodeRequest(raw)
		configuration = decoded.Config
		return err
	}
	handler.Check = func(ctx context.Context) process.Result {
		if configuration.Name == "fixture-blocked-manager" {
			select {} // Model a synchronous OS call that ignores cancellation.
		}
		return check(ctx, configuration, func(_ context.Context, name string) serviceState {
			switch name {
			case "fixture-running":
				return running
			case "fixture-stopped":
				return stopped
			case "fixture-missing":
				return missing
			default:
				return unknown
			}
		})
	}
	if handled, code := process.Dispatch(os.Args[1:], handler); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func TestSystemServiceConfigAndStrictWorkerPayload(t *testing.T) {
	for _, name := range []string{"Spooler", "sshd.service", "app@instance.service", "_fixture", "A" + strings.Repeat("x", 255)} {
		if (Config{Name: name}).Validate() != nil {
			t.Fatal("valid name refused", name)
		}
	}
	for _, name := range []string{"", "-x", ".service", "@instance", "name space", "a/b", `a\b`, "a?", "a*", "a'", "a\n", "é", strings.Repeat("x", 257)} {
		if (Config{Name: name}).Validate() == nil {
			t.Fatalf("unsafe name accepted: %q", name)
		}
	}
	for _, timeout := range []time.Duration{-1, 2*time.Minute + 1} {
		if (Config{Name: "fixture", Timeout: timeout}).Validate() == nil {
			t.Fatal("invalid timeout accepted")
		}
	}
	valid := []byte(`{"type":"system-service","name":"fixture","timeout":1000000000}`)
	if _, err := decodeRequest(valid); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		`null`, `[]`, `{}`, `{"type":"system-service","name":"fixture"}`,
		`{"type":"system-service","name":"fixture","timeout":null}`,
		`{"type":"system-service","name":null,"timeout":1000000000}`,
		`{"type":"system-service","Name":"fixture","timeout":1000000000}`,
		`{"type":"system-service","name":"fixture","name":"other","timeout":1000000000}`,
		`{"type":"system-service","name":"fixture","timeout":1000000000,"command":"unsafe"}`,
		`{"type":"other","name":"fixture","timeout":1000000000}`,
		string(valid) + `{}`, string(valid[:len(valid)-1]), strings.Repeat(" ", 2049),
	} {
		if _, err := decodeRequest([]byte(invalid)); err == nil {
			t.Fatalf("invalid private payload accepted: %s", invalid)
		}
	}
	handler := WorkerHandler()
	if handler.Validate(valid) != nil || handler.Validate([]byte(`{}`)) == nil {
		t.Fatal("handler validation state")
	}
	if result := handler.Check(context.Background()); result.State != "down" || result.Message != errConfig.Error() {
		t.Fatal("invalid payload retained prior authority", result)
	}
}

func TestSystemServiceStateAndDeadlineNeverInventRecovery(t *testing.T) {
	for _, test := range []struct {
		state    serviceState
		expected string
	}{{running, "up"}, {stopped, "down"}, {missing, "down"}, {unknown, "pending"}} {
		result := check(context.Background(), Config{Name: "private-service"}, func(ctx context.Context, name string) serviceState {
			if name != "private-service" {
				t.Fatal(name)
			}
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("manager query lacks deadline")
			}
			return test.state
		})
		if result.State != test.expected || strings.Contains(result.Message, "private-service") || result.LatencyMs != 0 {
			t.Fatal(result)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := check(ctx, Config{Name: "fixture"}, func(context.Context, string) serviceState { t.Fatal("canceled query started"); return running })
	if result.State != "pending" {
		t.Fatal(result)
	}
	result = check(context.Background(), Config{Name: "fixture", Timeout: time.Millisecond}, func(ctx context.Context, _ string) serviceState { <-ctx.Done(); return running })
	if result.State != "pending" {
		t.Fatal("expired manager result became UP", result)
	}
}

func TestSystemServiceWorkerPreservesDefinitiveAndUnavailableStates(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("worker controls unavailable on this platform")
	}
	for _, test := range []struct{ name, state, message string }{
		{"fixture-running", "up", "Service is running"}, {"fixture-stopped", "down", "Service is not running"},
		{"fixture-missing", "down", "Service is not installed"}, {"fixture-unknown", "pending", "Service status unavailable"},
	} {
		result := Check(context.Background(), Config{Name: test.name, Timeout: 5 * time.Second})
		if result.State != test.state || result.Message != test.message || strings.Contains(result.Message, test.name) {
			raw, _ := json.Marshal(result)
			t.Fatalf("%s: %s", test.name, raw)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := Check(ctx, Config{Name: "fixture-running"}); result.State != "pending" {
		t.Fatal("pre-start cancellation invented an observation", result)
	}
}

func TestSystemServiceBlockedManagerRemainsPendingAndReleasesCapacity(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("worker controls unavailable on this platform")
	}
	result := Check(context.Background(), Config{Name: "fixture-blocked-manager", Timeout: time.Second})
	if result.State != "pending" || result.Message != "Service status unavailable" {
		t.Fatal("an unreadable manager became a definitive outage", result)
	}
	if next := Check(context.Background(), Config{Name: "fixture-running", Timeout: 5 * time.Second}); next.State != "up" {
		t.Fatal("stalled manager retained worker capacity", next)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := time.AfterFunc(time.Second, cancel)
	defer timer.Stop()
	if canceled := Check(ctx, Config{Name: "fixture-blocked-manager", Timeout: 5 * time.Second}); canceled.State != "pending" {
		t.Fatal("cancellation certified a service outage", canceled)
	}
	if next := Check(context.Background(), Config{Name: "fixture-running", Timeout: 5 * time.Second}); next.State != "up" {
		t.Fatal("canceled manager retained worker capacity", next)
	}
}
