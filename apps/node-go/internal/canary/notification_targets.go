package canary

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	providerDelivery "uptime-canary/apps/node-go/internal/delivery"
)

type notificationTarget struct {
	ID, ChannelID, Revision, Provider, URL string
	Config                                 providerDelivery.Config
}

// Connection pools are shared; every caller supplies its own bounded context.
var channelDeliveryEngine = providerDelivery.New(providerDelivery.Options{Timeout: providerDelivery.MaxTimeout})

func targetsForChannel(c Channel) []notificationTarget {
	config := c.Config
	base := notificationTarget{ChannelID: c.ID, Revision: c.Revision, Provider: string(config.Kind), URL: config.Webhook.URL, Config: config}
	// A destination identity follows the route, not the secret used to reach it.
	// Rotating credentials retains receipts; routing to a new address does not.
	route := string(config.Kind)
	switch config.Kind {
	case "discord":
		if u := discordWebhookURL(config.Webhook.URL); u != nil {
			parts := strings.Split(u.Path, "/")
			query := u.Query()
			query.Del("wait")
			route += "\n" + parts[len(parts)-2] + "?" + query.Encode()
		}
	case providerDelivery.Webhook:
		route += "\n" + config.Webhook.URL
	case providerDelivery.SMTP:
		route += fmt.Sprintf("\n%s:%d", strings.ToLower(config.SMTP.Server), config.SMTP.Port)
	case providerDelivery.Mailgun:
		route += "\n" + config.Mailgun.Domain + "\n" + config.Mailgun.Region + "\n" + config.Mailgun.BaseURL
	case providerDelivery.Brevo:
		route += "\n" + config.Brevo.BaseURL
	case providerDelivery.Mandrill:
		route += "\n" + config.Mandrill.BaseURL
	case providerDelivery.Slack, providerDelivery.Telegram, providerDelivery.Ntfy, providerDelivery.Gotify, providerDelivery.Pushover:
		identity, err := providerDelivery.MessagingIdentity(config)
		if err != nil {
			return nil
		}
		route += "\n" + identity
	}
	if config.Kind == "discord" || config.Kind == providerDelivery.Webhook || providerDelivery.IsMessaging(config.Kind) {
		base.ID = digest("destination\x00" + c.ID + "\x00" + route)
		return []notificationTarget{base}
	}
	out := []notificationTarget{}
	for _, recipient := range config.Recipients {
		t := base
		t.Config.Recipients = []providerDelivery.Address{recipient}
		t.ID = digest("destination\x00" + c.ID + "\x00" + route + "\n" + strings.ToLower(config.Sender.Email) + "\n" + strings.ToLower(recipient.Email))
		out = append(out, t)
	}
	return out
}

func (n *Node) notificationTargets(m Monitor) []notificationTarget {
	out := []notificationTarget{}
	if m.WebhookURL != "" {
		out = append(out, notificationTarget{Provider: string(notificationProviderForURL(m.WebhookURL)), URL: m.WebhookURL})
	}
	for _, id := range m.NotificationIDs {
		if h := n.resources[resourceKey(resourceChannel, id)]; len(h) == 1 && !h[0].Resource.Deleted && n.resourceVersionReady(h[0]) {
			c := channelOf(h[0].Resource, "admin")
			if c.Enabled {
				out = append(out, targetsForChannel(c)...)
			}
		}
	}
	return out
}

func (n *Node) targetForAttempt(m Monitor, a attempt) (notificationTarget, bool) {
	for _, t := range n.notificationTargets(m) {
		if t.ID == a.DestinationID {
			return t, true
		}
	}
	return notificationTarget{}, false
}

func receiptsForTarget(receipts []Receipt, id string) []Receipt {
	out := []Receipt{}
	for _, r := range receipts {
		if r.DestinationID == id {
			out = append(out, r)
		}
	}
	return out
}

