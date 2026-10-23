package canary

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestIncompletePeerHistoryCannotStopLocalMonitoring(t *testing.T) {
	var now atomic.Int64
	now.Store(maintenanceClock(12, 0))
	var probes atomic.Int64
	var failing atomic.Bool
	r := webhook(t)
	o := cr2Options(t, false)
	o.Now = now.Load
	o.Probe = func(context.Context, Monitor) ProbeResult {
		probes.Add(1)
		state := "up"
		if failing.Load() {
			state = "down"
		}
		return ProbeResult{State: state, Message: "local probe"}
	}
	n := cr2New(t, o)
	m, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL), nil, "live-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	n.nextSync = maxSafe
	n.samples[m.ID+"/b"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "b", State: "up", CheckedAt: now.Load(), Message: "peer measurement"}, Sequence: 2, Revision: m.Revision}
	n.tick()
	n.wg.Wait()
	if probes.Load() != 1 {
		t.Fatal("fixture did not start monitoring")
	}
	// Receive a valid header before its separately paged facts, then lose the
	// peer. Unavailable older history must not stop an independent node's work.
	pending := cr2Incident(n, "interrupted-peer-outage", checkGeneration(m), 1)
	pending.MonitorID = m.ID
	pending.OpenedAt = now.Load() - 10000
	pending.Runs = map[string][]int64{"b": {1}}
	pending.EvidenceRoot = digest("unreceived immutable history")
	cr2Apply(t, n, Snapshot{Incidents: []Incident{pending}})
	if len(n.pendingIncidents) != 1 {
		t.Fatal("fixture did not retain incomplete evidence")
	}
	failing.Store(true)
	now.Add(1000)
	n.tick()
	n.wg.Wait()
	if probes.Load() != 2 {
		t.Error("an interrupted peer page stopped local checks")
	}
	// No more peer pages arrive. Even a very long partition must not turn
	// delivery-first monitoring into an indefinite coordination wait.
	now.Add(60 * 60 * 1000)
	n.nextSync = maxSafe
	n.tick()
	n.wg.Wait()
	if probes.Load() != 3 {
		t.Error("local monitoring never resumed during the partition")
	}
	history, err := n.store.history(m.ID, 100)
	if err != nil || len(history) != 3 || history[0].State != "down" {
		t.Error("partition hid real local history", len(history), err)
	}
	if r.count("down") == 0 {
		t.Error("incomplete peer history blocked an owed local outage indefinitely")
	}
}
