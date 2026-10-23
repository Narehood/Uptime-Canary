package canary

import (
	"encoding/json"
	"slices"
)

// Facts and links are content addressed. Roots commit complete sets without
// putting an ever-growing list in a mutable record. Links form an immutable
// append/union DAG; each append or union needs at most one new small record.
type EvidenceLink struct {
	ID     string `json:"id"`
	Left   string `json:"left,omitempty"`
	Right  string `json:"right,omitempty"`
	FactID string `json:"factId,omitempty"`
}

func contentID(v any) string       { b, _ := json.Marshal(v); return digest(string(b)) }
func linkID(v EvidenceLink) string { v.ID = ""; return contentID(v) }
func addEvidence(links map[string]EvidenceLink, root, fact string) string {
	v := EvidenceLink{Left: root, FactID: fact}
	v.ID = linkID(v)
	links[v.ID] = v
	return v.ID
}

// Iterative traversal handles arbitrarily deep local histories without stack
// growth. A missing link is unknown evidence, never an empty set.
func evidenceIDs(root string, links map[string]EvidenceLink) (map[string]bool, bool) {
	out, visited := map[string]bool{}, map[string]bool{}
	queue := []string{root}
	for len(queue) > 0 {
		id := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if id == "" || visited[id] {
			continue
		}
		visited[id] = true
		v, ok := links[id]
		if !ok {
			return nil, false
		}
		if v.FactID != "" {
			out[v.FactID] = true
		}
		queue = append(queue, v.Left, v.Right)
	}
	return out, true
}

type RevisionEdge struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	EntityID    string   `json:"entityId"`
	Revision    string   `json:"revision"`
	Parents     []string `json:"parents"`
	PayloadHash string   `json:"payloadHash"`
	Generation  string   `json:"generation,omitempty"`
	// Legacy ancestry is known transitive reachability, not invented direct
	// provenance. Its exact claims are paged and committed by this root.
	LegacyRoot string `json:"legacyRoot,omitempty"`
}

type RevisionClaim struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	EntityID string `json:"entityId"`
	Revision string `json:"revision"`
	Ancestor string `json:"ancestor"`
}

func revisionKey(kind, entity, revision string) string { return kind + "/" + entity + "/" + revision }
func edgeID(v RevisionEdge) string                     { v.ID = ""; return contentID(v) }
func claimID(v RevisionClaim) string                   { v.ID = ""; return contentID(v) }
func newRevisionEdge(kind, entity, revision string, payload any, parents []string, generation string) RevisionEdge {
	v := RevisionEdge{Kind: kind, EntityID: entity, Revision: revision, Parents: unique(parents), PayloadHash: contentID(payload), Generation: generation}
	v.ID = edgeID(v)
	return v
}

type revisionGraph struct {
	edges      map[string]RevisionEdge // by immutable edge ID
	byRevision map[string]string
	parents    map[string][]string
	// Only queried pairs are cached. This avoids expanding/copying transitive
	// ancestry on every write and repeating retirement walks on every tick.
	reach         map[string]bool
	pendingLegacy bool
	complete      map[string]bool
}

