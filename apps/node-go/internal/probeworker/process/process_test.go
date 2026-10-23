package process

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These tests keep the generic leaf independent of all service engines.
// The explicit race harness links a separately built non-race fixture child;
// parent state and I/O remain instrumented without weakening the child cap.
// This variable exists only in test executables, never in production.
var raceFixtureExecutable string

func fixtureExecutable() (string, error) {
	if raceFixtureExecutable == "" {
		return os.Executable()
	}
	info, err := os.Lstat(raceFixtureExecutable)
	if !filepath.IsAbs(raceFixtureExecutable) || err != nil || !info.Mode().IsRegular() {
		return "", errors.New("Race fixture artifact is unavailable")
	}
	return raceFixtureExecutable, nil
}

type testConfig struct {
	Type    string        `json:"type"`
	Timeout time.Duration `json:"timeout"`
	Redis   *testRedis    `json:"redis,omitempty"`
}
type testRedis struct {
	Address  string `json:"address"`
	Password string `json:"password,omitempty"`
	TLSMode  string `json:"tlsMode,omitempty"`
}

func (c testConfig) Validate() error {
	if c.Type != "redis" || c.Redis == nil || len(c.Redis.Password) > 4096 {
		return errProtocol
	}
	if _, _, err := net.SplitHostPort(c.Redis.Address); err != nil {
		return errProtocol
	}
	return nil
}
func configJSON(c testConfig) []byte { b, _ := json.Marshal(c); return b }
func runConfig(ctx context.Context, c testConfig) Result {
	return runConfigWithExecution(ctx, c, execution{slots: slots, executable: fixtureExecutable, prepare: prepareProcess})
}
func runConfigWithExecution(ctx context.Context, c testConfig, e execution) Result {
	return runEncoded(ctx, c.Timeout, func() ([]byte, error) { return EncodeConfig(c) }, e, time.Now())
}

func workerConfig(input io.Reader, output io.Writer, limits func() error, check func(context.Context, testConfig) Result) int {
	var c testConfig
	return worker(input, output, limits, Handler{
		Validate: func(b []byte) error {
			if decodeStrict(b, &c) != nil {
				return errProtocol
			}
			return c.Validate()
		},
		Check: func(ctx context.Context) Result { return check(ctx, c) },
	})
}
func localCheck(ctx context.Context, c testConfig) Result {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", c.Redis.Address)
	if err != nil {
		return Result{State: "down", Message: "connection failed"}
	}
	defer conn.Close()
	if d, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(d)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if _, err = io.WriteString(conn, "*1\r\n$4\r\nPING\r\n"); err != nil {
		return Result{State: "down", Message: "write failed"}
	}
	var b [7]byte
	if _, err = io.ReadFull(conn, b[:]); err != nil || string(b[:]) != "+PONG\r\n" {
		return Result{State: "down", Message: "read failed"}
	}
	return Result{State: "up", Message: "local fixture succeeded"}
}

