package delivery

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigValidation(t *testing.T) {
	base := fixtureConfig(Mailgun, "https://private.example/prefix")
	for _, tc := range []struct {
		name   string
		change func(*Config)
	}{
		{"unknown-kind", func(c *Config) { c.Kind = "fixture-secret" }},
		{"sender-crlf", func(c *Config) { c.Sender.Email += "\r\nBcc: injected@example.test" }},
		{"name-crlf", func(c *Config) { c.Sender.Name += "\r\nInjected: yes" }},
		{"recipient-invalid", func(c *Config) { c.Recipients[0].Email = "invalid" }},
		{"recipient-display-name", func(c *Config) { c.Recipients[0].Email = "First <first@example.test>" }},
		{"recipient-unicode", func(c *Config) { c.Recipients[0].Email = "first@exämple.test" }},
		{"recipient-duplicates", func(c *Config) { c.Recipients[1].Email = strings.ToUpper(c.Recipients[0].Email) }},
		{"no-recipients", func(c *Config) { c.Recipients = nil }},
		{"too-many-recipients", func(c *Config) { c.Recipients = make([]Address, MaxRecipients+1) }},
		{"invalid-domain", func(c *Config) { c.Mailgun.Domain = "mg.example.test/path" }},
		{"invalid-region", func(c *Config) { c.Mailgun.Region = "elsewhere" }},
		{"missing-key", func(c *Config) { c.Mailgun.APIKey = "" }},
		{"key-crlf", func(c *Config) { c.Mailgun.APIKey = "key\r\nInjected: yes" }},
		{"template-subject", func(c *Config) { c.Templates.Subject = "subject\r\nBcc: injected@example.test" }},
		{"template-body", func(c *Config) { c.Templates.Text = strings.Repeat("x", MaxBodyBytes+1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := base
			config.Recipients = append([]Address(nil), base.Recipients...)
			tc.change(&config)
			if err := Validate(config); err == nil || strings.Contains(err.Error(), "fixture-secret") {
				t.Fatal("invalid config accepted or unsafe error", err)
			}
			result := Send(context.Background(), config, fixtureMessage())
			encoded, _ := json.Marshal(result)
			if result.Accepted || result.ErrorCode != "invalid_config" || strings.Contains(string(encoded), "fixture-secret") {
				t.Fatal("invalid config result", string(encoded))
			}
		})
	}
	if err := Validate(base); err != nil {
		t.Fatal("valid config rejected", err)
	}
}

func TestURLValidation(t *testing.T) {
	for _, raw := range []string{
		"https://user:password@host.test", "https://host.test/#fragment", "https://host.test/#",
		"https://host.test/?password=secret", "https://host.test/?", "https://host.test:0", "https://host.test:65536",
		"https://host.test:abc", "https://host.test:", "file:///etc/passwd", "//host.test", "http://host.test",
		"https://host.test/%0d%0aInjected", "https://host.test/\\path", "https://", "https://bad_host.test", "https://host.test\r\nX: secret",
	} {
		t.Run(raw, func(t *testing.T) {
			if validURL(raw, false) {
				t.Error("invalid URL accepted")
			}
		})
	}
	for _, raw := range []string{"https://host.test", "https://host.test/prefix/", "https://[::1]:8443", "https://127.0.0.1:1"} {
		if !validURL(raw, false) {
			t.Error("valid URL rejected", raw)
		}
	}
	if !validURL("http://127.0.0.1:8080", true) {
		t.Error("explicit HTTP fixture rejected")
	}
}

func TestSMTPValidation(t *testing.T) {
	base := fixtureConfig(SMTP, "")
	base.SMTP = SMTPConfig{Server: "smtp.example.test", Port: 587, Username: "user", Password: "password"}
	if err := Validate(base); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*SMTPConfig)
	}{
		{"plain-credentials", func(s *SMTPConfig) { s.TLSMode = "plain" }},
		{"plaintext-password-only", func(s *SMTPConfig) { s.Username = "" }},
		{"missing-password", func(s *SMTPConfig) { s.Password = "" }},
		{"bad-mode", func(s *SMTPConfig) { s.TLSMode = "opportunistic" }},
		{"bad-port", func(s *SMTPConfig) { s.Port = 0 }},
		{"server-inline-password", func(s *SMTPConfig) { s.Server = "smtp://user:password@host.test" }},
		{"server-header-injection", func(s *SMTPConfig) { s.Server = "smtp.example.test\r\nAUTH secret" }},
		{"password-control", func(s *SMTPConfig) { s.Password = "pass\x00word" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := base
			tc.change(&config.SMTP)
			if Validate(config) == nil {
				t.Fatal("invalid SMTP config accepted")
			}
		})
	}
	base.SMTP.TLSMode, base.SMTP.Username, base.SMTP.Password = "plain", "", ""
	if err := Validate(base); err != nil {
		t.Fatal("explicit plain unauthenticated relay rejected", err)
	}
}

