package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNamedGroupsReplicateConflictsResolveAndTombstones(t *testing.T) {
	c := cluster(t, []string{"a", "b"}, nil)
	a, b := c.nodes[0], c.nodes[1]
	g := addGroup(t, a, GroupInput{Name: "Arbitrary user domain", NodeIDs: []string{"a", "b"}, GraceMs: 10000})
	eventually(t, func() bool {
		v := getOverview(t, b)
		return len(v.Groups) == 1 && v.Groups[0].Group.Revision == g.Revision
	})
	in := input([]string{"a"}, "http://127.0.0.1:1", "")
	in.Enabled = false
	in.GroupID = g.ID
	m := add(t, a, in)
	eventually(t, func() bool { return len(getOverview(t, b).Monitors) == 1 })
	c.split([]string{"a"}, []string{"b"})
	left, right := g, g
	left.Name = "Operator left"
	right.Name = "Operator right"
	for i, v := range []Group{left, right} {
		code, body := api(t, c.nodes[i], "/api/groups/"+g.ID, "PUT", v, adminToken)
		if code != 200 {
			t.Fatalf("%d %s", code, body)
		}
	}
	c.heal()
	eventually(t, func() bool {
		for _, n := range c.nodes {
			v := getOverview(t, n)
			if len(v.Groups) != 1 || !v.Groups[0].ConfigConflict {
				return false
			}
		}
		return true
	})
	code, body := api(t, a, "/api/groups/"+g.ID+"/conflicts", "GET", nil, adminToken)
	var heads []Group
	if code != 200 || json.Unmarshal(body, &heads) != nil || len(heads) != 2 {
		t.Fatal(code, string(body))
	}
	code, _ = api(t, a, "/api/groups/"+g.ID, "PUT", heads[0], adminToken)
	if code != 409 {
		t.Fatal(code)
	}
	resolution := map[string]any{"name": "Reconciled name", "nodeIds": g.NodeIDs, "graceMs": int64(0), "revisions": []string{heads[0].Revision}}
	code, _ = api(t, a, "/api/groups/"+g.ID+"/resolve", "POST", resolution, adminToken)
	if code != 409 {
		t.Fatal(code)
	}
	resolution["revisions"] = []string{heads[0].Revision, heads[1].Revision}
	code, body = api(t, a, "/api/groups/"+g.ID+"/resolve", "POST", resolution, adminToken)
	var resolved Group
	if code != 200 || json.Unmarshal(body, &resolved) != nil {
		t.Fatal(code, string(body))
	}
	eventually(t, func() bool { v := getOverview(t, b); return v.Groups[0].Group.Revision == resolved.Revision })
	code, _ = api(t, b, "/api/groups/"+g.ID+"/resolve", "POST", resolution, adminToken)
	if code != 409 {
		t.Fatal(code)
	}
	code, _ = api(t, a, "/api/groups/"+g.ID, "DELETE", map[string]string{"revision": resolved.Revision}, adminToken)
	if code != 204 {
		t.Fatal(code)
	}
	eventually(t, func() bool {
		for _, n := range c.nodes {
			v := getOverview(t, n)
			if len(v.Groups) != 0 || len(v.Monitors) != 1 || v.Monitors[0].Monitor.ID != m.ID {
				return false
			}
		}
		return true
	})
	code, body = api(t, b, "/api/groups/"+g.ID+"/conflicts", "GET", nil, adminToken)
	if code != 200 || !strings.Contains(string(body), `"deleted":true`) {
		t.Fatal(code, string(body))
	}
}

