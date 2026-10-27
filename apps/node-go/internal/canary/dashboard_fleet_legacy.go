package canary

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// Older placement APIs cannot atomically bundle resources. Keep their generated
// IDs and write intent in the encrypted plan; an uncertain create is read back,
// never blindly POSTed again. Reused native settings are never overwritten.
type dashboardLegacyResource struct {
	ID          string `json:"id,omitempty"`
	Revision    string `json:"revision,omitempty"`
	Hash        string `json:"hash,omitempty"`
	Owned       bool   `json:"owned,omitempty"`
	NativeReuse bool   `json:"nativeReuse,omitempty"`
	PendingHash string `json:"pendingHash,omitempty"`
}

func legacyFleetResourceKey(registration string, resource fleetResourceInput) string {
	return registration + "/" + resourceKey(resource.Kind, resource.ID)
}

func legacyFleetPreflightBytes(plan dashboardFleetPlan, p dashboardFleetPlacement) ([]byte, error) {
	body, err := fleetCommandBytes(plan, p)
	if err != nil {
		return nil, err
	}
	var command fleetPlacementCommand
	if err := json.Unmarshal(body, &command); err != nil {
		return nil, err
	}
	if command.Deleted {
		return body, nil
	}
	var input MonitorInput
	if err := json.Unmarshal(command.Monitor, &input); err != nil {
		return nil, err
	}
	for _, resource := range plan.Resources {
		ids := []string{resource.ID, plan.LegacyResources[legacyFleetResourceKey(p.RegistrationID, resource)].ID}
		if resource.Kind == resourceChannel {
			input.NotificationIDs = slices.DeleteFunc(input.NotificationIDs, func(id string) bool { return slices.Contains(ids, id) })
		} else {
			input.Tags = slices.DeleteFunc(input.Tags, func(tag MonitorTag) bool { return slices.Contains(ids, tag.TagID) })
		}
	}
	command.Resources = nil
	command.Monitor, err = json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return json.Marshal(command)
}

type legacyFleetSetting struct {
	ID, Revision, Hash string
	Conflict           bool
}

func (d *Dashboard) readLegacyFleetSettings(ctx context.Context, target registeredNodeRecord, token, kind string) ([]legacyFleetSetting, error) {
	path := "/api/tags"
	if kind == resourceChannel {
		path = "/api/notification-channels"
	}
	response, body, err := d.nodeRequest(ctx, target, token, "GET", path, nil)
	if err != nil || response.StatusCode != 200 {
		return nil, fail(502, "Waiting to read notification settings on a node")
	}
	settings := []legacyFleetSetting{}
	if kind == resourceChannel {
		var values []ChannelView
		if json.Unmarshal(body, &values) != nil || len(values) > maxResources {
			return nil, fail(502, "Invalid notification settings response")
		}
		for _, value := range values {
			data, _ := json.Marshal(value.Channel.ChannelInput)
			settings = append(settings, legacyFleetSetting{value.Channel.ID, value.Channel.Revision, digest(string(data)), value.ConfigConflict || value.Channel.Deleted})
		}
	} else {
		var values []TagView
		if json.Unmarshal(body, &values) != nil || len(values) > maxResources {
			return nil, fail(502, "Invalid tag settings response")
		}
		for _, value := range values {
			data, _ := json.Marshal(value.Tag.TagInput)
			settings = append(settings, legacyFleetSetting{value.Tag.ID, value.Tag.Revision, digest(string(data)), value.ConfigConflict || value.Tag.Deleted})
		}
	}
	for _, setting := range settings {
		if identifier(setting.ID, "resource ID") != nil || identifier(setting.Revision, "revision") != nil {
			return nil, fail(502, "Invalid copied notification identity")
		}
	}
	slices.SortFunc(settings, func(a, b legacyFleetSetting) int { return stringsCompare(a.ID, b.ID) })
	return settings, nil
}

