package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
)

type DeliveryAttemptView struct {
	ID                   string `json:"id"`
	NodeID               string `json:"nodeId"`
	MonitorID            string `json:"monitorId,omitempty"`
	IncidentID           string `json:"incidentId,omitempty"`
	SourceIncidentID     string `json:"sourceIncidentId,omitempty"`
	SatisfiedByReceiptID string `json:"satisfiedByReceiptId,omitempty"`
	Kind                 string `json:"kind"`
	Slot                 int64  `json:"slot,omitempty"`
	DestinationID        string `json:"destinationId,omitempty"`
	ChannelID            string `json:"channelId,omitempty"`
	Provider             string `json:"provider"`
	State                string `json:"state"`
	Failures             int    `json:"failures"`
	NextAttemptAt        int64  `json:"nextAttemptAt,omitempty"`
	LastAttemptAt        int64  `json:"lastAttemptAt,omitempty"`
	HTTPStatus           int    `json:"httpStatus,omitempty"`
	ErrorCode            string `json:"errorCode,omitempty"`
	ProviderID           string `json:"providerId,omitempty"`
	Held                 bool   `json:"held,omitempty"`
}

func (n *Node) attemptView(a attempt) DeliveryAttemptView {
	v := DeliveryAttemptView{ID: a.ID, NodeID: n.ID, MonitorID: a.MonitorID, IncidentID: a.IncidentID, Kind: a.Kind, Slot: a.Slot, DestinationID: a.DestinationID, ChannelID: a.ChannelID, Provider: a.Provider, State: a.State, Failures: a.Failures, NextAttemptAt: a.NextAt, LastAttemptAt: a.LastAt, HTTPStatus: a.HTTPStatus, ErrorCode: a.ErrorCode, ProviderID: a.ProviderID, Held: a.Held}
	v.SourceIncidentID = a.IncidentID
	if v.State == "" {
		v.State = "queued"
	}
	if v.Provider == "" {
		v.Provider = "generic"
	}
	for _, i := range n.episodes {
		if i.MonitorID == a.MonitorID && slices.Contains(episodeIDs(i), a.IncidentID) {
			v.IncidentID = i.ID
			for _, r := range receiptsForTarget(n.forEpisode(i), a.DestinationID) {
				if r.Kind == a.Kind && (a.Kind != "repeat" || r.Slot >= a.Slot) && v.SatisfiedByReceiptID != a.ID {
					v.State, v.NextAttemptAt, v.ErrorCode, v.NodeID, v.Held = "accepted", 0, "", r.NodeID, false
					v.SatisfiedByReceiptID = r.ID
				}
			}
			if v.State != "accepted" && i.RetiredBy != "" {
				v.State, v.NextAttemptAt, v.Held = "cancelled", 0, false
			}
			if v.State != "accepted" && i.MaintenanceComplete && !hasReceipt(receiptsForTarget(n.forEpisode(i), a.DestinationID), "down") {
				v.State, v.NextAttemptAt, v.Held, v.ErrorCode = "cancelled", 0, false, "completed_during_maintenance"
			}
			if v.State != "accepted" && !hasReceipt(receiptsForTarget(n.forEpisode(i), a.DestinationID), "down") && n.targetSuppressionComplete(i, a.DestinationID) {
				v.State, v.NextAttemptAt, v.Held, v.ErrorCode = "cancelled", 0, false, completedDependency
			} else if completedDependencyAttempt(a) && v.State == "cancelled" && i.RetiredBy == "" && !i.MaintenanceComplete {
				v.State, v.ErrorCode = "queued", ""
			}
			break
		}
	}
	if v.State == "accepted" {
		v.NextAttemptAt = 0
	} else if v.Held {
		v.State, v.NextAttemptAt = "held", 0
	}
	if a.Kind != "test" && v.State != "accepted" && v.State != "cancelled" {
		m := n.current(a.MonitorID)
		available := m != nil && m.Enabled
		if available {
			_, available = n.targetForAttempt(*m, a)
		}
		if !available {
			v.State, v.NextAttemptAt, v.Held, v.ErrorCode = "unavailable", 0, false, "destination_unavailable"
		} else if len(n.activeMaintenance(*m)) != 0 {
			v.State, v.NextAttemptAt, v.Held = "held", 0, true
		}
	}
	return v
}

