package canary

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// Caller holds d.mu. Older definitions predate an explicit resource source;
// their inherited placement is evidence of where the base reference IDs worked.
func (d *Dashboard) fleetNotificationSource(row dashboardFleetRecord, plan dashboardFleetPlan, fallback string) string {
	if plan.NotificationSourceRegistrationID != "" {
		return plan.NotificationSourceRegistrationID
	}
	for _, p := range row.Placements {
		if p.State == "removed" {
			continue
		}
		for _, id := range p.RegistrationIDs {
			ref := plan.References[id]
			if ref.NotificationIDs == nil && ref.Tags == nil {
				if _, ok := d.registry[id]; ok {
					return id
				}
			}
		}
	}
	if row.Source != nil {
		if _, ok := d.registry[row.Source.RegistrationID]; ok {
			return row.Source.RegistrationID
		}
	}
	return fallback
}

// Source IDs are namespaced by proved node identity, never by a display name.
func fleetResourceID(nodeID, kind, id string) string {
	if len(id) == len("fleet-")+32 && id[:6] == "fleet-" {
		return id
	}
	return "fleet-" + digest(nodeID + "\x00" + kind + "\x00" + id)[:32]
}

func (d *Dashboard) prepareFleetSettings(ctx context.Context, row dashboardFleetRecord, plan *dashboardFleetPlan) error {
	if len(plan.Monitor.NotificationIDs) == 0 && len(plan.Monitor.Tags) == 0 {
		plan.Resources = nil
		plan.ResourceIDs = nil
		plan.NativeResourceIDs = nil
		return nil
	}
	id := plan.NotificationSourceRegistrationID
	view, target, err := d.fleetRead(ctx, id, row.ActorRole)
	if err != nil {
		if target.ID == "" {
			return fail(409, "The notification source is no longer registered; choose its replacement")
		}
		return fail(502, "The notification source cannot be read yet")
	}
	secret, err := d.openSecrets(target)
	if err != nil {
		return err
	}
	resources := []fleetResourceInput{}
	mappings := map[string]string{}
	native := map[string]map[string]string{}
	rememberNative := func(source, kind, input, original string) {
		if native[source] == nil {
			native[source] = map[string]string{}
		}
		native[source][resourceKey(kind, input)] = original
	}
	if len(plan.Monitor.NotificationIDs) > 0 {
		response, b, err := d.nodeRequest(ctx, target, secret.token(row.ActorRole), "GET", "/api/notification-channels", nil)
		var channels []ChannelView
		if err != nil || response.StatusCode != 200 || json.Unmarshal(b, &channels) != nil || len(channels) > maxResources {
			return fail(502, "Notification channels cannot be read yet")
		}
		for _, id := range plan.Monitor.NotificationIDs {
			var found *Channel
			for _, channel := range channels {
				if channel.Channel.ID == id && !channel.Channel.Deleted && !channel.ConfigConflict {
					copy := channel.Channel
					found = &copy
					break
				}
			}
			if found == nil {
				return fail(409, "A selected notification channel is missing or conflicted on its source node")
			}
			data, _ := json.Marshal(found.ChannelInput)
			origin, originalID := plan.NotificationSourceRegistrationID, id
			mapped := fleetResourceID(view.Node.ID, resourceChannel, id)
			if source, original, canonical := d.canonicalFleetResource(*plan, resourceChannel, id); source != "" {
				origin, originalID = source, original
				mapped = canonical
				data, err = d.readCanonicalFleetResource(ctx, row, source, resourceChannel, original)
				if err != nil {
					return err
				}
			}
			rememberNative(origin, resourceChannel, id, originalID)
			mappings[resourceKey(resourceChannel, id)] = mapped
			resources = append(resources, fleetResourceInput{ID: mapped, Kind: resourceChannel, Data: data})
		}
	}
	if len(plan.Monitor.Tags) > 0 {
		response, b, err := d.nodeRequest(ctx, target, secret.token(row.ActorRole), "GET", "/api/tags", nil)
		var tags []TagView
		if err != nil || response.StatusCode != 200 || json.Unmarshal(b, &tags) != nil || len(tags) > maxResources {
			return fail(502, "Tags cannot be read yet")
		}
		for _, selected := range plan.Monitor.Tags {
			var found *Tag
			for _, tag := range tags {
				if tag.Tag.ID == selected.TagID && !tag.Tag.Deleted && !tag.ConfigConflict {
					copy := tag.Tag
					found = &copy
					break
				}
			}
			if found == nil {
				return fail(409, "A selected tag is missing or conflicted on its source node")
			}
			data, _ := json.Marshal(found.TagInput)
			origin, originalID := plan.NotificationSourceRegistrationID, selected.TagID
			mapped := fleetResourceID(view.Node.ID, resourceTag, selected.TagID)
			if source, original, canonical := d.canonicalFleetResource(*plan, resourceTag, selected.TagID); source != "" {
				origin, originalID = source, original
				mapped = canonical
				data, err = d.readCanonicalFleetResource(ctx, row, source, resourceTag, original)
				if err != nil {
					return err
				}
			}
			rememberNative(origin, resourceTag, selected.TagID, originalID)
			mappings[resourceKey(resourceTag, selected.TagID)] = mapped
			resources = append(resources, fleetResourceInput{ID: mapped, Kind: resourceTag, Data: data})
		}
	}
	slices.SortFunc(resources, func(a, b fleetResourceInput) int {
		return stringsCompare(resourceKey(a.Kind, a.ID), resourceKey(b.Kind, b.ID))
	})
	plan.Resources, plan.ResourceIDs = resources, mappings
	plan.NativeResourceIDs = native
	return nil
}

