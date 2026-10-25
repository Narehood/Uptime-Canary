package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uptime-canary/apps/node-go/internal/nodeconnection"
)

func TestNodeConnectionStaleResultDoesNotCancelSibling(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	d.connectionEnabled = true
	var gate atomic.Bool
	var results, reopened atomic.Int32
	firstHeld, secondHeld, thirdDelivered := make(chan struct{}, 1), make(chan struct{}, 1), make(chan struct{}, 1)
	firstRelease, secondRelease := make(chan struct{}), make(chan struct{})
	var firstOnce, secondOnce sync.Once
	unlockFirst := func() { firstOnce.Do(func() { close(firstRelease) }) }
	unlockSecond := func() { secondOnce.Do(func() { close(secondRelease) }) }
	staleStatus := make(chan int, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order := int32(0)
		if gate.Load() && r.URL.Path == "/connect/v1/open" {
			reopened.Add(1)
		}
		if gate.Load() && r.URL.Path == "/connect/v1/result" {
			order = results.Add(1)
			var release <-chan struct{}
			if order == 1 {
				firstHeld <- struct{}{}
				release = firstRelease
			} else if order == 2 {
				secondHeld <- struct{}{}
				release = secondRelease
			}
			if release != nil {
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
		}
		recorded := httptest.NewRecorder()
		d.ConnectionHandler().ServeHTTP(recorded, r)
		if order == 1 {
			staleStatus <- recorded.Code
		}
		if gate.Load() && r.URL.Path == "/connect/v1/poll" && recorded.Code == 200 {
			var frame nodeconnection.Request
			if json.Unmarshal(recorded.Body.Bytes(), &frame) == nil && frame.Path == "/api/tags" {
				thirdDelivered <- struct{}{}
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
	v := connectionInvitation(t, d, owner, "stale-result-node")
	n, err := New(Options{NodeID: v.NodeID, Host: "127.0.0.1", Port: Ptr(0), DBPath: ":memory:", IdentityDirectory: t.TempDir(), AdminToken: adminToken, ClusterToken: clusterToken, DashboardURL: server.URL, DashboardEnrollToken: v.Token})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unlockFirst(); unlockSecond(); _ = n.Stop() })
	n.connector.transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	if err := n.Start(); err != nil {
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
	credentials, err := d.openSecrets(row)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	await := func(ch <-chan struct{}, what string) {
		t.Helper()
		select {
		case <-ch:
		case <-ctx.Done():
			t.Fatal(what)
		}
	}
	read := func(ctx context.Context, path string) <-chan error {
		done := make(chan error, 1)
		go func() { _, _, err := d.nodeRequest(ctx, row, credentials.Admin, "GET", path, nil); done <- err }()
		return done
	}
	gate.Store(true)
	firstCtx, cancelFirst := context.WithCancel(ctx)
	defer cancelFirst()
	first := read(firstCtx, "/api/monitors")
	await(firstHeld, "first result was not held")
	cancelFirst()
	if err := <-first; err == nil {
		t.Fatal("cancelled caller reported success")
	}
	second := read(ctx, "/api/monitors")
	await(secondHeld, "sibling result was not held")
	third := read(ctx, "/api/tags")
	await(thirdDelivered, "third request did not reach the occupied node")
	unlockFirst()
	if status := <-staleStatus; status != 410 {
		t.Fatalf("cancelled request result was not distinguished from an epoch error: %d", status)
	}
	// Both local slots were occupied. Completing this third request proves
	// that the first result's 410 was handled without cancelling the session.
	if err := <-third; err != nil {
		t.Fatal("stale result interrupted another request", err)
	}
	unlockSecond()
	if err := <-second; err != nil {
		t.Fatal("stale result cancelled its in-flight sibling", err)
	}
	if reopened.Load() != 0 {
		t.Fatal("a stale request result reset the session")
	}
}

func TestNodeConnectionGoneIsOnlyForAStaleResult(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	d.connectionEnabled, d.connectionURL = true, "https://connection.fixture.invalid"
	v := connectionInvitation(t, d, owner, "result-code-node")
	key, _ := nodeconnection.RandomKey()
	if claimFixture(d, v, v.NodeID, key).Code != 201 {
		t.Fatal("claim failed")
	}
	opened := connectionRequest(d, "/connect/v1/open", key, v.ID, map[string]string{"registrationId": v.ID, "nodeId": v.NodeID})
	var session struct{ Session string }
	if opened.Code != 200 || json.Unmarshal(opened.Body.Bytes(), &session) != nil {
		t.Fatal("session failed")
	}
	for _, tc := range []struct {
		name, token, epoch string
		status, want       int
	}{
		{"stale", key, session.Session, 200, 410},
		{"changed epoch", key, "", 200, 409},
		{"refused authority", strings.Repeat("f", 64), session.Session, 200, 401},
		{"invalid response", key, session.Session, 100, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := map[string]any{"registrationId": v.ID, "session": tc.epoch, "id": strings.Repeat("e", 64), "status": tc.status, "nodeId": v.NodeID}
			if got := connectionRequest(d, "/connect/v1/result", tc.token, v.ID, input).Code; got != tc.want {
				t.Fatalf("result error classification: got %d want %d", got, tc.want)
			}
		})
	}
}
