package tailscalemonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) >= 3 && os.Args[1] == "--tailscale-client-fixture" {
		switch os.Args[2] {
		case "pong":
			fmt.Print("pong from private-host (100.64.0.2) via DERP(private-region) in 2.5ms\n")
		case "timeout":
			fmt.Print("ping \"100.64.0.2\" timed out\n")
			fmt.Fprintln(os.Stderr, "no reply")
			os.Exit(1)
		case "flood":
			fmt.Print(strings.Repeat("private-flood", 4096))
		case "stall":
			if len(os.Args) == 4 {
				conn, err := net.DialTimeout("tcp", os.Args[3], time.Second)
				if err != nil {
					os.Exit(3)
				}
				defer conn.Close()
				_, _ = conn.Write([]byte{'R'})
			}
			time.Sleep(time.Minute)
		default:
			os.Exit(2)
		}
		os.Exit(0)
	}
	if fixtureWorkerDispatch() {
		panic("fixture dispatch unexpectedly returned")
	}
	os.Exit(m.Run())
}

func TestTargetAdmission(t *testing.T) {
	for _, target := range []string{"100.64.0.2", "fd7a:115c:a1e0::1", "server", "office-device", "node.example.ts.net", "node.example.ts.net.", "SERVER"} {
		if Validate(Config{Name: target, Timeout: time.Second}) != nil {
			t.Fatalf("valid peer refused: %q", target)
		}
	}
	for _, target := range []string{"", "--help", "-v", "--socket=/tmp/x", "node --help", " node", "node\n", "node\x00", "https://node", "user@node", "node:3100", "[fd7a::1]", "node/path", "a..b", ".", "a-", "-a", "fe80::1%eth0", "0.0.0.0", "::", "ff02::1", "bad_name", "a;b", "a$HOME", "é", strings.Repeat("a", 64), strings.Repeat("a.", 128)} {
		if Validate(Config{Name: target, Timeout: time.Second}) == nil {
			t.Fatalf("invalid peer admitted: %q", target)
		}
	}
}

func TestCLIOutputContract(t *testing.T) {
	for _, item := range []struct {
		name, stdout, stderr, state string
		exit                        int
		latency                     float64
	}{
		{"direct", "pong from private-host (100.64.0.2) via 192.0.2.4:41641 in 2.5ms\n", "", "up", 0, 2.5},
		{"relay", "pong from private-host (100.64.0.2) via DERP(private-region) in 3ms\n", "", "up", 0, 3},
		{"peer-relay", "pong from private-host (100.64.0.2) via peer-relay(100.64.0.3) in 4ms\n", "", "up", 0, 4},
		{"ipv6-port", "pong from private-host (fd7a::1, 5555) via [2001:db8::1]:41641 in 500µs\n", "", "up", 0, .5},
		{"rounded-zero", "pong from private-host (100.64.0.2) via DERP(x) in 0s\n", "", "up", 0, 0},
		{"timeout", "ping \"100.64.0.2\" timed out\n", "no reply\n", "down", 1, 0},
		{"timeout-crlf", "ping \"fd7a::1\" timed out\r\n", "no reply\r\n", "down", 1, 0},
		{"local", "100.64.0.1 is local Tailscale IP\n", "", "pending", 0, 0},
		{"stopped", "Tailscale is stopped.\n", "", "pending", 1, 0},
		{"manager", "", "failed to connect to local tailscaled private-path\n", "pending", 1, 0},
		{"auth", "", "access denied for private-account\n", "pending", 1, 0},
		{"no-matching-peer", "", "no matching peer\n", "pending", 1, 0},
		{"unknown-output", "future command format\n", "", "pending", 0, 0},
		{"partial-timeout", "ping \"100.64.0.2\" timed out\n", "", "pending", 1, 0},
		{"success-after-timeout", "ping \"100.64.0.2\" timed out\npong from private-host (100.64.0.2) via DERP(x) in 1ms\n", "", "pending", 0, 0},
		{"wrong-exit", "pong from private-host (100.64.0.2) via DERP(x) in 1ms\n", "", "pending", 1, 0},
		{"warning", "pong from private-host (100.64.0.2) via DERP(x) in 1ms\n", "private diagnostic", "pending", 0, 0},
		{"bad-ip", "pong from private-host (private-secret) via DERP(x) in 1ms\n", "", "pending", 0, 0},
		{"bad-latency", "pong from private-host (100.64.0.2) via DERP(x) in NaNms\n", "", "pending", 0, 0},
		{"outside-deadline", "pong from private-host (100.64.0.2) via DERP(x) in 61s\n", "", "pending", 0, 0},
		{"bad-port", "pong from private-host (100.64.0.2, 65536) via DERP(x) in 1ms\n", "", "pending", 0, 0},
		{"control", "pong from private-host (100.64.0.2) via DERP(x) in 1ms\x00", "", "pending", 0, 0},
		{"terminal-control", "pong from private\x1b-host (100.64.0.2) via DERP(x) in 1ms\n", "", "pending", 0, 0},
	} {
		t.Run(item.name, func(t *testing.T) {
			r := parseResult(commandResult{stdout: []byte(item.stdout), stderr: []byte(item.stderr), exit: item.exit}, time.Minute)
			if r.State != item.state || r.LatencyMs != item.latency {
				t.Fatalf("state/RTT mismatch: %+v", r)
			}
			want := map[string]string{"up": Replied, "down": NoReply, "pending": Unavailable}[item.state]
			if r.Message != want {
				t.Fatal("client details escaped fixed summary")
			}
		})
	}
	for _, raw := range []commandResult{{stdout: []byte(strings.Repeat("x", 4097))}, {stdout: []byte("x"), stderr: []byte(strings.Repeat("x", 4097))}, {stdout: []byte{0xff}}} {
		if r := parseResult(raw, time.Second); r.State != "pending" {
			t.Fatal("oversized or invalid client output admitted")
		}
	}
}

