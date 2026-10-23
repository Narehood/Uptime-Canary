package canary

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

const discordFixtureURL = "https://discord.com/api/v10/webhooks/123456789012345678/fixture-secret-token"

func TestNotificationProviderDiscordURLDetection(t *testing.T) {
	for _, target := range []string{
		discordFixtureURL,
		"https://discord.com/api/webhooks/123/token",
		"https://DISCORD.COM:443/api/v9/webhooks/123/token?thread_id=456&wait=false",
		"https://discordapp.com/api/webhooks/123/token",
		"https://canary.discord.com/api/webhooks/123/token",
		"https://ptb.discord.com/api/webhooks/123/token",
	} {
		if got := notificationProviderForURL(target); got != notificationDiscord {
			t.Errorf("Official Discord execute-webhook route selected %s", got)
		}
	}
	for _, target := range []string{
		"http://discord.com/api/webhooks/123/token",
		"https://discord.com.attacker.example/api/webhooks/123/token",
		"https://attacker-discord.com/api/webhooks/123/token",
		"https://discordapp.com.attacker.example/api/webhooks/123/token",
		"https://discord.com@attacker.example/api/webhooks/123/token",
		"https://attacker.example@discord.com/api/webhooks/123/token",
		"https://discord.com:8443/api/webhooks/123/token",
		"https://discord.com/api/webhooks/123/token/messages/456",
		"https://discord.com/api/webhooks/123/token/slack",
		"https://discord.com/api/webhooks/123/token/github",
		"https://discord.com/api/webhooks/name/token",
		"https://discord.com/api/vx/webhooks/123/token",
		"https://discord.com/api/v0/webhooks/123/token",
		"https://discord.com/api/webhooks/123/token%2Fextra",
		"https://discord.com/api/webhooks/123/token#fragment",
		"https://discord.com/api/webhooks/123/",
		"https://discord.com./api/webhooks/123/token",
		"https://other.discord.com/api/webhooks/123/token",
		"https://webhook.example/discord.com/api/webhooks/123/token",
		"https://discord.com/api/webhooks/123/token/",
		"://invalid",
	} {
		if got := notificationProviderForURL(target); got != notificationGeneric {
			t.Errorf("Non-execute destination incorrectly selected %s", got)
		}
	}
}

// The fixture rejects the old Canary envelope just as executeWebhook does. It
// is reached through an injected Doer; no Discord request leaves this process.
func strictDiscordFixture(t *testing.T, r *http.Request) string {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.Scheme != "https" || r.URL.Host != "discord.com" || !discordWebhookPath.MatchString(r.URL.Path) {
		t.Error("Discord request did not use the official execute-webhook destination")
	}
	if r.URL.Query().Get("wait") != "true" || len(r.URL.Query()["wait"]) != 1 {
		t.Error("Discord delivery did not request a confirmed message")
	}
	if r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Idempotency-Key") == "" {
		t.Error("Notification request lost its content type or persisted attempt key")
	}
	var payload map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		t.Error("Invalid Discord JSON payload")
		return ""
	}
	var content string
	if json.Unmarshal(payload["content"], &content) != nil || strings.TrimSpace(content) == "" || !utf8.ValidString(content) || len(utf16.Encode([]rune(content))) > 2000 {
		t.Error("Discord message content must be nonempty, valid UTF-8, and at most 2000 UTF-16 units")
	}
	var mentions map[string]json.RawMessage
	if json.Unmarshal(payload["allowed_mentions"], &mentions) != nil || string(mentions["parse"]) != "[]" || len(mentions) != 1 {
		t.Error("Automatic mentions were not explicitly disabled")
	}
	if len(payload) != 2 {
		t.Error("Discord received unsupported notification fields")
	}
	return content
}

func notificationFixtureResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestNotificationProviderDiscordLifecycle(t *testing.T) {
	now, nowFn := clock()
	var down atomic.Bool
	down.Store(true)
	var mu sync.Mutex
	var contents, keys []string
	client := doFunc(func(r *http.Request) (*http.Response, error) {
		content := strictDiscordFixture(t, r)
		mu.Lock()
		contents, keys = append(contents, content), append(keys, r.Header.Get("Idempotency-Key"))
		mu.Unlock()
		return notificationFixtureResponse(200, `{"id":"987654321012345678","channel_id":"123"}`), nil
	})
	n := local(t, func(o *Options) {
		o.Now, o.Client, o.Probe = nowFn, client, controlled(&down)
		o.NotificationDelayMs = Ptr(int64(0))
	})
	in := input([]string{"a"}, "http://127.0.0.1:1", discordFixtureURL+"?wait=false&wait=false&thread_id=456")
	in.Name, in.RepeatAfterMs = "API @everyone <@123> <@&456>", Ptr(int64(1000))
	add(t, n, in)
	eventually(t, func() bool { return len(getOverview(t, n).Notifications) == 1 })
	now.Add(1000)
	eventually(t, func() bool { return len(getOverview(t, n).Notifications) == 2 })
	down.Store(false)
	now.Add(100)
	eventually(t, func() bool { return len(getOverview(t, n).Notifications) == 3 })
	mu.Lock()
	defer mu.Unlock()
	if len(contents) != 3 || !strings.Contains(contents[0], "Uptime Canary: DOWN") || !strings.Contains(contents[1], "STILL DOWN (repeat 1)") || !strings.Contains(contents[2], "RECOVERED") {
		t.Fatal("Outage, repeat, and recovery did not each reach the Discord fixture")
	}
	if !strings.Contains(contents[0], in.Name) || !strings.Contains(contents[2], "Recovered:") {
		t.Fatal("Discord messages lost monitor context or recovery time")
	}
	if keys[0] == keys[1] || keys[1] == keys[2] || keys[0] == keys[2] {
		t.Fatal("Distinct lifecycle events reused an attempt key")
	}
}

func TestNotificationProviderDiscordBoundedContentAndRedaction(t *testing.T) {
	m := Monitor{MonitorInput: input([]string{"a"}, "", discordFixtureURL)}
	m.Name = "@everyone <@123> <@&456> " + discordFixtureURL + strings.Repeat("🦜", 3000) + "\xff"
	observations := make([]Sample, 20)
	for index := range observations {
		observations[index].Observation = Observation{NodeID: strings.Repeat("監", 300), State: "down", Message: "fixture-secret-token " + strings.Repeat("é🦜", 1000)}
	}
	body, err := notificationDiscord.format(m, Incident{OpenedAt: 1}, attempt{IncidentID: "stable-id", Kind: "down"}, Identity{"sender", strings.Repeat("🦜", 200)}, observations)
	if err != nil || !utf8.Valid(body) {
		t.Fatal("Discord formatter produced invalid JSON or UTF-8")
	}
	req, _ := http.NewRequest(http.MethodPost, discordFixtureURL+"?wait=true", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "stable-attempt")
	content := strictDiscordFixture(t, req)
	if strings.Contains(content, "fixture-secret-token") || strings.Contains(content, discordFixtureURL) || !strings.Contains(content, "[redacted webhook]") {
		t.Fatal("Configured webhook credential leaked through display text")
	}
	if !strings.Contains(content, "Incident: stable-id") || !strings.Contains(content, "(15 more observations)") {
		t.Fatal("Bounded Discord content lost useful incident context")
	}
	for _, value := range []string{strings.Repeat("a", 2000), strings.Repeat("🦜", 1000)} {
		if got := discordBoundText(value, 2000); got != value {
			t.Fatal("An exactly fitting message was unnecessarily truncated")
		}
		got := discordBoundText(value+"🦜", 2000)
		if !utf8.ValidString(got) || len(utf16.Encode([]rune(got))) > 2000 || !strings.HasSuffix(got, "…") {
			t.Fatal("Discord truncation split Unicode or exceeded its character budget")
		}
	}
}

func TestNotificationProviderDeliveryConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name, target, response string
		status                 int
		delivered              bool
	}{
		{"discord-confirmed", discordFixtureURL, `{"id":"123"}`, 200, true},
		{"discord-empty", discordFixtureURL, "", 200, false},
		{"discord-unconfirmed", discordFixtureURL, "", 204, false},
		{"discord-accepted", discordFixtureURL, `{"id":"123"}`, 202, false},
		{"discord-invalid-message", discordFixtureURL, `{"id":"not-a-snowflake"}`, 200, false},
		{"discord-rejected", discordFixtureURL, `{"message":"secret provider error"}`, 400, false},
		{"generic-accepted", "http://provider.example", "", 202, true},
		{"generic-no-content", "http://provider.example", "", 204, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, m, incident, a := queuedAliasFixture(t, doFunc(func(*http.Request) (*http.Response, error) {
				return notificationFixtureResponse(tc.status, tc.response), nil
			}))
			// This node is not running; update only this isolated in-memory fixture.
			m.WebhookURL = tc.target
			m, err := n.writeMonitor(m.MonitorInput, n.heads[m.ID], m.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: n.now()}, Revision: m.Revision, DownSince: Ptr(int64(1))}
			n.sendNotification(m, incident, a, m.ID+"/down", nil)
			_, receiptExists, err := loadOne[Receipt](n.store, "receipts", a.ID)
			if err != nil || receiptExists != tc.delivered {
				t.Fatal("Delivery receipt did not match provider confirmation", receiptExists, err)
			}
			if !tc.delivered {
				pending, exists, err := loadOne[attempt](n.store, "attempts", a.ID)
				if err != nil || !exists || pending.ID != a.ID || pending.Failures != 1 || pending.NextAt <= n.now() {
					t.Fatal("Failed provider send did not retain a pending stable attempt")
				}
			}
		})
	}
}

