package canary

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestRepeatSlotsRecoveryAndNewIdentity(t *testing.T) {
	now, nowFn := clock()
	var down atomic.Bool
	down.Store(true)
	r := webhook(t)
	n := local(t, func(o *Options) { o.Now = nowFn; o.NotificationDelayMs = Ptr(int64(0)); o.Probe = controlled(&down) })
	in := input([]string{"a"}, "http://127.0.0.1:1", r.URL)
	in.RepeatAfterMs = Ptr(int64(1000))
	add(t, n, in)
	eventually(t, func() bool { return len(getOverview(t, n).Notifications) == 1 })
	now.Add(999)
	time.Sleep(60 * time.Millisecond)
	if len(r.all()) != 1 {
		t.Fatal(r.all())
	}
	now.Add(1)
	eventually(t, func() bool { return len(getOverview(t, n).Notifications) == 2 })
	now.Add(1000)
	eventually(t, func() bool { return len(getOverview(t, n).Notifications) == 3 })
	ds := r.all()
	if ds[1].Kind != "repeat" || ds[1].RepeatSlot != 1 || ds[2].RepeatSlot != 2 {
		t.Fatal(ds)
	}
	down.Store(false)
	now.Add(100)
	eventually(t, func() bool { return r.count("recovery") == 1 })
	now.Add(3000)
	time.Sleep(75 * time.Millisecond)
	if len(r.all()) != 4 {
		t.Fatal(r.all())
	}
	down.Store(true)
	now.Add(100)
	eventually(t, func() bool { return r.count("down") == 2 })
	ds = r.all()
	if ds[0].IncidentID == ds[4].IncidentID {
		t.Fatal("Later outage reused identity")
	}
	keys := map[string]bool{}
	for _, d := range ds {
		keys[d.Key] = true
	}
	if len(keys) != len(ds) {
		t.Fatal("Repeat keys not distinct", ds)
	}
}
func TestBoundedProviderRetryBursts(t *testing.T) {
	now, nowFn := clock()
	r := webhook(t)
	r.status.Store(503)
	n := local(t, func(o *Options) {
		o.Now = nowFn
		o.NotificationDelayMs = Ptr(int64(0))
		o.Probe = func(context.Context, Monitor) ProbeResult {
			return ProbeResult{State: "down", LatencyMs: 1, Message: "Retry probe"}
		}
	})
	add(t, n, input([]string{"a"}, "http://127.0.0.1:1", r.URL))
	eventually(t, func() bool { return len(r.all()) == 1 })
	for index, backoff := range []int64{1000, 2000, 4000, 8000, 60000} {
		eventually(t, func() bool {
			n.mu.Lock()
			defer n.mu.Unlock()
			for _, a := range n.attempts {
				if a.Failures == index+1 {
					return true
				}
			}
			return false
		})
		now.Add(backoff - 1)
		time.Sleep(45 * time.Millisecond)
		if len(r.all()) != index+1 {
			t.Fatal(index, r.all())
		}
		now.Add(1)
		eventually(t, func() bool { return len(r.all()) == index+2 })
	}
	if len(getOverview(t, n).Notifications) != 0 {
		t.Fatal("Failed sends created receipts")
	}
	key := r.all()[0].Key
	for _, d := range r.all() {
		if d.Key != key {
			t.Fatal("Retry changed idempotency key")
		}
	}
}