func TestMessageBoundsAndLiteralTemplates(t *testing.T) {
	config := fixtureConfig(Mailgun, "https://fixture.example")
	for _, tc := range []struct {
		name   string
		change func(*Message)
	}{
		{"subject-injection", func(m *Message) { m.Subject = "subject\r\nBcc: victim@example.test" }},
		{"subject-limit", func(m *Message) { m.Subject = strings.Repeat("x", 513) }},
		{"body-limit", func(m *Message) { m.Text = strings.Repeat("x", MaxBodyBytes+1) }},
		{"html-limit", func(m *Message) { m.HTML = strings.Repeat("x", MaxBodyBytes+1) }},
		{"null-body", func(m *Message) { m.Text = "text\x00body" }},
		{"invalid-utf8", func(m *Message) { m.Text = "\xff" }},
		{"metadata-limit", func(m *Message) { m.MonitorName = strings.Repeat("x", 257) }},
		{"key-injection", func(m *Message) { m.IdempotencyKey = "one\r\nTwo: bad" }},
		{"observations-limit", func(m *Message) { m.ObservationsSummary = strings.Repeat("x", (16<<10)+1) }},
		{"email-api-body", func(m *Message) { m.APIBody = json.RawMessage(`{"text":"override"}`) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := fixtureMessage()
			tc.change(&m)
			if _, err := prepareMessage(config, m); err == nil {
				t.Fatal("invalid message accepted")
			}
			if result := Send(context.Background(), config, m); result.ErrorCode != "invalid_message" || result.Accepted {
				t.Fatal(result)
			}
		})
	}
	config.Templates = Templates{Subject: "{{event_kind}}: {{monitor_name}}", Text: "{{observations_summary}} {{monitor_id}} {{incident_id}} {{idempotency_key}}", HTML: "<b>{{monitor_name}}</b>{{exec shell}}"}
	m, err := prepareMessage(config, Message{EventKind: "down", MonitorName: "<API>", MonitorID: "m1", IncidentID: "i1", IdempotencyKey: "r1", ObservationsSummary: "failure"})
	if err != nil || m.Subject != "down: <API>" || m.Text != "failure m1 i1 r1" || m.HTML != "<b>&lt;API&gt;</b>{{exec shell}}" {
		t.Fatalf("template result=%+v err=%v", m, err)
	}
	config.Templates.Subject = "{{observations_summary}}"
	if _, err := prepareMessage(config, Message{ObservationsSummary: "line one\r\nline two"}); err == nil {
		t.Fatal("rendered header injection accepted")
	}
	config.Templates = Templates{Text: strings.Repeat("{{observations_summary}}", 10)}
	if _, err := prepareMessage(config, Message{ObservationsSummary: strings.Repeat("x", 16<<10)}); err == nil {
		t.Fatal("rendered expansion exceeds limit")
	}
	config.Kind = Webhook
	for _, raw := range []string{"[]", "null", "invalid", strings.Repeat("x", MaxBodyBytes+1)} {
		if _, err := prepareMessage(config, Message{APIBody: json.RawMessage(raw)}); err == nil {
			t.Error("invalid webhook body accepted", raw[:min(len(raw), 20)])
		}
	}
}