func TestNotificationProviderDiscordRateLimitRetainsPendingAttempt(t *testing.T) {
	for _, tc := range []struct {
		name, header, body string
		wait               int64
	}{
		{"header", "3", `{"retry_after":1}`, 3000},
		{"json-fractional", "", `{"retry_after":2.3451}`, 2346},
		{"json-larger", "1", `{"retry_after":3.5}`, 3500},
		{"malformed-fallback", "not-a-delay", `{"retry_after":-1}`, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now, nowFn := clock()
			var calls atomic.Int64
			var mu sync.Mutex
			var keys []string
			client := doFunc(func(r *http.Request) (*http.Response, error) {
				strictDiscordFixture(t, r)
				mu.Lock()
				keys = append(keys, r.Header.Get("Idempotency-Key"))
				mu.Unlock()
				if calls.Add(1) == 1 {
					resp := notificationFixtureResponse(429, tc.body)
					resp.Header.Set("Retry-After", tc.header)
					return resp, nil
				}
				return notificationFixtureResponse(200, `{"id":"123"}`), nil
			})
			n := local(t, func(o *Options) {
				o.Now, o.Client = nowFn, client
				o.NotificationDelayMs = Ptr(int64(0))
				o.Probe = func(context.Context, Monitor) ProbeResult {
					return ProbeResult{State: "down", LatencyMs: 1, Message: "HTTP 503"}
				}
			})
			add(t, n, input([]string{"a"}, "http://127.0.0.1:1", discordFixtureURL))
			var first attempt
			eventually(t, func() bool {
				n.mu.Lock()
				defer n.mu.Unlock()
				for _, pending := range n.attempts {
					if pending.Failures == 1 {
						first = pending
						return true
					}
				}
				return false
			})
			if first.NextAt != now.Load()+tc.wait || len(getOverview(t, n).Notifications) != 0 {
				t.Fatal("Rate limit created a receipt or used the wrong retry delay")
			}
			now.Add(tc.wait - 1)
			time.Sleep(50 * time.Millisecond)
			if calls.Load() != 1 {
				t.Fatal("Rate-limited attempt retried before its persisted deadline")
			}
			now.Add(1)
			eventually(t, func() bool { return len(getOverview(t, n).Notifications) == 1 })
			mu.Lock()
			defer mu.Unlock()
			if calls.Load() != 2 || len(keys) != 2 || keys[0] != first.ID || keys[1] != first.ID {
				t.Fatal("Rate-limit retry changed the persisted attempt identity")
			}
		})
	}
}

func TestNotificationProviderRetryParsingBounds(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC).UnixMilli()
	for _, tc := range []struct {
		header, body string
		want         int64
	}{
		{"0.25", "", 1000},
		{"1.2341", "", 1235},
		{"999999999999999", "", notificationRetryLimitMs},
		{"", `{"retry_after":1e100}`, notificationRetryLimitMs},
		{time.UnixMilli(now + 5000).UTC().Format(http.TimeFormat), "", 5000},
		{time.UnixMilli(now - 5000).UTC().Format(http.TimeFormat), "", 0},
		{"NaN", `{"retry_after":null}`, 0},
		{"+Inf", `{"retry_after":-20}`, 0},
		{"-1", `not JSON`, 0},
		{"", `{"retry_after":2} {"retry_after":3}`, 0},
	} {
		if got := notificationRetryAfter(tc.header, []byte(tc.body), now); got != tc.want {
			t.Errorf("Retry delay = %d; want %d", got, tc.want)
		}
	}
}

