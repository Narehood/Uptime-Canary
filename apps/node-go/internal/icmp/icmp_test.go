package icmp

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func validConfig() Config {
	return Config{Target: "127.0.0.1", Timeout: 2500 * time.Millisecond}
}

func size(n int) *int { return &n }

func TestValidate(t *testing.T) {
	for _, target := range []string{"127.0.0.1", "::1", "2001:db8::1", "::ffff:127.0.0.1", "localhost", "EXAMPLE.test.", "a-b.test", "123", strings.Repeat("a", 63) + ".test", strings.Repeat("a.", 125) + "abc"} {
		c := validConfig()
		c.Target = target
		if err := Validate(c); err != nil {
			t.Errorf("valid target %q: %v", target, err)
		}
	}
	for _, target := range []string{"", " ", " localhost", "localhost ", "--help", "-n", "/?", "host\n-n", "host\r", "host\x00", "host\t", "host name", "https://host", "host:80", "[::1]", "fe80::1%eth0", "user@host", "host/other", "a..b", ".", "a..", "a.-b", "a.b-", "a_b", "é.test", "host;cmd", "host&cmd", "$(whoami)", "`whoami`", "host\\name", strings.Repeat("a", 64), strings.Repeat("a.", 127)} {
		c := validConfig()
		c.Target = target
		if err := Validate(c); err == nil {
			t.Errorf("accepted target %q", target)
		}
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Timeout = 0 },
		func(c *Config) { c.Timeout = -time.Second },
		func(c *Config) { c.Timeout = 120*time.Second + 1 },
		func(c *Config) { c.Count = -1 },
		func(c *Config) { c.Count = 11 },
		func(c *Config) { c.Size = size(-1) },
		func(c *Config) { c.Size = size(65501) },
		func(c *Config) { c.Family = "IPv4" },
		func(c *Config) { c.Family = "--help" },
		func(c *Config) { c.Family = "ipv6" },
		func(c *Config) { c.Target, c.Family = "::1", "ipv4" },
		func(c *Config) { c.Target, c.Family = "::ffff:127.0.0.1", "ipv6" },
	} {
		c := validConfig()
		mutate(&c)
		if err := Validate(c); err == nil {
			t.Errorf("accepted invalid config %+v", c)
		}
	}
	for _, n := range []int{0, 1, 15, 16, 32, 65500} {
		c := validConfig()
		c.Count, c.Size, c.Timeout = 10, size(n), 120*time.Second
		if err := Validate(c); err != nil {
			t.Fatalf("valid bounds: %v", err)
		}
	}
}

func TestArguments(t *testing.T) {
	tests := []struct {
		platform string
		family   string
		target   string
		count    int
		size     *int
		want     []string
	}{
		{"linux", "", "localhost", 0, nil, []string{"-n", "-c", "1", "-s", "32", "-i", "0.2", "-W", "3", "-w", "3", "--", "localhost"}},
		{"linux", "ipv6", "::1", 10, size(0), []string{"-n", "-c", "10", "-s", "0", "-i", "0.2", "-W", "3", "-w", "3", "-6", "--", "::1"}},
		{"windows", "ipv4", "127.0.0.1", 2, size(65500), []string{"-n", "2", "-l", "65500", "-w", "2500", "-4", "127.0.0.1"}},
		{"windows", "auto", "::1", 1, nil, []string{"-n", "1", "-l", "32", "-w", "2500", "::1"}},
		{"darwin", "ipv4", "localhost", 3, size(16), []string{"-n", "-c", "3", "-s", "16", "-i", "1", "-W", "2500", "-t", "3", "--", "localhost"}},
	}
	for _, tt := range tests {
		c := Config{Target: tt.target, Timeout: 2500 * time.Millisecond, Family: tt.family, Count: tt.count, Size: tt.size}
		got, err := Arguments(c, tt.platform)
		if err != nil || !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s/%s: got %v, %v; want %v", tt.platform, tt.family, got, err, tt.want)
		}
	}
	for _, platform := range []string{"linux", "windows", "darwin", "freebsd"} {
		c := validConfig()
		c.Target = "-w"
		if _, err := Arguments(c, platform); err == nil {
			t.Fatal("accepted option injection")
		}
	}
	c := validConfig()
	if _, err := Arguments(c, "freebsd"); err == nil {
		t.Fatal("accepted unsupported platform")
	}
	for _, target := range []string{"::1", "localhost"} {
		c.Target, c.Family = target, "ipv6"
		if _, err := Arguments(c, "darwin"); err == nil {
			t.Fatal("Darwin silently accepted IPv6")
		}
	}
	c = validConfig()
	c.Timeout = time.Nanosecond
	for _, platform := range []string{"linux", "windows", "darwin"} {
		args, err := Arguments(c, platform)
		if err != nil || !strings.Contains(strings.Join(args, " "), "-W 1") && !strings.Contains(strings.Join(args, " "), "-w 1") {
			t.Errorf("timeout did not round up: %v, %v", args, err)
		}
	}
}

