package delivery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"uptime-canary/apps/node-go/internal/notifyhttp"
)

var messagingKinds = []Kind{Slack, Telegram, Ntfy, Gotify, Pushover}

func messagingFixture(kind Kind, base string) Config {
	c := Config{Kind: kind}
	switch kind {
	case Slack:
		c.Slack = &notifyhttp.SlackConfig{WebhookURL: base + "/services/TTEST/BTEST/fixture_key", AllowInsecureHTTP: true}
	case Telegram:
		c.Telegram = &notifyhttp.TelegramConfig{BotToken: "12345:fixture_bot", ChatID: "12345", BaseURL: base, AllowInsecureHTTP: true}
	case Ntfy:
		c.Ntfy = &notifyhttp.NtfyConfig{Topic: "canary", BaseURL: base, Token: "fixture_token", AllowInsecureHTTP: true}
	case Gotify:
		c.Gotify = &notifyhttp.GotifyConfig{AppToken: "fixture_app", BaseURL: base, AllowInsecureHTTP: true}
	case Pushover:
		c.Pushover = &notifyhttp.PushoverConfig{AppToken: strings.Repeat("a", 30), UserKey: strings.Repeat("b", 30), BaseURL: base, AllowInsecureHTTP: true}
	}
	return c
}

func messagingReply(kind Kind) string {
	switch kind {
	case Slack:
		return "ok"
	case Telegram:
		return `{"ok":true,"result":{"message_id":1,"chat":{"id":12345}}}`
	case Ntfy:
		return `{"id":"Abcdef123456","event":"message","topic":"canary"}`
	case Gotify:
		return `{"id":1,"appid":2,"message":"fixture","date":"2026-10-22T00:00:00Z"}`
	case Pushover:
		return `{"status":1,"request":"647d2300-702c-4b38-8b2f-d56326ae460b"}`
	}
	return ""
}

func messagingBody(t *testing.T, kind Kind, r *http.Request) string {
	t.Helper()
	if r.Header.Get("Idempotency-Key") != "" {
		t.Error("local attempt identity became an unsupported provider header")
	}
	if kind == Pushover {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		return r.Form.Get("message")
	}
	var object map[string]json.RawMessage
	if json.NewDecoder(r.Body).Decode(&object) != nil {
		t.Error("invalid message body")
	}
	field := "message"
	if kind == Slack || kind == Telegram {
		field = "text"
	}
	var text string
	if json.Unmarshal(object[field], &text) != nil {
		t.Error("missing rendered provider text")
	}
	return text
}

func TestMessagingDeliveryRendersTemplatesAndNativeAcknowledgements(t *testing.T) {
	for _, kind := range messagingKinds {
		t.Run(string(kind), func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if body := messagingBody(t, kind, r); !strings.Contains(body, "Observed: HTTP 503") || strings.Contains(body, "fixture-local-attempt") {
					t.Error("rendered text or private metadata boundary changed")
				}
				io.WriteString(w, messagingReply(kind))
			}))
			defer s.Close()
			c := messagingFixture(kind, s.URL)
			c.Templates = Templates{Subject: "{{event_kind}} {{monitor_name}}", Text: "Observed: {{observations_summary}}"}
			b, _ := json.Marshal(c)
			var restored Config
			if json.Unmarshal(b, &restored) != nil || Validate(restored) != nil {
				t.Fatal("typed channel no longer round trips through durable JSON")
			}
			r := New(Options{}).Send(context.Background(), restored, Message{EventKind: "down", MonitorName: "Canary",
				ObservationsSummary: "HTTP 503", MonitorID: "m-1", IncidentID: "i-1", IdempotencyKey: "fixture-local-attempt"})
			if !r.Accepted || r.Kind != kind || r.Status != Accepted || r.HTTPStatus != 200 || calls.Load() != 1 || r.AcceptedRecipients != 0 || len(r.Recipients) != 0 {
				t.Fatalf("native result mapping failed: %+v", r)
			}
		})
	}
}

