package canary

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// Independent protocol-7 review: run against the immutable candidate in a
// private copy. Every node/database/receiver belongs to this test; nodes do not
// Start and probes do not access external targets.
func pcrOpaque(t *testing.T, n *Node, m Monitor, id string) (Incident, []IncidentFact, []EvidenceLink) {
	t.Helper()
	i := Incident{ID: id, Aliases: []string{id}, MonitorID: m.ID, MonitorName: m.Name,
		Revision: checkGeneration(m), OpenedAt: n.now() - 10000,
		Runs: map[string][]int64{"b": {1}}, Predecessors: []string{}}
	return partitionEvidence(i)
}

func pcrHoldWorkers(n *Node) func() {
	for range cap(n.notificationWorkers) {
		n.notificationWorkers <- struct{}{}
	}
	return func() {
		for range cap(n.notificationWorkers) {
			<-n.notificationWorkers
		}
	}
}

func pcrOnlyEpisode(t *testing.T, n *Node) Incident {
	t.Helper()
	if len(n.episodes) != 1 {
		t.Fatalf("expected one complete local source, got %d", len(n.episodes))
	}
	for _, i := range n.episodes {
		return i
	}
	panic("unreachable")
}

// An owed brief outage must survive beyond the one latest UP sample. Pending
// results and a later independent outage cannot erase already captured intent.
func TestPartitionCausalReviewOwedBriefDownSurvivesWitnessReplacement(t *testing.T) {
	for _, nextState := range []string{"pending", "down"} {
		t.Run(nextState, func(t *testing.T) {
			var now atomic.Int64
			now.Store(maintenanceClock(12, 0))
			var state atomic.Value
			state.Store("up")
			r := webhook(t)
			o := cr2Options(t, true)
			o.Now = now.Load
			o.Probe = func(context.Context, Monitor) ProbeResult {
				return ProbeResult{State: state.Load().(string), Message: "controlled local result"}
			}
			n := cr2New(t, o)
			m, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL), nil, "pcr-owed-replaced", false)
			if err != nil {
				t.Fatal(err)
			}
			partitionDrive(t, n)
			header, _, _ := pcrOpaque(t, n, m, "pcr-unavailable")
			cr2Apply(t, n, Snapshot{Incidents: []Incident{header}})
			release := pcrHoldWorkers(n)
			state.Store("down")
			now.Add(1000)
			partitionDrive(t, n)
			brief := pcrOnlyEpisode(t, n)
			if !n.hasDeliveryIntent(brief, "") {
				t.Fatal("brief outage did not capture a durable intent")
			}
			state.Store("up")
			now.Add(1000)
			partitionDrive(t, n)
			state.Store(nextState)
			now.Add(1000)
			partitionDrive(t, n)
			release()
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			for range 3 {
				now.Add(60 * 60 * 1000)
				partitionDrive(t, n)
			}
			if r.count("recovery") != 0 || r.count("repeat") != 0 {
				t.Fatal("unknown peer or replaced local witness invented follow-up", r.all())
			}
			gotBrief := 0
			for _, body := range r.all() {
				if body.Kind == "down" && body.IncidentID == brief.ID {
					gotBrief++
				}
			}
			history, err := n.store.history(m.ID, 100)
			if err != nil || len(history) != 7 {
				t.Fatalf("real probe history lost: count=%d err=%v", len(history), err)
			}
			if gotBrief != 1 {
				t.Fatalf("captured brief down lost after %s replaced its UP witness: want one for %s, got %d; intent=%v sends=%v", nextState, brief.ID, gotBrief, n.hasDeliveryIntent(n.episodes[brief.ID], ""), r.all())
			}
		})
	}
}

