package canary

import (
	"encoding/json"
	"maps"
	"slices"
)

const resourceChannel = "notification-channel"
const maxResources = 512

// Replicated resources use the same explicit conflict and tombstone semantics
// as monitors. Local credentials and delivery attempts never enter this DAG.
type Resource struct {
	Kind      string          `json:"kind"`
	ID        string          `json:"id"`
	Revision  string          `json:"revision"`
	UpdatedAt int64           `json:"updatedAt"`
	Deleted   bool            `json:"deleted,omitempty"`
	Data      json.RawMessage `json:"data"`
}

type ResourceVersion struct {
	Resource  Resource `json:"resource"`
	Ancestors []string `json:"ancestors"`
	EdgeID    string   `json:"edgeId,omitempty"`
}

func resourceKey(kind, id string) string { return kind + "/" + id }

func validateResource(v ResourceVersion) error {
	r := v.Resource
	if v.EdgeID != "" && (!validCommitment(v.EdgeID) || len(v.Ancestors) > maxClusterMembers) {
		return fail(400, "Invalid replicated resource")
	}
	if identifier(r.ID, "id") != nil || identifier(r.Revision, "revision") != nil || !validTime(r.UpdatedAt) || !validIDs(v.Ancestors, 10000) || slices.Contains(v.Ancestors, r.Revision) || len(r.Data) > maxAPIBody {
		return fail(400, "Invalid replicated resource")
	}
	switch r.Kind {
	case resourceChannel:
		_, err := parseChannel(r.Data)
		return err
	case resourceMaintenance:
		_, err := parseMaintenance(r.Data)
		return err
	case resourceStatusPage:
		_, err := parseStatusPage(r.Data)
		return err
	case resourceTag:
		_, err := parseTag(r.Data)
		return err
	default:
		return fail(400, "Unsupported replicated resource")
	}
}

func mergeResources(local map[string][]ResourceVersion, incoming []ResourceVersion, graphs ...*revisionGraph) (map[string][]ResourceVersion, error) {
	out := maps.Clone(local)
	grouped := map[string][]ResourceVersion{}
	if len(graphs) > 0 {
		for id, vs := range local {
			grouped[id] = append([]ResourceVersion{}, vs...)
		}
	}
	for _, v := range incoming {
		key := resourceKey(v.Resource.Kind, v.Resource.ID)
		if _, ok := grouped[key]; !ok {
			grouped[key] = append([]ResourceVersion{}, local[key]...)
		}
		grouped[key] = append(grouped[key], v)
	}
	for key, versions := range grouped {
		byRevision := map[string]ResourceVersion{}
		for _, v := range versions {
			if old, ok := byRevision[v.Resource.Revision]; ok && !equalJSON(old, v) {
				return nil, fail(400, "Immutable resource revision collision")
			}
			byRevision[v.Resource.Revision] = v
		}
		heads := []ResourceVersion{}
		for rev, v := range byRevision {
			dominated := false
			for _, other := range byRevision {
				if other.Resource.Revision == rev {
					continue
				}
				if len(graphs) > 0 {
					dominated = dominated || graphs[0].reaches(other.Resource.Kind, other.Resource.ID, other.Resource.Revision, rev)
				} else {
					dominated = dominated || slices.Contains(other.Ancestors, rev)
				}
			}
			if !dominated {
				heads = append(heads, v)
			}
		}
		if len(heads) == 0 || len(graphs) == 0 && len(heads) > 32 {
			return nil, fail(400, "Invalid resource revision graph")
		}
		slices.SortFunc(heads, func(a, b ResourceVersion) int { return stringsCompare(a.Resource.Revision, b.Resource.Revision) })
		out[key] = heads
	}
	if len(out) > maxResources*maxClusterMembers {
		return nil, fail(409, "Resource capacity exceeded")
	}
	return out, nil
}

func (n *Node) currentResource(kind, id string) *Resource {
	h := n.resources[resourceKey(kind, id)]
	if len(h) != 1 || h[0].Resource.Deleted {
		return nil
	}
	if !n.resourceVersionReady(h[0]) {
		return nil
	}
	r := h[0].Resource
	return &r
}

func (n *Node) writeResource(kind, id string, data any, deleted bool, prepared ...compiledMaintenance) (Resource, error) {
	if id == "" {
		id = newID()
	}
	key := resourceKey(kind, id)
	b, err := json.Marshal(data)
	if err != nil {
		return Resource{}, fail(400, "Invalid resource data")
	}
	r := Resource{Kind: kind, ID: id, Revision: newID(), UpdatedAt: n.now(), Deleted: deleted, Data: b}
	v, edges, err := n.joinResourceRevision(r, n.resources[key])
	if err != nil {
		return Resource{}, err
	}
	r = v.Resource
	h := []ResourceVersion{v}
	if err = validateResource(h[0]); err != nil {
		return Resource{}, err
	}
	candidate := maps.Clone(n.resources)
	candidate[key] = h
	var plans compiledMaintenance
	if len(prepared) > 0 {
		plans = prepared[0]
	}
	cache, err := n.maintenancePlansFor(candidate, plans)
	if err != nil {
		return Resource{}, err
	}
	if err = n.applySnapshot(Snapshot{Resources: h, RevisionEdges: edges, preparedMaintenance: cache}); err != nil {
		return Resource{}, err
	}
	n.nextSync = 0
	return r, nil
}
