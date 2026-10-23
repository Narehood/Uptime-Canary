package notifyhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Credentials have one provider-specific location. No payload can select a URL,
// override authentication, change method, or add arbitrary request headers.
func request(ctx context.Context, c Config, m Message) (*http.Request, error) {
	u := baseEndpoint(c)
	var payload any
	contentType := "application/json"
	var body []byte
	switch c.Kind {
	case Slack:
		payload = struct {
			Text        string `json:"text"`
			Parse       string `json:"parse"`
			Mrkdwn      bool   `json:"mrkdwn"`
			LinkNames   int    `json:"link_names"`
			UnfurlLinks bool   `json:"unfurl_links"`
			UnfurlMedia bool   `json:"unfurl_media"`
		}{Text: escapeSlack(combinedText(m)), Parse: "none"}
	case Telegram:
		p := c.Telegram
		prefix := strings.TrimSuffix(u.EscapedPath(), "/")
		appendRoute(u, "bot"+p.BotToken+"/sendMessage")
		u.RawPath = prefix + "/" + strings.ReplaceAll(url.PathEscape("bot"+p.BotToken), ":", "%3A") + "/sendMessage"
		payload = struct {
			ChatID  any    `json:"chat_id"`
			Text    string `json:"text"`
			Thread  int64  `json:"message_thread_id,omitempty"`
			Silent  bool   `json:"disable_notification,omitempty"`
			Protect bool   `json:"protect_content,omitempty"`
			Preview struct {
				Disabled bool `json:"is_disabled"`
			} `json:"link_preview_options"`
		}{ChatID: telegramChat(p.ChatID), Text: combinedText(m), Thread: p.MessageThreadID, Silent: p.Silent, Protect: p.ProtectContent,
			Preview: struct {
				Disabled bool `json:"is_disabled"`
			}{p.DisableLinkPreview}}
	case Ntfy:
		p := c.Ntfy
		appendRoute(u, "") // JSON publication goes to the API root, not /topic.
		payload = struct {
			Topic    string   `json:"topic"`
			Title    string   `json:"title,omitempty"`
			Message  string   `json:"message"`
			Priority int      `json:"priority,omitempty"`
			Tags     []string `json:"tags,omitempty"`
		}{p.Topic, m.Title, m.Text, p.Priority, p.Tags}
	case Gotify:
		appendRoute(u, "message")
		payload = struct {
			Title    string `json:"title,omitempty"`
			Message  string `json:"message"`
			Priority int    `json:"priority"`
		}{m.Title, m.Text, c.Gotify.Priority}
	case Pushover:
		p := c.Pushover
		appendRoute(u, "1/messages.json")
		form := url.Values{"token": {p.AppToken}, "user": {p.UserKey}, "message": {m.Text}, "priority": {strconv.Itoa(p.Priority)}}
		if m.Title != "" {
			form.Set("title", m.Title)
		}
		if p.Device != "" {
			form.Set("device", p.Device)
		}
		if p.Sound != "" {
			form.Set("sound", p.Sound)
		}
		if p.Priority == 2 {
			form.Set("retry", strconv.Itoa(p.RetrySeconds))
			form.Set("expire", strconv.Itoa(p.ExpireSeconds))
		}
		body = []byte(form.Encode())
		contentType = "application/x-www-form-urlencoded"
	}
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, errMessage
		}
	}
	if len(body) > MaxRequestBytes {
		return nil, errMessage
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, errMessage
	}
	req.GetBody = nil // Never permit an implicit body replay.
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	if c.Kind == Slack {
		req.Header.Set("Accept", "text/plain")
	}
	req.Header.Set("User-Agent", "Uptime-Canary-notifyhttp/1")
	switch c.Kind {
	case Ntfy:
		p := c.Ntfy
		if p.Token != "" {
			req.Header.Set("Authorization", "Bearer "+p.Token)
		}
		if p.Username != "" {
			req.SetBasicAuth(p.Username, p.Password)
		}
	case Gotify:
		req.Header.Set("X-Gotify-Key", c.Gotify.AppToken)
	}
	return req, nil
}

