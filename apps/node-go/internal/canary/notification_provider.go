package canary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// A provider describes wire formatting and delivery confirmation independently
// of monitor configuration. Future reusable channels can select one explicitly.
type notificationProvider string

const (
	notificationGeneric       notificationProvider = "generic"
	notificationDiscord       notificationProvider = "discord"
	notificationResponseLimit                      = 64 * 1024
	notificationRetryLimitMs                       = int64(24 * time.Hour / time.Millisecond)
)

var discordWebhookPath = regexp.MustCompile(`^/api(?:/v[1-9][0-9]*)?/webhooks/[0-9]+/[A-Za-z0-9_-]+$`)

func discordWebhookURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.RawPath != "" || u.Port() != "" && u.Port() != "443" {
		return nil
	}
	// Exact hosts only. Discord documents the API base at discord.com and the
	// PTB/Canary hosts in its official testing-client guide. Never match suffixes.
	// https://support.discord.com/hc/en-us/articles/360035675191
	switch strings.ToLower(u.Hostname()) {
	case "discord.com", "discordapp.com", "canary.discord.com", "ptb.discord.com":
	default:
		return nil
	}
	if !discordWebhookPath.MatchString(u.Path) {
		return nil
	}
	return u
}

func notificationProviderForURL(raw string) notificationProvider {
	if discordWebhookURL(raw) != nil {
		return notificationDiscord
	}
	return notificationGeneric
}

func (p notificationProvider) format(m Monitor, i Incident, a attempt, node Identity, observations []Sample) ([]byte, error) {
	if p == notificationDiscord {
		return formatDiscordNotification(m, i, a, node, observations)
	}
	return json.Marshal(map[string]any{"monitor": Identity{m.ID, m.Name}, "incident": map[string]any{"id": a.IncidentID, "aliases": episodeIDs(i), "openedAt": i.OpenedAt, "resolvedAt": i.ResolvedAt}, "monitorId": m.ID, "incidentId": a.IncidentID, "kind": a.Kind, "repeatSlot": a.Slot, "node": node, "observations": publicSamples(observations)})
}

func formatDiscordNotification(m Monitor, i Incident, a attempt, node Identity, observations []Sample) ([]byte, error) {
	// https://docs.discord.com/developers/resources/webhook#execute-webhook
	// Metadata and probe messages can contain arbitrary text. Never echo a
	// configured webhook credential, even if it appears in those display fields.
	secret := ""
	if u := discordWebhookURL(m.WebhookURL); u != nil {
		secret = u.Path[strings.LastIndexByte(u.Path, '/')+1:]
	}
	text := func(value string, limit int) string {
		if m.WebhookURL != "" {
			value = strings.ReplaceAll(value, m.WebhookURL, "[redacted webhook]")
		}
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
		value = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, strings.ToValidUTF8(value, "\uFFFD"))
		return discordBoundText(strings.Join(strings.Fields(value), " "), limit)
	}
	state := "DOWN"
	if a.Kind == "recovery" {
		state = "RECOVERED"
	} else if a.Kind == "repeat" {
		state = "STILL DOWN (repeat " + strconv.FormatInt(a.Slot, 10) + ")"
	} else if a.Kind == "test" {
		state = "TEST NOTIFICATION"
	}
	var content strings.Builder
	if a.Kind == "test" {
		fmt.Fprintf(&content, "Uptime Canary: %s\nChannel: %s\nSender: %s", state, text(m.Name, 240), text(node.Name+" ("+node.ID+")", 120))
	} else {
		fmt.Fprintf(&content, "Uptime Canary: %s\nMonitor: %s\nIncident: %s\nOpened: %s\nSender: %s", state, text(m.Name, 240), text(a.IncidentID, 100), time.UnixMilli(i.OpenedAt).UTC().Format(time.RFC3339), text(node.Name+" ("+node.ID+")", 120))
	}
	if a.Kind == "recovery" && i.ResolvedAt != nil {
		fmt.Fprintf(&content, "\nRecovered: %s", time.UnixMilli(*i.ResolvedAt).UTC().Format(time.RFC3339))
	}
	for _, s := range observations[:min(len(observations), 5)] {
		fmt.Fprintf(&content, "\n%s: %s — %s", text(s.NodeID, 60), text(s.State, 16), text(s.Message, 220))
	}
	if len(observations) > 5 {
		fmt.Fprintf(&content, "\n(%d more observations)", len(observations)-5)
	}
	// Empty parse disables user, role, and everyone mentions. Keep this explicit
	// (an empty JSON array, not null); untrusted display text must never ping.
	return json.Marshal(map[string]any{
		"content":          discordBoundText(content.String(), 2000),
		"allowed_mentions": map[string]any{"parse": []string{}},
	})
}

