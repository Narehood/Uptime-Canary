package canary

import (
	"encoding/json"
	"slices"
)

// These builders only construct a resolution. Callers retain their ordinary
// input/reference validation, hold n.mu, and atomically apply the returned final
// version together with every returned edge. Intermediate payloads are the
// chosen configuration with each edge's revision (and monitor generation).
// They need no mutable head records: retained edges commit their exact hashes.
func (n *Node) joinMonitorRevision(chosen Monitor, previous []Version) (Version, []RevisionEdge, error) {
	parents, err := revisionJoinParents(previous, n.heads[chosen.ID],
		func(v Version) string { return v.Monitor.Revision },
		func(v Version) bool { return v.Monitor.ID == chosen.ID }, n.monitorVersionReady)
	if err != nil {
		return Version{}, nil, err
	}
	if err = n.revisionJoinIdentity("monitor", chosen.ID, chosen.Revision, chosen.UpdatedAt, parents, chosen); err != nil {
		return Version{}, nil, err
	}
	generation := ""
	if !chosen.Deleted && len(previous) > 0 {
		generation = checkGeneration(previous[0].Monitor)
		for _, v := range previous {
			if v.Monitor.Deleted || !sameCheck(chosen.MonitorInput, v.Monitor.MonitorInput) || checkGeneration(v.Monitor) != generation || chosen.Type == "push" && v.Monitor.PushStartedAt != previous[0].Monitor.PushStartedAt {
				generation = ""
				break
			}
		}
	}
	chosen.PushStartedAt = 0
	if chosen.Type == "push" {
		chosen.PushStartedAt = chosen.UpdatedAt
		if generation != "" {
			chosen.PushStartedAt = previous[0].Monitor.PushStartedAt
		}
	}
	var final Monitor
	edges := n.buildRevisionJoins("monitor", chosen.ID, chosen.Revision, parents, func(revision string) (any, string) {
		m := chosen
		m.Revision, m.CheckGeneration = revision, generation
		if generation == "" {
			// The first join begins the chosen check. All later joins inherit
			// this root, even though only the final payload remains a head.
			generation = revision
		}
		final = m
		return m, checkGeneration(m)
	})
	e := edges[len(edges)-1]
	return Version{Monitor: final, Ancestors: e.Parents, EdgeID: e.ID}, edges, nil
}

func (n *Node) joinGroupRevision(chosen Group, previous []GroupVersion) (GroupVersion, []RevisionEdge, error) {
	parents, err := revisionJoinParents(previous, n.groups[chosen.ID],
		func(v GroupVersion) string { return v.Group.Revision },
		func(v GroupVersion) bool { return v.Group.ID == chosen.ID }, n.groupVersionReady)
	if err != nil {
		return GroupVersion{}, nil, err
	}
	if err = n.revisionJoinIdentity("group", chosen.ID, chosen.Revision, chosen.UpdatedAt, parents, chosen); err != nil {
		return GroupVersion{}, nil, err
	}
	edges := n.buildRevisionJoins("group", chosen.ID, chosen.Revision, parents, func(revision string) (any, string) {
		g := chosen
		g.Revision = revision
		return g, ""
	})
	e := edges[len(edges)-1]
	return GroupVersion{Group: chosen, Ancestors: e.Parents, EdgeID: e.ID}, edges, nil
}

