package delivery

import (
	"encoding/json"
	"errors"
	"html"
	"net"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var errConfig = errors.New("delivery configuration is invalid")
var errMessage = errors.New("delivery message is invalid")

// Validate checks local configuration only and returns a fixed, safe error.
func Validate(c Config) error {
	if c.Webhook.Embed {
		return errConfig
	}
	if !header(c.Templates.Subject, 512) || !body(c.Templates.Text, MaxBodyBytes) || !body(c.Templates.HTML, MaxBodyBytes) {
		return errConfig
	}
	if IsMessaging(c.Kind) {
		return validateMessaging(c)
	}
	if c.Slack != nil || c.Telegram != nil || c.Ntfy != nil || c.Gotify != nil || c.Pushover != nil {
		return errConfig
	}
	if c.Kind != Webhook {
		if !validAddress(c.Sender) || len(c.Recipients) == 0 || len(c.Recipients) > MaxRecipients {
			return errConfig
		}
		seen := make(map[string]bool, len(c.Recipients))
		for _, a := range c.Recipients {
			key := strings.ToLower(a.Email)
			if !validAddress(a) || seen[key] {
				return errConfig
			}
			seen[key] = true
		}
	}
	switch c.Kind {
	case SMTP:
		s := c.SMTP
		if !host(s.Server) || s.Port < 1 || s.Port > 65535 || !header(s.Username, 512) || !header(s.Password, 4096) {
			return errConfig
		}
		if (s.Username == "") != (s.Password == "") {
			return errConfig
		}
		switch s.TLSMode {
		case "", "starttls", "implicit":
		case "plain":
			if s.Username != "" || s.Password != "" {
				return errConfig
			}
		default:
			return errConfig
		}
	case Mailgun:
		m := c.Mailgun
		if !secret(m.APIKey) || !dnsName(m.Domain) || m.Region != "" && m.Region != "US" && m.Region != "EU" {
			return errConfig
		}
		if m.BaseURL != "" && !validURL(m.BaseURL, m.AllowInsecureHTTP) {
			return errConfig
		}
	case Brevo:
		if !secret(c.Brevo.APIKey) || c.Brevo.BaseURL != "" && !validURL(c.Brevo.BaseURL, c.Brevo.AllowInsecureHTTP) {
			return errConfig
		}
	case Mandrill:
		if !secret(c.Mandrill.APIKey) || c.Mandrill.BaseURL != "" && !validURL(c.Mandrill.BaseURL, c.Mandrill.AllowInsecureHTTP) {
			return errConfig
		}
	case Webhook:
		if !validURL(c.Webhook.URL, c.Webhook.AllowInsecureHTTP) || !header(c.Webhook.BearerToken, 4096) {
			return errConfig
		}
	default:
		return errConfig
	}
	return nil
}

func header(s string, limit int) bool {
	return len(s) <= limit && utf8.ValidString(s) && strings.IndexFunc(s, unicode.IsControl) < 0
}

func secret(s string) bool { return s != "" && header(s, 4096) && strings.TrimSpace(s) == s }

func body(s string, limit int) bool {
	return len(s) <= limit && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

func validAddress(a Address) bool {
	if !header(a.Name, 128) || len(a.Email) == 0 || len(a.Email) > 254 || !header(a.Email, 254) {
		return false
	}
	for _, r := range a.Email {
		if r > 126 || r < 33 {
			return false
		}
	}
	parsed, err := mail.ParseAddress(a.Email)
	if err != nil || parsed.Name != "" || parsed.Address != a.Email {
		return false
	}
	local, domain, ok := strings.Cut(a.Email, "@")
	return ok && len(local) <= 64 && dnsName(domain) && !strings.ContainsAny(local, "<>(),;:\\\"[]")
}

func dnsName(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

func host(s string) bool { return net.ParseIP(s) != nil || dnsName(s) }

func validURL(raw string, insecure bool) bool {
	if len(raw) == 0 || len(raw) > 2048 || !header(raw, 2048) {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.Fragment != "" || strings.Contains(raw, "#") || u.RawQuery != "" || u.ForceQuery || u.Host == "" || !host(u.Hostname()) {
		return false
	}
	if u.Scheme != "https" && !(insecure && u.Scheme == "http") {
		return false
	}
	if strings.HasSuffix(u.Host, ":") {
		return false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
	}
	return header(u.Path, 2048) && !strings.ContainsRune(u.Path, '\\')
}

func prepareMessage(c Config, m Message) (Message, error) {
	if c.Kind == Webhook && len(m.APIBody) > 0 {
		c.Templates = Templates{}
	}
	for _, value := range []string{m.Status, m.MonitorType, m.MonitorTarget, m.OpenedAt, m.ResolvedAt, m.Downtime, m.SenderNode} {
		if !header(value, 512) {
			return m, errMessage
		}
	}
	if !body(m.NodeResults, 16<<10) {
		return m, errMessage
	}
	for _, s := range []string{m.EventKind, m.MonitorName, m.MonitorID, m.IncidentID, m.IdempotencyKey} {
		if !header(s, 256) {
			return m, errMessage
		}
	}
	if !body(m.ObservationsSummary, 16<<10) {
		return m, errMessage
	}
	var err error
	if m.Subject == "" {
		m.Subject, err = renderTemplate(c.Templates.Subject, m, false, 512)
		if err != nil {
			return m, err
		}
	}
	if m.Text == "" {
		m.Text, err = renderTemplate(c.Templates.Text, m, false, MaxBodyBytes)
		if err != nil {
			return m, err
		}
	}
	if m.HTML == "" {
		m.HTML, err = renderTemplate(c.Templates.HTML, m, true, MaxBodyBytes)
		if err != nil {
			return m, err
		}
	}
	if m.Subject == "" {
		m.Subject = strings.TrimSpace(m.EventKind + " " + m.MonitorName)
		if m.Subject == "" {
			m.Subject = "Uptime Canary notification"
		}
	}
	if m.Text == "" && m.HTML == "" {
		m.Text = m.ObservationsSummary
		if m.Text == "" {
			m.Text = m.Subject
		}
	}
	if !header(m.Subject, 512) || !body(m.Text, MaxBodyBytes) || !body(m.HTML, MaxBodyBytes) {
		return m, errMessage
	}
	if len(m.APIBody) != 0 {
		if c.Kind != Webhook || len(m.APIBody) > MaxBodyBytes || !utf8.Valid(m.APIBody) {
			return m, errMessage
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(m.APIBody, &object) != nil || object == nil {
			return m, errMessage
		}
	}
	return m, nil
}

// Bound expansion during rendering, before allocating a potentially enormous
// expanded string. Values are inserted once; they are never template source.
func renderTemplate(source string, m Message, escape bool, limit int) (string, error) {
	values := map[string]string{
		"event_kind": m.EventKind, "monitor_name": m.MonitorName,
		"monitor_id": m.MonitorID, "incident_id": m.IncidentID,
		"observations_summary": m.ObservationsSummary, "idempotency_key": m.IdempotencyKey,
		"status": m.Status, "monitor_type": m.MonitorType, "monitor_target": m.MonitorTarget,
		"opened_at": m.OpenedAt, "resolved_at": m.ResolvedAt, "downtime": m.Downtime,
		"node_results": m.NodeResults, "sender_node": m.SenderNode,
	}
	var out strings.Builder
	appendBounded := func(value string) bool {
		if len(value) > limit-out.Len() {
			return false
		}
		out.WriteString(value)
		return true
	}
	for source != "" {
		start := strings.Index(source, "{{")
		if start < 0 {
			if !appendBounded(source) {
				return "", errMessage
			}
			break
		}
		if !appendBounded(source[:start]) {
			return "", errMessage
		}
		source = source[start:]
		end := strings.Index(source, "}}")
		if end < 0 {
			if !appendBounded(source) {
				return "", errMessage
			}
			break
		}
		token := source[:end+2]
		value, exists := values[source[2:end]]
		if !exists {
			value = token
		} else if escape {
			value = html.EscapeString(value)
		}
		if !appendBounded(value) {
			return "", errMessage
		}
		source = source[end+2:]
	}
	return out.String(), nil
}
