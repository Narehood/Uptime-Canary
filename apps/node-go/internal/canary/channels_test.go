package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	providerDelivery "uptime-canary/apps/node-go/internal/delivery"
)

func addChannel(t *testing.T, n *Node, in ChannelInput) Channel {
	t.Helper()
	code, b := api(t, n, "/api/notification-channels", "POST", in, adminToken)
	var c Channel
	if code != 201 || json.Unmarshal(b, &c) != nil {
		t.Fatalf("channel create: %d %s", code, b)
	}
	return c
}

func hookChannel(name, target string) ChannelInput {
	return ChannelInput{Name: name, Enabled: true, Config: providerDelivery.Config{Kind: providerDelivery.Webhook, Webhook: providerDelivery.WebhookConfig{URL: target, BearerToken: "fixture-only-bearer", AllowInsecureHTTP: true}}}
}

func TestChannelReplicationConflictsTombstonesAndViewerRedaction(t *testing.T) {
	c := cluster(t, []string{"a", "b"}, nil)
	a, b := c.nodes[0], c.nodes[1]
	channel := addChannel(t, a, hookChannel("Operations", "http://127.0.0.1:1/hook"))
	eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.currentResource(resourceChannel, channel.ID) != nil
	})
	a.mu.Lock()
	root := a.resources[resourceKey(resourceChannel, channel.ID)][0]
	a.mu.Unlock()
	c.split([]string{"a"}, []string{"b"})
	for index, n := range c.nodes {
		edit := channel
		edit.Name = []string{"Operations A", "Operations B"}[index]
		code, result := api(t, n, "/api/notification-channels/"+channel.ID, "PUT", edit, adminToken)
		if code != 200 {
			t.Fatalf("channel edit: %d %s", code, result)
		}
	}
	c.heal()
	eventually(t, func() bool {
		for _, n := range c.nodes {
			n.mu.Lock()
			count := len(n.resources[resourceKey(resourceChannel, channel.ID)])
			n.mu.Unlock()
			if count != 2 {
				return false
			}
		}
		return true
	})
	code, result := api(t, a, "/api/accounts", "POST", map[string]string{"name": "channel-viewer", "role": "viewer", "token": viewerToken}, adminToken)
	if code != 201 {
		t.Fatalf("viewer: %d %s", code, result)
	}
	code, result = api(t, a, "/api/notification-channels/"+channel.ID+"/conflicts", "GET", nil, viewerToken)
	if code != 200 || strings.Contains(string(result), "fixture-only-bearer") || strings.Contains(string(result), "127.0.0.1") {
		t.Fatal("viewer received channel secrets", code)
	}
	code, _ = api(t, a, "/api/notification-channels", "POST", channel.ChannelInput, viewerToken)
	if code != 403 {
		t.Fatal("viewer modified channels", code)
	}
	code, result = api(t, a, "/api/notification-channels/"+channel.ID+"/conflicts", "GET", nil, adminToken)
	var heads []Channel
	if code != 200 || json.Unmarshal(result, &heads) != nil || len(heads) != 2 {
		t.Fatal("conflicts unavailable", code)
	}
	in := map[string]any{"name": "Chosen operations", "enabled": true, "config": channel.Config, "revisions": []string{heads[0].Revision}}
	code, _ = api(t, a, "/api/notification-channels/"+channel.ID+"/resolve", "POST", in, adminToken)
	if code != 409 {
		t.Fatal("incomplete resolution accepted", code)
	}
	in["revisions"] = []string{heads[0].Revision, heads[1].Revision}
	code, result = api(t, a, "/api/notification-channels/"+channel.ID+"/resolve", "POST", in, adminToken)
	var resolved Channel
	if code != 200 || json.Unmarshal(result, &resolved) != nil {
		t.Fatalf("resolution: %d %s", code, result)
	}
	code, _ = api(t, a, "/api/notification-channels/"+channel.ID, "DELETE", map[string]string{"revision": resolved.Revision}, adminToken)
	if code != 204 {
		t.Fatal("channel delete", code)
	}
	eventually(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		h := b.resources[resourceKey(resourceChannel, channel.ID)]
		return len(h) == 1 && h[0].Resource.Deleted
	})
	b.mu.Lock()
	err := b.mergeSnapshot(Snapshot{Resources: []ResourceVersion{root}})
	resurrected := b.currentResource(resourceChannel, channel.ID) != nil
	b.mu.Unlock()
	if err != nil || resurrected {
		t.Fatal("delayed configuration resurrected a deleted channel", err)
	}
}

