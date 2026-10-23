package canary

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	maintenanceEngine "uptime-canary/apps/node-go/internal/maintenance"
)

// Independent round-two review. Only this new test file and the matching
// report are reviewer-owned. Nodes never Start; all stores are private.
func cr2Options(t *testing.T, disk bool) Options {
	t.Helper()
	path := ":memory:"
	if disk {
		path = filepath.Join(t.TempDir(), "review.db")
	}
	return Options{NodeID: "a", DBPath: path, Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Peers: []Peer{{ID: "b", URL: "http://127.0.0.1:1"}}, NotificationDelayMs: Ptr(int64(0))}
}

func cr2New(t *testing.T, o Options) *Node {
	t.Helper()
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	return n
}

func cr2Input(t *testing.T, ids []string) MonitorInput {
	t.Helper()
	in := input(ids, "http://127.0.0.1:1/", "")
	known := map[string]bool{"a": true, "b": true}
	if err := validateMonitor(&in, known); err != nil {
		t.Fatal(err)
	}
	return in
}

func cr2Body(t *testing.T, s Snapshot) []byte {
	t.Helper()
	s.Protocol, s.Node = peerProtocol, Identity{"b", "Review peer"}
	if s.Versions == nil {
		s.Versions = []Version{}
	}
	if s.GroupVersions == nil {
		s.GroupVersions = []GroupVersion{}
	}
	if s.Resources == nil {
		s.Resources = []ResourceVersion{}
	}
	if s.Observations == nil {
		s.Observations = []Sample{}
	}
	if s.Incidents == nil {
		s.Incidents = []Incident{}
	}
	if s.Receipts == nil {
		s.Receipts = []Receipt{}
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func cr2Decode(t *testing.T, n *Node, s Snapshot) Snapshot {
	t.Helper()
	p, err := n.validateSnapshot(cr2Body(t, s), "b")
	if err != nil {
		t.Fatal("invalid review fixture", err)
	}
	return p
}

func cr2Apply(t *testing.T, n *Node, s Snapshot) {
	t.Helper()
	p := cr2Decode(t, n, s)
	if err := n.applySnapshot(p); err != nil {
		t.Fatal(err)
	}
}

func cr2Post(t *testing.T, n *Node, b []byte) int {
	t.Helper()
	r := httptest.NewRequest("POST", "/internal/snapshot", bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer "+clusterToken)
	r.Header.Set("X-Canary-Node-Id", "b")
	w := httptest.NewRecorder()
	n.ServeHTTP(w, r)
	n.wg.Wait()
	return w.Code
}

func cr2Incident(n *Node, id, rev string, run int64) Incident {
	return Incident{ID: id, Aliases: []string{id}, MonitorID: "cr2-monitor", MonitorName: "Review", Revision: rev, OpenedAt: n.now() - 1000, Runs: map[string][]int64{"b": {run}}, Predecessors: []string{}}
}

func TestCausalCapacityReviewRound2NullCommitmentCannotBecomeLegacyAuthority(t *testing.T) {
	for _, kind := range []string{"monitor", "group", "resource", "incident"} {
		for _, variant := range []string{"committed-missing-proof", "legacy-omitted", "null", "empty", "case-null", "case-empty", "case-override-empty"} {
			t.Run(kind+"/"+variant, func(t *testing.T) {
				n := cr2New(t, cr2Options(t, false))
				s, key, field := Snapshot{}, "", "edgeId"
				switch kind {
				case "monitor":
					m := Monitor{MonitorInput: cr2Input(t, []string{"a", "b"}), ID: "cr2-monitor", Revision: "cr2-revision", UpdatedAt: n.now()}
					s.Versions, key = []Version{{Monitor: m, Ancestors: []string{}, EdgeID: digest("missing-edge")}}, "versions"
				case "group":
					g := Group{GroupInput: GroupInput{Name: "Review", NodeIDs: []string{"a", "b"}}, ID: "cr2-group", Revision: "cr2-revision", UpdatedAt: n.now()}
					s.GroupVersions, key = []GroupVersion{{Group: g, Ancestors: []string{}, EdgeID: digest("missing-edge")}}, "groupVersions"
				case "resource":
					data, _ := json.Marshal(hookChannel("Review", "http://127.0.0.1:1"))
					r := Resource{Kind: resourceChannel, ID: "cr2-channel", Revision: "cr2-revision", UpdatedAt: n.now(), Data: data}
					s.Resources, key = []ResourceVersion{{Resource: r, Ancestors: []string{}, EdgeID: digest("missing-edge")}}, "resources"
				case "incident":
					i := cr2Incident(n, "cr2-incident", "cr2-revision", 1)
					i.EvidenceRoot = digest("missing-root")
					s.Incidents, key, field = []Incident{i}, "incidents", "evidenceRoot"
				}
				var raw map[string]any
				if err := json.Unmarshal(cr2Body(t, s), &raw); err != nil {
					t.Fatal(err)
				}
				record := raw[key].([]any)[0].(map[string]any)
				if variant == "legacy-omitted" {
					delete(record, field)
				}
				if variant == "null" {
					record[field] = nil
				}
				if variant == "empty" {
					record[field] = ""
				}
				if variant == "case-null" || variant == "case-empty" {
					delete(record, field)
					record[strings.ToUpper(field)] = nil
					if variant == "case-empty" {
						record[strings.ToUpper(field)] = ""
					}
				}
				body, _ := json.Marshal(raw)
				if variant == "case-override-empty" {
					// Keep a valid exact-spelling field in the raw map, then let a
					// later case-insensitive alias overwrite the typed decoder.
					needle := fmt.Sprintf(`"%s":%q`, field, record[field])
					body = bytes.Replace(body, []byte(needle), []byte(needle+fmt.Sprintf(`,"%s":""`, strings.ToUpper(field))), 1)
				}
				status := cr2Post(t, n, body)
				ready := false
				switch kind {
				case "monitor":
					ready = n.current("cr2-monitor") != nil
				case "group":
					ready = n.currentGroup("cr2-group") != nil
				case "resource":
					ready = n.currentResource(resourceChannel, "cr2-channel") != nil
				case "incident":
					ready = len(n.episodes) != 0
				}
				if variant != "legacy-omitted" && variant != "committed-missing-proof" {
					if status != 400 || ready {
						t.Fatalf("null %s entered legacy migration: HTTP=%d executable=%t", field, status, ready)
					}
				} else if status != 200 || ready != (variant == "legacy-omitted") {
					t.Fatalf("control: HTTP=%d executable=%t", status, ready)
				}
			})
		}
	}
}

func TestCausalCapacityReviewRound2LateClosureReversesEvaluatedProjection(t *testing.T) {
	for _, evaluateBeforeClosure := range []bool{false, true} {
		t.Run(fmt.Sprint(evaluateBeforeClosure), func(t *testing.T) {
			r := webhook(t)
			o := cr2Options(t, true)
			n := cr2New(t, o)
			in := cr2Input(t, []string{"a", "b"})
			in.WebhookURL = r.URL + "/"
			m, err := n.writeMonitor(in, nil, "cr2-monitor", false)
			if err != nil {
				t.Fatal(err)
			}
			prior := cr2Incident(n, "a-prior", m.Revision, 1)
			prior.OpenedAt, prior.Runs = n.now()-10000, map[string][]int64{"a": {1}}
			prior.DeliveryTargets = []string{""}
			current := cr2Incident(n, "c-current", m.Revision, 3)
			current.OpenedAt = n.now() - 1000
			receipt := Receipt{NotificationView: NotificationView{ID: "cr2-old-receipt", MonitorID: m.ID, IncidentID: prior.ID, Kind: "down", NodeID: "b", SentAt: prior.OpenedAt + 100}}
			cr2Apply(t, n, Snapshot{Incidents: []Incident{prior, current}, Receipts: []Receipt{receipt}})
			if len(n.episodes) != 1 || len(n.incidentSources) != 2 {
				t.Fatal("fixture did not initially alias two original open sources")
			}
			if evaluateBeforeClosure {
				// A new local down run extends the inferred projection. Original
				// remote sources must still remain separable when closure arrives.
				n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: n.now(), Message: "new local run"}, Revision: m.Revision, Sequence: 3, DownSince: Ptr(int64(3))}
				n.samples[m.ID+"/b"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "b", State: "down", CheckedAt: n.now(), Message: "new peer run"}, Revision: m.Revision, Sequence: 3, DownSince: Ptr(int64(3))}
				n.evaluate()
				if n.fault != nil {
					t.Fatal(n.fault)
				}
			}
			prior.Closed, prior.ResolvedAt = map[string]int64{"a": 2, "b": 2}, Ptr(n.now()-5000)
			cr2Apply(t, n, Snapshot{Incidents: []Incident{prior}})
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			found := false
			for _, i := range n.episodes {
				if slices.Contains(episodeIDs(i), current.ID) {
					if i.Closed != nil || slices.Contains(episodeIDs(i), prior.ID) || len(n.forEpisode(i)) != 0 {
						t.Fatalf("local evaluation permanently promoted inferred aliases into source authority across restart: current=%+v sources=%+v receipts=%+v", i, n.incidentSources, n.forEpisode(i))
					}
					found = true
				}
			}
			if !found {
				t.Fatal("lost current source identity")
			}
			n.mu.Lock()
			n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: n.now(), Message: "new local run"}, Revision: m.Revision, Sequence: 3, DownSince: Ptr(int64(3))}
			n.samples[m.ID+"/b"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "b", State: "down", CheckedAt: n.now(), Message: "new peer run"}, Revision: m.Revision, Sequence: 3, DownSince: Ptr(int64(3))}
			n.evaluate()
			n.mu.Unlock()
			n.wg.Wait()
			if r.count("down") != 1 || r.count("recovery") != 0 {
				t.Fatal("late closure failed to restore the current source's owed down", r.all())
			}
		})
	}
}

