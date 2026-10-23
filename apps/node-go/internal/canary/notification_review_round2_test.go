package canary

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A temporary inability to resolve a route must not erase the fact that it was
// configured during an observed outage. All provider I/O stays on loopback.
func TestReviewRound2ClosedAudienceSurvivesTemporaryChannelUnavailability(t *testing.T) {
	for _, unavailable := range []string{"disabled", "conflicting"} {
		t.Run(unavailable, func(t *testing.T) {
			r := webhook(t)
			n := reviewNotificationNode(t)
			channel := hookChannel("Existing audience", r.URL)
			resource, err := n.writeResource(resourceChannel, "round2-channel", channel, false)
			if err != nil {
				t.Fatal(err)
			}
			in := input([]string{"a"}, "http://127.0.0.1:1", "")
			in.NotificationIDs = []string{resource.ID}
			m, err := n.writeMonitor(in, nil, "round2-monitor", false)
			if err != nil {
				t.Fatal(err)
			}
			target := n.notificationTargets(m)[0]
			// Observe a real outage and recovery while all incident workers are
			// occupied, as in the passing round 1 fast-outage guard.
			for range cap(n.notificationWorkers) {
				n.notificationWorkers <- struct{}{}
			}
			n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: n.now(), Message: "HTTP 503"}, Sequence: 1, Revision: m.Revision, DownSince: Ptr(int64(1))}
			n.evaluate()
			n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "up", CheckedAt: n.now(), Message: "HTTP 200"}, Sequence: 2, Revision: m.Revision}
			n.evaluate()
			for _, i := range n.episodes {
				if i.Closed == nil || !slices.Contains(i.DeliveryTargets, target.ID) {
					t.Fatal("fixture did not capture a closed outage with delivery intent")
				}
			}
			switch unavailable {
			case "disabled":
				channel.Enabled = false
				_, err = n.writeResource(resourceChannel, resource.ID, channel, false)
			case "conflicting":
				// Model a valid concurrent metadata edit received from a peer.
				other := n.resources[resourceKey(resourceChannel, resource.ID)][0]
				other.Resource.Revision = "round2-concurrent-channel-revision"
				other.EdgeID = "" // A new synthetic revision has its own proof identity.
				err = n.mergeSnapshot(Snapshot{Resources: []ResourceVersion{other}})
			}
			if err != nil {
				t.Fatal(err)
			}
			n.evaluate()
			channel.Enabled = true
			if _, err = n.writeResource(resourceChannel, resource.ID, channel, false); err != nil {
				t.Fatal(err)
			}
			for range cap(n.notificationWorkers) {
				<-n.notificationWorkers
			}
			for range 2 {
				n.mu.Lock()
				n.evaluate()
				n.mu.Unlock()
				n.wg.Wait()
			}
			if r.count("down") != 1 || r.count("recovery") != 1 {
				t.Fatalf("restoring the same route lost its observed outage/recovery: %+v", r.all())
			}
		})
	}
}

func TestReviewRound2ClosedAudienceSurvivesOutOfOrderConfiguration(t *testing.T) {
	n := reviewNotificationNode(t)
	channel := hookChannel("Delayed channel page", "http://127.0.0.1:1")
	data, err := json.Marshal(channel)
	if err != nil {
		t.Fatal(err)
	}
	r := ResourceVersion{Resource: Resource{Kind: resourceChannel, ID: "round2-delayed-channel", Revision: "round2-channel-revision", UpdatedAt: n.now(), Data: data}, Ancestors: []string{}}
	in := input([]string{"a"}, "http://127.0.0.1:1", "")
	in.NotificationIDs = []string{r.Resource.ID}
	m, err := n.writeMonitor(in, nil, "round2-delayed-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	target := targetsForChannel(channelOf(r.Resource, "admin"))[0]
	i := Incident{ID: "round2-delayed-incident", Aliases: []string{"round2-delayed-incident"}, MonitorID: m.ID, MonitorName: m.Name, Revision: checkGeneration(m), OpenedAt: n.now() - 10000, Runs: map[string][]int64{"a": {1}}, Predecessors: []string{}, Closed: map[string]int64{"a": 2}, ResolvedAt: Ptr(n.now() - 5000), DeliveryTargets: []string{target.ID}}
	// Independently mergeable protocol pages may arrive from separate peers or
	// after a configuration mutation behind a peer's keyset cursor.
	if err = n.mergeSnapshot(Snapshot{Incidents: []Incident{i}}); err != nil {
		t.Fatal(err)
	}
	if err = n.mergeSnapshot(Snapshot{Resources: []ResourceVersion{r}}); err != nil {
		t.Fatal(err)
	}
	stored, ok, err := loadOne[Incident](n.store, "incidents", i.ID)
	if err != nil || !ok || !slices.Contains(stored.DeliveryTargets, target.ID) {
		t.Fatalf("acknowledged closed-episode delivery intent was lost before its channel page arrived: targets=%v err=%v", stored.DeliveryTargets, err)
	}
}

func TestReviewRound2InterruptedNotificationTestsDoNotRemainPending(t *testing.T) {
	o := Options{NodeID: "a", DBPath: filepath.Join(t.TempDir(), "round2.db"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken}
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	// Persist the two crash boundaries of a bounded test batch. Tests have no
	// automatic retry scheduler; a restarted process cannot still be sending.
	for _, state := range []string{"queued", "sending"} {
		a := attempt{ID: "round2-interrupted-" + state, Kind: "test", ChannelID: "round2-channel", DestinationID: "round2-destination", Provider: "webhook", State: state, LastAt: n.now()}
		if err = put(n.store.db, "attempts", a.ID, a); err != nil {
			t.Fatal(err)
		}
	}
	if err = n.Stop(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Stop() })
	for _, a := range restarted.attempts {
		v := restarted.attemptView(a)
		if v.State == "queued" || v.State == "sending" || v.State == "retrying" || v.NextAttemptAt != 0 {
			t.Errorf("abandoned test still advertises pending work after restart: %+v", v)
		}
		if v.State == "accepted" {
			t.Error("interruption must not invent provider acceptance")
		}
	}
}

func TestReviewRound2ValidChannelIDsProduceReplicableDestinations(t *testing.T) {
	n := reviewNotificationNode(t)
	for range cap(n.notificationWorkers) {
		n.notificationWorkers <- struct{}{}
	}
	// IDs up to 128 bytes are accepted on replicated channel resources. A
	// derived destination must remain inside the same peer identifier limit.
	id := strings.Repeat("c", 104)
	r, err := n.writeResource(resourceChannel, id, hookChannel("Long valid ID", "http://127.0.0.1:1"), false)
	if err != nil {
		t.Fatal(err)
	}
	in := input([]string{"a"}, "http://127.0.0.1:1", "")
	in.NotificationIDs = []string{r.ID}
	m, err := n.writeMonitor(in, nil, "round2-long-id-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	validatePage := func() error {
		page, err := n.snapshotPage("", "")
		if err != nil {
			return err
		}
		body, err := json.Marshal(page)
		if err != nil {
			return err
		}
		_, err = n.validateSnapshot(body, n.ID)
		return err
	}
	if err = validatePage(); err != nil {
		t.Fatal("configuration must be valid before notification scheduling", err)
	}
	n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: n.now(), Message: "HTTP 503"}, Sequence: 1, Revision: m.Revision, DownSince: Ptr(int64(1))}
	n.evaluate()
	if err = validatePage(); err != nil {
		t.Fatalf("valid channel configuration produced an incident rejected by the peer decoder: %v", err)
	}
}