func TestEmailFanoutRetriesOnlyTheUnacceptedRecipient(t *testing.T) {
	type sent struct{ email, key string }
	var mu sync.Mutex
	requests := []sent{}
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			To      []providerDelivery.Address `json:"to"`
			Headers map[string]string          `json:"headers"`
		}
		if r.URL.Path != "/v3/smtp/email" || r.Header.Get("api-key") != "fixture-email-api-key" || json.NewDecoder(r.Body).Decode(&request) != nil || len(request.To) != 1 {
			w.WriteHeader(400)
			return
		}
		email := request.To[0].Email
		mu.Lock()
		counts[email]++
		count := counts[email]
		requests = append(requests, sent{email, request.Headers["X-Canary-Idempotency-Key"]})
		mu.Unlock()
		if email == "beta@example.test" && count == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(429)
			return
		}
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"messageId":"fixture-provider-id"}`))
	}))
	defer server.Close()
	var down atomic.Bool
	down.Store(true)
	n := local(t, func(o *Options) { o.Probe = controlled(&down) })
	t.Cleanup(func() {
		if t.Failed() {
			n.mu.Lock()
			for _, a := range n.attempts {
				t.Logf("delivery %s: %s failures=%d status=%d code=%s", a.Provider, a.State, a.Failures, a.HTTPStatus, a.ErrorCode)
			}
			n.mu.Unlock()
			mu.Lock()
			t.Logf("fixture recipient counts: %v", counts)
			mu.Unlock()
		}
	})
	legacy := webhook(t)
	c := addChannel(t, n, ChannelInput{Name: "Email", Enabled: true, Config: providerDelivery.Config{Kind: providerDelivery.Brevo, Sender: providerDelivery.Address{Email: "canary@example.test"}, Recipients: []providerDelivery.Address{{Email: "alpha@example.test"}, {Email: "beta@example.test"}}, Brevo: providerDelivery.BrevoConfig{APIKey: "fixture-email-api-key", BaseURL: server.URL, AllowInsecureHTTP: true}}})
	in := input([]string{n.ID}, "http://127.0.0.1:1", legacy.URL)
	in.NotificationIDs = []string{c.ID}
	m := add(t, n, in)
	eventually(t, func() bool { return len(getOverview(t, n).Notifications) == 3 })
	mu.Lock()
	if counts["alpha@example.test"] != 1 || counts["beta@example.test"] != 2 || len(requests) != 3 {
		mu.Unlock()
		t.Fatal("retry resent an accepted recipient")
	}
	betaKeys := []string{}
	for _, request := range requests {
		if request.email == "beta@example.test" {
			betaKeys = append(betaKeys, request.key)
		}
	}
	mu.Unlock()
	if len(betaKeys) != 2 || betaKeys[0] == "" || betaKeys[0] != betaKeys[1] {
		t.Fatal("retry identity changed")
	}
	if legacy.count("down") != 1 {
		t.Fatal("email retry replayed the legacy webhook")
	}
	code, result := api(t, n, "/api/notification-deliveries?monitorId="+m.ID, "GET", nil, adminToken)
	var attempts []DeliveryAttemptView
	if code != 200 || json.Unmarshal(result, &attempts) != nil || len(attempts) != 3 || strings.Contains(string(result), "fixture-email-api-key") || strings.Contains(string(result), "example.test") {
		t.Fatal("invalid delivery status", code)
	}
	for _, a := range attempts {
		if a.State != "accepted" {
			t.Fatal("accepted recipient not reported", a.State)
		}
	}
	down.Store(false)
	eventually(t, func() bool { return len(getOverview(t, n).Notifications) == 6 })
	mu.Lock()
	defer mu.Unlock()
	if counts["alpha@example.test"] != 2 || counts["beta@example.test"] != 3 || legacy.count("recovery") != 1 {
		t.Fatal("recovery did not independently follow each down acceptance")
	}
}

func TestGroupRequiresDeliveryForEachNamedDestination(t *testing.T) {
	var parentUp atomic.Bool
	var mu sync.Mutex
	a, b := map[string]int{}, map[string]int{}
	makeReceiver := func(counts map[string]int, rejectParent bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Monitor Identity `json:"monitor"`
				Kind    string   `json:"kind"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				w.WriteHeader(400)
				return
			}
			mu.Lock()
			counts[body.Monitor.Name+"/"+body.Kind]++
			mu.Unlock()
			if rejectParent && body.Monitor.Name == "Parent" {
				w.WriteHeader(503)
				return
			}
			w.WriteHeader(204)
		}))
	}
	first, second := makeReceiver(a, false), makeReceiver(b, true)
	defer first.Close()
	defer second.Close()
	n := local(t, func(o *Options) {
		o.Probe = func(_ context.Context, m Monitor) ProbeResult {
			if m.Name == "Parent" && parentUp.Load() {
				return ProbeResult{State: "up", Message: "fixture recovered"}
			}
			return ProbeResult{State: "down", Message: "fixture down"}
		}
	})
	one := addChannel(t, n, hookChannel("One", first.URL))
	two := addChannel(t, n, hookChannel("Two", second.URL))
	in := input([]string{n.ID}, "http://127.0.0.1:1", "")
	in.Name, in.NotificationIDs = "Parent", []string{one.ID, two.ID}
	parent := add(t, n, in)
	g := addGroup(t, n, GroupInput{Name: "User-defined group", NodeIDs: []string{n.ID}, OutageMonitorID: parent.ID, GraceMs: 250})
	in.Name, in.GroupID = "Child", g.ID
	child := add(t, n, in)
	eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return a["Parent/down"] == 1 && b["Child/down"] == 1 })
	time.Sleep(300 * time.Millisecond)
	mu.Lock()
	childA, childB := a["Child/down"], b["Child/down"]
	mu.Unlock()
	if childA != 0 || childB != 1 {
		t.Fatal("delivery on one route suppressed an unrelated route", childA, childB)
	}
	code, result := api(t, n, "/api/notification-deliveries?monitorId="+child.ID, "GET", nil, adminToken)
	var attempts []DeliveryAttemptView
	if code != 200 || json.Unmarshal(result, &attempts) != nil || len(attempts) != 2 {
		t.Fatal("child delivery status missing")
	}
	states := []string{attempts[0].State, attempts[1].State}
	slices.Sort(states)
	if !slices.Equal(states, []string{"accepted", "held"}) {
		t.Fatal("partial suppression hidden", states)
	}
	parentUp.Store(true)
	eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return a["Child/down"] == 1 })
}