func TestCausalCapacityReviewRound2LegacyCommitmentWaitsForExactClaims(t *testing.T) {
	for _, kind := range []string{"monitor", "group", "maintenance"} {
		t.Run(kind, func(t *testing.T) {
			o := cr2Options(t, true)
			o.Now = func() int64 { return time.Date(2026, 10, 22, 12, 5, 0, 0, time.UTC).UnixMilli() }
			n := cr2New(t, o)
			edges, claims, links := map[string]RevisionEdge{}, map[string]RevisionClaim{}, map[string]EvidenceLink{}
			s := Snapshot{}
			switch kind {
			case "monitor":
				m := Monitor{MonitorInput: cr2Input(t, []string{"a", "b"}), ID: "cr2-monitor", Revision: "legacy-tip", UpdatedAt: n.now()}
				e := legacyRevision("monitor", m.ID, m.Revision, m, []string{"legacy-parent"}, m.Revision, edges, claims, links)
				s.Versions = []Version{{Monitor: m, Ancestors: []string{}, EdgeID: e.ID}}
			case "group":
				g := Group{GroupInput: GroupInput{Name: "Review", NodeIDs: []string{"a", "b"}}, ID: "cr2-group", Revision: "legacy-tip", UpdatedAt: n.now()}
				e := legacyRevision("group", g.ID, g.Revision, g, []string{"legacy-parent"}, "", edges, claims, links)
				s.GroupVersions = []GroupVersion{{Group: g, Ancestors: []string{}, EdgeID: e.ID}}
			case "maintenance":
				in := MaintenanceInput{Name: "Review", Enabled: true, AllMonitors: true, Schedule: maintenanceEngine.Config{Strategy: maintenanceEngine.Once, Timezone: "UTC", StartDate: "2026-10-22", EndDate: "2026-10-22", StartTime: "12:00", DurationMinutes: 10}}
				data, _ := json.Marshal(in)
				r := Resource{Kind: resourceMaintenance, ID: "cr2-maintenance", Revision: "legacy-tip", UpdatedAt: n.now(), Data: data}
				e := legacyRevision(r.Kind, r.ID, r.Revision, r, []string{"legacy-parent"}, "", edges, claims, links)
				s.Resources = []ResourceVersion{{Resource: r, Ancestors: []string{}, EdgeID: e.ID}}
			}
			for _, e := range edges {
				s.RevisionEdges = append(s.RevisionEdges, e)
			}
			// This is the exact envelope produced by legacyRevision. Its small
			// edge can precede the independently paged exact claims and links.
			cr2Apply(t, n, s)
			ready := func() bool {
				switch kind {
				case "monitor":
					return n.current("cr2-monitor") != nil
				case "group":
					return n.currentGroup("cr2-group") != nil
				default:
					views := n.maintenanceViews()
					return len(views) == 1 && views[0].Active
				}
			}
			if ready() {
				t.Error("legacy root is missing its committed claims/links but configuration is executable")
			}
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			if ready() {
				t.Error("restart promoted incomplete legacy commitment into executable configuration")
			}
			late := Snapshot{}
			for _, c := range claims {
				late.RevisionClaims = append(late.RevisionClaims, c)
			}
			for _, l := range links {
				late.EvidenceLinks = append(late.EvidenceLinks, l)
			}
			cr2Apply(t, n, late)
			if !ready() {
				t.Fatal("complete exact claims failed to enable configuration/invalidate cached maintenance")
			}
		})
	}
}

