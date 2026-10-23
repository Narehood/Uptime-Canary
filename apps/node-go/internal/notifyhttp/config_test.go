package notifyhttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func cloneConfig(c Config) Config {
	// Fixtures only: avoid pointer aliasing while varying identity fields.
	data, _ := json.Marshal(c)
	var next Config
	if json.Unmarshal(data, &next) != nil {
		panic("invalid test fixture configuration")
	}
	return next
}

func TestPureValidationAndExactActivePointer(t *testing.T) {
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			base := fixtureConfig(kind, "https://127.0.0.1:44444/relay")
			if Validate(base) != nil {
				t.Fatal("valid native configuration rejected")
			}
			for _, foreign := range kinds {
				if foreign == kind {
					continue
				}
				c := cloneConfig(base)
				switch foreign {
				case Slack:
					c.Slack = &SlackConfig{}
				case Telegram:
					c.Telegram = &TelegramConfig{}
				case Ntfy:
					c.Ntfy = &NtfyConfig{}
				case Gotify:
					c.Gotify = &GotifyConfig{}
				case Pushover:
					c.Pushover = &PushoverConfig{}
				}
				if Validate(c) == nil {
					t.Fatal("foreign active pointer accepted")
				}
			}
			c := cloneConfig(base)
			c.Kind = "unsupported_kind_with_private_input"
			if Validate(c) == nil {
				t.Fatal("unknown kind accepted")
			}
			r := Send(context.Background(), c, Message{Text: "fixture"})
			requireResult(t, r, Rejected)
			if r.Kind != "" || r.ErrorCode != "invalid_config" {
				t.Fatal("invalid kind leaked through result")
			}
		})
	}
	for _, c := range []Config{{}, {Kind: Slack}, {Kind: Telegram, Slack: &SlackConfig{}}} {
		if Validate(c) == nil {
			t.Fatal("missing or mismatched provider accepted")
		}
	}
}

func TestURLPolicyNoNetwork(t *testing.T) {
	for _, raw := range []string{
		"", "http://127.0.0.1/message", "ftp://127.0.0.1/message", "//127.0.0.1/message",
		"https://fixture_user:fixture_password@127.0.0.1/message", "https://127.0.0.1/message?token=fixture_key", "https://127.0.0.1/message?", "https://127.0.0.1/message#fragment",
		"https://127.0.0.1:0/message", "https://127.0.0.1:65536/message", "https://127.0.0.1:/message", "https://127.0.0.1:bad/message",
		"https://127.0.0.1/a/../message", "https://127.0.0.1/a/%2e%2e/message", "https://127.0.0.1/a%2fb/message", "https://127.0.0.1/a%5cb/message", "https://127.0.0.1/a%252fb/message", "https://127.0.0.1/a//message",
		"https://127.0.0.1/a\\message", "https://127.0.0.1/a%0dmessage", "https://127.0.0.1/a\nmessage", "https://[::1%25fixture]/message", "https://-invalid.test/message", "https://invalid_.test/message", "https://127.0.0.1/" + strings.Repeat("x", MaxURLBytes),
	} {
		c := fixtureConfig(Gotify, "https://127.0.0.1")
		c.Gotify.BaseURL = raw
		c.Gotify.AllowInsecureHTTP = false
		if Validate(c) == nil {
			t.Fatal("unsafe or malformed endpoint accepted")
		}
	}
	for _, raw := range []string{"http://127.0.0.1:34567/relay", "http://192.168.1.2/relay", "http://notify.internal/relay", "http://[::1]:34567/relay"} {
		// Private/LAN/DNS examples are pure validation only, never send targets.
		c := fixtureConfig(Gotify, raw)
		if Validate(c) != nil {
			t.Fatal("explicit configured HTTP endpoint rejected")
		}
		c.Gotify.AllowInsecureHTTP = false
		if Validate(c) == nil {
			t.Fatal("HTTP allowed without explicit opt-in")
		}
	}
	for _, kind := range []Kind{Telegram, Ntfy, Pushover} {
		c := fixtureConfig(kind, "")
		if Validate(c) != nil {
			t.Fatal("official default endpoint rejected")
		}
		// Pure route construction verifies defaults without provider requests.
		req, err := request(context.Background(), c, Message{Text: "fixture"})
		if err != nil || req.URL.Scheme != "https" {
			t.Fatal("default endpoint is not HTTPS")
		}
		req.Body.Close()
	}
}

