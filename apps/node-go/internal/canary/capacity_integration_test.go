package canary

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
)

// Root moves this fixture into internal/canary only after combining the saved
// capacity candidate with the dependency-completion and source-identity repairs.
// Nodes never Start. A tick's sweep sees the old sample; its controlled probe
// then commits and evaluates the new sample before this helper returns.
// An empty monitor ID requests just the next full scheduler sweep.
func capacityIntegrationTick(t *testing.T, n *Node, monitorID, state string) {
	t.Helper()
	n.mu.Lock()
	before := n.samples[monitorID+"/"+n.ID].Sequence
	for id := range n.heads {
		n.nextProbe[id] = maxSafe
	}
	n.nextSync = maxSafe // These fixtures have no peers or background scheduler.
	if monitorID != "" {
		n.nextProbe[monitorID] = 0
		n.probe = func(_ context.Context, m Monitor) ProbeResult {
			if m.ID != monitorID {
				t.Errorf("unexpected probe: got %s want %s", m.ID, monitorID)
			}
			return ProbeResult{State: state, Message: "controlled capacity probe"}
		}
	}
	n.mu.Unlock()
	n.tick()
	n.wg.Wait()
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.started || len(n.probing) != 0 || n.fault != nil {
		t.Fatalf("controlled tick did not settle: started=%v probing=%d fault=%v", n.started, len(n.probing), n.fault)
	}
	if monitorID != "" {
		s := n.samples[monitorID+"/"+n.ID]
		if s.Sequence <= before || s.State != state || s.CheckedAt != n.now() {
			t.Fatalf("tick did not commit the requested probe: %+v", s)
		}
	}
}