func TestDependencyCyclesAndReferences(t *testing.T) {
	n := local(t, nil)
	in := input([]string{"a"}, "http://127.0.0.1:1", "")
	in.Enabled = false
	p := add(t, n, in)
	g := addGroup(t, n, GroupInput{Name: "G", NodeIDs: []string{"a"}, OutageMonitorID: p.ID, GraceMs: 0})
	p.GroupID = g.ID
	code, b := api(t, n, "/api/monitors/"+p.ID, "PUT", p, adminToken)
	if code != 200 {
		t.Fatalf("Dependency must be exempt from its own group: %d %s", code, b)
	}
	_ = json.Unmarshal(b, &p)
	in.GroupID = g.ID
	child := add(t, n, in)
	h := addGroup(t, n, GroupInput{Name: "H", NodeIDs: []string{"a"}, OutageMonitorID: child.ID, GraceMs: 10})
	p.GroupID = h.ID
	code, b = api(t, n, "/api/monitors/"+p.ID, "PUT", p, adminToken)
	if code != 400 || !strings.Contains(string(b), "cycle") {
		t.Fatal(code, string(b))
	}
	bad := in
	bad.GroupID = "missing"
	code, _ = api(t, n, "/api/monitors", "POST", bad, adminToken)
	if code != 400 {
		t.Fatal(code)
	}
	code, _ = api(t, n, "/api/groups", "POST", GroupInput{Name: "Bad ref", NodeIDs: []string{"a"}, OutageMonitorID: "missing", GraceMs: 0}, adminToken)
	if code != 400 {
		t.Fatal(code)
	}
	for _, ms := range []int64{-1, 120001} {
		code, _ = api(t, n, "/api/groups", "POST", GroupInput{Name: "Bad range", NodeIDs: []string{"a"}, GraceMs: ms}, adminToken)
		if code != 400 {
			t.Fatal(code)
		}
	}
}

func TestRootOutageSuppressesChildrenButNotIndependentGroup(t *testing.T) {
	var parent, c1, c2, unrelated atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		down := false
		switch r.URL.Path {
		case "/p":
			down = parent.Load()
		case "/c1":
			down = c1.Load()
		case "/c2":
			down = c2.Load()
		case "/u":
			down = unrelated.Load()
		}
		if down {
			w.WriteHeader(503)
		} else {
			w.WriteHeader(200)
		}
	}))
	defer target.Close()
	r := webhook(t)
	n := local(t, func(o *Options) { o.NotificationDelayMs = Ptr(int64(20)) })
	pi := input([]string{"a"}, target.URL+"/p", r.URL)
	pi.Name = "P"
	p := add(t, n, pi)
	g := addGroup(t, n, GroupInput{Name: "G", NodeIDs: []string{"a"}, OutageMonitorID: p.ID, GraceMs: 220})
	p.GroupID = g.ID
	code, b := api(t, n, "/api/monitors/"+p.ID, "PUT", p, adminToken)
	if code != 200 {
		t.Fatal(code, string(b))
	}
	var monitors []Monitor
	for _, name := range []string{"c1", "c2"} {
		in := input([]string{"a"}, target.URL+"/"+name, r.URL)
		in.Name = name
		in.GroupID = g.ID
		monitors = append(monitors, add(t, n, in))
	}
	independent := addGroup(t, n, GroupInput{Name: "Independent custom domain", NodeIDs: []string{"a"}, GraceMs: 0})
	ui := input([]string{"a"}, target.URL+"/u", r.URL)
	ui.Name = "U"
	ui.GroupID = independent.ID
	u := add(t, n, ui)
	eventually(t, func() bool {
		v := getOverview(t, n)
		if len(v.Monitors) != 4 {
			return false
		}
		for _, m := range v.Monitors {
			if m.State != "up" {
				return false
			}
		}
		return true
	})
	parent.Store(true)
	c1.Store(true)
	c2.Store(true)
	unrelated.Store(true)
	eventually(t, func() bool { return r.count("down") == 2 })
	time.Sleep(400 * time.Millisecond)
	ds := r.all()
	if len(ds) != 2 {
		t.Fatal(ds)
	}
	for _, d := range ds {
		if d.MonitorID != p.ID && d.MonitorID != u.ID {
			t.Fatal("Dependent notification escaped", ds)
		}
	}
	v := getOverview(t, n)
	for _, view := range v.Groups {
		if view.Group.ID == g.ID && (view.State != "down" || !strings.Contains(view.Reason, "dependency is down") || strings.Contains(strings.ToLower(view.Reason), "connectivity")) {
			t.Fatal("Reachable HTTP 503 dependency was assigned a connectivity diagnosis", view)
		}
	}
	for _, m := range v.Monitors {
		if slices.Contains([]string{monitors[0].ID, monitors[1].ID}, m.Monitor.ID) && m.SuppressedByGroupID != g.ID {
			t.Fatal("Missing suppression evidence", m)
		}
	}
	for _, m := range monitors {
		code, b := api(t, n, "/api/monitors/"+m.ID+"/history", "GET", nil, adminToken)
		var hist []Observation
		if code != 200 || json.Unmarshal(b, &hist) != nil || len(hist) < 3 || hist[0].State != "down" {
			t.Fatal("Suppression altered local history", code, string(b))
		}
	}
	c1.Store(false)
	eventually(t, func() bool {
		for _, m := range getOverview(t, n).Monitors {
			if m.Monitor.ID == monitors[0].ID {
				return m.State == "up"
			}
		}
		return false
	})
	parent.Store(false)
	eventually(t, func() bool {
		for _, d := range r.all() {
			if d.MonitorID == monitors[1].ID && d.Kind == "down" {
				return true
			}
		}
		return false
	})
	time.Sleep(300 * time.Millisecond)
	for _, d := range r.all() {
		if d.MonitorID == monitors[0].ID {
			t.Fatal("Recovered suppressed child replayed", r.all())
		}
	}
	c2.Store(false)
	eventually(t, func() bool {
		for _, d := range r.all() {
			if d.MonitorID == monitors[1].ID && d.Kind == "recovery" {
				return true
			}
		}
		return false
	})
	if getOverview(t, n).Groups[0].Reason == "power failed" {
		t.Fatal("Fabricated root cause")
	}
}

