package process

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

var slots = make(chan struct{}, 2)

// Run includes admission, startup, validation, I/O, and the engine in the
// earlier of ctx's deadline and the JSON config's timeout. It always kills/reaps a started
// child before releasing admission. There is no in-process fallback.
func Run(ctx context.Context, config []byte) Result {
	return run(ctx, config, execution{slots: slots, executable: workerExecutable, prepare: prepareProcess})
}

// RunConfig admits before bounded JSON serialization, so rejected concurrent
// checks cannot allocate input buffers outside the two-worker capacity limit.
// Values must be ordinary JSON configuration data, not custom marshalers.
func RunConfig(ctx context.Context, timeout time.Duration, config any) Result {
	return runEncoded(ctx, timeout, func() ([]byte, error) { return EncodeConfig(config) }, execution{slots: slots, executable: workerExecutable, prepare: prepareProcess}, time.Now())
}

// Private injection points allow controlled subprocess failure tests without
// adding test switches, arbitrary programs, or environment hooks to production.
type execution struct {
	slots      chan struct{}
	executable func() (string, error)
	prepare    func(*exec.Cmd) (processGuard, error)
	wrapStdout func(io.Writer) io.Writer
	wrapStderr func(io.Writer) io.Writer
	observe    func(executionObservation)
}

type processGuard interface {
	attach(*os.Process) error
	close()
}

func pending(message string) Result {
	return Result{State: "pending", Message: message}
}

func run(ctx context.Context, config []byte, e execution) Result {
	start := time.Now()
	var c configHeader
	if len(config) > maxInputBytes-1024 || json.Unmarshal(config, &c) != nil || len(c.Type) > 64 {
		return Result{State: "down", Message: "invalid or excessive service configuration"}
	}
	return runEncoded(ctx, c.Timeout, func() ([]byte, error) { return config, nil }, e, start)
}

