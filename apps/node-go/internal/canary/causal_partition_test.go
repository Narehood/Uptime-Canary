package canary

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
)

func partitionDrive(t *testing.T, n *Node) {
	t.Helper()
	n.mu.Lock()
	n.nextSync = maxSafe
	n.mu.Unlock()
	n.tick()
	n.wg.Wait()
	if n.Error() != nil {
		t.Fatal(n.Error())
	}
}
func partitionPeerUp(t *testing.T, n *Node, m Monitor, seq int64) {
	t.Helper()
	s := Sample{Observation: Observation{MonitorID: m.ID, NodeID: "b", State: "up", CheckedAt: n.now(), Message: "peer up"}, Revision: m.Revision, CheckGeneration: m.CheckGeneration, Sequence: seq}
	n.samples[m.ID+"/b"] = s
	if err := put(n.store.db, "samples", m.ID+"/b", s); err != nil {
		t.Fatal(err)
	}
}
func partitionEvidence(i Incident) (Incident, []IncidentFact, []EvidenceLink) {
	facts, links := map[string]IncidentFact{}, map[string]EvidenceLink{}
	i = boundedIncident(prepareIncident(i, "", facts, links))
	return i, slices.Collect(maps.Values(facts)), slices.Collect(maps.Values(links))
}
func partitionComplete(t *testing.T, n *Node, facts []IncidentFact, links []EvidenceLink, reverse bool) {
	t.Helper()
	facts = append([]IncidentFact{}, facts...)
	links = append([]EvidenceLink{}, links...)
	if reverse {
		slices.Reverse(facts)
		slices.Reverse(links)
	}
	sets := []Snapshot{{IncidentFacts: facts}, {EvidenceLinks: links}}
	if reverse {
		slices.Reverse(sets)
	}
	for _, page := range sets {
		if err := n.mergeSnapshot(cr2Decode(t, n, page)); err != nil {
			t.Fatal(err)
		}
		n.wg.Wait()
	}
}