func TestStaleDependencyFailsOpenAfterGrace(t *testing.T) {
	now, nowFn := clock()
	r := webhook(t)
	n := local(t, func(o *Options) {
		o.Now = nowFn
		o.Peers = []Peer{{ID: "b", URL: "http://127.0.0.1:1"}}
		o.NotificationDelayMs = Ptr(int64(0))
		o.Probe = func(ctx context.Context, m Monitor) ProbeResult {
			return ProbeResult{State: "down", LatencyMs: 1, Message: "Controlled"}
		}
	})
	pi := input([]string{"b"}, "http://127.0.0.1:1", r.URL)
	pi.Name = "P"
	p := add(t, n, pi)
	n.mu.Lock()
	sample := Sample{Observation: Observation{MonitorID: p.ID, NodeID: "b", State: "down", CheckedAt: now.Load(), LatencyMs: 1, Message: "Remote down"}, Sequence: 1, Revision: p.Revision, DownSince: Ptr(int64(1))}
	n.samples[p.ID+"/b"] = sample
	n.evaluate()
	// The parent really represents the outage only with current-episode
	// successful delivery evidence. Its observation will subsequently go stale.
	for _, i := range n.episodes {
		if i.MonitorID == p.ID {
			r := Receipt{NotificationView{ID: "parent-delivery", MonitorID: p.ID, IncidentID: i.ID, Kind: "down", NodeID: "b", SentAt: now.Load()}, 0}
			if !n.dbPut("receipts", r.ID, r) {
				t.Fatal("Persist parent receipt")
			}
			n.receipts[r.ID] = r
		}
	}
	n.mu.Unlock()
	g := addGroup(t, n, GroupInput{Name: "G", NodeIDs: []string{"a"}, OutageMonitorID: p.ID, GraceMs: 200})
	ci := input([]string{"a"}, "http://127.0.0.1:1", r.URL)
	ci.Name = "C"
	ci.GroupID = g.ID
	child := add(t, n, ci)
	eventually(t, func() bool {
		v := getOverview(t, n)
		return r.count("down") == 0 && len(v.Incidents) == 2 && v.Groups[0].SuppressionActive
	})
	now.Add(500)
	eventually(t, func() bool { v := getOverview(t, n); return v.Groups[0].State == "unknown" })
	eventually(t, func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()
		for _, i := range n.episodes {
			if i.MonitorID == child.ID && i.SuppressionReleasedAt != nil {
				return true
			}
		}
		return false
	})
	now.Add(201)
	eventually(t, func() bool {
		for _, d := range r.all() {
			if d.MonitorID == child.ID {
				return true
			}
		}
		return false
	})
}