// Count UTF-16 units conservatively, so astral characters also fit Discord's
// 2000-character limit. Truncation always preserves complete UTF-8 code points.
func discordBoundText(value string, limit int) string {
	units := 0
	cut := 0
	for offset, r := range value {
		if units <= limit-1 {
			cut = offset
		}
		width := 1
		if r > 0xFFFF {
			width = 2
		}
		if units+width > limit {
			return value[:cut] + "…"
		}
		units += width
	}
	return value
}

// Only safe outcome metadata escapes the provider boundary. Raw errors may
// contain the secret URL, and response bodies may contain credentials or text
// controlled by the provider; neither belongs in logs or delivery status.
type notificationDeliveryResult struct {
	Delivered    bool
	RetryAfterMs int64
	HTTPStatus   int
	ErrorCode    string
	ProviderID   string
}

func deliverNotification(ctx context.Context, client Doer, target string, body []byte, attemptID string, now int64) notificationDeliveryResult {
	provider := notificationProviderForURL(target)
	if provider == notificationDiscord {
		u := discordWebhookURL(target)
		query, err := url.ParseQuery(u.RawQuery)
		if err != nil {
			return notificationDeliveryResult{}
		}
		query.Set("wait", "true")
		u.RawQuery = query.Encode()
		// Canonicalize recognized legacy/testing hosts to the documented API
		// base, rather than following a redirect with a credential-bearing path.
		u.Host = "discord.com"
		target = u.String()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return notificationDeliveryResult{}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", attemptID)
	req.GetBody = nil // The durable scheduler owns retries, including ambiguous sends.
	if c, ok := client.(*http.Client); ok {
		// Do not mutate a shared client, including an injected client's policy.
		copy := *c
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &copy
	}
	resp, err := client.Do(req)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err != nil || resp == nil {
		return notificationDeliveryResult{}
	}
	var response []byte
	if resp.Body != nil {
		response, err = io.ReadAll(io.LimitReader(resp.Body, notificationResponseLimit+1))
	}
	result := notificationDeliveryResult{HTTPStatus: resp.StatusCode}
	if resp.StatusCode == http.StatusTooManyRequests {
		result.RetryAfterMs = notificationRetryAfter(resp.Header.Get("Retry-After"), response, now)
	}
	if provider == notificationGeneric {
		result.Delivered = resp.StatusCode >= 200 && resp.StatusCode < 300
	} else if resp.StatusCode == http.StatusOK && err == nil && len(response) <= notificationResponseLimit {
		// With wait=true, a created message is the confirmed success response.
		// An empty/202/204 response is not evidence that Discord saved a message.
		var message struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(response, &message) == nil {
			id, parseErr := strconv.ParseUint(message.ID, 10, 64)
			result.Delivered = parseErr == nil && id != 0
			if result.Delivered {
				result.ProviderID = digest(message.ID)
			}
		}
	}
	return result
}

func notificationRetryAfter(header string, body []byte, now int64) int64 {
	// Discord specifies fractional seconds, not milliseconds:
	// https://docs.discord.com/developers/topics/rate-limits#exceeding-a-rate-limit
	seconds := func(value string) int64 {
		v, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			return 0
		}
		return int64(math.Ceil(min(float64(notificationRetryLimitMs), max(1000, v*1000))))
	}
	delay := seconds(strings.TrimSpace(header))
	if delay == 0 {
		if date, err := http.ParseTime(header); err == nil && date.UnixMilli() > now {
			delay = min(notificationRetryLimitMs, max(1000, date.UnixMilli()-now))
		}
	}
	if len(body) <= notificationResponseLimit {
		var response struct {
			RetryAfter json.Number `json:"retry_after"`
		}
		if json.Unmarshal(body, &response) == nil {
			delay = max(delay, seconds(string(response.RetryAfter)))
		}
	}
	return delay
}
