package canary

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	providerDelivery "uptime-canary/apps/node-go/internal/delivery"
)

func TestNotificationTemplateDiscordChannelLifecycleReachesConfirmedAdapter(t *testing.T) {
	for _, embed := range []bool{false, true} {
		t.Run(fmt.Sprintf("card=%t", embed), func(t *testing.T) {
			now, nowFn := clock()
			var down atomic.Bool
			down.Store(true)
			var mu sync.Mutex
			messages := []string{}
			client := doFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "discord.com" || r.URL.Query().Get("wait") != "true" || r.Header.Get("Idempotency-Key") == "" {
					t.Error("custom channel bypassed confirmed Discord delivery")
				}
				var payload struct {
					Content string
					Embeds  []struct{ Title, Description string }
					Allowed struct{ Parse []string } `json:"allowed_mentions"`
				}
				if json.NewDecoder(r.Body).Decode(&payload) != nil || payload.Allowed.Parse == nil || len(payload.Allowed.Parse) != 0 {
					t.Error("custom delivery lost mention suppression")
				}
				text := payload.Content
				if embed {
					if len(payload.Embeds) != 1 {
						t.Error("card missing")
					} else {
						text = payload.Embeds[0].Title + "\n" + payload.Embeds[0].Description
					}
				}
				mu.Lock()
				messages = append(messages, text)
				mu.Unlock()
				return notificationFixtureResponse(200, `{"id":"987654321012345678"}`), nil
			})
			n := local(t, func(o *Options) {
				o.Now, o.Client, o.Probe = nowFn, client, controlled(&down)
				o.NotificationDelayMs = Ptr(int64(0))
			})
			channel := addChannel(t, n, ChannelInput{Name: "Template lifecycle", Enabled: true, Config: providerDelivery.Config{Kind: "discord", Webhook: providerDelivery.WebhookConfig{URL: discordFixtureURL, Embed: embed}, Templates: providerDelivery.Templates{Subject: "{{status}} {{monitor_name}}", Text: "{{monitor_type}} {{monitor_target}}\n{{node_results}}\n{{opened_at}}\n{{resolved_at}}\n{{downtime}}\n{{sender_node}}"}}})
			in := input([]string{n.ID}, "https://example.test/private-path?token=private-query", "")
			in.Name, in.NotificationIDs, in.RepeatAfterMs = "API *service*", []string{channel.ID}, Ptr(int64(1000))
			add(t, n, in)
			eventually(t, func() bool { return len(getOverview(t, n).Notifications) == 1 })
			now.Add(1000)
			eventually(t, func() bool { return len(getOverview(t, n).Notifications) == 2 })
			down.Store(false)
			now.Add(100)
			eventually(t, func() bool { return len(getOverview(t, n).Notifications) == 3 })
			mu.Lock()
			defer mu.Unlock()
			if len(messages) != 3 || !strings.HasPrefix(messages[0], "DOWN ") || !strings.HasPrefix(messages[1], "STILL DOWN ") || !strings.HasPrefix(messages[2], "UP ") {
				t.Fatal("new templates did not traverse outage, repeat and recovery")
			}
			for _, text := range messages {
				if !strings.Contains(text, "https://example.test") || !strings.Contains(text, "API \\*service\\*") || strings.Contains(text, "private-path") || strings.Contains(text, "private-query") || strings.Contains(text, "{{") {
					t.Fatal("substitution privacy or adapter pipeline failed")
				}
			}
		})
	}
}

func TestNotificationTemplateOversizeRecordsFailureWithoutProviderIO(t *testing.T) {
	var down atomic.Bool
	down.Store(true)
	var sent atomic.Int32
	n := local(t, func(o *Options) {
		o.Probe = controlled(&down)
		o.NotificationDelayMs = Ptr(int64(0))
		o.Client = doFunc(func(*http.Request) (*http.Response, error) {
			sent.Add(1)
			return notificationFixtureResponse(200, `{"id":"987654321012345678"}`), nil
		})
	})
	channel := addChannel(t, n, ChannelInput{Name: "Oversize template", Enabled: true, Config: providerDelivery.Config{Kind: "discord", Webhook: providerDelivery.WebhookConfig{URL: discordFixtureURL}, Templates: providerDelivery.Templates{Text: strings.Repeat("x", 2001)}}})
	in := input([]string{n.ID}, "https://example.test", "")
	in.NotificationIDs = []string{channel.ID}
	add(t, n, in)
	eventually(t, func() bool {
		n.mu.Lock()
		defer n.mu.Unlock()
		for _, a := range n.attempts {
			if a.ChannelID == channel.ID && a.ErrorCode == "invalid_message" {
				return len(n.receipts) == 0
			}
		}
		return false
	})
	if sent.Load() != 0 {
		t.Fatal("oversize custom text reached provider")
	}
}