func TestOutputFixtures(t *testing.T) {
	tests := []struct {
		name, platform         string
		replies, loss, latency bool
		avg                    float64
	}{
		{"iputils", "linux", true, false, true, 0.025},
		{"busybox6", "linux", true, false, true, 0.083},
		{"darwin", "darwin", true, false, true, 0.041},
		{"unix-partial", "linux", true, false, true, 3},
		{"unix-loss", "linux", false, true, false, 0},
		{"unix-zero-size", "linux", true, false, false, 0},
		{"windows", "windows", true, false, true, 0},
		{"windows-localized", "windows", true, false, true, 12.5},
		{"windows6", "windows", true, false, true, 2},
		{"windows-less-than", "windows", true, false, false, 0},
		{"windows-loss", "windows", false, true, false, 0},
		{"windows-unreachable", "windows", false, true, false, 0},
		{"windows-unknown-locale", "windows", false, false, false, 0},
		{"malformed", "linux", false, false, false, 0},
		{"malformed", "windows", false, true, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/"+tt.platform, func(t *testing.T) {
			p := parseOutput(fixture(t, tt.name), tt.platform)
			if p.replies != tt.replies || p.noReply != tt.loss || p.latency != tt.latency || math.Abs(p.avg-tt.avg) > 0.000001 {
				t.Fatalf("got %+v; want replies=%v loss=%v latency=%v avg=%v", p, tt.replies, tt.loss, tt.latency, tt.avg)
			}
		})
	}
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCheckClassification(t *testing.T) {
	tests := []struct {
		name     string
		e        execution
		platform string
		state    string
		latency  bool
	}{
		{"success", execution{started: true, stdout: fixture(t, "iputils")}, "linux", Up, true},
		{"partial nonzero", execution{started: true, stdout: fixture(t, "unix-partial"), err: errors.New("exit 1"), exitCode: 1}, "linux", Up, true},
		{"missing timing", execution{started: true, stdout: fixture(t, "unix-zero-size")}, "linux", Up, false},
		{"packet loss", execution{started: true, stdout: fixture(t, "unix-loss"), err: errors.New("exit 1"), exitCode: 1}, "linux", Down, false},
		{"Windows loss", execution{started: true, stdout: fixture(t, "windows-loss"), err: errors.New("exit 1"), exitCode: 1}, "windows", Down, false},
		{"Windows unreachable exit zero", execution{started: true, stdout: fixture(t, "windows-unreachable")}, "windows", Down, false},
		{"missing executable", execution{err: errors.New("secret program path")}, "linux", Unknown, false},
		{"empty successful output", execution{started: true}, "linux", Unknown, false},
		{"bare failure", execution{started: true, err: errors.New("exit 1"), exitCode: 1}, "linux", Unknown, false},
		{"exit 2 with loss", execution{started: true, stdout: fixture(t, "unix-loss"), err: errors.New("exit 2"), exitCode: 2}, "linux", Unknown, false},
		{"overflow with reply", execution{started: true, overflow: true, stdout: fixture(t, "iputils")}, "linux", Unknown, false},
		{"permission with loss", execution{started: true, stdout: fixture(t, "unix-loss"), stderr: "ping: socket: Operation not permitted", err: errors.New("exit 1"), exitCode: 1}, "linux", Unknown, false},
		{"local resource after reply", execution{started: true, stdout: fixture(t, "iputils"), stderr: "ping: sendmsg: No buffer space available"}, "linux", Unknown, false},
		{"Windows local failure", execution{started: true, stdout: "PING: transmit failed. General failure."}, "windows", Unknown, false},
		{"unsupported localization", execution{started: true, stdout: fixture(t, "windows-unknown-locale")}, "windows", Unknown, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := check(context.Background(), validConfig(), tt.platform, func(context.Context, []string) execution { return tt.e })
			if r.State != tt.state || r.LatencyAvailable != tt.latency || strings.Contains(r.Message, "secret") || strings.Contains(r.Message, "127.0.0.1") {
				t.Fatalf("unexpected result %+v", r)
			}
		})
	}
	for _, diagnostic := range []string{"ping: invalid option", "ping: bad address 'sensitive.test'", "ping: Network is unreachable", "ping: message too long", "ping: cannot allocate memory", "ping: cap_net_raw required", "ping: must be root"} {
		r := check(context.Background(), validConfig(), "linux", func(context.Context, []string) execution {
			return execution{started: true, stderr: diagnostic, stdout: fixture(t, "unix-loss"), exitCode: 1, err: errors.New("failure")}
		})
		if r.State != Unknown {
			t.Errorf("local failure became %s: %s", r.State, diagnostic)
		}
	}
}

