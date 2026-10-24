package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func endpoint(base, fallback, path string) string {
	if base == "" {
		base = fallback
	}
	u, _ := url.Parse(base) // Already validated.
	u.Path = strings.TrimRight(u.Path, "/") + path
	u.RawPath = ""
	return u.String()
}

func addressString(a Address) string { return (&mail.Address{Name: a.Name, Address: a.Email}).String() }

func (e *Engine) sendHTTP(ctx context.Context, c Config, m Message) Result {
	var target, contentType string
	var payload []byte
	switch c.Kind {
	case Mailgun:
		base := "https://api.mailgun.net"
		if c.Mailgun.Region == "EU" {
			base = "https://api.eu.mailgun.net"
		}
		target = endpoint(c.Mailgun.BaseURL, base, "/v3/"+c.Mailgun.Domain+"/messages")
		var buf bytes.Buffer
		form := multipart.NewWriter(&buf)
		_ = form.WriteField("from", addressString(c.Sender))
		for _, recipient := range c.Recipients {
			_ = form.WriteField("to", addressString(recipient))
		}
		_ = form.WriteField("subject", m.Subject)
		if m.Text != "" {
			_ = form.WriteField("text", m.Text)
		}
		if m.HTML != "" {
			_ = form.WriteField("html", m.HTML)
		}
		if m.IdempotencyKey != "" {
			_ = form.WriteField("v:canary-idempotency-key", m.IdempotencyKey)
		}
		_ = form.Close()
		payload, contentType = buf.Bytes(), form.FormDataContentType()
	case Brevo:
		target = endpoint(c.Brevo.BaseURL, "https://api.brevo.com", "/v3/smtp/email")
		// The static-content guide asks callers to choose one body type.
		body := map[string]any{"sender": c.Sender, "to": c.Recipients, "subject": m.Subject}
		if m.HTML != "" {
			body["htmlContent"] = m.HTML
		} else {
			body["textContent"] = m.Text
		}
		if m.IdempotencyKey != "" {
			body["headers"] = map[string]string{"X-Canary-Idempotency-Key": m.IdempotencyKey}
		}
		payload, _ = json.Marshal(body)
	case Mandrill:
		target = endpoint(c.Mandrill.BaseURL, "https://mandrillapp.com", "/api/1.0/messages/send")
		recipients := make([]map[string]string, len(c.Recipients))
		for i, a := range c.Recipients {
			recipients[i] = map[string]string{"email": a.Email, "name": a.Name, "type": "to"}
		}
		body := map[string]any{"from_email": c.Sender.Email, "from_name": c.Sender.Name,
			"to": recipients, "subject": m.Subject, "text": m.Text, "html": m.HTML}
		if m.IdempotencyKey != "" {
			body["metadata"] = map[string]string{"canary_idempotency_key": m.IdempotencyKey}
		}
		payload, _ = json.Marshal(map[string]any{"key": c.Mandrill.APIKey, "message": body, "async": false})
	case Webhook:
		target = c.Webhook.URL
		if len(m.APIBody) != 0 {
			payload = m.APIBody
		} else {
			payload, _ = json.Marshal(m)
		}
		if len(payload) > MaxBodyBytes {
			return failure(c.Kind, Rejected, "invalid_message")
		}
	}
	if contentType == "" {
		contentType = "application/json"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return failure(c.Kind, Rejected, "invalid_config")
	}
	// The caller owns retries, including on a stale pooled connection. Disabling
	// body replay also prevents net/http from replaying Idempotency-Key POSTs.
	request.GetBody = nil
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "application/json")
	switch c.Kind {
	case Mailgun:
		request.SetBasicAuth("api", c.Mailgun.APIKey)
	case Brevo:
		request.Header.Set("api-key", c.Brevo.APIKey)
	case Webhook:
		if c.Webhook.BearerToken != "" {
			request.Header.Set("Authorization", "Bearer "+c.Webhook.BearerToken)
		}
		if m.IdempotencyKey != "" {
			request.Header.Set("Idempotency-Key", m.IdempotencyKey)
		}
	}
	response, err := e.client.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return transportFailure(ctx, c.Kind)
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	var result Result
	switch {
	case response.StatusCode == http.StatusTooManyRequests:
		result = failure(c.Kind, Retryable, "rate_limited")
		result.RetryAfter = retryAfter(response.Header, time.Now())
	case response.StatusCode >= 500 && response.StatusCode <= 599:
		result = failure(c.Kind, Retryable, "unavailable")
		result.RetryAfter = retryAfter(response.Header, time.Now())
	case response.StatusCode < 200 || response.StatusCode >= 300:
		result = failure(c.Kind, Rejected, "provider_rejected")
	case len(data) > MaxResponseBytes:
		result = failure(c.Kind, Unknown, "response_too_large")
	case readErr != nil:
		result = transportFailure(ctx, c.Kind)
	default:
		result = parseAcknowledgement(c, response.StatusCode, data)
	}
	result.HTTPStatus = response.StatusCode
	return result
}