func TestNotificationTemplateFactsKeepLegacySummaryAndExcludePrivateTargets(t *testing.T) {
	m := Monitor{MonitorInput: MonitorInput{Name: "API", Type: "http", Target: "https://user:password@example.test:8443/private/token?key=secret#secret"}}
	i := Incident{OpenedAt: 1700000000000, ResolvedAt: Ptr(int64(1700000432000))}
	a := attempt{ID: "attempt", MonitorID: "m", IncidentID: "i", Kind: "recovery"}
	samples := []Sample{{NodeID: "NY", State: "up", Message: "HTTP 200", LatencyMs: 26.6}, {NodeID: "LA", State: "pending", Message: "not measured", LatencyAvailable: Ptr(false)}}
	facts := notificationTemplateFacts(m, i, a, Identity{"sender", "Cloud"}, samples)
	if facts.Status != "UP" || facts.MonitorTarget != "https://example.test:8443" || facts.Downtime != "7m 12s" || facts.SenderNode != "Cloud" {
		t.Fatalf("unexpected safe facts: %+v", facts)
	}
	if facts.ObservationsSummary != "NY: up - HTTP 200\nLA: pending - not measured" || facts.NodeResults != "NY: up - HTTP 200 (27 ms)\nLA: pending - not measured" {
		t.Fatal("legacy summary changed or latency unavailable was reported")
	}
	if facts.OpenedAt != time.UnixMilli(i.OpenedAt).UTC().Format(time.RFC3339) || facts.ResolvedAt != time.UnixMilli(*i.ResolvedAt).UTC().Format(time.RFC3339) {
		t.Fatal("times are not UTC incident times")
	}
	for _, typ := range []string{"mysql", "postgres", "gamedig", "push", "manual", "redis", "mongodb"} {
		m.Type = typ
		if monitorTemplateTarget(m) != "" {
			t.Fatal("private target published", typ)
		}
	}
	for _, kind := range []string{"down", "repeat", "test"} {
		a.Kind = kind
		f := notificationTemplateFacts(m, i, a, Identity{"sender", "Cloud"}, samples)
		if f.ResolvedAt != "" || f.Downtime != "" {
			t.Fatal("unresolved/test event has recovery facts")
		}
		if kind == "test" && (f.MonitorName != "Notification test" || f.MonitorType != "" || f.NodeResults != "" || f.OpenedAt != "") {
			t.Fatal("test exposed real monitor facts")
		}
	}
	for _, tc := range []struct {
		seconds int64
		want    string
	}{{0, "0s"}, {3600, "1h"}, {4021, "1h 7m"}, {86401, "1d 1s"}, {-20, "0s"}} {
		if templateDowntime(100000, 100000+1000*tc.seconds) != tc.want {
			t.Fatal("downtime format", tc)
		}
	}
}

