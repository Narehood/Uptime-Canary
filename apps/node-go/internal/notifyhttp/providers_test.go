package notifyhttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var kinds = []Kind{Slack, Telegram, Ntfy, Gotify, Pushover}

func fixtureConfig(kind Kind, base string) Config {
	c := Config{Kind: kind}
	switch kind {
	case Slack:
		c.Slack = &SlackConfig{WebhookURL: base + "/services/TTEST/BTEST/fixture_webhook_key", AllowInsecureHTTP: true}
	case Telegram:
		c.Telegram = &TelegramConfig{BotToken: "98765:fixture_bot_key", ChatID: "-1000123456789", BaseURL: base, AllowInsecureHTTP: true}
	case Ntfy:
		c.Ntfy = &NtfyConfig{Topic: "fixture-topic", Token: "fixture_ntfy_key", BaseURL: base, AllowInsecureHTTP: true}
	case Gotify:
		c.Gotify = &GotifyConfig{AppToken: "fixture_gotify_key", BaseURL: base, AllowInsecureHTTP: true}
	case Pushover:
		c.Pushover = &PushoverConfig{AppToken: strings.Repeat("a", 30), UserKey: strings.Repeat("b", 30), BaseURL: base, AllowInsecureHTTP: true}
	}
	return c
}

func goodReply(kind Kind) string {
	switch kind {
	case Slack:
		return "ok"
	case Telegram:
		return `{"ok":true,"result":{"message_id":123,"chat":{"id":-1000123456789,"type":"supergroup"}}}`
	case Ntfy:
		return `{"id":"Abcdef123456","event":"message","topic":"fixture-topic","time":1792700000}`
	case Gotify:
		return `{"id":9223372036854775807,"appid":4,"date":"2026-10-22T00:00:00Z","message":"fixture"}`
	case Pushover:
		return `{"status":1,"request":"647d2300-702c-4b38-8b2f-d56326ae460b"}`
	}
	return ""
}

func testEngine(t *testing.T, options Options) *Engine {
	t.Helper()
	e, err := New(options)
	if err != nil {
		t.Fatal("engine construction failed")
	}
	t.Cleanup(e.CloseIdleConnections)
	return e
}

func requireResult(t *testing.T, r Result, want Status) {
	t.Helper()
	if r.Status != want || r.Accepted != (want == Accepted) {
		t.Fatalf("result status %q, expected %q; code=%q", r.Status, want, r.ErrorCode)
	}
	if r.RetryAfter < 0 || r.RetryAfter > MaxRetryAfter {
		t.Fatal("retry delay is outside bounds")
	}
	if r.ProviderID != "" {
		if len(r.ProviderID) != 64 {
			t.Fatal("provider ID is not a SHA256 digest")
		}
		for _, b := range []byte(r.ProviderID) {
			if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f') {
				t.Fatal("provider ID is not hex")
			}
		}
	}
	if r.Status != Accepted && (r.ErrorCode == "" || r.ErrorMessage == "") {
		t.Fatal("failure lacks fixed error information")
	}
}