func (d *Dashboard) saveLegacyFleetProgress(row dashboardFleetRecord, plan dashboardFleetPlan) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fleet[row.ID].Revision != row.Revision || !d.fleetAuthorized(row) {
		return fail(409, "Deployment changed while copying notification settings")
	}
	return d.saveFleet(nil, AccountView{}, row, plan)
}

func (d *Dashboard) prepareLegacyFleetResources(ctx context.Context, row dashboardFleetRecord, plan *dashboardFleetPlan, placements []dashboardFleetPlacement) error {
	if plan.Deleted || len(plan.Resources) == 0 || len(plan.LegacyRegistrations) == 0 {
		return nil
	}
	d.fleetSettingsMu.Lock()
	defer d.fleetSettingsMu.Unlock()
	for _, p := range placements {
		if !plan.LegacyRegistrations[p.RegistrationID] || len(p.NodeIDs) == 0 {
			continue
		}
		input, err := fleetReferencedInput(*plan, p)
		if err != nil {
			return err
		}
		d.mu.Lock()
		target, exists := d.registry[p.RegistrationID]
		d.mu.Unlock()
		if !exists {
			return fail(409, "A notification destination was removed")
		}
		secrets, err := d.openSecrets(target)
		if err != nil {
			return err
		}
		for _, resource := range plan.Resources {
			used := resource.Kind == resourceChannel && slices.Contains(input.NotificationIDs, resource.ID)
			for _, tag := range input.Tags {
				used = used || resource.Kind == resourceTag && tag.TagID == resource.ID
			}
			if !used {
				continue
			}
			if err := d.copyLegacyFleetResource(ctx, row, plan, target, secrets.token(row.ActorRole), resource); err != nil {
				return err
			}
		}
	}
	return nil
}

