package canary

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func editMonitor(t *testing.T, n *Node, m Monitor) Monitor {
	t.Helper()
	code, body := api(t, n, "/api/monitors/"+m.ID, "PUT", m, adminToken)
	var edited Monitor
	if code != 200 || json.Unmarshal(body, &edited) != nil {
		t.Fatal(code, string(body))
	}
	return edited
}

func captureLifecyclePage(t *testing.T, n *Node) []byte {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	page, err := n.snapshotPage("", "")
	if err != nil || page.Next != "" {
		t.Fatal("Expected a complete small fixture page", err)
	}
	body, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func postLifecyclePage(t *testing.T, n *Node, sender string, body []byte) {
	t.Helper()
	req, err := http.NewRequest("POST", n.URL()+"/internal/snapshot", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+clusterToken)
	req.Header.Set("X-Canary-Node-Id", sender)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode, string(out))
	}
}

func currentLifecycleState(t *testing.T, c *testCluster, m Monitor, state string) bool {
	t.Helper()
	for _, n := range c.nodes {
		v := getOverview(t, n)
		if len(v.Monitors) != 1 || v.Monitors[0].Monitor.Revision != m.Revision || v.Monitors[0].UnknownNodes != 0 || v.Monitors[0].State != state {
			return false
		}
		for _, o := range v.Monitors[0].Observations {
			if o.State != state {
				return false
			}
		}
	}
	return true
}