func TestNativeProviderWireContracts(t *testing.T) {
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			var calls atomic.Int32
			wire := make(chan *http.Request, 1)
			bodies := make(chan []byte, 1)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				body, _ := io.ReadAll(io.LimitReader(r.Body, MaxRequestBytes+1))
				r.Body.Close()
				wire <- r
				bodies <- body
				io.WriteString(w, goodReply(kind))
			}))
			defer s.Close()
			c := fixtureConfig(kind, s.URL+"/relay")
			if c.Ntfy != nil {
				c.Ntfy.Priority = 4
				c.Ntfy.Tags = []string{"warning", "監視"}
			}
			if c.Gotify != nil {
				c.Gotify.Priority = 7
			}
			if c.Pushover != nil {
				c.Pushover.Device = "fixture_phone"
				c.Pushover.Sound = "pushover"
				c.Pushover.Priority = 2
				c.Pushover.RetrySeconds = 30
				c.Pushover.ExpireSeconds = 60
			}
			if c.Telegram != nil {
				c.Telegram.Silent = true
				c.Telegram.ProtectContent = true
				c.Telegram.DisableLinkPreview = true
			}
			m := Message{Title: "監視 <title>", Text: "<@U123> <!here> & @everyone *plain* 😀\nhttps://127.0.0.1/", EventKind: "fixture_event_metadata", MonitorName: "fixture_monitor_metadata", MonitorID: "fixture_monitor_id", IncidentID: "fixture_incident_id", IdempotencyKey: "fixture_receipt_key"}
			r := testEngine(t, Options{}).Send(context.Background(), c, m)
			requireResult(t, r, Accepted)
			if r.HTTPStatus != 200 || calls.Load() != 1 {
				t.Fatal("send did not make one acknowledged request")
			}
			req, body := <-wire, <-bodies
			if req.Method != "POST" || req.URL.RawQuery != "" || req.Header.Get("Cookie") != "" || req.Header.Get("Proxy-Authorization") != "" || req.Header.Get("Idempotency-Key") != "" {
				t.Fatal("unexpected method, query, cookie or idempotency header")
			}
			if req.Header.Get("Accept-Encoding") != "" || !req.Close {
				t.Fatal("request permits compression or connection replay")
			}
			for _, value := range []string{m.EventKind, m.MonitorName, m.MonitorID, m.IncidentID, m.IdempotencyKey} {
				if strings.Contains(string(body), value) {
					t.Fatal("local metadata leaked to provider")
				}
			}
			if kind == Pushover {
				if req.URL.Path != "/relay/1/messages.json" || req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
					t.Fatal("incorrect Pushover endpoint or encoding")
				}
				form, err := url.ParseQuery(string(body))
				if err != nil {
					t.Fatal("invalid form")
				}
				want := url.Values{"token": {c.Pushover.AppToken}, "user": {c.Pushover.UserKey}, "title": {m.Title}, "message": {m.Text}, "priority": {"2"}, "device": {"fixture_phone"}, "sound": {"pushover"}, "retry": {"30"}, "expire": {"60"}}
				if !reflect.DeepEqual(form, want) || req.Header.Get("Authorization") != "" {
					t.Fatal("incorrect Pushover wire fields")
				}
				return
			}
			if req.Header.Get("Content-Type") != "application/json" {
				t.Fatal("incorrect JSON media type")
			}
			object, err := strictObject(body)
			if err != nil {
				t.Fatal("invalid request JSON")
			}
			switch kind {
			case Slack:
				if req.URL.Path != "/relay/services/TTEST/BTEST/fixture_webhook_key" || stringField(object, "text") != escapeSlack(combinedText(m)) || stringField(object, "parse") != "none" {
					t.Fatal("incorrect Slack path, text or parsing")
				}
				for _, key := range []string{"mrkdwn", "unfurl_links", "unfurl_media"} {
					if v, ok := object[key].(bool); !ok || v {
						t.Fatal("Slack formatting/unfurl option enabled")
					}
				}
				if n, ok := integerField(object, "link_names"); !ok || n != 0 || len(object) != 6 || req.Header.Get("Authorization") != "" {
					t.Fatal("Slack mention options or auth are incorrect")
				}
				if r.ProviderID != "" {
					t.Fatal("Slack invented a message receipt")
				}
			case Telegram:
				if req.URL.Path != "/relay/bot98765:fixture_bot_key/sendMessage" || !strings.Contains(req.RequestURI, "bot98765%3A") || stringField(object, "text") != combinedText(m) {
					t.Fatal("incorrect Telegram route or literal text")
				}
				if id, ok := integerField(object, "chat_id"); !ok || id != -1000123456789 {
					t.Fatal("Telegram numeric recipient lost precision")
				}
				if hasAny(object, "parse_mode", "entities", "allow_paid_broadcast", "token", "url", "method") || req.Header.Get("Authorization") != "" {
					t.Fatal("Telegram has unsupported wire options")
				}
				for _, key := range []string{"disable_notification", "protect_content"} {
					if b, ok := object[key].(bool); !ok || !b {
						t.Fatal("Telegram optional flag missing")
					}
				}
				if disabled, ok := objectField(object, "link_preview_options")["is_disabled"].(bool); !ok || !disabled {
					t.Fatal("Telegram preview option missing")
				}
			case Ntfy:
				if req.URL.Path != "/relay/" || stringField(object, "topic") != c.Ntfy.Topic || stringField(object, "message") != m.Text || stringField(object, "title") != m.Title || req.Header.Get("Authorization") != "Bearer "+c.Ntfy.Token {
					t.Fatal("incorrect ntfy publication fields or authentication")
				}
				if len(object) != 5 || hasAny(object, "actions", "attach", "filename", "sequence_id") {
					t.Fatal("ntfy invented publication options")
				}
			case Gotify:
				if req.URL.Path != "/relay/message" || req.Header.Get("X-Gotify-Key") != c.Gotify.AppToken || req.Header.Get("Authorization") != "" || stringField(object, "message") != m.Text || stringField(object, "title") != m.Title || len(object) != 3 {
					t.Fatal("incorrect Gotify wire fields or authentication")
				}
				if priority, ok := integerField(object, "priority"); !ok || priority != 7 {
					t.Fatal("Gotify priority was not an integer")
				}
			}
			if kind != Slack && r.ProviderID == "" {
				t.Fatal("accepted provider receipt was not hashed")
			}
		})
	}
}

