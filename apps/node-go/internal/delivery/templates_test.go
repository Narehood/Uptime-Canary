package delivery

import (
	"strings"
	"testing"
)

func TestEnhancedTemplateFieldsSubstituteOnceWithoutInventingFallbacks(t *testing.T) {
	m := Message{EventKind: "recovery", MonitorName: "{{status}}", MonitorID: "monitor", IncidentID: "incident", ObservationsSummary: "legacy", IdempotencyKey: "key", Status: "UP", MonitorType: "http", MonitorTarget: "https://example.test", OpenedAt: "2026-01-01T00:00:00Z", ResolvedAt: "2026-01-01T00:07:12Z", Downtime: "7m 12s", NodeResults: "NY: up (27 ms)", SenderNode: "Cloud"}
	template := "{{event_kind}}|{{monitor_name}}|{{monitor_id}}|{{incident_id}}|{{observations_summary}}|{{idempotency_key}}|{{status}}|{{monitor_type}}|{{monitor_target}}|{{opened_at}}|{{resolved_at}}|{{downtime}}|{{node_results}}|{{sender_node}}"
	want := "recovery|{{status}}|monitor|incident|legacy|key|UP|http|https://example.test|2026-01-01T00:00:00Z|2026-01-01T00:07:12Z|7m 12s|NY: up (27 ms)|Cloud"
	got, err := RenderTextTemplate(template, m, MaxBodyBytes)
	if err != nil || got != want {
		t.Fatal("fields missing or substituted recursively", got, err)
	}
	if got, err = RenderTextTemplate("", m, MaxBodyBytes); err != nil || got != "" {
		t.Fatal("custom template invented a fallback")
	}
	if _, err = RenderTextTemplate(strings.Repeat("{{node_results}}", 5000), m, MaxBodyBytes); err == nil {
		t.Fatal("expansion bound bypassed")
	}
}

func TestWebhookPayloadDoesNotRenderUnusedTemplates(t *testing.T) {
	c := Config{Kind: Webhook, Webhook: WebhookConfig{URL: "https://example.test/webhook"}, Templates: Templates{Text: strings.Repeat("{{monitor_name}}", 1000)}}
	// Stay within the existing 256-byte monitor-name contract while making
	// template expansion exceed the body bound if it were incorrectly rendered.
	m := Message{MonitorName: strings.Repeat("m", 256), APIBody: []byte(`{"kind":"test"}`)}
	prepared, err := prepareMessage(c, m)
	if err != nil || string(prepared.APIBody) != string(m.APIBody) {
		t.Fatal("unused webhook template broke JSON delivery", err)
	}
}