// Opaque history received after a complete locally observed brief outage must
// not revoke that outage's existing authority and permanently lose its intent.
func TestPartitionCausalReviewLateOpaqueHeaderPreservesAlreadyOwedDown(t *testing.T) {
	for _, opaque := range []bool{false, true} {
		t.Run(fmt.Sprintf("opaque=%v", opaque), func(t *testing.T) {
			var now atomic.Int64
			now.Store(maintenanceClock(12, 0))
			var state atomic.Value
			state.Store("up")
			r := webhook(t)
			o := cr2Options(t, true)
			o.Now = now.Load
			o.Probe = func(context.Context, Monitor) ProbeResult {
				return ProbeResult{State: state.Load().(string), Message: "controlled local result"}
			}
			n := cr2New(t, o)
			m, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL), nil, "pcr-late-header", false)
			if err != nil {
				t.Fatal(err)
			}
			partitionPeerUp(t, n, m, 2)
			partitionDrive(t, n)
			release := pcrHoldWorkers(n)
			state.Store("down")
			now.Add(1000)
			partitionDrive(t, n)
			brief := pcrOnlyEpisode(t, n)
			state.Store("up")
			now.Add(1000)
			partitionDrive(t, n)
			if !n.hasDeliveryIntent(brief, "") || n.episodes[brief.ID].Closed == nil {
				t.Fatal("fixture lost its owed intent or failed to record proven global closure")
			}
			if opaque {
				header, _, _ := pcrOpaque(t, n, m, "pcr-late-unavailable")
				cr2Apply(t, n, Snapshot{Incidents: []Incident{header}})
			}
			release()
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			for range 3 {
				now.Add(60 * 60 * 1000)
				partitionDrive(t, n)
			}
			if r.count("down") != 1 || r.count("recovery") != 0 {
				t.Fatalf("late opaque history revoked an already owed local down: intent=%v sends=%v", n.hasDeliveryIntent(n.episodes[brief.ID], ""), r.all())
			}
		})
	}
}

// A proof page can carry a foreign witness or observer, but it must never
// authorize delivery from this observer or leak a successful old receipt.
func TestPartitionCausalReviewObserverAndRunScope(t *testing.T) {
	for _, observer := range []string{"a", "b"} {
		for _, witness := range []int64{1, 3} {
			t.Run(fmt.Sprintf("observer=%s/run=%d", observer, witness), func(t *testing.T) {
				var now atomic.Int64
				now.Store(maintenanceClock(12, 0))
				n := cr2New(t, cr2Options(t, false))
				n.now = now.Load
				r := webhook(t)
				m, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL), nil, "pcr-witness", false)
				if err != nil {
					t.Fatal(err)
				}
				header, _, _ := pcrOpaque(t, n, m, "pcr-scope-opaque")
				remote := Incident{ID: "pcr-other-branch", Aliases: []string{"pcr-other-branch"}, MonitorID: m.ID, MonitorName: m.Name,
					Revision: checkGeneration(m), OpenedAt: now.Load() - 1000,
					Runs: map[string][]int64{"a": {witness}}, Predecessors: []string{},
					uncertainties: []incidentUncertainty{{Header: uncertainHeader(header), ObserverID: observer, NodeID: "a", Run: witness}}}
				rh, facts, links := partitionEvidence(remote)
				cr2Apply(t, n, Snapshot{Incidents: []Incident{header, rh}, Receipts: []Receipt{{NotificationView: NotificationView{
					ID: "pcr-foreign-success", MonitorID: m.ID, IncidentID: remote.ID, Kind: "down", NodeID: "b", SentAt: now.Load() - 500,
				}}}})
				partitionComplete(t, n, facts, links, false)
				n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: now.Load()}, Revision: m.Revision, Sequence: 3, DownSince: Ptr(int64(3))}
				n.evaluate()
				n.wg.Wait()
				if observer == "a" && witness == 3 {
					if len(n.episodes) != 1 || r.count("down") != 0 {
						t.Fatal("matching observer/run branch lost its receipt", r.all())
					}
					return
				}
				if r.count("down") != 1 {
					t.Fatal("unrelated observer/run suppressed independently owed down", r.all())
				}
				for _, i := range n.episodes {
					if slices.Contains(episodeIDs(i), r.all()[0].IncidentID) && hasReceipt(n.forEpisode(i), "down") && len(n.forEpisode(i)) != 1 {
						t.Fatal("foreign success leaked to local branch")
					}
				}
			})
		}
	}
}