func (d *Dashboard) copyLegacyFleetResource(ctx context.Context, row dashboardFleetRecord, plan *dashboardFleetPlan, target registeredNodeRecord, token string, resource fleetResourceInput) error {
	key := legacyFleetResourceKey(target.ID, resource)
	entry := plan.LegacyResources[key]
	settings, err := d.readLegacyFleetSettings(ctx, target, token, resource.Kind)
	if err != nil {
		return err
	}
	wanted := digest(string(resource.Data))
	claimed := map[string]bool{}
	proofs := []dashboardLegacyResource{}
	prefix := target.ID + "/" + resource.Kind + "/"
	collect := func(otherPlan dashboardFleetPlan) {
		for otherKey, assigned := range otherPlan.LegacyResources {
			if otherKey != key && strings.HasPrefix(otherKey, prefix) && assigned.ID != "" {
				claimed[assigned.ID] = true
			}
		}
		proof := otherPlan.LegacyResources[key]
		if proof.Owned {
			proofs = append(proofs, proof)
		}
		// A later consumer follows the same canonical copy even when the source
		// has changed. The checks below still fence updates by revision and hash.
		if entry.ID == "" && proof.ID != "" {
			entry = proof
			plan.LegacyResources[key] = entry
		}
		// Share another consumer's uncertain create instead of POSTing again.
		if entry.ID == "" && entry.PendingHash == "" && proof.ID == "" && proof.PendingHash != "" {
			entry.PendingHash = proof.PendingHash
			plan.LegacyResources[key] = entry
		}
	}
	collect(*plan)
	d.mu.Lock()
	for _, other := range d.fleet {
		if other.State == "removed" {
			continue
		}
		if otherPlan, err := d.openFleet(other); err == nil {
			collect(otherPlan)
		}
	}
	d.mu.Unlock()
	if entry.PendingHash != "" && entry.ID == "" {
		for _, setting := range settings {
			if !setting.Conflict && !claimed[setting.ID] && setting.Hash == entry.PendingHash {
				entry.ID, entry.Revision, entry.Hash, entry.Owned, entry.PendingHash = setting.ID, setting.Revision, setting.Hash, false, ""
				break
			}
		}
		if entry.ID == "" {
			return fail(502, "Waiting for an older node to confirm copied notification settings; its create request will not be repeated")
		}
	}
	var current *legacyFleetSetting
	for _, setting := range settings {
		if setting.ID == entry.ID {
			copy := setting
			current = &copy
			break
		}
	}
	if entry.ID != "" && (current == nil || current.Conflict) {
		return fail(409, "Copied notification settings were removed or conflicted on a node; review them before syncing")
	}
	if current != nil {
		previouslyOwned := entry.Owned
		entry.Owned = entry.Owned && entry.Revision == current.Revision && entry.Hash == current.Hash
		// Several monitors can consume one copied channel. Their encrypted
		// journals prove dashboard ownership without adopting a native resource
		// by name, and keep a lagging consumer from duplicating a source edit.
		for _, proof := range proofs {
			if proof.ID == entry.ID && proof.Owned && proof.Hash == current.Hash && proof.Revision == current.Revision {
				entry.Hash, entry.Owned, entry.NativeReuse = current.Hash, true, false
				break
			}
		}
		if previouslyOwned && !entry.Owned && current.Hash != wanted {
			return fail(409, "Copied notification settings were edited on this node; review them before replacing")
		}
		entry.Revision = current.Revision
	}
	if current != nil && current.Hash == wanted {
		entry.Hash, entry.PendingHash = wanted, ""
		plan.LegacyResources[key] = entry
		return d.saveLegacyFleetProgress(row, *plan)
	}
	if current != nil && entry.Owned && current.Hash != entry.Hash {
		return fail(409, "Copied notification settings were edited on this node; review them before replacing")
	}
	if current == nil || !entry.Owned {
		for _, setting := range settings {
			if !setting.Conflict && !claimed[setting.ID] && setting.Hash == wanted {
				plan.LegacyResources[key] = dashboardLegacyResource{ID: setting.ID, Revision: setting.Revision, Hash: wanted, NativeReuse: true}
				// Re-enter with the stable local ID so ownership shared by
				// another monitor is retained in this consumer's journal too.
				return d.copyLegacyFleetResource(ctx, row, plan, target, token, resource)
			}
		}
		entry = dashboardLegacyResource{}
	}
	entry.PendingHash = wanted
	plan.LegacyResources[key] = entry
	if err := d.saveLegacyFleetProgress(row, *plan); err != nil {
		return err
	}
	path, method := "/api/tags", "POST"
	if resource.Kind == resourceChannel {
		path = "/api/notification-channels"
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(resource.Data, &body) != nil {
		return fail(409, "Cannot copy invalid notification settings")
	}
	if current != nil && entry.Owned {
		path, method = path+"/"+entry.ID, "PUT"
		body["revision"], _ = json.Marshal(current.Revision)
	}
	encoded, _ := json.Marshal(body)
	response, result, err := d.nodeRequest(ctx, target, token, method, path, encoded)
	if err == nil && (response.StatusCode == 400 || response.StatusCode == 404 || response.StatusCode == 409) {
		entry.PendingHash = ""
		plan.LegacyResources[key] = entry
		if err := d.saveLegacyFleetProgress(row, *plan); err != nil {
			return err
		}
		return fail(response.StatusCode, "A node cannot accept the selected notification or tag settings; review its supported providers and local edits")
	}
	if err != nil || response.StatusCode != 200 && response.StatusCode != 201 {
		return fail(502, "Waiting for an older node to confirm copied notification settings")
	}
	var saved struct {
		ID       string `json:"id"`
		Revision string `json:"revision"`
	}
	if json.Unmarshal(result, &saved) != nil || identifier(saved.ID, "resource ID") != nil || identifier(saved.Revision, "revision") != nil || method == "PUT" && saved.ID != entry.ID {
		return fail(502, "Invalid copied notification acknowledgement")
	}
	entry.ID, entry.Revision, entry.Hash, entry.Owned, entry.PendingHash = saved.ID, saved.Revision, wanted, true, ""
	plan.LegacyResources[key] = entry
	return d.saveLegacyFleetProgress(row, *plan)
}