func TestNtfyBasicAndTelegramThreadAlias(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		valid := make(chan bool, 1)
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, p, ok := r.BasicAuth()
			valid <- ok && u == "fixture_user" && p == "fixture_password" && r.URL.RawQuery == ""
			io.WriteString(w, goodReply(Ntfy))
		}))
		defer s.Close()
		c := fixtureConfig(Ntfy, s.URL)
		c.Ntfy.Token = ""
		c.Ntfy.Username = "fixture_user"
		c.Ntfy.Password = "fixture_password"
		requireResult(t, Send(context.Background(), c, Message{Text: "fixture"}), Accepted)
		if !<-valid {
			t.Fatal("Basic credentials were not isolated to auth header")
		}
	})
	for _, chat := range []string{"-1000123456789", "@FixtureChannel"} {
		t.Run(strconv.FormatBool(strings.HasPrefix(chat, "@")), func(t *testing.T) {
			valid := make(chan bool, 1)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				object, _ := strictObject(body)
				thread, ok := integerField(object, "message_thread_id")
				valid <- ok && thread == 17
				io.WriteString(w, `{"ok":true,"result":{"message_id":42,"message_thread_id":17,"chat":{"id":-1000123456789,"username":"fixturechannel"}}}`)
			}))
			defer s.Close()
			c := fixtureConfig(Telegram, s.URL)
			c.Telegram.ChatID = chat
			c.Telegram.MessageThreadID = 17
			requireResult(t, Send(context.Background(), c, Message{Text: "fixture"}), Accepted)
			if !<-valid {
				t.Fatal("thread not sent as an integer")
			}
		})
	}
}

