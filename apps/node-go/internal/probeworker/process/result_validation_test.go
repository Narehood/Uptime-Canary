package process

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type delayedFinalWriter struct {
	sink    *streamSink
	gate    <-chan struct{}
	once    sync.Once
	reached chan struct{}
	before  bool
}

// Fixed private test mode only. It executes no query, opens no target, and
// applies the ordinary child controls before reading the bounded request.
func resultValidationWorker() int {
	if applyChildLimits() != nil {
		return 78
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, maxInputBytes+1))
	if err != nil || len(b) > maxInputBytes {
		return 65
	}
	var req request
	if decodeStrict(b, &req) != nil || !validNonce(req.Nonce) {
		return 65
	}
	var cfg testConfig
	if decodeStrict(req.Config, &cfg) != nil || cfg.Validate() != nil {
		return 65
	}
	emit := func(f frame) {
		b, _ := json.Marshal(f)
		fmt.Fprintf(os.Stdout, "\n%s%s %s\n", frameMagic, req.Nonce, b)
	}
	emit(frame{Kind: "started"})
	if cfg.Redis.Password == "missing-final" {
		return 0
	}
	r := Result{State: "up", Message: "service check succeeded", LatencyMs: 1}
	if cfg.Redis.Password == "invalid-state" {
		r.State = "unknown"
	}
	if cfg.Redis.Password == "private-message" {
		r.Message = "private-result-text"
	}
	emit(frame{Kind: "result", Result: &r})
	switch cfg.Redis.Password {
	case "nonzero":
		return 23
	case "duplicate":
		emit(frame{Kind: "result", Result: &r})
	case "truncated":
		fmt.Fprintf(os.Stdout, "\n%s%s {", frameMagic, req.Nonce)
	case "stdout-overflow":
		for i := 0; i < 80; i++ {
			_, _ = os.Stdout.Write(make([]byte, 1024))
		}
	case "stderr-overflow":
		for i := 0; i < 80; i++ {
			_, _ = os.Stderr.Write(make([]byte, 1024))
		}
	}
	return 0
}

