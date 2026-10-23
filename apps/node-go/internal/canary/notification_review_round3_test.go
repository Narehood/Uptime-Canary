package canary

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Exercise the actual peer decoder for independently mergeable pages rather
// than calling reconcileEpisodes with the entire causal history at once.
func reviewRound3Page(t *testing.T, n *Node, s Snapshot) Snapshot {
	t.Helper()
	s.Protocol, s.Node = peerProtocol, Identity{"b", "Review peer"}
	if s.GroupVersions == nil {
		s.GroupVersions = []GroupVersion{}
	}
	if s.Resources == nil {
		s.Resources = []ResourceVersion{}
	}
	if s.Intents == nil {
		s.Intents = []NotificationIntent{}
	}
	if s.Versions == nil {
		s.Versions = []Version{}
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
	body, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	validated, err := n.validateSnapshot(body, "b")
	if err != nil {
		t.Fatal("fixture page is invalid", err)
	}
	return validated
}

func reviewRound3Merge(t *testing.T, n *Node, s Snapshot) {
	t.Helper()
	s = reviewRound3Page(t, n, s)
	n.mu.Lock()
	err := n.mergeSnapshot(s)
	n.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func TestReviewRound3DelayedPredecessorCannotMoveOldReceiptIntoNewOutage(t *testing.T) {
	for _, order := range []string{"causal-history-together", "predecessor-page-last"} {
		t.Run(order, func(t *testing.T) {
			r := webhook(t)
			n, err := New(Options{NodeID: "a", DBPath: ":memory:", Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Peers: []Peer{{ID: "b", URL: "http://127.0.0.1:1"}}, NotificationDelayMs: Ptr(int64(0))})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = n.Stop() })
			resource, err := n.writeResource(resourceChannel, "round3-channel", hookChannel("Current destination", r.URL), false)
			if err != nil {
				t.Fatal(err)
			}
			in := input([]string{"a", "b"}, "http://127.0.0.1:1", "")
			in.NotificationIDs = []string{resource.ID}
			m, err := n.writeMonitor(in, nil, "round3-monitor", false)
			if err != nil {
				t.Fatal(err)
			}
			target := n.notificationTargets(m)[0]
			prior := Incident{ID: "z-prior", Aliases: []string{"z-prior"}, MonitorID: m.ID, MonitorName: m.Name, Revision: m.Revision, OpenedAt: n.now() - 10000, Runs: map[string][]int64{"a": {1}}, Closed: map[string]int64{"a": 2, "b": 2}, ResolvedAt: Ptr(n.now() - 9000), Predecessors: []string{}}
			sibling := Incident{ID: "b-delayed-sibling", Aliases: []string{"b-delayed-sibling"}, MonitorID: m.ID, MonitorName: m.Name, Revision: m.Revision, OpenedAt: n.now() - 9900, Runs: map[string][]int64{"b": {1}}, Predecessors: []string{}}
			current := Incident{ID: "c-current", Aliases: []string{"c-current"}, MonitorID: m.ID, MonitorName: m.Name, Revision: m.Revision, OpenedAt: n.now() - 1000, Runs: map[string][]int64{"a": {3}}, Predecessors: []string{prior.ID}}
			oldReceipt := Receipt{NotificationView: NotificationView{ID: "round3-old-success", MonitorID: m.ID, IncidentID: sibling.ID, DestinationID: target.ID, ChannelID: target.ChannelID, Provider: target.Provider, Kind: "down", NodeID: "b", SentAt: prior.OpenedAt + 200}, Slot: 0}
			fact := deliveryIntent(m.ID, sibling.ID, target.ID)
			if order == "causal-history-together" {
				reviewRound3Merge(t, n, Snapshot{Incidents: []Incident{current, sibling, prior}, Intents: []NotificationIntent{fact}, Receipts: []Receipt{oldReceipt}})
			} else {
				// Keyset order may put the successor and a delayed open sibling
				// before the closed incident proving that sibling's old lifetime.
				reviewRound3Merge(t, n, Snapshot{Intents: []NotificationIntent{fact}, Receipts: []Receipt{oldReceipt}, Incidents: []Incident{sibling}})
				reviewRound3Merge(t, n, Snapshot{Incidents: []Incident{current}})
				reviewRound3Merge(t, n, Snapshot{Incidents: []Incident{prior}})
			}
			var latest Incident
			for _, i := range n.episodes {
				if slices.Contains(episodeIDs(i), current.ID) {
					latest = i
				}
			}
			if latest.ID == "" || slices.Contains(episodeIDs(latest), sibling.ID) || n.hasDeliveryIntent(latest, target.ID) || len(n.forEpisode(latest)) != 0 {
				t.Errorf("older sibling audience/receipt contaminated the later outage: episode=%+v receipts=%+v", latest, n.forEpisode(latest))
			}
			// Fresh evidence for a genuine later run must cause a new down,
			// independent of a successful delivery for the preceding outage.
			n.mu.Lock()
			n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: n.now(), Message: "current outage"}, Sequence: 3, Revision: m.Revision, DownSince: Ptr(int64(3))}
			n.samples[m.ID+"/b"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "b", State: "up", CheckedAt: n.now(), Message: "up"}, Sequence: 4, Revision: m.Revision}
			n.evaluate()
			n.mu.Unlock()
			n.wg.Wait()
			if r.count("down") != 1 {
				t.Errorf("a previous outage receipt suppressed the genuine later down: deliveries=%+v", r.all())
			}
			if order == "predecessor-page-last" {
				// Even a later replay of the complete source history cannot
				// restore the successor's original independent identity.
				reviewRound3Merge(t, n, Snapshot{Incidents: []Incident{current, sibling, prior}})
				n.wg.Wait()
				for _, i := range n.episodes {
					if slices.Contains(episodeIDs(i), current.ID) && (i.Closed != nil || slices.Contains(episodeIDs(i), sibling.ID)) {
						t.Errorf("replayed complete causal history closed or mixed the successor: %+v", i)
					}
				}
			}
		})
	}
}

