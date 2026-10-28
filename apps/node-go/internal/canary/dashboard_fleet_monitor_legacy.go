package canary

import (
	"context"
	"encoding/json"
	"slices"
)

// The pre-fleet API generates IDs on POST and fences edits by revision. Retain
// a create's baseline and attempt before dispatch; recovery reads its result
// instead of issuing another POST. A non-unique result requires manual review.
type legacyMonitorAttempt struct {
	Sequence  int64    `json:"sequence"`
	Sent      bool     `json:"sent"`
	BeforeIDs []string `json:"beforeIds,omitempty"`
}

func (d *Dashboard) validateLegacyMonitor(ctx context.Context, row dashboardFleetRecord, plan dashboardFleetPlan, p dashboardFleetPlacement, staging bool) error {
	view, target, err := d.fleetRead(ctx, p.RegistrationID, row.ActorRole)
	if err != nil {
		return err
	}
	body, err := fleetCommandBytes(plan, p)
	if staging {
		body, err = legacyFleetPreflightBytes(plan, p)
	}
	if err != nil {
		return err
	}
	var command fleetPlacementCommand
	if err := json.Unmarshal(body, &command); err != nil {
		return err
	}
	var current *MonitorView
	for _, item := range view.Monitors {
		if item.Monitor.ID == p.MonitorID {
			copy := item
			current = &copy
			break
		}
	}
	newCopy := p.Sequence == 0 && !command.Adopt && p.MonitorID == "fleet-"+p.ID
	if newCopy {
		if current != nil || len(view.Monitors) >= maxMonitors {
			return fail(409, "The older node cannot admit a new monitor")
		}
	} else if current == nil {
		if command.Deleted {
			return nil
		}
		return fail(409, "The original monitor no longer exists on this node")
	} else if current.ConfigConflict || current.Monitor.Revision != command.Revision {
		return fail(409, "The original monitor changed; review its node revision before syncing")
	}
	if command.Deleted {
		return nil
	}
	known := map[string]bool{view.Node.ID: true}
	for _, node := range view.Nodes {
		known[node.ID] = true
	}
	input, err := parseMonitorWrite(command.Monitor, known)
	if err != nil {
		return err
	}
	if input.GroupID != "" {
		found := false
		for _, group := range view.Groups {
			found = found || group.Group.ID == input.GroupID && !group.ConfigConflict && !group.Group.Deleted
		}
		if !found {
			return fail(400, "The outage dependency group is missing on an older node; choose its local references")
		}
	}
	secret, err := d.openSecrets(target)
	if err != nil {
		return err
	}
	for _, kind := range []string{resourceChannel, resourceTag} {
		ids := slices.Clone(input.NotificationIDs)
		if kind == resourceTag {
			ids = nil
			for _, tag := range input.Tags {
				ids = append(ids, tag.TagID)
			}
		}
		if len(ids) == 0 {
			continue
		}
		settings, err := d.readLegacyFleetSettings(ctx, target, secret.token(row.ActorRole), kind)
		if err != nil {
			return err
		}
		for _, id := range ids {
			found := false
			for _, setting := range settings {
				found = found || setting.ID == id && !setting.Conflict
			}
			if !found {
				return fail(400, "A selected channel or tag is missing on an older node; review its references")
			}
		}
	}
	return nil
}

