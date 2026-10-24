package canary

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func fullReviewCount(r *receiver, monitor, kind string) int {
	count := 0
	for _, d := range r.all() {
		if d.MonitorID == monitor && d.Kind == kind {
			count++
		}
	}
	return count
}

// No node is started. Filling the worker semaphore prevents child I/O before
// recovery without racing a provider response or relying on wall-clock sleeps.
func TestFullReviewNamedDestinationOwedAfterSuppressedSiblingRecovers(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory", true: "restart"}[restart], func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(maintenanceClock(11, 58))
			path := filepath.Join(t.TempDir(), "destinations.db")
			n := maintenanceNode(t, &clock, path)
			a, b := webhook(t), webhook(t)
			b.status.Store(503)
			channels := []string{}
			for index, r := range []*receiver{a, b} {
				resource, err := n.writeResource(resourceChannel, "", hookChannel(fmt.Sprintf("Route %d", index), r.URL), false)
				if err != nil {
					t.Fatal(err)
				}
				channels = append(channels, resource.ID)
			}
			in := input([]string{"a"}, "http://127.0.0.1:1", "")
			in.Name, in.NotificationIDs = "Parent", channels
			parent := maintenanceReviewMonitor(t, n, in, nil)
			group, err := n.writeGroup(GroupInput{Name: "Dependency", NodeIDs: []string{"a"}, OutageMonitorID: parent.ID, GraceMs: 0}, nil, "", false)
			if err != nil {
				t.Fatal(err)
			}
			in.Name, in.GroupID = "Child", group.ID
			child := maintenanceReviewMonitor(t, n, in, nil)
			maintenanceReviewSample(n, parent, "down", 1, Ptr(int64(1)))
			if fullReviewCount(a, parent.ID, "down") != 1 || fullReviewCount(b, parent.ID, "down") != 1 {
				t.Fatal("fixture did not attempt both parent destinations")
			}
			targets := n.notificationTargets(child)
			if len(targets) != 2 {
				t.Fatal("fixture needs two named destinations")
			}
			if held, _, _ := n.suppressionForTarget(child, targets[0]); held != group.ID {
				t.Fatal("accepted parent A did not cover child A")
			}
			if held, _, _ := n.suppressionForTarget(child, targets[1]); held != "" {
				t.Fatal("unaccepted parent B incorrectly covers child B")
			}
			for range cap(n.notificationWorkers) {
				n.notificationWorkers <- struct{}{}
			}
			clock.Add(1)
			maintenanceReviewSample(n, child, "down", 1, Ptr(int64(1)))
			var episode Incident
			for _, i := range n.episodes {
				if i.MonitorID == child.ID {
					episode = i
				}
			}
			if episode.ID == "" || !n.hasDeliveryIntent(episode, targets[1].ID) || !n.hasLocalIncidentDelivery(episode, targets[1].ID) || hasReceipt(n.forEpisode(episode), "down") {
				t.Fatal("fixture did not capture an undelivered local child B obligation")
			}
			clock.Add(1)
			maintenanceReviewSample(n, child, "up", 2, nil)
			if n.episodes[episode.ID].Closed == nil {
				t.Fatal("fixture child did not recover before delivery")
			}
			for range cap(n.notificationWorkers) {
				<-n.notificationWorkers
			}
			if restart {
				if err := n.Stop(); err != nil {
					t.Fatal(err)
				}
				n = maintenanceNode(t, &clock, path)
				if !n.hasLocalIncidentDelivery(n.episodes[episode.ID], targets[1].ID) {
					t.Fatal("restart lost the independently persisted B obligation")
				}
			}
			b.status.Store(204)
			// Fake time remains before parent B's deterministic 1s retry, so it
			// cannot acquire parent coverage while the child's owed pair drains.
			maintenanceReviewSample(n, parent, "down", 2, Ptr(int64(1)))
			maintenanceReviewSample(n, child, "up", 3, nil)
			maintenanceReviewSample(n, child, "up", 4, nil)
			if fullReviewCount(b, parent.ID, "down") != 1 {
				t.Fatal("fixture retried parent B before checking the child")
			}
			if down, recovery := fullReviewCount(b, child.ID, "down"), fullReviewCount(b, child.ID, "recovery"); down != 1 || recovery != 1 {
				t.Errorf("uncovered B lost owed pair after recovery: down=%d recovery=%d", down, recovery)
			}
			clock.Add(1)
			maintenanceReviewSample(n, parent, "up", 3, nil)
			maintenanceReviewSample(n, child, "up", 5, nil)
			maintenanceReviewSample(n, child, "up", 6, nil)
			if down, recovery := fullReviewCount(a, child.ID, "down"), fullReviewCount(a, child.ID, "recovery"); down != 0 || recovery != 0 {
				t.Errorf("covered A replayed a suppressed child pair: down=%d recovery=%d", down, recovery)
			}
		})
	}
}