func (n *Node) deliverToTarget(ctx context.Context, t notificationTarget, body []byte, a attempt) notificationDeliveryResult {
	if t.ChannelID == "" || t.Provider == "discord" {
		return deliverNotification(ctx, n.client, t.URL, body, a.ID, n.now())
	}
	var payload struct {
		Monitor      Identity      `json:"monitor"`
		Observations []Observation `json:"observations"`
	}
	_ = json.Unmarshal(body, &payload)
	lines := []string{}
	for _, o := range payload.Observations {
		lines = append(lines, o.NodeID+": "+o.State+" - "+o.Message)
	}
	message := providerDelivery.Message{EventKind: a.Kind, MonitorName: payload.Monitor.Name, MonitorID: a.MonitorID, IncidentID: a.IncidentID, ObservationsSummary: strings.Join(lines, "\n"), IdempotencyKey: a.ID}
	if t.Config.Kind == providerDelivery.Webhook {
		message.APIBody = body
	}
	result := channelDeliveryEngine.Send(ctx, t.Config, message)
	return notificationDeliveryResult{Delivered: result.Accepted, RetryAfterMs: min(notificationRetryLimitMs, max(0, result.RetryAfter.Milliseconds())), HTTPStatus: result.HTTPStatus, ErrorCode: result.ErrorCode, ProviderID: result.ProviderID}
}

// Destination-specific dependency receipts prevent a working Discord route
// from silencing an independently failing email route on a dependent monitor.
func (n *Node) suppressionForTarget(m Monitor, t notificationTarget) (string, string, int64) {
	group, reason, grace := n.baseSuppression(m)
	if group == "" || t.ChannelID == "" || reason == "Waiting for outage dependency grace period" {
		return group, reason, grace
	}
	g := n.currentGroup(group)
	if g == nil {
		return "", "", grace
	}
	p := n.current(g.OutageMonitorID)
	if p == nil {
		return "", "", grace
	}
	for _, parentTarget := range n.notificationTargets(*p) {
		if parentTarget.ID != t.ID {
			continue
		}
		for _, i := range n.episodes {
			if i.MonitorID != p.ID || i.Revision != checkGeneration(*p) || i.Closed != nil {
				continue
			}
			for _, s := range n.fresh(*p) {
				if s.State == "down" && s.DownSince != nil && slices.Contains(i.Runs[s.NodeID], *s.DownSince) && hasReceipt(receiptsForTarget(n.forEpisode(i), t.ID), "down") {
					return group, reason, grace
				}
			}
		}
	}
	return "", "", grace
}

// Shared adapters acknowledge provider acceptance. Inbox delivery and remote
// idempotency support remain properties of the configured provider.
func (n *Node) suppression(m Monitor) (string, string, int64) {
	group, reason, grace := n.baseSuppression(m)
	if group == "" {
		return group, reason, grace
	}
	for _, t := range n.notificationTargets(m) {
		if held, _, _ := n.suppressionForTarget(m, t); held == "" {
			return "", "Some notification destinations are not covered by successful dependency delivery", grace
		}
	}
	return group, reason, grace
}

func attemptIdentity(monitor, incident, kind string, slot int64, destination string) string {
	key := fmt.Sprintf("%s/%s/%s/%d", monitor, incident, kind, slot)
	if destination != "" {
		key += "/" + destination
	}
	return digest(key)
}

// Hold transitions are local scheduling state per destination. They never
// authorize delivery or replace the replicated successful-send receipts.
func (n *Node) deliveryHold(m Monitor, i Incident, t notificationTarget, held bool) int64 {
	ids := episodeIDs(i)
	var a attempt
	found := false
	for _, candidate := range n.attempts {
		if candidate.MonitorID == m.ID && candidate.DestinationID == t.ID && candidate.Kind == "down" && slices.Contains(ids, candidate.IncidentID) {
			a, found = candidate, true
			break
		}
	}
	if !found {
		a = attempt{ID: attemptIdentity(m.ID, i.ID, "down", 0, t.ID), MonitorID: m.ID, IncidentID: i.ID, Kind: "down", DestinationID: t.ID, ChannelID: t.ChannelID, TargetRevision: t.Revision, Provider: t.Provider, State: "queued"}
	}
	previous := a
	if completedDependencyAttempt(a) {
		if a.SuppressionBoundary == dependencyBoundary(i) && i.Closed != nil {
			return a.ReleasedAt
		}
		// New evidence can extend or reopen a previously completed lifetime.
		// Its old local cancellation cannot discard that newly owed work.
		a.State, a.ErrorCode, a.SuppressionBoundary = "queued", "", ""
	}
	if a.Held && !held {
		a.ReleasedAt = n.now()
	}
	a.Held = held
	if !found || !equalJSON(previous, a) {
		if n.dbPut("attempts", a.ID, a) {
			n.attempts[a.ID] = a
		}
	}
	return a.ReleasedAt
}

