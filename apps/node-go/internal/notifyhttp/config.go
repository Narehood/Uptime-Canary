package notifyhttp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	errConfig        = errors.New("notification configuration is invalid")
	errOptions       = errors.New("notification engine options are invalid")
	errMessage       = errors.New("notification message is invalid")
	topicPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	botPattern       = regexp.MustCompile(`^[0-9]{1,20}:[A-Za-z0-9_-]{1,235}$`)
	aliasPattern     = regexp.MustCompile(`^@[A-Za-z][A-Za-z0-9_]{0,31}$`)
	keyPattern       = regexp.MustCompile(`^[A-Za-z0-9]{30}$`)
	devicePattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,25}$`)
	soundPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	slackTeamPattern = regexp.MustCompile(`^T[A-Z0-9]{1,63}$`)
	slackAppPattern  = regexp.MustCompile(`^B[A-Z0-9]{1,63}$`)
)

const maxChatID int64 = (1 << 52) - 1

// UnmarshalJSON rejects unknown fields, alternate casing, duplicate keys, and
// foreign provider options. Its error never contains input or decoder details.
// Validate also applies to programmatically constructed Config values.
func (c *Config) UnmarshalJSON(data []byte) error {
	if len(data) > MaxConfigBytes {
		return errConfig
	}
	value, err := strictObject(data)
	if err != nil || !exactFields(value, "kind", "slack", "telegram", "ntfy", "gotify", "pushover") {
		return errConfig
	}
	fields := map[string][]string{
		"slack":    {"webhookURL", "allowInsecureHTTP"},
		"telegram": {"botToken", "chatId", "messageThreadId", "silent", "protectContent", "disableLinkPreview", "baseURL", "allowInsecureHTTP"},
		"ntfy":     {"topic", "token", "username", "password", "priority", "tags", "baseURL", "allowInsecureHTTP"},
		"gotify":   {"appToken", "priority", "baseURL", "allowInsecureHTTP"},
		"pushover": {"appToken", "userKey", "device", "priority", "sound", "retrySeconds", "expireSeconds", "baseURL", "allowInsecureHTTP"},
	}
	for name, allowed := range fields {
		if v, exists := value[name]; exists {
			object, ok := v.(map[string]any)
			if !ok || !exactFields(object, allowed...) {
				return errConfig
			}
		}
	}
	type plain Config
	var next plain
	if json.Unmarshal(data, &next) != nil || Validate(Config(next)) != nil {
		return errConfig
	}
	*c = Config(next)
	return nil
}

func exactFields(object map[string]any, fields ...string) bool {
	for key := range object {
		found := false
		for _, field := range fields {
			if key == field {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Validate performs only local checks. It never resolves DNS or calls a provider.
func Validate(c Config) error {
	count := 0
	for _, active := range []bool{c.Slack != nil, c.Telegram != nil, c.Ntfy != nil, c.Gotify != nil, c.Pushover != nil} {
		if active {
			count++
		}
	}
	if count != 1 {
		return errConfig
	}
	switch c.Kind {
	case Slack:
		if c.Slack == nil {
			return errConfig
		}
		u, err := endpoint(c.Slack.WebhookURL, "", c.Slack.AllowInsecureHTTP)
		if err != nil || u.Path == "" || u.Path == "/" || strings.HasSuffix(u.Path, "/") {
			return errConfig
		}
	case Telegram:
		if c.Telegram == nil {
			return errConfig
		}
		p := c.Telegram
		if !botPattern.MatchString(p.BotToken) || !validChat(p.ChatID) || p.MessageThreadID < 0 || p.MessageThreadID > maxChatID {
			return errConfig
		}
		if _, err := endpoint(p.BaseURL, "https://api.telegram.org", p.AllowInsecureHTTP); err != nil {
			return errConfig
		}
	case Ntfy:
		if c.Ntfy == nil {
			return errConfig
		}
		p := c.Ntfy
		if !topicPattern.MatchString(p.Topic) || p.Priority < 0 || p.Priority > 5 || len(p.Tags) > 16 {
			return errConfig
		}
		if p.Token != "" && (p.Username != "" || p.Password != "") {
			return errConfig
		}
		if (p.Username == "") != (p.Password == "") || strings.Contains(p.Username, ":") {
			return errConfig
		}
		if !optionalSecret(p.Token, 512) || !singleLine(p.Username, 128, true) || !singleLine(p.Password, 512, true) {
			return errConfig
		}
		for _, tag := range p.Tags {
			if !singleLine(tag, 64, false) || strings.Contains(tag, ",") {
				return errConfig
			}
		}
		if _, err := endpoint(p.BaseURL, "https://ntfy.sh", p.AllowInsecureHTTP); err != nil {
			return errConfig
		}
	case Gotify:
		if c.Gotify == nil {
			return errConfig
		}
		p := c.Gotify
		if !secret(p.AppToken, 512) || p.Priority < 0 || p.Priority > 10 {
			return errConfig
		}
		if _, err := endpoint(p.BaseURL, "", p.AllowInsecureHTTP); err != nil {
			return errConfig
		}
	case Pushover:
		if c.Pushover == nil {
			return errConfig
		}
		p := c.Pushover
		if !keyPattern.MatchString(p.AppToken) || !keyPattern.MatchString(p.UserKey) || p.Priority < -2 || p.Priority > 2 {
			return errConfig
		}
		if p.Device != "" && !devicePattern.MatchString(p.Device) || p.Sound != "" && !soundPattern.MatchString(p.Sound) {
			return errConfig
		}
		if p.Priority == 2 {
			if p.RetrySeconds < 30 || p.RetrySeconds > 10800 || p.ExpireSeconds < 1 || p.ExpireSeconds > 10800 || p.RetrySeconds > p.ExpireSeconds {
				return errConfig
			}
		} else if p.RetrySeconds != 0 || p.ExpireSeconds != 0 {
			return errConfig
		}
		if _, err := endpoint(p.BaseURL, "https://api.pushover.net", p.AllowInsecureHTTP); err != nil {
			return errConfig
		}
	default:
		return errConfig
	}
	return nil
}

func secret(s string, limit int) bool {
	if s == "" || len(s) > limit {
		return false
	}
	for _, b := range []byte(s) {
		if b < 0x21 || b > 0x7e {
			return false
		}
	}
	return true
}

func optionalSecret(s string, limit int) bool { return s == "" || secret(s, limit) }

func singleLine(s string, limit int, empty bool) bool {
	if (!empty && s == "") || len(s) > limit || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return false
		}
	}
	return true
}

func validChat(s string) bool {
	if len(s) > 33 {
		return false
	}
	if aliasPattern.MatchString(s) {
		return true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return err == nil && n != 0 && n >= -maxChatID && n <= maxChatID && strconv.FormatInt(n, 10) == s
}

// endpoint normalizes scheme, host, default port and a single trailing slash.
// Prefixes are exact, clean path segments. No URL resolution can replace the
// configured origin, smuggle a query, or make a credential part of an option URL.
func endpoint(raw, fallback string, allowHTTP bool) (*url.URL, error) {
	if raw == "" {
		raw = fallback
	}
	if !singleLine(raw, MaxURLBytes, false) || strings.ContainsAny(raw, "?#\\") {
		return nil, errConfig
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
		return nil, errConfig
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "https" && !(u.Scheme == "http" && allowHTTP) {
		return nil, errConfig
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "% ") {
		return nil, errConfig
	}
	if net.ParseIP(host) == nil {
		if strings.Contains(host, ":") {
			return nil, errConfig
		}
		for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return nil, errConfig
			}
			for _, b := range []byte(label) {
				if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-') {
					return nil, errConfig
				}
			}
		}
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, errConfig
		}
		port = strconv.Itoa(n)
	}
	if port == "443" && u.Scheme == "https" || port == "80" && u.Scheme == "http" {
		port = ""
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil, errConfig
	}
	u.Host = host
	if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	}
	if u.Path != "" && !strings.HasPrefix(u.Path, "/") {
		return nil, errConfig
	}
	segments := strings.Split(u.EscapedPath(), "/")
	for i, part := range segments {
		if i == 0 || i == len(segments)-1 && part == "" {
			continue
		}
		decoded, err := url.PathUnescape(part)
		if err != nil || decoded == "" || decoded == "." || decoded == ".." || !singleLine(decoded, MaxURLBytes, false) || strings.ContainsAny(decoded, "/\\?#") {
			return nil, errConfig
		}
		// Encoded path separators and nested escapes have proxy-dependent routing.
		if strings.Contains(decoded, "%") {
			return nil, errConfig
		}
	}
	u.RawPath = "" // Canonical encoding, never a user-selected RawPath.
	return u, nil
}

func baseEndpoint(c Config) *url.URL {
	var u *url.URL
	switch c.Kind {
	case Slack:
		u, _ = endpoint(c.Slack.WebhookURL, "", c.Slack.AllowInsecureHTTP)
	case Telegram:
		u, _ = endpoint(c.Telegram.BaseURL, "https://api.telegram.org", c.Telegram.AllowInsecureHTTP)
	case Ntfy:
		u, _ = endpoint(c.Ntfy.BaseURL, "https://ntfy.sh", c.Ntfy.AllowInsecureHTTP)
	case Gotify:
		u, _ = endpoint(c.Gotify.BaseURL, "", c.Gotify.AllowInsecureHTTP)
	case Pushover:
		u, _ = endpoint(c.Pushover.BaseURL, "https://api.pushover.net", c.Pushover.AllowInsecureHTTP)
	}
	return u
}

func appendRoute(u *url.URL, suffix string) {
	u.Path = strings.TrimSuffix(u.Path, "/") + "/" + suffix
	u.RawPath = ""
}

func digest(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// Identity hashes only the active destination route. Root must ALSO bind the
// configuration resource ID, especially for Gotify (whose app token is opaque).
func Identity(c Config) (string, error) {
	if Validate(c) != nil {
		return "", errConfig
	}
	u := baseEndpoint(c)
	var route []string
	switch c.Kind {
	case Slack:
		parts := strings.Split(u.Path, "/")
		if len(parts) == 5 && parts[1] == "services" && slackTeamPattern.MatchString(parts[2]) && slackAppPattern.MatchString(parts[3]) {
			u.Path = strings.Join(parts[:4], "/")
		}
	case Telegram:
		appendRoute(u, "sendMessage")
		route = []string{strings.ToLower(c.Telegram.ChatID), strconv.FormatInt(c.Telegram.MessageThreadID, 10)}
	case Ntfy:
		u.Path = strings.TrimSuffix(u.Path, "/") + "/"
		route = []string{c.Ntfy.Topic}
	case Gotify:
		appendRoute(u, "message")
	case Pushover:
		appendRoute(u, "1/messages.json")
		route = []string{digest(c.Pushover.UserKey), c.Pushover.Device}
	}
	route = append([]string{string(c.Kind), u.String()}, route...)
	// JSON tuple avoids collisions between endpoint and recipient boundaries.
	data, _ := json.Marshal(route)
	return digest(string(data)), nil
}

func validateMessage(c Config, m Message) error {
	if !utf8.ValidString(m.Title) || utf8.RuneCountInString(m.Title) > MaxTitleRunes || len(m.Title) > 1000 {
		return errMessage
	}
	if !singleLine(m.Title, 1000, true) || !utf8.ValidString(m.Text) || m.Text == "" || len(m.Text) > MaxTextBytes {
		return errMessage
	}
	for _, r := range m.Text {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return errMessage
		}
	}
	for _, s := range []string{m.EventKind, m.MonitorName, m.MonitorID, m.IncidentID, m.IdempotencyKey} {
		if !singleLine(s, MaxMetadataBytes, true) {
			return errMessage
		}
	}
	combined := combinedText(m)
	switch c.Kind {
	case Slack:
		if utf8.RuneCountInString(escapeSlack(combined)) > 4000 {
			return errMessage
		}
	case Telegram:
		// Conservative UTF-16 bound also fits Telegram's character limit.
		units := 0
		for _, r := range combined {
			units++
			if r > 0xffff {
				units++
			}
		}
		if units > 4096 {
			return errMessage
		}
	case Ntfy:
		if len(m.Text) > 4096 {
			return errMessage
		}
	case Gotify:
		if len(m.Text) > MaxTextBytes {
			return errMessage
		}
	case Pushover:
		if utf8.RuneCountInString(m.Text) > 1024 {
			return errMessage
		}
	}
	return nil
}

func combinedText(m Message) string {
	if m.Title == "" {
		return m.Text
	}
	return m.Title + "\n" + m.Text
}

func escapeSlack(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