// Each observer has one exact run from the same complete ongoing boundary.
// The boundary itself contains both witnesses, proving continuity transitively.
func TestPartitionCausalReviewMixedObserverContinuityConverges(t *testing.T) {
	for _, lifetime := range []string{"ongoing", "completed", "later"} {
		t.Run(lifetime, func(t *testing.T) {
			n := cr2New(t, cr2Options(t, false))
			old := Incident{ID: "pcr-mixed-old", Aliases: []string{"pcr-mixed-old"}, MonitorID: "pcr-mixed-monitor", MonitorName: "Mixed observers",
				Revision: "mixed-generation", OpenedAt: 10, Runs: map[string][]int64{"a": {3}, "b": {3}}, Predecessors: []string{}}
			if lifetime != "ongoing" {
				old.Closed, old.ResolvedAt = map[string]int64{"a": 4, "b": 4}, Ptr(int64(20))
			}
			header, facts, links := partitionEvidence(old)
			cr2Apply(t, n, Snapshot{Incidents: []Incident{header}})
			for _, observer := range []string{"a", "b"} {
				id := "pcr-mixed-branch-" + observer
				run, opened := int64(3), int64(15)
				if lifetime == "later" {
					run, opened = 5, 30
				}
				branch := Incident{ID: id, Aliases: []string{id}, MonitorID: old.MonitorID, MonitorName: old.MonitorName, Revision: old.Revision,
					OpenedAt: opened, Runs: map[string][]int64{observer: {run}}, Predecessors: []string{},
					uncertainties: []incidentUncertainty{{Header: uncertainHeader(header), ObserverID: observer, NodeID: observer, Run: run}}}
				bh, bf, bl := partitionEvidence(branch)
				cr2Apply(t, n, Snapshot{Incidents: []Incident{bh}, IncidentFacts: bf, EvidenceLinks: bl})
			}
			if len(n.episodes) != 2 {
				t.Fatal("opaque proof granted authority before completion")
			}
			partitionComplete(t, n, facts, links, true)
			want := 1
			if lifetime == "later" {
				want = 2
			}
			for range 3 {
				cr2Apply(t, n, Snapshot{Incidents: []Incident{header}, IncidentFacts: facts, EvidenceLinks: links})
			}
			if len(n.episodes) != want {
				groups := [][]string{}
				for _, i := range n.episodes {
					groups = append(groups, episodeIDs(i))
				}
				t.Fatalf("complete %s proof failed mixed-observer convergence after duplicate replays: want %d lifetime(s), got %v", lifetime, want, groups)
			}
		})
	}
}