func TestFixedArgumentsAndCancellation(t *testing.T) {
	c := Config{Name: "private-node.example.ts.net", Timeout: 4 * time.Second}
	r := check(context.Background(), c, func(ctx context.Context, args []string) commandResult {
		if strings.Join(args, "|") != "ping|--c=1|--until-direct=false|--timeout=3s|--|private-node.example.ts.net" {
			t.Fatal("request acquired non-ping authority or changed target")
		}
		return commandResult{stdout: []byte("pong from private-host (100.64.0.2) via DERP(x) in 2ms\n")}
	})
	if r.State != "up" || r.Message != Replied || r.LatencyMs != 2 {
		t.Fatal("fixed CLI query failed", r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = check(ctx, c, func(context.Context, []string) commandResult {
		t.Fatal("canceled check ran client")
		return commandResult{}
	})
	if r.State != "pending" || r.LatencyMs != 0 {
		t.Fatal("cancellation became an outage or a latency sample")
	}
}

func TestStrictWorkerAdmission(t *testing.T) {
	valid := `{"type":"tailscale-ping","name":"private-node","timeout":1000000000}`
	if _, err := decode([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"type":"tailscale-ping","name":"private-node","timeout":1,"command":"evil"}`,
		`{"type":"tailscale-ping","name":"private-node","timeout":1,"fixture":"pong"}`,
		`{"type":"tailscale-ping","name":"private-node","name":"other","timeout":1}`,
		`{"type":"tailscale-ping","Name":"private-node","timeout":1}`,
		`{"type":"tailscale-ping","name":null,"timeout":1}`,
		`{"type":"tailscale-ping","name":"private-node","timeout":null}`,
		`{"type":"tailscale-ping","name":"private-node","timeout":0}`,
		`{"type":"tailscale-ping","name":"private-node","timeout":-1}`,
		`{"type":"pm2","name":"private-node","timeout":1}`,
		valid + `{}`,
	} {
		h := WorkerHandler()
		if h.Validate([]byte(valid)) != nil || h.Validate([]byte(raw)) == nil || h.Check(context.Background()).Message != Invalid {
			t.Fatal("ambiguous worker input admitted or validation retained old authority")
		}
	}
	b, err := json.Marshal(struct {
		Type string `json:"type"`
		Config
	}{Type, Config{Name: "private-node", Timeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decode(b); err != nil {
		t.Fatal("typed parent request refused", err)
	}
}

func TestBoundedClientProcess(t *testing.T) {
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []string{"pong", "timeout", "flood"} {
		t.Run(fixture, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			raw := runCommand(ctx, path, []string{"--tailscale-client-fixture", fixture})
			if len(raw.stdout) > 4096 || len(raw.stderr) > 4096 {
				t.Fatal("client output escaped resource bounds")
			}
			if fixture == "flood" {
				if raw.err == nil {
					t.Fatal("client flood was accepted")
				}
				return
			}
			if raw.err != nil {
				t.Fatal("private client fixture failed", raw.err)
			}
			r := parseResult(raw, time.Second)
			if fixture == "pong" && r.State != "up" || fixture == "timeout" && r.State != "down" {
				t.Fatal("actual client process result was misclassified", r)
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if raw := runCommand(ctx, path, []string{"--tailscale-client-fixture", "stall"}); raw.err == nil || time.Since(start) > time.Second {
		t.Fatal("stalled private client was not killed and reaped promptly")
	}
}
