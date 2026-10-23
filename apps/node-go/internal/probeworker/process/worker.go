package process

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"time"
)

// Handler is constructed separately for each dispatch. Validate decodes and
// validates the bounded payload inside the guarded child. Check executes it
// locally without calling this process runner again.
type Handler struct {
	Validate func([]byte) error
	Check    func(context.Context) Result
}

// Dispatch claims the fixed mode before ordinary executable startup.
func Dispatch(args []string, handler Handler) (handled bool, exitCode int) {
	if len(args) == 0 || args[0] != ModeArgument {
		return false, 0
	}
	if len(args) != 1 {
		return true, 64
	}
	return true, worker(os.Stdin, os.Stdout, applyChildLimits, handler)
}

func worker(input io.Reader, output io.Writer, limits func() error, handler Handler) int {
	if limits() != nil {
		return 78
	}
	runtime.GOMAXPROCS(1)
	debug.SetMemoryLimit(softMemoryBytes)
	b, err := io.ReadAll(io.LimitReader(input, maxInputBytes+1))
	if err != nil || len(b) > maxInputBytes {
		return 65
	}
	var req request
	if decodeStrict(b, &req) != nil || req.Version != 1 || !validNonce(req.Nonce) {
		return 65
	}
	var config configHeader
	if json.Unmarshal(req.Config, &config) != nil {
		return 65
	}
	emit := func(f frame) bool {
		b, e := json.Marshal(f)
		if e != nil || len(b)+len(frameMagic)+len(req.Nonce)+3 > maxFrameBytes {
			return false
		}
		_, e = fmt.Fprintf(output, "\n%s%s %s\n", frameMagic, req.Nonce, b)
		return e == nil
	}
	deadline := time.Unix(0, req.Deadline)
	if config.Timeout <= 0 || config.Timeout > 5*time.Minute || len(config.Type) > 64 || time.Until(deadline) <= 0 || time.Until(deadline) > config.Timeout {
		return 65
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	if handler.Validate == nil || handler.Check == nil || handler.Validate(req.Config) != nil {
		if emit(frame{Kind: "error", Code: "invalid-config"}) {
			return 0
		}
		return 74
	}
	if ctx.Err() != nil {
		return 75
	}
	if !emit(frame{Kind: "started"}) {
		return 74
	}
	r := handler.Check(ctx)
	if !validTypedResult(config.Type, r, config.Timeout) {
		return 70
	}
	r.Message = safeMessage(config.Type, r)
	if !emit(frame{Kind: "result", Result: &r}) {
		return 74
	}
	return 0
}
