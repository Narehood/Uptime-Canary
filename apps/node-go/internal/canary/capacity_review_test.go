package canary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	maintenanceEngine "uptime-canary/apps/node-go/internal/maintenance"
)

// These are desired-behavior regressions. Every configuration used by the
// partition fixtures is admitted by the authenticated management handler;
// every peer page is produced by snapshotPage and sent over real loopback HTTP.
// Nodes are not Start'ed, so there are no background probes or notifications.
func capacityReviewNode(t *testing.T, id string, peers []string) *Node {
	t.Helper()
	options := Options{NodeID: id, DBPath: ":memory:", Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken}
	for _, peer := range peers {
		if peer != id {
			options.Peers = append(options.Peers, Peer{ID: peer, URL: "http://127.0.0.1:1"})
		}
	}
	n, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	return n
}

func capacityReviewAPI(t *testing.T, n *Node, method, path string, input any) (int, []byte) {
	t.Helper()
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	n.ServeHTTP(recorder, req)
	return recorder.Code, recorder.Body.Bytes()
}

func capacityReviewCreate(t *testing.T, n *Node, path string, input any) map[string]any {
	t.Helper()
	status, body := capacityReviewAPI(t, n, "POST", path, input)
	if status != http.StatusCreated {
		t.Fatalf("valid local create %s: %d %s", path, status, body)
	}
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil {
		t.Fatal(err)
	}
	return object
}

func capacityReviewPage(t *testing.T, n *Node, after string) (Snapshot, []byte) {
	t.Helper()
	n.mu.Lock()
	page, err := n.snapshotPage(after, "")
	n.mu.Unlock()
	if err != nil {
		t.Fatal("cannot produce bounded peer page", err)
	}
	body, err := json.Marshal(page)
	if err != nil || len(body) > maxBody {
		t.Fatal("peer page exceeds HTTP bound", len(body), err)
	}
	count := len(page.Versions) + len(page.GroupVersions) + len(page.Resources) + len(page.Intents) + len(page.Observations) + len(page.Incidents) + len(page.Receipts) + len(page.RevisionEdges) + len(page.RevisionClaims) + len(page.IncidentFacts) + len(page.EvidenceLinks)
	if count > pageRecords {
		t.Fatal("peer page exceeds record bound", count)
	}
	return page, body
}

func capacityReviewPost(t *testing.T, sender *Node, receiverURL string, body []byte) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest("POST", receiverURL+"/internal/snapshot", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+clusterToken)
	req.Header.Set("X-Canary-Node-Id", sender.ID)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	out, err := readBounded(response.Body, maxBody)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, out
}

func capacityReviewPush(t *testing.T, sender, receiver *Node, receiverURL string) (int, []byte) {
	t.Helper()
	for cursor, pages := "", 0; ; pages++ {
		if pages >= 128 {
			t.Fatal("fixture exceeded bounded sweep rounds")
		}
		page, body := capacityReviewPage(t, sender, cursor)
		if _, err := receiver.validateSnapshot(body, sender.ID); err != nil {
			t.Fatal("producer emitted an invalid fixture page", err)
		}
		status, out := capacityReviewPost(t, sender, receiverURL, body)
		if status != http.StatusOK || page.Next == "" {
			return status, out
		}
		if page.Next <= cursor {
			t.Fatal("page cursor failed to advance")
		}
		cursor = page.Next
	}
}

