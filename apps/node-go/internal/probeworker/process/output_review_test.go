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
	"strings"
	"sync"
	"testing"
	"time"
)

// These fixed modes exist only in the test executable. The race harness links
// that separately built CGO-disabled fixture; no child is race instrumented.
func init() {
	if len(os.Args) != 2 {
		return
	}
	switch os.Args[1] {
	case "--probeworker-output-review-completion", "--probeworker-output-review-partial-input":
		os.Exit(outputReviewChild(os.Args[1]))
	}
}

func outputReviewChild(mode string) int {
	if applyChildLimits() != nil {
		return 78
	}
	var nonce, scenario string
	if mode == "--probeworker-output-review-partial-input" {
		var header [128]byte
		if _, err := io.ReadFull(os.Stdin, header[:]); err != nil {
			return 65
		}
		_, tail, ok := strings.Cut(string(header[:]), `"nonce":"`)
		if !ok || len(tail) < 65 || tail[64] != '"' {
			return 65
		}
		nonce = tail[:64]
		_ = os.Stdin.Close() // Leave most of the bounded request undelivered.
	} else {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, maxInputBytes+1))
		var req request
		if err != nil || len(b) > maxInputBytes || decodeStrict(b, &req) != nil {
			return 65
		}
		var cfg testConfig
		if decodeStrict(req.Config, &cfg) != nil || cfg.Validate() != nil {
			return 65
		}
		nonce, scenario = req.Nonce, cfg.Redis.Password
	}
	if !validNonce(nonce) {
		return 65
	}
	frames := outputReviewFrames(nonce)
	if scenario == "missing-final-newline" {
		frames = strings.TrimSuffix(frames, "\n")
	}
	if _, err := io.WriteString(os.Stdout, frames); err != nil {
		return 74
	}
	switch scenario {
	case "exact-budgets", "stdout-last-byte", "stderr-last-byte":
		outBytes, errBytes := maxOutputBytes-len(frames), maxOutputBytes
		if scenario == "stdout-last-byte" {
			outBytes++
		}
		if scenario == "stderr-last-byte" {
			errBytes++
		}
		for _, stream := range []struct {
			file *os.File
			n    int
		}{{os.Stdout, outBytes}, {os.Stderr, errBytes}} {
			var block [1024]byte
			for stream.n > 0 {
				n := min(len(block), stream.n)
				if _, err := stream.file.Write(block[:n]); err != nil {
					return 74
				}
				stream.n -= n
			}
		}
	case "oversized-trailing-frame":
		_, _ = fmt.Fprintf(os.Stdout, "%s%s %s\n", frameMagic, nonce, strings.Repeat("x", maxFrameBytes))
	case "private-trailing-logs":
		_, _ = io.WriteString(os.Stdout, "private-review-query-and-nonce-log")
		_, _ = io.WriteString(os.Stderr, "private-review-raw-error")
	}
	return 0
}

func outputReviewFrames(nonce string) string {
	return "\n" + frameMagic + nonce + " " + `{"kind":"started"}` + "\n" +
		frameMagic + nonce + " " + `{"kind":"result","result":{"state":"up","message":"service check succeeded","latencyMs":1}}` + "\n"
}

func outputReviewExecution(t *testing.T, mode string, recorded **exec.Cmd) execution {
	t.Helper()
	e := testExecution(t, 2, nil, recorded, false)
	prepare := e.prepare
	e.prepare = func(cmd *exec.Cmd) (processGuard, error) {
		guard, err := prepare(cmd)
		cmd.Args = []string{cmd.Path, mode}
		return guard, err
	}
	return e
}

