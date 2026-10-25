package canary

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uptime-canary/apps/node-go/internal/nodeconnection"
)

// A form loads several catalogues concurrently. Two transport jobs may run at
// once, but the other already bounded dashboard callers must wait, not fail as
// unavailable merely because the VPN has enough latency to overlap the reads.
func TestOutboundNodeAdmissionQueuesConcurrentReadsWithinGlobalBound(t *testing.T) {
	d, _, _ := dashboardRegistryFixture(t)
	key, _ := nodeconnection.RandomKey()
	id := key[:32]
	row := registeredNodeRecord{RegisteredNode: RegisteredNode{ID: id, NodeID: "queued-node", State: "starting", Connection: "outbound"}, ConnectionHash: digest(key)}
	d.mu.Lock()
	d.registry[id] = row
	d.mu.Unlock()
	epoch, err := d.connections.Open(id)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	release := make(chan struct{})
	var once sync.Once
	unlock := func() { once.Do(func() { close(release) }) }
	var workers sync.WaitGroup
	t.Cleanup(func() { unlock(); cancel(); d.connections.Close(); workers.Wait() })
	arrived := make(chan struct{}, 2)
	var active, maximum, dispatched atomic.Int32
	workers.Add(1)
	go func() {
		defer workers.Done()
		for ctx.Err() == nil {
			frame, e := d.connections.Poll(ctx, id, epoch)
			if e != nil {
				return
			}
			if frame == nil {
				continue
			}
			dispatched.Add(1)
			workers.Add(1)
			go func(frame nodeconnection.Request) {
				defer workers.Done()
				n := active.Add(1)
				for {
					old := maximum.Load()
					if old >= n || maximum.CompareAndSwap(old, n) {
						break
					}
				}
				select {
				case arrived <- struct{}{}:
				default:
				}
				select {
				case <-release:
				case <-ctx.Done():
					active.Add(-1)
					return
				}
				// Count work until response publication, not the few instructions
				// after Complete has already delivered it to a waiting caller.
				active.Add(-1)
				_ = d.connections.Complete(id, epoch, nodeconnection.Response{ID: frame.ID, Status: 200, ContentType: "application/json", NodeID: row.NodeID, Body: []byte("[]")})
			}(*frame)
		}
	}()
	const callers = 6
	var started sync.WaitGroup
	started.Add(callers)
	done := make(chan error, callers)
	for range callers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			started.Done()
			response, body, e := d.nodeRequest(ctx, row, "private-admission-role-fixture", "GET", "/api/monitors", nil)
			if e == nil && (response.StatusCode != 200 || string(body) != "[]") {
				e = fmt.Errorf("invalid queued response")
			}
			done <- e
		}()
	}
	started.Wait()
	for range 2 {
		select {
		case <-arrived:
		case <-ctx.Done():
			t.Fatal("two concurrent reads did not reach the node")
		}
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal("bounded concurrent catalogue read was refused instead of queued")
		}
		t.Fatal("held read completed without a peer response")
	case <-time.After(50 * time.Millisecond):
	}
	// A canceled waiter must release its global admission without consuming a
	// transport job or preventing the original burst from completing.
	waitContext, stopWaiting := context.WithCancel(ctx)
	defer stopWaiting()
	canceled := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		_, _, e := d.nodeRequest(waitContext, row, "private-canceled-role-fixture", "GET", "/api/monitors", nil)
		canceled <- e
	}()
	eventually(t, func() bool { return len(d.networkSlots) == callers+1 })
	stopWaiting()
	select {
	case e := <-canceled:
		if e == nil {
			t.Fatal("canceled admission waiter reported success")
		}
	case <-ctx.Done():
		t.Fatal("canceled admission waiter did not release")
	}
	unlock()
	for range callers {
		select {
		case e := <-done:
			if e != nil {
				t.Fatal("queued catalogue read failed", e)
			}
		case <-ctx.Done():
			t.Fatal("queued catalogue read did not finish")
		}
	}
	if maximum.Load() > nodeconnection.MaxPending {
		t.Fatal("per-node in-flight transport bound was exceeded")
	}
	if dispatched.Load() != callers || len(d.networkSlots) != 0 {
		t.Fatal("canceled waiter was dispatched or admission capacity leaked")
	}
}