func TestCausalCapacityReviewRound2ImmutablePayloadScopeAndCycleChecks(t *testing.T) {
	for _, direction := range []string{"head-first", "edge-first"} {
		t.Run(direction, func(t *testing.T) {
			n := cr2New(t, cr2Options(t, false))
			m := Monitor{MonitorInput: cr2Input(t, []string{"a"}), ID: "cr2-monitor", Revision: "revision", UpdatedAt: n.now()}
			e := newRevisionEdge("monitor", m.ID, m.Revision, m, []string{}, m.Revision)
			v := Version{Monitor: m, Ancestors: []string{}, EdgeID: e.ID}
			changed := v
			changed.Monitor.Name = "different immutable content"
			first, bad, good := Snapshot{Versions: []Version{v}}, Snapshot{}, Snapshot{}
			if direction == "head-first" {
				wrong := newRevisionEdge("monitor", m.ID, m.Revision, changed.Monitor, []string{}, m.Revision)
				bad.RevisionEdges, good.RevisionEdges = []RevisionEdge{wrong}, []RevisionEdge{e}
			} else {
				first, bad, good = Snapshot{RevisionEdges: []RevisionEdge{e}}, Snapshot{Versions: []Version{changed}}, Snapshot{Versions: []Version{v}}
			}
			cr2Apply(t, n, first)
			before := contentID([]any{n.heads, n.revisionEdges})
			if err := n.applySnapshot(cr2Decode(t, n, bad)); err == nil {
				t.Fatal("accepted immutable payload mismatch")
			}
			if contentID([]any{n.heads, n.revisionEdges}) != before {
				t.Fatal("rejected mismatch changed state")
			}
			cr2Apply(t, n, good)
			if n.current(m.ID) == nil {
				t.Fatal("valid proof did not enable head")
			}
		})
	}
	t.Run("scope-and-namespaces", func(t *testing.T) {
		n := cr2New(t, cr2Options(t, false))
		m := Monitor{MonitorInput: cr2Input(t, []string{"a"}), ID: "shared-id", Revision: "shared-revision", UpdatedAt: n.now()}
		g := Group{GroupInput: GroupInput{Name: "Review", NodeIDs: []string{"a"}}, ID: m.ID, Revision: m.Revision, UpdatedAt: n.now()}
		data, _ := json.Marshal(hookChannel("Review", "http://127.0.0.1:1"))
		r := Resource{Kind: resourceChannel, ID: m.ID, Revision: m.Revision, UpdatedAt: n.now(), Data: data}
		me, ge, re := newRevisionEdge("monitor", m.ID, m.Revision, m, []string{}, m.Revision), newRevisionEdge("group", g.ID, g.Revision, g, []string{}, ""), newRevisionEdge(r.Kind, r.ID, r.Revision, r, []string{}, "")
		cr2Apply(t, n, Snapshot{Versions: []Version{{Monitor: m, Ancestors: []string{}, EdgeID: me.ID}}, GroupVersions: []GroupVersion{{Group: g, Ancestors: []string{}, EdgeID: ge.ID}}, Resources: []ResourceVersion{{Resource: r, Ancestors: []string{}, EdgeID: re.ID}}, RevisionEdges: []RevisionEdge{me, ge, re}})
		if n.current(m.ID) == nil || n.currentGroup(g.ID) == nil || n.currentResource(r.Kind, r.ID) == nil {
			t.Fatal("entity namespaces collided")
		}
		m.ID, m.Revision = "other-monitor", "other-revision"
		if err := n.applySnapshot(cr2Decode(t, n, Snapshot{Versions: []Version{{Monitor: m, Ancestors: []string{}, EdgeID: me.ID}}})); err == nil {
			t.Fatal("cross-entity edge acquired authority")
		}
		for _, generation := range []string{"other-generation", ""} {
			collision := me
			collision.Generation = generation
			collision.ID = edgeID(collision)
			if err := n.applySnapshot(cr2Decode(t, n, Snapshot{RevisionEdges: []RevisionEdge{collision}})); err == nil {
				t.Fatal("same entity/revision acquired a second edge identity")
			}
		}
	})
	t.Run("cycle-arrives-later", func(t *testing.T) {
		n := cr2New(t, cr2Options(t, false))
		a := newRevisionEdge("group", "cycle-group", "r1", "payload1", []string{"r2"}, "")
		b := newRevisionEdge("group", "cycle-group", "r2", "payload2", []string{"r1"}, "")
		cr2Apply(t, n, Snapshot{RevisionEdges: []RevisionEdge{a}})
		if err := n.applySnapshot(cr2Decode(t, n, Snapshot{RevisionEdges: []RevisionEdge{b}})); err == nil {
			t.Fatal("accepted revision cycle")
		}
		if len(n.revisionEdges) != 1 {
			t.Fatal("cycle rejection published staged edge")
		}
		var count int
		if err := n.store.db.QueryRow(`SELECT COUNT(*) FROM revision_index`).Scan(&count); err != nil || count != 1 {
			t.Fatal("cycle changed durable index", count, err)
		}
	})
	t.Run("incident-fact-scope", func(t *testing.T) {
		n := cr2New(t, cr2Options(t, false))
		i := cr2Incident(n, "scope-incident", "generation", 1)
		facts, links := map[string]IncidentFact{}, map[string]EvidenceLink{}
		other := i
		other.MonitorID = "other-monitor"
		other = prepareIncident(other, "", facts, links)
		i.EvidenceRoot = other.EvidenceRoot
		s := Snapshot{Incidents: []Incident{i}}
		for _, f := range facts {
			s.IncidentFacts = append(s.IncidentFacts, f)
		}
		for _, l := range links {
			s.EvidenceLinks = append(s.EvidenceLinks, l)
		}
		if err := n.applySnapshot(cr2Decode(t, n, s)); err == nil {
			t.Fatal("accepted cross-monitor incident commitment")
		}
		if len(n.incidentFacts)+len(n.evidenceLinks)+len(n.episodes)+len(n.pendingIncidents) != 0 {
			t.Fatal("scope rejection published staged facts")
		}
	})
}

