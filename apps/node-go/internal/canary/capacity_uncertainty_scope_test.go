package canary

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"testing"
)

// Compare scoped resolution with the preserved global algorithm, including
// private proof state. No stores, probes, notifications or clocks are used.

// JSON alone misses uncertainties and their resolved pointers. Capture both,
// sorting only the unordered uncertainty set; retain every public field exactly.
func capacityScopeState(t *testing.T, records map[string]Incident) string {
	t.Helper()
	var record func(Incident, map[*Incident]bool) json.RawMessage
	record = func(i Incident, active map[*Incident]bool) json.RawMessage {
		private := []string{}
		for _, u := range i.uncertainties {
			var resolved json.RawMessage
			if u.resolved != nil {
				if active[u.resolved] {
					t.Fatal("unexpected cycle in small uncertainty fixture")
				}
				active[u.resolved] = true
				resolved = record(*u.resolved, active)
				delete(active, u.resolved)
			}
			b, err := json.Marshal([]any{u, resolved})
			if err != nil {
				t.Fatal(err)
			}
			private = append(private, string(b))
		}
		slices.Sort(private)
		b, err := json.Marshal([]any{i, private})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	out := map[string]json.RawMessage{}
	for key, i := range records {
		out[key] = record(i, map[*Incident]bool{})
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func capacityScopeDiff(t *testing.T, original map[string]Incident, facts map[string]IncidentFact, links map[string]EvidenceLink) (map[string]Incident, error) {
	t.Helper()
	before, beforeFacts, beforeLinks := capacityScopeState(t, original), contentID(facts), contentID(links)
	unchanged := func() {
		t.Helper()
		if capacityScopeState(t, original) != before || contentID(facts) != beforeFacts || contentID(links) != beforeLinks {
			t.Fatal("uncertainty resolution mutated an input, including private proof state")
		}
	}
	want, wantErr := resolveUncertaintyScope(original, original, facts, links)
	unchanged()
	got, gotErr := resolveUncertaintySet(original, facts, links)
	unchanged()
	if !reflect.DeepEqual(gotErr, wantErr) || (got == nil) != (want == nil) {
		t.Fatalf("global/partition result mismatch: got %v, want %v; nil maps %v/%v", gotErr, wantErr, got == nil, want == nil)
	}
	if capacityScopeState(t, got) != capacityScopeState(t, want) {
		t.Fatal("partition changed public incident data or a nested resolved proof")
	}
	keys := slices.Sorted(maps.Keys(got))
	gotContext, wantContext := []Incident{}, []Incident{}
	for _, key := range keys {
		gotContext = append(gotContext, got[key])
		wantContext = append(wantContext, want[key])
	}
	for _, a := range keys {
		for _, b := range keys {
			if causallyEquivalent(got[a], got[b], gotContext) != causallyEquivalent(want[a], want[b], wantContext) {
				t.Fatal("partition changed causal continuity", a, b)
			}
		}
	}
	return got, gotErr
}

func capacityScopeOriginal(id, monitor, generation string, run, opened int64) Incident {
	return Incident{ID: id, Aliases: []string{id}, MonitorID: monitor, MonitorName: "Scope fixture", Revision: generation,
		OpenedAt: opened, Runs: map[string][]int64{"a": {run}, "b": {run}}, Predecessors: []string{}}
}

func capacityScopeCommit(t *testing.T, i Incident, facts map[string]IncidentFact, links map[string]EvidenceLink) Incident {
	t.Helper()
	i = boundedIncident(prepareIncident(i, "", facts, links))
	full, complete, err := hydrateIncident(i, facts, links)
	if err != nil || !complete {
		t.Fatal("incomplete fixture source", i.ID, err)
	}
	full.uncertainties = combineUncertainties(full.uncertainties, nil)
	return full
}

func capacityScopeReference(i Incident, witness int64) incidentUncertainty {
	return incidentUncertainty{Header: uncertainHeader(i), ObserverID: "a", NodeID: "a", Run: witness}
}

func TestCapacityUncertaintyScopeDifferential(t *testing.T) {
	for _, scopes := range []int{1, 3} {
		for order := range 3 {
			t.Run(fmt.Sprintf("scopes=%d/order=%d", scopes, order), func(t *testing.T) {
				facts, links := map[string]IncidentFact{}, map[string]EvidenceLink{}
				deferredFacts, deferredLinks := map[string]IncidentFact{}, map[string]EvidenceLink{}
				original := map[string]Incident{}
				pairs := [][2]string{}
				for scope := range scopes {
					monitor, generation := "monitor-a", "generation-a"
					if scope == 1 {
						monitor = "monitor-b"
					} else if scope == 2 {
						generation = "generation-b"
					}
					makeOriginal := func(name string, run, opened int64) Incident {
						return capacityScopeOriginal(fmt.Sprintf("s%d-%s", scope, name), monitor, generation, run, opened)
					}
					// An original ID must survive an alias that sorts before it.
					q := makeOriginal("z-predecessor", 1, 1)
					q.Aliases = append(q.Aliases, "a-shared-predecessor")
					q = capacityScopeCommit(t, q, facts, links)
					seed := makeOriginal("seed", 1, 1)
					seed.Closed, seed.ResolvedAt = map[string]int64{"a": 2, "b": 2}, Ptr(int64(5))
					seed = capacityScopeCommit(t, seed, facts, links)
					old := capacityScopeCommit(t, makeOriginal("old", 3, 10), deferredFacts, deferredLinks)
					closed := makeOriginal("closed", 3, 10)
					closed.Closed, closed.ResolvedAt = map[string]int64{"a": 4, "b": 4}, Ptr(int64(20))
					closed.Predecessors = []string{"a-shared-predecessor"}
					closed.uncertainties = []incidentUncertainty{capacityScopeReference(q, 3)}
					closed = capacityScopeCommit(t, closed, deferredFacts, deferredLinks)
					middle := makeOriginal("middle", 5, 30)
					middle.uncertainties = []incidentUncertainty{capacityScopeReference(old, 5), capacityScopeReference(closed, 5)}
					middle = capacityScopeCommit(t, middle, deferredFacts, deferredLinks)
					pair := [2]string{}
					branches := []Incident{}
					for index, name := range []string{"branch-a", "branch-b"} {
						branch := makeOriginal(name, 5, 35)
						branch.uncertainties = []incidentUncertainty{capacityScopeReference(old, 5), capacityScopeReference(closed, 5), capacityScopeReference(middle, 5)}
						branch = capacityScopeCommit(t, branch, facts, links)
						pair[index] = "source/" + branch.ID // Preserve map keys, too.
						branches = append(branches, branch)
					}
					if order == 1 {
						original[pair[1]], original[pair[0]] = branches[1], branches[0]
						original[seed.ID], original[q.ID] = seed, q
					} else {
						original[q.ID], original[seed.ID] = q, seed
						original[pair[0]], original[pair[1]] = branches[0], branches[1]
					}
					pairs = append(pairs, pair)
				}
				initial, err := capacityScopeDiff(t, original, facts, links)
				if err != nil {
					t.Fatal(err)
				}
				for _, pair := range pairs {
					for _, u := range initial[pair[0]].uncertainties {
						if u.resolved != nil {
							t.Fatal("missing proof became resolved")
						}
					}
				}
				pages := []string{}
				for _, id := range slices.Sorted(maps.Keys(deferredFacts)) {
					pages = append(pages, "f"+id)
				}
				for _, id := range slices.Sorted(maps.Keys(deferredLinks)) {
					pages = append(pages, "l"+id)
				}
				if order == 1 {
					slices.Reverse(pages) // Links before facts; reverse dependencies.
				} else if order == 2 {
					// Fixed alternating extremes interleave scopes and proof kinds.
					mixed := []string{}
					for low, high := 0, len(pages)-1; low <= high; low, high = low+1, high-1 {
						mixed = append(mixed, pages[low])
						if low != high {
							mixed = append(mixed, pages[high])
						}
					}
					pages = mixed
				}
				got := initial
				for page, key := range pages {
					id := key[1:]
					if key[0] == 'f' {
						facts[id] = deferredFacts[id]
					} else {
						links[id] = deferredLinks[id]
					}
					got, err = capacityScopeDiff(t, original, facts, links)
					if err != nil {
						t.Fatalf("page %d: %v", page, err)
					}
				}
				n := &Node{known: map[string]bool{"a": true, "b": true}}
				if err := n.validateCausalFacts(Snapshot{IncidentFacts: slices.Collect(maps.Values(facts)), EvidenceLinks: slices.Collect(maps.Values(links))}); err != nil {
					t.Fatal("differential fixture contains inadmissible proof", err)
				}
				for _, pair := range pairs {
					a, b := got[pair[0]], got[pair[1]]
					if !causallyEquivalent(a, b, nil) || !positiveContinuity(a, b, nil) || a.Closed != nil || len(episodeIDs(a)) != 1 {
						t.Fatal("complete same-scope proof failed continuity or promoted proof authority")
					}
					for _, u := range a.uncertainties {
						if u.resolved == nil || len(u.resolved.uncertainties) == 0 && u.resolved.Closed == nil {
							t.Fatal("complete nested/derived closure was lost", u.Header.ID)
						}
					}
				}
				for key, source := range got {
					if source.ID != original[key].ID || source.Closed == nil && original[key].Closed != nil || source.Closed != nil && original[key].Closed == nil {
						t.Fatal("derived proof closure changed an original source", key)
					}
				}
				// Re-entry also protects already bound private pointer graphs.
				if _, err := capacityScopeDiff(t, got, facts, links); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestCapacityUncertaintyScopeControls(t *testing.T) {
	for _, original := range []map[string]Incident{nil, {}} {
		if _, err := capacityScopeDiff(t, original, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, foreign := range []string{"monitor", "generation"} {
		t.Run("nested-id-collision-"+foreign, func(t *testing.T) {
			facts, links := map[string]IncidentFact{}, map[string]EvidenceLink{}
			old := capacityScopeCommit(t, capacityScopeOriginal("collision", "m", "g", 1, 1), facts, links)
			middle := capacityScopeOriginal("middle", "m", "g", 3, 30)
			middle.uncertainties = []incidentUncertainty{capacityScopeReference(old, 3)}
			middle = capacityScopeCommit(t, middle, facts, links)
			owner := capacityScopeOriginal("owner", "m", "g", 3, 30)
			owner.uncertainties = []incidentUncertainty{capacityScopeReference(middle, 3)}
			collision := capacityScopeOriginal(old.ID, "m", "g", 1, 1)
			if foreign == "monitor" {
				collision.MonitorID = "foreign"
			} else {
				collision.Revision = "foreign"
			}
			original := map[string]Incident{owner.ID: owner, collision.ID: collision}
			missing := maps.Clone(links)
			delete(missing, old.EvidenceRoot)
			if got, err := capacityScopeDiff(t, original, facts, missing); err != nil || got == nil {
				t.Fatal("unknown nested proof rejected an unproved ID collision", err)
			}
			got, err := capacityScopeDiff(t, original, facts, links)
			if got != nil || err == nil || err.Error() != "Uncertainty boundary scope mismatch" {
				t.Fatal("nested cross-scope original ID collision lost its rejection", err)
			}
		})
	}
	t.Run("foreign-predecessor-is-not-proof", func(t *testing.T) {
		facts, links := map[string]IncidentFact{}, map[string]EvidenceLink{}
		old := capacityScopeCommit(t, capacityScopeOriginal("old", "m", "g", 3, 10), facts, links)
		closed := capacityScopeOriginal("closed", "m", "g", 3, 10)
		closed.Closed, closed.ResolvedAt, closed.Predecessors = map[string]int64{"a": 4, "b": 4}, Ptr(int64(20)), []string{"foreign-only"}
		closed = capacityScopeCommit(t, closed, facts, links)
		foreign := capacityScopeOriginal("foreign-only", "other", "g", 1, 1)
		foreign.Closed, foreign.ResolvedAt = map[string]int64{"a": 2, "b": 2}, Ptr(int64(5))
		owner := capacityScopeOriginal("owner", "m", "g", 5, 30)
		owner.uncertainties = []incidentUncertainty{capacityScopeReference(old, 5), capacityScopeReference(closed, 5)}
		got, err := capacityScopeDiff(t, map[string]Incident{owner.ID: owner, foreign.ID: foreign}, facts, links)
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range got[owner.ID].uncertainties {
			if u.Header.ID == old.ID && (u.resolved == nil || u.resolved.Closed != nil) {
				t.Fatal("foreign predecessor supplied same-scope closure")
			}
		}
	})
}

// Raw local incidents bypass the IncidentFacts admission check. Preserve old
// behavior with a global fallback for a top-level foreign header, or explicitly
// decide that this unvalidated internal input is outside the wrapper contract.
// This is a concrete compatibility control, not an admitted-peer exploit.
func TestCapacityUncertaintyScopeRawForeignHeader(t *testing.T) {
	facts, links := map[string]IncidentFact{}, map[string]EvidenceLink{}
	old := capacityScopeCommit(t, capacityScopeOriginal("foreign-old", "foreign", "g", 1, 1), facts, links)
	closed := capacityScopeOriginal("foreign-closed", "foreign", "g", 1, 1)
	closed.Closed, closed.ResolvedAt = map[string]int64{"a": 2, "b": 2}, Ptr(int64(20))
	owner := capacityScopeOriginal("raw-owner", "local", "g", 3, 30)
	owner.uncertainties = []incidentUncertainty{capacityScopeReference(old, 3)}
	original := map[string]Incident{owner.ID: owner, closed.ID: closed}
	want, err := resolveUncertaintyScope(original, original, facts, links)
	if err != nil || want[owner.ID].uncertainties[0].resolved == nil || want[owner.ID].uncertainties[0].resolved.Closed == nil {
		t.Fatal("control lacks the old global foreign-scope closure", err)
	}
	n := &Node{known: map[string]bool{"a": true, "b": true}}
	header := uncertainHeader(old)
	fact := IncidentFact{MonitorID: owner.MonitorID, Revision: owner.Revision, IncidentID: owner.ID, Kind: "uncertainty", ObserverID: "a", NodeID: "a", Run: 3, Boundary: &header}
	fact.ID = incidentFactID(fact)
	if err := n.validateCausalFacts(Snapshot{IncidentFacts: []IncidentFact{fact}}); err == nil {
		t.Fatal("invalid foreign-header control unexpectedly passed fact admission")
	}
	header.MonitorID = owner.MonitorID
	fact.ID = incidentFactID(fact)
	if err := n.validateCausalFacts(Snapshot{IncidentFacts: []IncidentFact{fact}}); err != nil {
		t.Fatal("same-scope header control failed fact admission", err)
	}
	if err := n.validateCausalFacts(Snapshot{Incidents: []Incident{owner}}); err != nil {
		t.Fatal("raw local incident control unexpectedly failed fact admission", err)
	}
	if _, err := capacityScopeDiff(t, original, facts, links); err != nil {
		t.Fatal(err)
	}
}

// A same-scope top-level header can hydrate a private, unvalidated fact whose
// own header crosses scope. Scanning only original headers misses this case.
// The wrapper must preserve the global oracle, including delayed nested proof;
// peer and persisted fact admission still reject this private control.
func TestCapacityUncertaintyScopeNestedRawForeignHeader(t *testing.T) {
	for _, foreign := range []string{"monitor", "generation"} {
		for _, depth := range []int{1, 3} {
			t.Run(fmt.Sprintf("%s/depth=%d", foreign, depth), func(t *testing.T) {
				facts, links := map[string]IncidentFact{}, map[string]EvidenceLink{}
				foreignMonitor, foreignGeneration := "local", "g"
				if foreign == "monitor" {
					foreignMonitor = "foreign"
				} else {
					foreignGeneration = "foreign"
				}
				old := capacityScopeCommit(t, capacityScopeOriginal("nested-foreign-old", foreignMonitor, foreignGeneration, 1, 1), facts, links)
				closed := capacityScopeOriginal("nested-foreign-closed", foreignMonitor, foreignGeneration, 1, 1)
				closed.Closed, closed.ResolvedAt = map[string]int64{"a": 2, "b": 2}, Ptr(int64(20))
				nested := old
				for level := range depth {
					middle := capacityScopeOriginal(fmt.Sprintf("nested-local-%d", level), "local", "g", 3, 30)
					middle.uncertainties = []incidentUncertainty{capacityScopeReference(nested, 3)}
					nested = capacityScopeCommit(t, middle, facts, links)
				}
				owner := capacityScopeOriginal("nested-owner", "local", "g", 3, 30)
				owner.uncertainties = []incidentUncertainty{capacityScopeReference(nested, 3)}
				owner = capacityScopeCommit(t, owner, facts, links)
				original := map[string]Incident{owner.ID: owner, closed.ID: closed}
				for _, source := range original {
					for _, u := range source.uncertainties {
						if u.Header.MonitorID != source.MonitorID || u.Header.Revision != source.Revision {
							t.Fatal("nested control accidentally exercises the top-level fallback")
						}
					}
				}
				n := &Node{known: map[string]bool{"a": true, "b": true}}
				if err := n.validateCausalFacts(Snapshot{IncidentFacts: slices.Collect(maps.Values(facts)), EvidenceLinks: slices.Collect(maps.Values(links))}); err == nil {
					t.Fatal("private nested foreign header unexpectedly passed fact admission")
				}
				missing := maps.Clone(links)
				delete(missing, old.EvidenceRoot)
				for _, stage := range []struct {
					name     string
					links    map[string]EvidenceLink
					complete bool
				}{{"pending", missing, false}, {"complete", links, true}} {
					t.Run(stage.name, func(t *testing.T) {
						got, err := capacityScopeDiff(t, original, facts, stage.links)
						if err != nil {
							t.Fatal(err)
						}
						cursor := got[owner.ID]
						for level := range depth {
							if len(cursor.uncertainties) != 1 || cursor.uncertainties[0].resolved == nil {
								t.Fatal("complete local nesting was lost", level)
							}
							cursor = *cursor.uncertainties[0].resolved
							if cursor.MonitorID != "local" || cursor.Revision != "g" {
								t.Fatal("foreign header occurs at the wrong nesting depth", level)
							}
						}
						if len(cursor.uncertainties) != 1 {
							t.Fatal("nested foreign reference was lost")
						}
						boundary := cursor.uncertainties[0].resolved
						if stage.complete {
							if boundary == nil || boundary.Closed == nil || boundary.MonitorID != foreignMonitor || boundary.Revision != foreignGeneration {
								t.Fatal("control lost the old global nested foreign closure")
							}
						} else if boundary != nil {
							t.Fatal("missing nested proof became authoritative")
						}
					})
				}
			})
		}
	}
}