// Split all facts/links into independent pages, withhold one exact old run,
// restart in the middle, and duplicate every page. Mixed direct/nested boundary
// references must preserve old receipts and separate a completed later outage.
func TestPartitionCausalReviewMixedNestedPageSchedules(t *testing.T) {
	for seed := int64(0); seed < 8; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			o := cr2Options(t, true)
			n := cr2New(t, o)
			old := Incident{ID: "pcr-order-old", Aliases: []string{"pcr-order-old"}, MonitorID: "pcr-order-monitor", MonitorName: "Nested page order", Revision: "order-generation",
				OpenedAt: 10, Runs: map[string][]int64{"a": {1}, "b": {1}}, Closed: map[string]int64{"a": 2, "b": 2}, ResolvedAt: Ptr(int64(20)), Predecessors: []string{}}
			oh, of, ol := partitionEvidence(old)
			a := Incident{ID: "pcr-order-a", Aliases: []string{"pcr-order-a"}, MonitorID: old.MonitorID, MonitorName: old.MonitorName, Revision: old.Revision,
				OpenedAt: 30, Runs: map[string][]int64{"a": {3}}, Predecessors: []string{},
				uncertainties: []incidentUncertainty{{Header: uncertainHeader(oh), ObserverID: "a", NodeID: "a", Run: 3}}}
			ah, af, al := partitionEvidence(a)
			b := a
			b.ID, b.Aliases, b.OpenedAt = "pcr-order-b", []string{"pcr-order-b"}, 32
			b.uncertainties = []incidentUncertainty{{Header: uncertainHeader(ah), ObserverID: "b", NodeID: "a", Run: 3}}
			bh, bf, bl := partitionEvidence(b)
			c := a
			c.ID, c.Aliases, c.OpenedAt = "pcr-order-c", []string{"pcr-order-c"}, 34
			c.uncertainties = []incidentUncertainty{
				{Header: uncertainHeader(oh), ObserverID: "b", NodeID: "a", Run: 3},
				{Header: uncertainHeader(ah), ObserverID: "b", NodeID: "a", Run: 3},
			}
			ch, cf, cl := partitionEvidence(c)
			closedA := ah
			closedA.Closed, closedA.ResolvedAt = map[string]int64{"a": 4, "b": 4}, Ptr(int64(40))
			later := Incident{ID: "pcr-order-later", Aliases: []string{"pcr-order-later"}, MonitorID: old.MonitorID, MonitorName: old.MonitorName, Revision: old.Revision,
				OpenedAt: 50, Runs: map[string][]int64{"a": {5}}, Closed: map[string]int64{"a": 6, "b": 6}, ResolvedAt: Ptr(int64(60)), Predecessors: []string{a.ID, b.ID, c.ID}}
			lh, lf, ll := partitionEvidence(later)
			receipt := Receipt{NotificationView: NotificationView{ID: "pcr-order-old-success", MonitorID: old.MonitorID, IncidentID: old.ID, Kind: "down", NodeID: "b", SentAt: 11}}
			cr2Apply(t, n, Snapshot{Incidents: []Incident{oh, ah, bh, ch, lh, closedA}, Receipts: []Receipt{receipt}})
			pages := []Snapshot{}
			var held IncidentFact
			for _, facts := range [][]IncidentFact{of, af, bf, cf, lf} {
				for _, fact := range facts {
					if fact.IncidentID == old.ID && fact.Kind == "run" && fact.NodeID == "b" {
						held = fact
						continue
					}
					pages = append(pages, Snapshot{IncidentFacts: []IncidentFact{fact}})
				}
			}
			for _, links := range [][]EvidenceLink{ol, al, bl, cl, ll} {
				for _, link := range links {
					pages = append(pages, Snapshot{EvidenceLinks: []EvidenceLink{link}})
				}
			}
			rand.New(rand.NewSource(seed)).Shuffle(len(pages), func(i, j int) { pages[i], pages[j] = pages[j], pages[i] })
			for index, page := range pages {
				for range 2 {
					cr2Apply(t, n, page)
				}
				if index == len(pages)/2 {
					if err := n.Stop(); err != nil {
						t.Fatal(err)
					}
					n = cr2New(t, o)
				}
				for _, episode := range n.episodes {
					if len(episodeIDs(episode)) != 1 || len(n.forEpisode(episode)) != 0 {
						t.Fatal("partial historical proof joined sources or transferred receipt", index, episode.ID, episodeIDs(episode))
					}
				}
			}
			if held.ID == "" {
				t.Fatal("fixture failed to withhold exact historical run")
			}
			cr2Apply(t, n, Snapshot{IncidentFacts: []IncidentFact{held}})
			if len(n.pendingIncidents) != 0 || len(n.episodes) != 3 {
				t.Fatalf("completed mixed proof failed to converge: pending=%d episodes=%d", len(n.pendingIncidents), len(n.episodes))
			}
			found := false
			for _, i := range n.episodes {
				if slices.Contains(episodeIDs(i), a.ID) {
					found = slices.Contains(episodeIDs(i), b.ID) && slices.Contains(episodeIDs(i), c.ID) && !slices.Contains(episodeIDs(i), old.ID) && !slices.Contains(episodeIDs(i), later.ID) && i.Closed != nil && len(n.forEpisode(i)) == 0
				}
			}
			if !found {
				t.Fatal("mixed direct/nested sources lost their completed lifetime or borrowed an old receipt")
			}
			peer := cr2New(t, cr2Options(t, false))
			wirePages := causalCapacityPages(t, n)
			slices.Reverse(wirePages)
			for _, page := range wirePages {
				cr2Apply(t, peer, page)
			}
			if len(peer.episodes) != 3 || len(peer.pendingIncidents) != 0 || len(peer.incidentFacts) != len(n.incidentFacts) {
				t.Fatal("producer's nested proof failed a complete reverse peer sweep")
			}
		})
	}
}

func pcrSweep(t *testing.T, from, to *Node) {
	t.Helper()
	for _, page := range causalCapacityPages(t, from) {
		cr2Apply(t, to, page)
	}
}

