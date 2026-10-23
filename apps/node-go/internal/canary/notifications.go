package canary

import (
	"context"
	"slices"
)

func (n *Node) scheduleNotification(m Monitor, observations []Sample) {
	for _, target := range n.notificationTargets(m) {
		n.scheduleTargetNotification(m, observations, target)
	}
}

func (n *Node) scheduleTargetNotification(m Monitor, observations []Sample, target notificationTarget) {
	records := []Incident{}
	for _, i := range n.episodes {
		if i.MonitorID == m.ID && i.Revision == checkGeneration(m) && i.RetiredBy == "" {
			records = append(records, i)
		}
	}
	for _, i := range records {
		if !n.deliveryEpisodeEligible(m, i, observations) {
			continue
		}
		successes := receiptsForTarget(n.forEpisode(i), target.ID)
		superseded := false
		for _, other := range records {
			if other.ID != i.ID && intersects(other.Predecessors, episodeIDs(i)) {
				superseded = true
				break
			}
		}
		if superseded && !n.owedClosedLocalDown(i, target.ID, successes) {
			continue
		}
		maintenanceHeld := len(n.activeMaintenance(m)) != 0
		var down *Receipt
		for _, r := range successes {
			if r.Kind == "down" {
				copy := r
				down = &copy
				break
			}
		}
		if i.Closed != nil && down == nil && !n.hasDeliveryIntent(i, target.ID) {
			continue
		}
		groupID, reason, grace := n.suppressionForTarget(m, target)
		releasedAt := int64(0)
		if target.ChannelID != "" || maintenanceHeld || i.MaintenanceStartedAt != nil {
			releasedAt = n.deliveryHold(m, i, target, groupID != "" || maintenanceHeld)
		}
		if groupID != "" {
			if i.SuppressedByGroupID == "" || i.SuppressedAt == nil || i.SuppressionReleasedAt != nil && *i.SuppressionReleasedAt >= *i.SuppressedAt {
				i.SuppressedAt = Ptr(n.now())
				i.SuppressedByGroupID = groupID
				i.SuppressionReason = reason
			}
			if i.Closed != nil && !hasReceipt(n.forEpisode(i), "down") {
				i.SuppressedComplete = true
			}
			n.saveEpisode(i)
		} else if i.SuppressedAt != nil && (i.SuppressionReleasedAt == nil || *i.SuppressionReleasedAt < *i.SuppressedAt) {
			i.SuppressionReleasedAt = Ptr(max(n.now(), *i.SuppressedAt))
			n.saveEpisode(i)
		}
		if (i.SuppressedComplete || i.MaintenanceComplete) && down == nil {
			continue
		}
		if maintenanceHeld {
			continue
		}
		kind := ""
		slot := int64(0)
		due := int64(0)
		if down == nil {
			if i.Closed == nil {
				evidence := false
				for _, s := range observations {
					if s.State == "down" && s.DownSince != nil && slices.Contains(i.Runs[s.NodeID], *s.DownSince) {
						evidence = true
					}
				}
				evidence = evidence || n.owedLocalBranchDown(i, target.ID, observations)
				if !evidence {
					continue
				}
			}
			// Grouped short episodes which finish before delivery do not replay a
			// stale outage/recovery pair after the dependency's grace period.
			if i.Closed != nil && (i.SuppressedByGroupID != "" || grace > 0) {
				i.SuppressedComplete = true
				n.saveEpisode(i)
				continue
			}
			kind = "down"
			due = i.OpenedAt + n.settings.NotificationDelayMs
		} else if i.Closed != nil {
			if hasReceipt(successes, "recovery") || len(observations) != len(m.NodeIDs) {
				continue
			}
			allUp := true
			for _, s := range observations {
				allUp = allUp && s.State == "up"
			}
			if !allUp {
				continue
			}
			kind = "recovery"
			due = *i.ResolvedAt + n.settings.NotificationDelayMs
		} else {
			repeat := int64(0)
			if m.RepeatAfterMs != nil {
				repeat = *m.RepeatAfterMs
			}
			if repeat == 0 {
				continue
			}
			hasDown := false
			for _, s := range observations {
				hasDown = hasDown || s.State == "down"
			}
			if !hasDown {
				continue
			}
			latest := int64(0)
			slot = 1
			for _, r := range successes {
				if r.Kind == "down" || r.Kind == "repeat" {
					latest = max(latest, r.SentAt)
				}
				if r.Kind == "repeat" {
					slot = max(slot, r.Slot+1)
				}
			}
			kind = "repeat"
			due = latest + repeat
			// Slots advance from delivery evidence rather than dividing by the
			// editable repeat interval. Lengthening that interval must not make
			// every future repeat collide with an already-delivered old slot.
		}
		if kind != "recovery" {
			if groupID != "" {
				continue
			}
			due = max(due, i.OpenedAt+grace)
			due = max(due, releasedAt)
			if target.ChannelID == "" && i.SuppressionReleasedAt != nil {
				// A long suppressed outage has already passed its opening-based
				// sender windows. Stagger anew at release so connected candidates
				// can learn the preferred sender's receipt before failing over.
				due = max(due, *i.SuppressionReleasedAt)
			}
		}
		due = max(due, releasedAt)
		candidates := append([]string{}, m.NodeIDs...)
		slices.Sort(candidates)
		rank := slices.Index(candidates, n.ID)
		if rank < 0 {
			continue
		}
		stagger := max(100, n.settings.NotificationDelayMs, n.settings.SyncIntervalMs*3+n.settings.PeerTimeoutMs)
		due += int64(rank) * stagger
		if n.now() < due {
			continue
		}
		inflight := m.ID + "/" + kind
		if target.ID != "" {
			inflight += "/" + target.ID
		}
		if n.sending[inflight] {
			continue
		}
		ids := episodeIDs(i)
		var a *attempt
		for _, at := range n.attempts {
			if at.MonitorID == m.ID && at.DestinationID == target.ID && slices.Contains(ids, at.IncidentID) && at.Kind == kind && at.Slot == slot {
				copy := at
				a = &copy
				break
			}
		}
		if a == nil {
			identity := i.ID
			if down != nil {
				// A newly learned sibling can change the canonical alias after
				// delivery. Follow-ups retain the identity actually delivered.
				identity = down.IncidentID
			}
			value := attempt{ID: attemptIdentity(m.ID, identity, kind, slot, target.ID), MonitorID: m.ID, IncidentID: identity, Kind: kind, Slot: slot, NextAt: due, DestinationID: target.ID, ChannelID: target.ChannelID, TargetRevision: target.Revision, Provider: target.Provider, State: "queued"}
			if !n.dbPut("attempts", value.ID, value) {
				return
			}
			n.attempts[value.ID] = value
			a = &value
		}
		if n.now() < a.NextAt {
			continue
		}
		a.TargetRevision = target.Revision
		body, err := n.notificationBody(m, i, *a, observations)
		if err != nil {
			continue
		}
		select {
		case n.notificationWorkers <- struct{}{}:
		default:
			continue
		}
		n.sending[inflight] = true
		at := *a
		incident := i
		n.launchLocked(func() {
			defer func() { <-n.notificationWorkers }()
			n.sendNotification(m, incident, at, inflight, body)
		})
	}
}
func (n *Node) notificationBody(m Monitor, i Incident, a attempt, observations []Sample) ([]byte, error) {
	// Retries retain the persisted attempt identity even when reconciliation
	// changes the canonical ID. Every payload includes the current full aliases.
	target, ok := n.targetForAttempt(m, a)
	if !ok {
		return nil, fail(409, "Notification destination is unavailable")
	}
	m.WebhookURL = target.URL
	provider := notificationGeneric
	if target.Provider == "discord" {
		provider = notificationDiscord
	}
	return provider.format(m, i, a, Identity{n.ID, n.Name}, observations)
}
func publicSamples(samples []Sample) []Observation {
	out := make([]Observation, 0, len(samples))
	for _, s := range samples {
		out = append(out, s.Observation)
	}
	return out
}
func (n *Node) sendNotification(m Monitor, _ Incident, a attempt, inflight string, body []byte) {
	// Recheck under the state lock immediately before I/O. Edits and replicated
	// dependency evidence invalidate work that has not yet reached the provider.
	n.mu.Lock()
	current := n.current(m.ID)
	target, targetExists := n.targetForAttempt(m, a)
	valid := !n.stopped && n.fault == nil && current != nil && current.Enabled && current.Revision == m.Revision && targetExists && target.Revision == a.TargetRevision
	var latest Incident
	found := false
	// The captured projection can split while this job is queued. Only the
	// attempt's stable source identity may select its current lifetime; old
	// sibling aliases must not borrow another lifetime's empty receipt set.
	for _, episode := range n.episodes {
		if episode.MonitorID == m.ID && episode.Revision == checkGeneration(m) && slices.Contains(episodeIDs(episode), a.IncidentID) {
			latest = episode
			found = true
			break
		}
	}
	valid = valid && found && latest.RetiredBy == "" && len(n.activeMaintenance(m)) == 0 && n.deliveryEpisodeEligible(m, latest, n.fresh(m))
	// A queued worker can span a hold/release transition after its original
	// eligibility check. Reapply the current release deadline before provider I/O.
	releasedAt := int64(0)
	for _, pending := range n.attempts {
		if pending.MonitorID == m.ID && pending.DestinationID == a.DestinationID && pending.Kind == "down" && slices.Contains(episodeIDs(latest), pending.IncidentID) {
			releasedAt = max(releasedAt, pending.ReleasedAt)
		}
	}
	if a.Kind != "recovery" && a.DestinationID == "" && latest.SuppressionReleasedAt != nil {
		releasedAt = max(releasedAt, *latest.SuppressionReleasedAt)
	}
	if releasedAt != 0 {
		candidates := append([]string{}, m.NodeIDs...)
		slices.Sort(candidates)
		rank := slices.Index(candidates, n.ID)
		stagger := max(100, n.settings.NotificationDelayMs, n.settings.SyncIntervalMs*3+n.settings.PeerTimeoutMs)
		valid = valid && rank >= 0 && n.now() >= releasedAt+int64(rank)*stagger
	}
	successes := receiptsForTarget(n.forEpisode(latest), a.DestinationID)
	observations := n.fresh(m)
	for _, episode := range n.episodes {
		if episode.MonitorID == m.ID && episode.Revision == checkGeneration(m) && intersects(episode.Predecessors, episodeIDs(latest)) && !(a.Kind == "down" && n.owedClosedLocalDown(latest, a.DestinationID, successes)) {
			valid = false
		}
	}
	if a.Kind != "recovery" {
		group, _, grace := n.suppressionForTarget(m, target)
		valid = valid && group == ""
		valid = valid && n.now() >= latest.OpenedAt+grace
		if (latest.SuppressedComplete || latest.MaintenanceComplete) && !hasReceipt(successes, "down") {
			valid = false
		}
		if latest.Closed == nil {
			evidence := false
			for _, s := range observations {
				evidence = evidence || s.State == "down" && s.DownSince != nil && slices.Contains(latest.Runs[s.NodeID], *s.DownSince)
			}
			evidence = evidence || a.Kind == "down" && n.owedLocalBranchDown(latest, a.DestinationID, observations)
			valid = valid && evidence
		} else if a.Kind == "repeat" || latest.SuppressedByGroupID != "" || grace > 0 || !n.hasDeliveryIntent(latest, a.DestinationID) {
			valid = false
		}
		if a.Kind == "repeat" && !hasReceipt(successes, "down") {
			valid = false
		}
	} else {
		valid = valid && latest.Closed != nil && hasReceipt(successes, "down") && len(observations) == len(m.NodeIDs)
		for _, s := range observations {
			valid = valid && s.State == "up"
		}
	}
	for _, receipt := range successes {
		if receipt.Kind == a.Kind && (a.Kind != "repeat" || receipt.Slot >= a.Slot) {
			valid = false
		}
	}
	if valid {
		var err error
		body, err = n.notificationBody(m, latest, a, observations)
		valid = err == nil
		if valid {
			a.LastAt, a.State = n.now(), "sending"
			valid = n.dbPut("attempts", a.ID, a)
			if valid {
				n.attempts[a.ID] = a
			}
		}
	}
	n.mu.Unlock()
	result := notificationDeliveryResult{}
	if valid {
		ctx, cancel := context.WithTimeout(n.ctx, n.webhookTimeout)
		defer cancel()
		result = n.deliverToTarget(ctx, target, body, a)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.sending, inflight)
	if !valid || n.stopped || n.fault != nil {
		return
	}
	if result.Delivered {
		r := Receipt{NotificationView{ID: a.ID, MonitorID: m.ID, IncidentID: a.IncidentID, Kind: a.Kind, NodeID: n.ID, SentAt: n.now(), DestinationID: a.DestinationID, ChannelID: a.ChannelID, Provider: target.Provider}, a.Slot}
		if !n.dbPut("receipts", r.ID, r) {
			return
		}
		n.receipts[r.ID] = r
		a.State, a.HTTPStatus, a.ErrorCode, a.ProviderID = "accepted", result.HTTPStatus, "", result.ProviderID
		if n.dbPut("attempts", a.ID, a) {
			n.attempts[a.ID] = a
		}
		n.nextSync = 0
		if !n.syncing {
			n.startSyncLocked()
		}
		return
	}
	a.Failures++
	a.State, a.HTTPStatus, a.ErrorCode = "retrying", result.HTTPStatus, result.ErrorCode
	if a.ErrorCode == "" {
		a.ErrorCode = "delivery_unconfirmed"
	}
	backoff := int64(1000) << ((a.Failures - 1) % 5)
	if a.Failures%5 == 0 {
		backoff = 60000
	}
	a.NextAt = n.now() + min(30000, backoff)
	if a.Failures%5 == 0 {
		a.NextAt = n.now() + 60000
	}
	a.NextAt = max(a.NextAt, n.now()+result.RetryAfterMs)
	if n.dbPut("attempts", a.ID, a) {
		n.attempts[a.ID] = a
	}
}
