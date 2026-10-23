package canary

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPartitionDeliveryRepairContinuityRequiresPositiveEdges(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(fmt.Sprint(completed), func(t *testing.T) {
			n := cr2New(t, cr2Options(t, false))
			old := Incident{ID: "repair-edge-old", Aliases: []string{"repair-edge-old"}, MonitorID: "repair-edge-monitor", MonitorName: "Positive edges", Revision: "repair-edge-generation", OpenedAt: 10, Runs: map[string][]int64{"a": {3}, "b": {3}}, Predecessors: []string{}}
			if completed {
				old.Closed, old.ResolvedAt = map[string]int64{"a": 4, "b": 4}, Ptr(int64(20))
			}
			header, facts, links := partitionEvidence(old)
			cr2Apply(t, n, Snapshot{Incidents: []Incident{header}})
			for _, observer := range []string{"a", "b"} {
				id := "repair-edge-branch-" + observer
				branch := Incident{ID: id, Aliases: []string{id}, MonitorID: old.MonitorID, MonitorName: old.MonitorName, Revision: old.Revision, OpenedAt: 15, Runs: map[string][]int64{observer: {3}}, Predecessors: []string{},
					uncertainties: []incidentUncertainty{{Header: uncertainHeader(header), ObserverID: observer, NodeID: observer, Run: 3}}}
				bh, bf, bl := partitionEvidence(branch)
				cr2Apply(t, n, Snapshot{Incidents: []Incident{bh}, IncidentFacts: bf, EvidenceLinks: bl})
			}
			later := Incident{ID: "repair-edge-unrelated", Aliases: []string{"repair-edge-unrelated"}, MonitorID: old.MonitorID, MonitorName: old.MonitorName, Revision: old.Revision, OpenedAt: 30, Runs: map[string][]int64{"a": {5}}, Predecessors: []string{},
				uncertainties: []incidentUncertainty{{Header: uncertainHeader(header), ObserverID: "a", NodeID: "a", Run: 5}}}
			lh, lf, ll := partitionEvidence(later)
			cr2Apply(t, n, Snapshot{Incidents: []Incident{lh}, IncidentFacts: lf, EvidenceLinks: ll})
			partitionComplete(t, n, facts, links, false)
			if len(n.episodes) != 2 {
				t.Fatal("positive continuity split its branches or swallowed a later run", n.episodes)
			}
			for _, episode := range n.episodes {
				if slices.Contains(episodeIDs(episode), later.ID) && len(episodeIDs(episode)) != 1 {
					t.Fatal("original continuity edge crossed an unproved later branch")
				}
			}
		})
	}
}

func TestPartitionDeliveryRepairContinuityCannotCrossClosedUncertainty(t *testing.T) {
	n := cr2New(t, cr2Options(t, false))
	old := Incident{ID: "repair-closed-edge-old", Aliases: []string{"repair-closed-edge-old"}, MonitorID: "repair-closed-edge-monitor", MonitorName: "Closed edge", Revision: "repair-closed-edge-generation", OpenedAt: 10, Runs: map[string][]int64{"a": {3}, "b": {3}}, Predecessors: []string{}}
	oh, of, ol := partitionEvidence(old)
	boundary := old
	boundary.ID, boundary.Aliases = "repair-closed-edge-boundary", []string{"repair-closed-edge-boundary"}
	boundary.Closed, boundary.ResolvedAt = map[string]int64{"a": 4, "b": 4}, Ptr(int64(20))
	dh, df, dl := partitionEvidence(boundary)
	a := old
	a.ID, a.Aliases, a.OpenedAt = "repair-closed-edge-a", []string{"repair-closed-edge-a"}, 15
	a.Runs = map[string][]int64{"a": {3}}
	a.uncertainties = []incidentUncertainty{
		{Header: uncertainHeader(oh), ObserverID: "a", NodeID: "a", Run: 3},
		{Header: uncertainHeader(dh), ObserverID: "a", NodeID: "a", Run: 3},
	}
	ah, af, al := partitionEvidence(a)
	b := old
	b.ID, b.Aliases, b.OpenedAt = "repair-closed-edge-b", []string{"repair-closed-edge-b"}, 30
	b.Runs = map[string][]int64{"a": {5}, "b": {3}}
	b.uncertainties = []incidentUncertainty{{Header: uncertainHeader(oh), ObserverID: "b", NodeID: "b", Run: 3}}
	bh, bf, bl := partitionEvidence(b)
	// D is complete only as embedded proof. No standalone closed D source
	// supplies a projection closure that could mask a missing phase check.
	cr2Apply(t, n, Snapshot{Incidents: []Incident{oh, ah, bh}, IncidentFacts: append(append(append(of, df...), af...), bf...), EvidenceLinks: append(append(append(ol, dl...), al...), bl...),
		Receipts: []Receipt{{NotificationView: NotificationView{ID: "repair-closed-edge-success", MonitorID: old.MonitorID, IncidentID: old.ID, NodeID: "a", Kind: "down", SentAt: 12}}}})
	if len(n.episodes) != 2 {
		t.Fatal("positive original edge crossed an embedded closure frontier", n.episodes)
	}
	if episode := n.episodes[b.ID]; len(episodeIDs(episode)) != 1 || len(n.forEpisode(episode)) != 0 {
		t.Fatal("mixed later source borrowed the old lifetime or successful receipt", episode)
	}
}

