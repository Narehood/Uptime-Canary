package canary

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	providerDelivery "uptime-canary/apps/node-go/internal/delivery"
	"uptime-canary/apps/node-go/internal/notifyhttp"
)

var messagingChannelKinds = []providerDelivery.Kind{providerDelivery.Slack, providerDelivery.Telegram, providerDelivery.Ntfy, providerDelivery.Gotify, providerDelivery.Pushover}

func messagingAPI(n *Node, path, method string, body any, token string) (int, []byte) {
	w := maintenanceAPIRequest(n, method, path, body, token)
	return w.Code, w.Body.Bytes()
}

func TestMessagingChannelIncidentReceiptsSurvivePeerAndRestart(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	var healthy atomic.Bool
	var mu sync.Mutex
	calls := map[string]int{}
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls[r.URL.Path]++
		mu.Unlock()
		switch {
		case strings.Contains(r.URL.Path, "/services/"):
			io.WriteString(w, "ok")
		case strings.HasPrefix(r.URL.Path, "/telegram/"):
			io.WriteString(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":12345}}}`)
		case strings.HasPrefix(r.URL.Path, "/ntfy/"):
			io.WriteString(w, `{"id":"Abcdef123456","event":"message","topic":"canary"}`)
		case strings.HasPrefix(r.URL.Path, "/gotify/"):
			io.WriteString(w, `{"id":1,"appid":2,"message":"fixture","date":"2026-10-22T00:00:00Z"}`)
		case strings.HasPrefix(r.URL.Path, "/pushover/"):
			io.WriteString(w, `{"status":1,"request":"647d2300-702c-4b38-8b2f-d56326ae460b"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer sink.Close()
	database := filepath.Join(t.TempDir(), "incidents.db")
	a := messagingNode(t, "a", "b", database, &clock)
	b := messagingNode(t, "b", "a", ":memory:", &clock)
	probe := func(context.Context, Monitor) ProbeResult {
		state := "down"
		if healthy.Load() {
			state = "up"
		}
		return ProbeResult{State: state, Message: "local fixture"}
	}
	a.probe, b.probe = probe, probe
	in := input([]string{"a", "b"}, "http://127.0.0.1:1", "")
	in.IntervalMs, in.TimeoutMs = 60000, 1000
	for _, kind := range messagingChannelKinds {
		channel := addMessagingChannel(t, a, messagingChannelInput(kind, sink.URL))
		in.NotificationIDs = append(in.NotificationIDs, channel.ID)
	}
	code, body := messagingAPI(a, "/api/monitors", "POST", in, adminToken)
	var m Monitor
	if code != 201 || json.Unmarshal(body, &m) != nil {
		t.Fatal("monitor fixture", code, string(body))
	}
	messagingPeerPage(t, a, b)
	partitionDrive(t, a)
	messagingPeerPage(t, a, b)
	partitionDrive(t, b)
	messagingPeerPage(t, b, a)
	messagingPeerPage(t, a, b)
	check := func(want int, nodes ...*Node) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if len(calls) != 5 {
			t.Fatal("missing provider route", calls)
		}
		for path, count := range calls {
			if count != want {
				t.Fatal("provider accepted duplicate or missing incident delivery", path, count, want)
			}
		}
		for _, node := range nodes {
			if len(node.receipts) != 5*want {
				t.Fatal("destination receipts did not persist or replicate", node.ID, len(node.receipts), want)
			}
			perTarget := map[string]int{}
			for _, receipt := range node.receipts {
				if !providerDelivery.IsMessaging(providerDelivery.Kind(receipt.Provider)) || receipt.DestinationID == "" {
					t.Fatal("receipt lost native provider or destination identity")
				}
				perTarget[receipt.DestinationID]++
			}
			if len(perTarget) != 5 {
				t.Fatal("receipt destinations collapsed", perTarget)
			}
			for _, count := range perTarget {
				if count != want {
					t.Fatal("destination lost down or recovery", count, want)
				}
			}
		}
	}
	check(1, a, b)
	if err := a.Stop(); err != nil {
		t.Fatal(err)
	}
	a = messagingNode(t, "a", "b", database, &clock)
	a.probe = probe
	stagger := max(100, a.settings.NotificationDelayMs, a.settings.SyncIntervalMs*3+a.settings.PeerTimeoutMs)
	// Exceed the backup sender's window: an absent receipt would now resend.
	clock.Add(stagger + 1)
	partitionDrive(t, a)
	partitionDrive(t, b)
	check(1, a, b)
	healthy.Store(true)
	clock.Add(1000)
	a.nextProbe[m.ID], b.nextProbe[m.ID] = 0, 0
	partitionDrive(t, a)
	partitionDrive(t, b)
	messagingPeerPage(t, b, a)
	partitionDrive(t, a)
	messagingPeerPage(t, a, b)
	clock.Add(stagger + 1)
	partitionDrive(t, b)
	messagingPeerPage(t, b, a)
	check(2, a, b)
}

func addMessagingChannel(t *testing.T, n *Node, in ChannelInput) Channel {
	t.Helper()
	code, body := messagingAPI(n, "/api/notification-channels", "POST", in, adminToken)
	var channel Channel
	if code != 201 || json.Unmarshal(body, &channel) != nil {
		t.Fatal("messaging channel creation", code, string(body))
	}
	return channel
}

func messagingChannelInput(kind providerDelivery.Kind, base string) ChannelInput {
	c := providerDelivery.Config{Kind: kind}
	switch kind {
	case providerDelivery.Slack:
		c.Slack = &notifyhttp.SlackConfig{WebhookURL: base + "/services/TTEST/BTEST/fixture_key", AllowInsecureHTTP: true}
	case providerDelivery.Telegram:
		c.Telegram = &notifyhttp.TelegramConfig{BotToken: "12345:fixture_bot", ChatID: "12345", BaseURL: base + "/telegram", AllowInsecureHTTP: true}
	case providerDelivery.Ntfy:
		c.Ntfy = &notifyhttp.NtfyConfig{Topic: "canary", BaseURL: base + "/ntfy", Token: "fixture_ntfy_key", AllowInsecureHTTP: true}
	case providerDelivery.Gotify:
		c.Gotify = &notifyhttp.GotifyConfig{AppToken: "fixture_gotify_key", BaseURL: base + "/gotify", AllowInsecureHTTP: true}
	case providerDelivery.Pushover:
		c.Pushover = &notifyhttp.PushoverConfig{AppToken: strings.Repeat("a", 30), UserKey: strings.Repeat("b", 30), BaseURL: base + "/pushover", AllowInsecureHTTP: true}
	}
	return ChannelInput{Name: "Fixture " + string(kind), Enabled: true, Config: c}
}

func messagingNode(t *testing.T, id, other, database string, clock *atomic.Int64) *Node {
	t.Helper()
	n, err := New(Options{NodeID: id, DBPath: database, Port: Ptr(0), Now: clock.Load,
		AdminToken: adminToken, ClusterToken: clusterToken, NotificationDelayMs: Ptr(int64(0)),
		Peers: []Peer{{ID: other, URL: "http://127.0.0.1:1"}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	return n
}

func messagingPeerPage(t *testing.T, from, to *Node) {
	t.Helper()
	cursor := ""
	for {
		page, err := from.snapshotPage(cursor, "")
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		validated, err := to.validateSnapshot(b, from.ID)
		if err != nil {
			t.Fatal("producer emitted a peer-invalid messaging page", err)
		}
		if err := to.mergeSnapshot(validated); err != nil {
			t.Fatal(err)
		}
		if page.Next == "" {
			return
		}
		cursor = page.Next
	}
}

func TestMessagingChannelCRUDTestsRedactionAndRestart(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	database := filepath.Join(t.TempDir(), "messaging.db")
	n := messagingNode(t, "a", "b", database, &clock)
	var calls atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch {
		case strings.Contains(r.URL.Path, "/services/"):
			io.WriteString(w, "ok")
		case strings.HasPrefix(r.URL.Path, "/telegram/"):
			io.WriteString(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":12345}}}`)
		case strings.HasPrefix(r.URL.Path, "/ntfy/"):
			io.WriteString(w, `{"id":"Abcdef123456","event":"message","topic":"canary"}`)
		case strings.HasPrefix(r.URL.Path, "/gotify/"):
			io.WriteString(w, `{"id":1,"appid":2,"message":"fixture","date":"2026-10-22T00:00:00Z"}`)
		case strings.HasPrefix(r.URL.Path, "/pushover/"):
			io.WriteString(w, `{"status":1,"request":"647d2300-702c-4b38-8b2f-d56326ae460b"}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer sink.Close()
	if code, _ := messagingAPI(n, "/api/accounts", "POST", map[string]string{"name": "viewer", "role": "viewer", "token": viewerToken}, adminToken); code != 201 {
		t.Fatal("viewer fixture", code)
	}
	channels := []Channel{}
	for _, kind := range messagingChannelKinds {
		c := addMessagingChannel(t, n, messagingChannelInput(kind, sink.URL))
		channels = append(channels, c)
		if code, _ := messagingAPI(n, "/api/notification-channels/"+c.ID+"/test", "POST", map[string]string{"revision": c.Revision}, viewerToken); code != 403 {
			t.Fatal("viewer initiated a messaging send", code)
		}
		clock.Add(6000)
		if code, b := messagingAPI(n, "/api/notification-channels/"+c.ID+"/test", "POST", map[string]string{"revision": c.Revision}, adminToken); code != 202 {
			t.Fatal("native notification test", code, string(b))
		}
		n.wg.Wait()
	}
	if calls.Load() != 5 {
		t.Fatal("messaging channels did not each send exactly once", calls.Load())
	}
	code, body := messagingAPI(n, "/api/notification-channels", "GET", nil, viewerToken)
	if code != 200 || strings.Contains(string(body), "127.0.0.1") || strings.Contains(string(body), "fixture_") || strings.Contains(string(body), strings.Repeat("b", 30)) {
		t.Fatal("viewer received a messaging address or credential", code)
	}
	code, body = messagingAPI(n, "/api/notification-deliveries", "GET", nil, adminToken)
	var attempts []DeliveryAttemptView
	if code != 200 || json.Unmarshal(body, &attempts) != nil || len(attempts) != 5 {
		t.Fatal("native attempts unavailable", code)
	}
	for _, attempt := range attempts {
		if attempt.State != "accepted" || attempt.HTTPStatus != 200 || attempt.Failures != 0 {
			t.Fatal("native acceptance was not persisted", attempt.Provider, attempt.State, attempt.ErrorCode)
		}
	}
	b := messagingNode(t, "b", "a", filepath.Join(t.TempDir(), "peer.db"), &clock)
	messagingPeerPage(t, n, b)
	for _, c := range channels {
		r := b.currentResource(resourceChannel, c.ID)
		if r == nil || targetsForChannel(channelOf(*r, "admin"))[0].ID != targetsForChannel(c)[0].ID {
			t.Fatal("replication changed a native destination identity")
		}
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = messagingNode(t, "a", "b", database, &clock)
	if len(n.channelViews("admin")) != 5 || len(n.attempts) != 5 || calls.Load() != 5 {
		t.Fatal("restart lost config/outcomes or replayed a notification test")
	}
	for _, c := range channels {
		r := n.currentResource(resourceChannel, c.ID)
		if r == nil || targetsForChannel(channelOf(*r, "admin"))[0].ID != targetsForChannel(c)[0].ID {
			t.Fatal("restart changed native route identity")
		}
	}
}

func TestMessagingChannelCredentialRotationAndResourceBinding(t *testing.T) {
	for _, kind := range messagingChannelKinds {
		t.Run(string(kind), func(t *testing.T) {
			c := Channel{ChannelInput: messagingChannelInput(kind, "http://127.0.0.1:1"), ID: "logical-channel", Revision: "revision-1"}
			before := targetsForChannel(c)
			if len(before) != 1 || before[0].Provider != string(kind) || len(before[0].ID) != 64 {
				t.Fatal("native provider did not produce one bounded destination")
			}
			switch kind {
			case providerDelivery.Slack:
				c.Config.Slack.WebhookURL = strings.ReplaceAll(c.Config.Slack.WebhookURL, "fixture_key", "rotated_key")
			case providerDelivery.Telegram:
				c.Config.Telegram.BotToken = "54321:rotated_bot"
			case providerDelivery.Ntfy:
				c.Config.Ntfy.Token = "rotated_key"
			case providerDelivery.Gotify:
				c.Config.Gotify.AppToken = "rotated_same_app_key"
			case providerDelivery.Pushover:
				c.Config.Pushover.AppToken = strings.Repeat("c", 30)
			}
			c.Revision = "revision-2"
			if targetsForChannel(c)[0].ID != before[0].ID {
				t.Fatal("credential-only rotation lost receipt continuity")
			}
			c.ID = "different-logical-channel"
			if targetsForChannel(c)[0].ID == before[0].ID {
				t.Fatal("distinct channel resources share destination receipts")
			}
		})
	}
}

func TestMessagingChannelWaitsForReferencedRevisionProof(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	a := messagingNode(t, "a", "b", ":memory:", &clock)
	b := messagingNode(t, "b", "a", ":memory:", &clock)
	c := addMessagingChannel(t, a, messagingChannelInput(providerDelivery.Gotify, "http://127.0.0.1:1"))
	in := input([]string{"a"}, "http://127.0.0.1:1", "")
	in.NotificationIDs = []string{c.ID}
	code, body := messagingAPI(a, "/api/monitors", "POST", in, adminToken)
	var m Monitor
	if code != 201 || json.Unmarshal(body, &m) != nil {
		t.Fatal("monitor fixture", code, string(body))
	}
	page, err := a.snapshotPage("", "")
	if err != nil {
		t.Fatal(err)
	}
	edgeID := a.resources[resourceKey(resourceChannel, c.ID)][0].EdgeID
	var held RevisionEdge
	kept := []RevisionEdge{}
	for _, edge := range page.RevisionEdges {
		if edge.ID == edgeID {
			held = edge
		} else {
			kept = append(kept, edge)
		}
	}
	page.RevisionEdges = kept
	raw, _ := json.Marshal(page)
	validated, err := b.validateSnapshot(raw, "a")
	if err != nil || held.ID == "" {
		t.Fatal("partial proof fixture", err)
	}
	if err := b.mergeSnapshot(validated); err != nil {
		t.Fatal(err)
	}
	if b.current(m.ID) == nil || b.currentResource(resourceChannel, c.ID) != nil || len(b.notificationTargets(m)) != 0 || !b.channelViews("admin")[0].ConfigConflict {
		t.Fatal("unproved native channel acquired executable routing authority")
	}
	if err := b.mergeSnapshot(Snapshot{RevisionEdges: []RevisionEdge{held}}); err != nil {
		t.Fatal(err)
	}
	if b.currentResource(resourceChannel, c.ID) == nil || len(b.notificationTargets(m)) != 1 || b.channelViews("admin")[0].ConfigConflict {
		t.Fatal("late edge proof did not release the exact native route")
	}
}