func TestCapacityReviewPartitionCreatesConvergePastLocalAdmissionLimits(t *testing.T) {
	monitor := input([]string{"a", "b"}, "http://127.0.0.1:1", "")
	monitor.Enabled = false
	channel := hookChannel("Disabled fixture channel", "http://127.0.0.1:1/hook")
	channel.Enabled = false
	maintenance := MaintenanceInput{Name: "Disabled fixture maintenance", Enabled: false, AllMonitors: true, Schedule: maintenanceEngine.Config{Strategy: maintenanceEngine.Once, Timezone: "UTC", StartDate: "2026-10-22", EndDate: "2026-10-22", StartTime: "10:00", DurationMinutes: 1}}
	for _, fixture := range []struct {
		name, path, category, errorText string
		limit                           int
		input                           any
		count                           func(*Node) int
	}{
		{"monitors", "/api/monitors", "heads", "Monitor capacity exceeded", maxMonitors, monitor, func(n *Node) int { return len(n.heads) }},
		{"groups", "/api/groups", "groupHeads", "Group capacity exceeded", maxGroups, GroupInput{Name: "Fixture group", NodeIDs: []string{"a", "b"}, GraceMs: 0}, func(n *Node) int { return len(n.groups) }},
		{"channels", "/api/notification-channels", "resourceHeads", "Resource capacity exceeded", maxResources, channel, func(n *Node) int { return len(n.resources) }},
		{"maintenance", "/api/maintenance", "resourceHeads", "Maintenance capacity exceeded", maxMaintenances, maintenance, func(n *Node) int { return len(n.resources) }},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			a, b := capacityReviewNode(t, "a", []string{"a", "b"}), capacityReviewNode(t, "b", []string{"a", "b"})
			as, bs := httptest.NewServer(a), httptest.NewServer(b)
			t.Cleanup(as.Close)
			t.Cleanup(bs.Close)
			var common map[string]any
			for range fixture.limit - 10 {
				common = capacityReviewCreate(t, a, fixture.path, fixture.input)
			}
			if code, body := capacityReviewPush(t, a, b, bs.URL); code != http.StatusOK {
				t.Fatalf("initial common state failed: %d %s", code, body)
			}
			// No exchanges during these valid, concurrent partition-local creates.
			for _, n := range []*Node{a, b} {
				for range 10 {
					capacityReviewCreate(t, n, fixture.path, fixture.input)
				}
				if fixture.count(n) != fixture.limit {
					t.Fatal("fixture did not reach exactly the local admission limit")
				}
			}
			for _, direction := range []struct {
				sender, receiver *Node
				url              string
			}{{a, b, bs.URL}, {b, a, as.URL}} {
				for attempt := range 3 {
					code, body := capacityReviewPush(t, direction.sender, direction.receiver, direction.url)
					if code != http.StatusOK {
						t.Errorf("valid %d-ID union must converge (retry %d %s->%s): %d %s", fixture.limit+10, attempt+1, direction.sender.ID, direction.receiver.ID, code, body)
					}
				}
				direction.sender.settings.Peers[0].URL = direction.url
				before := direction.sender.peerProgress[direction.receiver.ID]
				direction.sender.exchangePage(direction.sender.settings.Peers[0])
				if fixture.count(direction.receiver) == fixture.limit && direction.sender.peerProgress[direction.receiver.ID] != before {
					t.Fatal("rejected production exchange advanced its cursor")
				}
			}
			// Deleting shared configuration cannot free these retained IDs. It
			// also cannot unblock acknowledgement of a page containing new IDs.
			for _, n := range []*Node{a, b} {
				status, body := capacityReviewAPI(t, n, "DELETE", fixture.path+"/"+common["id"].(string), map[string]any{"revision": common["revision"]})
				if status != http.StatusNoContent {
					t.Fatalf("delete valid shared object: %d %s", status, body)
				}
				if fixture.count(n) < fixture.limit {
					t.Fatal("tombstone was discarded")
				}
			}
			if code, body := capacityReviewPush(t, a, b, bs.URL); code != http.StatusOK {
				t.Errorf("tombstoning shared configuration did not restore convergence: %d %s", code, body)
			}
			// Check durable SQLite counts separately from the in-memory maps.
			for _, n := range []*Node{a, b} {
				stored, err := loadAll[json.RawMessage](n.store, fixture.category)
				if err != nil || len(stored) != fixture.count(n) {
					t.Fatal("memory and SQLite disagree", len(stored), fixture.count(n), err)
				}
			}
			t.Logf("shared=%d, accepted partition-local creates=10+10, required union=%d, retained SQLite IDs=%d/%d; rejection family=%s", fixture.limit-10, fixture.limit+10, fixture.count(a), fixture.count(b), fixture.errorText)
		})
	}
}