func TestCapacityIntegrationTickCompletesOnlyCoveredDestination(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(fmt.Sprintf("restart=%v", restart), func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(11, 58))
			path := filepath.Join(t.TempDir(), "tick-completion.db")
			n := maintenanceNode(t, &clock, path)
			a, b := webhook(t), webhook(t)
			b.status.Store(503)
			channels := []string{}
			for index, r := range []*receiver{a, b} {
				c, err := n.writeResource(resourceChannel, "", hookChannel(fmt.Sprintf("Capacity route %d", index), r.URL), false)
				if err != nil {
					t.Fatal(err)
				}
				channels = append(channels, c.ID)
			}
			in := input([]string{"a"}, "http://127.0.0.1:1", "")
			in.Name, in.NotificationIDs = "Parent", channels
			parent := maintenanceReviewMonitor(t, n, in, nil)
			g, err := n.writeGroup(GroupInput{Name: "Capacity dependency", NodeIDs: []string{"a"}, OutageMonitorID: parent.ID}, nil, "", false)
			if err != nil {
				t.Fatal(err)
			}
			in.Name, in.GroupID = "Child", g.ID
			child := maintenanceReviewMonitor(t, n, in, nil)
			var targetA, targetB notificationTarget
			for _, target := range n.notificationTargets(child) {
				if target.ChannelID == channels[0] {
					targetA = target
				}
				if target.ChannelID == channels[1] {
					targetB = target
				}
			}
			if targetA.ID == "" || targetB.ID == "" || targetA.ID == targetB.ID {
				t.Fatal("fixture needs two distinct named destinations")
			}
			capacityIntegrationTick(t, n, parent.ID, "down")
			if fullReviewCount(a, parent.ID, "down") != 1 || fullReviewCount(b, parent.ID, "down") != 1 {
				t.Fatal("fixture did not attempt both parent destinations")
			}
			if held, _, _ := n.suppressionForTarget(child, targetA); held != g.ID {
				t.Fatal("accepted parent A did not cover child A")
			}
			if held, _, _ := n.suppressionForTarget(child, targetB); held != "" {
				t.Fatal("failed parent B incorrectly covered child B")
			}
			release := fullReviewFollowupBlock(n)
			defer release()
			clock.Add(1)
			capacityIntegrationTick(t, n, child.ID, "down")
			opened := fullReviewControlEpisode(t, n, child)
			if opened.Closed != nil || !n.hasLocalIncidentDelivery(opened, targetB.ID) || !n.hasDeliveryIntent(opened, targetB.ID) || hasReceipt(n.forEpisode(opened), "down") {
				t.Fatal("fixture lacks an undelivered, locally captured child B obligation")
			}
			clock.Add(1)
			capacityIntegrationTick(t, n, child.ID, "up")
			// Do not call evaluate, a sample helper, or another tick here. Only
			// the real probe completion could have marked this observed closure.
			checkCompletion := func() {
				t.Helper()
				closed := fullReviewControlEpisode(t, n, child)
				if closed.Closed == nil || closed.ResolvedAt == nil || closed.ID != opened.ID {
					t.Fatal("probe did not close the original child lifetime", closed)
				}
				id := attemptIdentity(child.ID, closed.ID, "down", 0, targetA.ID)
				marker := n.attempts[id]
				if !completedDependencyAttempt(marker) || marker.Held || marker.NextAt != 0 || marker.SuppressionBoundary != dependencyBoundary(closed) || !n.targetSuppressionComplete(closed, targetA.ID) {
					t.Errorf("probe closure did not complete covered A at its exact boundary: %+v", marker)
				}
				persisted, exists, err := loadOne[attempt](n.store, "attempts", id)
				if err != nil || !exists || !equalJSON(persisted, marker) {
					t.Fatal("probe closure marker was not durable", exists, err)
				}
				if n.targetSuppressionComplete(closed, targetB.ID) || !n.hasDeliveryIntent(closed, targetB.ID) || !n.hasLocalIncidentDelivery(closed, targetB.ID) || !n.owedClosedLocalDown(closed, targetB.ID, receiptsForTarget(n.forEpisode(closed), targetB.ID)) {
					t.Error("probe closure completed or discarded uncovered B's owed down")
				}
				if fullReviewCount(a, child.ID, "down") != 0 || fullReviewCount(b, child.ID, "down") != 0 {
					t.Fatal("occupied worker pool did not keep child delivery pending")
				}
			}
			checkCompletion()
			release()
			if restart {
				if err := n.Stop(); err != nil {
					t.Fatal(err)
				}
				n = maintenanceNode(t, &clock, path)
				checkCompletion()
			}
			b.status.Store(204)
			// Fake time remains before parent B's 1s retry, so B stays uncovered
			// while two scheduler sweeps drain its child's down/recovery pair.
			capacityIntegrationTick(t, n, "", "")
			capacityIntegrationTick(t, n, "", "")
			if fullReviewCount(b, parent.ID, "down") != 1 {
				t.Fatal("fixture retried parent B before checking the child's owed pair")
			}
			if down, recovery := fullReviewCount(b, child.ID, "down"), fullReviewCount(b, child.ID, "recovery"); down != 1 || recovery != 1 {
				t.Errorf("uncovered B lost its owed pair: down=%d recovery=%d", down, recovery)
			}
			clock.Add(1)
			capacityIntegrationTick(t, n, parent.ID, "up")
			capacityIntegrationTick(t, n, "", "")
			capacityIntegrationTick(t, n, "", "")
			if down, recovery := fullReviewCount(a, child.ID, "down"), fullReviewCount(a, child.ID, "recovery"); down != 0 || recovery != 0 {
				t.Errorf("covered A replayed after parent recovery: down=%d recovery=%d", down, recovery)
			}
			if down, recovery := fullReviewCount(b, child.ID, "down"), fullReviewCount(b, child.ID, "recovery"); down != 1 || recovery != 1 {
				t.Errorf("parent recovery replayed B's completed pair: down=%d recovery=%d", down, recovery)
			}
		})
	}
}

