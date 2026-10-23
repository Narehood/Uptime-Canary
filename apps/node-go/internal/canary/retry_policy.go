package canary

import (
	"errors"
	"fmt"
	"uptime-canary/apps/node-go/internal/checkpolicy"
)

// Only the observing node stores this state. Latest policy outcomes replicate
// as samples; another observer's streak never contributes to this counter.
type localCheckPolicy struct {
	State     checkpolicy.State `json:"state"`
	DownSince *int64            `json:"downSince"`
}

func retryInterval(m MonitorInput) int64 {
	if m.RetryIntervalMs != nil {
		return *m.RetryIntervalMs
	}
	return m.IntervalMs
}

func probeSpacing(m Monitor, s Sample) int64 {
	if s.State == "down" || s.State == "pending" || s.State == "unknown" {
		return retryInterval(m.MonitorInput)
	}
	return m.IntervalMs
}

func (n *Node) loadProbePolicies() error {
	var err error
	n.probePolicies, err = loadAll[localCheckPolicy](n.store, "probePolicies")
	if err != nil {
		return errors.New("Cannot load local check policy")
	}
	for id, p := range n.probePolicies {
		if identifier(id, "monitor ID") != nil || p.State.Validate() != nil || p.DownSince != nil && (p.State.Generation == "" || *p.DownSince < 1 || *p.DownSince > n.sequence) {
			return errors.New("Invalid persisted local check policy")
		}
	}
	return nil
}

func (n *Node) applyProbePolicy(m Monitor, r ProbeResult, seq int64) (localCheckPolicy, ProbeResult, checkpolicy.Result, error) {
	p, exists := n.probePolicies[m.ID]
	prev := n.samples[m.ID+"/"+n.ID]
	// Legacy databases have no local policy row. Preserve an already confirmed
	// outage through migration; retry-count edits create a different generation.
	if !exists && sampleGeneration(prev) == checkGeneration(m) {
		p.State = checkpolicy.State{Generation: sampleGeneration(prev), ConsecutiveFailures: prev.ConsecutiveFailures}
		p.DownSince = prev.DownSince
	}
	if p.State.Generation != checkGeneration(m) {
		p.DownSince = nil
	}
	state, result, err := checkpolicy.Evaluate(checkpolicy.Config{Generation: checkGeneration(m), Retries: m.Retries, IntervalMs: m.IntervalMs, RetryIntervalMs: retryInterval(m.MonitorInput), TimeoutMs: m.TimeoutMs, UpsideDown: m.UpsideDown}, p.State, checkpolicy.Status(r.State))
	if err != nil {
		return p, r, result, err
	}
	p.State = state
	if m.UpsideDown && (r.State == "up" || r.State == "down") {
		r.Message = "Inverted result: " + r.Message
	}
	r.State = string(result.EffectiveState)
	switch result.EffectiveState {
	case checkpolicy.Up:
		p.DownSince = nil
	case checkpolicy.Down:
		if p.DownSince == nil {
			p.DownSince = Ptr(seq)
		}
	case checkpolicy.Pending:
		if result.ConsecutiveFailures > 0 {
			r.Message = fmt.Sprintf("Failure %d/%d before alert: %s", result.ConsecutiveFailures, m.Retries, r.Message)
		}
	}
	return p, r, result, nil
}