func TestCapacityReviewOldProtocolsRejectedBeforeConfigurationMerge(t *testing.T) {
	a, b := capacityReviewNode(t, "a", []string{"a", "b"}), capacityReviewNode(t, "b", []string{"a", "b"})
	server := httptest.NewServer(b)
	t.Cleanup(server.Close)
	in := input([]string{"a", "b"}, "http://127.0.0.1:1", "")
	in.Enabled = false
	capacityReviewCreate(t, a, "/api/monitors", in)
	page, currentBody := capacityReviewPage(t, a, "")
	for protocol := 1; protocol < peerProtocol; protocol++ {
		page.Protocol = protocol
		body, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		if code, out := capacityReviewPost(t, a, server.URL, body); code != http.StatusBadRequest {
			t.Errorf("old protocol %d must be rejected: %d %s", protocol, code, out)
		}
		if len(b.heads) != 0 {
			t.Fatal("old protocol merged configuration before rejection", protocol)
		}
	}
	if code, out := capacityReviewPost(t, a, server.URL, currentBody); code != http.StatusOK || len(b.heads) != 1 {
		t.Fatal("current protocol control failed", code, string(out), len(b.heads))
	}
}

func TestCapacityReviewLocallyAccumulatedRunsRemainReplicable(t *testing.T) {
	a, b := capacityReviewNode(t, "a", []string{"a", "b"}), capacityReviewNode(t, "b", []string{"a", "b"})
	as, bs := httptest.NewServer(a), httptest.NewServer(b)
	t.Cleanup(as.Close)
	t.Cleanup(bs.Close)
	var now atomic.Int64
	now.Store(time.Now().UnixMilli())
	var bDown atomic.Bool
	a.now, b.now = now.Load, now.Load
	a.probe = func(context.Context, Monitor) ProbeResult {
		return ProbeResult{State: "down", Message: "persistent a outage"}
	}
	b.probe = controlled(&bDown)
	// A supported long sync interval keeps A's real observation fresh while B
	// executes many local checks. Refresh A once halfway through the test.
	a.settings.SyncIntervalMs, b.settings.SyncIntervalMs = 60000, 60000
	drive := func(n *Node) {
		t.Helper()
		n.mu.Lock()
		// Drive explicit partition/reconnect points instead of background sync.
		n.nextSync = maxSafe
		n.mu.Unlock()
		n.tick()
		n.wg.Wait()
		if n.Error() != nil {
			t.Fatal(n.Error())
		}
	}
	created := capacityReviewCreate(t, a, "/api/monitors", input([]string{"a", "b"}, "http://127.0.0.1:1", ""))
	monitorID := created["id"].(string)
	drive(a)
	if code, body := capacityReviewPush(t, a, b, bs.URL); code != http.StatusOK {
		t.Fatal(code, string(body))
	}
	var boundaryBytes int
	for cycle := range 1001 {
		if cycle == 500 {
			drive(a)
			if code, body := capacityReviewPush(t, a, b, bs.URL); code != http.StatusOK {
				t.Fatal(code, string(body))
			}
		}
		now.Add(100)
		bDown.Store(true)
		drive(b)
		if cycle == 999 {
			_, body := capacityReviewPage(t, b, "")
			if _, err := a.validateSnapshot(body, b.ID); err != nil {
				t.Fatal("1000-run boundary should be valid", err)
			}
			boundaryBytes = len(body)
		}
		if cycle != 1000 {
			now.Add(100)
			bDown.Store(false)
			drive(b)
		}
	}
	if len(b.episodes) != 1 {
		t.Fatal("flapping must remain one open outage while A stays down", len(b.episodes))
	}
	for _, incident := range b.episodes {
		if incident.MonitorID != monitorID || incident.Closed != nil || len(incident.Runs["b"]) != 1001 || len(incident.Runs["a"]) != 1 {
			t.Fatalf("fixture did not create the valid sustained outage: %+v", incident)
		}
	}
	_, body := capacityReviewPage(t, b, "")
	if _, err := a.validateSnapshot(body, b.ID); err != nil {
		t.Errorf("real local probes persisted 1001 runs in one incident but peer decoder rejects it: %v", err)
	}
	if code, out := capacityReviewPush(t, b, a, as.URL); code != http.StatusOK {
		t.Errorf("authenticated reconnect cannot accept locally generated incident: %d %s", code, out)
	}
	received := 0
	for _, incident := range a.episodes {
		if incident.MonitorID == monitorID {
			received++
		}
		if incident.MonitorID == monitorID && len(incident.Runs["b"]) != 1001 {
			t.Fatal("bounded pages lost complete peer run evidence", len(incident.Runs["b"]))
		}
	}
	if received != 1 {
		t.Fatal("peer did not hydrate the complete sustained outage", received)
	}
	stored, err := loadAll[Incident](b.store, "incidents")
	if err != nil || len(stored) != 1 {
		t.Fatal("incident was not durable", len(stored), err)
	}
	for _, incident := range stored {
		if len(incident.Runs["b"]) > inlineIncidentRuns || incident.EvidenceRoot == "" {
			t.Fatal("durable incident is not a bounded evidence commitment")
		}
		hydrated, complete, hydrateErr := hydrateIncident(incident, b.incidentFacts, b.evidenceLinks)
		if hydrateErr != nil || !complete || len(hydrated.Runs["b"]) != 1001 {
			t.Fatal("SQLite facts did not retain locally produced run union", complete, hydrateErr)
		}
	}
	t.Logf("2001 real B probe completions, two A observations, one still-open incident: 1000-run page=%d bytes, 1001-run page=%d bytes (limit=%d)", boundaryBytes, len(body), maxBody)
}