func (n *Node) deliveryAPI(r *http.Request) (int, any, error) {
	limit := 200
	if r.URL.Query().Has("limit") {
		var err error
		limit, err = strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil || limit < 1 || limit > 1000 {
			return 0, nil, fail(400, "limit must be between 1 and 1000")
		}
	}
	monitor, incident := r.URL.Query().Get("monitorId"), r.URL.Query().Get("incidentId")
	out := []DeliveryAttemptView{}
	for _, a := range n.attempts {
		v := n.attemptView(a)
		if monitor != "" && v.MonitorID != monitor || incident != "" && v.IncidentID != incident {
			continue
		}
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b DeliveryAttemptView) int {
		if a.LastAttemptAt != b.LastAttemptAt {
			if a.LastAttemptAt > b.LastAttemptAt {
				return -1
			}
			return 1
		}
		return stringsCompare(a.ID, b.ID)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return 200, out, nil
}

func (n *Node) startChannelTest(id string, b []byte) (int, any, error) {
	var in struct {
		Revision string `json:"revision"`
	}
	if json.Unmarshal(b, &in) != nil {
		return 0, nil, fail(400, "Invalid test request")
	}
	r := n.currentResource(resourceChannel, id)
	if r == nil || in.Revision != r.Revision {
		return 0, nil, fail(409, "Channel revision is stale, deleted or conflicting")
	}
	c := channelOf(*r, "admin")
	if !c.Enabled {
		return 0, nil, fail(409, "Enable the channel before testing it")
	}
	if n.pendingNotificationTests != 0 {
		return 0, nil, fail(429, "A notification test batch is still pending")
	}
	if n.lastNotificationTest != 0 && n.now() < n.lastNotificationTest+5000 {
		return 0, nil, fail(429, "Wait five seconds between notification tests")
	}
	targets := targetsForChannel(c)
	queued := []attempt{}
	for _, t := range targets {
		a := attempt{ID: newID(), Kind: "test", DestinationID: t.ID, ChannelID: id, TargetRevision: t.Revision, Provider: t.Provider, State: "queued", LastAt: n.now()}
		if !n.dbPut("attempts", a.ID, a) {
			return 0, nil, fail(503, "Persistence unavailable")
		}
		n.attempts[a.ID] = a
		queued = append(queued, a)
	}
	n.lastNotificationTest = n.now()
	n.pendingNotificationTests = len(queued)
	out := []DeliveryAttemptView{}
	type job struct {
		target  notificationTarget
		attempt attempt
	}
	jobs := make(chan job, len(queued))
	for index, t := range targets {
		a := queued[index]
		out = append(out, n.attemptView(a))
		jobs <- job{t, a}
	}
	close(jobs)
	// Test traffic has two dedicated workers and one bounded batch. Slow test
	// recipients cannot consume the eight workers reserved for incident alerts.
	for range min(2, len(queued)) {
		n.launchLocked(func() {
			for j := range jobs {
				n.sendChannelTest(c, j.target, j.attempt)
			}
		})
	}
	return 202, map[string]any{"attempts": out}, nil
}

func (n *Node) sendChannelTest(c Channel, t notificationTarget, a attempt) {
	defer func() {
		n.mu.Lock()
		n.pendingNotificationTests--
		n.mu.Unlock()
	}()
	if n.ctx.Err() != nil {
		return
	}
	n.mu.Lock()
	r := n.currentResource(resourceChannel, c.ID)
	if n.stopped || n.fault != nil || r == nil || r.Revision != c.Revision {
		if !n.stopped && n.fault == nil {
			a.State, a.ErrorCode = "cancelled", "channel_changed"
			if n.dbPut("attempts", a.ID, a) {
				n.attempts[a.ID] = a
			}
		}
		n.mu.Unlock()
		return
	}
	a.State, a.LastAt = "sending", n.now()
	if !n.dbPut("attempts", a.ID, a) {
		n.mu.Unlock()
		return
	}
	n.attempts[a.ID] = a
	openedAt := n.now()
	n.mu.Unlock()
	body, err := json.Marshal(map[string]any{"kind": "test", "channelId": c.ID, "monitor": Identity{Name: "Notification test"}, "node": Identity{n.ID, n.Name}, "observations": []Observation{}})
	if t.Provider == "discord" {
		body, err = formatDiscordNotification(Monitor{MonitorInput: MonitorInput{Name: c.Name, WebhookURL: t.URL}}, Incident{OpenedAt: openedAt}, a, Identity{n.ID, n.Name}, nil)
	}
	result := notificationDeliveryResult{ErrorCode: "invalid_message"}
	if err == nil {
		ctx, cancel := context.WithTimeout(n.ctx, n.webhookTimeout)
		result = n.deliverToTarget(ctx, t, body, a)
		cancel()
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stopped || n.fault != nil {
		return
	}
	a.State, a.HTTPStatus, a.ErrorCode, a.ProviderID = "failed", result.HTTPStatus, result.ErrorCode, result.ProviderID
	if result.Delivered {
		a.State, a.ErrorCode = "accepted", ""
	} else {
		a.Failures = 1
		if a.ErrorCode == "" {
			a.ErrorCode = "delivery_unconfirmed"
		}
	}
	if n.dbPut("attempts", a.ID, a) {
		n.attempts[a.ID] = a
	}
}
