package canary

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
	providerDelivery "uptime-canary/apps/node-go/internal/delivery"
)

var errTemplateMessage = errors.New("Notification template exceeds the provider limits")

func templateField(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.ToValidUTF8(value, "\uFFFD")), " ")
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

// Target URLs are origins only. Paths, queries, userinfo, push tokens and
// private service DSNs never become implicit notification template facts.
func monitorTemplateTarget(m Monitor) string {
	if slices.Contains([]string{"http", "real-browser", "websocket"}, m.Type) {
		u, err := url.Parse(m.Target)
		if err != nil || u.Opaque != "" || u.Hostname() == "" || !slices.Contains([]string{"http", "https", "ws", "wss"}, strings.ToLower(u.Scheme)) {
			return ""
		}
		return templateField(strings.ToLower(u.Scheme)+"://"+u.Host, 512)
	}
	if slices.Contains([]string{"ping", "tcp", "tls", "dns", "smtp", "tailscale-ping"}, m.Type) && !strings.ContainsAny(m.Target, "/@?#\\ \r\n\t") {
		return templateField(m.Target, 512)
	}
	return ""
}

func templateStatus(kind string) string {
	switch kind {
	case "recovery":
		return "UP"
	case "repeat":
		return "STILL DOWN"
	case "test":
		return "TEST"
	default:
		return "DOWN"
	}
}

func templateDowntime(opened, resolved int64) string {
	seconds := max(int64(0), (resolved-opened)/1000)
	parts := []string{}
	for _, unit := range []struct {
		seconds int64
		name    string
	}{{86400, "d"}, {3600, "h"}, {60, "m"}, {1, "s"}} {
		if value := seconds / unit.seconds; value > 0 {
			parts = append(parts, strconv.FormatInt(value, 10)+unit.name)
			seconds %= unit.seconds
			if len(parts) == 2 {
				break
			}
		}
	}
	if len(parts) == 0 {
		return "0s"
	}
	return strings.Join(parts, " ")
}

func notificationTemplateFacts(m Monitor, i Incident, a attempt, node Identity, observations []Sample) providerDelivery.Message {
	lines, results := []string{}, []string{}
	for _, s := range observations {
		lines = append(lines, s.NodeID+": "+s.State+" - "+s.Message)
		line := templateField(s.NodeID, 128) + ": " + templateField(s.State, 32) + " - " + templateField(s.Message, 512)
		if (s.LatencyAvailable == nil || *s.LatencyAvailable) && finite(s.LatencyMs) && s.LatencyMs >= 0 {
			line += fmt.Sprintf(" (%.0f ms)", math.Round(s.LatencyMs))
		}
		results = append(results, line)
	}
	message := providerDelivery.Message{EventKind: a.Kind, MonitorName: m.Name, MonitorID: a.MonitorID, IncidentID: a.IncidentID, IdempotencyKey: a.ID, ObservationsSummary: strings.Join(lines, "\n"), Status: templateStatus(a.Kind), SenderNode: templateField(node.Name, 512)}
	if a.Kind == "test" {
		message.MonitorName = "Notification test"
		return message
	}
	message.MonitorType = templateField(m.Type, 512)
	message.MonitorTarget = monitorTemplateTarget(m)
	message.OpenedAt = time.UnixMilli(i.OpenedAt).UTC().Format(time.RFC3339)
	if a.Kind == "recovery" && i.ResolvedAt != nil {
		message.ResolvedAt = time.UnixMilli(*i.ResolvedAt).UTC().Format(time.RFC3339)
		message.Downtime = templateDowntime(i.OpenedAt, *i.ResolvedAt)
	}
	message.NodeResults = strings.Join(results, "\n")
	if len(message.NodeResults) > 16<<10 {
		message.NodeResults = message.NodeResults[:16<<10]
		for !utf8.ValidString(message.NodeResults) {
			message.NodeResults = message.NodeResults[:len(message.NodeResults)-1]
		}
	}
	return message
}

func discordConfigured(config providerDelivery.Config) bool {
	return config.Templates.Subject != "" || config.Templates.Text != "" || config.Webhook.Embed
}