func TestNotificationProviderDiscordQueuedDeliveryRechecksAliasReceipts(t *testing.T) {
	var calls atomic.Int64
	n, m, captured, a := queuedAliasFixture(t, doFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return notificationFixtureResponse(200, `{"id":"123"}`), nil
	}))
	m.WebhookURL = discordFixtureURL
	m, err := n.writeMonitor(m.MonitorInput, n.heads[m.ID], m.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "down", CheckedAt: n.now()}, Revision: m.Revision, DownSince: Ptr(int64(1))}
	merged := captured
	merged.ID, merged.Aliases = "A", []string{"A", "B"}
	if err := n.mergeSnapshot(Snapshot{Incidents: []Incident{merged}, Receipts: []Receipt{{NotificationView{ID: "delivered-on-A", MonitorID: m.ID, IncidentID: "A", Kind: "down", NodeID: "a", SentAt: n.now()}, 0}}}); err != nil {
		t.Fatal(err)
	}
	n.sendNotification(m, captured, a, m.ID+"/down", nil)
	if calls.Load() != 0 {
		t.Fatal("Discord adapter bypassed the final alias/receipt delivery gate")
	}
}

func TestNotificationProviderCanonicalWaitQueryAndGenericEnvelope(t *testing.T) {
	for _, target := range []string{
		"https://discordapp.com/api/webhooks/123/token?thread_id=456&wait=false",
		"https://canary.discord.com/api/v10/webhooks/123/token?thread_id=456&wait=false",
		"https://ptb.discord.com/api/webhooks/123/token?thread_id=456&wait=false",
	} {
		result := deliverNotification(context.Background(), doFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != "discord.com" || r.URL.Query().Get("thread_id") != "456" || r.URL.Query().Get("wait") != "true" {
				t.Error("Canonical Discord destination lost its thread or confirmation query")
			}
			return notificationFixtureResponse(200, `{"id":"123"}`), nil
		}), target, []byte(`{"content":"message"}`), "stable-attempt", 0)
		if !result.Delivered {
			t.Fatal("Canonical Discord request did not confirm delivery")
		}
	}
	m := Monitor{MonitorInput: input([]string{"a"}, "", "http://provider.example/path?wait=false")}
	m.ID = "monitor"
	incident := Incident{ID: "canonical", Aliases: []string{"alias", "canonical"}}
	a := attempt{ID: "stable-attempt", IncidentID: "alias", Kind: "repeat", Slot: 2}
	body, err := notificationGeneric.format(m, incident, a, Identity{"a", "sender"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := deliverNotification(context.Background(), doFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != m.WebhookURL || r.Header.Get("Idempotency-Key") != a.ID {
			t.Error("Generic webhook URL or persisted attempt key changed")
		}
		var event map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&event) != nil || string(event["kind"]) != `"repeat"` || string(event["incidentId"]) != `"alias"` || string(event["repeatSlot"]) != "2" || string(event["observations"]) != "[]" || event["content"] != nil {
			t.Error("Generic webhook envelope changed")
		}
		return notificationFixtureResponse(202, ""), nil
	}), m.WebhookURL, body, a.ID, 0)
	if !result.Delivered {
		t.Fatal("Generic 202 acceptance no longer produces delivery success")
	}
}

type notificationCountingBody struct {
	read, closed int
}

func (b *notificationCountingBody) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	b.read += len(p)
	return len(p), nil
}

func (b *notificationCountingBody) Close() error { b.closed++; return nil }

func TestNotificationProviderBoundsResponseAndDiscardsSecrets(t *testing.T) {
	for _, status := range []int{200, 400, 429} {
		body := &notificationCountingBody{}
		result := deliverNotification(context.Background(), doFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: body}, nil
		}), discordFixtureURL, nil, "attempt", 0)
		if result.Delivered || body.read != notificationResponseLimit+1 || body.closed != 1 {
			t.Fatal("Provider body was not bounded/closed or an oversized message was confirmed")
		}
	}
	body := &notificationCountingBody{}
	result := deliverNotification(context.Background(), doFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{Body: body}, errors.New("transport failed for " + discordFixtureURL)
	}), discordFixtureURL, nil, "attempt", 0)
	encoded, err := json.Marshal(result)
	if err != nil || strings.Contains(string(encoded), "fixture-secret-token") || result.Delivered || body.closed != 1 {
		t.Fatal("Transport error leaked a secret or failed to close the response")
	}
}

func TestNotificationProviderDoesNotFollowRedirects(t *testing.T) {
	var leaked atomic.Int64
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer sink.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", sink.URL+"/credential-token")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	client := &http.Client{}
	result := deliverNotification(context.Background(), client, redirect.URL+"/webhook-token", []byte("private body"), "attempt", 0)
	if result.Delivered || leaked.Load() != 0 || client.CheckRedirect != nil {
		t.Fatal("Notification followed a redirect or mutated the shared client's policy")
	}
}