func TestAcknowledgementCannotFalseAccept(t *testing.T) {
	bad := map[Kind][]string{
		Slack: {"", "ok\n", "OK", " ok", `{"ok":true}`, "invalid_payload"},
		Telegram: {
			`{"ok":false,"error_code":400,"description":"fixture_provider_body"}`,
			`{"ok":true,"result":{"message_id":123,"chat":{"id":-999}}}`,
			`{"ok":true,"result":{"message_id":0,"chat":{"id":-1000123456789}}}`,
			`{"ok":true,"result":{"message_id":1.0,"chat":{"id":-1000123456789}}}`,
			`{"ok":true,"result":{"message_id":1,"chat":{"id":"-1000123456789"}}}`,
			`{"ok":true,"result":{"message_id":1,"chat":{"id":-1000123456789}},"error_code":400}`,
			`{"ok":true,"OK":false,"result":{"message_id":1,"chat":{"id":-1000123456789}}}`,
			`{"ok":true,"result":{"message_id":1,"message_id":2,"chat":{"id":-1000123456789}}}`,
			`{"ok":true,"result":{"message_id":1,"chat":{"id":-1000123456789}},"status":"unrecognized"}`,
			`{"Ok":true,"result":{"message_id":1,"chat":{"id":-1000123456789}}}`,
			`{"ok":true,"result":{"Message_id":1,"chat":{"id":-1000123456789}}}`,
			`{"ok":true,"result":{"message_id":9223372036854775808,"chat":{"id":-1000123456789}}}`,
			`{"ok":true,"result":true}`, `{"ok":"true"}`, `null`,
		},
		Ntfy: {
			`{"id":"Abcdef123456","event":"keepalive","topic":"fixture-topic"}`,
			`{"id":"Abcdef123456","event":"message","topic":"different-topic"}`,
			`{"id":"Abcdef123456","event":"message","topic":"fixture-topic","code":40001,"error":"fixture_provider_body"}`,
			`{"id":"Abcdef123456","ID":"Xbcdef123456","event":"message","topic":"fixture-topic"}`,
			`{"id":"short","event":"message","topic":"fixture-topic"}`,
			`{"id":"Abcdef123456","Event":"message","topic":"fixture-topic"}`,
			`{"id":"Abcdef123456","event":"message","topic":"fixture-topic","status":"unrecognized"}`,
		},
		Gotify:   {`{"id":1,"error":"fixture_provider_body","errorCode":400}`, `{"id":0}`, `{"id":-1}`, `{"id":"1"}`, `{"id":1e3}`, `{"id":1,"id":2}`, `{"ID":1}`, `{"error":"fixture_provider_body"}`, `{"id":1,"status":"unrecognized"}`},
		Pushover: {`{"status":0,"errors":["fixture_provider_body"],"request":"647d2300-702c-4b38-8b2f-d56326ae460b"}`, `{"status":2,"request":"647d2300-702c-4b38-8b2f-d56326ae460b"}`, `{"status":"1","request":"647d2300-702c-4b38-8b2f-d56326ae460b"}`, `{"status":1,"request":"invalid"}`, `{"status":1,"errors":["fixture_provider_body"],"request":"647d2300-702c-4b38-8b2f-d56326ae460b"}`, `{"status":1,"Status":0,"request":"647d2300-702c-4b38-8b2f-d56326ae460b"}`, `{"Status":1,"request":"647d2300-702c-4b38-8b2f-d56326ae460b"}`},
	}
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			cases := append(bad[kind], goodReply(kind)+"x", goodReply(kind)+"\n"+goodReply(kind), goodReply(kind)[:len(goodReply(kind))-1], strings.Repeat("x", MaxResponseBytes+1), "[{}]")
			for index, body := range cases {
				t.Run(strconv.Itoa(index), func(t *testing.T) {
					s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
					defer s.Close()
					r := Send(context.Background(), fixtureConfig(kind, s.URL), Message{Text: "fixture_request_text"})
					if r.Accepted || r.Status == Accepted {
						t.Fatal("invalid acknowledgement was accepted")
					}
					encoded, _ := json.Marshal(r)
					for _, sensitive := range []string{"fixture_provider_body", "fixture_request_text", "fixture_webhook_key", "fixture_bot_key", "fixture_ntfy_key", "fixture_gotify_key", s.URL} {
						if strings.Contains(string(encoded), sensitive) {
							t.Fatal("result contains private input")
						}
					}
				})
			}
			for _, status := range []int{201, 202, 204, 206, 299} {
				t.Run("unexpected-status-"+strconv.Itoa(status), func(t *testing.T) {
					s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(status)
						io.WriteString(w, goodReply(kind))
					}))
					defer s.Close()
					requireResult(t, Send(context.Background(), fixtureConfig(kind, s.URL), Message{Text: "fixture"}), Unknown)
				})
			}
		})
	}
}

func TestProviderRecipientAcknowledgement(t *testing.T) {
	for _, test := range []struct {
		name, chat, body string
		thread           int64
	}{
		{"alias-missing-username", "@FixtureChannel", goodReply(Telegram), 0},
		{"alias-mismatch", "@FixtureChannel", `{"ok":true,"result":{"message_id":1,"chat":{"id":-1000123456789,"username":"different"}}}`, 0},
		{"thread-missing", "-1000123456789", goodReply(Telegram), 17},
		{"thread-mismatch", "-1000123456789", `{"ok":true,"result":{"message_id":1,"message_thread_id":18,"chat":{"id":-1000123456789}}}`, 17},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, test.body) }))
			defer s.Close()
			c := fixtureConfig(Telegram, s.URL)
			c.Telegram.ChatID = test.chat
			c.Telegram.MessageThreadID = test.thread
			requireResult(t, Send(context.Background(), c, Message{Text: "fixture"}), Unknown)
		})
	}
}