func TestNotificationTestUsesAdapterWithoutInventingMonitoringState(t *testing.T) {
	var got atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Kind    string   `json:"kind"`
			Monitor Identity `json:"monitor"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Kind != "test" || body.Monitor.ID != "" || r.Header.Get("Authorization") != "Bearer fixture-only-bearer" {
			w.WriteHeader(400)
			return
		}
		got.Add(1)
		w.WriteHeader(202)
	}))
	defer server.Close()
	n := local(t, nil)
	c := addChannel(t, n, hookChannel("Test destination", server.URL))
	path := "/api/notification-channels/" + c.ID + "/test"
	code, _ := api(t, n, path, "POST", map[string]string{"revision": "stale"}, adminToken)
	if code != 409 {
		t.Fatal("stale notification test accepted", code)
	}
	code, result := api(t, n, path, "POST", map[string]string{"revision": c.Revision}, adminToken)
	if code != 202 {
		t.Fatalf("test queued: %d %s", code, result)
	}
	eventually(t, func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()
		for _, a := range n.attempts {
			if a.State == "accepted" {
				return true
			}
		}
		return false
	})
	code, _ = api(t, n, path, "POST", map[string]string{"revision": c.Revision}, adminToken)
	if code != 429 {
		t.Fatal("notification tests not bounded", code)
	}
	n.mu.Lock()
	invented := len(n.heads) + len(n.episodes) + len(n.receipts) + len(n.samples)
	n.mu.Unlock()
	if got.Load() != 1 || invented != 0 {
		t.Fatal("notification test changed monitoring state", got.Load(), invented)
	}
}

func TestOldPeerProtocolRejectedBeforeExpandedConfigurationIsMerged(t *testing.T) {
	n := local(t, func(o *Options) { o.Peers = []Peer{{ID: "b", URL: "http://127.0.0.1:1"}} })
	for _, protocol := range []int{1, 2, 3} {
		s := Snapshot{Protocol: protocol, Node: Identity{"b", "Old peer"}, Versions: []Version{}, Observations: []Sample{}, Incidents: []Incident{}, Receipts: []Receipt{}}
		b, _ := json.Marshal(s)
		req := httptest.NewRequest("POST", "/internal/snapshot", strings.NewReader(string(b)))
		req.Header.Set("Authorization", "Bearer "+clusterToken)
		req.Header.Set("X-Canary-Node-Id", "b")
		w := httptest.NewRecorder()
		n.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatal("old peer schema was accepted", protocol, w.Code)
		}
	}
}
