package canary

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestMonitorRateConsentRequiredForNormalAndRetryIntervals(t *testing.T) {
	for _, tc := range []struct {
		name     string
		interval int64
		retry    *int64
		consent  bool
		status   int
	}{
		{"one minute", 60_000, nil, false, 201},
		{"sub minute", 59_999, nil, false, 400},
		{"internal fast", 100, nil, true, 201},
		{"internal lower bound", 99, nil, true, 400},
		{"one minute retry", 60_000, Ptr(int64(60_000)), false, 201},
		{"fast retry without consent", 60_000, Ptr(int64(59_999)), false, 400},
		{"internal fast retry", 60_000, Ptr(int64(100)), true, 201},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := dashboardRemoteFixture(t, "a")
			in := input([]string{"a"}, "http://127.0.0.1:9", "")
			in.IntervalMs, in.RetryIntervalMs, in.InternalOnly = tc.interval, tc.retry, tc.consent
			w := dashboardHTTP(n, "POST", "/api/monitors", adminToken, "", in)
			if w.Code != tc.status {
				t.Fatalf("rate admission: %d %s", w.Code, w.Body.String())
			}
			if tc.status == 400 && len(n.heads) != 0 {
				t.Fatal("rejected rate wrote configuration")
			}
		})
	}
}

func TestMonitorRateConsentCannotBeInferredAndCanBeRemoved(t *testing.T) {
	n := dashboardRemoteFixture(t, "a")
	in := input([]string{"a"}, "http://127.0.0.1:9", "")
	for _, value := range []any{nil, "true", 1} {
		body, _ := json.Marshal(in)
		var fields map[string]any
		_ = json.Unmarshal(body, &fields)
		fields["internalOnly"] = value
		if w := dashboardHTTP(n, "POST", "/api/monitors", adminToken, "", fields); w.Code != 400 {
			t.Fatalf("non-boolean consent accepted: %d", w.Code)
		}
	}
	m := groupMonitorFixture(t, n, in)
	m.InternalOnly = false
	if w := dashboardHTTP(n, "PUT", "/api/monitors/"+m.ID, adminToken, "", m); w.Code != 400 || n.current(m.ID).Revision != m.Revision {
		t.Fatal("removing consent retained a fast active schedule", w.Code)
	}
	m.IntervalMs = 60_000
	w := dashboardHTTP(n, "PUT", "/api/monitors/"+m.ID, adminToken, "", m)
	if w.Code != 200 || n.current(m.ID).InternalOnly || bytes.Contains(w.Body.Bytes(), []byte(`"internalOnly"`)) {
		t.Fatal("one-minute edit did not remove the acknowledgement", w.Code, w.Body.String())
	}
}

func TestFleetRateConsentCannotBypassNodeAdmission(t *testing.T) {
	n := placementFixtureNode(t, ":memory:")
	command := placementFixtureCommand(t, 1, "", false)
	in := placementFixtureInput()
	in.InternalOnly, in.Enabled = false, true
	command.Monitor, _ = json.Marshal(in)
	placementFixtureWrite(t, n, command, 400)
	if len(n.heads) != 0 {
		t.Fatal("fleet placement bypassed the rate minimum")
	}
	d, owner, _ := dashboardRegistryFixture(t)
	server := httptest.NewServer(n)
	defer server.Close()
	row := enrollDashboardNode(t, d, owner, server.URL)
	w := dashboardHTTP(d, "POST", "/api/dashboard/fleet/monitors", owner, "", map[string]any{
		"requestId": placementFixtureID, "registrationIds": []string{row.ID}, "monitor": in,
	})
	if w.Code != 400 || len(d.fleet) != 0 {
		t.Fatal("dashboard admitted a fast plan without consent", w.Code, w.Body.String())
	}
}

func TestLegacyFastMonitorCanBePausedWithoutGrantingConsent(t *testing.T) {
	n := dashboardRemoteFixture(t, "a")
	in := input([]string{"a"}, "http://127.0.0.1:9", "")
	in.InternalOnly = false
	m, err := n.writeMonitor(in, nil, "legacy-pause-rate", false)
	if err != nil {
		t.Fatal(err)
	}
	m.Enabled = false
	w := dashboardHTTP(n, "PUT", "/api/monitors/"+m.ID, adminToken, "", m)
	if w.Code != 200 || n.current(m.ID).InternalOnly || n.current(m.ID).Enabled {
		t.Fatal("pausing a legacy monitor required consent", w.Code, w.Body.String())
	}
	m = *n.current(m.ID)
	m.Enabled = true
	if w := dashboardHTTP(n, "PUT", "/api/monitors/"+m.ID, adminToken, "", m); w.Code != 400 {
		t.Fatal("resuming a fast legacy monitor bypassed explicit consent", w.Code)
	}
}

func TestLegacyMonitorRateIsCappedWithoutRewritingRevision(t *testing.T) {
	for _, state := range []string{"up", "down", "pending", "unknown"} {
		t.Run(state, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy-rate.db")
			n := causalCapacityNode(t, "a", path)
			in := input([]string{"a"}, "http://127.0.0.1:9", "")
			in.InternalOnly, in.RetryIntervalMs = false, Ptr(int64(100))
			m, err := n.writeMonitor(in, nil, "legacy-fast-monitor", false)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(m)
			if _, err := parseMonitor(before, n.known); err != nil {
				t.Fatal("legacy configuration no longer loads", err)
			}
			if err := put(n.store.db, "settings", "causalSchema", peerProtocol-1); err != nil {
				t.Fatal(err)
			}
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = causalCapacityNode(t, "a", path)
			after, _ := json.Marshal(n.current(m.ID))
			if !bytes.Equal(before, after) {
				t.Fatal("startup rewrote immutable monitor settings or inferred consent")
			}
			var clock, calls atomic.Int64
			clock.Store(maintenanceClock(12, 0))
			n.now, n.nextSync = clock.Load, maxSafe
			n.probe = func(context.Context, Monitor) ProbeResult {
				calls.Add(1)
				return ProbeResult{State: state, Message: "private rate fixture"}
			}
			tick := func() { n.tick(); n.wg.Wait() }
			tick()
			if calls.Load() != 1 || n.Error() != nil {
				t.Fatal("initial legacy probe failed", calls.Load(), n.Error())
			}
			clock.Add(59_999)
			tick()
			if calls.Load() != 1 {
				t.Fatal("legacy normal or retry interval bypassed one-minute floor")
			}
			clock.Add(1)
			tick()
			if calls.Load() != 2 {
				t.Fatal("legacy probe did not resume at one minute", calls.Load())
			}
		})
	}
}

func TestMonitorRateConsentReplicatesWithMonitorIdentity(t *testing.T) {
	a, b := causalCapacityNode(t, "a", ":memory:"), causalCapacityNode(t, "b", ":memory:")
	in := input([]string{"a", "b"}, "http://127.0.0.1:9", "")
	m, err := a.writeMonitor(in, nil, "internal-rate-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, page := range causalCapacityPages(t, a) {
		raw, _ := json.Marshal(page)
		decoded, err := b.validateSnapshot(raw, "a")
		if err != nil {
			t.Fatal("wire decoder dropped rate consent", err)
		}
		if err := b.applySnapshot(decoded); err != nil {
			t.Fatal(err)
		}
	}
	if copy := b.current(m.ID); copy == nil || !copy.InternalOnly || copy.Revision != m.Revision || copy.IntervalMs != m.IntervalMs {
		t.Fatal("replication lost the explicit consent or original monitor identity", copy)
	}
}
