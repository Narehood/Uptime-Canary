package canary

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"
)

// Source-only review fixture for 24fc3dd plus root's conservative candidate:
// incidentSummariesChanged(sources map[string]Incident, incoming []Incident) bool.
// Run in the canary package using root's bounded validation/overlay workflow.
// The oracle is the existing unconditional sourceProjection, with the same
// merged immutable evidence. capacityScopeState includes nested private proof.
// Nodes never Start; stores are private and no notification workers are driven.

func capacityDuplicateCopy(t *testing.T, i Incident) Incident {
	t.Helper()
	b, err := json.Marshal(i)
	if err != nil {
		t.Fatal(err)
	}
	var out Incident
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out // Deliberately models a wire summary without private uncertainties.
}

func capacityDuplicateApply(t *testing.T, n *Node, s Snapshot, wantSummariesChanged bool) {
	t.Helper()
	facts, links := maps.Clone(n.incidentFacts), maps.Clone(n.evidenceLinks)
	for _, f := range s.IncidentFacts {
		facts[f.ID] = f
	}
	for _, l := range s.EvidenceLinks {
		links[l.ID] = l
	}
	if got := incidentSummariesChanged(n.incidentSources, s.Incidents); got != wantSummariesChanged {
		t.Fatalf("summaries changed=%v want=%v", got, wantSummariesChanged)
	}
	wantSources, wantPending, wantEpisodes, err := sourceProjection(n.incidentSources, n.pendingIncidents, s.Incidents, facts, links)
	if err != nil {
		t.Fatal("global projection oracle rejected fixture", err)
	}
	if err := n.applySnapshot(s); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]map[string]Incident{{n.incidentSources, wantSources}, {n.pendingIncidents, wantPending}, {n.episodes, wantEpisodes}} {
		if capacityScopeState(t, pair[0]) != capacityScopeState(t, pair[1]) {
			t.Fatal("shortcut differs from global source/pending/episode oracle, including private proof")
		}
	}
	if !equalJSON(n.incidentFacts, facts) || !equalJSON(n.evidenceLinks, links) {
		t.Fatal("shortcut lost or invented immutable evidence")
	}
}

func capacityDuplicateMemory(t *testing.T, n *Node) string {
	t.Helper()
	return contentID([]any{cr2State(n), n.samples, n.localIncidentDeliveries,
		capacityScopeState(t, n.incidentSources), capacityScopeState(t, n.pendingIncidents), capacityScopeState(t, n.episodes)})
}