// Helpers exist only in the test executable. Production has no fault, program,
// environment, or arbitrary-argument injection switches.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--probeworker-test-result-validation" {
		os.Exit(resultValidationWorker())
	}
	if len(os.Args) == 2 && os.Args[1] == "--probeworker-test-before-input" {
		if applyChildLimits() != nil {
			os.Exit(78)
		}
		time.Sleep(10 * time.Second) // parent deadline must interrupt blocked stdin
		os.Exit(75)
	}
	if len(os.Args) == 2 && os.Args[1] == "--probeworker-test-resource-failure" {
		os.Exit(workerConfig(forbiddenReader{}, os.Stdout, func() error { return errProtocol }, localCheck))
	}
	if len(os.Args) == 2 && os.Args[1] == "--probeworker-test-controller" {
		var c testConfig
		if json.NewDecoder(io.LimitReader(os.Stdin, maxInputBytes)).Decode(&c) != nil {
			os.Exit(65)
		}
		r := runConfig(context.Background(), c)
		if r.State != "up" {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && os.Args[1] == ModeArgument {
		if len(os.Args) != 2 {
			os.Exit(64)
		}
		os.Exit(workerConfig(os.Stdin, os.Stdout, applyChildLimits, testCheck))
	}
	os.Exit(m.Run())
}

func testCheck(ctx context.Context, c testConfig) Result {
	if c.Redis != nil {
		switch c.Redis.Password {
		case "helper-crash":
			os.Exit(23)
		case "helper-panic":
			go func() { panic("private-driver-text") }()
			select {}
		case "helper-cpu":
			// Signal entry through the loopback fixture, then run synchronous work
			// ignoring ctx. Never allocate a large buffer or stress shared memory.
			conn, err := net.DialTimeout("tcp", c.Redis.Address, time.Second)
			if err == nil {
				_ = conn.Close()
			}
			for {
				runtime.KeepAlive(c.Type)
			}
		case "helper-output":
			for i := 0; i < 80; i++ {
				_, _ = io.WriteString(os.Stdout, strings.Repeat("x", 1024))
			}
		case "helper-stderr":
			for i := 0; i < 80; i++ {
				_, _ = io.WriteString(os.Stderr, strings.Repeat("x", 1024))
			}
		case "helper-logs":
			fmt.Fprint(os.Stdout, "private-stdout-driver-log")
			fmt.Fprint(os.Stderr, "private-stderr-driver-log")
			return Result{State: "up", Message: "private-driver-result"}
		case "helper-environment":
			if runtime.GOMAXPROCS(0) != 1 {
				os.Exit(24)
			}
			for _, env := range os.Environ() {
				key, _, _ := strings.Cut(env, "=")
				switch strings.ToUpper(key) {
				case "GOMAXPROCS", "GOMEMLIMIT", "GOTRACEBACK", "SYSTEMROOT", "WINDIR", "SSL_CERT_FILE", "SSL_CERT_DIR":
				default:
					os.Exit(25)
				}
			}
			return Result{State: "up", Message: "environment accepted"}
		}
	}
	return localCheck(ctx, c)
}

func supported(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("kernel resource control unsupported on this OS")
	}
}

func redisConfig(address, password string) testConfig {
	return testConfig{Type: "redis", Timeout: 3 * time.Second, Redis: &testRedis{Address: address, Password: password, TLSMode: "plain"}}
}

func fixture(t *testing.T, serve func(net.Conn) error) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		defer close(done)
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(6 * time.Second))
				if err := serve(c); err != nil {
					t.Errorf("loopback fixture: %v", err)
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = l.Close()
		<-done
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return l.Addr().String()
}

func TestRunLoopbackWorker(t *testing.T) {
	supported(t)
	address := fixture(t, func(c net.Conn) error {
		want := "*1\r\n$4\r\nPING\r\n"
		b := make([]byte, len(want))
		if _, err := io.ReadFull(c, b); err != nil {
			return err
		}
		if string(b) != want {
			return errors.New("fixture PING missing")
		}
		_, err := io.WriteString(c, "+PONG\r\n")
		return err
	})
	var cmd *exec.Cmd
	r := runConfigWithExecution(context.Background(), redisConfig(address, ""), testExecution(t, 2, nil, &cmd, false))
	if r.State != "up" || r.Message != "service check succeeded" || r.LatencyMs <= 0 {
		t.Fatalf("loopback worker result: %+v; worker exit: %v", r, cmd.ProcessState)
	}
}

func TestRunChildLogsEnvironmentAndFailures(t *testing.T) {
	supported(t)
	for _, tt := range []struct{ helper, state, message string }{
		{"helper-logs", "up", "service check succeeded"},
		{"helper-environment", "up", "service check succeeded"},
		{"helper-crash", "pending", "service worker stopped without a valid result"},
		{"helper-panic", "pending", "service worker stopped without a valid result"},
		{"helper-output", "pending", "service worker output rejected"},
		{"helper-stderr", "pending", "service worker output rejected"},
	} {
		t.Run(tt.helper, func(t *testing.T) {
			t.Setenv("CANARY_ADMIN_TOKEN", "private-admin-token")
			t.Setenv("CANARY_CLUSTER_SECRET", "private-cluster-secret")
			t.Setenv("ORACLE_HOME", "private-oracle-home")
			t.Setenv("PGPASSWORD", "private-password")
			r := runConfig(context.Background(), redisConfig("127.0.0.1:1", tt.helper))
			if r.State != tt.state || r.Message != tt.message {
				t.Fatalf("result: %+v", r)
			}
			if strings.Contains(r.Message, "private") {
				t.Fatal("child diagnostic escaped")
			}
		})
	}
}