func runEncoded(ctx context.Context, timeout time.Duration, encode func() ([]byte, error), e execution, start time.Time) Result {
	if timeout <= 0 || timeout > 5*time.Minute {
		return Result{State: "down", Message: "invalid service configuration"}
	}
	ctx, cancel := context.WithDeadline(ctx, start.Add(timeout))
	defer cancel()
	if ctx.Err() != nil {
		return beforeCheck(ctx)
	}
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	default:
		return pending("service worker capacity unavailable")
	}
	config, err := encode()
	if err != nil {
		return Result{State: "down", Message: "invalid or excessive service configuration"}
	}
	var c configHeader
	if len(config) > maxInputBytes-1024 || json.Unmarshal(config, &c) != nil || len(c.Type) > 64 || c.Timeout != timeout {
		return Result{State: "down", Message: "invalid or excessive service configuration"}
	}
	if ctx.Err() != nil {
		return beforeCheck(ctx)
	}
	var nonceBytes [32]byte
	if _, err := rand.Read(nonceBytes[:]); err != nil {
		return pending("service worker startup unavailable")
	}
	nonce := hex.EncodeToString(nonceBytes[:])
	deadline, _ := ctx.Deadline()
	input, err := json.Marshal(request{Version: 1, Nonce: nonce, Deadline: deadline.UnixNano(), Config: config})
	if err != nil || len(input) > maxInputBytes {
		return Result{State: "down", Message: "invalid or excessive service configuration"}
	}
	if ctx.Err() != nil {
		return beforeCheck(ctx)
	}
	executable, err := e.executable()
	if err != nil || !filepath.IsAbs(executable) {
		return pending("service worker executable unavailable")
	}
	cmd := exec.CommandContext(ctx, executable, ModeArgument)
	cmd.Env = childEnvironment()
	cmd.Dir = filepath.Dir(executable)
	// Output uses owned file pipes; WaitDelay remains unchanged for exec's
	// context watcher and never decides whether buffered output is valid.
	cmd.WaitDelay = 10 * time.Millisecond
	guard, err := e.prepare(cmd)
	if err != nil {
		return pending("service worker resource control unavailable")
	}
	defer guard.close()
	reader, writer, err := os.Pipe()
	if err != nil {
		return pending("service worker input unavailable")
	}
	defer reader.Close()
	defer writer.Close()
	cmd.Stdin = reader
	out := &streamSink{prefix: frameMagic + nonce + " ", timeout: c.Timeout, serviceType: c.Type, stop: cancel}
	errout := &streamSink{stop: cancel}
	stdoutReader, stdoutWriter, err := newOutputPipe()
	if err != nil {
		return pending("service worker output unavailable")
	}
	defer stdoutReader.Close()
	defer stdoutWriter.Close()
	stderrReader, stderrWriter, err := newOutputPipe()
	if err != nil {
		return pending("service worker output unavailable")
	}
	defer stderrReader.Close()
	defer stderrWriter.Close()
	cmd.Stdout, cmd.Stderr = stdoutWriter, stderrWriter
	if err = cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return beforeCheck(ctx)
		}
		return pending("service worker startup unavailable")
	}
	_ = reader.Close()
	// Only the child's copies may keep these output pipes open.
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	var outputWriter io.Writer = out
	if e.wrapStdout != nil {
		outputWriter = e.wrapStdout(out)
	}
	output := collectOutput(stdoutReader, out, outputWriter)
	var errorWriter io.Writer = errout
	if e.wrapStderr != nil {
		errorWriter = e.wrapStderr(errout)
	}
	errorOutput := collectOutput(stderrReader, errout, errorWriter)
	if err = guard.attach(cmd.Process); err != nil {
		_ = cmd.Process.Kill()
		waitErr := cmd.Wait()
		_ = stdoutReader.Close()
		_ = stderrReader.Close()
		<-output.done
		<-errorOutput.done
		if e.observe != nil {
			e.observe(observeExecution(cmd, waitErr, err, ctx, output, errorOutput))
		}
		return pending("service worker resource control unavailable")
	}
	// No credentials are fed until the OS guard is installed. Context expiry
	// also closes owned pipes, so no collector or input writer outlives Run.
	closed := make(chan struct{})
	stopIO := context.AfterFunc(ctx, func() {
		_ = writer.Close()
		_ = stdoutReader.Close()
		_ = stderrReader.Close()
		close(closed)
	})
	defer func() {
		if !stopIO() {
			<-closed
		}
	}()
	written := make(chan error, 1)
	go func() {
		_, writeErr := io.Copy(writer, bytes.NewReader(input))
		_ = writer.Close()
		written <- writeErr
	}()
	err = cmd.Wait() // OS exit status only: exec owns no output-copy goroutines.
	_ = writer.Close()
	writeErr := <-written
	completeOutput := awaitOutput(ctx, output, errorOutput)
	if e.observe != nil {
		e.observe(observeExecution(cmd, err, writeErr, ctx, output, errorOutput))
	}
	badOut, started, final := out.snapshot()
	badErr, _, _ := errout.snapshot()
	if badOut || badErr {
		return pending("service worker output rejected")
	}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && started {
			return Result{State: "down", Message: "service check timed out", LatencyMs: float64(time.Since(start)) / float64(time.Millisecond)}
		}
		if errors.Is(ctx.Err(), context.Canceled) && started {
			return Result{State: "down", Message: "service check canceled", LatencyMs: float64(time.Since(start)) / float64(time.Millisecond)}
		}
		return beforeCheck(ctx)
	}
	if !completeOutput {
		return pending("service worker output incomplete")
	}
	if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 78 {
		return pending("service worker resource control unavailable")
	}
	if err != nil || writeErr != nil || final == nil {
		return pending("service worker stopped without a valid result")
	}
	if final.Kind == "error" {
		return Result{State: "down", Message: "invalid service configuration", LatencyMs: float64(time.Since(start)) / float64(time.Millisecond)}
	}
	r := *final.Result
	r.LatencyMs = float64(time.Since(start)) / float64(time.Millisecond)
	return r
}

func beforeCheck(ctx context.Context) Result {
	if errors.Is(ctx.Err(), context.Canceled) {
		return pending("service check canceled")
	}
	return pending("service worker deadline before check")
}