func TestOutputReviewCompletionAndExactBudgets(t *testing.T) {
	supported(t)
	for _, tc := range []struct{ scenario, state, message string }{
		{"exact-budgets", "up", "service check succeeded"},
		{"stdout-last-byte", "pending", "service worker output rejected"},
		{"stderr-last-byte", "pending", "service worker output rejected"},
		{"missing-final-newline", "pending", "service worker output rejected"},
		{"oversized-trailing-frame", "pending", "service worker output rejected"},
		{"private-trailing-logs", "up", "service check succeeded"},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			e := outputReviewExecution(t, "--probeworker-output-review-completion", nil)
			var obs executionObservation
			e.observe = func(o executionObservation) { obs = o }
			r := runConfigWithExecution(context.Background(), redisConfig("127.0.0.1:1", tc.scenario), e)
			if r.State != tc.state || r.Message != tc.message || !obs.Reaped || len(e.slots) != 0 {
				t.Fatalf("scenario=%s result=%+v observation=%+v", tc.scenario, r, obs)
			}
			if tc.state == "up" && (obs.ExitCode != 0 || obs.Wait != "success" || !obs.InputComplete || !obs.Stdout.EOF || !obs.Stderr.EOF || obs.Context != "active") {
				t.Fatalf("accepted incomplete execution: %+v", obs)
			}
			if tc.scenario == "exact-budgets" && (obs.Stdout.Bytes != maxOutputBytes || obs.Stderr.Bytes != maxOutputBytes) {
				t.Fatalf("per-stream budgets not exercised: %+v", obs)
			}
		})
	}
}

func TestOutputReviewCompleteFramesCannotOverridePartialInput(t *testing.T) {
	supported(t)
	e := outputReviewExecution(t, "--probeworker-output-review-partial-input", nil)
	var obs executionObservation
	e.observe = func(o executionObservation) { obs = o }
	// run accepts bounded raw JSON; the child deliberately does not validate or
	// consume this large configuration. It must still fail complete delivery.
	config, err := json.Marshal(struct {
		Type    string        `json:"type"`
		Timeout time.Duration `json:"timeout"`
		Padding string        `json:"padding"`
	}{"redis", 3 * time.Second, strings.Repeat("x", 512<<10)})
	if err != nil {
		t.Fatal(err)
	}
	r := run(context.Background(), config, e)
	if r.State != "pending" || r.Message != "service worker stopped without a valid result" || !obs.Reaped || obs.ExitCode != 0 || obs.Wait != "success" || obs.InputComplete || !obs.Stdout.Final || !obs.Stdout.EOF || !obs.Stderr.EOF || len(e.slots) != 0 {
		t.Fatalf("partial input accepted or control not exercised: result=%+v observation=%+v", r, obs)
	}
}

func TestOutputReviewReadinessPreservesBufferedBytesAndEOF(t *testing.T) {
	supported(t)
	r, w, err := newOutputPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	want := []byte(outputReviewFrames(strings.Repeat("a", 64)))
	if _, err := w.Write(want); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		if ready, err := pipeReadable(r); !ready || err != nil {
			t.Fatalf("probe %d: ready=%t error=%v", i, ready, err)
		}
	}
	_ = w.Close()
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("probe consumed or changed buffered data: bytes=%d error=%v", len(got), err)
	}
	if ready, err := pipeReadable(r); !ready || err != nil {
		t.Fatalf("EOF must remain readable: ready=%t error=%v", ready, err)
	}
}

func TestOutputReviewCancellationJoinsPendingReads(t *testing.T) {
	supported(t)
	for i := 0; i < 32; i++ {
		r, w, err := newOutputPipe()
		if err != nil {
			t.Fatal(err)
		}
		er, ew, err := newOutputPipe()
		if err != nil {
			_ = r.Close()
			_ = w.Close()
			t.Fatal(err)
		}
		o, e := &streamSink{}, &streamSink{}
		out, errout := collectOutput(r, o, o), collectOutput(er, e, e)
		ctx, cancel := context.WithCancel(context.Background())
		time.Sleep(time.Millisecond) // Let both reads become pending with live writers.
		cancel()
		start := time.Now()
		complete := awaitOutput(ctx, out, errout)
		_ = w.Close()
		_ = ew.Close()
		if complete || time.Since(start) > time.Second || out.observation().Read != "closed" || errout.observation().Read != "closed" {
			t.Fatalf("iteration=%d cancellation did not join both collectors: stdout=%+v stderr=%+v", i, out.observation(), errout.observation())
		}
	}
}