func buildRevisionGraph(edges map[string]RevisionEdge, claims map[string]RevisionClaim, links map[string]EvidenceLink) (*revisionGraph, error) {
	g := &revisionGraph{edges: edges, byRevision: map[string]string{}, parents: map[string][]string{}, reach: map[string]bool{}, complete: map[string]bool{}}
	for id, e := range edges {
		key := revisionKey(e.Kind, e.EntityID, e.Revision)
		if previous, ok := g.byRevision[key]; ok && previous != id {
			return nil, fail(400, "Immutable revision collision")
		}
		g.byRevision[key] = id
		g.parents[key] = append([]string{}, e.Parents...)
		g.complete[id] = e.LegacyRoot == ""
		if e.LegacyRoot != "" {
			ids, complete := evidenceIDs(e.LegacyRoot, links)
			if !complete {
				g.pendingLegacy = true
				continue
			}
			for fact := range ids {
				c, ok := claims[fact]
				if !ok {
					complete = false
					g.pendingLegacy = true
					break
				}
				if c.Kind != e.Kind || c.EntityID != e.EntityID || c.Revision != e.Revision {
					return nil, fail(400, "Invalid legacy revision evidence")
				}
			}
			if complete {
				g.complete[id] = true
				for fact := range ids {
					g.parents[key] = append(g.parents[key], claims[fact].Ancestor)
				}
			}
		}
	}
	// Kahn's algorithm detects cycles without recursion or quadratic per-edge
	// reachability checks. Missing parents are allowed and remain unresolved.
	degree, children := map[string]int{}, map[string][]string{}
	for key, parents := range g.parents {
		for _, parent := range parents {
			e := edges[g.byRevision[key]]
			p := revisionKey(e.Kind, e.EntityID, parent)
			if _, known := g.parents[p]; known {
				degree[key]++
				children[p] = append(children[p], key)
			}
		}
	}
	queue := []string{}
	for key := range g.parents {
		if degree[key] == 0 {
			queue = append(queue, key)
		}
	}
	visited := 0
	for len(queue) > 0 {
		key := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		visited++
		e := edges[g.byRevision[key]]
		ready := g.complete[e.ID]
		for _, parent := range e.Parents {
			parentID, known := g.byRevision[revisionKey(e.Kind, e.EntityID, parent)]
			ready = ready && known && g.complete[parentID]
		}
		g.complete[e.ID] = ready
		for _, child := range children[key] {
			degree[child]--
			if degree[child] == 0 {
				queue = append(queue, child)
			}
		}
	}
	if visited != len(g.parents) {
		return nil, fail(400, "Invalid revision graph")
	}
	return g, nil
}

func (g *revisionGraph) reaches(kind, entity, from, target string) bool {
	if from == target {
		return true
	}
	if g == nil {
		return false
	}
	cache := revisionKey(kind, entity, from) + "/" + target
	if answer, ok := g.reach[cache]; ok {
		return answer
	}
	seen, queue := map[string]bool{}, []string{from}
	for len(queue) > 0 {
		rev := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if seen[rev] {
			continue
		}
		seen[rev] = true
		for _, parent := range g.parents[revisionKey(kind, entity, rev)] {
			if parent == target {
				g.reach[cache] = true
				return true
			}
			queue = append(queue, parent)
		}
	}
	g.reach[cache] = false
	return false
}

func (g *revisionGraph) matches(id, kind, entity, revision string, payload any, parents []string) bool {
	if id == "" {
		return true
	} // self-contained legacy records are migrated on admission
	if g == nil {
		return false
	}
	e, ok := g.edges[id]
	return ok && g.complete[id] && e.Kind == kind && e.EntityID == entity && e.Revision == revision && e.PayloadHash == contentID(payload) && slices.Equal(e.Parents, parents)
}

func (n *Node) monitorVersionReady(v Version) bool {
	if v.EdgeID == "" {
		return true
	}
	return n.graph.matches(v.EdgeID, "monitor", v.Monitor.ID, v.Monitor.Revision, v.Monitor, v.Ancestors) && n.graph.reaches("monitor", v.Monitor.ID, v.Monitor.Revision, checkGeneration(v.Monitor))
}

func (n *Node) groupVersionReady(v GroupVersion) bool {
	return v.EdgeID == "" || n.graph.matches(v.EdgeID, "group", v.Group.ID, v.Group.Revision, v.Group, v.Ancestors)
}

func (n *Node) resourceVersionReady(v ResourceVersion) bool {
	r := v.Resource
	return v.EdgeID == "" || n.graph.matches(v.EdgeID, r.Kind, r.ID, r.Revision, r, v.Ancestors)
}

type IncidentFact struct {
	ID         string            `json:"id"`
	MonitorID  string            `json:"monitorId"`
	Revision   string            `json:"revision"`
	IncidentID string            `json:"incidentId"` // original identity; never rewritten by aliases
	Kind       string            `json:"kind"`       // run, alias, predecessor
	NodeID     string            `json:"nodeId,omitempty"`
	Run        int64             `json:"run,omitempty"`
	RelatedID  string            `json:"relatedId,omitempty"`
	ObserverID string            `json:"observerId,omitempty"`
	Boundary   *uncertainHistory `json:"boundary,omitempty"`
}

func incidentFactID(v IncidentFact) string { v.ID = ""; return contentID(v) }