// A default channel on a destination may itself be a synced copy. Follow its
// recorded origin so a second monitor cannot push an older copy back upstream.
func (d *Dashboard) canonicalFleetResource(consumer dashboardFleetPlan, kind, id string) (string, string, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	origin := func(source, original string) (string, string, string) {
		return source, original, fleetResourceID(d.registry[source].NodeID, kind, original)
	}
	for registration, ids := range consumer.NativeResourceIDs {
		if original := ids[resourceKey(kind, id)]; original != "" && (original != id || registration != consumer.NotificationSourceRegistrationID) {
			return origin(registration, original)
		}
	}
	for _, row := range d.fleet {
		if row.State == "removed" {
			continue
		}
		plan, err := d.openFleet(row)
		if err != nil || !plan.SyncNotifications {
			continue
		}
		mappedID := id
		if !strings.HasPrefix(id, "fleet-") {
			// Ordinary resource IDs belong to one registration. Resolve that
			// node's saved copy before consulting its canonical source mapping.
			prefix := consumer.NotificationSourceRegistrationID + "/" + kind + "/"
			mappedID = ""
			for key, receipt := range plan.LegacyResources {
				if receipt.ID == id && !receipt.NativeReuse && strings.HasPrefix(key, prefix) {
					mappedID = strings.TrimPrefix(key, prefix)
					break
				}
			}
			if mappedID == "" {
				continue
			}
		}
		for registration, ids := range plan.NativeResourceIDs {
			for key, original := range ids {
				if original != "" && strings.HasPrefix(key, kind+"/") && (key == resourceKey(kind, mappedID) || plan.ResourceIDs[key] == mappedID) {
					return origin(registration, original)
				}
			}
		}
		for key, mapped := range plan.ResourceIDs {
			if mapped == mappedID && strings.HasPrefix(key, kind+"/") {
				original := strings.TrimPrefix(key, kind+"/")
				if original != mappedID {
					return origin(plan.NotificationSourceRegistrationID, original)
				}
			}
		}
	}
	return "", "", ""
}
func (d *Dashboard) readCanonicalFleetResource(ctx context.Context, row dashboardFleetRecord, source, kind, id string) (json.RawMessage, error) {
	_, target, err := d.fleetRead(ctx, source, row.ActorRole)
	if err != nil {
		if target.ID == "" {
			return nil, fail(409, "The original notification or tag source is no longer registered; choose its replacement")
		}
		return nil, fail(502, "The original notification or tag source cannot be read yet")
	}
	secret, err := d.openSecrets(target)
	if err != nil {
		return nil, err
	}
	path := "/api/tags/" + id
	if kind == resourceChannel {
		path = "/api/notification-channels/" + id
	}
	response, b, err := d.nodeRequest(ctx, target, secret.token(row.ActorRole), "GET", path, nil)
	if err != nil || response.StatusCode != 200 {
		return nil, fail(409, "The original notification channel or tag needs review on its source node")
	}
	if kind == resourceChannel {
		var channel Channel
		if json.Unmarshal(b, &channel) != nil || channel.ID != id || channel.Deleted {
			return nil, fail(502, "Invalid notification source")
		}
		return json.Marshal(channel.ChannelInput)
	}
	var tag Tag
	if json.Unmarshal(b, &tag) != nil || tag.ID != id || tag.Deleted {
		return nil, fail(502, "Invalid tag source")
	}
	return json.Marshal(tag.TagInput)
}