func capacityDuplicateDisk(t *testing.T, n *Node) string {
	t.Helper()
	rows, err := n.store.db.Query(`SELECT category,id,payload FROM records ORDER BY category,id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	state := [][3]string{}
	for rows.Next() {
		var row [3]string
		if err := rows.Scan(&row[0], &row[1], &row[2]); err != nil {
			t.Fatal(err)
		}
		state = append(state, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return contentID(state)
}

func TestCapacityDuplicateProjectionPredicate(t *testing.T) {
	facts, links := map[string]IncidentFact{}, map[string]EvidenceLink{}
	base := capacityScopeOriginal("duplicate-source", "duplicate-monitor", "duplicate-generation", 1, 10)
	base = boundedIncident(prepareIncident(base, "", facts, links))
	u := incidentUncertainty{Header: uncertainHeader(base), ObserverID: "a", NodeID: "a", Run: 1}
	private := capacityDuplicateCopy(t, base)
	private.uncertainties = []incidentUncertainty{u}
	unknown := capacityDuplicateCopy(t, base)
	unknown.ID = "unknown-source"
	scope := capacityDuplicateCopy(t, base)
	scope.MonitorID = "other-monitor"
	generation := capacityDuplicateCopy(t, base)
	generation.Revision = "other-generation"
	shape := capacityDuplicateCopy(t, base)
	shape.Predecessors = nil
	cases := []struct {
		name     string
		incoming []Incident
		want     bool
		sources  map[string]Incident
	}{
		{"empty", nil, false, nil},
		{"exact", []Incident{base}, false, nil},
		{"all-exact", []Incident{base, base}, false, nil},
		{"private-json-equal", []Incident{private}, true, nil},
		{"private-last", []Incident{base, private}, true, nil},
		{"private-first", []Incident{private, base}, true, nil},
		{"unknown-last", []Incident{base, unknown}, true, nil},
		{"unknown-first", []Incident{unknown, base}, true, nil},
		{"pending-only", []Incident{base}, true, map[string]Incident{}},
		{"stored-private-only", []Incident{base}, false, map[string]Incident{base.ID: private}},
		{"changed-monitor", []Incident{scope}, true, nil},
		{"changed-generation", []Incident{generation}, true, nil},
		{"nil-versus-empty", []Incident{shape}, true, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sources := map[string]Incident{base.ID: base}
			if tc.sources != nil {
				sources = tc.sources
			}
			if got := incidentSummariesChanged(sources, tc.incoming); got != tc.want {
				t.Fatalf("summaries changed=%v want=%v", got, tc.want)
			}
		})
	}
	if !equalJSON(base, private) {
		t.Fatal("private-input control must have exactly equal exported JSON")
	}
}

func TestCapacityDuplicateProjectionGlobalDifferential(t *testing.T) {
	for _, change := range []string{"exact", "closed-frontier", "resolution", "runs", "alias", "target", "root", "new-source", "predecessor", "retirement", "opened", "name", "suppression", "maintenance", "private-rooted", "private-local"} {
		t.Run(change, func(t *testing.T) {
			n := cr2New(t, cr2Options(t, false))
			base := capacityScopeOriginal("z-duplicate-source", "duplicate-monitor", "duplicate-generation", 1, 10)
			base.Aliases = append(base.Aliases, "a-canonical")
			base.Closed, base.ResolvedAt = map[string]int64{"a": 2, "b": 2}, Ptr(int64(20))
			capacityDuplicateApply(t, n, Snapshot{Incidents: []Incident{base}}, true)
			if _, exists := n.episodes[base.ID]; exists {
				t.Fatal("fixture must retain an original hidden by its canonical alias")
			}
			incoming := capacityDuplicateCopy(t, n.incidentSources[base.ID])
			switch change {
			case "closed-frontier":
				incoming.Closed["a"] = 3
			case "resolution":
				incoming.ResolvedAt = Ptr(int64(15))
			case "runs":
				incoming.EvidenceRoot = ""
				incoming.Runs["a"] = []int64{1, 3}
			case "alias":
				incoming.EvidenceRoot = ""
				incoming.Aliases = append(incoming.Aliases, "00-new-canonical")
			case "target":
				incoming.DeliveryTargets = []string{"new-destination"}
			case "root":
				l := EvidenceLink{Left: incoming.EvidenceRoot, Right: incoming.EvidenceRoot}
				l.ID = linkID(l)
				capacityDuplicateApply(t, n, Snapshot{EvidenceLinks: []EvidenceLink{l}}, false)
				incoming.EvidenceRoot = l.ID
			case "new-source":
				incoming.ID, incoming.Aliases, incoming.EvidenceRoot = "new-original", []string{"new-original"}, ""
			case "predecessor":
				incoming.EvidenceRoot, incoming.Predecessors = "", []string{"missing-predecessor"}
			case "retirement":
				incoming.RetiredBy = "new-generation"
			case "opened":
				incoming.OpenedAt = 5
			case "name":
				incoming.MonitorName = "Changed metadata"
			case "suppression":
				incoming.SuppressedByGroupID, incoming.SuppressionReason = "duplicate-group", "Changed suppression"
			case "maintenance":
				incoming.MaintenanceIDs, incoming.MaintenanceStartedAt = []string{"duplicate-maintenance"}, Ptr(int64(5))
			case "private-rooted", "private-local":
				header := uncertainHeader(incoming)
				header.ID, header.EvidenceRoot = "opaque-boundary", digest("missing-boundary-proof")
				incoming.uncertainties = []incidentUncertainty{{Header: header, ObserverID: "a", NodeID: "a", Run: 1}}
				if change == "private-local" {
					incoming.EvidenceRoot = ""
				}
			}
			capacityDuplicateApply(t, n, Snapshot{Incidents: []Incident{incoming}}, change != "exact")
			// A public duplicate of a source with private proof remains eligible.
			duplicate := capacityDuplicateCopy(t, n.incidentSources[incoming.ID])
			capacityDuplicateApply(t, n, Snapshot{Incidents: []Incident{duplicate}}, false)
		})
	}
}

func TestCapacityDuplicateProjectionPendingAndNestedProof(t *testing.T) {
	for _, tc := range []struct {
		name             string
		nested, holdLink bool
	}{{"pending-fact", false, false}, {"pending-link", false, true}, {"nested-fact", true, false}, {"nested-link-and-duplicate", true, true}} {
		t.Run(tc.name, func(t *testing.T) {
			n := cr2New(t, cr2Options(t, false))
			seed := capacityScopeOriginal("duplicate-seed", "duplicate-monitor", "duplicate-generation", 1, 10)
			header, facts, links := partitionEvidence(seed)
			var held IncidentFact
			partial := []IncidentFact{}
			for _, f := range facts {
				if f.Kind == "run" && f.NodeID == "b" {
					held = f
				} else {
					partial = append(partial, f)
				}
			}
			if held.ID == "" {
				t.Fatal("missing final-proof control")
			}
			page := Snapshot{Incidents: []Incident{header}, IncidentFacts: partial, EvidenceLinks: links}
			if tc.nested {
				middle := seed
				middle.ID, middle.Aliases = "duplicate-middle", []string{"duplicate-middle"}
				middle.uncertainties = []incidentUncertainty{{Header: uncertainHeader(header), ObserverID: "a", NodeID: "a", Run: 1}}
				mh, mf, ml := partitionEvidence(middle)
				page.Incidents = nil
				page.IncidentFacts, page.EvidenceLinks = append(partial, mf...), append(links, ml...)
				for _, observer := range []string{"a", "b"} {
					branch := capacityScopeOriginal("duplicate-branch-"+observer, seed.MonitorID, seed.Revision, 1, 15)
					branch.Runs = map[string][]int64{observer: {1}}
					branch.uncertainties = []incidentUncertainty{{Header: uncertainHeader(mh), ObserverID: observer, NodeID: observer, Run: 1}}
					h, bf, bl := partitionEvidence(branch)
					page.Incidents = append(page.Incidents, h)
					page.IncidentFacts, page.EvidenceLinks = append(page.IncidentFacts, bf...), append(page.EvidenceLinks, bl...)
				}
			}
			completion := Snapshot{IncidentFacts: []IncidentFact{held}}
			if tc.holdLink {
				page.IncidentFacts = append(page.IncidentFacts, held)
				var missing EvidenceLink
				page.EvidenceLinks = slices.DeleteFunc(page.EvidenceLinks, func(l EvidenceLink) bool {
					if l.ID == header.EvidenceRoot {
						missing = l
						return true
					}
					return false
				})
				if missing.ID == "" {
					t.Fatal("missing final-link control")
				}
				completion = Snapshot{EvidenceLinks: []EvidenceLink{missing}}
			}
			capacityDuplicateApply(t, n, cr2Decode(t, n, page), true)
			if tc.nested {
				if len(n.pendingIncidents) != 0 || len(n.episodes) != 2 || !hasUncertaintyReferences(n.episodes) {
					t.Fatal("nested proof must remain unresolved without a standalone pending record")
				}
			} else {
				if len(n.pendingIncidents) != 1 || len(n.episodes) != 0 {
					t.Fatal("incomplete root became authoritative")
				}
				capacityDuplicateApply(t, n, Snapshot{Incidents: []Incident{header}}, true)
			}
			if tc.nested && tc.holdLink {
				completion.Incidents = []Incident{capacityDuplicateCopy(t, n.incidentSources["duplicate-branch-a"])}
			}
			// Both proof-only and duplicate-plus-new-proof pages must refresh.
			capacityDuplicateApply(t, n, completion, false)
			if len(n.pendingIncidents) != 0 || len(n.episodes) != 1 {
				t.Fatal("completed immutable proof did not converge")
			}
			duplicates := []Incident{}
			for _, i := range n.incidentSources {
				duplicates = append(duplicates, capacityDuplicateCopy(t, i))
			}
			capacityDuplicateApply(t, n, Snapshot{Incidents: duplicates, IncidentFacts: completion.IncidentFacts, EvidenceLinks: completion.EvidenceLinks}, false)
		})
	}
}

func TestCapacityDuplicateProjectionSideEffectsRollbackAndRestart(t *testing.T) {
	o := cr2Options(t, true)
	n := cr2New(t, o)
	i := capacityScopeOriginal("duplicate-side-effects", "duplicate-monitor", "duplicate-generation", 1, 10)
	capacityDuplicateApply(t, n, Snapshot{Incidents: []Incident{i}}, true)
	i = capacityDuplicateCopy(t, n.incidentSources[i.ID])
	intent := deliveryIntent(i.MonitorID, i.ID, "separately-paged-destination")
	sample := Sample{Observation: Observation{MonitorID: i.MonitorID, NodeID: "b", State: "up", CheckedAt: n.now(), Message: "duplicate page"}, Revision: i.Revision, Sequence: 2}
	receipt := Receipt{NotificationView: NotificationView{ID: "duplicate-receipt", MonitorID: i.MonitorID, IncidentID: i.ID, Kind: "down", NodeID: "b", SentAt: n.now()}}
	g := Group{GroupInput: GroupInput{Name: "Duplicate page group", NodeIDs: []string{"a", "b"}}, ID: "duplicate-group", Revision: "duplicate-group-revision", UpdatedAt: n.now()}
	page := cr2Decode(t, n, Snapshot{Incidents: []Incident{i}, Intents: []NotificationIntent{intent}, Observations: []Sample{sample}, Receipts: []Receipt{receipt}, GroupVersions: []GroupVersion{{Group: g, Ancestors: []string{}}}})
	before, diskBefore, inputBefore := capacityDuplicateMemory(t, n), capacityDuplicateDisk(t, n), contentID(page)
	if incidentSummariesChanged(n.incidentSources, page.Incidents) {
		t.Fatal("side-effect page must exercise the actual duplicate shortcut")
	}
	if _, err := n.store.db.Exec(`CREATE TRIGGER duplicate_fail BEFORE INSERT ON records WHEN NEW.category='receipts' BEGIN SELECT RAISE(FAIL,'duplicate page rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if err := n.applySnapshot(page); err == nil || capacityDuplicateMemory(t, n) != before || capacityDuplicateDisk(t, n) != diskBefore || contentID(page) != inputBefore {
		t.Fatal("duplicate-page SQL failure published authority, changed retry input, or was acknowledged", err)
	}
	if _, err := n.store.db.Exec(`DROP TRIGGER duplicate_fail`); err != nil {
		t.Fatal(err)
	}
	// Causal validation must also precede any duplicate short circuit.
	bad := slices.Collect(maps.Values(n.incidentFacts))[0]
	bad.Run++ // Keep the old content ID, so admission must reject this record.
	if err := n.applySnapshot(Snapshot{Incidents: []Incident{i}, IncidentFacts: []IncidentFact{bad}}); err == nil || capacityDuplicateMemory(t, n) != before || capacityDuplicateDisk(t, n) != diskBefore {
		t.Fatal("duplicate page bypassed immutable evidence validation", err)
	}
	badIncident := capacityDuplicateCopy(t, i)
	badIncident.MonitorID = "different-evidence-scope"
	if err := n.applySnapshot(Snapshot{Incidents: []Incident{i, badIncident}}); err == nil || capacityDuplicateMemory(t, n) != before || capacityDuplicateDisk(t, n) != diskBefore {
		t.Fatal("duplicate page bypassed hydration scope validation", err)
	}
	capacityDuplicateApply(t, n, page, false)
	check := func(n *Node) {
		t.Helper()
		if !equalJSON(n.samples[i.MonitorID+"/b"], sample) || !equalJSON(n.receipts[receipt.ID], receipt) || !equalJSON(n.intents[intent.ID], intent) || !n.hasDeliveryIntent(n.episodes[i.ID], intent.DestinationID) || n.currentGroup(g.ID) == nil {
			t.Fatal("duplicate shortcut lost samples, receipts, intent index, or configuration")
		}
	}
	check(n)
	committed := capacityDuplicateMemory(t, n)
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = cr2New(t, o)
	check(n)
	if capacityDuplicateMemory(t, n) != committed {
		t.Fatal("restart changed committed duplicate-page state")
	}
	capacityDuplicateApply(t, n, page, false)
}

func TestCapacityDuplicateProjectionLocalProvenanceAndEvaluation(t *testing.T) {
	for _, purpose := range []string{"local-provenance", "merge-evaluation"} {
		t.Run(purpose, func(t *testing.T) {
			o := cr2Options(t, false)
			o.Now = func() int64 { return 100000 }
			n := cr2New(t, o)
			in := cr2Input(t, []string{"a", "b"})
			if purpose == "local-provenance" {
				in.WebhookURL = "http://127.0.0.1:1/no-notification-worker"
			}
			m, err := n.writeMonitor(in, nil, "duplicate-local-monitor", false)
			if err != nil {
				t.Fatal(err)
			}
			i := capacityScopeOriginal("duplicate-local-source", m.ID, checkGeneration(m), 1, n.now()-1000)
			if purpose == "local-provenance" {
				i.DeliveryTargets = []string{""}
			}
			capacityDuplicateApply(t, n, cr2Decode(t, n, Snapshot{Incidents: []Incident{i}}), true)
			state := "up"
			if purpose == "local-provenance" {
				state = "down"
			}
			localSample := Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: state, CheckedAt: n.now(), Message: "Duplicate page observation"}, Revision: m.Revision, Sequence: 2}
			if state == "down" {
				localSample.DownSince = Ptr(int64(1))
			}
			n.samples[m.ID+"/a"] = localSample
			if err := put(n.store.db, "samples", m.ID+"/a", localSample); err != nil {
				t.Fatal(err)
			}
			if purpose == "merge-evaluation" {
				peer := localSample
				peer.NodeID = "b"
				peer.DownSince = nil
				duplicate := capacityDuplicateCopy(t, n.incidentSources[i.ID])
				page := cr2Decode(t, n, Snapshot{Incidents: []Incident{duplicate}, Observations: []Sample{peer}})
				capacityDuplicateApply(t, n, page, false)
				if err := n.mergeSnapshot(page); err != nil {
					t.Fatal(err)
				}
				closed := n.episodes[i.ID]
				if closed.ResolvedAt == nil || !equalJSON(closed.Closed, map[string]int64{"a": 2, "b": 2}) {
					t.Fatal("duplicate merge skipped all-up incident evaluation")
				}
				return
			}
			local, complete, err := hydrateIncident(n.incidentSources[i.ID], n.incidentFacts, n.evidenceLinks)
			if err != nil || !complete || n.hasLocalIncidentDelivery(local, "") {
				t.Fatal("remote seed must have no local provenance", err)
			}
			local.EvidenceRoot = "" // Local updates remain conservative, even with identical hydrated evidence.
			intent := deliveryIntent(m.ID, i.ID, "separate-local-intent")
			page := Snapshot{Incidents: []Incident{local}, Intents: []NotificationIntent{intent}}
			before, diskBefore := capacityDuplicateMemory(t, n), capacityDuplicateDisk(t, n)
			if _, err := n.store.db.Exec(`CREATE TRIGGER duplicate_local_fail BEFORE INSERT ON records WHEN NEW.category='localIncidentDeliveries' BEGIN SELECT RAISE(FAIL,'local provenance rollback'); END`); err != nil {
				t.Fatal(err)
			}
			if err := n.applySnapshot(page); err == nil || capacityDuplicateMemory(t, n) != before || capacityDuplicateDisk(t, n) != diskBefore {
				t.Fatal("local obligation failure did not roll back intent and provenance", err)
			}
			if _, err := n.store.db.Exec(`DROP TRIGGER duplicate_local_fail`); err != nil {
				t.Fatal(err)
			}
			capacityDuplicateApply(t, n, page, true)
			if !n.hasLocalIncidentDelivery(n.episodes[i.ID], "") || !n.hasDeliveryIntent(n.episodes[i.ID], intent.DestinationID) {
				t.Fatal("unchanged local evidence failed to publish its missing obligation and separate intent")
			}
			markers, err := loadAll[localIncidentDelivery](n.store, "localIncidentDeliveries")
			if err != nil || !equalJSON(markers, n.localIncidentDeliveries) {
				t.Fatal("local provenance was not durably committed", err)
			}
		})
	}
}
