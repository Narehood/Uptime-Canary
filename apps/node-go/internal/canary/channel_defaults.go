package canary

import (
	"encoding/json"
	"slices"
)

// Caller holds n.mu. Omitted notificationIds on a new monitor chooses the
// current enabled defaults. Explicit [] and a legacy webhook remain explicit.
func (n *Node) defaultChannelIDs() ([]string, error) {
	out := []string{}
	for _, heads := range n.resources {
		candidate := false
		for _, head := range heads {
			if head.Resource.Kind != resourceChannel || head.Resource.Deleted {
				continue
			}
			channel := channelOf(head.Resource, "admin")
			candidate = candidate || channel.IsDefault && channel.Enabled
		}
		if !candidate {
			continue
		}
		if len(heads) != 1 || !n.resourceVersionReady(heads[0]) {
			return nil, fail(409, "Resolve default notification channel conflicts before creating a monitor, or choose notificationIds explicitly")
		}
		out = append(out, heads[0].Resource.ID)
	}
	if len(out) > 16 {
		return nil, fail(409, "More than sixteen default notification channels are enabled; choose notificationIds explicitly")
	}
	slices.Sort(out)
	return out, nil
}

func (n *Node) applyChannel(id string, raw []byte) (int, any, error) {
	object, err := strictResourceObject(raw, "revision", "monitors")
	if err != nil || required(object, "revision", "monitors") != nil {
		return 0, nil, fail(400, "Invalid channel application request")
	}
	var selectedObjects []json.RawMessage
	if json.Unmarshal(object["monitors"], &selectedObjects) != nil {
		return 0, nil, fail(400, "Invalid monitor selection")
	}
	for _, selected := range selectedObjects {
		fields, err := strictResourceObject(selected, "id", "revision")
		if err != nil || required(fields, "id", "revision") != nil {
			return 0, nil, fail(400, "Invalid monitor selection")
		}
	}
	var input struct {
		Revision string `json:"revision"`
		Monitors []struct {
			ID       string `json:"id"`
			Revision string `json:"revision"`
		} `json:"monitors"`
	}
	if json.Unmarshal(raw, &input) != nil || input.Revision == "" || len(input.Monitors) < 1 || len(input.Monitors) > maxMonitors {
		return 0, nil, fail(400, "Name a channel revision and one to two hundred monitor revisions")
	}
	channel := n.currentResource(resourceChannel, id)
	if channel == nil || channel.Revision != input.Revision || !channelOf(*channel, "admin").Enabled {
		return 0, nil, fail(409, "Channel is stale, disabled or conflicting")
	}
	seen := map[string]bool{}
	versions, edges := []Version{}, []RevisionEdge{}
	changed := []string{}
	for _, selected := range input.Monitors {
		if seen[selected.ID] {
			return 0, nil, fail(400, "Duplicate monitor selection")
		}
		seen[selected.ID] = true
		monitor := n.current(selected.ID)
		if monitor == nil || monitor.Revision != selected.Revision {
			return 0, nil, fail(409, "A selected monitor is stale, deleted or conflicting; refresh before applying")
		}
		if slices.Contains(monitor.NotificationIDs, id) {
			continue
		}
		if len(monitor.NotificationIDs) >= 16 {
			return 0, nil, fail(409, "A selected monitor already has sixteen notification channels")
		}
		candidate := *monitor
		candidate.NotificationIDs = append(slices.Clone(monitor.NotificationIDs), id)
		slices.Sort(candidate.NotificationIDs)
		candidate.Revision, candidate.UpdatedAt = newID(), n.now()
		if err := n.validateReferences(&candidate, nil); err != nil {
			return 0, nil, err
		}
		version, proof, err := n.joinMonitorRevision(candidate, n.heads[selected.ID])
		if err != nil {
			return 0, nil, err
		}
		versions, edges = append(versions, version), append(edges, proof...)
		changed = append(changed, selected.ID)
	}
	// One projection and one transaction. A stale member or commit failure
	// cannot leave a partially applied batch or erase an existing destination.
	if len(versions) > 0 {
		if err := n.applySnapshot(Snapshot{Versions: versions, RevisionEdges: edges}); err != nil {
			return 0, nil, fail(503, "Persistence unavailable")
		}
	}
	slices.Sort(changed)
	return 200, map[string]any{"changedIds": changed}, nil
}