func TestCapacityIntegrationParentRecoveryReleasesChildOnFullTick(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(11, 58))
	n, r := maintenanceNode(t, &clock, ":memory:"), webhook(t)
	parent, child := fullReviewControlMonitors(t, n, r.URL, 0)
	capacityIntegrationTick(t, n, parent.ID, "down")
	capacityIntegrationTick(t, n, child.ID, "down")
	i := fullReviewControlEpisode(t, n, child)
	target := n.notificationTargets(child)[0]
	if held, _, _ := n.suppressionForTarget(child, target); held != child.GroupID || fullReviewCount(r, child.ID, "down") != 0 {
		t.Fatal("fixture child was not held by accepted parent coverage")
	}
	clock.Add(1)
	capacityIntegrationTick(t, n, parent.ID, "up")
	if fullReviewCount(r, child.ID, "down") != 0 {
		t.Fatal("single-monitor completion unexpectedly swept the dependent child")
	}
	childSequence := n.samples[child.ID+"/a"].Sequence
	capacityIntegrationTick(t, n, "", "")
	if down, recovery := fullReviewCount(r, child.ID, "down"), fullReviewCount(r, child.ID, "recovery"); down != 1 || recovery != 0 {
		t.Fatalf("full tick did not release the still-open child: down=%d recovery=%d", down, recovery)
	}
	if n.samples[child.ID+"/a"].Sequence != childSequence || fullReviewControlEpisode(t, n, child).Closed != nil || n.targetSuppressionComplete(i, target.ID) {
		t.Fatal("release required an extra child probe or incorrectly completed its lifetime")
	}
	clock.Add(1)
	capacityIntegrationTick(t, n, child.ID, "up")
	if fullReviewCount(r, child.ID, "down") != 1 || fullReviewCount(r, child.ID, "recovery") != 1 {
		t.Fatal("released child lost its ordinary recovery")
	}
}

func capacityIntegrationOriginal(t *testing.T, n *Node, want Incident, targets []notificationTarget) {
	t.Helper()
	got, complete, err := hydrateIncident(n.incidentSources[want.ID], n.incidentFacts, n.evidenceLinks)
	got.EvidenceRoot, want.EvidenceRoot = "", ""
	if err != nil || !complete || !equalJSON(got, want) || !equalJSON(combineUncertainties(got.uncertainties, nil), combineUncertainties(want.uncertainties, nil)) {
		t.Fatalf("retained original no longer hydrates with the expected evidence: complete=%v err=%v got=%+v want=%+v", complete, err, got, want)
	}
	projection, exists := n.episodes[want.Aliases[0]]
	if len(n.incidentSources) != 1 || len(n.pendingIncidents) != 0 || !exists || projection.ID == want.ID || !slices.Contains(episodeIDs(projection), want.ID) {
		t.Fatal("explicit earlier alias changed original identity or lost its projection")
	}
	for _, target := range targets {
		wanted := slices.Contains(want.DeliveryTargets, target.ID)
		if n.hasDeliveryIntent(got, target.ID) != wanted || n.hasLocalIncidentDelivery(got, target.ID) != wanted {
			t.Fatalf("original lost or manufactured a destination obligation: destination=%s wanted=%v", target.ID, wanted)
		}
		if wanted {
			key := deliveryIntent(want.MonitorID, want.ID, target.ID).ID
			marker := n.localIncidentDeliveries[key]
			if !equalJSON(marker, localIncidentDelivery{MonitorID: want.MonitorID, IncidentID: want.ID, DestinationID: target.ID, Revision: want.Revision, ObserverID: "a", NodeID: "a", Run: 1}) {
				t.Fatal("local provenance no longer names the exact original down run", marker)
			}
		}
	}
}