func TestReviewRound3IntentRequiresExplicitNonNullDestination(t *testing.T) {
	for _, variant := range []string{"missing", "null", "explicit-empty"} {
		t.Run(variant, func(t *testing.T) {
			r := webhook(t)
			n, err := New(Options{NodeID: "a", DBPath: ":memory:", Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Peers: []Peer{{ID: "b", URL: "http://127.0.0.1:1"}}, NotificationDelayMs: Ptr(int64(0))})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = n.Stop() })
			m, err := n.writeMonitor(input([]string{"a"}, "http://127.0.0.1:1", ""), nil, "round3-monitor", false)
			if err != nil {
				t.Fatal(err)
			}
			i := Incident{ID: "round3-closed", Aliases: []string{"round3-closed"}, MonitorID: m.ID, MonitorName: m.Name, Revision: m.Revision, OpenedAt: n.now() - 10000, Runs: map[string][]int64{"a": {1}}, Closed: map[string]int64{"a": 2}, ResolvedAt: Ptr(n.now() - 5000), Predecessors: []string{}}
			n.saveEpisode(i)
			m.WebhookURL = r.URL
			m, err = n.writeMonitor(m.MonitorInput, n.heads[m.ID], m.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "up", CheckedAt: n.now(), Message: "up"}, Sequence: 2, Revision: m.Revision, CheckGeneration: m.CheckGeneration}
			n.evaluate()
			n.wg.Wait()
			if r.count("") != 0 {
				t.Fatal("fixture replayed before receiving malformed intent")
			}
			s := Snapshot{Protocol: peerProtocol, Node: Identity{"b", "Review peer"}, Versions: []Version{}, GroupVersions: []GroupVersion{}, Resources: []ResourceVersion{}, Observations: []Sample{}, Incidents: []Incident{}, Receipts: []Receipt{}}
			body, _ := json.Marshal(s)
			var raw map[string]any
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatal(err)
			}
			fact := deliveryIntent(m.ID, i.ID, "")
			record := map[string]any{"id": fact.ID, "monitorId": fact.MonitorID, "incidentId": fact.IncidentID}
			if variant == "null" {
				record["destinationId"] = nil
			}
			expectedStatus := 400
			if variant == "explicit-empty" {
				record["destinationId"], expectedStatus = "", 200
			}
			raw["intents"] = []any{record}
			body, _ = json.Marshal(raw)
			req := httptest.NewRequest("POST", "/internal/snapshot", strings.NewReader(string(body)))
			req.Header.Set("Authorization", "Bearer "+clusterToken)
			req.Header.Set("X-Canary-Node-Id", "b")
			response := httptest.NewRecorder()
			n.ServeHTTP(response, req)
			n.wg.Wait()
			n.mu.Lock()
			n.evaluate()
			n.mu.Unlock()
			n.wg.Wait()
			if response.Code != expectedStatus || expectedStatus == 400 && (len(n.intents) != 0 || r.count("") != 0) {
				t.Fatalf("malformed authenticated intent got HTTP %d and legacy-webhook authority: down=%d recovery=%d", response.Code, r.count("down"), r.count("recovery"))
			}
			if expectedStatus == 200 && (r.count("down") != 1 || r.count("recovery") != 1) {
				t.Fatal("explicit empty destination must retain legitimate legacy-webhook intent")
			}
		})
	}
}