type recordingGuard struct {
	processGuard
	cmd      *exec.Cmd
	attached *atomic.Int32
	fail     bool
}

func (g recordingGuard) attach(p *os.Process) error {
	if g.fail {
		return errors.New("private assignment failure")
	}
	err := g.processGuard.attach(p)
	if err == nil && g.attached != nil {
		g.attached.Add(1)
	}
	return err
}

func testExecution(t *testing.T, limit int, attached *atomic.Int32, recorded **exec.Cmd, fail bool) execution {
	t.Helper()
	return execution{slots: make(chan struct{}, limit), executable: fixtureExecutable, prepare: func(cmd *exec.Cmd) (processGuard, error) {
		g, err := prepareProcess(cmd)
		if err != nil {
			return nil, err
		}
		if recorded != nil {
			*recorded = cmd
		}
		return recordingGuard{processGuard: g, cmd: cmd, attached: attached, fail: fail}, nil
	}}
}

func TestRunSynchronousDeadlineCancellationAndReaping(t *testing.T) {
	supported(t)
	for _, cancelAfterEntry := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelAfterEntry), func(t *testing.T) {
			entered := make(chan struct{}, 1)
			address := fixture(t, func(c net.Conn) error { entered <- struct{}{}; return nil })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := redisConfig(address, "helper-cpu")
			c.Timeout = 800 * time.Millisecond
			var cmd *exec.Cmd
			e := testExecution(t, 2, nil, &cmd, false)
			result := make(chan Result, 1)
			start := time.Now()
			go func() { result <- runConfigWithExecution(ctx, c, e) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("child did not reach synchronous helper")
			}
			if cancelAfterEntry {
				cancel()
			}
			select {
			case r := <-result:
				want := "down"
				if r.State != want || cmd.ProcessState == nil || len(e.slots) != 0 {
					t.Fatalf("not reaped/admission leaked: %+v", r)
				}
				if time.Since(start) > 2*time.Second {
					t.Fatal("deadline not bounded")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("synchronous child escaped deadline")
			}
		})
	}
}

func TestConcurrentAdmissionReturnsPending(t *testing.T) {
	supported(t)
	entered := make(chan struct{}, 2)
	address := fixture(t, func(c net.Conn) error { entered <- struct{}{}; _, _ = io.Copy(io.Discard, c); return nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var count atomic.Int32
	e := testExecution(t, 2, &count, nil, false)
	results := make(chan Result, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- runConfigWithExecution(ctx, redisConfig(address, ""), e) }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(4 * time.Second):
			t.Fatal("workers not admitted")
		}
	}
	start := time.Now()
	r := runConfigWithExecution(ctx, redisConfig(address, ""), e)
	if r.State != "pending" || r.LatencyMs != 0 || count.Load() != 2 || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("capacity result: %+v; processes=%d", r, count.Load())
	}
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case <-results:
		case <-time.After(3 * time.Second):
			t.Fatal("worker not reaped")
		}
	}
	if len(e.slots) != 0 {
		t.Fatal("admission leaked")
	}
}

func TestAdmissionPrecedesSerialization(t *testing.T) {
	e := execution{slots: make(chan struct{}, 1)}
	e.slots <- struct{}{}
	encode := func() ([]byte, error) { t.Fatal("capacity rejection serialized configuration"); return nil, nil }
	r := runEncoded(context.Background(), time.Second, encode, e, time.Now())
	if r.State != "pending" || r.LatencyMs != 0 {
		t.Fatalf("capacity result: %+v", r)
	}
}

func TestConfigEncodingBoundsCyclesAndRawInput(t *testing.T) {
	type recursive struct{ Next *recursive }
	c := &recursive{}
	c.Next = c
	if _, err := EncodeConfig(c); err == nil {
		t.Fatal("recursive configuration accepted")
	}
	for _, b := range [][]byte{nil, []byte("{}"), []byte(`{"type":"redis","timeout":1} {}`), make([]byte, maxInputBytes)} {
		if r := Run(context.Background(), b); r.State != "down" {
			t.Fatalf("invalid JSON config accepted: %+v", r)
		}
	}
}