func TestPartitionDeliveryRepairEmbeddedOpenContinuityConverges(t *testing.T) {
	n := cr2New(t, cr2Options(t, false))
	old := Incident{ID: "repair-open-embedded-old", Aliases: []string{"repair-open-embedded-old"}, MonitorID: "repair-open-embedded-monitor", MonitorName: "Embedded open bridge", Revision: "repair-open-embedded-generation", OpenedAt: 10, Runs: map[string][]int64{"a": {3}, "b": {3}}, Predecessors: []string{}}
	header, facts, links := partitionEvidence(old)
	for _, observer := range []string{"a", "b"} {
		id := "repair-open-embedded-branch-" + observer
		branch := Incident{ID: id, Aliases: []string{id}, MonitorID: old.MonitorID, MonitorName: old.MonitorName, Revision: old.Revision, OpenedAt: 15, Runs: map[string][]int64{observer: {3}}, Predecessors: []string{},
			uncertainties: []incidentUncertainty{{Header: uncertainHeader(header), ObserverID: observer, NodeID: observer, Run: 3}}}
		bh, bf, bl := partitionEvidence(branch)
		cr2Apply(t, n, Snapshot{Incidents: []Incident{bh}, IncidentFacts: bf, EvidenceLinks: bl})
	}
	if len(n.pendingIncidents) != 0 || len(n.episodes) != 2 {
		t.Fatal("opaque embedded root granted a continuity bridge")
	}
	partitionComplete(t, n, facts, links, true)
	if len(n.pendingIncidents) != 0 || len(n.episodes) != 1 {
		t.Fatal("complete embedded original failed to bridge exact mixed-observer runs", len(n.episodes))
	}
	for _, episode := range n.episodes {
		if slices.Contains(episodeIDs(episode), old.ID) || len(episodeIDs(episode)) != 2 {
			t.Fatal("proof-only boundary gained alias or receipt identity", episodeIDs(episode))
		}
	}
}