func (d *Dashboard) applyLegacyMonitor(ctx context.Context, row *dashboardFleetRecord, plan *dashboardFleetPlan, p *dashboardFleetPlacement, body []byte) (fleetPlacementResult, error) {
	// Serialize old API mutations with their resource staging. Another create
	// cannot overtake an uncertain one and make its generated ID ambiguous.
	d.fleetSettingsMu.Lock()
	defer d.fleetSettingsMu.Unlock()
	zero := fleetPlacementResult{}
	var command fleetPlacementCommand
	if err := json.Unmarshal(body, &command); err != nil {
		return zero, err
	}
	if plan.LegacyMonitorAttempts == nil {
		plan.LegacyMonitorAttempts = map[string]legacyMonitorAttempt{}
	}
	attempt := plan.LegacyMonitorAttempts[p.ID]
	if attempt.Sequence != command.Sequence {
		attempt = legacyMonitorAttempt{Sequence: command.Sequence}
	}
	unsent := func() {
		if plan.Unsent == nil {
			plan.Unsent = map[string]bool{}
		}
		plan.Unsent[p.ID] = true
	}
	view, target, err := d.fleetRead(ctx, p.RegistrationID, row.ActorRole)
	if err != nil {
		if !attempt.Sent {
			unsent()
		}
		return zero, err
	}
	secret, err := d.openSecrets(target)
	if err != nil {
		return zero, err
	}
	var input MonitorInput
	if !command.Deleted {
		known := map[string]bool{view.Node.ID: true}
		for _, node := range view.Nodes {
			if node.State != "unlisted" {
				known[node.ID] = true
			}
		}
		input, err = parseMonitorWrite(command.Monitor, known)
		if err != nil {
			if !attempt.Sent {
				unsent()
			}
			return zero, err
		}
	}
	result := func(monitor Monitor) (fleetPlacementResult, error) {
		p.MonitorID = monitor.ID
		if err := d.saveLegacyFleetProgress(*row, *plan); err != nil {
			return zero, err
		}
		return fleetPlacementResult{PlacementID: p.ID, MonitorID: monitor.ID, Sequence: command.Sequence, Revision: monitor.Revision, Deleted: command.Deleted}, nil
	}
	newCopy := p.Sequence == 0 && !command.Adopt && p.MonitorID == "fleet-"+p.ID
	if newCopy && !attempt.Sent {
		d.mu.Lock()
		waiting := false
		for _, other := range d.fleet {
			otherPlan, err := d.openFleet(other)
			if err != nil {
				continue
			}
			for _, placement := range other.Placements {
				if placement.ID != p.ID && placement.RegistrationID == p.RegistrationID && otherPlan.LegacyMonitors[placement.RegistrationID] && otherPlan.LegacyMonitorAttempts[placement.ID].Sent && placement.MonitorID == "fleet-"+placement.ID {
					waiting = true
				}
			}
		}
		d.mu.Unlock()
		if waiting {
			unsent()
			return zero, fail(502, "Waiting for this older node to confirm its earlier monitor creation before starting another")
		}
	}
	if newCopy && attempt.Sent {
		claimed := map[string]bool{}
		d.mu.Lock()
		for _, other := range d.fleet {
			for _, placement := range other.Placements {
				if placement.ID != p.ID && (placement.RegistrationID == p.RegistrationID || slices.Contains(placement.RegistrationIDs, p.RegistrationID)) {
					claimed[placement.MonitorID] = true
				}
			}
		}
		d.mu.Unlock()
		candidates := []Monitor{}
		for _, item := range view.Monitors {
			if !item.ConfigConflict && !claimed[item.Monitor.ID] && !slices.Contains(attempt.BeforeIDs, item.Monitor.ID) && equalJSON(item.Monitor.MonitorInput, input) {
				candidates = append(candidates, item.Monitor)
			}
		}
		if len(candidates) == 1 {
			return result(candidates[0])
		}
		return zero, fail(502, "An older node has not uniquely confirmed the new monitor; review its copies before retrying. The create will not be repeated")
	}
	var current *MonitorView
	for _, item := range view.Monitors {
		if item.Monitor.ID == p.MonitorID {
			copy := item
			current = &copy
			break
		}
	}
	if command.Deleted && current == nil {
		return result(Monitor{ID: p.MonitorID, Revision: p.Revision})
	}
	if !newCopy {
		if current == nil || current.ConfigConflict {
			if !attempt.Sent {
				unsent()
			}
			return zero, fail(409, "The monitor was removed or conflicted on its older node")
		}
		if current.Monitor.Revision != command.Revision {
			if attempt.Sent && !command.Deleted && equalJSON(current.Monitor.MonitorInput, input) {
				return result(current.Monitor)
			}
			if !attempt.Sent {
				unsent()
			}
			return zero, fail(409, "The monitor changed on its node; review the current revision before replacing it")
		}
	}
	if !d.fleetAuthorized(*row) {
		if !attempt.Sent {
			unsent()
		}
		return zero, fail(403, "Deployment authorization changed")
	}
	path, method := "/api/monitors", "POST"
	var value map[string]json.RawMessage
	if command.Deleted {
		value = map[string]json.RawMessage{}
	} else if err := json.Unmarshal(command.Monitor, &value); err != nil {
		return zero, err
	}
	if newCopy {
		attempt.BeforeIDs = nil
		for _, item := range view.Monitors {
			attempt.BeforeIDs = append(attempt.BeforeIDs, item.Monitor.ID)
		}
		// Ordinary POST would otherwise opt into the destination's defaults.
		value["notificationIds"], _ = json.Marshal(append([]string{}, input.NotificationIDs...))
	} else {
		path, method = path+"/"+p.MonitorID, "PUT"
		if command.Deleted {
			method = "DELETE"
		}
		value["revision"], _ = json.Marshal(command.Revision)
	}
	attempt.Sent = true
	plan.LegacyMonitorAttempts[p.ID] = attempt
	if err := d.saveLegacyFleetProgress(*row, *plan); err != nil {
		return zero, err
	}
	encoded, _ := json.Marshal(value)
	response, data, err := d.nodeRequest(ctx, target, secret.token(row.ActorRole), method, path, encoded)
	if err != nil {
		return zero, fail(502, "Waiting for an older node to confirm its saved monitor operation")
	}
	if response.StatusCode == 400 || response.StatusCode == 404 || response.StatusCode == 409 {
		attempt.Sent = false
		attempt.BeforeIDs = nil
		plan.LegacyMonitorAttempts[p.ID] = attempt
		unsent()
		return zero, fail(response.StatusCode, "An older node refused the monitor settings; review its supported options and current revision")
	}
	if command.Deleted && response.StatusCode == 204 {
		return result(Monitor{ID: p.MonitorID, Revision: p.Revision})
	}
	var saved Monitor
	if response.StatusCode != 200 && response.StatusCode != 201 || json.Unmarshal(data, &saved) != nil || identifier(saved.ID, "monitor ID") != nil || identifier(saved.Revision, "revision") != nil || saved.Deleted || !newCopy && saved.ID != p.MonitorID {
		return zero, fail(502, "An older node has not confirmed the saved monitor operation")
	}
	p.MonitorID = saved.ID
	if err := d.saveLegacyFleetProgress(*row, *plan); err != nil {
		return zero, err
	}
	if !equalJSON(saved.MonitorInput, input) {
		return zero, fail(409, "An older node returned different monitor settings; review that node before retrying")
	}
	return result(saved)
}

// Caller holds d.mu. Do not collect an unacknowledged generated ID as another
// native monitor while its original saved POST is being recovered.
func (d *Dashboard) groupWaitsForLegacyCreate(group string) bool {
	for _, row := range d.fleet {
		if row.State == "removed" || !slices.Contains(row.GroupIDs, group) {
			continue
		}
		plan, err := d.openFleet(row)
		if err != nil {
			continue
		}
		for _, p := range row.Placements {
			attempt := plan.LegacyMonitorAttempts[p.ID]
			if plan.LegacyMonitors[p.RegistrationID] && attempt.Sent && p.MonitorID == "fleet-"+p.ID {
				return true
			}
		}
	}
	return false
}