func TestProviderFieldBounds(t *testing.T) {
	cases := []struct {
		name   string
		kind   Kind
		change func(*Config)
	}{
		{"token-path-injection", Telegram, func(c *Config) { c.Telegram.BotToken = "123:fixture/other" }},
		{"token-query-injection", Telegram, func(c *Config) { c.Telegram.BotToken = "123:fixture?other" }},
		{"token-max", Telegram, func(c *Config) { c.Telegram.BotToken = strings.Repeat("x", 257) }},
		{"chat-zero", Telegram, func(c *Config) { c.Telegram.ChatID = "0" }},
		{"chat-overflow", Telegram, func(c *Config) { c.Telegram.ChatID = "9007199254740992" }},
		{"chat-not-canonical", Telegram, func(c *Config) { c.Telegram.ChatID = "+123" }},
		{"chat-leading-zero", Telegram, func(c *Config) { c.Telegram.ChatID = "0123" }},
		{"chat-alias-route", Telegram, func(c *Config) { c.Telegram.ChatID = "@fixture/path" }},
		{"chat-command", Telegram, func(c *Config) { c.Telegram.ChatID = "sendMessage" }},
		{"negative-thread", Telegram, func(c *Config) { c.Telegram.MessageThreadID = -1 }},
		{"large-thread", Telegram, func(c *Config) { c.Telegram.MessageThreadID = maxChatID + 1 }},
		{"topic-path", Ntfy, func(c *Config) { c.Ntfy.Topic = "fixture/other" }},
		{"topic-bound", Ntfy, func(c *Config) { c.Ntfy.Topic = strings.Repeat("a", 65) }},
		{"topic-empty", Ntfy, func(c *Config) { c.Ntfy.Topic = "" }},
		{"dual-auth", Ntfy, func(c *Config) { c.Ntfy.Username = "fixture"; c.Ntfy.Password = "fixture" }},
		{"partial-basic", Ntfy, func(c *Config) { c.Ntfy.Token = ""; c.Ntfy.Password = "fixture" }},
		{"basic-colon", Ntfy, func(c *Config) { c.Ntfy.Token = ""; c.Ntfy.Username = "fixture:user"; c.Ntfy.Password = "fixture" }},
		{"token-header-injection", Ntfy, func(c *Config) { c.Ntfy.Token = "fixture\r\nOther: value" }},
		{"tags-count", Ntfy, func(c *Config) { c.Ntfy.Tags = make([]string, 17) }},
		{"tags-bound", Ntfy, func(c *Config) { c.Ntfy.Tags = []string{strings.Repeat("a", 65)} }},
		{"tag-separator", Ntfy, func(c *Config) { c.Ntfy.Tags = []string{"one,two"} }},
		{"priority-ntfy", Ntfy, func(c *Config) { c.Ntfy.Priority = 6 }},
		{"priority-gotify", Gotify, func(c *Config) { c.Gotify.Priority = 11 }},
		{"token-gotify-empty", Gotify, func(c *Config) { c.Gotify.AppToken = "" }},
		{"token-gotify-large", Gotify, func(c *Config) { c.Gotify.AppToken = strings.Repeat("a", 513) }},
		{"token-gotify-control", Gotify, func(c *Config) { c.Gotify.AppToken = "fixture\n" }},
		{"pushover-app-token", Pushover, func(c *Config) { c.Pushover.AppToken = "fixture" }},
		{"pushover-user-key", Pushover, func(c *Config) { c.Pushover.UserKey = strings.Repeat("b", 31) }},
		{"pushover-multiple-users", Pushover, func(c *Config) { c.Pushover.UserKey = strings.Repeat("b", 30) + "," + strings.Repeat("c", 30) }},
		{"pushover-device", Pushover, func(c *Config) { c.Pushover.Device = strings.Repeat("x", 26) }},
		{"pushover-multiple-devices", Pushover, func(c *Config) { c.Pushover.Device = "one,two" }},
		{"pushover-sound", Pushover, func(c *Config) { c.Pushover.Sound = "fixture/sound" }},
		{"pushover-priority", Pushover, func(c *Config) { c.Pushover.Priority = 3 }},
		{"pushover-emergency-missing", Pushover, func(c *Config) { c.Pushover.Priority = 2 }},
		{"pushover-emergency-fast", Pushover, func(c *Config) { c.Pushover.Priority = 2; c.Pushover.RetrySeconds = 29; c.Pushover.ExpireSeconds = 60 }},
		{"pushover-emergency-expiry", Pushover, func(c *Config) {
			c.Pushover.Priority = 2
			c.Pushover.RetrySeconds = 30
			c.Pushover.ExpireSeconds = 10801
		}},
		{"pushover-emergency-ineffective", Pushover, func(c *Config) { c.Pushover.Priority = 2; c.Pushover.RetrySeconds = 60; c.Pushover.ExpireSeconds = 30 }},
		{"pushover-inactive-retry", Pushover, func(c *Config) { c.Pushover.RetrySeconds = 30 }},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			c := fixtureConfig(test.kind, "https://127.0.0.1/relay")
			test.change(&c)
			if Validate(c) == nil {
				t.Fatal("invalid field accepted")
			}
		})
	}
}