// Capture occurs before the notification delay or worker admission. Probe
// history may expire completely; it is not the durable delivery authority.
func TestPartitionDeliveryRepairOwedDownSurvivesPrunedHistory(t *testing.T) {
	for _, nextState := range []string{"pending", "down"} {
		t.Run(nextState, func(t *testing.T) {
			var now atomic.Int64
			now.Store(maintenanceClock(12, 0))
			var state atomic.Value
			state.Store("up")
			r := webhook(t)
			o := cr2Options(t, true)
			o.Now, o.HistoryLimit, o.HistoryRetention = now.Load, 1, time.Second
			o.NotificationDelayMs = Ptr(int64(5000))
			o.Probe = func(context.Context, Monitor) ProbeResult {
				return ProbeResult{State: state.Load().(string), Message: "controlled result"}
			}
			n := cr2New(t, o)
			in := input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL)
			in.RepeatAfterMs = Ptr(int64(1000))
			if err := validateMonitor(&in, n.known); err != nil {
				t.Fatal(err)
			}
			m, err := n.writeMonitor(in, nil, "repair-pruned-history", false)
			if err != nil {
				t.Fatal(err)
			}
			partitionDrive(t, n)
			header, _, _ := pcrOpaque(t, n, m, "repair-pruned-opaque")
			cr2Apply(t, n, Snapshot{Incidents: []Incident{header}})
			release := pcrHoldWorkers(n)
			state.Store("down")
			now.Add(1000)
			partitionDrive(t, n)
			brief := pcrOnlyEpisode(t, n)
			if len(n.attempts) != 0 || !n.hasLocalIncidentDelivery(brief, "") {
				t.Fatal("down obligation depended on attempt admission or delay")
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
				now.Add(3600000)
				partitionDrive(t, n)
			}
			history, err := n.store.history(m.ID, 100)
			if err != nil || len(history) != 1 || history[0].State != nextState {
				t.Fatal("fixture failed to prune its down/up history", history, err)
			}
			got := 0
			for _, body := range r.all() {
				if body.IncidentID == brief.ID {
					if body.Kind != "down" {
						t.Fatal("old local obligation authorized a follow-up", r.all())
					}
					got++
				}
			}
			if got != 1 || n.episodes[brief.ID].Closed != nil || r.count("recovery") != 0 {
				t.Fatal("owed down was lost, duplicated, or granted global closure", got, r.all())
			}
			peer := cr2New(t, cr2Options(t, false))
			pcrSweep(t, n, peer)
			if len(peer.localIncidentDeliveries) != 0 {
				t.Fatal("local provenance replicated to another database")
			}
		})
	}
}

// Inject failures both at the marker write and after it. The exact original
// incident, intent, immutable evidence, SQL indexes and marker retry together.
func TestPartitionDeliveryRepairMarkerAtomicRollbackAndRestart(t *testing.T) {
	for _, category := range []string{"localIncidentDeliveries", "incidentSources"} {
		t.Run(category, func(t *testing.T) {
			o := cr2Options(t, true)
			n := cr2New(t, o)
			r := webhook(t)
			m, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL), nil, "repair-atomic-monitor", false)
			if err != nil {
				t.Fatal(err)
			}
			header, _, _ := pcrOpaque(t, n, m, "repair-atomic-opaque")
			cr2Apply(t, n, Snapshot{Incidents: []Incident{header}})
			sample := Sample{Observation: Observation{MonitorID: m.ID, NodeID: n.ID, State: "down", CheckedAt: n.now()}, Revision: m.Revision, CheckGeneration: m.CheckGeneration, Sequence: 3, DownSince: Ptr(int64(3))}
			n.samples[m.ID+"/"+n.ID] = sample
			i := Incident{ID: "repair-atomic-source", Aliases: []string{"repair-atomic-source"}, MonitorID: m.ID, MonitorName: m.Name, Revision: checkGeneration(m), OpenedAt: n.now(), Runs: map[string][]int64{n.ID: {3}}, Predecessors: []string{}, DeliveryTargets: []string{""},
				uncertainties: []incidentUncertainty{{Header: uncertainHeader(header), ObserverID: n.ID, NodeID: n.ID, Run: 3}}}
			intent := deliveryIntent(m.ID, i.ID, "")
			page := Snapshot{Incidents: n.localIncidentUpdates(i), Intents: []NotificationIntent{intent}}
			before, inputBefore := cr2State(n), contentID(page)
			trigger := fmt.Sprintf(`CREATE TRIGGER repair_marker_fail BEFORE INSERT ON records WHEN NEW.category='%s' BEGIN SELECT RAISE(FAIL,'injected marker failure'); END`, category)
			if _, err := n.store.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			if err := n.applySnapshot(page); err == nil {
				t.Fatal("failed marker transaction acknowledged")
			}
			stored, err := loadAll[localIncidentDelivery](n.store, "localIncidentDeliveries")
			if err != nil || len(stored) != 0 || len(n.localIncidentDeliveries) != 0 || cr2State(n) != before || contentID(page) != inputBefore {
				t.Fatal("failed marker transaction changed durable or in-memory authority", stored, err)
			}
			if _, err := n.store.db.Exec(`DROP TRIGGER repair_marker_fail`); err != nil {
				t.Fatal(err)
			}
			if err := n.applySnapshot(page); err != nil {
				t.Fatal("identical local source/intent could not retry", err)
			}
			if len(n.localIncidentDeliveries) != 1 || !n.hasLocalIncidentDelivery(n.episodes[i.ID], "") {
				t.Fatal("successful transaction did not capture local provenance")
			}
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			if len(n.localIncidentDeliveries) != 1 || !n.hasLocalIncidentDelivery(n.episodes[i.ID], "") || !n.hasDeliveryIntent(n.episodes[i.ID], "") {
				t.Fatal("restart lost source/intent-scoped local provenance")
			}
		})
	}
}

