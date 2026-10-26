package canary

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"
)

func splitLifecyclePeers(c *testCluster, groups ...[]string) error {
	c.split(groups...)
	// New cross-partition calls are refused now, but a previously admitted
	// request can still return a page. Finish those rounds before edits begin.
	// Otherwise a response prepared after an edit can cross the test partition
	// and correctly make the other editor's original revision stale.
	deadline := time.Now().Add(8 * time.Second)
	for _, node := range c.nodes {
		for {
			node.mu.Lock()
			idle := !node.syncing
			node.mu.Unlock()
			if idle {
				break
			}
			if time.Now().After(deadline) {
				return errors.New("Pre-partition peer work did not drain")
			}
			time.Sleep(15 * time.Millisecond)
		}
	}
	return nil
}

func TestLifecyclePartitionDrainsAcknowledgedPeerPage(t *testing.T) {
	b := local(t, func(o *Options) {
		o.NodeID = "b"
		o.Peers = []Peer{{ID: "a", URL: "http://127.0.0.1:1"}}
		o.PeerClient = doFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("controlled fixture partition")
		})
	})
	in := input([]string{"a", "b"}, "http://127.0.0.1:1", "")
	in.Enabled = false
	m := add(t, b, in)
	body := captureLifecyclePage(t, b)
	entered, release := make(chan struct{}), make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	a := local(t, func(o *Options) {
		o.Peers = []Peer{{ID: "b", URL: "http://127.0.0.1:1"}}
		// Hold one acknowledged response without racing a probe deadline.
		o.SyncIntervalMs, o.PeerTimeoutMs = Ptr(int64(5000)), Ptr(int64(5000))
		o.PeerClient = doFunc(func(r *http.Request) (*http.Response, error) {
			select {
			case <-entered:
			default:
				close(entered)
			}
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body))}, nil
		})
	})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Controlled peer exchange did not start")
	}
	c := &testCluster{nodes: []*Node{a}}
	done := make(chan error, 1)
	go func() {
		done <- splitLifecyclePeers(c, []string{"a"}, []string{"b"})
	}()
	eventually(t, func() bool {
		c.mu.RLock()
		defer c.mu.RUnlock()
		return len(c.partition) == 2
	})
	select {
	case err := <-done:
		t.Fatal("Partition returned while an acknowledged peer page was still pending", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	released = true
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Partition did not finish after the peer exchange drained")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current := a.current(m.ID)
	if a.syncing || current == nil || current.Revision != m.Revision {
		t.Fatal("Partition did not finish the pre-partition merge before allowing edits")
	}
}