func TestFullReviewIncompleteMaintenanceCannotCompleteOutage(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(11, 58))
	n := maintenanceReviewNode(t, &clock, ":memory:")
	r := webhook(t)
	m := maintenanceReviewMonitor(t, n, input([]string{"a"}, "http://127.0.0.1:1", r.URL), nil)
	first, second := plannedWork(), plannedWork()
	first.Schedule.DurationMinutes = 6
	second.Name, second.Schedule.StartTime, second.Schedule.DurationMinutes = "Awaiting parent", "12:05", 5
	maintenanceReviewWork(t, n, first)
	data, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	resource := Resource{Kind: resourceMaintenance, ID: "unproven-maintenance", Revision: "unproven-child", UpdatedAt: clock.Load(), Data: data}
	edge := newRevisionEdge(resourceMaintenance, resource.ID, resource.Revision, resource, []string{"withheld-parent"}, "")
	v := ResourceVersion{Resource: resource, Ancestors: edge.Parents, EdgeID: edge.ID}
	maintenanceReviewMerge(t, n, Snapshot{Resources: []ResourceVersion{v}, RevisionEdges: []RevisionEdge{edge}})
	if n.resourceVersionReady(v) || n.currentMaintenancePlans()[maintenancePlanKey(second.Schedule)] == nil {
		t.Fatal("fixture needs an unready revision with a precompiled plan")
	}
	clock.Store(maintenanceClock(12, 1))
	maintenanceReviewSample(n, m, "down", 1, Ptr(int64(1)))
	if maintenanceReviewEpisode(t, n).MaintenanceStartedAt == nil || r.count("") != 0 {
		t.Fatal("ready first window did not hold the initial outage")
	}
	// No observation occurs at the boundary: only authoritative schedules may
	// prove that the entire interval between these observations was planned.
	clock.Store(maintenanceClock(12, 9))
	found := false
	for _, view := range n.maintenanceViews() {
		if view.Maintenance.ID == resource.ID {
			found = true
			if view.Active || !view.ConfigConflict {
				t.Fatal("incomplete maintenance was applied as an active schedule")
			}
		}
	}
	if !found {
		t.Fatal("unproven maintenance disappeared instead of awaiting evidence")
	}
	maintenanceReviewSample(n, m, "up", 2, nil)
	i := maintenanceReviewEpisode(t, n)
	if i.Closed == nil {
		t.Fatal("fixture outage did not close")
	}
	if i.MaintenanceComplete {
		t.Error("incomplete parent proof permanently completed an uncovered outage")
	}
	clock.Store(maintenanceClock(12, 10))
	maintenanceReviewSample(n, m, "up", 3, nil)
	maintenanceReviewSample(n, m, "up", 4, nil)
	if r.count("down") != 1 || r.count("recovery") != 1 {
		t.Errorf("incomplete schedule discarded owed pair: down=%d recovery=%d", r.count("down"), r.count("recovery"))
	}
}