// Existing protocol-7 sources have no marker. A fresh observation of their
// unchanged own run must capture it even when the incident/audience is a no-op.
func TestPartitionDeliveryRepairFreshDownUpgradesMarkerAbsentSource(t *testing.T) {
	var now atomic.Int64
	now.Store(maintenanceClock(12, 0))
	var state atomic.Value
	state.Store("down")
	r := webhook(t)
	o := cr2Options(t, true)
	o.Now, o.NotificationDelayMs = now.Load, Ptr(int64(60000))
	o.Probe = func(context.Context, Monitor) ProbeResult {
		return ProbeResult{State: state.Load().(string), Message: "controlled upgrade"}
	}
	n := cr2New(t, o)
	m, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL), nil, "repair-upgrade-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	partitionDrive(t, n)
	brief := pcrOnlyEpisode(t, n)
	key := deliveryIntent(m.ID, brief.ID, "").ID
	if !n.hasLocalIncidentDelivery(brief, "") {
		t.Fatal("fixture failed to capture initial local down")
	}
	// Model a latest pre-marker DB, keeping the original source, policy,
	// local run and intent. No historical sequence or identity is rolled back.
	if err := remove(n.store.db, "localIncidentDeliveries", key); err != nil {
		t.Fatal(err)
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = cr2New(t, o)
	if len(n.localIncidentDeliveries) != 0 || !n.hasDeliveryIntent(n.episodes[brief.ID], "") {
		t.Fatal("startup inferred local provenance or lost the preexisting intent")
	}
	before := n.incidentSources[brief.ID].EvidenceRoot
	now.Add(1000)
	partitionDrive(t, n)
	if !n.hasLocalIncidentDelivery(n.episodes[brief.ID], "") || n.incidentSources[brief.ID].EvidenceRoot != before || len(n.episodes) != 1 {
		t.Fatal("unchanged fresh down did not capture its old source's obligation")
	}
	state.Store("up")
	now.Add(1000)
	partitionDrive(t, n)
	state.Store("pending")
	now.Add(1000)
	partitionDrive(t, n)
	header, _, _ := pcrOpaque(t, n, m, "repair-upgrade-late-opaque")
	cr2Apply(t, n, Snapshot{Incidents: []Incident{header}})
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = cr2New(t, o)
	now.Add(3600000)
	for range 3 {
		partitionDrive(t, n)
	}
	if r.count("down") != 1 || r.count("recovery") != 0 || r.count("repeat") != 0 || r.all()[0].IncidentID != brief.ID {
		t.Fatal("upgraded local obligation was lost after its sample was replaced", r.all())
	}
}