func TestDependencyDeliveryFailureReleasesChildrenAndViewsAtGrace(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(map[bool]string{true: "missing destination", false: "HTTP 503 destination"}[missing], func(t *testing.T) {
			now, nowFn := clock()
			parentHook, childHook := webhook(t), webhook(t)
			parentHook.status.Store(503)
			n := local(t, func(o *Options) {
				o.Now = nowFn
				o.NotificationDelayMs = Ptr(int64(0))
				o.Probe = func(context.Context, Monitor) ProbeResult {
					return ProbeResult{State: "down", LatencyMs: 1, Message: "HTTP 503"}
				}
			})
			pi := input([]string{"a"}, "http://127.0.0.1:1", parentHook.URL)
			if missing {
				pi.WebhookURL = ""
			}
			pi.Name = "User dependency"
			p := add(t, n, pi)
			g := addGroup(t, n, GroupInput{Name: "A custom group", NodeIDs: []string{"a"}, OutageMonitorID: p.ID, GraceMs: 1000})
			ci := input([]string{"a"}, "http://127.0.0.1:1", childHook.URL)
			ci.GroupID = g.ID
			ci.RepeatAfterMs = Ptr(int64(1000))
			child := add(t, n, ci)
			eventually(t, func() bool {
				v := getOverview(t, n)
				return len(v.Incidents) == 2 && v.Groups[0].SuppressionActive
			})
			if !missing {
				eventually(t, func() bool { return parentHook.count("down") == 1 })
			}
			now.Add(999)
			time.Sleep(45 * time.Millisecond)
			if len(childHook.all()) != 0 {
				t.Fatal("Child bypassed initial settling", childHook.all())
			}
			now.Add(1)
			eventually(t, func() bool { return childHook.count("down") == 1 })
			v := getOverview(t, n)
			if v.Groups[0].SuppressionActive || v.Groups[0].State != "down" || !strings.Contains(v.Groups[0].Reason, "delivery") && !strings.Contains(v.Groups[0].Reason, "destination") {
				t.Fatal("Group view disagrees with actual fail-open eligibility", v.Groups)
			}
			for _, m := range v.Monitors {
				if m.Monitor.ID == child.ID && m.SuppressedByGroupID != "" {
					t.Fatal("Delivered child still displayed as suppressed", m)
				}
			}
			now.Add(1000)
			eventually(t, func() bool { return childHook.count("repeat") == 1 })
		})
	}
}

func TestDeliveredDependencySuppressesRepeatsOnlyForItsCurrentEpisode(t *testing.T) {
	now, nowFn := clock()
	parentHook, childHook := webhook(t), webhook(t)
	var parentDown atomic.Bool
	n := local(t, func(o *Options) {
		o.Now = nowFn
		o.NotificationDelayMs = Ptr(int64(0))
		o.Probe = func(_ context.Context, m Monitor) ProbeResult {
			if m.Name == "P" && !parentDown.Load() {
				return ProbeResult{State: "up", LatencyMs: 1, Message: "HTTP 200"}
			}
			return ProbeResult{State: "down", LatencyMs: 1, Message: "HTTP 503"}
		}
	})
	pi := input([]string{"a"}, "http://127.0.0.1:1", parentHook.URL)
	pi.Name = "P"
	p := add(t, n, pi)
	g := addGroup(t, n, GroupInput{Name: "Custom dependency", NodeIDs: []string{"a"}, OutageMonitorID: p.ID, GraceMs: 200})
	ci := input([]string{"a"}, "http://127.0.0.1:1", childHook.URL)
	ci.GroupID = g.ID
	ci.RepeatAfterMs = Ptr(int64(1000))
	add(t, n, ci)
	eventually(t, func() bool { return len(getOverview(t, n).Incidents) == 1 })
	now.Add(200)
	eventually(t, func() bool { return childHook.count("down") == 1 })
	parentDown.Store(true)
	now.Add(100)
	eventually(t, func() bool { return parentHook.count("down") == 1 && getOverview(t, n).Groups[0].SuppressionActive })
	// Model elapsed time with completed probes. A single 1.1s jump makes this
	// deliberately short-interval dependency stale, which correctly fails open
	// and tests a different policy from fresh, delivered-parent suppression.
	for range 11 {
		now.Add(100)
		eventually(t, func() bool {
			for _, m := range getOverview(t, n).Monitors {
				for _, o := range m.Observations {
					if o.CheckedAt < now.Load() {
						return false
					}
				}
			}
			return true
		})
	}
	time.Sleep(60 * time.Millisecond)
	if childHook.count("repeat") != 0 {
		t.Fatal("Delivered parent failed to suppress previously-notified child's repeat")
	}
	v := getOverview(t, n).Groups[0]
	if strings.Contains(strings.ToLower(v.Reason), "connectivity") || !strings.Contains(v.Reason, "dependency is down") {
		t.Fatal("HTTP failure was assigned an invented connectivity diagnosis", v)
	}
	parentDown.Store(false)
	now.Add(100)
	eventually(t, func() bool { return parentHook.count("recovery") == 1 && childHook.count("repeat") == 1 })
	parentHook.status.Store(503)
	parentDown.Store(true)
	now.Add(100)
	eventually(t, func() bool { return parentHook.count("down") == 2 })
	now.Add(1000)
	eventually(t, func() bool { return childHook.count("repeat") == 2 })
	if getOverview(t, n).Groups[0].SuppressionActive {
		t.Fatal("Successful receipt from an earlier outage silenced a later undelivered one")
	}
}