// The internal envelope is never sent as a webhook event. It lets provider
// rendering fail as a recorded delivery attempt rather than skip scheduling.
func addNotificationTemplateFacts(body []byte, m Monitor, i Incident, a attempt, node Identity, observations []Sample, discordLegacy []byte) ([]byte, error) {
	var payload map[string]json.RawMessage
	if json.Unmarshal(body, &payload) != nil || payload == nil {
		return nil, errTemplateMessage
	}
	facts := notificationTemplateFacts(m, i, a, node, observations)
	encoded, err := json.Marshal(facts)
	if err != nil {
		return nil, errTemplateMessage
	}
	payload["templateFacts"] = encoded
	if len(discordLegacy) > 0 {
		payload["discordLegacy"] = json.RawMessage(discordLegacy)
	}
	eventTime := i.OpenedAt
	if a.Kind == "repeat" && a.LastAt > 0 {
		eventTime = a.LastAt
	}
	if a.Kind == "recovery" && i.ResolvedAt != nil {
		eventTime = *i.ResolvedAt
	}
	payload["eventTime"], _ = json.Marshal(time.UnixMilli(eventTime).UTC().Format(time.RFC3339))
	return json.Marshal(payload)
}

func discordTemplateEscape(value string) string {
	var out strings.Builder
	for _, r := range value {
		if strings.ContainsRune("\\*_~`|>#-[]()", r) {
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	return out.String()
}

func redactDiscordTemplateFact(value, webhook string) string {
	if webhook != "" {
		value = strings.ReplaceAll(value, webhook, "[redacted webhook]")
	}
	if parsed := discordWebhookURL(webhook); parsed != nil {
		secret := parsed.Path[strings.LastIndexByte(parsed.Path, '/')+1:]
		value = strings.ReplaceAll(value, secret, "[redacted]")
	}
	return value
}

func formatDiscordTemplates(config providerDelivery.Config, message providerDelivery.Message, legacy []byte, eventTime string) ([]byte, error) {
	var original struct {
		Content string `json:"content"`
	}
	if json.Unmarshal(legacy, &original) != nil {
		return nil, errTemplateMessage
	}
	raw := message
	for _, value := range []*string{&message.EventKind, &message.MonitorName, &message.MonitorID, &message.IncidentID, &message.ObservationsSummary, &message.IdempotencyKey, &message.Status, &message.MonitorType, &message.MonitorTarget, &message.OpenedAt, &message.ResolvedAt, &message.Downtime, &message.NodeResults, &message.SenderNode} {
		*value = discordTemplateEscape(redactDiscordTemplateFact(*value, config.Webhook.URL))
	}
	title, err := providerDelivery.RenderTextTemplate(config.Templates.Subject, message, providerDelivery.MaxBodyBytes)
	if err != nil {
		return nil, errTemplateMessage
	}
	text, err := providerDelivery.RenderTextTemplate(config.Templates.Text, message, providerDelivery.MaxBodyBytes)
	if err != nil {
		return nil, errTemplateMessage
	}
	payload := map[string]any{"allowed_mentions": map[string]any{"parse": []string{}}}
	if !config.Webhook.Embed {
		content := title
		if text != "" {
			if content != "" {
				content += "\n"
			}
			content += text
		}
		if content == "" || discordUnits(content) > 2000 {
			return nil, errTemplateMessage
		}
		payload["content"] = content
		return json.Marshal(payload)
	}
	if config.Templates.Subject == "" {
		title = raw.Status + " " + discordBoundText(templateField(redactDiscordTemplateFact(raw.MonitorName, config.Webhook.URL), 1024), 240)
	}
	if config.Templates.Text == "" {
		text = original.Content
	}
	if discordUnits(title) > 256 || discordUnits(text) > 4096 || title == "" && text == "" {
		return nil, errTemplateMessage
	}
	color := 0xD03B3B
	switch raw.EventKind {
	case "repeat":
		color = 0xFAB219
	case "recovery":
		color = 0x0CA30C
	case "test":
		color = 0x2A78D6
	}
	if _, err = time.Parse(time.RFC3339, eventTime); err != nil {
		return nil, errTemplateMessage
	}
	payload["content"] = ""
	payload["embeds"] = []any{map[string]any{"title": title, "description": text, "color": color, "timestamp": eventTime}}
	return json.Marshal(payload)
}

func discordUnits(value string) int {
	units := 0
	for _, r := range value {
		units++
		if r > 0xFFFF {
			units++
		}
	}
	return units
}
