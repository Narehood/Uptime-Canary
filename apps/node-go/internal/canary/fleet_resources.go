package canary

import (
	"encoding/json"
	"strings"
)

// Settings arrive only through an authenticated dashboard delegation and commit
// atomically with the monitor. Managed IDs never collide with native resources.
type fleetResourceInput struct {
	ID   string          `json:"id"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}
type fleetResourceReceipt struct {
	Owner string `json:"owner"`
	Hash  string `json:"hash"`
}

func (n *Node) prepareFleetResources(owner string, inputs []fleetResourceInput) (Snapshot, map[string]fleetResourceReceipt, map[string]bool, error) {
	snapshot := Snapshot{}
	receipts := map[string]fleetResourceReceipt{}
	known := map[string]bool{}
	if len(inputs) > 48 {
		return snapshot, nil, nil, fail(400, "Too many synced resources")
	}
	added := 0
	for _, in := range inputs {
		key := resourceKey(in.Kind, in.ID)
		if identifier(in.ID, "synced resource") != nil || !strings.HasPrefix(in.ID, "fleet-") || known[key] || (in.Kind != resourceChannel && in.Kind != resourceTag) {
			return snapshot, nil, nil, fail(400, "Invalid synced resource")
		}
		known[key] = true
		// Provider validation is identical to ordinary channel management.
		if in.Kind == resourceChannel {
			if _, err := parseChannel(in.Data); err != nil {
				return snapshot, nil, nil, err
			}
		}
		heads := n.resources[key]
		r := Resource{Kind: in.Kind, ID: in.ID, Revision: newID(), UpdatedAt: n.now(), Data: in.Data}
		if len(heads) != 0 {
			if len(heads) != 1 || heads[0].Resource.Deleted || !n.resourceVersionReady(heads[0]) {
				return snapshot, nil, nil, fail(409, "A synced resource has conflicting or deleted settings")
			}
			current := heads[0].Resource
			if equalJSON(json.RawMessage(current.Data), json.RawMessage(in.Data)) {
				continue
			}
			previous, ok, err := loadOne[fleetResourceReceipt](n.store, "fleetResourceReceipts", key)
			if err != nil {
				return snapshot, nil, nil, fail(503, "Cannot read synced resource ownership")
			}
			if !ok || previous.Owner != owner || previous.Hash != digest(string(current.Data)) {
				return snapshot, nil, nil, fail(409, "A synced resource was edited on this node; review it before replacing")
			}
		} else {
			added++
			if len(n.resources)+added > maxResources {
				return snapshot, nil, nil, fail(409, "Resource capacity exceeded")
			}
		}
		version, edges, err := n.joinResourceRevision(r, heads)
		if err != nil {
			return snapshot, nil, nil, err
		}
		if err = validateResource(version); err != nil {
			return snapshot, nil, nil, err
		}
		snapshot.Resources = append(snapshot.Resources, version)
		snapshot.RevisionEdges = append(snapshot.RevisionEdges, edges...)
		receipts[key] = fleetResourceReceipt{Owner: owner, Hash: digest(string(in.Data))}
	}
	return snapshot, receipts, known, nil
}