const inlineIncidentIDs = 32
const inlineIncidentRuns = 32

func boundedIncident(i Incident) Incident {
	i = boundedDeliveryTargets(i)
	i.Aliases = unique(i.Aliases)
	if len(i.Aliases) > inlineIncidentIDs {
		i.Aliases = i.Aliases[:inlineIncidentIDs]
	}
	i.Predecessors = unique(i.Predecessors)
	if len(i.Predecessors) > inlineIncidentIDs {
		i.Predecessors = i.Predecessors[:inlineIncidentIDs]
	}
	runs := map[string][]int64{}
	for node, seqs := range i.Runs {
		seqs = unique(seqs)
		if len(seqs) > inlineIncidentRuns {
			seqs = seqs[len(seqs)-inlineIncidentRuns:]
		}
		runs[node] = seqs
	}
	i.Runs = runs
	return i
}

// prepareIncident adds only facts not already committed by the old root. The
// complete hydrated projection is supplied here, never an inline wire summary.
func prepareIncident(i Incident, oldRoot string, facts map[string]IncidentFact, links map[string]EvidenceLink) Incident {
	ids, complete := evidenceIDs(oldRoot, links)
	if !complete {
		oldRoot = ""
		ids = map[string]bool{}
	}
	add := func(f IncidentFact) {
		f.MonitorID, f.Revision, f.IncidentID = i.MonitorID, i.Revision, i.ID
		f.ID = incidentFactID(f)
		facts[f.ID] = f
		if !ids[f.ID] {
			oldRoot = addEvidence(links, oldRoot, f.ID)
			ids[f.ID] = true
		}
	}
	for _, alias := range episodeIDs(i) {
		add(IncidentFact{Kind: "alias", RelatedID: alias})
	}
	for _, pred := range unique(i.Predecessors) {
		add(IncidentFact{Kind: "predecessor", RelatedID: pred})
	}
	for _, u := range i.uncertainties {
		header := u.Header
		add(IncidentFact{Kind: "uncertainty", ObserverID: u.ObserverID, NodeID: u.NodeID, Run: u.Run, Boundary: &header})
	}
	nodes := []string{}
	for node := range i.Runs {
		nodes = append(nodes, node)
	}
	slices.Sort(nodes)
	for _, node := range nodes {
		for _, run := range unique(i.Runs[node]) {
			add(IncidentFact{Kind: "run", NodeID: node, Run: run})
		}
	}
	i.EvidenceRoot = oldRoot
	return i
}

func hydrateIncident(i Incident, facts map[string]IncidentFact, links map[string]EvidenceLink) (Incident, bool, error) {
	if i.EvidenceRoot == "" {
		return i, true, nil
	}
	ids, complete := evidenceIDs(i.EvidenceRoot, links)
	if !complete {
		return i, false, nil
	}
	i.Aliases, i.Predecessors, i.Runs = []string{}, []string{}, map[string][]int64{}
	i.uncertainties = nil
	for id := range ids {
		f, ok := facts[id]
		if !ok {
			return i, false, nil
		}
		if f.MonitorID != i.MonitorID || f.Revision != i.Revision || f.IncidentID != i.ID {
			return i, false, fail(400, "Incident evidence scope mismatch")
		}
		switch f.Kind {
		case "alias":
			i.Aliases = append(i.Aliases, f.RelatedID)
		case "predecessor":
			i.Predecessors = append(i.Predecessors, f.RelatedID)
		case "run":
			i.Runs[f.NodeID] = append(i.Runs[f.NodeID], f.Run)
		case "uncertainty":
			if f.Boundary == nil {
				return i, false, fail(400, "Invalid uncertainty boundary")
			}
			i.uncertainties = append(i.uncertainties, incidentUncertainty{Header: *f.Boundary, ObserverID: f.ObserverID, NodeID: f.NodeID, Run: f.Run})
		}
	}
	i.Aliases = unique(i.Aliases)
	i.Predecessors = unique(i.Predecessors)
	for node := range i.Runs {
		i.Runs[node] = unique(i.Runs[node])
	}
	if !slices.Contains(i.Aliases, i.ID) || len(i.Runs) == 0 {
		return i, false, fail(400, "Incomplete incident evidence")
	}
	return i, true, nil
}
