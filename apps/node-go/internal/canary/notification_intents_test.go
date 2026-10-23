package canary

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"testing"
)

func TestPagedIntentBeyondIncidentSummarySurvivesDelayedPagesAndRestart(t *testing.T) {
	r := webhook(t)
	a, err := New(Options{NodeID: "a", DBPath: ":memory:", Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Peers: []Peer{{ID: "b", URL: "http://127.0.0.1:1"}}, NotificationDelayMs: Ptr(int64(0))})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Stop()
	resource, err := a.writeResource(resourceChannel, "intent-channel", hookChannel("Observed destination", r.URL), false)
	if err != nil {
		t.Fatal(err)
	}
	in := input([]string{"a", "b"}, "http://127.0.0.1:1", "")
	in.NotificationIDs = []string{resource.ID}
	m, err := a.writeMonitor(in, nil, "intent-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	target := a.notificationTargets(m)[0]
	i := Incident{ID: "intent-episode", Aliases: []string{"intent-episode"}, MonitorID: m.ID, MonitorName: m.Name, Revision: m.Revision, OpenedAt: a.now() - 10000, ResolvedAt: Ptr(a.now() - 5000), Runs: map[string][]int64{"a": {1}, "b": {1}}, Closed: map[string]int64{"a": 2, "b": 2}, Predecessors: []string{}}
	for index := range 850 {
		i.DeliveryTargets = append(i.DeliveryTargets, fmt.Sprintf("0-old-route-%04d", index))
	}
	i.DeliveryTargets = append(i.DeliveryTargets, target.ID)
	a.saveEpisode(i)
	i = a.episodes[i.ID]
	if len(i.DeliveryTargets) != inlineDeliveryTargets || slices.Contains(i.DeliveryTargets, target.ID) || !a.indexedDeliveryIntent(i, target.ID) {
		t.Fatal("fixture did not produce a durable audience beyond the summary")
	}
	// Every outgoing page, including the full audience, must validate alone.
	count := 0
	for cursor := ""; ; {
		page, err := a.snapshotPage(cursor, "")
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(page)
		if _, err = a.validateSnapshot(raw, a.ID); err != nil {
			t.Fatal("self-produced intent page failed validation", err)
		}
		count += len(page.Intents)
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	if count != 851 {
		t.Fatal("intent paging lost records", count)
	}
	o := Options{NodeID: "b", DBPath: filepath.Join(t.TempDir(), "receiver.db"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Peers: []Peer{{ID: "a", URL: "http://127.0.0.1:1"}}, NotificationDelayMs: Ptr(int64(0)), SyncIntervalMs: Ptr(int64(60)), PeerTimeoutMs: Ptr(int64(60))}
	b, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if b != nil {
			_ = b.Stop()
		}
	}()
	// A closed incident may precede its independent intent page.
	if err = b.mergeSnapshot(Snapshot{Versions: []Version{{Monitor: m, Ancestors: []string{}}}, Resources: a.resources[resourceKey(resourceChannel, resource.ID)], Incidents: []Incident{i}, RevisionEdges: slices.Collect(maps.Values(a.revisionEdges)), IncidentFacts: slices.Collect(maps.Values(a.incidentFacts)), EvidenceLinks: slices.Collect(maps.Values(a.evidenceLinks))}); err != nil {
		t.Fatal(err)
	}
	for _, node := range []string{"a", "b"} {
		b.samples[m.ID+"/"+node] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: node, State: "up", CheckedAt: b.now(), Message: "fixture up"}, Sequence: 2, Revision: m.Revision}
	}
	b.mu.Lock()
	b.evaluate()
	b.mu.Unlock()
	b.wg.Wait()
	if r.count("") != 0 {
		t.Fatal("missing intent was invented from current configuration")
	}
	fact := deliveryIntent(m.ID, i.ID, target.ID)
	if err = b.mergeSnapshot(Snapshot{Intents: []NotificationIntent{fact}}); err != nil {
		t.Fatal(err)
	}
	b.wg.Wait()
	// Persisted intent is enough to resume owed delivery after a process restart.
	if err = b.Stop(); err != nil {
		t.Fatal(err)
	}
	b, err = New(o)
	if err != nil {
		t.Fatal(err)
	}
	if !b.indexedDeliveryIntent(b.episodes[i.ID], target.ID) {
		t.Fatal("audience beyond the summary was lost across restart")
	}
	for _, node := range []string{"a", "b"} {
		b.samples[m.ID+"/"+node] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: node, State: "up", CheckedAt: b.now(), Message: "fixture up"}, Sequence: 2, Revision: m.Revision}
	}
	// Node b is the second sender; move its deterministic window into the past.
	for range 2 {
		b.mu.Lock()
		b.evaluate()
		b.mu.Unlock()
		b.wg.Wait()
	}
	if r.count("down") != 1 || r.count("recovery") != 1 {
		t.Fatal("late audience fact lost its observed outage/recovery", r.all())
	}
}

func TestDeliveryIntentAndEpisodeCommitAtomically(t *testing.T) {
	n := reviewNotificationNode(t)
	_, err := n.store.db.Exec(`CREATE TRIGGER intent_write_failure BEFORE INSERT ON records WHEN NEW.category='notificationIntents' BEGIN SELECT RAISE(FAIL,'injected intent failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	i := Incident{ID: "atomic-episode", Aliases: []string{"atomic-episode"}, MonitorID: "atomic-monitor", MonitorName: "Atomic", Revision: "atomic-revision", OpenedAt: n.now(), Runs: map[string][]int64{"a": {1}}, Predecessors: []string{}, DeliveryTargets: []string{"atomic-destination"}}
	n.saveEpisode(i)
	if n.fault == nil || len(n.episodes) != 0 || len(n.intents) != 0 {
		t.Fatal("failed intent write changed in-memory coordination")
	}
	if _, ok, err := loadOne[Incident](n.store, "incidents", i.ID); err != nil || ok {
		t.Fatal("failed intent write committed the episode", err)
	}
	fact := deliveryIntent(i.MonitorID, i.ID, "atomic-destination")
	if _, ok, err := loadOne[NotificationIntent](n.store, "notificationIntents", fact.ID); err != nil || ok {
		t.Fatal("failed intent write committed audience", err)
	}
}