func (n *Node) joinResourceRevision(chosen Resource, previous []ResourceVersion) (ResourceVersion, []RevisionEdge, error) {
	if chosen.Kind != resourceChannel && chosen.Kind != resourceMaintenance && chosen.Kind != resourceStatusPage && chosen.Kind != resourceTag {
		return ResourceVersion{}, nil, fail(400, "Unsupported replicated resource")
	}
	parents, err := revisionJoinParents(previous, n.resources[resourceKey(chosen.Kind, chosen.ID)],
		func(v ResourceVersion) string { return v.Resource.Revision },
		func(v ResourceVersion) bool { return v.Resource.Kind == chosen.Kind && v.Resource.ID == chosen.ID }, n.resourceVersionReady)
	if err != nil {
		return ResourceVersion{}, nil, err
	}
	if err = n.revisionJoinIdentity(chosen.Kind, chosen.ID, chosen.Revision, chosen.UpdatedAt, parents, chosen); err != nil {
		return ResourceVersion{}, nil, err
	}
	edges := n.buildRevisionJoins(chosen.Kind, chosen.ID, chosen.Revision, parents, func(revision string) (any, string) {
		r := chosen
		r.Revision = revision
		return r, ""
	})
	e := edges[len(edges)-1]
	return ResourceVersion{Resource: chosen, Ancestors: e.Parents, EdgeID: e.ID}, edges, nil
}

// A bounded representation must still resolve the complete observed vote set.
// Comparing full versions also prevents a same-revision payload substitution.
func revisionJoinParents[T any](previous, observed []T, revision func(T) string, sameScope func(T) bool, ready func(T) bool) ([]string, error) {
	byRevision := map[string]T{}
	for _, v := range previous {
		rev := revision(v)
		if identifier(rev, "parent revision") != nil || !sameScope(v) {
			return nil, fail(400, "Invalid resolution parent")
		}
		if old, exists := byRevision[rev]; exists {
			if !equalJSON(old, v) {
				return nil, fail(400, "Immutable revision collision")
			}
			return nil, fail(409, "Resolution must name every current head exactly once")
		}
		byRevision[rev] = v
	}
	if len(byRevision) != len(observed) {
		return nil, fail(409, "Configuration heads changed; reload all current heads")
	}
	for _, v := range observed {
		old, exists := byRevision[revision(v)]
		if !exists || !equalJSON(old, v) {
			return nil, fail(409, "Configuration heads changed; reload all current heads")
		}
	}
	parents := []string{}
	for _, v := range previous {
		if !ready(v) {
			return nil, fail(409, "Revision evidence is still synchronizing")
		}
		parents = append(parents, revision(v))
	}
	slices.Sort(parents)
	return parents, nil
}

func (n *Node) revisionJoinIdentity(kind, entity, revision string, updatedAt int64, parents []string, payload any) error {
	if identifier(entity, "id") != nil || identifier(revision, "revision") != nil || !validTime(updatedAt) {
		return fail(400, "Invalid resolution identity")
	}
	if slices.Contains(parents, revision) {
		return fail(400, "Immutable revision collision")
	}
	if n.graph != nil {
		if _, exists := n.graph.byRevision[revisionKey(kind, entity, revision)]; exists {
			return fail(400, "Immutable revision collision")
		}
	}
	if _, err := json.Marshal(payload); err != nil {
		return fail(400, "Invalid resolution payload")
	}
	return nil
}

func (n *Node) buildRevisionJoins(kind, entity, finalRevision string, parents []string, payload func(string) (any, string)) []RevisionEdge {
	reserved := map[string]bool{finalRevision: true}
	for _, parent := range parents {
		reserved[parent] = true
	}
	freshRevision := func() string {
		for {
			rev := newID()
			known := false
			if n.graph != nil {
				_, known = n.graph.byRevision[revisionKey(kind, entity, rev)]
			}
			if !reserved[rev] && !known {
				reserved[rev] = true
				return rev
			}
		}
	}
	edges := []RevisionEdge{}
	previous := ""
	for {
		capacity := maxClusterMembers
		if previous != "" {
			capacity--
		}
		count := min(capacity, len(parents))
		batch := append([]string{}, parents[:count]...)
		parents = parents[count:]
		if previous != "" {
			batch = append(batch, previous)
		}
		revision := finalRevision
		if len(parents) > 0 {
			revision = freshRevision()
		}
		value, generation := payload(revision)
		edges = append(edges, newRevisionEdge(kind, entity, revision, value, batch, generation))
		if len(parents) == 0 {
			return edges
		}
		previous = revision
	}
}
