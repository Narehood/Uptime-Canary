// Package jsvm evaluates official JSONata only inside the guarded worker.
package jsvm

import (
	"context"
	_ "embed"
	"errors"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja/parser"
)

// Canonical patched source retained for unchanged standalone JS reference tests.
//
//go:embed jsonata-source.js
var library string

// Actual worker asset: canonical source with whitespace-only esbuild output.
//
//go:embed jsonata.js
var runtimeLibrary string

// All untrusted text enters as primitive arguments, never as executable JS text.
const bridge = `(async function(expression, document, depth, timeout, range) {
    const input = JSON.parse(document);
    const query = jsonata(expression, {stack: depth, timeout: timeout});
    const value = await query.evaluate(input);
    if (value === null || typeof value === 'undefined' ||
        (typeof value !== 'string' && typeof value !== 'number' && typeof value !== 'boolean') ||
        (typeof value === 'number' && !Number.isFinite(value))) throw new Error('rejected scalar');
    return JSON.stringify(value);
})`

var errEvaluation = errors.New("JSON query evaluation failed")

func Evaluate(ctx context.Context, expression string, document []byte, depth, milliseconds, maxRange int) ([]byte, error) {
	if ctx.Err() != nil {
		return nil, errEvaluation
	}
	vm := goja.New()
	vm.SetParserOptions(parser.WithDisableSourceMaps)
	done := make(chan struct{})
	limit := time.NewTimer(time.Duration(milliseconds) * time.Millisecond)
	var interrupted atomic.Bool
	defer limit.Stop()
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			interrupted.Store(true)
			vm.Interrupt(errEvaluation)
		case <-limit.C:
			interrupted.Store(true)
			vm.Interrupt(errEvaluation)
		case <-done:
		}
	}()
	// All initialization, trusted compilation, builtin capture, parsing and
	// evaluation remain after the original timer starts, inside this worker.
	validator := privateValidator(vm, ctx, &interrupted)
	if validator == nil {
		return nil, errEvaluation
	}
	factory, err := vm.RunString("(function(canaryPrivateValidator,canaryPrivateNumericString){\n" + runtimeLibrary + "\n;return jsonata;})")
	if err != nil {
		return nil, errEvaluation
	}
	initialize, ok := goja.AssertFunction(factory)
	if !ok {
		return nil, errEvaluation
	}
	if _, err := initialize(goja.Undefined(), vm.ToValue(validator), vm.ToValue(privateNumericString(vm, ctx, &interrupted))); err != nil {
		return nil, errEvaluation
	}
	value, err := vm.RunString(bridge)
	if err != nil {
		return nil, errEvaluation
	}
	call, ok := goja.AssertFunction(value)
	if !ok {
		return nil, errEvaluation
	}
	value, err = call(goja.Undefined(), vm.ToValue(expression), vm.ToValue(string(document)), vm.ToValue(depth), vm.ToValue(milliseconds), vm.ToValue(maxRange))
	if err != nil || ctx.Err() != nil {
		return nil, errEvaluation
	}
	promise, ok := value.Export().(*goja.Promise)
	if !ok || promise.State() != goja.PromiseStateFulfilled {
		return nil, errEvaluation
	}
	output := promise.Result()
	if _, ok := output.Export().(string); !ok {
		return nil, errEvaluation
	}
	return []byte(output.String()), nil
}