func TestCapacityReviewConcurrentEditAncestryRemainsResolvableAndReplicable(t *testing.T) {
	// 32 static members, each making 313 valid sequential local edits from one
	// common root, yield 10017 ancestors at full-head resolution. Unlike a
	// 10000-deep serial fixture, each SQLite write here is only ~313 IDs long.
	ids := []string{}
	for index := range 32 {
		ids = append(ids, fmt.Sprintf("node%02d", index))
	}
	monitor := input([]string{ids[0]}, "http://127.0.0.1:1", "")
	monitor.Enabled = false
	channel := hookChannel("Disabled DAG fixture", "http://127.0.0.1:1/hook")
	channel.Enabled = false
	for _, fixture := range []struct {
		name, path string
		input      any
	}{
		{"monitors", "/api/monitors", monitor},
		{"groups", "/api/groups", GroupInput{Name: "DAG fixture group", NodeIDs: []string{ids[0]}, GraceMs: 0}},
		{"channels", "/api/notification-channels", channel},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			nodes := []*Node{}
			servers := []*httptest.Server{}
			for _, id := range ids {
				n := capacityReviewNode(t, id, ids)
				nodes = append(nodes, n)
				s := httptest.NewServer(n)
				servers = append(servers, s)
				t.Cleanup(s.Close)
			}
			collector := nodes[0]
			root := capacityReviewCreate(t, collector, fixture.path, fixture.input)
			id := root["id"].(string)
			_, rootBody := capacityReviewPage(t, collector, "")
			for index, n := range nodes {
				if index != 0 {
					if code, body := capacityReviewPost(t, collector, servers[index].URL, rootBody); code != http.StatusOK {
						t.Fatal("root replication", code, string(body))
					}
				}
				// These actual mutators take the node's own current heads. Only
				// Name changes; no invented ancestry or stale-head write is used.
				for edit := range 313 {
					name := fmt.Sprintf("Valid branch %s edit %03d", n.ID, edit)
					var err error
					switch fixture.name {
					case "monitors":
						in := n.heads[id][0].Monitor.MonitorInput
						in.Name = name
						_, err = n.writeMonitor(in, n.heads[id], id, false)
					case "groups":
						in := n.groups[id][0].Group.GroupInput
						in.Name = name
						_, err = n.writeGroup(in, n.groups[id], id, false)
					case "channels":
						in := channelOf(*n.currentResource(resourceChannel, id), "admin").ChannelInput
						in.Name = name
						_, err = n.writeResource(resourceChannel, id, in, false)
					}
					if err != nil {
						t.Fatal("valid local branch edit", err)
					}
				}
			}
			for _, n := range nodes[1:] {
				if code, body := capacityReviewPush(t, n, collector, servers[0].URL); code != http.StatusOK {
					t.Fatal("valid branch replication", code, string(body))
				}
			}
			revisions := []string{}
			switch fixture.name {
			case "monitors":
				for _, head := range collector.heads[id] {
					revisions = append(revisions, head.Monitor.Revision)
				}
			case "groups":
				for _, head := range collector.groups[id] {
					revisions = append(revisions, head.Group.Revision)
				}
			case "channels":
				for _, head := range collector.resources[resourceKey(resourceChannel, id)] {
					revisions = append(revisions, head.Resource.Revision)
				}
			}
			if len(revisions) != 32 {
				t.Fatal("expected every valid branch as a retained current head", len(revisions))
			}
			resolution := map[string]any{}
			encoded, _ := json.Marshal(fixture.input)
			if err := json.Unmarshal(encoded, &resolution); err != nil {
				t.Fatal(err)
			}
			resolution["revisions"] = revisions
			code, result := capacityReviewAPI(t, collector, "POST", fixture.path+"/"+id+"/resolve", resolution)
			if code != http.StatusOK {
				t.Errorf("valid supported concurrent edits leave full-head resolution unavailable: %d %s", code, result)
				return
			}
			var resolved struct {
				Revision string `json:"revision"`
			}
			if json.Unmarshal(result, &resolved) != nil || resolved.Revision == "" {
				t.Fatal("missing resolved revision")
			}
			inline := 0
			kind := fixture.name
			switch fixture.name {
			case "monitors":
				kind, inline = "monitor", len(collector.heads[id][0].Ancestors)
			case "groups":
				kind, inline = "group", len(collector.groups[id][0].Ancestors)
			case "channels":
				kind, inline = resourceChannel, len(collector.resources[resourceKey(resourceChannel, id)][0].Ancestors)
			}
			if inline > 32 {
				t.Fatal("resolution exceeded bounded direct parents", inline)
			}
			edgeCount := 0
			for _, edge := range collector.revisionEdges {
				if edge.Kind == kind && edge.EntityID == id {
					edgeCount++
					if len(edge.Parents) > 32 {
						t.Fatal("unbounded retained revision edge")
					}
				}
			}
			if edgeCount != 10018 {
				t.Fatal("actual edit history was not retained as independent facts", edgeCount)
			}
			for _, rev := range append(append([]string{}, revisions...), root["revision"].(string)) {
				if !collector.graph.reaches(kind, id, resolved.Revision, rev) {
					t.Fatal("resolution lost branch/root ancestry", rev)
				}
			}
			if status, out := capacityReviewPush(t, collector, nodes[1], servers[1].URL); status != http.StatusOK {
				t.Errorf("authenticated peer cannot accept successful local resolution: %d %s", status, out)
			}
			for _, rev := range revisions {
				if !nodes[1].graph.reaches(kind, id, resolved.Revision, rev) {
					t.Fatal("peer lost complete branch causality", rev)
				}
			}
			switch fixture.name {
			case "monitors":
				if h := nodes[1].heads[id]; len(h) != 1 || h[0].Monitor.Revision != resolved.Revision {
					t.Fatal("monitor did not converge after full proof sweep")
				}
			case "groups":
				if h := nodes[1].groups[id]; len(h) != 1 || h[0].Group.Revision != resolved.Revision {
					t.Fatal("group did not converge after full proof sweep")
				}
			case "channels":
				if h := nodes[1].resources[resourceKey(resourceChannel, id)]; len(h) != 1 || h[0].Resource.Revision != resolved.Revision {
					t.Fatal("channel did not converge after full proof sweep")
				}
			}
			t.Logf("32 valid branches x 313 actual local edits + common root; full-head API resolution HTTP %d, retained revision edges=%d, direct parents=%d", code, edgeCount, inline)
		})
	}
}
