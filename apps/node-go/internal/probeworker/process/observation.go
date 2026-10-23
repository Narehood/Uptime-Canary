package process

import (
	"context"
	"errors"
	"os/exec"
)

// Private test observations contain only fixed categories and counters. Never
// retain error text, command lines, nonce, configuration, or result messages.
type executionObservation struct {
	Reaped        bool
	ExitCode      int
	ExitSuccess   bool
	Wait          string
	InputComplete bool
	Context       string
	Stdout        streamObservation
	Stderr        streamObservation
}

type streamObservation struct {
	Bytes       int
	Rejected    bool
	Started     bool
	Final       bool
	EOF         bool
	Read        string
	Probes      int
	HeldOpen    bool
	ProbeFailed bool
}

func observeExecution(cmd *exec.Cmd, waitErr, inputErr error, ctx context.Context, out, errout *outputCollector) executionObservation {
	o := executionObservation{ExitCode: -1, InputComplete: inputErr == nil, Stdout: out.observation(), Stderr: errout.observation()}
	if cmd.ProcessState != nil {
		o.Reaped, o.ExitCode, o.ExitSuccess = true, cmd.ProcessState.ExitCode(), cmd.ProcessState.Success()
	}
	switch {
	case waitErr == nil:
		o.Wait = "success"
	case errors.Is(waitErr, exec.ErrWaitDelay):
		o.Wait = "wait-delay"
	default:
		var exit *exec.ExitError
		if errors.As(waitErr, &exit) {
			o.Wait = "exit"
		} else {
			o.Wait = "other"
		}
	}
	switch ctx.Err() {
	case nil:
		o.Context = "active"
	case context.Canceled:
		o.Context = "canceled"
	case context.DeadlineExceeded:
		o.Context = "deadline"
	}
	return o
}

func (c *outputCollector) observation() streamObservation {
	<-c.done
	s := c.sink
	s.mu.Lock()
	defer s.mu.Unlock()
	return streamObservation{Bytes: s.total, Rejected: s.failed, Started: s.started, Final: s.final != nil, Read: c.read, EOF: c.eof, Probes: c.probes, HeldOpen: c.heldOpen, ProbeFailed: c.probeFailed}
}