func TestExecCopyTimerExpiresAfterSuccessfulExit(t *testing.T) {
	supported(t)
	path, err := fixtureExecutable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--probeworker-test-result-validation")
	cmd.Env = childEnvironment()
	cmd.WaitDelay = outputPipeGrace
	guard, err := prepareProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.close()
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer writer.Close()
	cmd.Stdin = input
	nonce := strings.Repeat("a", 64)
	sink := &streamSink{prefix: frameMagic + nonce + " ", timeout: 5 * time.Second, serviceType: "redis", stop: cancel}
	gate, reached := make(chan struct{}), make(chan struct{})
	cmd.Stdout = &delayedFinalWriter{sink: sink, gate: gate, reached: reached}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	if err = guard.attach(cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	go func() {
		select {
		case <-reached:
			time.Sleep(150 * time.Millisecond)
		case <-ctx.Done():
		}
		close(gate)
	}()
	c := redisConfig("127.0.0.1:1", "valid")
	c.Timeout = 5 * time.Second
	b, _ := json.Marshal(request{Version: 1, Nonce: nonce, Deadline: time.Now().Add(c.Timeout).UnixNano(), Config: configJSON(c)})
	if _, err = io.Copy(writer, bytes.NewReader(b)); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	err = cmd.Wait()
	bad, started, final := sink.snapshot()
	t.Logf("reaped=%t exit=%d success=%t wait-delay=%t rejected=%t started=%t final=%t", cmd.ProcessState != nil, cmd.ProcessState.ExitCode(), cmd.ProcessState.Success(), errors.Is(err, exec.ErrWaitDelay), bad, started, final != nil)
	if !errors.Is(err, exec.ErrWaitDelay) || !cmd.ProcessState.Success() || bad || !started || final == nil {
		t.Fatal("stdlib copy-timer control did not reproduce")
	}
}

func TestKernelReadinessAndHeldOutput(t *testing.T) {
	supported(t)
	for _, heldStream := range []string{"stdout", "stderr"} {
		t.Run(heldStream, func(t *testing.T) {
			r, w, err := newOutputPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			defer w.Close()
			er, ew, err := newOutputPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer er.Close()
			defer ew.Close()
			if ready, err := pipeReadable(r); ready || err != nil {
				t.Fatal("empty open pipe was readable")
			}
			if _, err = w.Write([]byte("incidental\n")); err != nil {
				t.Fatal(err)
			}
			if ready, err := pipeReadable(r); !ready || err != nil {
				t.Fatal("buffered pipe was not readable")
			}
			if heldStream == "stdout" {
				_ = ew.Close()
			} else {
				_ = w.Close()
			}
			out, errout := &streamSink{}, &streamSink{}
			o, e := collectOutput(r, out, out), collectOutput(er, errout, errout)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			start := time.Now()
			if awaitOutput(ctx, o, e) || time.Since(start) > 250*time.Millisecond || !(o.heldOpen || e.heldOpen) {
				t.Fatal("held output was not rejected within the existing grace")
			}
			t.Logf("held=%s stdout=%+v stderr=%+v", heldStream, o.observation(), e.observation())
		})
	}
}

func TestCompletedFrameNeverOverridesInvalidCompletion(t *testing.T) {
	supported(t)
	for _, mode := range []string{"valid", "nonzero", "missing-final", "duplicate", "truncated", "invalid-state", "private-message", "stdout-overflow", "stderr-overflow"} {
		t.Run(mode, func(t *testing.T) {
			e := testExecution(t, 2, nil, nil, false)
			prepare := e.prepare
			e.prepare = func(cmd *exec.Cmd) (processGuard, error) {
				g, err := prepare(cmd)
				cmd.Args = []string{cmd.Path, "--probeworker-test-result-validation"}
				return g, err
			}
			var o executionObservation
			e.observe = func(obs executionObservation) { o = obs }
			c := redisConfig("127.0.0.1:1", mode)
			c.Timeout = 3 * time.Second
			r := runConfigWithExecution(context.Background(), c, e)
			want := "pending"
			if mode == "valid" {
				want = "up"
			}
			if r.State != want || strings.Contains(r.Message, "private") || !o.Reaped || len(e.slots) != 0 {
				t.Fatalf("mode=%s state=%s observation=%+v", mode, r.State, o)
			}
			if mode == "nonzero" && (o.Wait != "exit" || o.ExitCode != 23 || !o.Stdout.Final || !o.Stdout.EOF || !o.Stderr.EOF) {
				t.Fatalf("nonzero exit not independently checked: %+v", o)
			}
		})
	}
}

func TestCompletedChildrenUnderLoad(t *testing.T) {
	supported(t)
	old := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(old)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var load sync.WaitGroup
	for i := 0; i < 2; i++ {
		load.Add(1)
		go func() {
			defer load.Done()
			for ctx.Err() == nil {
				runtime.Gosched()
			}
		}()
	}
	defer func() { cancel(); load.Wait() }()
	e := testExecution(t, 2, nil, nil, false)
	var complete, failures atomic.Int32
	e.observe = func(o executionObservation) {
		if o.Reaped && o.ExitCode == 0 && o.ExitSuccess && o.Wait == "success" && o.InputComplete && o.Stdout.Final && o.Stdout.EOF && o.Stderr.EOF && !o.Stdout.Rejected && !o.Stderr.Rejected && o.Context == "active" {
			complete.Add(1)
		} else {
			failures.Add(1)
		}
	}
	var runs sync.WaitGroup
	for i := 0; i < 2; i++ {
		runs.Add(1)
		go func() {
			defer runs.Done()
			for j := 0; j < 128; j++ {
				if r := runConfigWithExecution(ctx, redisConfig("127.0.0.1:1", "helper-logs"), e); r.State != "up" {
					failures.Add(1)
				}
			}
		}()
	}
	runs.Wait()
	t.Logf("children=%d failures=%d", complete.Load(), failures.Load())
	if complete.Load() != 256 || failures.Load() != 0 || len(e.slots) != 0 {
		t.Fatal("completed children lost under scheduler load")
	}
}

func (w *delayedFinalWriter) Write(b []byte) (int, error) {
	if w.before {
		w.once.Do(func() { close(w.reached); <-w.gate })
	}
	n, err := w.sink.Write(b)
	_, _, final := w.sink.snapshot()
	if !w.before && final != nil {
		w.once.Do(func() { close(w.reached); <-w.gate })
	}
	return n, err
}

func TestCompletedChildSurvivesDelayedOutput(t *testing.T) {
	supported(t)
	for _, phase := range []string{"stdout-before-parse", "stdout-after-final", "stderr-before-parse"} {
		t.Run(phase, func(t *testing.T) {
			gate, reached := make(chan struct{}), make(chan struct{})
			e := testExecution(t, 2, nil, nil, false)
			wrap := func(w io.Writer) io.Writer {
				return &delayedFinalWriter{sink: w.(*streamSink), gate: gate, reached: reached, before: phase != "stdout-after-final"}
			}
			if phase == "stderr-before-parse" {
				e.wrapStderr = wrap
			} else {
				e.wrapStdout = wrap
			}
			var observation executionObservation
			e.observe = func(o executionObservation) { observation = o }
			go func() {
				select {
				case <-reached:
					time.Sleep(150 * time.Millisecond)
				case <-time.After(4 * time.Second):
				}
				close(gate)
			}()
			c := redisConfig("127.0.0.1:1", "helper-logs")
			c.Timeout = 5 * time.Second
			r := runConfigWithExecution(context.Background(), c, e)
			t.Logf("state=%s observation=%+v", r.State, observation)
			if r.State != "up" || observation.Wait != "success" || !observation.Reaped || observation.ExitCode != 0 || !observation.ExitSuccess || !observation.InputComplete || !observation.Stdout.Final || observation.Stdout.Rejected || observation.Stderr.Rejected || observation.Context != "active" || !observation.Stdout.EOF || !observation.Stderr.EOF || observation.Stdout.Probes+observation.Stderr.Probes == 0 {
				t.Fatalf("completed child lost after delayed output: state=%s observation=%+v", r.State, observation)
			}
		})
	}
}

func TestCancellationWhileCompletedOutputDrains(t *testing.T) {
	supported(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate, reached := make(chan struct{}), make(chan struct{})
	e := testExecution(t, 2, nil, nil, false)
	e.wrapStdout = func(w io.Writer) io.Writer {
		return &delayedFinalWriter{sink: w.(*streamSink), gate: gate, reached: reached}
	}
	var observation executionObservation
	e.observe = func(o executionObservation) { observation = o }
	go func() {
		select {
		case <-reached:
			time.Sleep(30 * time.Millisecond)
		case <-time.After(4 * time.Second):
		}
		cancel()
		close(gate)
	}()
	r := runConfigWithExecution(ctx, redisConfig("127.0.0.1:1", "helper-logs"), e)
	if r.State != "down" || r.Message != "service check canceled" || !observation.Reaped || observation.Context != "canceled" || len(e.slots) != 0 {
		t.Fatalf("cancellation lost during drain: state=%s observation=%+v", r.State, observation)
	}
}