// The mixed-observer convergence failure has visible connected behavior: the
// second observer repeats after receiving the first observer's current receipt.
func TestPartitionCausalReviewMixedObserverConnectedRepeatDedup(t *testing.T) {
	var now atomic.Int64
	now.Store(maintenanceClock(12, 0))
	r := webhook(t)
	makeNode := func(id, peer string) *Node {
		o := cr2Options(t, true)
		o.NodeID, o.Peers = id, []Peer{{ID: peer, URL: "http://127.0.0.1:1"}}
		o.Now = now.Load
		o.Probe = func(context.Context, Monitor) ProbeResult {
			return ProbeResult{State: "down", Message: "continuous local failure"}
		}
		return cr2New(t, o)
	}
	a, b := makeNode("a", "b"), makeNode("b", "a")
	in := input([]string{"a", "b"}, "http://127.0.0.1:1/", r.URL)
	in.RepeatAfterMs = Ptr(int64(1000))
	if err := validateMonitor(&in, a.known); err != nil {
		t.Fatal(err)
	}
	m, err := a.writeMonitor(in, nil, "pcr-runtime-mixed", false)
	if err != nil {
		t.Fatal(err)
	}
	pcrSweep(t, a, b)
	old := Incident{ID: "pcr-runtime-old", Aliases: []string{"pcr-runtime-old"}, MonitorID: m.ID, MonitorName: m.Name, Revision: checkGeneration(m), OpenedAt: now.Load() - 10000,
		Runs: map[string][]int64{"a": {1}, "b": {1}}, Predecessors: []string{}}
	header, facts, links := partitionEvidence(old)
	for _, n := range []*Node{a, b} {
		cr2Apply(t, n, Snapshot{Incidents: []Incident{header}})
		partitionDrive(t, n)
	}
	// Reach the nonpreferred sender's configured stagger without allowing a
	// repeat from the first observer before the partition has reconciled.
	now.Add(60000)
	partitionDrive(t, b)
	if r.count("down") != 2 {
		t.Fatal("independent partition branches did not each deliver", r.all())
	}
	releaseA, releaseB := pcrHoldWorkers(a), pcrHoldWorkers(b)
	partitionDrive(t, a)
	pcrSweep(t, a, b)
	pcrSweep(t, b, a)
	for _, n := range []*Node{a, b} {
		partitionComplete(t, n, facts, links, false)
	}
	for range 2 {
		pcrSweep(t, a, b)
		pcrSweep(t, b, a)
	}
	releaseA()
	releaseB()
	now.Add(60000)
	for _, n := range []*Node{a, b} {
		release := pcrHoldWorkers(n)
		partitionDrive(t, n)
		release()
	}
	a.evaluate()
	a.wg.Wait()
	if r.count("repeat") != 1 {
		t.Fatal("first observer failed its owed repeat", r.all())
	}
	pcrSweep(t, a, b)
	b.evaluate()
	b.wg.Wait()
	if r.count("repeat") != 1 {
		t.Fatalf("complete ongoing proof and an acknowledged current receipt did not deduplicate connected repeats: a=%d lifetimes b=%d lifetimes sends=%v", len(a.episodes), len(b.episodes), r.all())
	}
}