func TestChildRecoveredDuringFailedParentGraceNeverReplays(t *testing.T) {
	now, nowFn := clock()
	parentHook, childHook := webhook(t), webhook(t)
	parentHook.status.Store(503)
	var childDown atomic.Bool
	childDown.Store(true)
	n := local(t, func(o *Options) {
		o.Now = nowFn
		o.NotificationDelayMs = Ptr(int64(0))
		o.Probe = func(_ context.Context, m Monitor) ProbeResult {
			if m.Name == "P" || childDown.Load() {
				return ProbeResult{State: "down", LatencyMs: 1, Message: "HTTP 503"}
			}
			return ProbeResult{State: "up", LatencyMs: 1, Message: "HTTP 200"}
		}
	})
	pi := input([]string{"a"}, "http://127.0.0.1:1", parentHook.URL)
	pi.Name = "P"
	p := add(t, n, pi)
	g := addGroup(t, n, GroupInput{Name: "Custom group", NodeIDs: []string{"a"}, OutageMonitorID: p.ID, GraceMs: 1000})
	ci := input([]string{"a"}, "http://127.0.0.1:1", childHook.URL)
	ci.GroupID = g.ID
	c := add(t, n, ci)
	eventually(t, func() bool { return len(getOverview(t, n).Incidents) == 2 })
	childDown.Store(false)
	now.Add(200)
	eventually(t, func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()
		for _, i := range n.episodes {
			if i.MonitorID == c.ID && i.Closed != nil && i.SuppressedComplete {
				return true
			}
		}
		return false
	})
	now.Add(2000)
	eventually(t, func() bool { return !getOverview(t, n).Groups[0].SuppressionActive })
	if len(childHook.all()) != 0 {
		t.Fatal("Child recovered while settling replayed an obsolete outage", childHook.all())
	}
	childDown.Store(true)
	now.Add(100)
	eventually(t, func() bool { return len(getOverview(t, n).Incidents) == 3 })
	now.Add(1000)
	eventually(t, func() bool { return childHook.count("down") == 1 })
	if childHook.count("recovery") != 0 {
		t.Fatal("Earlier suppressed episode leaked a stale recovery", childHook.all())
	}
}

