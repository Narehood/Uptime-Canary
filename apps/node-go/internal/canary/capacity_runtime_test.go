package canary

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// Profile the same monitor cardinality without network fixtures. There is no
// time-based assertion: the unchanged process proof supplies the liveness gate.
func TestCapacityRuntimeEvaluationProfile(t *testing.T) {
	n := capacityReviewNode(t, "capacity-a", []string{"capacity-a", "capacity-b"})
	// An occupied delivery pool exercises scheduling without issuing network I/O.
	n.notificationWorkers = make(chan struct{})
	channels := []string{}
	for _, name := range []string{"fast", "backlog"} {
		created := capacityReviewCreate(t, n, "/api/notification-channels", map[string]any{
			"name": name, "enabled": true, "config": map[string]any{"kind": "webhook", "webhook": map[string]any{
				"url": "http://127.0.0.1:1/" + name, "allowInsecureHTTP": true,
			}},
		})
		channels = append(channels, created["id"].(string))
	}
	for index := range 64 {
		created := capacityReviewCreate(t, n, "/api/monitors", map[string]any{
			"name": fmt.Sprintf("Capacity %d", index), "type": "http", "target": "http://127.0.0.1:1/service",
			"intervalMs": 1000, "timeoutMs": 500, "nodeIds": []string{"capacity-a", "capacity-b"}, "enabled": true,
			"notificationIds": channels,
		})
		id := created["id"].(string)
		m := n.current(id)
		for _, node := range m.NodeIDs {
			n.samples[id+"/"+node] = Sample{Observation: Observation{MonitorID: id, NodeID: node,
				State: "down", CheckedAt: n.now(), Message: "HTTP 503"}, Revision: m.Revision, Sequence: 1, DownSince: Ptr(int64(1))}
		}
	}
	start := time.Now()
	n.mu.Lock()
	for range 12 {
		n.evaluate()
		if n.fault != nil {
			n.mu.Unlock()
			t.Fatal(n.fault)
		}
	}
	n.mu.Unlock()
	if len(n.episodes) != 64 {
		t.Fatalf("expected one outage per monitor, got %d", len(n.episodes))
	}
	t.Logf("64 monitors, 12 evaluations: %s", time.Since(start))
	peer := capacityReviewNode(t, "capacity-b", []string{"capacity-a", "capacity-b"})
	peer.notificationWorkers = make(chan struct{})
	start = time.Now()
	for range 32 {
		for cursor := ""; ; {
			page, err := n.snapshotPage(cursor, "")
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(page)
			if err != nil {
				t.Fatal(err)
			}
			validated, err := peer.validateSnapshot(body, n.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := peer.applySnapshot(validated); err != nil {
				t.Fatal(err)
			}
			peer.evaluate()
			if page.Next == "" {
				break
			}
			cursor = page.Next
		}
	}
	t.Logf("64 monitors, 32 validated peer sweeps: %s", time.Since(start))
}
