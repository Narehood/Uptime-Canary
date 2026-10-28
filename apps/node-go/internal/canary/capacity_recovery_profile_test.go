package canary

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// Diagnostic recovery profile with a fixed private workload. Run only with
// -benchtime=1x and an owned CPU-profile path; this is not acceptance evidence.
func BenchmarkCapacityRecoveryProfile(b *testing.B) {
	if b.N != 1 {
		b.Fatal("use -benchtime=1x for this fixed, disposable workload")
	}
	for range b.N {
		var down, blocked atomic.Bool
		blocked.Store(true)
		var accepted atomic.Int64
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if down.Load() {
				w.WriteHeader(503)
			}
			_, _ = w.Write([]byte("fixture"))
		}))
		b.Cleanup(target.Close)
		sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			if r.URL.Path == "/backlog" && blocked.Load() {
				time.Sleep(75 * time.Millisecond)
				w.Header().Set("Retry-After", "4")
				w.WriteHeader(429)
			} else {
				accepted.Add(1)
				w.WriteHeader(204)
			}
		}))
		b.Cleanup(sink.Close)
		paths := map[string]string{}
		makeNode := func(id, peer string) *Node {
			if paths[id] == "" {
				paths[id] = filepath.Join(b.TempDir(), "node.db")
			}
			n, err := New(Options{NodeID: id, DBPath: paths[id], Port: Ptr(0),
				AdminToken: adminToken, ClusterToken: clusterToken, Peers: []Peer{{ID: peer, URL: "http://127.0.0.1:1"}},
				SyncIntervalMs: Ptr(int64(150)), NotificationDelayMs: Ptr(int64(750)), PeerTimeoutMs: Ptr(int64(1500)), HistoryLimit: 8})
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = n.Stop() })
			if err := n.Start(); err != nil {
				b.Fatal(err)
			}
			return n
		}
		a, peer := makeNode("capacity-a", "capacity-b"), makeNode("capacity-b", "capacity-a")
		aURL, bURL := a.URL(), peer.URL()
		a.mu.Lock()
		a.settings.Peers[0].URL = bURL
		a.mu.Unlock()
		peer.mu.Lock()
		peer.settings.Peers[0].URL = aURL
		peer.mu.Unlock()
		post := func(path string, input any) map[string]any {
			body, _ := json.Marshal(input)
			r := httptest.NewRequest("POST", path, bytes.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+adminToken)
			w := httptest.NewRecorder()
			a.ServeHTTP(w, r)
			if w.Code != 201 {
				b.Fatalf("fixture create failed: %d", w.Code)
			}
			var out map[string]any
			if json.Unmarshal(w.Body.Bytes(), &out) != nil {
				b.Fatal("invalid fixture result")
			}
			return out
		}
		channels := []string{}
		for _, name := range []string{"fast", "backlog"} {
			c := post("/api/notification-channels", map[string]any{"name": name, "enabled": true,
				"config": map[string]any{"kind": "webhook", "webhook": map[string]any{"url": sink.URL + "/" + name, "allowInsecureHTTP": true}}})
			channels = append(channels, c["id"].(string))
		}
		for index := range 64 {
			post("/api/monitors", map[string]any{"name": fmt.Sprint("Capacity ", index), "type": "http", "target": target.URL,
				"internalOnly": true, "intervalMs": 1000, "timeoutMs": 500, "nodeIds": []string{a.ID, peer.ID}, "enabled": true, "notificationIds": channels})
		}
		time.Sleep(3 * time.Second)
		down.Store(true)
		time.Sleep(8 * time.Second)
		if err := a.Stop(); err != nil {
			b.Fatal(err)
		}
		if err := peer.Stop(); err != nil {
			b.Fatal(err)
		}
		a, peer = makeNode("capacity-a", "capacity-b"), makeNode("capacity-b", "capacity-a")
		a.mu.Lock()
		a.settings.Peers[0].URL = peer.URL()
		a.mu.Unlock()
		peer.mu.Lock()
		peer.settings.Peers[0].URL = a.URL()
		peer.mu.Unlock()
		blocked.Store(false)
		time.Sleep(5 * time.Second)
		down.Store(false)
		time.Sleep(20 * time.Second)
		b.Logf("accepts during diagnostic profile: %d", accepted.Load())
	}
}