func TestMessagingDefaultExcerptsAndExactCustomText(t *testing.T) {
	for _, kind := range messagingKinds {
		t.Run(string(kind), func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				text := messagingBody(t, kind, r)
				if kind != Gotify && !strings.Contains(text, "[truncated; see monitor details]") {
					t.Error("generated excerpt did not announce omitted detail")
				}
				io.WriteString(w, messagingReply(kind))
			}))
			defer s.Close()
			c := messagingFixture(kind, s.URL)
			message := Message{EventKind: "down", MonitorName: "Canary", ObservationsSummary: strings.Repeat("&🚧<>", 2000)}
			r := New(Options{}).Send(context.Background(), c, message)
			if !r.Accepted || calls.Load() != 1 {
				t.Fatalf("generated Unicode/escaped message exceeded provider limits: %+v", r)
			}
			if kind == Gotify {
				return // Its 16 KiB body fits the entire original observation.
			}
			c.Templates.Text = message.ObservationsSummary
			r = New(Options{}).Send(context.Background(), c, message)
			if r.Status != Rejected || r.ErrorCode != "invalid_message" || calls.Load() != 1 {
				t.Fatal("custom text was silently shortened or sent above its provider bound")
			}
		})
	}
}

func TestMessagingConfigurationRemainsStrictAcrossDeliveryEnvelope(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"slack","slack":{"webhookURL":"http://127.0.0.1/hook","webhookURL":"http://127.0.0.1/other","allowInsecureHTTP":true}}`,
		`{"kind":"slack","Slack":{"webhookURL":"https://127.0.0.1/hook"}}`,
		`{"kind":"slack","slack":{"WebhookURL":"https://127.0.0.1/hook"}}`,
		`{"kind":"slack","slack":null}`,
		`{"kind":"slack","slack":{"webhookURL":"https://127.0.0.1/hook","future":true}}`,
		`{"kind":"slack","slack":{"webhookURL":"https://127.0.0.1/hook"},"templates":{"text":null}}`,
		`{"kind":"slack","kind":"webhook","slack":{"webhookURL":"https://127.0.0.1/hook"}}`,
	} {
		var c Config
		if json.Unmarshal([]byte(raw), &c) == nil {
			t.Fatal("delivery envelope erased an ambiguous native option")
		}
	}
	c := messagingFixture(Slack, "https://127.0.0.1")
	c.Sender.Email = "unused@example.test"
	if Validate(c) == nil {
		t.Fatal("inactive email credentials were accepted for messaging")
	}
	c = fixtureConfig(Mailgun, "https://127.0.0.1")
	c.Slack = &notifyhttp.SlackConfig{WebhookURL: "https://127.0.0.1/hook"}
	if Validate(c) == nil {
		t.Fatal("native provider pointer was ignored by an email profile")
	}
	var viewer Config
	if json.Unmarshal([]byte(`{"kind":"slack"}`), &viewer) != nil || Validate(viewer) == nil {
		t.Fatal("redacted projection either failed to decode or became executable")
	}
}

func TestMessagingRetryHintsAndInvalidHTMLStayBounded(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(429)
	}))
	defer s.Close()
	c := messagingFixture(Ntfy, s.URL)
	r := New(Options{}).Send(context.Background(), c, Message{Text: "fixture"})
	if r.Kind != Ntfy || r.Status != Retryable || r.RetryAfter != 2*time.Second || r.HTTPStatus != 429 || calls.Load() != 1 {
		t.Fatalf("retry evidence was lost: %+v", r)
	}
	c.Templates.HTML = "<p>unsupported</p>"
	if Validate(c) == nil {
		t.Fatal("HTML template was silently ignored")
	}
	c.Templates.HTML = ""
	r = New(Options{}).Send(context.Background(), c, Message{Text: "fixture", HTML: "<p>unsupported</p>"})
	if r.Status != Rejected || calls.Load() != 1 {
		t.Fatal("direct HTML was silently ignored or contacted a provider")
	}
}