func restartLifecyclePeer(t *testing.T, c *testCluster, index int, db string) {
	t.Helper()
	old := c.nodes[index]
	if err := old.Stop(); err != nil {
		t.Fatal(err)
	}
	n, err := New(Options{NodeID: old.ID, DBPath: db, Port: Ptr(0), ClusterToken: clusterToken, PeerClient: old.peerClient, TickInterval: 15 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	if err = n.Start(); err != nil {
		t.Fatal(err)
	}
	c.nodes[index] = n
	c.mu.Lock()
	c.urls[n.ID] = n.URL()
	c.mu.Unlock()
}

func TestMonitorMetadataEditsPreserveDeliveredOutageAcrossPeersAndRestart(t *testing.T) {
	var status atomic.Int64
	status.Store(200)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(int(status.Load())) }))
	defer target.Close()
	initialHook, editedHook := webhook(t), webhook(t)
	dir := t.TempDir()
	c := cluster(t, []string{"a", "b"}, func(o *Options) { o.DBPath = filepath.Join(dir, o.NodeID+".db") })
	a, b := c.nodes[0], c.nodes[1]
	g1 := addGroup(t, a, GroupInput{Name: "First", NodeIDs: []string{"a", "b"}})
	g2 := addGroup(t, a, GroupInput{Name: "Second", NodeIDs: []string{"a", "b"}})
	in := input([]string{"a", "b"}, target.URL, initialHook.URL)
	in.GroupID = g1.ID
	m := add(t, a, in)
	originalRevision := m.Revision
	eventually(t, func() bool { return currentLifecycleState(t, c, m, "up") })
	status.Store(503)
	eventually(t, func() bool {
		return initialHook.count("down") == 1 && currentLifecycleState(t, c, m, "down") && len(getOverview(t, b).Notifications) == 1
	})
	identity := getOverview(t, a).Incidents[0]
	oldState := captureLifecyclePage(t, b)

	// Deliver an actual authenticated old config/incident/sample page after a
	// group-only edit, while the other peer still knows only the old revision.
	c.split([]string{"a"}, []string{"b"})
	m.GroupID = g2.ID
	m = editMonitor(t, a, m)
	if checkGeneration(m) != originalRevision {
		t.Fatal("Group-only edit started a different check generation")
	}
	postLifecyclePage(t, a, b.ID, oldState)
	eventually(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return len(a.fresh(m)) == 1 && a.fresh(m)[0].Revision == m.Revision
	})
	if getOverview(t, a).Monitors[0].UnknownNodes != 1 {
		t.Fatal("Old-revision peer observation was treated as current")
	}
	c.heal()
	eventually(t, func() bool { return currentLifecycleState(t, c, m, "down") })

	m.Name, m.IntervalMs, m.RepeatAfterMs, m.WebhookURL = "Renamed while down", 140, Ptr(int64(60000)), editedHook.URL
	m = editMonitor(t, a, m)
	eventually(t, func() bool { return currentLifecycleState(t, c, m, "down") })
	postLifecyclePage(t, a, b.ID, oldState)
	postLifecyclePage(t, b, a.ID, captureLifecyclePage(t, a))
	for _, n := range c.nodes {
		v := getOverview(t, n)
		if len(v.Incidents) != 1 || v.Incidents[0].ID != identity.ID || v.Incidents[0].OpenedAt != identity.OpenedAt || v.Incidents[0].ResolvedAt != nil || v.Incidents[0].NotifiedAt == nil || *v.Incidents[0].NotifiedAt != *identity.NotifiedAt {
			t.Fatal("Edit lost incident identity or delivery evidence", v.Incidents)
		}
	}

	// Reload each SQLite store (including the editing node) without bootstrap
	// credentials, preserving actual HTTP peer routing and persisted settings.
	restartLifecyclePeer(t, c, 1, filepath.Join(dir, "b.db"))
	restartLifecyclePeer(t, c, 0, filepath.Join(dir, "a.db"))
	a, b = c.nodes[0], c.nodes[1]
	eventually(t, func() bool { return currentLifecycleState(t, c, m, "down") })
	time.Sleep(650 * time.Millisecond)
	if initialHook.count("") != 1 || editedHook.count("") != 0 {
		t.Fatal("An edit/restart replayed the initial outage", initialHook.all(), editedHook.all())
	}

	status.Store(200)
	eventually(t, func() bool { return editedHook.count("recovery") == 1 && currentLifecycleState(t, c, m, "up") })
	for _, n := range c.nodes {
		eventually(t, func() bool {
			v := getOverview(t, n)
			return len(v.Incidents) == 1 && v.Incidents[0].ID == identity.ID && v.Incidents[0].ResolvedAt != nil && v.Incidents[0].RecoveryNotifiedAt != nil
		})
	}
	if editedHook.all()[0].IncidentID != initialHook.all()[0].IncidentID {
		t.Fatal("Recovery changed the successfully delivered incident identity")
	}
	status.Store(503)
	eventually(t, func() bool { return editedHook.count("down") == 1 && currentLifecycleState(t, c, m, "down") })

	// An old open sibling, learned after both edit and recovery, belongs to the
	// earlier closed episode and cannot close or absorb the later real outage.
	var delayed Snapshot
	if err := json.Unmarshal(oldState, &delayed); err != nil {
		t.Fatal(err)
	}
	delayed.Incidents[0].ID, delayed.Incidents[0].Aliases = "late-before-edit", []string{"late-before-edit"}
	delayed.Incidents[0].EvidenceRoot = "" // synthetic identity has self-contained run evidence
	late, _ := json.Marshal(delayed)
	postLifecyclePage(t, a, b.ID, late)
	time.Sleep(650 * time.Millisecond)
	if initialHook.count("") != 1 || editedHook.count("") != 2 || editedHook.all()[1].IncidentID == initialHook.all()[0].IncidentID {
		t.Fatal("A later outage was merged or replayed", editedHook.all())
	}
	for _, n := range c.nodes {
		eventually(t, func() bool {
			v := getOverview(t, n)
			if len(v.Incidents) != 2 {
				return false
			}
			open := 0
			for _, i := range v.Incidents {
				if i.ResolvedAt == nil {
					open++
					if i.ID == identity.ID || i.NotifiedAt == nil {
						return false
					}
				}
			}
			return open == 1
		})
	}
}