func TestConfigJSONStrictAndSanitized(t *testing.T) {
	valid := fixtureConfig(Telegram, "https://127.0.0.1/relay")
	data, _ := json.Marshal(valid)
	for _, input := range []string{
		`{"kind":"telegram","telegram":{"botToken":"98765:fixture_bot_key","chatId":"-1000123456789","parse_mode":"HTML"}}`,
		`{"kind":"telegram","telegram":{"botToken":"98765:fixture_bot_key","chatId":"-1000123456789","silent":true,"Silent":false}}`,
		`{"kind":"telegram","telegram":{"botToken":"98765:fixture_bot_key","chatId":"-1000123456789","chatId":"other"}}`,
		`{"kind":"telegram","Kind":"gotify","telegram":{"botToken":"98765:fixture_bot_key","chatId":"-1000123456789"}}`,
		`{"kind":"telegram","Telegram":{"botToken":"98765:fixture_bot_key","chatId":"-1000123456789"}}`,
		`{"kind":"telegram","telegram":{"BotToken":"98765:fixture_bot_key","chatId":"-1000123456789"}}`,
		`{"kind":"telegram","telegram":{"botToken":"98765:fixture_bot_key","chatId":"-1000123456789"},"ntfy":{}}`,
		`{"kind":"telegram","telegram":{"botToken":"98765:fixture_bot_key","chatId":"-1000123456789"},"ntfy":null}`,
		`{"kind":"telegram","telegram":null}`, `{"kind":"slack","slack":true}`, `{"kind":"slack","unknown_private_input":true}`,
		`{"kind":"telegram","telegram":{"botToken":"98765:fixture_bot_key","chatId":"-1000123456789","messageThreadId":1.5}}`,
		string(data) + " trailing_private_input", strings.Repeat("x", MaxConfigBytes+1), "null",
	} {
		var c Config
		if err := c.UnmarshalJSON([]byte(input)); err == nil || err.Error() != errConfig.Error() {
			t.Fatal("invalid JSON configuration did not return a fixed error")
		}
	}
	var c Config
	if json.Unmarshal(data, &c) != nil || Validate(c) != nil {
		t.Fatal("valid JSON configuration rejected")
	}
}

