package canary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const adminToken = "admin-test-8Ff2vR6mW9qB3sX7"
const clusterToken = "cluster-test-4Gg7pK9dN2jL6aC8"
const viewerToken = "viewer-test-5Hh8cT3bM7vQ2nD6"
const editorToken = "editor-test-9Jj3rZ5xP8wS1uF7"

type doFunc func(*http.Request) (*http.Response, error)

func (f doFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }
func eventually(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("Condition did not become true")
}
func local(t *testing.T, configure func(*Options)) *Node {
	t.Helper()
	o := Options{NodeID: "a", DBPath: ":memory:", Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, SyncIntervalMs: Ptr(int64(60)), PeerTimeoutMs: Ptr(int64(60)), NotificationDelayMs: Ptr(int64(160)), TickInterval: 15 * time.Millisecond}
	if configure != nil {
		configure(&o)
	}
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	if err = n.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := n.Stop(); err != nil {
			t.Error(err)
		}
	})
	return n
}
func api(t *testing.T, n *Node, path, method string, body any, token string) (int, []byte) {
	t.Helper()
	var b []byte
	if body != nil {
		var err error
		b, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	return apiBytes(t, n, path, method, b, token)
}
func apiBytes(t *testing.T, n *Node, path, method string, b []byte, token string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, n.URL()+path, bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	c := &http.Client{Timeout: 3 * time.Second}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	out, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, out
}
func getOverview(t *testing.T, n *Node) Overview {
	t.Helper()
	code, b := api(t, n, "/api/overview", "GET", nil, adminToken)
	if code != 200 {
		t.Fatalf("overview %d: %s", code, b)
	}
	var v Overview
	if json.Unmarshal(b, &v) != nil {
		t.Fatal("Invalid overview")
	}
	return v
}
func input(ids []string, target, hook string) MonitorInput {
	// Private loopback fixtures explicitly opt in to the unchanged fast schedule.
	return MonitorInput{Name: "Service", Type: "http", Target: target, InternalOnly: true, IntervalMs: 100, TimeoutMs: 70, NodeIDs: ids, Enabled: true, WebhookURL: hook, RepeatAfterMs: Ptr(int64(0))}
}
func add(t *testing.T, n *Node, in MonitorInput) Monitor {
	t.Helper()
	code, b := api(t, n, "/api/monitors", "POST", in, adminToken)
	if code != 201 {
		t.Fatalf("add %d: %s", code, b)
	}
	var m Monitor
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
func addGroup(t *testing.T, n *Node, in GroupInput) Group {
	t.Helper()
	code, b := api(t, n, "/api/groups", "POST", in, adminToken)
	if code != 201 {
		t.Fatalf("addGroup %d: %s", code, b)
	}
	var g Group
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	return g
}

type delivery struct {
	Kind       string   `json:"kind"`
	IncidentID string   `json:"incidentId"`
	MonitorID  string   `json:"monitorId"`
	Node       Identity `json:"node"`
	Incident   struct {
		Aliases []string `json:"aliases"`
	} `json:"incident"`
	RepeatSlot int64 `json:"repeatSlot"`
	Key        string
}
type receiver struct {
	URL        string
	mu         sync.Mutex
	deliveries []delivery
	status     atomic.Int64
}

func webhook(t *testing.T) *receiver {
	r := &receiver{deliveries: []delivery{}}
	r.status.Store(204)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var d delivery
		if err := json.NewDecoder(req.Body).Decode(&d); err != nil {
			w.WriteHeader(400)
			return
		}
		d.Key = req.Header.Get("Idempotency-Key")
		r.mu.Lock()
		r.deliveries = append(r.deliveries, d)
		r.mu.Unlock()
		w.WriteHeader(int(r.status.Load()))
	}))
	t.Cleanup(s.Close)
	r.URL = s.URL
	return r
}
func (r *receiver) all() []delivery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]delivery{}, r.deliveries...)
}
func (r *receiver) count(kind string) int {
	v := 0
	for _, d := range r.all() {
		if kind == "" || d.Kind == kind {
			v++
		}
	}
	return v
}
func clock() (*atomic.Int64, func() int64) {
	c := &atomic.Int64{}
	c.Store(time.Now().UnixMilli())
	return c, c.Load
}

type testCluster struct {
	nodes     []*Node
	mu        sync.RWMutex
	urls      map[string]string
	partition [][]string
	client    *http.Client
}

func cluster(t *testing.T, ids []string, configure func(*Options)) *testCluster {
	c := &testCluster{urls: map[string]string{}, client: newHTTPClient()}
	t.Cleanup(c.client.CloseIdleConnections)
	for _, id := range ids {
		nodeID := id
		n := local(t, func(o *Options) {
			o.NodeID = nodeID
			o.Peers = []Peer{}
			for _, p := range ids {
				if p != nodeID {
					o.Peers = append(o.Peers, Peer{ID: p, URL: "http://" + p + ".test"})
				}
			}
			if configure != nil {
				configure(o)
			}
			o.PeerClient = doFunc(func(r *http.Request) (*http.Response, error) {
				target := strings.Split(r.URL.Hostname(), ".")[0]
				c.mu.RLock()
				dest := c.urls[target]
				allowed := len(c.partition) == 0
				for _, g := range c.partition {
					foundA, foundB := false, false
					for _, x := range g {
						foundA = foundA || x == nodeID
						foundB = foundB || x == target
					}
					allowed = allowed || foundA && foundB
				}
				c.mu.RUnlock()
				if dest == "" || !allowed {
					return nil, fmt.Errorf("Test transport partition")
				}
				parsed, _ := url.Parse(dest)
				copy := r.Clone(r.Context())
				u := *r.URL
				u.Scheme = parsed.Scheme
				u.Host = parsed.Host
				copy.URL = &u
				copy.Host = parsed.Host
				return c.client.Do(copy)
			})
		})
		c.nodes = append(c.nodes, n)
		c.mu.Lock()
		c.urls[id] = n.URL()
		c.mu.Unlock()
	}
	return c
}
func (c *testCluster) split(groups ...[]string) { c.mu.Lock(); c.partition = groups; c.mu.Unlock() }
func (c *testCluster) heal()                    { c.mu.Lock(); c.partition = nil; c.mu.Unlock() }
func controlled(state *atomic.Bool) func(context.Context, Monitor) ProbeResult {
	return func(context.Context, Monitor) ProbeResult {
		if state.Load() {
			return ProbeResult{State: "down", LatencyMs: 1, Message: "Controlled down"}
		}
		return ProbeResult{State: "up", LatencyMs: 1, Message: "Controlled up"}
	}
}