func TestMonitorSemanticEditsRetireWithoutRecoveryAndRejectLateOldState(t *testing.T) {
	for _, change := range []string{"target", "type", "expectedStatusCodes", "timeout", "placement", "pause", "delete"} {
		t.Run(change, func(t *testing.T) {
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
			defer target.Close()
			r := webhook(t)
			c := cluster(t, []string{"a", "b"}, nil)
			a, b := c.nodes[0], c.nodes[1]
			m := add(t, a, input([]string{"a", "b"}, target.URL, r.URL))
			eventually(t, func() bool {
				return r.count("down") == 1 && len(getOverview(t, b).Notifications) == 1 && currentLifecycleState(t, c, m, "down")
			})
			oldGeneration, oldID := checkGeneration(m), getOverview(t, a).Incidents[0].ID
			old := captureLifecyclePage(t, b)
			switch change {
			case "target":
				m.Target += "/new-check"
			case "type":
				m.Type, m.Target = "ping", "127.0.0.1"
			case "expectedStatusCodes":
				m.ExpectedStatusCodes = []int{503}
			case "timeout":
				m.TimeoutMs = 50
			case "placement":
				m.NodeIDs = []string{"a"}
			case "pause":
				m.Enabled = false
			}
			if change == "delete" {
				code, body := api(t, a, "/api/monitors/"+m.ID, "DELETE", map[string]string{"revision": m.Revision}, adminToken)
				if code != 204 {
					t.Fatal(code, string(body))
				}
			} else {
				m = editMonitor(t, a, m)
				if checkGeneration(m) == oldGeneration {
					t.Fatal("Semantic edit retained the old check generation")
				}
			}
			checkRetired := func(n *Node) bool {
				v := getOverview(t, n)
				for _, i := range v.Incidents {
					if i.ID == oldID {
						return i.ResolvedAt != nil && i.RetiredBy != "" && i.RetirementReason != "" && i.RecoveryNotifiedAt == nil
					}
				}
				return false
			}
			eventually(t, func() bool { return checkRetired(a) && checkRetired(b) })
			postLifecyclePage(t, a, b.ID, old)
			time.Sleep(350 * time.Millisecond)
			if !checkRetired(a) || !checkRetired(b) || r.count("recovery") != 0 {
				t.Fatal("Retirement was undone or an edit sent fake recovery", r.all())
			}
			if slices.Contains([]string{"target", "timeout", "placement"}, change) {
				eventually(t, func() bool { return r.count("down") == 2 })
				if r.all()[1].IncidentID == r.all()[0].IncidentID {
					t.Fatal("A different check inherited the retired incident")
				}
			} else if change == "pause" {
				m.Enabled = true
				m = editMonitor(t, a, m)
				eventually(t, func() bool { return r.count("down") == 2 })
				if r.all()[1].IncidentID == oldID {
					t.Fatal("Resume resurrected a retired incident")
				}
			}
		})
	}
}

func TestMonitorConflictResolutionPreservesOnlySharedCheckGeneration(t *testing.T) {
	for _, semantic := range []bool{false, true} {
		name := "metadata"
		if semantic {
			name = "semantic"
		}
		t.Run(name, func(t *testing.T) {
			var status atomic.Int64
			status.Store(503)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(int(status.Load())) }))
			defer target.Close()
			r := webhook(t)
			c := cluster(t, []string{"a", "b"}, nil)
			a, b := c.nodes[0], c.nodes[1]
			m := add(t, a, input([]string{"a", "b"}, target.URL, r.URL))
			eventually(t, func() bool {
				return r.count("down") == 1 && len(getOverview(t, b).Notifications) == 1 && currentLifecycleState(t, c, m, "down")
			})
			original := getOverview(t, a).Incidents[0]
			c.split([]string{"a"}, []string{"b"})
			left, right := m, m
			left.Name, right.Name = "Left", "Right"
			if semantic {
				// A change and a later reversion must still break the generation;
				// matching the old target is insufficient causal evidence.
				right.Target += "/different"
				right = editMonitor(t, b, right)
				right.Target = m.Target
			}
			left = editMonitor(t, a, left)
			right = editMonitor(t, b, right)
			c.heal()
			eventually(t, func() bool {
				return getOverview(t, a).Monitors[0].ConfigConflict && getOverview(t, b).Monitors[0].ConfigConflict
			})
			// Only the full explicit resolution can decide which generation to
			// run. Conflicting observations cannot authorize a webhook/recovery.
			encoded, _ := json.Marshal(left)
			var resolution map[string]any
			_ = json.Unmarshal(encoded, &resolution)
			resolution["revisions"] = []string{left.Revision, right.Revision}
			code, body := api(t, a, "/api/monitors/"+m.ID+"/resolve", "POST", resolution, adminToken)
			if code != 200 || json.Unmarshal(body, &m) != nil {
				t.Fatal(code, string(body))
			}
			eventually(t, func() bool { return currentLifecycleState(t, c, m, "down") })
			if semantic {
				if checkGeneration(m) == checkGeneration(left) || checkGeneration(m) == checkGeneration(right) {
					t.Fatal("Mixed-generation resolution reused an old generation")
				}
				eventually(t, func() bool {
					for _, n := range c.nodes {
						for _, i := range getOverview(t, n).Incidents {
							if i.ID == original.ID && (i.ResolvedAt == nil || i.RetiredBy == "") {
								return false
							}
						}
					}
					return true
				})
			} else {
				if checkGeneration(m) != checkGeneration(left) || checkGeneration(m) != checkGeneration(right) {
					t.Fatal("Metadata conflict resolution split an uninterrupted outage")
				}
				time.Sleep(500 * time.Millisecond)
				for _, n := range c.nodes {
					v := getOverview(t, n)
					if len(v.Incidents) != 1 || v.Incidents[0].ID != original.ID || v.Incidents[0].ResolvedAt != nil || r.count("") != 1 {
						t.Fatal("Metadata conflict lost continuity", v.Incidents, r.all())
					}
				}
				status.Store(200)
				eventually(t, func() bool { return r.count("recovery") == 1 })
				if r.all()[1].IncidentID != r.all()[0].IncidentID {
					t.Fatal("Resolved metadata conflict recovered a different episode")
				}
			}
		})
	}
}

