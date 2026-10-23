package canary

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	providerDelivery "uptime-canary/apps/node-go/internal/delivery"
)

// These regression fixtures exercise desired behavior found during the
// independent notification integration review. All destinations are loopback;
// the Discord route test performs no I/O.
func reviewNotificationNode(t *testing.T) *Node {
	t.Helper()
	n, err := New(Options{NodeID: "a", DBPath: ":memory:", Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, NotificationDelayMs: Ptr(int64(0))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	return n
}

func TestReviewDestinationIdentityUsesOnlyActiveRoute(t *testing.T) {
	t.Run("Discord credential rotation", func(t *testing.T) {
		c := Channel{ID: "discord-route", ChannelInput: ChannelInput{Name: "Discord", Enabled: true, Config: providerDelivery.Config{Kind: "discord", Webhook: providerDelivery.WebhookConfig{URL: "https://discord.com/api/webhooks/123456/token_before"}}}}
		before := targetsForChannel(c)[0].ID
		c.Config.Webhook.URL = "https://discord.com/api/webhooks/123456/token_after"
		if after := targetsForChannel(c)[0].ID; after != before {
			t.Fatalf("rotating only the Discord token changed receipt identity: %s -> %s", before, after)
		}
		c.Config.Webhook.URL = "https://discord.com/api/webhooks/654321/token_after"
		if targetsForChannel(c)[0].ID == before {
			t.Fatal("changing the actual Discord webhook must create a new destination")
		}
	})
	t.Run("inactive provider options", func(t *testing.T) {
		c := Channel{ID: "brevo-route", ChannelInput: ChannelInput{Name: "Email", Enabled: true, Config: providerDelivery.Config{Kind: providerDelivery.Brevo, Sender: providerDelivery.Address{Email: "sender@example.test"}, Recipients: []providerDelivery.Address{{Email: "oncall@example.test"}}, Brevo: providerDelivery.BrevoConfig{APIKey: "fixture-only-key"}}}}
		before := targetsForChannel(c)[0].ID
		c.Config.SMTP.Server = "unused.example.test"
		c.Config.Mailgun.Domain = "unused.example.test"
		if err := providerDelivery.Validate(c.Config); err != nil {
			t.Fatal("fixture must remain an accepted channel configuration", err)
		}
		if after := targetsForChannel(c)[0].ID; after != before {
			t.Fatalf("editing inactive providers changed the Brevo receipt identity: %s -> %s", before, after)
		}
		c.Config.Recipients[0].Email = "new-oncall@example.test"
		if targetsForChannel(c)[0].ID == before {
			t.Fatal("changing the actual recipient must create a new destination")
		}
	})
}

func TestReviewNewDestinationDoesNotReplayClosedOutage(t *testing.T) {
	r := webhook(t)
	n := reviewNotificationNode(t)
	m, err := n.writeMonitor(input([]string{"a"}, "http://127.0.0.1:1", ""), nil, "review-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	i := Incident{ID: "review-closed", Aliases: []string{"review-closed"}, MonitorID: m.ID, MonitorName: m.Name, Revision: checkGeneration(m), OpenedAt: n.now() - 10000, Runs: map[string][]int64{"a": {1}}, Predecessors: []string{}, Closed: map[string]int64{"a": 2}, ResolvedAt: Ptr(n.now() - 5000)}
	n.saveEpisode(i)
	// The destination did not exist while the outage was open. Its addition
	// changes delivery configuration but preserves the monitoring generation.
	resource, err := n.writeResource(resourceChannel, "review-channel", hookChannel("New destination", r.URL), false)
	if err != nil {
		t.Fatal(err)
	}
	m.NotificationIDs = []string{resource.ID}
	m, err = n.writeMonitor(m.MonitorInput, n.heads[m.ID], m.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "up", CheckedAt: n.now(), Message: "HTTP 200"}, Sequence: 2, Revision: m.Revision, CheckGeneration: m.CheckGeneration}
	for range 2 {
		n.mu.Lock()
		n.evaluate()
		n.mu.Unlock()
		n.wg.Wait()
	}
	if got := r.all(); len(got) != 0 {
		t.Fatalf("newly configured destination received a historical resolved outage: %+v", got)
	}
}

func TestReviewExistingDestinationRetainsFastOutageDelivery(t *testing.T) {
	r := webhook(t)
	n := reviewNotificationNode(t)
	resource, err := n.writeResource(resourceChannel, "review-existing-channel", hookChannel("Existing destination", r.URL), false)
	if err != nil {
		t.Fatal(err)
	}
	in := input([]string{"a"}, "http://127.0.0.1:1", "")
	in.NotificationIDs = []string{resource.ID}
	m, err := n.writeMonitor(in, nil, "review-existing-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	// Observe and resolve a real outage before any worker can perform I/O.
	// Closing an episode must retain its already established delivery audience.
	for range cap(n.notificationWorkers) {
		n.notificationWorkers <- struct{}{}
	}
	n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: n.now(), Message: "HTTP 503"}, Sequence: 1, Revision: m.Revision, DownSince: Ptr(int64(1))}
	n.evaluate()
	n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "up", CheckedAt: n.now(), Message: "HTTP 200"}, Sequence: 2, Revision: m.Revision}
	n.evaluate()
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
		t.Fatalf("existing destination lost a fast observed outage/recovery: %+v", r.all())
	}
}