func TestFullReviewRestoreActivationAcrossWirePageBoundary(t *testing.T) {
	for _, count := range []int{512, 513} {
		t.Run(fmt.Sprintf("heads-%d", count), func(t *testing.T) {
			o := originRestoreReviewOptions(t)
			o.Peers = []Peer{{ID: "b", URL: "http://127.0.0.1:1"}}
			n := originRestoreReviewNode(t, o)
			in := input([]string{"a"}, "http://127.0.0.1:1", "")
			if err := validateMonitor(&in, n.known); err != nil {
				t.Fatal(err)
			}
			// Replicated partition-local creation can exceed the 200-monitor
			// management limit. Admit small valid wire pages, never one >512 page.
			for start := 0; start < count; start += 128 {
				page := Snapshot{}
				for index := start; index < min(start+128, count); index++ {
					id := fmt.Sprintf("review-%04d", index)
					m := Monitor{MonitorInput: in, ID: id, Revision: id + "-root", UpdatedAt: n.now()}
					edge := newRevisionEdge("monitor", id, m.Revision, m, []string{}, m.Revision)
					page.Versions = append(page.Versions, Version{Monitor: m, Ancestors: edge.Parents, EdgeID: edge.ID})
					page.RevisionEdges = append(page.RevisionEdges, edge)
				}
				reviewRound3Merge(t, n, page)
			}
			if len(n.heads) != count {
				t.Fatal("replicated configuration lost heads")
			}
			beforeHeads, beforeEdges := n.heads, n.revisionEdges
			floor, err := n.nextProbeSequence()
			if err != nil {
				t.Fatal(err)
			}
			staged := originRestoreReviewStage(t, o.DBPath)
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			checkUnchanged := func() {
				t.Helper()
				s, err := openStore(staged)
				if err != nil {
					t.Fatal(err)
				}
				defer s.close()
				heads, headErr := loadAll[[]Version](s, "heads")
				edges, edgeErr := loadAll[RevisionEdge](s, "revisionEdges")
				quarantined, exists, quarantineErr := loadOne[bool](s, "settings", "restoreQuarantined")
				_, activated, activationErr := loadOne[RestoreActivation](s, "settings", "restoreActivation")
				if headErr != nil || edgeErr != nil || quarantineErr != nil || activationErr != nil || !exists || !quarantined || activated || !equalJSON(heads, beforeHeads) || !equalJSON(edges, beforeEdges) {
					t.Fatal("failed reset published partial heads/evidence or released quarantine", headErr, edgeErr, quarantineErr, activationErr)
				}
			}
			setFailure := func(install bool) {
				t.Helper()
				s, err := openStore(staged)
				if err != nil {
					t.Fatal(err)
				}
				defer s.close()
				statement := "DROP TRIGGER full_review_reset_failure"
				if install {
					statement = `CREATE TRIGGER full_review_reset_failure BEFORE UPDATE ON records WHEN NEW.category='heads' AND NEW.id='review-0001' BEGIN SELECT RAISE(FAIL,'fixture reset failure'); END`
				}
				if _, err := s.db.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			setFailure(true)
			if _, err := ActivateRestoredDatabase(context.Background(), staged, o.IdentityDirectory); err == nil {
				t.Fatal("injected reset failure was acknowledged")
			}
			checkUnchanged()
			o.DBPath = staged
			if unsafe, err := New(o); err == nil {
				_ = unsafe.Stop()
				t.Fatal("failed activation permitted ordinary startup")
			}
			setFailure(false)
			result, err := ActivateRestoredDatabase(context.Background(), staged, o.IdentityDirectory)
			if err != nil {
				checkUnchanged()
				t.Fatalf("valid %d-head restore could not activate: %v", count, err)
			}
			if result.MonitorsReset != count || result.NodeID != "a" || result.SequenceFloor <= floor {
				t.Fatal("activation lost monitors or retained origin continuity", result, floor)
			}
			restored := originRestoreReviewNode(t, o)
			for id, heads := range beforeHeads {
				old, current := heads[0].Monitor, restored.current(id)
				if current == nil || checkGeneration(*current) == checkGeneration(old) || current.Target != old.Target || !restored.graph.reaches("monitor", id, current.Revision, old.Revision) {
					t.Fatalf("activation did not preserve/reset monitor %s atomically: %+v", id, current)
				}
			}
			quarantined, exists, quarantineErr := loadOne[bool](restored.store, "settings", "restoreQuarantined")
			marker, activated, activationErr := loadOne[RestoreActivation](restored.store, "settings", "restoreActivation")
			if quarantineErr != nil || activationErr != nil || !exists || quarantined || !activated || !equalJSON(marker, result) {
				t.Fatal("successful activation did not durably publish marker and quarantine release")
			}
		})
	}
}