func TestPartitionDeliveryRepairPeerAssertionsCannotCaptureLocalDelivery(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			o := cr2Options(t, true)
			n := cr2New(t, o)
			r := webhook(t)
			m, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL), nil, "repair-peer-monitor", false)
			if err != nil {
				t.Fatal(err)
			}
			// Even a claim matching a currently fresh local run remains peer
			// input until local evaluation captures its own source and audience.
			n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: n.now()}, Revision: m.Revision, CheckGeneration: m.CheckGeneration, Sequence: 3, DownSince: Ptr(int64(3))}
			i := Incident{ID: "repair-peer-source", Aliases: []string{"repair-peer-source"}, MonitorID: m.ID, MonitorName: m.Name, Revision: checkGeneration(m), OpenedAt: n.now(), Runs: map[string][]int64{"a": {3}}, Predecessors: []string{}, DeliveryTargets: []string{""}}
			page := Snapshot{Incidents: []Incident{i}, Intents: []NotificationIntent{deliveryIntent(m.ID, i.ID, "")}}
			if !legacy {
				h, facts, links := partitionEvidence(i)
				page.Incidents, page.IncidentFacts, page.EvidenceLinks = []Incident{h}, facts, links
			}
			cr2Apply(t, n, page)
			if len(n.localIncidentDeliveries) != 0 {
				t.Fatal("peer assertion acquired local delivery provenance")
			}
			// Finish only the peer's source, then introduce unrelated opaque
			// history. Its owed intent alone cannot manufacture a local marker.
			i = n.incidentSources[i.ID]
			i.Closed, i.ResolvedAt = map[string]int64{"a": 4, "b": 4}, Ptr(n.now())
			cr2Apply(t, n, Snapshot{Incidents: []Incident{i}})
			header, _, _ := pcrOpaque(t, n, m, "repair-peer-opaque")
			cr2Apply(t, n, Snapshot{Incidents: []Incident{header}})
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			if len(n.localIncidentDeliveries) != 0 || n.hasLocalIncidentDelivery(n.episodes[i.ID], "") {
				t.Fatal("restart promoted remote-only history into local provenance")
			}
			n.evaluate()
			n.wg.Wait()
			if r.count("") != 0 {
				t.Fatal("remote-only intent gained local owed-down authority", r.all())
			}
		})
	}
}

func TestPartitionDeliveryRepairMarkerLoadRejectsMalformedAuthority(t *testing.T) {
	for _, damage := range []string{"omitted", "null", "case", "duplicate", "wrong-key", "foreign-node", "wrong-generation", "wrong-run", "wrong-destination", "missing-intent"} {
		t.Run(damage, func(t *testing.T) {
			o := cr2Options(t, true)
			n := cr2New(t, o)
			r := webhook(t)
			m, err := n.writeMonitor(input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL), nil, "repair-load-monitor", false)
			if err != nil {
				t.Fatal(err)
			}
			n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: n.now()}, Revision: m.Revision, CheckGeneration: m.CheckGeneration, Sequence: 3, DownSince: Ptr(int64(3))}
			release := pcrHoldWorkers(n)
			n.evaluate()
			release()
			var key string
			var marker localIncidentDelivery
			for key, marker = range n.localIncidentDeliveries {
				break
			}
			if key == "" {
				t.Fatal("fixture did not capture a marker")
			}
			switch damage {
			case "foreign-node":
				marker.NodeID = "b"
			case "wrong-generation":
				marker.Revision = "foreign-generation"
			case "wrong-run":
				marker.Run++
			case "wrong-destination":
				marker.DestinationID = "other-route"
			case "missing-intent":
				if err := remove(n.store.db, "notificationIntents", key); err != nil {
					t.Fatal(err)
				}
			}
			body, err := json.Marshal(marker)
			if err != nil {
				t.Fatal(err)
			}
			raw := string(body)
			switch damage {
			case "omitted":
				raw = strings.Replace(raw, `"destinationId":"",`, "", 1)
			case "null":
				raw = strings.Replace(raw, `"destinationId":""`, `"destinationId":null`, 1)
			case "case":
				raw = strings.Replace(raw, `"destinationId"`, `"DestinationId"`, 1)
			case "duplicate":
				raw = strings.Replace(raw, `"destinationId":""`, `"destinationId":"","destinationId":""`, 1)
			case "wrong-key":
				if err := remove(n.store.db, "localIncidentDeliveries", key); err != nil {
					t.Fatal(err)
				}
				key = "wrong-marker-key"
			}
			if _, err := n.store.db.Exec(`INSERT INTO records(category,id,payload) VALUES('localIncidentDeliveries',?,?) ON CONFLICT(category,id) DO UPDATE SET payload=excluded.payload`, key, raw); err != nil {
				t.Fatal(err)
			}
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			reopened, err := New(o)
			if reopened != nil {
				_ = reopened.Stop()
			}
			if err == nil || !strings.Contains(err.Error(), "Invalid local incident delivery") {
				t.Fatal("malformed marker resumed as local authority", err)
			}
		})
	}
}