func cr2State(n *Node) string {
	return contentID([]any{n.heads, n.groups, n.resources, n.revisionEdges, n.revisionClaims, n.incidentFacts, n.evidenceLinks, n.incidentSources, n.pendingIncidents, n.episodes, n.receipts, n.intents, n.intentIndex})
}

func TestCausalCapacityReviewRound2FailedSQLLeavesInputAndIndexesRetryable(t *testing.T) {
	for _, stage := range []string{"revision_parents", "incident_fact_index", "incidentSources"} {
		t.Run(stage, func(t *testing.T) {
			o := cr2Options(t, true)
			n := cr2New(t, o)
			m := Monitor{MonitorInput: cr2Input(t, []string{"a", "b"}), ID: "cr2-monitor", Revision: "legacy-revision", UpdatedAt: n.now()}
			i := cr2Incident(n, "atomic-source", m.Revision, 1)
			i.DeliveryTargets = []string{"audience-target"}
			// Exercise the exact input normalization that previously mutated the
			// caller's slices on failure: an explicit self-contained legacy head.
			page := cr2Decode(t, n, Snapshot{Versions: []Version{{Monitor: m, Ancestors: []string{"legacy-ancestor"}}}, Incidents: []Incident{i}, Intents: []NotificationIntent{deliveryIntent(m.ID, i.ID, "separate-target")}})
			inputBefore, memoryBefore := contentID(page), cr2State(n)
			trigger := fmt.Sprintf(`CREATE TRIGGER cr2_fail BEFORE INSERT ON %s BEGIN SELECT RAISE(FAIL,'injected index failure'); END`, stage)
			if stage == "incidentSources" {
				trigger = `CREATE TRIGGER cr2_fail BEFORE INSERT ON records WHEN NEW.category='incidentSources' BEGIN SELECT RAISE(FAIL,'injected source failure'); END`
			}
			if _, err := n.store.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			if err := n.applySnapshot(page); err == nil {
				t.Fatal("failed SQL page was acknowledged")
			}
			if contentID(page) != inputBefore || cr2State(n) != memoryBefore {
				t.Fatal("failed page changed retry input or published map/index authority")
			}
			for _, table := range []string{"revision_index", "revision_parents", "incident_fact_index"} {
				var count int
				if err := n.store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("rollback left %s rows: %d %v", table, count, err)
				}
			}
			var count int
			if err := n.store.db.QueryRow(`SELECT COUNT(*) FROM records WHERE category IN ('heads','revisionEdges','revisionClaims','incidentFacts','evidenceLinks','incidentSources','pendingIncidents','incidents','notificationIntents')`).Scan(&count); err != nil || count != 0 {
				t.Fatal("rollback persisted staged records", count, err)
			}
			if _, err := n.store.db.Exec(`DROP TRIGGER cr2_fail`); err != nil {
				t.Fatal(err)
			}
			if err := n.applySnapshot(page); err != nil {
				t.Fatal("identical input retry failed", err)
			}
			if contentID(page) != inputBefore || n.current(m.ID) == nil || !n.hasDeliveryIntent(n.episodes[i.ID], "separate-target") {
				t.Fatal("retry changed input or lost execution/audience authority")
			}
			for table, want := range map[string]int{"revision_index": 1, "revision_parents": 1, "incident_fact_index": 2} {
				if err := n.store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != want {
					t.Fatalf("successful retry %s count=%d want=%d err=%v", table, count, want, err)
				}
			}
			committed := cr2State(n)
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			if cr2State(n) != committed {
				t.Fatal("restart changed committed causal identities/indexes")
			}
			if err := n.applySnapshot(page); err != nil || cr2State(n) != committed {
				t.Fatal("legacy replay after restart was not idempotent", err)
			}
		})
	}
}