func TestCapacityIntegrationExplicitAliasOriginalNoopAndChanges(t *testing.T) {
	for _, closedDB := range []bool{false, true} {
		for _, change := range []string{"none", "missing-local-marker", "new-local-target", "new-run", "new-uncertainty", "closure"} {
			t.Run(fmt.Sprintf("closed-db=%v/%s", closedDB, change), func(t *testing.T) {
				var clock atomic.Int64
				clock.Store(maintenanceClock(11, 58))
				path := filepath.Join(t.TempDir(), "aliased-original.db")
				n := maintenanceNode(t, &clock, path)
				channels := []string{}
				for _, name := range []string{"first", "added"} {
					c, err := n.writeResource(resourceChannel, "", hookChannel(name, "http://127.0.0.1:1/"+name), false)
					if err != nil {
						t.Fatal(err)
					}
					channels = append(channels, c.ID)
				}
				in := input([]string{"a"}, "http://127.0.0.1:1", "")
				in.NotificationIDs = channels
				m := maintenanceReviewMonitor(t, n, in, nil)
				targets := make([]notificationTarget, 2)
				for _, target := range n.notificationTargets(m) {
					for index, channel := range channels {
						if target.ChannelID == channel {
							targets[index] = target
						}
					}
				}
				if targets[0].ID == "" || targets[1].ID == "" {
					t.Fatal("fixture lacks named destinations")
				}
				// This fixture isolates saveEpisode, rather than scheduling delivery.
				// Only the first route was captured by this source's local down.
				n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: clock.Load()}, Revision: m.Revision, CheckGeneration: m.CheckGeneration, Sequence: 2, DownSince: Ptr(int64(1))}
				original := Incident{ID: "z-capacity-original", Aliases: []string{"z-capacity-original"}, MonitorID: m.ID, MonitorName: m.Name, Revision: checkGeneration(m), OpenedAt: clock.Load(), Runs: map[string][]int64{"a": {1}}, Predecessors: []string{}, DeliveryTargets: []string{targets[0].ID}}
				if err := n.applySnapshot(Snapshot{Incidents: []Incident{original}}); err != nil {
					t.Fatal(err)
				}
				// Update the same original with an explicit earlier alias. A second
				// source merely hiding this ID would miss the source-identity repair.
				original.Aliases = []string{"0-capacity-alias", original.ID}
				if err := n.applySnapshot(Snapshot{Incidents: []Incident{original}}); err != nil {
					t.Fatal(err)
				}
				capacityIntegrationOriginal(t, n, original, targets)
				update, complete, err := hydrateIncident(n.incidentSources[original.ID], n.incidentFacts, n.evidenceLinks)
				if err != nil || !complete {
					t.Fatal("cannot hydrate original for local save", complete, err)
				}
				switch change {
				case "missing-local-marker":
					key := deliveryIntent(m.ID, original.ID, targets[0].ID).ID
					if err := remove(n.store.db, "localIncidentDeliveries", key); err != nil {
						t.Fatal(err)
					}
					delete(n.localIncidentDeliveries, key)
				case "new-local-target":
					update.DeliveryTargets = unique(append(append([]string{}, update.DeliveryTargets...), targets[1].ID))
				case "new-run":
					update.Runs["a"] = []int64{1, 2}
				case "new-uncertainty":
					update.uncertainties = []incidentUncertainty{{Header: uncertainHistory{ID: "capacity-withheld-history", MonitorID: m.ID, Revision: checkGeneration(m), OpenedAt: clock.Load() - 20, Closed: map[string]int64{"a": 1}, ResolvedAt: Ptr(clock.Load() - 10), EvidenceRoot: digest("capacity-withheld-proof")}, ObserverID: "a", NodeID: "a", Run: 1}}
				case "closure":
					update.Closed, update.ResolvedAt = map[string]int64{"a": 3}, Ptr(clock.Load() + 1)
				}
				n.nextSync = maxSafe
				if closedDB {
					if err := n.store.db.Close(); err != nil {
						t.Fatal(err)
					}
				}
				n.saveEpisode(update)
				if closedDB {
					if (n.Error() == nil) != (change == "none") {
						t.Fatalf("closed-store persistence decision was wrong: change=%s fault=%v", change, n.Error())
					}
					return
				}
				if n.Error() != nil || (n.nextSync == maxSafe) != (change == "none") {
					t.Fatalf("local save did not distinguish unchanged original from changed evidence: change=%s fault=%v nextSync=%d", change, n.Error(), n.nextSync)
				}
				capacityIntegrationOriginal(t, n, update, targets)
				if err := n.Stop(); err != nil {
					t.Fatal(err)
				}
				n = maintenanceNode(t, &clock, path)
				capacityIntegrationOriginal(t, n, update, targets)
			})
		}
	}
}