func TestReviewNotificationTestsRejectAnotherPendingBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	n := reviewNotificationNode(t)
	now, nowFn := clock()
	n.now = nowFn
	// Keep every production worker occupied. A pending test batch is permitted
	// to use a separate bounded test budget, but must not admit a second batch
	// solely because five seconds passed.
	for range cap(n.notificationWorkers) {
		n.notificationWorkers <- struct{}{}
	}
	c := ChannelInput{Name: "Review test batch", Enabled: true, Config: providerDelivery.Config{Kind: providerDelivery.Brevo, Sender: providerDelivery.Address{Email: "sender@example.test"}, Recipients: []providerDelivery.Address{{Email: "one@example.test"}, {Email: "two@example.test"}}, Brevo: providerDelivery.BrevoConfig{APIKey: "fixture-only-key", BaseURL: server.URL, AllowInsecureHTTP: true}}}
	r, err := n.writeResource(resourceChannel, "review-test-channel", c, false)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"revision": r.Revision})
	n.mu.Lock()
	status, _, err := n.startChannelTest(r.ID, body)
	n.mu.Unlock()
	if err != nil || status != 202 {
		t.Fatalf("first bounded batch should be accepted: %d %v", status, err)
	}
	now.Add((6 * time.Second).Milliseconds())
	n.mu.Lock()
	status, _, err = n.startChannelTest(r.ID, body)
	n.mu.Unlock()
	if err == nil || status == 202 {
		t.Fatal("second test batch was admitted while the first batch was pending")
	}
}

