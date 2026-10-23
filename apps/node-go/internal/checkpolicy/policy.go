// Package checkpolicy evaluates consecutive-failure retries for one monitor
// generation and one observer. It has no clocks, I/O, or runtime dependencies.
package checkpolicy

import (
	"errors"
	"unicode/utf8"
)

// Status is a raw probe observation or an effective policy result. Computed
// views such as stale are not observations accepted by Evaluate. Unknown is an
// explicit local capability/measurement failure, not an inferred healthy state.
type Status string

const (
	Up      Status = "up"
	Down    Status = "down"
	Pending Status = "pending"
	Unknown Status = "unknown"
)

const (
	MaxRetries                   = 100
	MaxConsecutiveFailures       = MaxRetries + 1
	MinIntervalMs          int64 = 100
	MaxIntervalMs          int64 = 86_400_000
	MinTimeoutMs           int64 = 25
	MaxTimeoutMs           int64 = 120_000
	MaxGenerationBytes           = 256
)

// Config has no implicit interval or timeout defaults. Generation is an opaque,
// nonempty identity for the check's semantics. The caller must change it for
// result-changing edits, including retries, upsideDown, timeout, and target
// changes; interval-only edits must retain it.
type Config struct {
	Generation      string `json:"generation"`
	Retries         int    `json:"retries"`
	IntervalMs      int64  `json:"intervalMs"`
	RetryIntervalMs int64  `json:"retryIntervalMs"`
	TimeoutMs       int64  `json:"timeoutMs"`
	UpsideDown      bool   `json:"upsideDown"`
}

// State is all policy state needed across restarts. Keep it separately for each
// monitor and observer. The zero value starts a new streak. ConsecutiveFailures
// counts post-inversion failures, saturating at MaxConsecutiveFailures.
type State struct {
	Generation          string `json:"generation"`
	ConsecutiveFailures int    `json:"consecutiveFailures"`
}

// Result preserves the original observation and reports its policy outcome.
// NextIntervalMs is the requested spacing between check starts, not a deadline
// or a delay measured from completion. Messages and incident state are caller
// owned; Pending never establishes recovery.
type Result struct {
	RawState            Status `json:"rawState"`
	EffectiveState      Status `json:"effectiveState"`
	ConsecutiveFailures int    `json:"consecutiveFailures"`
	NextIntervalMs      int64  `json:"nextIntervalMs"`
}

// Validate checks explicit limits without normalizing or mutating config.
// Timeout must fit both intervals, even when Retries is zero.
func Validate(config Config) error {
	if !validGeneration(config.Generation) {
		return errors.New("generation must be valid UTF-8 and contain 1..256 bytes")
	}
	if config.Retries < 0 || config.Retries > MaxRetries {
		return errors.New("retries must be between 0 and 100")
	}
	if config.IntervalMs < MinIntervalMs || config.IntervalMs > MaxIntervalMs {
		return errors.New("intervalMs must be between 100 and 86400000")
	}
	if config.RetryIntervalMs < MinIntervalMs || config.RetryIntervalMs > MaxIntervalMs {
		return errors.New("retryIntervalMs must be between 100 and 86400000")
	}
	if config.TimeoutMs < MinTimeoutMs || config.TimeoutMs > MaxTimeoutMs {
		return errors.New("timeoutMs must be between 25 and 120000")
	}
	if config.TimeoutMs > config.IntervalMs || config.TimeoutMs > config.RetryIntervalMs {
		return errors.New("timeoutMs must not exceed intervalMs or retryIntervalMs")
	}
	return nil
}

// Validate accepts the zero state or a bounded, initialized persisted state.
// A corrupt state is an error even when its generation has been superseded.
func (state State) Validate() error {
	if state.ConsecutiveFailures < 0 || state.ConsecutiveFailures > MaxConsecutiveFailures {
		return errors.New("consecutiveFailures must be between 0 and 101")
	}
	if state.Generation == "" && state.ConsecutiveFailures == 0 {
		return nil
	}
	if !validGeneration(state.Generation) {
		return errors.New("state generation must be valid UTF-8 and contain 1..256 bytes")
	}
	return nil
}

// Evaluate consumes exactly one completed, current raw probe observation.
// Inversion precedes retries: effective failures 1..Retries are Pending, then
// Down. Up resets the streak; a service Pending also breaks the streak without
// reporting recovery or failure. Both failures and service Pending request the
// retry interval, including confirmed Down and Retries == 0.
//
// A different generation discards the previous streak before this observation.
// On any error the returned state equals the input and the result is zero;
// callers must check the error before using the result. No inputs are mutated.
func Evaluate(config Config, state State, raw Status) (State, Result, error) {
	if err := Validate(config); err != nil {
		return state, Result{}, err
	}
	if err := state.Validate(); err != nil {
		return state, Result{}, err
	}
	if raw != Up && raw != Down && raw != Pending && raw != Unknown {
		return state, Result{}, errors.New("raw state must be up, down, pending, or unknown")
	}
	if state.Generation != config.Generation {
		state = State{Generation: config.Generation}
	}

	effective := raw
	if config.UpsideDown {
		switch raw {
		case Up:
			effective = Down
		case Down:
			effective = Up
		}
	}

	interval := config.RetryIntervalMs
	switch effective {
	case Up:
		state.ConsecutiveFailures = 0
		interval = config.IntervalMs
	case Down:
		if state.ConsecutiveFailures < MaxConsecutiveFailures {
			state.ConsecutiveFailures++
		}
		if state.ConsecutiveFailures <= config.Retries {
			effective = Pending
		}
	case Pending, Unknown:
		state.ConsecutiveFailures = 0
	}
	return state, Result{
		RawState:            raw,
		EffectiveState:      effective,
		ConsecutiveFailures: state.ConsecutiveFailures,
		NextIntervalMs:      interval,
	}, nil
}

func validGeneration(generation string) bool {
	return len(generation) > 0 && len(generation) <= MaxGenerationBytes && utf8.ValidString(generation)
}