func TestPartitionUncertaintyOngoingAndLaterOutagesAcrossRestartAndCompletion(t *testing.T) {
	for _, ongoing := range []bool{true, false} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("ongoing=%v/reverse=%v", ongoing, reverse), func(t *testing.T) {
				var now atomic.Int64
				now.Store(maintenanceClock(12, 0))
				var failing atomic.Bool
				failing.Store(ongoing)
				var probes atomic.Int64
				r := webhook(t)
				o := cr2Options(t, true)
				o.Now = now.Load
				o.Probe = func(context.Context, Monitor) ProbeResult {
					probes.Add(1)
					if failing.Load() {
						return ProbeResult{State: "down", Message: "local failure"}
					}
					return ProbeResult{State: "up", Message: "local up"}
				}
				n := cr2New(t, o)
				in := input([]string{"a", "b"}, "http://127.0.0.1:1", "")
				in.Enabled = false
				m, err := n.writeMonitor(in, nil, "partition-monitor", false)
				if err != nil {
					t.Fatal(err)
				}
				// Enable before the controlled probes; no audience until after the
				// opaque header, so the control must deliver from its new local branch.
				in.Enabled = true
				m, err = n.writeMonitor(in, n.heads[m.ID], m.ID, false)
				if err != nil {
					t.Fatal(err)
				}
				partitionPeerUp(t, n, m, 2)
				partitionDrive(t, n)
				old := Incident{ID: "old-peer-source", Aliases: []string{"old-peer-source"}, MonitorID: m.ID, MonitorName: m.Name, Revision: checkGeneration(m), OpenedAt: now.Load() - 10000, Runs: map[string][]int64{"a": {1}}, Predecessors: []string{}}
				if !ongoing {
					old.Runs = map[string][]int64{"b": {1}}
					old.Closed = map[string]int64{"a": 1, "b": 2}
					old.ResolvedAt = Ptr(now.Load() - 5000)
				}
				header, facts, links := partitionEvidence(old)
				receipts := []Receipt{{NotificationView: NotificationView{ID: "prior-down", MonitorID: m.ID, IncidentID: old.ID, Kind: "down", NodeID: "b", SentAt: old.OpenedAt + 100}}}
				if !ongoing {
					receipts = append(receipts, Receipt{NotificationView: NotificationView{ID: "prior-recovery", MonitorID: m.ID, IncidentID: old.ID, Kind: "recovery", NodeID: "b", SentAt: *old.ResolvedAt + 100}})
				}
				cr2Apply(t, n, Snapshot{Incidents: []Incident{header}, Receipts: receipts, Intents: []NotificationIntent{deliveryIntent(m.ID, old.ID, "")}})
				in.WebhookURL = r.URL
				m, err = n.writeMonitor(in, n.heads[m.ID], m.ID, false)
				if err != nil {
					t.Fatal(err)
				}
				failing.Store(true)
				now.Add(1000)
				partitionPeerUp(t, n, m, 2)
				partitionDrive(t, n)
				if probes.Load() != 2 || r.count("down") != 1 || len(n.pendingIncidents) != 1 {
					t.Fatal("opaque history stopped immediate local execution", probes.Load(), r.all())
				}
				localID := ""
				for _, i := range n.episodes {
					if localBranch(i, n.ID, downWitness(n.fresh(m), n.ID)) {
						localID = i.ID
						if len(n.forEpisode(i)) != 1 {
							t.Fatal("old receipt acquired the local branch", n.forEpisode(i))
						}
					}
				}
				if localID == "" {
					t.Fatal("fresh branch missing")
				}
				if err = n.Stop(); err != nil {
					t.Fatal(err)
				}
				n = cr2New(t, o)
				now.Add(60 * 60 * 1000)
				partitionDrive(t, n)
				if probes.Load() != 3 || r.count("down") != 1 {
					t.Fatal("restart or long partition lost the branch", probes.Load(), r.all())
				}
				history, e := n.store.history(m.ID, 100)
				if e != nil || len(history) != 3 {
					t.Fatal("local history lost", e, len(history))
				}
				partitionComplete(t, n, facts, links, reverse)
				var current Incident
				for _, i := range n.episodes {
					if slices.Contains(episodeIDs(i), localID) {
						current = i
					}
				}
				if current.ID == "" || current.Closed != nil {
					t.Fatal("completion closed or lost the local outage")
				}
				if ongoing {
					if !slices.Contains(episodeIDs(current), old.ID) || len(n.episodes) != 1 {
						t.Fatal("positive same-run proof did not converge ongoing outage", n.episodes)
					}
				} else {
					if slices.Contains(episodeIDs(current), old.ID) || len(n.forEpisode(current)) != 1 {
						t.Fatal("old closed proof contaminated a later outage", current, n.forEpisode(current))
					}
				}
				if r.count("down") != 1 {
					t.Fatal("proof arrival invented another local down", r.all())
				}
				// Replaying the stale header is idempotent after the proof is complete.
				cr2Apply(t, n, Snapshot{Incidents: []Incident{header}, Receipts: receipts})
				n.evaluate()
				n.wg.Wait()
				if r.count("down") != 1 {
					t.Fatal("stale replay rearmed delivery", r.all())
				}
				failing.Store(false)
				now.Add(1000)
				partitionPeerUp(t, n, m, 4)
				partitionDrive(t, n)
				if r.count("recovery") != 1 {
					t.Fatal("known all-up evidence did not recover local delivery", r.all())
				}
				// The source projection and every original fact remain reproducible at
				// a passive peer after complete reversed-page catch-up.
				b := causalCapacityNode(t, "b", filepath.Join(t.TempDir(), "peer.db"))
				b.now = now.Load
				pages := causalCapacityPages(t, n)
				slices.Reverse(pages)
				for _, page := range pages {
					if e = b.applySnapshot(page); e != nil {
						t.Fatal(e)
					}
				}
				if len(b.pendingIncidents) != 0 || len(b.incidentFacts) != len(n.incidentFacts) || len(b.receipts) != len(n.receipts) || len(b.episodes) != len(n.episodes) {
					t.Fatal("complete page catch-up did not converge", len(b.episodes), len(n.episodes))
				}
				for _, i := range b.episodes {
					if slices.Contains(episodeIDs(i), localID) && i.Closed == nil {
						t.Fatal("passive projection resurrected local source")
					}
				}
				_ = n.Stop()
			})
		}
	}
}

