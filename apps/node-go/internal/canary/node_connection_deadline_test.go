package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uptime-canary/apps/node-go/internal/nodeconnection"
)

// A result can reach the dashboard before its HTTP acknowledgement reaches the
// node. Keep both node execution slots occupied by delayed acknowledgements,
// deliver a third mutation, let its original deadline expire, then release the
// slots. Waiting locally must not give that mutation a fresh execution budget.
func TestNodeConnectionExpiredMutationIsNotDispatchedAfterLocalSlotWait(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	d.connectionEnabled = true
	var gate atomic.Bool
	var held atomic.Int32
	var reopened atomic.Int32
	acknowledged := make(chan struct{}, 2)
	mutationDelivered := make(chan struct{}, 1)
	handled := make(chan struct{}, 2)
	release := make(chan struct{})
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unlock)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorded := httptest.NewRecorder()
		d.ConnectionHandler().ServeHTTP(recorded, r)
		if gate.Load() && r.URL.Path == "/connect/v1/result" {
			if held.Add(1) <= 2 {
				acknowledged <- struct{}{}
				select {
				case <-release:
				case <-r.Context().Done():
				}
			} else {
				select {
				case handled <- struct{}{}:
				default:
				}
			}
		}
		if gate.Load() && r.URL.Path == "/connect/v1/poll" && recorded.Code == 200 {
			var frame nodeconnection.Request
			if json.Unmarshal(recorded.Body.Bytes(), &frame) == nil && frame.Method == "POST" && frame.Path == "/api/monitors" {
				select {
				case mutationDelivered <- struct{}{}:
				default:
				}
			}
		}
		if gate.Load() && r.URL.Path == "/connect/v1/open" {
			reopened.Add(1)
			select {
			case handled <- struct{}{}:
			default:
			}
		}
		for key, values := range recorded.Header() {
			w.Header()[key] = values
		}
		w.WriteHeader(recorded.Code)
		_, _ = w.Write(recorded.Body.Bytes())
	}))
	t.Cleanup(server.Close)
	d.connectionURL = server.URL
	v := connectionInvitation(t, d, owner, "deadline-node")
	n, err := New(Options{NodeID: "deadline-node", Host: "127.0.0.1", Port: Ptr(0), DBPath: ":memory:", IdentityDirectory: connectionIdentityDirectory(t), AdminToken: adminToken, ClusterToken: clusterToken, DashboardURL: server.URL, DashboardEnrollToken: v.Token})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unlock(); _ = n.Stop() })
	n.connector.transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	if err = n.Start(); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.invitations[v.ID].State == "connected" && d.connections.LastSeen(v.ID) > 0
	})
	if dashboardHTTP(d, "POST", "/api/dashboard/invitations/"+v.ID+"/confirm", owner, "", nil).Code != 201 {
		t.Fatal("confirmation failed")
	}
	d.mu.Lock()
	row := d.registry[v.ID]
	d.mu.Unlock()
	secrets, err := d.openSecrets(row)
	if err != nil {
		t.Fatal(err)
	}
	gate.Store(true)
	reads := make(chan error, 2)
	for range 2 {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, _, err := d.nodeRequest(ctx, row, secrets.Admin, "GET", "/api/monitors", nil)
			reads <- err
		}()
	}
	for range 2 {
		select {
		case <-acknowledged:
		case <-time.After(4 * time.Second):
			t.Fatal("both acknowledgement waits were not held")
		}
	}
	for range 2 {
		if err := <-reads; err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	mutation := make(chan error, 1)
	body, _ := json.Marshal(MonitorInput{Name: "Must not be created", Type: "manual", Target: "", NodeIDs: []string{n.ID}, Enabled: false, IntervalMs: 1000, TimeoutMs: 500, Manual: &ManualMonitorOptions{State: "up"}})
	go func() {
		_, _, err := d.nodeRequest(ctx, row, secrets.Admin, "POST", "/api/monitors", body)
		mutation <- err
	}()
	select {
	case <-mutationDelivered:
	case <-ctx.Done():
		t.Fatal("mutation did not reach the waiting node before its deadline")
	}
	<-ctx.Done()
	if err := <-mutation; err == nil {
		t.Fatal("expired caller reported success")
	}
	// The protocol conveys remaining milliseconds, not synchronized wall
	// clocks. Allow loopback response delivery time before claiming the node's
	// received budget has also expired. Both execution slots stay held here.
	transitAllowance := time.NewTimer(500 * time.Millisecond)
	<-transitAllowance.C
	unlock()
	// A locally expired job must neither execute nor tear down the shared
	// session. A new read proves progress without allowing a reconnect retry.
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer probeCancel()
	if _, _, err := d.nodeRequest(probeCtx, row, secrets.Admin, "GET", "/api/monitors", nil); err != nil {
		t.Fatal("expired local work interrupted the next read", err)
	}
	if reopened.Load() != 0 {
		t.Fatal("a request deadline reset the shared connection session")
	}
	select {
	case <-handled:
	case <-time.After(4 * time.Second):
		t.Fatal("node did not finish the queued request or reconnect")
	}
	w := dashboardHTTP(n, "GET", "/api/monitors", adminToken, "", nil)
	var monitors []MonitorView
	decodeError := json.Unmarshal(w.Body.Bytes(), &monitors)
	if w.Code != 200 || decodeError != nil || len(monitors) != 0 {
		t.Fatalf("a mutation was dispatched after its original deadline expired (status %d, monitors %d, unreadable %t)", w.Code, len(monitors), decodeError != nil)
	}
}