func TestNotifiedChildRecoveryRemainsValidDuringParentOutage(t *testing.T) {
	var parent, child atomic.Bool
	child.Store(true)
	r := webhook(t)
	n := local(t, func(o *Options) {
		o.NotificationDelayMs = Ptr(int64(0))
		o.Probe = func(_ context.Context, m Monitor) ProbeResult {
			down := child.Load()
			if m.Name == "P" {
				down = parent.Load()
			}
			if down {
				return ProbeResult{State: "down", LatencyMs: 1, Message: "Controlled down"}
			}
			return ProbeResult{State: "up", LatencyMs: 1, Message: "Controlled up"}
		}
	})
	pi := input([]string{"a"}, "http://127.0.0.1:1", r.URL)
	pi.Name = "P"
	p := add(t, n, pi)
	g := addGroup(t, n, GroupInput{Name: "G", NodeIDs: []string{"a"}, OutageMonitorID: p.ID, GraceMs: 0})
	ci := input([]string{"a"}, "http://127.0.0.1:1", r.URL)
	ci.Name = "C"
	ci.GroupID = g.ID
	ci.RepeatAfterMs = Ptr(int64(1000))
	c := add(t, n, ci)
	eventually(t, func() bool { return r.count("down") == 1 })
	parent.Store(true)
	eventually(t, func() bool { return r.count("down") == 2 })
	child.Store(false)
	eventually(t, func() bool {
		for _, d := range r.all() {
			if d.MonitorID == c.ID && d.Kind == "recovery" {
				return true
			}
		}
		return false
	})
	time.Sleep(150 * time.Millisecond)
	if r.count("repeat") != 0 {
		t.Fatal(r.all())
	}
}

func TestDelayedReplicatedDependencyFailsOpenAfterGrace(t *testing.T) {
	now, nowFn := clock()
	r := webhook(t)
	n := local(t, func(o *Options) {
		o.Now = nowFn
		o.NotificationDelayMs = Ptr(int64(0))
		o.Probe = func(context.Context, Monitor) ProbeResult {
			return ProbeResult{State: "down", LatencyMs: 1, Message: "Controlled down"}
		}
	})
	g := Group{GroupInput: GroupInput{Name: "Delayed arbitrary domain", NodeIDs: []string{"a"}, OutageMonitorID: "not-arrived", GraceMs: 200}, ID: "replicated-group", Revision: "group-rev", UpdatedAt: now.Load()}
	n.mu.Lock()
	err := n.mergeSnapshot(Snapshot{GroupVersions: []GroupVersion{{Group: g, Ancestors: []string{}}}})
	n.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	in := input([]string{"a"}, "http://127.0.0.1:1", r.URL)
	in.GroupID = g.ID
	add(t, n, in)
	eventually(t, func() bool {
		v := getOverview(t, n)
		return len(v.Incidents) == 1 && len(v.Groups) == 1 && v.Groups[0].State == "unknown"
	})
	now.Add(199)
	time.Sleep(50 * time.Millisecond)
	if len(r.all()) != 0 {
		t.Fatal("Unknown parent bypassed grace", r.all())
	}
	now.Add(1)
	eventually(t, func() bool { return r.count("down") == 1 })
	if getOverview(t, n).Monitors[0].SuppressedByGroupID != "" {
		t.Fatal("Unknown parent suppressed child")
	}
}

func TestReplicatedCycleIsSafeAndVisible(t *testing.T) {
	n := local(t, nil)
	in := input([]string{"a"}, "http://127.0.0.1:1", "")
	in.Enabled = false
	p := add(t, n, in)
	g := addGroup(t, n, GroupInput{Name: "G", NodeIDs: []string{"a"}, OutageMonitorID: p.ID, GraceMs: 0})
	in.GroupID = g.ID
	c := add(t, n, in)
	h := addGroup(t, n, GroupInput{Name: "H", NodeIDs: []string{"a"}, OutageMonitorID: c.ID, GraceMs: 0})
	// Simulate the other side of an independently valid partition edit. Local
	// management rejects this cycle; delayed replicated configuration is retained.
	p.GroupID = h.ID
	oldRevision := p.Revision
	p.Revision = "remote-revision"
	n.mu.Lock()
	err := n.mergeSnapshot(Snapshot{Versions: []Version{{Monitor: p, Ancestors: []string{oldRevision}}}})
	cycle := n.dependencyCycle(nil, nil, p.ID)
	active, _, _ := n.suppression(c)
	n.mu.Unlock()
	if err != nil || !cycle || active != "" {
		t.Fatal("Replicated cycle was unsafe", err, cycle, active)
	}
	if len(getOverview(t, n).Monitors) != 2 {
		t.Fatal("Cycle hid monitors")
	}
	independent := input([]string{"a"}, "http://127.0.0.1:1", "")
	independent.Enabled = false
	add(t, n, independent)
	if len(getOverview(t, n).Monitors) != 3 {
		t.Fatal("Unrelated management blocked by a replicated cycle")
	}
}