func TestPartitionMultipleOpaqueHeadersLocalHealthyBoundaryAndAtomicRetry(t *testing.T) {
	var now atomic.Int64
	now.Store(maintenanceClock(12, 0))
	var failing atomic.Bool
	o := cr2Options(t, true)
	o.Now = now.Load
	o.Probe = func(context.Context, Monitor) ProbeResult {
		if failing.Load() {
			return ProbeResult{State: "down", Message: "down"}
		}
		return ProbeResult{State: "up", Message: "up"}
	}
	r := webhook(t)
	n := cr2New(t, o)
	m, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL), nil, "multiple-pending", false)
	if err != nil {
		t.Fatal(err)
	}
	partitionPeerUp(t, n, m, 2)
	partitionDrive(t, n)
	for index := range 40 {
		i := Incident{ID: fmt.Sprintf("opaque-%02d", index), Aliases: []string{fmt.Sprintf("opaque-%02d", index)}, MonitorID: m.ID, MonitorName: m.Name, Revision: checkGeneration(m), OpenedAt: now.Load() - 10000, Runs: map[string][]int64{"b": {1}}, Predecessors: []string{}, EvidenceRoot: digest(fmt.Sprintf("unreceived-%d", index))}
		cr2Apply(t, n, Snapshot{Incidents: []Incident{i}})
	}
	failing.Store(true)
	now.Add(1000)
	partitionDrive(t, n)
	if r.count("down") != 1 {
		t.Fatal("multiple pending branches blocked down", r.all())
	}
	firstID := r.all()[0].IncidentID
	// A local healthy observation followed by a new down run gets independent
	// authority even when the unavailable peer prevents global recovery proof.
	failing.Store(false)
	now.Add(60 * 60 * 1000)
	partitionDrive(t, n)
	failing.Store(true)
	now.Add(1000)
	partitionDrive(t, n)
	if r.count("down") != 2 || r.all()[1].IncidentID == firstID {
		t.Fatal("old local receipt suppressed post-healthy evidence", r.all())
	}
	for _, i := range n.episodes {
		if len(n.forEpisode(i)) != 1 {
			t.Fatal("separate unproven branches borrowed receipts")
		}
	}
	pages := causalCapacityPages(t, n)
	for _, page := range pages {
		for _, fact := range page.IncidentFacts {
			b, _ := json.Marshal(fact)
			if len(b) > 16384 {
				t.Fatal("uncertainty fact not independently bounded", len(b))
			}
		}
	}
	before := contentID([]any{n.incidentSources, n.pendingIncidents, n.incidentFacts, n.evidenceLinks, n.episodes, n.receipts})
	if _, err = n.store.db.Exec(`CREATE TRIGGER uncertainty_index_failure BEFORE INSERT ON incident_fact_index BEGIN SELECT RAISE(FAIL,'injected uncertainty index failure'); END`); err != nil {
		t.Fatal(err)
	}
	staged := n.episodes[r.all()[1].IncidentID]
	staged.EvidenceRoot = ""
	staged.Runs = map[string][]int64{"a": {77}}
	page := Snapshot{Incidents: []Incident{staged}}
	if err = n.applySnapshot(page); err == nil {
		t.Fatal("interrupted fact transaction was acknowledged")
	}
	if contentID([]any{n.incidentSources, n.pendingIncidents, n.incidentFacts, n.evidenceLinks, n.episodes, n.receipts}) != before {
		t.Fatal("failed branch write published authority")
	}
	if _, err = n.store.db.Exec(`DROP TRIGGER uncertainty_index_failure`); err != nil {
		t.Fatal(err)
	}
	if err = n.applySnapshot(page); err != nil {
		t.Fatal("identical interrupted update did not retry", err)
	}
	if err = n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = cr2New(t, o)
	if len(n.pendingIncidents) != 40 {
		t.Fatal("restart lost opaque headers")
	}
	_ = n.Stop()
}