func TestReviewUnavailableDestinationDoesNotClaimScheduledRetry(t *testing.T) {
	for _, change := range []string{"deleted", "disabled", "conflicting", "unreferenced"} {
		t.Run(change, func(t *testing.T) {
			n := reviewNotificationNode(t)
			c := hookChannel("Unavailable route", "http://127.0.0.1:1")
			r, err := n.writeResource(resourceChannel, "review-unavailable-channel", c, false)
			if err != nil {
				t.Fatal(err)
			}
			in := input([]string{"a"}, "http://127.0.0.1:1", "")
			in.NotificationIDs = []string{r.ID}
			m, err := n.writeMonitor(in, nil, "review-unavailable-monitor", false)
			if err != nil {
				t.Fatal(err)
			}
			target := n.notificationTargets(m)[0]
			i := Incident{ID: "review-unavailable-incident", Aliases: []string{"review-unavailable-incident"}, MonitorID: m.ID, MonitorName: m.Name, Revision: checkGeneration(m), OpenedAt: n.now(), Runs: map[string][]int64{"a": {1}}, Predecessors: []string{}}
			n.saveEpisode(i)
			a := attempt{ID: "review-unavailable-attempt", MonitorID: m.ID, IncidentID: i.ID, Kind: "down", ChannelID: r.ID, DestinationID: target.ID, Provider: target.Provider, State: "retrying", Failures: 1, NextAt: n.now() + 1000}
			switch change {
			case "deleted", "disabled":
				c.Enabled = change != "disabled"
				_, err = n.writeResource(resourceChannel, r.ID, c, change == "deleted")
			case "conflicting":
				key := resourceKey(resourceChannel, r.ID)
				other := n.resources[key][0]
				other.Resource.Revision = "review-conflicting-revision"
				n.resources[key] = append(n.resources[key], other)
			case "unreferenced":
				m.NotificationIDs = nil
				_, err = n.writeMonitor(m.MonitorInput, n.heads[m.ID], m.ID, false)
			}
			if err != nil {
				t.Fatal(err)
			}
			v := n.attemptView(a)
			if v.NextAttemptAt != 0 || v.State == "queued" || v.State == "sending" || v.State == "retrying" {
				t.Errorf("unavailable destination still advertises runnable work: %+v", v)
			}
			// Successful acceptance remains historical fact after a route disappears.
			n.receipts[a.ID] = Receipt{NotificationView: NotificationView{ID: a.ID, MonitorID: m.ID, IncidentID: i.ID, Kind: "down", NodeID: n.ID, DestinationID: target.ID, ChannelID: r.ID, SentAt: n.now()}}
			if got := n.attemptView(a); got.State != "accepted" {
				t.Errorf("destination removal erased historical acceptance: %+v", got)
			}
			if n.episodes[i.ID].Closed != nil || n.episodes[i.ID].RetiredBy != "" {
				t.Fatal("destination unavailability closed or retired the ongoing incident")
			}
			if change == "disabled" {
				delete(n.receipts, a.ID)
				c.Enabled = true
				if _, err = n.writeResource(resourceChannel, r.ID, c, false); err != nil {
					t.Fatal(err)
				}
				if got := n.attemptView(a); got.State != "retrying" || got.NextAttemptAt != a.NextAt {
					t.Errorf("reenabled valid destination did not resume owed active-outage work: %+v", got)
				}
			}
		})
	}
}

func TestReviewAudienceRemainsReplicableAcrossRecipientEdits(t *testing.T) {
	n := reviewNotificationNode(t)
	for range cap(n.notificationWorkers) {
		n.notificationWorkers <- struct{}{}
	}
	in := input([]string{"a"}, "http://127.0.0.1:1", "")
	var first ChannelInput
	for index := range 16 {
		c := ChannelInput{Name: "Audience capacity", Enabled: true, Config: providerDelivery.Config{Kind: providerDelivery.Brevo, Sender: providerDelivery.Address{Email: "sender@example.test"}, Brevo: providerDelivery.BrevoConfig{APIKey: "fixture-only-key", BaseURL: "http://127.0.0.1:1", AllowInsecureHTTP: true}}}
		for recipient := range providerDelivery.MaxRecipients {
			c.Config.Recipients = append(c.Config.Recipients, providerDelivery.Address{Email: fmt.Sprintf("old-%d@example.test", recipient)})
		}
		if index == 0 {
			first = c
		}
		r, err := n.writeResource(resourceChannel, fmt.Sprintf("review-capacity-%d", index), c, false)
		if err != nil {
			t.Fatal(err)
		}
		in.NotificationIDs = append(in.NotificationIDs, r.ID)
	}
	m, err := n.writeMonitor(in, nil, "review-capacity-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: n.now(), Message: "HTTP 503"}, Sequence: 1, Revision: m.Revision, DownSince: Ptr(int64(1))}
	n.evaluate()
	// Maximum simultaneous fanout is 800. Replacing just one channel's valid
	// recipient list during that outage must not produce a peer-invalid record.
	for index := range first.Config.Recipients {
		first.Config.Recipients[index].Email = fmt.Sprintf("new-%d@example.test", index)
	}
	if _, err = n.writeResource(resourceChannel, in.NotificationIDs[0], first, false); err != nil {
		t.Fatal(err)
	}
	n.evaluate()
	page, err := n.snapshotPage("2/", "")
	if err != nil || len(page.Incidents) != 1 {
		t.Fatalf("expected the active incident in a bounded peer page: %v", err)
	}
	body, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = n.validateSnapshot(body, n.ID); err != nil {
		t.Fatalf("locally produced incident became invalid for peers after a valid recipient edit: %v", err)
	}
}