func TestNotificationTemplateDiscordLimitsEscapingAndCardFallbacks(t *testing.T) {
	config := providerDelivery.Config{Kind: "discord", Webhook: providerDelivery.WebhookConfig{URL: "https://discord.com/api/webhooks/123/fixture-secret"}, Templates: providerDelivery.Templates{Subject: "**{{status}}** {{monitor_name}}", Text: "{{node_results}}\n{{sender_node}}"}}
	facts := providerDelivery.Message{EventKind: "recovery", Status: "UP", MonitorName: "**name**", NodeResults: "n: up (27 ms)", SenderNode: "fixture-secret"}
	legacy := []byte(`{"content":"legacy fixed message","allowed_mentions":{"parse":[]}}`)
	stamp := "2026-10-25T08:06:05Z"
	raw, err := formatDiscordTemplates(config, facts, legacy, stamp)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Content         string
		AllowedMentions struct{ Parse []string } `json:"allowed_mentions"`
		Embeds          []struct {
			Title, Description, Timestamp string
			Color                         int
		}
	}
	if json.Unmarshal(raw, &parsed) != nil || parsed.Content != "**UP** \\*\\*name\\*\\*\nn: up \\(27 ms\\)\n\\[redacted\\]" || parsed.AllowedMentions.Parse == nil || len(parsed.AllowedMentions.Parse) != 0 {
		t.Fatal("substitution escaping, template formatting or mentions changed", string(raw))
	}
	config.Templates = providerDelivery.Templates{Text: strings.Repeat("x", 2001)}
	if _, err = formatDiscordTemplates(config, facts, legacy, stamp); err == nil {
		t.Fatal("custom plain message was shortened instead of refused")
	}
	config.Templates = providerDelivery.Templates{Text: strings.Repeat("😀", 1000)}
	if _, err = formatDiscordTemplates(config, facts, legacy, stamp); err != nil {
		t.Fatal("2000 UTF16 units refused", err)
	}
	config.Templates.Text += "x"
	if _, err = formatDiscordTemplates(config, facts, legacy, stamp); err == nil {
		t.Fatal("UTF16 bound bypassed")
	}
	config.Webhook.Embed = true
	config.Templates = providerDelivery.Templates{}
	for _, tc := range []struct {
		kind  string
		color int
	}{{"down", 0xd03b3b}, {"repeat", 0xfab219}, {"recovery", 0x0ca30c}, {"test", 0x2a78d6}} {
		facts.EventKind = tc.kind
		raw, err = formatDiscordTemplates(config, facts, legacy, stamp)
		if err != nil || json.Unmarshal(raw, &parsed) != nil || len(parsed.Embeds) != 1 || parsed.Embeds[0].Color != tc.color || parsed.Embeds[0].Description != "legacy fixed message" || parsed.Embeds[0].Timestamp != stamp || parsed.Content != "" {
			t.Fatal("built-in card fallback failed", tc, err)
		}
	}
	for _, tc := range []providerDelivery.Templates{{Subject: strings.Repeat("x", 257)}, {Text: strings.Repeat("x", 4097)}} {
		config.Templates = tc
		if _, err = formatDiscordTemplates(config, facts, legacy, stamp); err == nil {
			t.Fatal("oversize card was shortened")
		}
	}
	config.Templates = providerDelivery.Templates{Subject: "custom"}
	raw, err = formatDiscordTemplates(config, facts, legacy, stamp)
	if err != nil || json.Unmarshal(raw, &parsed) != nil || parsed.Embeds[0].Title != "custom" || parsed.Embeds[0].Description != "legacy fixed message" {
		t.Fatal("per-part fallback failed")
	}
	if _, err = formatDiscordTemplates(config, facts, legacy, "invalid"); err == nil {
		t.Fatal("invalid card timestamp accepted")
	}
}

func TestNotificationTemplateEnvelopeUsesRepeatAttemptTimeAndDoesNotReplaceLegacyBytes(t *testing.T) {
	m := Monitor{MonitorInput: MonitorInput{Name: "API", Type: "http", Target: "https://example.test/private"}}
	i := Incident{OpenedAt: 1700000000000}
	a := attempt{Kind: "repeat", LastAt: i.OpenedAt + 300000}
	legacy, err := formatDiscordNotification(m, i, a, Identity{"n", "Cloud"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := addNotificationTemplateFacts([]byte(`{"kind":"repeat"}`), m, i, a, Identity{"n", "Cloud"}, nil, legacy)
	var envelope struct {
		EventTime     string
		DiscordLegacy json.RawMessage
		TemplateFacts providerDelivery.Message
	}
	if err != nil || json.Unmarshal(wrapped, &envelope) != nil || !bytes.Equal(envelope.DiscordLegacy, legacy) || envelope.EventTime != time.UnixMilli(a.LastAt).UTC().Format(time.RFC3339) || envelope.TemplateFacts.Status != "STILL DOWN" {
		t.Fatal("legacy payload or repeat timestamp changed", err)
	}
	// Existing fixed Discord formatting remains exactly the pre-template path.
	if discordConfigured(providerDelivery.Config{Kind: "discord"}) {
		t.Fatal("blank templates opted into new rendering")
	}
}