func telegramChat(s string) any {
	if strings.HasPrefix(s, "@") {
		return s
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

var (
	ntfyIDPattern    = regexp.MustCompile(`^[A-Za-z0-9]{12}$`)
	requestIDPattern = regexp.MustCompile(`^[A-Fa-f0-9]{8}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{4}-[A-Fa-f0-9]{12}$`)
	// time.Parse's layout fallback permits one-digit hours, comma fractions
	// and out-of-range zone components. Gate syntax before checking the date.
	receiptDatePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T(?:[01][0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9](?:\.[0-9]+)?(?:Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])$`)
)

func acknowledgement(c Config, r Result, object map[string]any) Result {
	switch c.Kind {
	case Telegram:
		ok, exists := object["ok"].(bool)
		if !exists {
			return r
		}
		if !ok {
			code, _ := integerField(object, "error_code")
			state, errorCode := Rejected, "provider_rejected"
			if code == 429 {
				state, errorCode = Retryable, "rate_limited"
			} else if code >= 500 && code <= 599 {
				state, errorCode = Retryable, "unavailable"
			}
			next := failure(c.Kind, state, errorCode)
			next.HTTPStatus, next.RetryAfter = r.HTTPStatus, r.RetryAfter
			return next
		}
		if hasAny(object, "error_code", "error", "errors", "parameters", "errorCode", "code", "status") {
			return r
		}
		if description, present := object["description"]; present {
			if _, valid := description.(string); !valid {
				return r
			}
		}
		result := objectField(object, "result")
		id, validID := integerField(result, "message_id")
		chat := objectField(result, "chat")
		chatID, validChatID := integerField(chat, "id")
		if !validID || id <= 0 || !validChatID || chatID == 0 || chatID < -maxChatID || chatID > maxChatID {
			return r
		}
		p := c.Telegram
		if strings.HasPrefix(p.ChatID, "@") {
			if !strings.EqualFold(stringField(chat, "username"), strings.TrimPrefix(p.ChatID, "@")) {
				return r
			}
		} else if strconv.FormatInt(chatID, 10) != p.ChatID {
			return r
		}
		if p.MessageThreadID != 0 {
			thread, ok := integerField(result, "message_thread_id")
			if !ok || thread != p.MessageThreadID {
				return r
			}
		}
		// Include chat in opaque receipt hash because message IDs are per-chat.
		return accepted(r, strconv.FormatInt(chatID, 10)+":"+strconv.FormatInt(id, 10))
	case Ntfy:
		if hasAny(object, "error", "errors", "code", "error_code", "errorCode", "status") {
			return r
		}
		id := stringField(object, "id")
		if ntfyIDPattern.MatchString(id) && stringField(object, "event") == "message" && stringField(object, "topic") == c.Ntfy.Topic {
			return accepted(r, id)
		}
	case Gotify:
		if hasAny(object, "error", "errors", "errorCode", "error_code", "code", "status") {
			return r
		}
		id, ok := integerField(object, "id")
		appID, validAppID := integerField(object, "appid")
		_, validMessage := object["message"].(string)
		date := stringField(object, "date")
		_, dateError := time.Parse(time.RFC3339Nano, date)
		if ok && id > 0 && validAppID && appID > 0 && validMessage && receiptDatePattern.MatchString(date) && dateError == nil {
			return accepted(r, strconv.FormatInt(id, 10))
		}
	case Pushover:
		status, ok := integerField(object, "status")
		if !ok {
			return r
		}
		if status == 0 {
			next := failure(c.Kind, Rejected, "provider_rejected")
			next.HTTPStatus, next.RetryAfter = r.HTTPStatus, r.RetryAfter
			return next
		}
		if status != 1 || hasAny(object, "error", "errors", "error_code", "errorCode", "code") {
			return r
		}
		id := stringField(object, "request")
		if requestIDPattern.MatchString(id) {
			return accepted(r, id)
		}
	}
	return r
}