func TestCallerDeadlineKeepsVerifiedResult(t *testing.T) {
	for _, tc := range []struct {
		name, output, state string
	}{
		{"reply", fixture(t, "unix-partial"), Up},
		{"no reply", fixture(t, "unix-loss"), Down},
		{"no evidence", "", Unknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				// The node scheduler wraps ICMP in the monitor's own timeout. Its
				// deadline can expire before the check's nested timeout does.
				// Fake time advances only after the runner waits on the deadline.
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
				defer cancel()
				r := check(ctx, validConfig(), "linux", func(budget context.Context, _ []string) execution {
					<-budget.Done()
					return execution{started: true, stdout: tc.output, err: budget.Err(), exitCode: -1}
				})
				if r.State != tc.state || r.Message == "ICMP check canceled" {
					t.Fatalf("caller deadline discarded verified evidence: %+v", r)
				}
			})
		})
	}
}

func TestWholeBudgetAndCancellation(t *testing.T) {
	c := validConfig()
	c.Count, c.Timeout = 10, 25*time.Millisecond
	for _, output := range []string{"", fixture(t, "unix-partial")} {
		start := time.Now()
		r := check(context.Background(), c, "linux", func(ctx context.Context, args []string) execution {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > c.Timeout {
				t.Fatal("missing whole-check deadline")
			}
			<-ctx.Done()
			return execution{started: true, stdout: output, err: ctx.Err(), exitCode: -1}
		})
		want := Unknown
		if output != "" {
			want = Up
		}
		if r.State != want || time.Since(start) > 500*time.Millisecond {
			t.Fatalf("deadline result %+v", r)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := check(ctx, validConfig(), "linux", func(context.Context, []string) execution {
		cancel()
		return execution{started: true, stdout: fixture(t, "iputils")}
	})
	if r.State != Unknown || r.Message != "ICMP check canceled" {
		t.Fatalf("cancellation result %+v", r)
	}
	called := false
	check(ctx, validConfig(), "linux", func(context.Context, []string) execution { called = true; return execution{} })
	if called {
		t.Fatal("started canceled check")
	}
	if Check(nil, validConfig()).State != Unknown {
		t.Fatal("accepted nil context")
	}
	c.Target = "--help"
	check(context.Background(), c, "linux", func(context.Context, []string) execution { t.Fatal("started invalid check"); return execution{} })
}

func TestResultLatencyComesFromEchoRTT(t *testing.T) {
	r := check(context.Background(), validConfig(), "linux", func(context.Context, []string) execution {
		return execution{started: true, stdout: fixture(t, "iputils")}
	})
	if r.State != Up || !r.LatencyAvailable || r.LatencyMs != 0.025 {
		t.Fatalf("result did not preserve parsed RTT: %+v", r)
	}
	r = check(context.Background(), validConfig(), "windows", func(context.Context, []string) execution {
		return execution{started: true, stdout: fixture(t, "windows")}
	})
	if r.State != Up || !r.LatencyAvailable || r.LatencyMs != 0 {
		t.Fatalf("result lost a genuine zero RTT: %+v", r)
	}
}

func TestOutputBoundAndEnvironment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &outputBudget{remaining: outputLimit, cancel: cancel}
	out, errout := &outputStream{budget: b}, &outputStream{budget: b}
	var wg sync.WaitGroup
	for _, stream := range []*outputStream{out, errout} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := stream.Write([]byte(strings.Repeat("x", outputLimit)))
			if n != outputLimit || err != nil {
				t.Error("writer did not consume input")
			}
		}()
	}
	wg.Wait()
	if len(out.data.String())+len(errout.data.String()) != outputLimit || !b.overflow || ctx.Err() == nil {
		t.Fatal("output limit failed")
	}
	parent := []string{"PATH=/usr/bin:/bin", "SystemRoot=C:\\Windows", "WINDIR=C:\\Windows", "TOKEN=secret", "HTTP_PROXY=secret", "LD_PRELOAD=secret", "LC_ALL=hostile", "LANG=hostile", "LANGUAGE=hostile", "PING_SIZE=secret"}
	env := pingEnvironment(parent)
	if strings.Contains(strings.Join(env, "\n"), "secret") || strings.Contains(strings.Join(env, "\n"), "hostile") || len(env) != 6 {
		t.Fatalf("unexpected child environment %v", env)
	}
}