type outputReviewGateWriter struct {
	writer  io.Writer
	gate    <-chan struct{}
	reached chan struct{}
	once    sync.Once
}

func (w *outputReviewGateWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.once.Do(func() { w.reached <- struct{}{}; <-w.gate })
	return n, err
}

func TestOutputReviewAdmissionHeldUntilCollectorJoins(t *testing.T) {
	supported(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate, reached := make(chan struct{}), make(chan struct{}, 2)
	var release sync.Once
	defer release.Do(func() { close(gate) })
	e := outputReviewExecution(t, "--probeworker-output-review-completion", nil)
	e.wrapStdout = func(w io.Writer) io.Writer {
		return &outputReviewGateWriter{writer: w, gate: gate, reached: reached}
	}
	results := make(chan Result, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- runConfigWithExecution(ctx, redisConfig("127.0.0.1:1", "private-trailing-logs"), e) }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-reached:
		case <-time.After(3 * time.Second):
			t.Fatal("collector did not reach gate")
		}
	}
	if len(e.slots) != 2 {
		t.Fatal("two workers were not admitted")
	}
	cancel()
	time.Sleep(25 * time.Millisecond)
	if len(e.slots) != 2 {
		t.Fatal("admission released before collectors joined")
	}
	select {
	case <-results:
		t.Fatal("run returned before blocked collector joined")
	default:
	}
	busy := runConfigWithExecution(context.Background(), redisConfig("127.0.0.1:1", "private-trailing-logs"), e)
	if busy.State != "pending" || busy.Message != "service worker capacity unavailable" {
		t.Fatalf("busy admission result=%+v", busy)
	}
	release.Do(func() { close(gate) })
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if r.State != "down" && r.State != "pending" {
				t.Fatalf("cancellation accepted result: %+v", r)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("collector or child was not joined")
		}
	}
	if len(e.slots) != 0 {
		t.Fatal("admission leaked")
	}
}

func TestOutputReviewChunkBoundariesAndTruncatedSuffix(t *testing.T) {
	nonce := strings.Repeat("a", 64)
	data := []byte(outputReviewFrames(nonce))
	for split := 0; split <= len(data); split++ {
		s := &streamSink{prefix: frameMagic + nonce + " ", timeout: time.Second, serviceType: "redis"}
		for _, part := range [][]byte{data[:split], data[split:]} {
			if _, err := s.Write(part); err != nil {
				t.Fatalf("split=%d rejected complete frames", split)
			}
		}
		if err := s.finish(); err != nil {
			t.Fatalf("split=%d rejected EOF", split)
		}
		if failed, started, final := s.snapshot(); failed || !started || final == nil {
			t.Fatalf("split=%d lost complete frames", split)
		}
	}
	for _, suffix := range []string{
		frameMagic + nonce + " ",
		frameMagic + nonce + " {",
		frameMagic + nonce + ` {"kind":"started"}`,
	} {
		s := &streamSink{prefix: frameMagic + nonce + " ", timeout: time.Second, serviceType: "redis"}
		for _, b := range append(append([]byte(nil), data...), suffix...) {
			if _, err := s.Write([]byte{b}); err != nil {
				t.Fatal("truncated candidate rejected before EOF")
			}
		}
		if err := s.finish(); err == nil {
			t.Fatal("nonce-matching truncated trailing frame accepted")
		}
	}
}

type outputReviewShortWriter struct{}

func (outputReviewShortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestOutputReviewCollectorShortWriteIsIncomplete(t *testing.T) {
	supported(t)
	r, w, err := newOutputPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if _, err := io.WriteString(w, "review"); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	sink := &streamSink{}
	c := collectOutput(r, sink, outputReviewShortWriter{})
	select {
	case <-c.done:
	case <-time.After(time.Second):
		t.Fatal("collector did not terminate on short write")
	}
	if c.eof || c.read != "write-error" {
		t.Fatalf("short write treated as EOF: %+v", c.observation())
	}
	if _, err := r.Read(make([]byte, 1)); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}