func TestPartitionProtocolSixRejectedBeforeAnyMerge(t *testing.T) {
	n := cr2New(t, cr2Options(t, false))
	m := Monitor{MonitorInput: cr2Input(t, []string{"a", "b"}), ID: "old-six-monitor", Revision: "old-six-revision", UpdatedAt: n.now()}
	page := Snapshot{Versions: []Version{{Monitor: m, Ancestors: []string{}}}}
	var raw map[string]any
	if err := json.Unmarshal(cr2Body(t, page), &raw); err != nil {
		t.Fatal(err)
	}
	raw["protocol"] = 6
	body, _ := json.Marshal(raw)
	if status := cr2Post(t, n, body); status != http.StatusBadRequest {
		t.Fatal("protocol6 was acknowledged", status)
	}
	if len(n.heads)+len(n.revisionEdges)+len(n.incidentFacts)+len(n.episodes) != 0 {
		t.Fatal("protocol6 merged state before rejection")
	}
}

func TestPartitionMetadataSemanticEditsAndStaleLaterHeader(t *testing.T) {
	var now atomic.Int64
	now.Store(maintenanceClock(12, 0))
	var failing atomic.Bool
	o := cr2Options(t, true)
	o.Now = now.Load
	o.Probe = func(context.Context, Monitor) ProbeResult {
		if failing.Load() {
			return ProbeResult{State: "down", Message: "down"}
		}
		return ProbeResult{State: "up", Message: "up"}
	}
	r := webhook(t)
	n := cr2New(t, o)
	in := input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL)
	m, err := n.writeMonitor(in, nil, "edit-partition", false)
	if err != nil {
		t.Fatal(err)
	}
	partitionPeerUp(t, n, m, 2)
	partitionDrive(t, n)
	header := Incident{ID: "unavailable-old", Aliases: []string{"unavailable-old"}, MonitorID: m.ID, MonitorName: m.Name, Revision: checkGeneration(m), OpenedAt: now.Load() - 10000, Runs: map[string][]int64{"b": {1}}, Predecessors: []string{}, EvidenceRoot: digest("unavailable-old")}
	cr2Apply(t, n, Snapshot{Incidents: []Incident{header}})
	failing.Store(true)
	now.Add(1000)
	partitionDrive(t, n)
	if r.count("down") != 1 {
		t.Fatal(r.all())
	}
	first := r.all()[0].IncidentID
	generation := checkGeneration(m)
	in.Name = "Metadata while interrupted"
	m, err = n.writeMonitor(in, n.heads[m.ID], m.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	now.Add(1000)
	partitionDrive(t, n)
	if checkGeneration(m) != generation || r.count("down") != 1 {
		t.Fatal("metadata edit reset an independently observed outage", r.all())
	}
	in.Target = "http://127.0.0.1:2"
	m, err = n.writeMonitor(in, n.heads[m.ID], m.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	now.Add(1000)
	partitionDrive(t, n)
	if checkGeneration(m) == generation || r.count("down") != 2 || r.count("recovery") != 0 || n.episodes[first].RetiredBy != m.Revision {
		t.Fatal("semantic change failed generation isolation", r.all())
	}
	// A delayed header for the retired generation cannot stop or relabel the
	// current generation, including after a latest-database restart.
	header.EvidenceRoot = digest("later-stale-old-state")
	cr2Apply(t, n, Snapshot{Incidents: []Incident{header}})
	if err = n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = cr2New(t, o)
	now.Add(1000)
	partitionDrive(t, n)
	if n.current(m.ID) == nil || r.count("down") != 2 || r.count("recovery") != 0 {
		t.Fatal("stale later header crossed generation/restart boundary", r.all())
	}
	_ = n.Stop()
}

func TestPartitionUncertaintyStillHonorsMaintenanceAndFreshAllUp(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprint(complete), func(t *testing.T) {
			var now atomic.Int64
			now.Store(maintenanceClock(12, 1))
			var failing atomic.Bool
			failing.Store(true)
			o := cr2Options(t, false)
			o.Now = now.Load
			o.Probe = func(context.Context, Monitor) ProbeResult {
				if failing.Load() {
					return ProbeResult{State: "down", Message: "down"}
				}
				return ProbeResult{State: "up", Message: "up"}
			}
			r := webhook(t)
			n := cr2New(t, o)
			m, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL), nil, "held-partition", false)
			if err != nil {
				t.Fatal(err)
			}
			maintenanceReviewWork(t, n, plannedWork())
			header := Incident{ID: "held-opaque-old", Aliases: []string{"held-opaque-old"}, MonitorID: m.ID, MonitorName: m.Name, Revision: checkGeneration(m), OpenedAt: now.Load() - 10000, Runs: map[string][]int64{"b": {1}}, Predecessors: []string{}, EvidenceRoot: digest("held-opaque-old")}
			cr2Apply(t, n, Snapshot{Incidents: []Incident{header}})
			partitionPeerUp(t, n, m, 2)
			partitionDrive(t, n)
			if r.count("down") != 0 || len(n.episodes) != 1 {
				t.Fatal("fresh branch escaped maintenance or lost observations", r.all())
			}
			if complete {
				failing.Store(false)
				now.Store(maintenanceClock(12, 2))
				partitionPeerUp(t, n, m, 3)
				partitionDrive(t, n)
			}
			now.Store(maintenanceClock(12, 11))
			partitionPeerUp(t, n, m, 4)
			partitionDrive(t, n)
			if complete {
				if r.count("") != 0 {
					t.Fatal("completed held local branch replayed from opaque history", r.all())
				}
			} else if r.count("down") != 1 {
				t.Fatal("held ongoing local branch failed to deliver after release", r.all())
			}
		})
	}
}