func TestNativeLoopback(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("unsupported platform")
	}
	for _, tc := range []struct {
		target, family string
		count          int
		payload        *int
	}{
		{"127.0.0.1", "ipv4", 2, nil},
		{"127.0.0.1", "ipv4", 1, size(0)},
		{"127.0.0.1", "auto", 1, size(64)},
	} {
		t.Run(fmt.Sprintf("%s/%s/%d/%v", tc.target, tc.family, tc.count, tc.payload), func(t *testing.T) {
			r := Check(context.Background(), Config{Target: tc.target, Family: tc.family, Count: tc.count, Size: tc.payload, Timeout: 5 * time.Second})
			if r.State != Up {
				t.Fatalf("native loopback: %+v", r)
			}
			if tc.payload == nil && !r.LatencyAvailable {
				t.Fatalf("default loopback did not measure RTT: %+v", r)
			}
			if r.LatencyMs < 0 || math.IsNaN(r.LatencyMs) || math.IsInf(r.LatencyMs, 0) {
				t.Fatalf("bad RTT: %+v", r)
			}
		})
	}
	if runtime.GOOS != "darwin" {
		r := Check(context.Background(), Config{Target: "::1", Family: "ipv6", Timeout: 5 * time.Second})
		if r.State != Up || !r.LatencyAvailable {
			t.Fatalf("native IPv6 loopback: %+v", r)
		}
	}
	t.Run("deadline", func(t *testing.T) {
		start := time.Now()
		r := Check(context.Background(), Config{Target: "127.0.0.1", Count: 10, Timeout: 60 * time.Millisecond})
		if r.State == Down || time.Since(start) > time.Second {
			t.Fatalf("native deadline: %+v in %v", r, time.Since(start))
		}
	})
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		timer := time.AfterFunc(80*time.Millisecond, cancel)
		defer timer.Stop()
		defer cancel()
		start := time.Now()
		r := Check(ctx, Config{Target: "127.0.0.1", Count: 10, Timeout: 5 * time.Second})
		if r.State != Unknown || r.Message != "ICMP check canceled" || time.Since(start) > time.Second {
			t.Fatalf("native cancellation: %+v", r)
		}
	})
}
