package canary

import (
	"context"
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestUnavailableICMPDoesNotRecoverOrReplaceOutage(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 0))
	state := "down"
	r := webhook(t)
	o := Options{NodeID: "a", DBPath: filepath.Join(t.TempDir(), "unknown.db"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: clock.Load, NotificationDelayMs: Ptr(int64(0)), Peers: []Peer{{ID: "b", URL: "http://127.0.0.1:1"}}, Probe: func(context.Context, Monitor) ProbeResult {
		return ProbeResult{State: state, LatencyMs: 20, LatencyAvailable: Ptr(true), Message: "fixture"}
	}}
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	in := input([]string{"a"}, "127.0.0.1", r.URL)
	in.Type, in.Ping = "ping", &PingMonitorOptions{Count: 2, Size: Ptr(0), Family: "ipv4"}
	if err := validateMonitor(&in, n.known); err != nil {
		t.Fatal(err)
	}
	m, err := n.writeMonitor(in, nil, "icmp-unavailable", false)
	if err != nil {
		t.Fatal(err)
	}
	tick := func() { clock.Add(100); partitionDrive(t, n) }
	tick()
	if r.count("down") != 1 {
		t.Fatal("missing initial outage")
	}
	run := *n.samples[m.ID+"/a"].DownSince
	state = "unknown"
	tick()
	sample := n.samples[m.ID+"/a"]
	view := n.monitorViews("viewer")[0]
	if sample.State != "unknown" || sample.RawState != "unknown" || sample.LatencyAvailable == nil || *sample.LatencyAvailable || sample.LatencyMs != 0 || view.State != "unknown" || view.UnknownNodes != 1 || r.count("recovery") != 0 {
		t.Fatal("unavailable probe invented known status or RTT", sample, view)
	}
	peerOptions := o
	peerOptions.NodeID, peerOptions.DBPath, peerOptions.Peers = "b", ":memory:", []Peer{{ID: "a", URL: "http://127.0.0.1:1"}}
	peer, err := New(peerOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Stop()
	for _, page := range causalCapacityPages(t, n) {
		revisionJoinsApply(t, peer, page)
	}
	if got := peer.samples[m.ID+"/a"]; got.State != "unknown" || got.LatencyAvailable == nil || *got.LatencyAvailable {
		t.Fatal("unknown measurement did not replicate", got)
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n, err = New(o)
	if err != nil {
		t.Fatal(err)
	}
	state = "down"
	tick()
	if got := n.samples[m.ID+"/a"]; got.DownSince == nil || *got.DownSince != run || r.count("down") != 1 {
		t.Fatal("unknown/restart split the ongoing outage", got)
	}
	state = "up"
	tick()
	if r.count("recovery") != 1 {
		t.Fatal("true recovery missing")
	}
	stats, err := n.historyStatistics(m.ID, url.Values{})
	if err != nil || stats.Count != 4 || stats.Unknown != 1 || stats.LatencyCount != 3 || stats.AverageLatencyMs == nil || *stats.AverageLatencyMs != 20 || stats.P50LatencyMs == nil || *stats.P50LatencyMs != 20 {
		t.Fatal("missing RTT biased history statistics", stats, err)
	}
}

func TestPingOptionsValidateAndPreserveExplicitZeroPayload(t *testing.T) {
	in := input([]string{"a"}, "127.0.0.1", "")
	in.Type = "ping"
	b, _ := json.Marshal(in)
	base := strings.TrimSuffix(string(b), "}")
	for _, raw := range []string{`null`, `{"count":11}`, `{"count":-1}`, `{"count":1.5}`, `{"size":null}`, `{"size":65501}`, `{"family":"ipv6"}`, `{"family":"IPV4"}`, `{"Count":2}`, `{"count":1,"count":2}`, `{"command":"ping"}`} {
		if _, err := parseMonitor([]byte(base+`,"ping":`+raw+`}`), map[string]bool{"a": true}); err == nil {
			t.Fatal("invalid ping options accepted", raw)
		}
	}
	got, err := parseMonitor([]byte(base+`,"ping":{"count":3,"size":0,"family":"ipv4"}}`), map[string]bool{"a": true})
	if err != nil || got.Ping.Size == nil || *got.Ping.Size != 0 || got.Ping.Count != 3 {
		t.Fatal("explicit zero ping payload lost", got.Ping, err)
	}
	other := got
	other.Ping = &PingMonitorOptions{Count: 2, Size: Ptr(0), Family: "ipv4"}
	if sameCheck(got, other) {
		t.Fatal("packet-count edit kept old checking generation")
	}
}