func TestPartitionOpaqueHeaderCannotCaptureExistingSourceAudience(t *testing.T) {
	n := cr2New(t, cr2Options(t, false))
	i := Incident{ID: "complete-source", Aliases: []string{"complete-source"}, MonitorID: "audience-monitor", MonitorName: "Audience", Revision: "generation", OpenedAt: n.now(), Runs: map[string][]int64{"a": {1}}, Predecessors: []string{}}
	n.saveEpisode(i)
	pending := boundedIncident(n.episodes[i.ID])
	pending.EvidenceRoot = digest("missing-newer-source-facts")
	pending.DeliveryTargets = []string{"uncertified-route"}
	cr2Apply(t, n, Snapshot{Incidents: []Incident{pending}})
	if n.hasDeliveryIntent(n.episodes[i.ID], "uncertified-route") {
		t.Fatal("opaque summary granted historical audience authority")
	}
}

func TestPartitionOpaqueParentHistoryCannotAuthorizeChildSuppression(t *testing.T) {
	var now atomic.Int64
	now.Store(maintenanceClock(12, 0))
	o := cr2Options(t, false)
	o.Now = now.Load
	n := cr2New(t, o)
	failed := webhook(t)
	failed.status.Store(503)
	childHook := webhook(t)
	p, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", failed.URL), nil, "partition-parent", false)
	if err != nil {
		t.Fatal(err)
	}
	g, err := n.writeGroup(GroupInput{Name: "Dependency", NodeIDs: []string{"a", "b"}, OutageMonitorID: p.ID}, nil, "partition-group", false)
	if err != nil {
		t.Fatal(err)
	}
	in := input([]string{"a", "b"}, "http://127.0.0.1:1", childHook.URL)
	in.GroupID = g.ID
	c, err := n.writeMonitor(in, nil, "partition-child", false)
	if err != nil {
		t.Fatal(err)
	}
	bridge := Incident{ID: "older-parent-bridge", Aliases: []string{"older-parent-bridge"}, MonitorID: p.ID, MonitorName: p.Name, Revision: checkGeneration(p), OpenedAt: now.Load() - 10000, Runs: map[string][]int64{"a": {1, 3}, "b": {1}}, Predecessors: []string{}}
	receipt := Receipt{NotificationView: NotificationView{ID: "old-parent-success", MonitorID: p.ID, IncidentID: bridge.ID, Kind: "down", NodeID: "b", SentAt: bridge.OpenedAt + 100}}
	cr2Apply(t, n, Snapshot{Incidents: []Incident{bridge}, Receipts: []Receipt{receipt}})
	boundary := bridge
	boundary.ID, boundary.Aliases = "missing-parent-closure", []string{"missing-parent-closure"}
	boundary.Runs = map[string][]int64{"a": {1}}
	boundary.Closed, boundary.ResolvedAt = map[string]int64{"a": 2, "b": 2}, Ptr(now.Load()-5000)
	boundary.EvidenceRoot = digest("missing-parent-boundary")
	cr2Apply(t, n, Snapshot{Incidents: []Incident{boundary}})
	for _, m := range []Monitor{p, c} {
		n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: now.Load(), Message: "new run"}, Sequence: 3, DownSince: Ptr(int64(3)), Revision: m.Revision}
		partitionPeerUp(t, n, m, 4)
	}
	// evaluate schedules real sender goroutines and requires the same lock as
	// production's tick/merge callers, including while visiting later monitors.
	n.mu.Lock()
	n.evaluate()
	n.mu.Unlock()
	n.wg.Wait()
	if childHook.count("down") != 1 {
		t.Fatal("old parent receipt extended child suppression without boundary proof", childHook.all())
	}
}