func TestCausalCapacityReviewRound2PendingPagesHydrateAllFactsAndStayBounded(t *testing.T) {
	source := cr2New(t, cr2Options(t, false))
	i := cr2Incident(source, "z-source", "generation", 1)
	i.Runs["b"] = []int64{}
	for run := int64(1); run <= 230; run++ {
		i.Runs["b"] = append(i.Runs["b"], run)
	}
	for index := range 44 {
		i.Aliases = append(i.Aliases, fmt.Sprintf("alias-%03d", index))
	}
	for index := range 60 {
		i.Predecessors = append(i.Predecessors, fmt.Sprintf("predecessor-%03d", index))
	}
	source.saveEpisode(i)
	if source.fault != nil {
		t.Fatal(source.fault)
	}
	pages := []Snapshot{}
	for cursor := ""; ; {
		p, err := source.snapshotPage(cursor, "")
		if err != nil {
			t.Fatal(err)
		}
		body, err := json.Marshal(p)
		if err != nil || len(body) > maxBody {
			t.Fatal("producer body exceeds hard bound", len(body), err)
		}
		count := len(p.Versions) + len(p.GroupVersions) + len(p.Resources) + len(p.Intents) + len(p.Observations) + len(p.Incidents) + len(p.Receipts) + len(p.RevisionEdges) + len(p.RevisionClaims) + len(p.IncidentFacts) + len(p.EvidenceLinks)
		if count > pageRecords {
			t.Fatal("producer page exceeds record bound", count)
		}
		pages = append(pages, p)
		if p.Next == "" {
			break
		}
		if len(pages) > 16 || p.Next <= cursor {
			t.Fatal("pagination did not progress")
		}
		cursor = p.Next
	}
	if len(pages) < 2 {
		t.Fatal("fixture did not cross a real page boundary")
	}
	o := cr2Options(t, true)
	n := cr2New(t, o)
	summary := source.incidentSources[i.ID]
	if len(summary.Runs["b"]) != inlineIncidentRuns || len(summary.Aliases) != inlineIncidentIDs || len(summary.Predecessors) != inlineIncidentIDs {
		t.Fatal("source summary is not bounded")
	}
	cr2Apply(t, n, Snapshot{Incidents: []Incident{summary}})
	if len(n.episodes) != 0 || len(n.pendingIncidents) != 1 {
		t.Fatal("incomplete incident summary became executable")
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = cr2New(t, o)
	if len(n.episodes) != 0 || len(n.pendingIncidents) != 1 {
		t.Fatal("pending commitment changed authority on restart")
	}
	// Delay one final immutable run fact while delivering every other record,
	// in reverse page order. Links and the summary alone cannot certify it.
	var withheld IncidentFact
	for _, f := range source.incidentFacts {
		if f.IncidentID == i.ID && f.Kind == "run" && f.Run == 1 {
			withheld = f
			break
		}
	}
	if withheld.ID == "" {
		t.Fatal("missing final-fact fixture")
	}
	slices.Reverse(pages)
	for _, p := range pages {
		p.IncidentFacts = slices.DeleteFunc(append([]IncidentFact{}, p.IncidentFacts...), func(f IncidentFact) bool { return f.ID == withheld.ID })
		cr2Apply(t, n, p)
	}
	if len(n.episodes) != 0 || len(n.pendingIncidents) != 1 {
		t.Fatal("missing single run fact was interpreted as absent historical evidence")
	}
	cr2Apply(t, n, Snapshot{IncidentFacts: []IncidentFact{withheld}})
	canonical := unique(i.Aliases)[0]
	got := n.episodes[canonical]
	if len(n.pendingIncidents) != 0 || !equalJSON(got.Runs, i.Runs) || !slices.Equal(got.Aliases, unique(i.Aliases)) || !slices.Equal(got.Predecessors, unique(i.Predecessors)) {
		t.Fatalf("completed incident lost facts beyond its inline summaries: pending=%d runs=%d/%d aliases=%d/%d predecessors=%d/%d root=%s", len(n.pendingIncidents), len(got.Runs["b"]), len(i.Runs["b"]), len(got.Aliases), len(i.Aliases), len(got.Predecessors), len(i.Predecessors), got.EvidenceRoot)
	}
	var count int
	if err := n.store.db.QueryRow(`SELECT COUNT(*) FROM incident_fact_index`).Scan(&count); err != nil || count != len(n.incidentFacts) {
		t.Fatal("fact index is not atomic/complete", count, len(n.incidentFacts), err)
	}
	before := cr2State(n)
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = cr2New(t, o)
	if cr2State(n) != before || !slices.Contains(n.episodes[canonical].Runs["b"], int64(1)) {
		t.Fatal("restart changed root identities or truncated old run evidence")
	}
}