func TestHTTPClassificationAndRetryHints(t *testing.T) {
	for _, kind := range kinds {
		for _, test := range []struct {
			status int
			want   Status
		}{{400, Rejected}, {401, Rejected}, {403, Rejected}, {404, Rejected}, {408, Rejected}, {429, Retryable}, {500, Retryable}, {503, Retryable}, {599, Retryable}} {
			t.Run(string(kind)+"-"+strconv.Itoa(test.status), func(t *testing.T) {
				var calls atomic.Int32
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.Header().Set("Retry-After", "7")
					w.WriteHeader(test.status)
					io.WriteString(w, goodReply(kind))
				}))
				defer s.Close()
				r := Send(context.Background(), fixtureConfig(kind, s.URL), Message{Text: "fixture"})
				requireResult(t, r, test.want)
				if r.HTTPStatus != test.status || r.RetryAfter != 7*time.Second || calls.Load() != 1 {
					t.Fatal("HTTP classification, retry hint or attempt count incorrect")
				}
			})
		}
	}
	for _, test := range []struct {
		name, header, body string
		want               time.Duration
	}{
		{"seconds", "9", `{"ok":false,"error_code":429,"parameters":{"retry_after":12}}`, 12 * time.Second},
		{"cap", "999999999", `{"ok":false,"error_code":429}`, MaxRetryAfter},
		{"overflow-cap", "999999999999999999999999999999999999999999", `{"ok":false,"error_code":429}`, MaxRetryAfter},
		{"hint-cap", "0", `{"ok":false,"error_code":429,"parameters":{"retry_after":9223372036854775807}}`, MaxRetryAfter},
		{"negative", "-10", `{"ok":false,"error_code":429,"parameters":{"retry_after":-4}}`, 0},
		{"fraction", "1.5", `{"ok":false,"error_code":429,"parameters":{"retry_after":2.5}}`, 0},
		{"string-hint", "invalid", `{"ok":false,"error_code":429,"parameters":{"retry_after":"20"}}`, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", test.header)
				w.WriteHeader(429)
				io.WriteString(w, test.body)
			}))
			defer s.Close()
			r := Send(context.Background(), fixtureConfig(Telegram, s.URL), Message{Text: "fixture"})
			requireResult(t, r, Retryable)
			if r.RetryAfter != test.want {
				t.Fatal("retry delay not parsed/bounded correctly")
			}
		})
	}
	for _, test := range []struct {
		code int
		want Status
	}{{400, Rejected}, {429, Retryable}, {500, Retryable}} {
		t.Run("telegram-error-http200-"+strconv.Itoa(test.code), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, `{"ok":false,"error_code":`+strconv.Itoa(test.code)+`,"parameters":{"retry_after":13}}`)
			}))
			defer s.Close()
			r := Send(context.Background(), fixtureConfig(Telegram, s.URL), Message{Text: "fixture"})
			requireResult(t, r, test.want)
			if r.RetryAfter != 13*time.Second || r.HTTPStatus != 200 {
				t.Fatal("Telegram HTTP200 error lost its rate hint or status")
			}
		})
	}
	t.Run("http-date", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", time.Now().Add(20*time.Second).UTC().Format(http.TimeFormat))
			w.WriteHeader(429)
		}))
		defer s.Close()
		r := Send(context.Background(), fixtureConfig(Ntfy, s.URL), Message{Text: "fixture"})
		requireResult(t, r, Retryable)
		if r.RetryAfter < 18*time.Second || r.RetryAfter > 20*time.Second {
			t.Fatal("HTTP date retry hint not honored")
		}
	})
	t.Run("pushover-unix-reset", func(t *testing.T) {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Limit-App-Remaining", "0")
			w.Header().Set("X-Limit-App-Reset", strconv.FormatInt(time.Now().Add(40*time.Second).Unix(), 10))
			w.WriteHeader(429)
		}))
		defer s.Close()
		r := Send(context.Background(), fixtureConfig(Pushover, s.URL), Message{Text: "fixture"})
		requireResult(t, r, Retryable)
		if r.RetryAfter < 38*time.Second || r.RetryAfter > 40*time.Second {
			t.Fatal("UNIX quota reset hint not honored")
		}
	})
}
