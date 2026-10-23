package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func TestBoundedHTTPProbes(t *testing.T) {
	m := Monitor{MonitorInput: input([]string{"a"}, "http://127.0.0.1:1", "")}
	client := newHTTPClient()
	defer client.CloseIdleConnections()
	hung := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer hung.Close()
	m.Target = hung.URL
	start := time.Now()
	r := HTTPProbe(context.Background(), m, client)
	if r.State != "down" || time.Since(start) > time.Second {
		t.Fatal(r)
	}
	var redirects atomic.Int64
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirects.Add(1)
		w.Header().Set("Location", "/")
		w.WriteHeader(302)
	}))
	defer loop.Close()
	m.Target = loop.URL
	r = HTTPProbe(context.Background(), m, client)
	if r.State != "down" || redirects.Load() != 6 {
		t.Fatal(r, redirects.Load())
	}
	unsafe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "file:///private")
		w.WriteHeader(302)
	}))
	defer unsafe.Close()
	m.Target = unsafe.URL
	if HTTPProbe(context.Background(), m, client).State != "down" {
		t.Fatal("Unsafe redirect accepted")
	}
	closed := make(chan struct{})
	infinite := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("Headers"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(closed)
	}))
	defer infinite.Close()
	m.Target = infinite.URL
	if HTTPProbe(context.Background(), m, client).State != "up" {
		t.Fatal("Body consumed")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Response body left open")
	}
}
func TestActualICMPAndFixedArguments(t *testing.T) {
	args, err := PingArguments("127.0.0.1", 500, "linux")
	if err != nil || !slices.Equal(args, []string{"-n", "-c", "1", "-W", "1", "--", "127.0.0.1"}) {
		t.Fatal(args, err)
	}
	if _, err = PingArguments("-n 100", 500, "windows"); err == nil {
		t.Fatal("Unsafe ping target accepted")
	}
	if _, err = exec.LookPath("ping"); err != nil {
		t.Skip("Actual ICMP requires ping on PATH")
	}
	m := Monitor{MonitorInput: input([]string{"a"}, "127.0.0.1", "")}
	m.Type = "ping"
	m.TimeoutMs = 2000
	r := PingProbe(context.Background(), m)
	if r.State != "up" {
		t.Fatal(r)
	}
}
func TestNoOverlappingProbesAndDiscardEditedCompletions(t *testing.T) {
	var calls, running, maximum atomic.Int64
	release := make(chan struct{}, 2)
	n := local(t, func(o *Options) {
		o.Probe = func(context.Context, Monitor) ProbeResult {
			calls.Add(1)
			r := running.Add(1)
			for {
				v := maximum.Load()
				if r <= v || maximum.CompareAndSwap(v, r) {
					break
				}
			}
			<-release
			running.Add(-1)
			return ProbeResult{State: "down", LatencyMs: 1, Message: "Old result"}
		}
	})
	t.Cleanup(func() { close(release) })
	created := add(t, n, input([]string{"a"}, "http://127.0.0.1:1", ""))
	eventually(t, func() bool { return calls.Load() == 1 })
	time.Sleep(180 * time.Millisecond)
	if calls.Load() != 1 || maximum.Load() != 1 {
		t.Fatal(calls.Load(), maximum.Load())
	}
	created.Enabled = false
	code, b := api(t, n, "/api/monitors/"+created.ID, "PUT", created, adminToken)
	if code != 200 {
		t.Fatal(code, string(b))
	}
	var paused Monitor
	_ = json.Unmarshal(b, &paused)
	release <- struct{}{}
	time.Sleep(60 * time.Millisecond)
	code, b = api(t, n, "/api/monitors/"+created.ID+"/history", "GET", nil, adminToken)
	if code != 200 || string(b) != "[]" {
		t.Fatal(code, string(b))
	}
	paused.Enabled = true
	paused.Target = "http://127.0.0.1:2"
	code, b = api(t, n, "/api/monitors/"+created.ID, "PUT", paused, adminToken)
	var edited Monitor
	_ = json.Unmarshal(b, &edited)
	if code != 200 {
		t.Fatal(code, string(b))
	}
	eventually(t, func() bool { return calls.Load() == 2 })
	code, _ = api(t, n, "/api/monitors/"+created.ID, "DELETE", map[string]string{"revision": edited.Revision}, adminToken)
	if code != 204 {
		t.Fatal(code)
	}
	release <- struct{}{}
	time.Sleep(60 * time.Millisecond)
	code, b = api(t, n, "/api/monitors/"+created.ID+"/history", "GET", nil, adminToken)
	if code != 200 || string(b) != "[]" {
		t.Fatal(code, string(b))
	}
}
func TestHTTPTimeoutsAreRecordedAsDown(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer target.Close()
	n := local(t, nil)
	m := add(t, n, input([]string{"a"}, target.URL, ""))
	eventually(t, func() bool { v := getOverview(t, n); return v.Monitors[0].State == "down" })
	code, b := api(t, n, "/api/monitors/"+m.ID+"/history", "GET", nil, adminToken)
	var h []Observation
	if code != 200 || json.Unmarshal(b, &h) != nil || len(h) == 0 || h[0].State != "down" {
		t.Fatal(code, string(b))
	}
}