func TestIdentityCredentialRotationAndRouteChanges(t *testing.T) {
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			base := fixtureConfig(kind, "https://127.0.0.1/relay")
			if kind == Slack {
				base.Slack.WebhookURL = "https://127.0.0.1/services/TTEST/BTEST/fixture_webhook_key"
			}
			before, err := Identity(base)
			if err != nil || len(before) != 64 {
				t.Fatal("invalid identity")
			}
			rotated := cloneConfig(base)
			switch kind {
			case Slack:
				rotated.Slack.WebhookURL = "https://127.0.0.1/services/TTEST/BTEST/fixture_rotated_key"
			case Telegram:
				rotated.Telegram.BotToken = "54321:fixture_rotated_key"
				rotated.Telegram.Silent = true
				rotated.Telegram.ProtectContent = true
				rotated.Telegram.DisableLinkPreview = true
			case Ntfy:
				rotated.Ntfy.Token = "fixture_rotated_key"
				rotated.Ntfy.Priority = 5
				rotated.Ntfy.Tags = []string{"fixture_tag"}
			case Gotify:
				rotated.Gotify.AppToken = "fixture_rotated_key"
				rotated.Gotify.Priority = 10
			case Pushover:
				rotated.Pushover.AppToken = strings.Repeat("c", 30)
				rotated.Pushover.Sound = "siren"
				rotated.Pushover.Priority = 1
			}
			after, err := Identity(rotated)
			if err != nil || before != after {
				t.Fatal("credential or presentation change changed destination identity")
			}
			route := cloneConfig(base)
			switch kind {
			case Slack:
				route.Slack.WebhookURL = "https://127.0.0.1/services/TTEST/BOTHER/fixture_webhook_key"
			case Telegram:
				route.Telegram.ChatID = "-1000123456790"
			case Ntfy:
				route.Ntfy.Topic = "another-topic"
			case Gotify:
				route.Gotify.BaseURL = "https://127.0.0.1/another-relay"
			case Pushover:
				route.Pushover.UserKey = strings.Repeat("d", 30)
			}
			after, err = Identity(route)
			if err != nil || before == after {
				t.Fatal("destination change failed to change identity")
			}
			endpointChange := cloneConfig(base)
			switch kind {
			case Slack:
				endpointChange.Slack.WebhookURL = strings.Replace(endpointChange.Slack.WebhookURL, "127.0.0.1", "127.0.0.2", 1)
			case Telegram:
				endpointChange.Telegram.BaseURL = "https://127.0.0.2/relay"
			case Ntfy:
				endpointChange.Ntfy.BaseURL = "https://127.0.0.2/relay"
			case Gotify:
				endpointChange.Gotify.BaseURL = "https://127.0.0.2/relay"
			case Pushover:
				endpointChange.Pushover.BaseURL = "https://127.0.0.2/relay"
			}
			after, err = Identity(endpointChange)
			if err != nil || before == after {
				t.Fatal("endpoint change failed to change identity")
			}
		})
	}
	for _, test := range []struct {
		kind   Kind
		change func(*Config)
	}{
		{Telegram, func(c *Config) { c.Telegram.MessageThreadID = 17 }},
		{Pushover, func(c *Config) { c.Pushover.Device = "fixture_phone" }},
	} {
		c := fixtureConfig(test.kind, "https://127.0.0.1/relay")
		before, _ := Identity(c)
		test.change(&c)
		after, _ := Identity(c)
		if before == after {
			t.Fatal("thread/device routing did not change identity")
		}
	}
	custom := fixtureConfig(Slack, "https://127.0.0.1/custom")
	before, _ := Identity(custom)
	custom.Slack.WebhookURL = "https://127.0.0.1/custom/different-route"
	after, _ := Identity(custom)
	if before == after {
		t.Fatal("custom Slack route blindly stripped final segment")
	}
	for _, kind := range kinds {
		c := fixtureConfig(kind, "https://127.0.0.1/relay")
		a, _ := Identity(c)
		switch kind {
		case Slack:
			c.Slack.WebhookURL = strings.Replace(c.Slack.WebhookURL, "https://127.0.0.1/", "HTTPS://127.0.0.1:443/", 1)
		case Telegram:
			c.Telegram.BaseURL = "HTTPS://127.0.0.1:443/relay/"
		case Ntfy:
			c.Ntfy.BaseURL = "HTTPS://127.0.0.1:443/relay/"
		case Gotify:
			c.Gotify.BaseURL = "HTTPS://127.0.0.1:443/relay/"
		case Pushover:
			c.Pushover.BaseURL = "HTTPS://127.0.0.1:443/relay/"
		}
		b, err := Identity(c)
		if err != nil || a != b {
			t.Fatal("equivalent endpoint spellings changed identity")
		}
	}
	if id, err := Identity(Config{}); id != "" || err == nil {
		t.Fatal("invalid configuration has identity")
	}
}

func TestMessageLimitsAreEnforcedBeforeIO(t *testing.T) {
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, goodReply(Gotify)) }))
	defer s.Close()
	base := fixtureConfig(Gotify, s.URL)
	cases := []Message{{}, {Text: string([]byte{0xff})}, {Text: "fixture\x00text"}, {Text: strings.Repeat("x", MaxTextBytes+1)}, {Title: strings.Repeat("界", 251), Text: "fixture"}, {Title: "title\nother", Text: "fixture"}, {Text: "fixture", MonitorName: strings.Repeat("x", 257)}}
	for index, m := range cases {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			r := Send(context.Background(), base, m)
			requireResult(t, r, Rejected)
			if r.ErrorCode != "invalid_message" {
				t.Fatal("message error misclassified")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("invalid messages reached the network")
	}
	for _, test := range []struct {
		kind Kind
		text string
	}{{Slack, strings.Repeat("<", 1001)}, {Telegram, strings.Repeat("😀", 2049)}, {Ntfy, strings.Repeat("界", 1366)}, {Pushover, strings.Repeat("界", 1025)}} {
		c := fixtureConfig(test.kind, s.URL)
		requireResult(t, Send(context.Background(), c, Message{Text: test.text}), Rejected)
	}
	for _, test := range []struct {
		kind Kind
		text string
	}{{Slack, strings.Repeat("x", 4000)}, {Telegram, strings.Repeat("😀", 2048)}, {Ntfy, strings.Repeat("x", 4096)}, {Gotify, strings.Repeat("界", 5461)}, {Pushover, strings.Repeat("界", 1024)}} {
		c := fixtureConfig(test.kind, "https://127.0.0.1")
		if validateMessage(c, Message{Text: test.text}) != nil {
			t.Fatal("valid text boundary rejected")
		}
	}
	engine := testEngine(t, Options{Timeout: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	requireResult(t, engine.Send(ctx, base, Message{Text: "fixture"}), Unknown)
	if calls.Load() != 0 {
		t.Fatal("pre-canceled request reached the network")
	}
}