func TestParentCrashClosesWorkerConnection(t *testing.T) {
	supported(t)
	entered := make(chan struct{}, 1)
	closed := make(chan struct{}, 1)
	address := fixture(t, func(c net.Conn) error {
		b := make([]byte, len("*1\r\n$4\r\nPING\r\n"))
		if _, err := io.ReadFull(c, b); err != nil {
			return err
		}
		entered <- struct{}{}
		_, _ = io.Copy(io.Discard, c)
		closed <- struct{}{}
		return nil
	})
	c := redisConfig(address, "")
	c.Timeout = 10 * time.Second
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	controller := exec.Command(path, "--probeworker-test-controller")
	controller.Env = childEnvironment()
	controller.Stdin = bytes.NewReader(b)
	controller.Stdout, controller.Stderr = io.Discard, io.Discard
	if err = controller.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = controller.Process.Kill(); _ = controller.Wait() }()
	select {
	case <-entered:
	case <-time.After(4 * time.Second):
		t.Fatal("worker never connected")
	}
	if err = controller.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = controller.Wait(); err == nil {
		t.Fatal("controller unexpectedly exited cleanly")
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("worker survived parent crash")
	}
}

func TestRunRefusesInvalidInputAndResourceFailures(t *testing.T) {
	supported(t)
	c := redisConfig("127.0.0.1:1", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := runConfig(ctx, c); r.State != "pending" {
		t.Fatalf("canceled: %+v", r)
	}
	bad := c
	bad.Timeout = 0
	if r := runConfig(context.Background(), bad); r.State != "down" {
		t.Fatalf("bad timeout: %+v", r)
	}
	bad = redisConfig("invalid", "")
	if r := runConfig(context.Background(), bad); r.State != "down" || r.Message != "invalid service configuration" {
		t.Fatalf("bad config: %+v", r)
	}
	bad = redisConfig("127.0.0.1:1", strings.Repeat("x", maxInputBytes/6))
	if r := runConfig(context.Background(), bad); r.State != "down" || !strings.Contains(r.Message, "excessive") {
		t.Fatalf("large input: %+v", r)
	}
	e := execution{slots: make(chan struct{}, 2), executable: fixtureExecutable, prepare: func(*exec.Cmd) (processGuard, error) { return nil, errors.New("private kernel failure") }}
	if r := runConfigWithExecution(context.Background(), c, e); r.State != "pending" || r.Message != "service worker resource control unavailable" {
		t.Fatalf("resource failure: %+v", r)
	}
	var cmd *exec.Cmd
	e = testExecution(t, 2, nil, &cmd, true)
	if r := runConfigWithExecution(context.Background(), c, e); r.State != "pending" || cmd.ProcessState == nil || len(e.slots) != 0 {
		t.Fatalf("assignment failure: %+v", r)
	}
	e.executable = func() (string, error) { return "relative-program", nil }
	if r := runConfigWithExecution(context.Background(), c, e); r.State != "pending" {
		t.Fatalf("relative executable accepted: %+v", r)
	}
}

func TestStartupAndStdinAreInsideTotalDeadline(t *testing.T) {
	supported(t)
	for _, mode := range []string{"--probeworker-test-before-input", "--probeworker-test-resource-failure"} {
		t.Run(mode, func(t *testing.T) {
			var cmd *exec.Cmd
			e := testExecution(t, 2, nil, &cmd, false)
			prepare := e.prepare
			e.prepare = func(c *exec.Cmd) (processGuard, error) {
				g, err := prepare(c)
				c.Args = []string{c.Path, mode} // private test-only fixed mode
				return g, err
			}
			c := redisConfig("127.0.0.1:1", strings.Repeat("x", 256<<10))
			c.Timeout = 300 * time.Millisecond
			start := time.Now()
			r := runConfigWithExecution(context.Background(), c, e)
			if r.State != "pending" || r.LatencyMs != 0 || time.Since(start) > 2*time.Second || cmd.ProcessState == nil || len(e.slots) != 0 {
				t.Fatalf("startup/stdin not bounded: %+v", r)
			}
			if mode == "--probeworker-test-resource-failure" && r.Message != "service worker resource control unavailable" {
				t.Fatalf("child resource failure not explicit: %+v", r)
			}
		})
	}
}

type forbiddenReader struct{}

func (forbiddenReader) Read([]byte) (int, error) { panic("configuration read before limits") }

type repeatedReader struct{ remaining int }

func (r *repeatedReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(r.remaining, len(p))
	clear(p[:n])
	r.remaining -= n
	return n, nil
}

func requestBytes(c testConfig) []byte {
	b, _ := json.Marshal(request{Version: 1, Nonce: strings.Repeat("a", 64), Deadline: time.Now().Add(c.Timeout).UnixNano(), Config: configJSON(c)})
	return b
}

func TestWorkerLimitsPrecedeReadsAndStrictInput(t *testing.T) {
	if code := workerConfig(forbiddenReader{}, io.Discard, func() error { return errProtocol }, localCheck); code != 78 {
		t.Fatalf("limits exit=%d", code)
	}
	called := false
	check := func(context.Context, testConfig) Result {
		called = true
		return Result{State: "up", Message: "test"}
	}
	for _, b := range [][]byte{nil, []byte("{}"), []byte(`{"version":1,"unknown":true}`), append(requestBytes(redisConfig("127.0.0.1:1", "")), []byte("{}")...)} {
		if code := workerConfig(bytes.NewReader(b), io.Discard, func() error { return nil }, check); code != 65 {
			t.Fatalf("invalid input exit=%d", code)
		}
	}
	r := &repeatedReader{remaining: maxInputBytes + 100}
	if code := workerConfig(r, io.Discard, func() error { return nil }, check); code != 65 || r.remaining != 99 {
		t.Fatalf("input not bounded: %d/%d", code, r.remaining)
	}
	if called {
		t.Fatal("invalid input reached engine")
	}
	if handled, code := Dispatch([]string{ModeArgument, "extra"}, Handler{}); !handled || code != 64 {
		t.Fatal("invalid worker args not claimed")
	}
	if handled, _ := Dispatch([]string{"serve"}, Handler{}); handled {
		t.Fatal("normal startup claimed")
	}
}

func TestFrameNonceLimitsAndResultValidation(t *testing.T) {
	nonce := strings.Repeat("a", 64)
	newSink := func() *streamSink {
		return &streamSink{prefix: frameMagic + nonce + " ", timeout: time.Second, serviceType: "oracledb"}
	}
	emit := func(n string, f frame) []byte {
		b, _ := json.Marshal(f)
		return []byte("\n" + frameMagic + n + " " + string(b) + "\n")
	}
	s := newSink()
	_, _ = s.Write(emit(strings.Repeat("b", 64), frame{Kind: "started"}))
	if _, started, _ := s.snapshot(); started {
		t.Fatal("wrong nonce accepted")
	}
	_, _ = s.Write([]byte(strings.Repeat("x", maxFrameBytes+200) + "\n"))
	_, _ = s.Write(emit(nonce, frame{Kind: "started"}))
	_, _ = s.Write(emit(nonce, frame{Kind: "result", Result: &Result{State: "up", Message: "Oracle monitoring query succeeded", LatencyMs: 1}}))
	if bad, started, final := s.snapshot(); bad || !started || final == nil {
		t.Fatal("valid frame not accepted")
	}
	_, _ = s.Write(emit(nonce, frame{Kind: "started"}))
	if bad, _, _ := s.snapshot(); !bad {
		t.Fatal("duplicate frame accepted")
	}
	for _, r := range []Result{
		{State: "pending", Message: "test"}, {State: "up", Message: ""}, {State: "up", Message: strings.Repeat("x", 257)},
		{State: "up", Message: "test\n"}, {State: "up", Message: "test", LatencyMs: -1}, {State: "up", Message: "test", LatencyMs: math.NaN()},
		{State: "up", Message: "test", LatencyMs: math.Inf(1)}, {State: "up", Message: "test", LatencyMs: 1001},
	} {
		if validResult(r, time.Second) {
			t.Fatalf("invalid result accepted: %+v", r)
		}
	}
	s = newSink()
	_, _ = s.Write(emit(nonce, frame{Kind: "started"}))
	if _, err := s.Write(emit(nonce, frame{Kind: "result", Result: &Result{State: "up", Message: "private-password"}})); err == nil {
		t.Fatal("arbitrary diagnostic accepted")
	}
	for _, prefix := range []string{"", frameMagic + nonce + " "} {
		s = newSink()
		s.prefix = prefix
		if _, err := s.Write(make([]byte, maxOutputBytes+1)); err == nil || len(s.line) > maxFrameBytes {
			t.Fatal("stream budget not enforced")
		}
	}
}