func TestPartitionDeliveryRepairOwedMarkerHonorsDeliveryHolds(t *testing.T) {
	for _, hold := range []string{"disabled-route", "maintenance", "new-generation"} {
		t.Run(hold, func(t *testing.T) {
			var now atomic.Int64
			now.Store(maintenanceClock(11, 59))
			var state atomic.Value
			state.Store("up")
			r := webhook(t)
			o := cr2Options(t, true)
			o.Now = now.Load
			o.Probe = func(context.Context, Monitor) ProbeResult {
				return ProbeResult{State: state.Load().(string), Message: "controlled hold"}
			}
			n := cr2New(t, o)
			channel := hookChannel("Marker route", r.URL)
			resource, err := n.writeResource(resourceChannel, "repair-hold-route", channel, false)
			if err != nil {
				t.Fatal(err)
			}
			in := input([]string{"a", "b"}, "http://127.0.0.1:1", "")
			in.NotificationIDs, in.RepeatAfterMs = []string{resource.ID}, Ptr(int64(1000))
			m, err := n.writeMonitor(in, nil, "repair-hold-monitor", false)
			if err != nil {
				t.Fatal(err)
			}
			partitionDrive(t, n)
			header, _, _ := pcrOpaque(t, n, m, "repair-hold-opaque")
			cr2Apply(t, n, Snapshot{Incidents: []Incident{header}})
			release := pcrHoldWorkers(n)
			state.Store("down")
			now.Add(1000)
			partitionDrive(t, n)
			brief := pcrOnlyEpisode(t, n)
			destination := n.notificationTargets(m)[0].ID
			if !n.hasLocalIncidentDelivery(brief, destination) || n.hasLocalIncidentDelivery(brief, "other-route") {
				t.Fatal("local obligation was not destination-scoped")
			}
			state.Store("up")
			now.Add(1000)
			partitionDrive(t, n)
			state.Store("pending")
			now.Add(1000)
			partitionDrive(t, n)
			switch hold {
			case "disabled-route":
				channel.Enabled = false
				_, err = n.writeResource(resourceChannel, resource.ID, channel, false)
			case "maintenance":
				now.Store(maintenanceClock(12, 1))
				storeMaintenance(t, n, plannedWork())
			case "new-generation":
				in.Target = "http://127.0.0.1:2"
				m, err = n.writeMonitor(in, n.heads[m.ID], m.ID, false)
			}
			if err != nil {
				t.Fatal(err)
			}
			release()
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			n = cr2New(t, o)
			n.evaluate()
			n.wg.Wait()
			if r.count("") != 0 || !n.hasLocalIncidentDelivery(n.episodes[brief.ID], destination) {
				t.Fatal("delivery hold was bypassed or erased the owed marker", r.all())
			}
			if hold == "disabled-route" {
				channel.Enabled = true
				if _, err := n.writeResource(resourceChannel, resource.ID, channel, false); err != nil {
					t.Fatal(err)
				}
			}
			now.Add(3600000)
			for range 3 {
				n.evaluate()
				n.wg.Wait()
			}
			wantDown := 1
			if hold == "new-generation" {
				wantDown = 0
				if n.episodes[brief.ID].RetiredBy == "" {
					t.Fatal("semantic edit did not retire the marked original source")
				}
			}
			if r.count("down") != wantDown || r.count("repeat") != 0 || r.count("recovery") != 0 {
				t.Fatal("hold release lost owed delivery or granted a false follow-up", hold, r.all())
			}
		})
	}
}