func transportFailure(ctx context.Context, kind Kind) Result {
	if contextExpired(ctx) {
		return failure(kind, Unknown, "canceled")
	}
	return failure(kind, Unknown, "transport")
}

// Socket deadlines can fire just before the context timer publishes ctx.Err.
func contextExpired(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	deadline, ok := ctx.Deadline()
	return ok && !time.Now().Before(deadline)
}

func parseAcknowledgement(c Config, status int, data []byte) Result {
	if c.Kind == Webhook {
		return accepted(c, "")
	}
	bad := failure(c.Kind, Unknown, "response")
	if !utf8.Valid(data) {
		return bad
	}
	switch c.Kind {
	case Mailgun:
		var body struct {
			ID string `json:"id"`
		}
		if status != 200 || !acknowledgementObject(data, "id") || json.Unmarshal(data, &body) != nil || providerID(body.ID) == "" {
			return bad
		}
		return accepted(c, body.ID)
	case Brevo:
		var body struct {
			ID string `json:"messageId"`
		}
		if status != 201 || !acknowledgementObject(data, "messageId") || json.Unmarshal(data, &body) != nil || providerID(body.ID) == "" {
			return bad
		}
		return accepted(c, body.ID)
	case Mandrill:
		var entries []json.RawMessage
		if json.Unmarshal(data, &entries) != nil {
			return bad
		}
		for _, entry := range entries {
			if !acknowledgementObject(entry, "email", "status", "_id") {
				return bad
			}
		}
		var body []struct {
			Email  string `json:"email"`
			Status string `json:"status"`
			ID     string `json:"_id"`
		}
		if status != 200 || json.Unmarshal(data, &body) != nil || len(body) != len(c.Recipients) {
			return bad
		}
		indices := make(map[string]int, len(c.Recipients))
		for i, a := range c.Recipients {
			indices[strings.ToLower(a.Email)] = i
		}
		seen := make(map[int]bool, len(body))
		result := Result{Kind: c.Kind, Status: Accepted, Recipients: make([]RecipientResult, len(body))}
		for _, item := range body {
			i, ok := indices[strings.ToLower(item.Email)]
			if !ok || seen[i] {
				return bad
			}
			seen[i] = true
			r := RecipientResult{Index: i}
			switch item.Status {
			case "sent", "queued", "scheduled":
				if providerID(item.ID) == "" {
					return bad
				}
				r.Accepted, r.Status, r.ProviderID = true, Accepted, providerID(item.ID)
				result.AcceptedRecipients++
			case "rejected", "invalid":
				r.Status = Rejected
				result.RejectedRecipients++
			default:
				return bad
			}
			result.Recipients[i] = r
		}
		switch {
		case result.RejectedRecipients == 0:
			result.Accepted = true
			if len(result.Recipients) == 1 {
				result.ProviderID = result.Recipients[0].ProviderID
			}
		case result.AcceptedRecipients == 0:
			result.Status, result.ErrorCode, result.ErrorMessage = Rejected, "recipient_rejected", "Provider rejected one or more recipients"
		default:
			result.Status, result.ErrorCode, result.ErrorMessage = Partial, "partial_acceptance", "Provider accepted only some recipients"
		}
		return result
	}
	return bad
}

// Only exact, unambiguous authority fields may acknowledge a delivery. Other
// additive metadata remains allowed; its values do not authorize acceptance.
func acknowledgementObject(data []byte, authority ...string) bool {
	d := json.NewDecoder(bytes.NewReader(data))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[strings.ToLower(key)] {
			return false
		}
		seen[strings.ToLower(key)] = true
		for _, field := range authority {
			if strings.EqualFold(key, field) && key != field {
				return false
			}
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return false
		}
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return false
	}
	_, err := d.Token()
	return err == io.EOF
}

func retryAfter(header http.Header, now time.Time) time.Duration {
	// Standard Retry-After is either nonnegative delta seconds or an HTTP date.
	if value := header.Get("Retry-After"); value != "" {
		if delay, ok := secondsDelay(value); ok {
			return delay
		}
		if date, err := http.ParseTime(value); err == nil {
			return boundDelay(date.Sub(now))
		}
	}
	if delay, ok := secondsDelay(header.Get("x-sib-ratelimit-reset")); ok {
		return delay
	}
	return 0
}

func secondsDelay(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 20 {
		return 0, false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	seconds, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return MaxRetryAfter, true
	}
	if seconds >= uint64(MaxRetryAfter/time.Second) {
		return MaxRetryAfter, true
	}
	return time.Duration(seconds) * time.Second, true
}

func boundDelay(delay time.Duration) time.Duration {
	if delay < 0 {
		return 0
	}
	if delay > MaxRetryAfter {
		return MaxRetryAfter
	}
	return delay
}
