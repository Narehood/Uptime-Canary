package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"uptime-canary/apps/node-go/internal/notifyhttp"
)

func IsMessaging(kind Kind) bool {
	switch kind {
	case Slack, Telegram, Ntfy, Gotify, Pushover:
		return true
	}
	return false
}

func messagingConfig(c Config) notifyhttp.Config {
	return notifyhttp.Config{Kind: notifyhttp.Kind(c.Kind), Slack: c.Slack,
		Telegram: c.Telegram, Ntfy: c.Ntfy, Gotify: c.Gotify, Pushover: c.Pushover}
}

func validateMessaging(c Config) error {
	if c.Sender != (Address{}) || len(c.Recipients) != 0 || c.SMTP != (SMTPConfig{}) ||
		c.Mailgun != (MailgunConfig{}) || c.Brevo != (BrevoConfig{}) ||
		c.Mandrill != (MandrillConfig{}) || c.Webhook != (WebhookConfig{}) || c.Templates.HTML != "" ||
		notifyhttp.Validate(messagingConfig(c)) != nil {
		return errConfig
	}
	return nil
}

// MessagingIdentity is a credential-independent route digest. The runtime must
// bind it to its channel resource ID, particularly for Gotify's opaque app token.
func MessagingIdentity(c Config) (string, error) {
	if !IsMessaging(c.Kind) || Validate(c) != nil {
		return "", errConfig
	}
	return notifyhttp.Identity(messagingConfig(c))
}

// UnmarshalJSON preserves raw nested provider objects until the native leaf has
// checked exact names, duplicates and types. Standard struct decoding alone
// would erase those distinctions before validation.
func (c *Config) UnmarshalJSON(data []byte) error {
	if len(data) > 4*MaxBodyBytes {
		return errConfig
	}
	allowed := map[string]bool{"kind": true, "sender": true, "recipients": true,
		"smtp": true, "mailgun": true, "brevo": true, "mandrill": true, "webhook": true,
		"templates": true, "slack": true, "telegram": true, "ntfy": true, "gotify": true, "pushover": true}
	raw, err := configFields(data, allowed)
	if err != nil {
		return err
	}
	type plain Config
	var decoded plain
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&decoded) != nil {
		return errConfig
	}
	projected := map[string]json.RawMessage{"kind": raw["kind"]}
	hasProvider := false
	for _, key := range []string{"slack", "telegram", "ntfy", "gotify", "pushover"} {
		if value, exists := raw[key]; exists {
			projected[key], hasProvider = value, true
		}
	}
	if hasProvider {
		b, err := json.Marshal(projected)
		var native notifyhttp.Config
		if err != nil || json.Unmarshal(b, &native) != nil {
			return errConfig
		}
	}
	if value, exists := raw["templates"]; exists && IsMessaging(decoded.Kind) {
		fields, err := configFields(value, map[string]bool{"subject": true, "text": true, "html": true})
		if err != nil {
			return errConfig
		}
		for _, value := range fields {
			var text string
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &text) != nil {
				return errConfig
			}
		}
	}
	*c = Config(decoded)
	return nil
}

func configFields(data []byte, allowed map[string]bool) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	opening, err := d.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, errConfig
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || !allowed[key] || out[key] != nil {
			return nil, errConfig
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, errConfig
		}
		out[key] = value
	}
	if closing, err := d.Token(); err != nil || closing != json.Delim('}') {
		return nil, errConfig
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errConfig
	}
	return out, nil
}

func (e *Engine) sendMessaging(ctx context.Context, c Config, m Message, defaultTitle, defaultText bool) Result {
	if m.HTML != "" {
		return failure(c.Kind, Rejected, "invalid_message")
	}
	if defaultTitle {
		m.Subject = excerpt(m.Subject, 250, utf8.RuneCountInString)
		m.Subject = excerpt(m.Subject, 1000, lenBytes)
	}
	if defaultText {
		limit, cost := 16<<10, lenBytes
		switch c.Kind {
		case Slack:
			limit, cost = 4000-slackCost(m.Subject+"\n"), slackCost
		case Telegram:
			limit, cost = 4096-telegramCost(m.Subject+"\n"), telegramCost
		case Ntfy:
			limit = 4096
		case Pushover:
			limit, cost = 1024, utf8.RuneCountInString
		}
		m.Text = excerpt(m.Text, limit, cost)
	}
	r := e.messaging.Send(ctx, messagingConfig(c), notifyhttp.Message{Title: m.Subject, Text: m.Text,
		EventKind: m.EventKind, MonitorName: m.MonitorName, MonitorID: m.MonitorID,
		IncidentID: m.IncidentID, IdempotencyKey: m.IdempotencyKey})
	return Result{Kind: c.Kind, Accepted: r.Accepted, Status: Status(r.Status), HTTPStatus: r.HTTPStatus,
		ProviderID: r.ProviderID, RetryAfter: r.RetryAfter, ErrorCode: r.ErrorCode, ErrorMessage: r.ErrorMessage}
}

// Only generated defaults may be shortened. User templates/direct text remain
// exact and fail with invalid_message if they exceed the provider's limits.
func excerpt(text string, budget int, cost func(string) int) string {
	if cost(text) <= budget {
		return text
	}
	const marker = " [truncated; see monitor details]"
	remaining := budget - cost(marker)
	if remaining < 0 {
		return ""
	}
	used, end := 0, 0
	for index, value := range text {
		count := cost(string(value))
		if count > remaining-used {
			break
		}
		used += count
		end = index + utf8.RuneLen(value)
	}
	return text[:end] + marker
}

func lenBytes(value string) int { return len(value) }

func slackCost(value string) int {
	return utf8.RuneCountInString(value) + 4*strings.Count(value, "&") + 3*strings.Count(value, "<") + 3*strings.Count(value, ">")
}

func telegramCost(value string) int {
	count := 0
	for _, value := range value {
		count++
		if value > 0xffff {
			count++
		}
	}
	return count
}