func TestPartitionBriefLocalDownKeepsOwedIntentWithoutInventingRecovery(t *testing.T) {
	var now atomic.Int64
	now.Store(maintenanceClock(12, 0))
	var failing atomic.Bool
	o := cr2Options(t, false)
	o.Now = now.Load
	o.Probe = func(context.Context, Monitor) ProbeResult {
		if failing.Load() {
			return ProbeResult{State: "down", Message: "down"}
		}
		return ProbeResult{State: "up", Message: "up"}
	}
	r := webhook(t)
	n := cr2New(t, o)
	m, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL), nil, "brief-partition", false)
	if err != nil {
		t.Fatal(err)
	}
	partitionDrive(t, n)
	header := Incident{ID: "brief-opaque", Aliases: []string{"brief-opaque"}, MonitorID: m.ID, MonitorName: m.Name, Revision: checkGeneration(m), OpenedAt: now.Load() - 10000, Runs: map[string][]int64{"b": {1}}, Predecessors: []string{}, EvidenceRoot: digest("brief-opaque")}
	cr2Apply(t, n, Snapshot{Incidents: []Incident{header}})
	for range cap(n.notificationWorkers) {
		n.notificationWorkers <- struct{}{}
	}
	failing.Store(true)
	now.Add(1000)
	partitionDrive(t, n)
	failing.Store(false)
	now.Add(1000)
	partitionDrive(t, n)
	for range cap(n.notificationWorkers) {
		<-n.notificationWorkers
	}
	n.evaluate()
	n.wg.Wait()
	if r.count("down") != 1 || r.count("recovery") != 0 {
		t.Fatal("brief local outage lost its own intent or invented global recovery", r.all())
	}
	for _, i := range n.episodes {
		if i.Closed != nil {
			t.Fatal("unknown assigned peer authorized a global closure")
		}
	}
}