const completedDependency = "completed_during_dependency"

func completedDependencyAttempt(a attempt) bool {
	return a.State == "cancelled" && a.ErrorCode == completedDependency
}

// Peer up-observation frontiers can grow without extending an outage. Keep
// resolution changes strict: historical dependency timing is otherwise unknown.
func dependencyBoundary(i Incident) string {
	return contentID(struct {
		Monitor, Revision string
		Opened            int64
		Resolved          *int64
		Runs              map[string][]int64
	}{i.MonitorID, i.Revision, i.OpenedAt, i.ResolvedAt, i.Runs})
}

// Legacy incident-wide metadata cannot establish which named route was held.
// Missing local, destination-specific completion evidence therefore fails open.
func (n *Node) targetSuppressionComplete(i Incident, destination string) bool {
	if destination == "" {
		return i.SuppressedComplete
	}
	if i.Closed == nil {
		return false
	}
	boundary := dependencyBoundary(i)
	for _, a := range n.attempts {
		if a.MonitorID == i.MonitorID && a.DestinationID == destination && a.Kind == "down" &&
			slices.Contains(episodeIDs(i), a.IncidentID) && completedDependencyAttempt(a) && a.SuppressionBoundary == boundary {
			return true
		}
	}
	return false
}

// Called only at an observed closure, never to infer an old episode's coverage
// from a dependency that became healthy/delivered at a later time.
func (n *Node) completeDependencySuppressions(m Monitor, i Incident) {
	if i.Closed == nil || i.ResolvedAt == nil {
		return
	}
	// An interrupted local source can close a larger reconciled episode. Bind
	// completion to that resulting lifetime, including every observer's runs.
	var projection *Incident
	for _, candidate := range n.episodes {
		if candidate.MonitorID == i.MonitorID && candidate.Revision == i.Revision && slices.Contains(episodeIDs(candidate), i.ID) {
			if projection != nil || candidate.Closed == nil || candidate.ResolvedAt == nil {
				return // Ambiguous or still-open history cannot complete delivery.
			}
			copy := candidate
			projection = &copy
		}
	}
	if projection == nil {
		return
	}
	i = *projection
	for _, target := range n.notificationTargets(m) {
		if target.ChannelID == "" || hasReceipt(receiptsForTarget(n.forEpisode(i), target.ID), "down") {
			continue
		}
		group, reason, grace := n.suppressionForTarget(m, target)
		covered := group != "" && reason != "Waiting for outage dependency grace period"
		insideGrace := grace > 0 && *i.ResolvedAt < i.OpenedAt+grace
		if !covered && !insideGrace {
			continue
		}
		found := false
		for id, a := range n.attempts {
			if a.MonitorID != m.ID || a.DestinationID != target.ID || a.Kind != "down" || !slices.Contains(episodeIDs(i), a.IncidentID) {
				continue
			}
			found = true
			a.State, a.ErrorCode, a.Held, a.NextAt = "cancelled", completedDependency, false, 0
			a.SuppressionBoundary = dependencyBoundary(i)
			if !n.dbPut("attempts", id, a) {
				return
			}
			n.attempts[id] = a
		}
		if !found {
			a := attempt{ID: attemptIdentity(m.ID, i.ID, "down", 0, target.ID), MonitorID: m.ID, IncidentID: i.ID,
				Kind: "down", DestinationID: target.ID, ChannelID: target.ChannelID, TargetRevision: target.Revision,
				Provider: target.Provider, State: "cancelled", ErrorCode: completedDependency, SuppressionBoundary: dependencyBoundary(i)}
			if !n.dbPut("attempts", a.ID, a) {
				return
			}
			n.attempts[a.ID] = a
		}
	}
}
