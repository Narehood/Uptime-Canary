package canary

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type peerCancellationBody struct {
	*bytes.Reader
	read chan struct{}
	once sync.Once
}

func (b *peerCancellationBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		b.once.Do(func() { close(b.read) })
	}
	return n, err
}

func TestPeerSnapshotCancelledBeforeAdmissionDoesNotPublish(t *testing.T) {
	for _, queued := range []bool{false, true} {
		name := "already-cancelled"
		if queued {
			name = "cancelled-with-lock-unavailable"
		}
		t.Run(name, func(t *testing.T) {
			n := cr2New(t, cr2Options(t, false))
			sample := Sample{Observation: Observation{MonitorID: "cancel-monitor", NodeID: "b", State: "up", CheckedAt: n.now(), Message: "Peer cancellation fixture"}, Revision: "cancel-revision", Sequence: 7}
			body := cr2Body(t, Snapshot{Observations: []Sample{sample}})
			seen := int64(12345)
			progress := syncProgress{Send: "2/previous-receipt", Receive: "2a/previous-fact"}
			n.peerSeen["b"], n.peerProgress["b"] = seen, progress
			request := func(ctx context.Context, reader io.Reader) *http.Request {
				r := httptest.NewRequest(http.MethodPost, "/internal/snapshot", reader).WithContext(ctx)
				r.Header.Set("Authorization", "Bearer "+clusterToken)
				r.Header.Set("X-Canary-Node-Id", "b")
				return r
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reader := &peerCancellationBody{Reader: bytes.NewReader(body), read: make(chan struct{})}
			w := httptest.NewRecorder()
			done := make(chan struct{})
			locked := false
			if queued {
				n.mu.Lock()
				locked = true
			} else {
				cancel()
			}
			defer func() {
				if locked {
					n.mu.Unlock()
				}
			}()
			go func() { n.ServeHTTP(w, request(ctx, reader)); close(done) }()
			if queued {
				select {
				case <-reader.read:
				case <-time.After(5 * time.Second):
					t.Fatal("peer handler did not consume its bounded body")
				}
				cancel()
				n.mu.Unlock()
				locked = false
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled peer handler did not return")
			}
			stored, err := loadAll[Sample](n.store, "samples")
			if err != nil {
				t.Fatal(err)
			}
			if w.Code == http.StatusOK || len(n.samples) != 0 || len(stored) != 0 || n.peerSeen["b"] != seen || n.peerProgress["b"] != progress {
				t.Fatalf("abandoned request published state or acknowledged success: HTTP %d, memory=%d disk=%d seen=%d progress=%+v", w.Code, len(n.samples), len(stored), n.peerSeen["b"], n.peerProgress["b"])
			}
			// Cancellation does not consume the page. A live retry still commits
			// and acknowledges the original observation without advancing the
			// receiver's independently owned outgoing cursors.
			retry := httptest.NewRecorder()
			n.ServeHTTP(retry, request(context.Background(), bytes.NewReader(body)))
			stored, err = loadAll[Sample](n.store, "samples")
			if err != nil || retry.Code != http.StatusOK || !equalJSON(n.samples["cancel-monitor/b"], sample) || !equalJSON(stored["cancel-monitor/b"], sample) || n.peerProgress["b"] != progress || n.peerSeen["b"] <= seen {
				t.Fatal("live retry did not durably commit and acknowledge the page", retry.Code, err)
			}
		})
	}
}