func TestRetirementSurvivesRestartAndUnknownLateSibling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "retired.db")
	o := Options{NodeID: "a", DBPath: path, Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Peers: []Peer{{ID: "b", URL: "http://127.0.0.1:1"}}}
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	m, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", ""), nil, "monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	old := episode("original", map[string][]int64{"a": {1}})
	old.Revision = m.Revision
	n.saveEpisode(old)
	originalVersion := n.heads[m.ID][0]
	m.Enabled = false
	paused, err := n.writeMonitor(m.MonitorInput, n.heads[m.ID], m.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	n.evaluate()
	if n.episodes[old.ID].RetiredBy != paused.Revision {
		t.Fatal("Pause did not retire the active generation")
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	o.AdminToken = ""
	n, err = New(o)
	if err != nil {
		t.Fatal(err)
	}
	if n.episodes[old.ID].RetiredBy != paused.Revision {
		t.Fatal("Restart lost the retirement marker")
	}
	// The peer run was never seen by the retiring node; its sequence is not
	// covered by the old closure. It still cannot remain an active orphan.
	late := episode("unknown-late", map[string][]int64{"b": {10}})
	late.Revision = old.Revision
	if err := n.mergeSnapshot(Snapshot{Versions: []Version{originalVersion}, Incidents: []Incident{old, late}}); err != nil {
		t.Fatal(err)
	}
	for _, i := range n.episodes {
		if i.Closed == nil || i.RetiredBy == "" {
			t.Fatal("A late old incident resurrected or escaped retirement", i)
		}
	}
	_ = n.Stop()
}

func TestRepeatPolicyEditsRetainIncidentAndAdvanceDeliverySlots(t *testing.T) {
	now, nowFn := clock()
	var status atomic.Int64
	status.Store(503)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(int(status.Load())) }))
	defer target.Close()
	r := webhook(t)
	n := local(t, func(o *Options) { o.Now = nowFn; o.NotificationDelayMs = Ptr(int64(0)) })
	in := input([]string{"a"}, target.URL, r.URL)
	in.RepeatAfterMs = Ptr(int64(1000))
	m := add(t, n, in)
	eventually(t, func() bool { return len(getOverview(t, n).Notifications) == 1 })
	initial := getOverview(t, n).Incidents[0]
	for slot := int64(1); slot <= 3; slot++ {
		now.Add(1000)
		eventually(t, func() bool { return len(getOverview(t, n).Notifications) == int(slot)+1 })
	}
	m.RepeatAfterMs = Ptr(int64(10000))
	m = editMonitor(t, n, m)
	now.Add(9999)
	time.Sleep(75 * time.Millisecond)
	if r.count("repeat") != 3 || r.count("down") != 1 {
		t.Fatal("Lengthening the repeat interval replayed or sent early", r.all())
	}
	now.Add(1)
	eventually(t, func() bool { return len(getOverview(t, n).Notifications) == 5 })
	if r.all()[4].RepeatSlot != 4 {
		t.Fatal("Repeat interval edit reused an old delivered slot", r.all())
	}
	m.RepeatAfterMs = Ptr(int64(1000))
	m = editMonitor(t, n, m)
	now.Add(1000)
	eventually(t, func() bool { return len(getOverview(t, n).Notifications) == 6 })
	if r.all()[5].RepeatSlot != 5 {
		t.Fatal("Shorter interval did not retain slot ordering", r.all())
	}
	m.RepeatAfterMs = Ptr(int64(0))
	m = editMonitor(t, n, m)
	now.Add(20000)
	time.Sleep(75 * time.Millisecond)
	v := getOverview(t, n)
	if r.count("") != 6 || len(v.Incidents) != 1 || v.Incidents[0].ID != initial.ID || v.Incidents[0].OpenedAt != initial.OpenedAt || *v.Incidents[0].NotifiedAt != *initial.NotifiedAt {
		t.Fatal("Repeat policy edit split or replayed the incident", r.all(), v.Incidents)
	}
	status.Store(200)
	now.Add(100)
	eventually(t, func() bool { return r.count("recovery") == 1 })
	for _, d := range r.all() {
		if d.IncidentID != r.all()[0].IncidentID {
			t.Fatal("Repeat edit changed delivered incident identity", r.all())
		}
	}
}