func TestReviewRound3MergedAliasIntentTransactionRollsBackAndRestarts(t *testing.T) {
	o := Options{NodeID: "a", DBPath: filepath.Join(t.TempDir(), "round3-atomic.db"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Peers: []Peer{{ID: "b", URL: "http://127.0.0.1:1"}}}
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	i := Incident{ID: "z-original", Aliases: []string{"z-original"}, MonitorID: "round3-monitor", MonitorName: "Atomic alias", Revision: "round3-revision", OpenedAt: n.now() - 10000, Runs: map[string][]int64{"a": {1}}, Closed: map[string]int64{"a": 2}, ResolvedAt: Ptr(n.now() - 5000), Predecessors: []string{}, DeliveryTargets: []string{"old-destination"}}
	n.saveEpisode(i)
	if n.fault != nil {
		t.Fatal(n.fault)
	}
	alias := i
	alias.ID, alias.Aliases, alias.DeliveryTargets = "a-canonical", []string{"a-canonical"}, []string{}
	fact := deliveryIntent(i.MonitorID, i.ID, "new-destination")
	page := reviewRound3Page(t, n, Snapshot{Incidents: []Incident{alias}, Intents: []NotificationIntent{fact}})
	_, err = n.store.db.Exec(`CREATE TRIGGER round3_episode_write_failure BEFORE INSERT ON records WHEN NEW.category='incidents' AND NEW.id='a-canonical' BEGIN SELECT RAISE(FAIL,'injected episode failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err = n.mergeSnapshot(page); err == nil {
		t.Fatal("injected merged episode failure was acknowledged")
	}
	if len(n.episodes) != 1 || n.episodes[i.ID].ID != i.ID || len(n.intents) != 1 || n.indexedDeliveryIntent(i, fact.DestinationID) {
		t.Fatal("failed page changed memory or the alias intent index")
	}
	storedEpisodes, err := loadAll[Incident](n.store, "incidents")
	if err != nil || !equalJSON(storedEpisodes, n.episodes) {
		t.Fatal("failed merged page committed episode or alias deletion", err)
	}
	storedIntents, err := loadAll[NotificationIntent](n.store, "notificationIntents")
	if err != nil || !equalJSON(storedIntents, n.intents) {
		t.Fatal("failed merged page committed audience fact", err)
	}
	if _, err = n.store.db.Exec(`DROP TRIGGER round3_episode_write_failure`); err != nil {
		t.Fatal(err)
	}
	if err = n.mergeSnapshot(page); err != nil {
		t.Fatal("retrying the identical valid page failed", err)
	}
	if len(n.episodes) != 1 || n.episodes[alias.ID].ID != alias.ID || !n.indexedDeliveryIntent(n.episodes[alias.ID], fact.DestinationID) {
		t.Fatal("successful retry lost the noncanonical alias fact")
	}
	if err = n.Stop(); err != nil {
		t.Fatal(err)
	}
	n, err = New(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(n.episodes) != 1 || !n.indexedDeliveryIntent(n.episodes[alias.ID], fact.DestinationID) {
		t.Fatal("alias deletion or separately received audience failed restart")
	}
}

func TestReviewRound3MaximumLengthIDsValidateWithIntentAndReceipt(t *testing.T) {
	n := reviewNotificationNode(t)
	channelID := strings.Repeat("c", 128)
	r, err := n.writeResource(resourceChannel, channelID, hookChannel("Maximum channel ID", "http://127.0.0.1:1"), false)
	if err != nil {
		t.Fatal(err)
	}
	in := input([]string{"a"}, "http://127.0.0.1:1", "")
	in.Enabled, in.NotificationIDs = false, []string{r.ID}
	m, err := n.writeMonitor(in, nil, strings.Repeat("m", 128), false)
	if err != nil {
		t.Fatal(err)
	}
	target := n.notificationTargets(m)[0]
	i := Incident{ID: strings.Repeat("i", 128), Aliases: []string{strings.Repeat("i", 128)}, MonitorID: m.ID, MonitorName: m.Name, Revision: m.Revision, OpenedAt: n.now() - 10000, Runs: map[string][]int64{"a": {1}}, Closed: map[string]int64{"a": 2}, ResolvedAt: Ptr(n.now() - 5000), Predecessors: []string{}, DeliveryTargets: []string{target.ID}}
	n.saveEpisode(i)
	receipt := Receipt{NotificationView: NotificationView{ID: attemptIdentity(m.ID, i.ID, "down", 0, target.ID), MonitorID: m.ID, IncidentID: i.ID, DestinationID: target.ID, ChannelID: channelID, Provider: target.Provider, Kind: "down", NodeID: n.ID, SentAt: n.now()}, Slot: 0}
	if !n.dbPut("receipts", receipt.ID, receipt) {
		t.Fatal(n.fault)
	}
	n.receipts[receipt.ID] = receipt
	page, err := n.snapshotPage("", "")
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = n.validateSnapshot(body, n.ID); err != nil {
		t.Fatal("maximum-length valid IDs produced an invalid intent/receipt page", err)
	}
	if len(target.ID) != 64 || len(page.Intents) != 1 || len(page.Receipts) != 1 {
		t.Fatal("fixture did not cover derived destination, intent and receipt")
	}
}