func TestPartitionNestedUncertaintyProofAcrossRestartAndPageOrders(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			o := cr2Options(t, true)
			n := cr2New(t, o)
			old := Incident{ID: "nested-old", Aliases: []string{"nested-old"}, MonitorID: "nested-monitor", MonitorName: "Nested", Revision: "generation", OpenedAt: 10, Runs: map[string][]int64{"a": {1}}, Closed: map[string]int64{"a": 2, "b": 2}, ResolvedAt: Ptr(int64(20)), Predecessors: []string{}}
			header, oldFacts, oldLinks := partitionEvidence(old)
			branch := Incident{ID: "nested-branch", Aliases: []string{"nested-branch"}, MonitorID: old.MonitorID, MonitorName: old.MonitorName, Revision: old.Revision, OpenedAt: 30, Runs: map[string][]int64{"a": {3}}, Predecessors: []string{}, uncertainties: []incidentUncertainty{{Header: uncertainHeader(header), ObserverID: "a", NodeID: "a", Run: 3}}}
			branchHeader, branchFacts, branchLinks := partitionEvidence(branch)
			next := Incident{ID: "nested-next", Aliases: []string{"nested-next"}, MonitorID: old.MonitorID, MonitorName: old.MonitorName, Revision: old.Revision, OpenedAt: 35, Runs: map[string][]int64{"a": {3}}, Predecessors: []string{}, uncertainties: []incidentUncertainty{{Header: uncertainHeader(branchHeader), ObserverID: "b", NodeID: "a", Run: 3}}}
			nextHeader, nextFacts, nextLinks := partitionEvidence(next)
			cr2Apply(t, n, Snapshot{Incidents: []Incident{header, branchHeader, nextHeader}})
			partitionComplete(t, n, append(branchFacts, nextFacts...), append(branchLinks, nextLinks...), reverse)
			if len(n.episodes) != 2 || len(n.pendingIncidents) != 1 {
				t.Fatal("nested reference granted authority before its older boundary", n.episodes)
			}
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			if len(n.episodes) != 2 || len(n.pendingIncidents) != 1 {
				t.Fatal("restart lost nested proof boundary")
			}
			if reverse {
				slices.Reverse(oldFacts)
				slices.Reverse(oldLinks)
			}
			pages := []Snapshot{}
			for _, fact := range oldFacts {
				pages = append(pages, Snapshot{IncidentFacts: []IncidentFact{fact}})
			}
			for _, link := range oldLinks {
				pages = append(pages, Snapshot{EvidenceLinks: []EvidenceLink{link}})
			}
			if reverse {
				slices.Reverse(pages)
			}
			for index, page := range pages {
				cr2Apply(t, n, page)
				if index < len(pages)-1 && len(n.episodes) != 2 {
					t.Fatal("partial older proof joined nested sources", index)
				}
			}
			if len(n.pendingIncidents) != 0 || len(n.episodes) != 2 {
				t.Fatal("complete nested proof failed to converge", n.episodes)
			}
			found := false
			for _, episode := range n.episodes {
				if slices.Contains(episodeIDs(episode), branch.ID) {
					found = slices.Contains(episodeIDs(episode), next.ID) && !slices.Contains(episodeIDs(episode), old.ID)
				}
			}
			if !found {
				t.Fatal("positive continuity did not join the two later sources", n.episodes)
			}
			peer := cr2New(t, cr2Options(t, false))
			pages = causalCapacityPages(t, n)
			if reverse {
				slices.Reverse(pages)
			}
			for _, page := range pages {
				cr2Apply(t, peer, page)
			}
			if len(peer.episodes) != len(n.episodes) || len(peer.incidentFacts) != len(n.incidentFacts) || len(peer.pendingIncidents) != 0 {
				t.Fatal("nested projected fact set failed a full peer sweep")
			}
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