// Final proof completion must remain retryable even if SQLite fails after
// facts, indexes, and derived projections have been staged.
func TestPartitionCausalReviewCompletionRollbackAndRetry(t *testing.T) {
	o := cr2Options(t, true)
	n := cr2New(t, o)
	old := Incident{ID: "pcr-rollback-old", Aliases: []string{"pcr-rollback-old"}, MonitorID: "pcr-rollback-monitor", MonitorName: "Rollback", Revision: "rollback-generation",
		OpenedAt: 10, Runs: map[string][]int64{"a": {1}}, Closed: map[string]int64{"a": 2, "b": 2}, ResolvedAt: Ptr(int64(20)), Predecessors: []string{}}
	header, facts, links := partitionEvidence(old)
	branch := Incident{ID: "pcr-rollback-branch", Aliases: []string{"pcr-rollback-branch"}, MonitorID: old.MonitorID, MonitorName: old.MonitorName, Revision: old.Revision,
		OpenedAt: 30, Runs: map[string][]int64{"a": {3}}, Predecessors: []string{},
		uncertainties: []incidentUncertainty{{Header: uncertainHeader(header), ObserverID: "a", NodeID: "a", Run: 3}}}
	bh, bf, bl := partitionEvidence(branch)
	cr2Apply(t, n, Snapshot{Incidents: []Incident{header, bh}, IncidentFacts: bf, EvidenceLinks: bl})
	page := cr2Decode(t, n, Snapshot{IncidentFacts: facts, EvidenceLinks: links})
	before, inputBefore := cr2State(n), contentID(page)
	if _, err := n.store.db.Exec(`CREATE TRIGGER pcr_completion_fail BEFORE INSERT ON records WHEN NEW.category='incidentSources' BEGIN SELECT RAISE(FAIL,'review completion failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := n.applySnapshot(page); err == nil {
		t.Fatal("failed completion acknowledged")
	}
	if cr2State(n) != before || contentID(page) != inputBefore {
		t.Fatal("failed completion changed authority or caller retry page")
	}
	if _, err := n.store.db.Exec(`DROP TRIGGER pcr_completion_fail`); err != nil {
		t.Fatal(err)
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = cr2New(t, o)
	if len(n.pendingIncidents) != 1 || len(n.episodes) != 1 {
		t.Fatal("failed proof completion survived as authority after reopen")
	}
	if err := n.applySnapshot(page); err != nil {
		t.Fatal("identical proof completion could not retry after restart", err)
	}
	if len(n.pendingIncidents) != 0 || len(n.episodes) != 2 {
		t.Fatal("complete proof did not retain separate old and new lifetimes")
	}
	cr2Apply(t, n, page)
	peer := cr2New(t, cr2Options(t, false))
	pcrSweep(t, n, peer)
	if len(peer.episodes) != 2 || len(peer.incidentFacts) != len(n.incidentFacts) {
		t.Fatal("successful retry did not reproduce at a peer")
	}
}

// Only the supported restore command is tested. Raw byte rollback under the
// same identity remains outside this protocol's guarantees.
func TestPartitionCausalReviewPreparedRestoreRemainsQuarantined(t *testing.T) {
	o := cr2Options(t, true)
	n := cr2New(t, o)
	restored := filepath.Join(t.TempDir(), "restored.db")
	if _, err := PrepareDatabaseRestore(context.Background(), o.DBPath, restored); err != nil {
		t.Fatal(err)
	}
	o.DBPath = restored
	reopened, err := New(o)
	if reopened != nil {
		_ = reopened.Stop()
	}
	if err == nil || !strings.Contains(err.Error(), "re-enrollment") {
		t.Fatal("prepared restore resumed the old node identity", err)
	}
	if n.Error() != nil {
		t.Fatal("private restore altered source runtime", n.Error())
	}
}

// A boundary header can arrive only inside a committed uncertainty fact. Its
// separately paged proof must invalidate the projection even when there is no
// standalone pending Incident row to trigger the usual completion path.
func TestPartitionCausalReviewEmbeddedBoundaryCompletionReprojects(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			o := cr2Options(t, true)
			n := cr2New(t, o)
			old := Incident{ID: "pcr-embedded-old", Aliases: []string{"pcr-embedded-old"}, MonitorID: "pcr-embedded-monitor", MonitorName: "Embedded boundary", Revision: "embedded-generation",
				OpenedAt: 10, Runs: map[string][]int64{"a": {1}}, Closed: map[string]int64{"a": 2, "b": 2}, ResolvedAt: Ptr(int64(20)), Predecessors: []string{}}
			header, facts, links := partitionEvidence(old)
			for _, observer := range []string{"a", "b"} {
				id := "pcr-embedded-branch-" + observer
				branch := Incident{ID: id, Aliases: []string{id}, MonitorID: old.MonitorID, MonitorName: old.MonitorName, Revision: old.Revision,
					OpenedAt: 30, Runs: map[string][]int64{"a": {3}}, Predecessors: []string{},
					uncertainties: []incidentUncertainty{{Header: uncertainHeader(header), ObserverID: observer, NodeID: "a", Run: 3}}}
				bh, bf, bl := partitionEvidence(branch)
				cr2Apply(t, n, Snapshot{Incidents: []Incident{bh}, IncidentFacts: bf, EvidenceLinks: bl})
			}
			if len(n.pendingIncidents) != 0 || len(n.episodes) != 2 {
				t.Fatal("incomplete embedded boundary already granted equivalence")
			}
			pages := []Snapshot{}
			for _, fact := range facts {
				pages = append(pages, Snapshot{IncidentFacts: []IncidentFact{fact}})
			}
			for _, link := range links {
				pages = append(pages, Snapshot{EvidenceLinks: []EvidenceLink{link}})
			}
			if reverse {
				slices.Reverse(pages)
			}
			for _, page := range pages {
				for range 2 {
					cr2Apply(t, n, page)
				}
			}
			if _, complete, err := hydrateIncident(header, n.incidentFacts, n.evidenceLinks); err != nil || !complete {
				t.Fatal("fixture did not finish the exact embedded boundary", complete, err)
			}
			beforeRestart := len(n.episodes)
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			if len(n.episodes) != 1 {
				t.Fatal("restart control failed to recognize complete embedded proof", len(n.episodes))
			}
			if beforeRestart != 1 {
				t.Fatalf("complete embedded proof did not invalidate the live projection: before restart=%d lifetimes, after restart=%d", beforeRestart, len(n.episodes))
			}
		})
	}
}