func TestSuppressionReleaseKeepsSenderStaggerAfterLongOutage(t *testing.T) {
	r := webhook(t)
	now, nowFn := clock()
	n, err := New(Options{NodeID: "b", DBPath: ":memory:", Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: nowFn, Peers: []Peer{{ID: "a", URL: "http://127.0.0.1:1"}}, NotificationDelayMs: Ptr(int64(160)), SyncIntervalMs: Ptr(int64(60)), PeerTimeoutMs: Ptr(int64(60))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	n.mu.Lock()
	g, err := n.writeGroup(GroupInput{Name: "Released policy", NodeIDs: []string{"a", "b"}, OutageMonitorID: "missing-parent", GraceMs: 1000}, nil, "group", false)
	if err != nil {
		n.mu.Unlock()
		t.Fatal(err)
	}
	in := input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL)
	in.GroupID = g.ID
	m, err := n.writeMonitor(in, nil, "monitor", false)
	if err != nil {
		n.mu.Unlock()
		t.Fatal(err)
	}
	i := episode("suppressed-episode", map[string][]int64{"b": {1}})
	i.Revision, i.OpenedAt = m.Revision, now.Load()-100000
	i.SuppressedByGroupID, i.SuppressedAt = g.ID, Ptr(i.OpenedAt)
	n.saveEpisode(i)
	n.samples[m.ID+"/b"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "b", State: "down", CheckedAt: now.Load(), Message: "HTTP 503"}, Sequence: 1, Revision: m.Revision, DownSince: Ptr(int64(1))}
	n.evaluate()
	if n.sending[m.ID+"/down"] || len(n.attempts) != 0 {
		n.mu.Unlock()
		t.Fatal("A released secondary sender skipped the preferred sender's window")
	}
	// The child's original grace expired long ago. Only the 240ms sender
	// stagger starts at release; no new 1000ms group grace is imposed.
	now.Add(239)
	n.evaluate()
	if n.sending[m.ID+"/down"] {
		n.mu.Unlock()
		t.Fatal("Secondary sender was eligible before its release window")
	}
	now.Add(1)
	n.evaluate()
	n.mu.Unlock()
	eventually(t, func() bool { return r.count("down") == 1 })
	if r.all()[0].IncidentID != i.ID {
		t.Fatal("Release replaced the suppressed incident's identity", r.all())
	}
}
