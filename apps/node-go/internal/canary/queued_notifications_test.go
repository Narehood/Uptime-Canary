package canary

import (
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// Capture B before the peer merges A. Dispatch that exact queued job after the
// merge, exercising the final provider-I/O gate rather than a receipt helper.
func TestQueuedNotificationUsesCurrentAliasesAndRepeatReceipts(t *testing.T) {
	for _, kind := range []string{"down", "recovery", "repeat-same", "repeat-newer"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int64
			n, m, captured, a := queuedAliasFixture(t, doFunc(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
			}))
			a.Kind = kind
			if strings.HasPrefix(kind, "repeat") {
				a.Kind, a.Slot = "repeat", 3
			}
			merged := captured
			merged.ID, merged.Aliases = "A", []string{"A", "B"}
			if a.Kind == "recovery" {
				merged.Closed, merged.ResolvedAt = map[string]int64{"a": 2}, Ptr(n.now())
				n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "up", CheckedAt: n.now(), Message: "HTTP 200"}, Sequence: 2, Revision: m.Revision}
			}
			receipts := []Receipt{{NotificationView{ID: "down-on-A", MonitorID: m.ID, IncidentID: "A", Kind: "down", NodeID: "a", SentAt: n.now()}, 0}}
			if a.Kind != "down" {
				slot := a.Slot
				if kind == "repeat-newer" {
					slot++
				}
				receipts = append(receipts, Receipt{NotificationView{ID: "success-on-A", MonitorID: m.ID, IncidentID: "A", Kind: a.Kind, NodeID: "a", SentAt: n.now()}, slot})
			}
			inflight := m.ID + "/" + a.Kind
			n.sending[inflight] = true
			if err := n.mergeSnapshot(Snapshot{Incidents: []Incident{merged}, Receipts: receipts}); err != nil {
				t.Fatal(err)
			}
			body, _ := n.notificationBody(m, captured, a, nil)
			n.sendNotification(m, captured, a, inflight, body)
			if calls.Load() != 0 || n.sending[inflight] {
				t.Fatal("Known successful delivery reached provider after alias reconciliation", calls.Load())
			}
		})
	}
}

func TestQueuedRetryRetainsIdentityAndRefreshesAliases(t *testing.T) {
	var got delivery
	var calls int
	n, m, captured, a := queuedAliasFixture(t, doFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		got.Key = r.Header.Get("Idempotency-Key")
		return &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader(""))}, nil
	}))
	merged := captured
	merged.ID, merged.Aliases = "A", []string{"A", "B"}
	inflight := m.ID + "/down"
	n.sending[inflight] = true
	if err := n.mergeSnapshot(Snapshot{Incidents: []Incident{merged}}); err != nil {
		t.Fatal(err)
	}
	body, _ := n.notificationBody(m, captured, a, nil)
	n.sendNotification(m, captured, a, inflight, body)
	if calls != 1 || got.IncidentID != "B" || got.Key != a.ID || !slices.Equal(got.Incident.Aliases, []string{"A", "B"}) {
		t.Fatal("Retry identity or current aliases changed", calls, got)
	}
	persisted, ok, err := loadOne[Receipt](n.store, "receipts", a.ID)
	if err != nil || !ok || persisted.IncidentID != "B" {
		t.Fatal("Successful retry lost its stable incident identity", persisted, err)
	}
}

func TestRecoveryUsesSuccessfullyDeliveredIdentityAfterCanonicalAliasChanges(t *testing.T) {
	r := webhook(t)
	n, m, captured, _ := queuedAliasFixture(t, newHTTPClient())
	n.mu.Lock()
	m.WebhookURL = r.URL
	m, err := n.writeMonitor(m.MonitorInput, n.heads[m.ID], m.ID, false)
	if err != nil {
		n.mu.Unlock()
		t.Fatal(err)
	}
	n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "up", CheckedAt: n.now(), Message: "HTTP 200"}, Sequence: 2, Revision: m.Revision, CheckGeneration: m.CheckGeneration}
	merged := captured
	merged.ID, merged.Aliases = "A", []string{"A", "B"}
	merged.Closed, merged.ResolvedAt = map[string]int64{"a": 2}, Ptr(n.now())
	err = n.mergeSnapshot(Snapshot{Incidents: []Incident{merged}, Receipts: []Receipt{{NotificationView{ID: "delivered-B", MonitorID: m.ID, IncidentID: "B", Kind: "down", NodeID: "a", SentAt: n.now()}, 0}}})
	n.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return r.count("recovery") == 1 })
	got := r.all()[0]
	if got.IncidentID != "B" || !slices.Equal(got.Incident.Aliases, []string{"A", "B"}) {
		t.Fatal("Recovery replaced the delivered identity with a newly canonical alias", got)
	}
}

func queuedAliasFixture(t *testing.T, client Doer) (*Node, Monitor, Incident, attempt) {
	t.Helper()
	n, err := New(Options{NodeID: "a", DBPath: ":memory:", Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, NotificationDelayMs: Ptr(int64(0)), Client: client})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	m, err := n.writeMonitor(input([]string{"a"}, "http://127.0.0.1:1", "http://provider.example"), nil, "monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	captured := episode("B", map[string][]int64{"a": {1}})
	captured.Revision, captured.OpenedAt = m.Revision, n.now()
	n.saveEpisode(captured)
	n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: n.now(), Message: "HTTP 503"}, Sequence: 1, Revision: m.Revision, DownSince: Ptr(int64(1))}
	a := attempt{ID: "persisted-B-attempt", MonitorID: m.ID, IncidentID: "B", Kind: "down"}
	n.attempts[a.ID] = a
	if !n.dbPut("attempts", a.ID, a) {
		t.Fatal("Persist attempt")
	}
	return n, m, captured, a
}
