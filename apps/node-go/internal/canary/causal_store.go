package canary

import (
	"maps"
	"slices"
)

func legacyRevision(kind, entity, revision string, payload any, ancestors []string, generation string, edges map[string]RevisionEdge, claims map[string]RevisionClaim, links map[string]EvidenceLink) RevisionEdge {
	e := newRevisionEdge(kind, entity, revision, payload, []string{}, generation)
	for _, ancestor := range unique(ancestors) {
		c := RevisionClaim{Kind: kind, EntityID: entity, Revision: revision, Ancestor: ancestor}
		c.ID = claimID(c)
		claims[c.ID] = c
		e.LegacyRoot = addEvidence(links, e.LegacyRoot, c.ID)
	}
	e.ID = edgeID(e)
	edges[e.ID] = e
	return e
}

// Upgrade only self-contained old records. No history is inferred from their
// UUID ordering; each old ancestor becomes an explicit transitive claim.
func upgradeConfiguration(s *Snapshot, edges map[string]RevisionEdge, claims map[string]RevisionClaim, links map[string]EvidenceLink) {
	for index, v := range s.Versions {
		if v.EdgeID == "" {
			e := legacyRevision("monitor", v.Monitor.ID, v.Monitor.Revision, v.Monitor, v.Ancestors, checkGeneration(v.Monitor), edges, claims, links)
			v.EdgeID, v.Ancestors = e.ID, []string{}
			s.Versions[index] = v
		}
	}
	for index, v := range s.GroupVersions {
		if v.EdgeID == "" {
			e := legacyRevision("group", v.Group.ID, v.Group.Revision, v.Group, v.Ancestors, "", edges, claims, links)
			v.EdgeID, v.Ancestors = e.ID, []string{}
			s.GroupVersions[index] = v
		}
	}
	for index, v := range s.Resources {
		if v.EdgeID == "" {
			r := v.Resource
			e := legacyRevision(r.Kind, r.ID, r.Revision, r, v.Ancestors, "", edges, claims, links)
			v.EdgeID, v.Ancestors = e.ID, []string{}
			s.Resources[index] = v
		}
	}
}

func sameRevisionPayload(g *revisionGraph, edge, kind, entity, rev string, payload any, parents []string) error {
	if prior, ok := g.byRevision[revisionKey(kind, entity, rev)]; ok {
		e := g.edges[prior]
		if e.PayloadHash != contentID(payload) || edge != prior || !slices.Equal(e.Parents, parents) {
			return fail(400, "Immutable revision collision")
		}
	}
	if e, ok := g.edges[edge]; ok && (e.Kind != kind || e.EntityID != entity || e.Revision != rev || e.PayloadHash != contentID(payload) || !slices.Equal(e.Parents, parents)) {
		return fail(400, "Revision edge mismatch")
	}
	return nil
}

func persistRevisionEdge(e dbExec, v RevisionEdge) error {
	if err := put(e, "revisionEdges", v.ID, v); err != nil {
		return err
	}
	key := v.Kind + "/" + v.EntityID
	if _, err := e.Exec(`INSERT INTO revision_index(entity,revision,edge_id,payload_hash) VALUES(?,?,?,?) ON CONFLICT(entity,revision) DO NOTHING`, key, v.Revision, v.ID, v.PayloadHash); err != nil {
		return err
	}
	for _, parent := range v.Parents {
		if _, err := e.Exec(`INSERT OR IGNORE INTO revision_parents(entity,revision,parent) VALUES(?,?,?)`, key, v.Revision, parent); err != nil {
			return err
		}
	}
	return nil
}

func persistRevisionClaim(e dbExec, v RevisionClaim) error {
	if err := put(e, "revisionClaims", v.ID, v); err != nil {
		return err
	}
	_, err := e.Exec(`INSERT OR IGNORE INTO revision_parents(entity,revision,parent) VALUES(?,?,?)`, v.Kind+"/"+v.EntityID, v.Revision, v.Ancestor)
	return err
}

func persistIncidentFact(e dbExec, v IncidentFact) error {
	if err := put(e, "incidentFacts", v.ID, v); err != nil {
		return err
	}
	_, err := e.Exec(`INSERT OR IGNORE INTO incident_fact_index(fact_id,monitor_id,incident_id,revision,kind,node_id,run,related_id) VALUES(?,?,?,?,?,?,?,?)`, v.ID, v.MonitorID, v.IncidentID, v.Revision, v.Kind, v.NodeID, v.Run, v.RelatedID)
	return err
}

// sourceProjection preserves original source records while keeping inferred
// alias combinations out of the source set. Pending commitments are stored
// independently and do not become evidence until all links and facts exist.
func sourceProjection(sources, pending map[string]Incident, incoming []Incident, facts map[string]IncidentFact, links map[string]EvidenceLink) (map[string]Incident, map[string]Incident, map[string]Incident, error) {
	full := map[string]Incident{}
	remaining := map[string]Incident{}
	admit := func(i Incident) error {
		h, complete, err := hydrateIncident(i, facts, links)
		if err != nil {
			return err
		}
		if !complete {
			remaining[contentID(i)] = i
			return nil
		}
		h = boundedDeliveryTargets(h)
		if old, ok := full[h.ID]; ok {
			if old.MonitorID != h.MonitorID || old.Revision != h.Revision {
				return fail(400, "Immutable incident identity collision")
			}
			// Source commitments keep their original identity even when a new
			// alias sorts before it. Canonical IDs belong only to projections;
			// reusing the old root under a canonical ID corrupts its fact scope.
			originalID := h.ID
			h = combine(old, h)
			h.ID = originalID
		}
		full[h.ID] = h
		return nil
	}
	for _, set := range []map[string]Incident{sources, pending} {
		for _, i := range set {
			if err := admit(i); err != nil {
				return nil, nil, nil, err
			}
		}
	}
	for _, i := range incoming {
		if err := admit(i); err != nil {
			return nil, nil, nil, err
		}
	}
	var err error
	full, err = resolveUncertaintySet(full, facts, links)
	if err != nil {
		return nil, nil, nil, err
	}
	out := map[string]Incident{}
	records := []Incident{}
	for id, i := range full {
		root := i.EvidenceRoot
		if previous, ok := sources[id]; ok {
			root = previous.EvidenceRoot
		}
		i = prepareIncident(i, root, facts, links)
		out[id] = boundedIncident(i)
		records = append(records, i)
	}
	projected := map[string]Incident{}
	for _, i := range reconcileEpisodes(records) {
		// A projection commits its own complete evidence, but only original
		// source summaries are transmitted or used for the next reconciliation.
		root := ""
		if original, ok := out[i.ID]; ok {
			root = original.EvidenceRoot
		}
		i = prepareIncident(i, root, facts, links)
		projected[i.ID] = i
	}
	return out, remaining, projected, nil
}

// An exact replay of a complete source adds no incident evidence. Local-only
// uncertainty inputs are not represented by JSON and must never use this path.
// Facts/links are checked separately, so newly available proof still projects.
func incidentSummariesChanged(sources map[string]Incident, incoming []Incident) bool {
	for _, next := range incoming {
		previous, exists := sources[next.ID]
		if !exists || len(next.uncertainties) != 0 || !equalJSON(previous, next) {
			return true
		}
	}
	return false
}

func (n *Node) initializeCausalFacts() error {
	var err error
	if n.revisionEdges, err = loadAll[RevisionEdge](n.store, "revisionEdges"); err != nil {
		return err
	}
	if n.revisionClaims, err = loadAll[RevisionClaim](n.store, "revisionClaims"); err != nil {
		return err
	}
	if n.evidenceLinks, err = loadAll[EvidenceLink](n.store, "evidenceLinks"); err != nil {
		return err
	}
	if n.incidentFacts, err = loadAll[IncidentFact](n.store, "incidentFacts"); err != nil {
		return err
	}
	if n.incidentSources, err = loadAll[Incident](n.store, "incidentSources"); err != nil {
		return err
	}
	if n.pendingIncidents, err = loadAll[Incident](n.store, "pendingIncidents"); err != nil {
		return err
	}
	if err = n.loadLocalIncidentDeliveries(); err != nil {
		return err
	}
	for id, v := range n.revisionEdges {
		if id != v.ID {
			return fail(503, "Invalid persisted revision edge")
		}
		if err = n.validateCausalFacts(Snapshot{RevisionEdges: []RevisionEdge{v}}); err != nil {
			return err
		}
	}
	for id, v := range n.revisionClaims {
		if id != v.ID {
			return fail(503, "Invalid persisted revision claim")
		}
		if err = n.validateCausalFacts(Snapshot{RevisionClaims: []RevisionClaim{v}}); err != nil {
			return err
		}
	}
	for id, v := range n.incidentFacts {
		if id != v.ID {
			return fail(503, "Invalid persisted incident fact")
		}
		if err = n.validateCausalFacts(Snapshot{IncidentFacts: []IncidentFact{v}}); err != nil {
			return err
		}
	}
	for id, v := range n.evidenceLinks {
		if id != v.ID {
			return fail(503, "Invalid persisted evidence link")
		}
		if err = n.validateCausalFacts(Snapshot{EvidenceLinks: []EvidenceLink{v}}); err != nil {
			return err
		}
	}
	if n.graph, err = buildRevisionGraph(n.revisionEdges, n.revisionClaims, n.evidenceLinks); err != nil {
		return err
	}
	return n.ensureCausalFacts()
}

// Also supports the repository's prototype/bootstrap fixtures which populate
// records directly. Production writers already produce committed roots.
func (n *Node) ensureCausalFacts() error {
	s := Snapshot{}
	for _, heads := range n.heads {
		for _, v := range heads {
			if v.EdgeID == "" {
				s.Versions = append(s.Versions, v)
			}
		}
	}
	for _, heads := range n.groups {
		for _, v := range heads {
			if v.EdgeID == "" {
				s.GroupVersions = append(s.GroupVersions, v)
			}
		}
	}
	for _, heads := range n.resources {
		for _, v := range heads {
			if v.EdgeID == "" {
				s.Resources = append(s.Resources, v)
			}
		}
	}
	for _, i := range n.episodes {
		if i.EvidenceRoot == "" || !n.causalReady && len(n.incidentSources) == 0 {
			s.Incidents = append(s.Incidents, i)
		}
	}
	if len(s.Versions)+len(s.GroupVersions)+len(s.Resources)+len(s.Incidents) == 0 && n.causalReady {
		return nil
	}
	if err := n.applySnapshot(s); err != nil {
		return err
	}
	n.causalReady = true
	return nil
}

// applySnapshot stages every map, evidence index and derived projection before
// one SQL transaction. No in-memory authority changes on a failed commit.
func (n *Node) applySnapshot(s Snapshot) error {
	return n.applySnapshotLocal(s, nil)
}

// A local management receipt can commit with its configuration revision. The
// callback is never supplied by peer input and must not publish memory state.
func (n *Node) applySnapshotLocal(s Snapshot, local func(dbExec) error) error {
	if err := n.validateCausalFacts(s); err != nil {
		return err
	}
	pushEvents, err := n.mergePushEvents(s.Heartbeats)
	if err != nil {
		return err
	}
	// A failed attempt must leave the caller's retryable page untouched too.
	s.Versions = append([]Version{}, s.Versions...)
	s.GroupVersions = append([]GroupVersion{}, s.GroupVersions...)
	s.Resources = append([]ResourceVersion{}, s.Resources...)
	edges, claims := maps.Clone(n.revisionEdges), maps.Clone(n.revisionClaims)
	facts, links := maps.Clone(n.incidentFacts), maps.Clone(n.evidenceLinks)
	for _, v := range s.RevisionEdges {
		edges[v.ID] = v
	}
	for _, v := range s.RevisionClaims {
		claims[v.ID] = v
	}
	for _, v := range s.IncidentFacts {
		facts[v.ID] = v
	}
	for _, v := range s.EvidenceLinks {
		links[v.ID] = v
	}
	upgradeConfiguration(&s, edges, claims, links)
	// Legacy local heads may still be present during the root upgrade (or
	// bootstrap). Normalize their representation before immutable comparison.
	localHeads, localGroups, localResources := maps.Clone(n.heads), maps.Clone(n.groups), maps.Clone(n.resources)
	legacy := Snapshot{}
	for _, vs := range localHeads {
		for _, v := range vs {
			if v.EdgeID == "" {
				legacy.Versions = append(legacy.Versions, v)
			}
		}
	}
	for _, vs := range localGroups {
		for _, v := range vs {
			if v.EdgeID == "" {
				legacy.GroupVersions = append(legacy.GroupVersions, v)
			}
		}
	}
	for _, vs := range localResources {
		for _, v := range vs {
			if v.EdgeID == "" {
				legacy.Resources = append(legacy.Resources, v)
			}
		}
	}
	upgradeConfiguration(&legacy, edges, claims, links)
	for _, v := range legacy.Versions {
		vs := append([]Version{}, localHeads[v.Monitor.ID]...)
		for index, old := range vs {
			if old.Monitor.Revision == v.Monitor.Revision {
				vs[index] = v
			}
		}
		localHeads[v.Monitor.ID] = vs
	}
	for _, v := range legacy.GroupVersions {
		vs := append([]GroupVersion{}, localGroups[v.Group.ID]...)
		for index, old := range vs {
			if old.Group.Revision == v.Group.Revision {
				vs[index] = v
			}
		}
		localGroups[v.Group.ID] = vs
	}
	for _, v := range legacy.Resources {
		key := resourceKey(v.Resource.Kind, v.Resource.ID)
		vs := append([]ResourceVersion{}, localResources[key]...)
		for index, old := range vs {
			if old.Resource.Revision == v.Resource.Revision {
				vs[index] = v
			}
		}
		localResources[key] = vs
	}
	graph := n.graph
	graphChanged := graph == nil || len(edges) != len(n.revisionEdges) || len(claims) != len(n.revisionClaims) || graph.pendingLegacy && len(links) != len(n.evidenceLinks)
	if graphChanged {
		graph, err = buildRevisionGraph(edges, claims, links)
		if err != nil {
			return err
		}
	}
	for _, v := range s.Versions {
		if err = sameRevisionPayload(graph, v.EdgeID, "monitor", v.Monitor.ID, v.Monitor.Revision, v.Monitor, v.Ancestors); err != nil {
			return err
		}
	}
	for _, v := range s.GroupVersions {
		if err = sameRevisionPayload(graph, v.EdgeID, "group", v.Group.ID, v.Group.Revision, v.Group, v.Ancestors); err != nil {
			return err
		}
	}
	for _, v := range s.Resources {
		r := v.Resource
		if err = sameRevisionPayload(graph, v.EdgeID, r.Kind, r.ID, r.Revision, r, v.Ancestors); err != nil {
			return err
		}
	}
	headChanged, groupChanged, resourceChanged := graphChanged || len(s.Versions) > 0, graphChanged || len(s.GroupVersions) > 0, graphChanged || len(s.Resources) > 0
	heads, groups, resources := n.heads, n.groups, n.resources
	if headChanged {
		heads, err = mergeVersions(localHeads, s.Versions, graph)
	}
	if err != nil {
		return err
	}
	if groupChanged {
		groups, err = mergeGroupVersions(localGroups, s.GroupVersions, graph)
	}
	if err != nil {
		return err
	}
	if resourceChanged {
		resources, err = mergeResources(localResources, s.Resources, graph)
	}
	if err != nil {
		return err
	}
	if headChanged {
		for _, vs := range heads {
			for _, v := range vs {
				if err = sameRevisionPayload(graph, v.EdgeID, "monitor", v.Monitor.ID, v.Monitor.Revision, v.Monitor, v.Ancestors); err != nil {
					return err
				}
			}
		}
	}
	if groupChanged {
		for _, vs := range groups {
			for _, v := range vs {
				if err = sameRevisionPayload(graph, v.EdgeID, "group", v.Group.ID, v.Group.Revision, v.Group, v.Ancestors); err != nil {
					return err
				}
			}
		}
	}
	if resourceChanged {
		for _, vs := range resources {
			for _, v := range vs {
				r := v.Resource
				if err = sameRevisionPayload(graph, v.EdgeID, r.Kind, r.ID, r.Revision, r, v.Ancestors); err != nil {
					return err
				}
			}
		}
	}
	plans, err := n.maintenancePlansFor(resources, s.preparedMaintenance)
	if err != nil {
		return err
	}
	samples := maps.Clone(n.samples)
	for _, o := range s.Observations {
		if o.NodeID != n.ID {
			key := o.MonitorID + "/" + o.NodeID
			if previous, ok := samples[key]; !ok || o.Sequence > previous.Sequence {
				samples[key] = o
			}
		}
	}
	intents := maps.Clone(n.intents)
	for _, i := range s.Intents {
		intents[i.ID] = i
	}
	for _, i := range s.Incidents {
		_, complete, hydrateErr := hydrateIncident(i, facts, links)
		if hydrateErr != nil {
			return hydrateErr
		}
		if !complete {
			continue
		}
		for _, target := range i.DeliveryTargets {
			intent := deliveryIntent(i.MonitorID, i.ID, target)
			intents[intent.ID] = intent
		}
	}
	sources, pending, episodes := n.incidentSources, n.pendingIncidents, n.episodes
	// Complete sources may reference incomplete embedded boundaries even when
	// no standalone incident is pending. New proof must refresh those bindings.
	proofChanged := len(facts) != len(n.incidentFacts) || len(links) != len(n.evidenceLinks)
	projected := !n.causalReady || incidentSummariesChanged(sources, s.Incidents) ||
		proofChanged && (len(s.Incidents) > 0 || len(pending) > 0 || hasUncertaintyReferences(episodes))
	if projected {
		sources, pending, episodes, err = sourceProjection(sources, pending, s.Incidents, facts, links)
		if err != nil {
			return err
		}
	}
	for _, i := range sources {
		for _, target := range i.DeliveryTargets {
			intent := deliveryIntent(i.MonitorID, i.ID, target)
			intents[intent.ID] = intent
		}
	}
	localDeliveries := n.stageLocalIncidentDeliveries(s, sources, intents, facts, links)
	receipts := maps.Clone(n.receipts)
	for _, r := range s.Receipts {
		if _, exists := receipts[r.ID]; !exists {
			receipts[r.ID] = r
		}
	}
	tx, err := n.store.db.Begin()
	if err != nil {
		return fail(503, "Persistence unavailable")
	}
	defer tx.Rollback()
	write := func(category, id string, value any) {
		if err == nil {
			err = put(tx, category, id, value)
		}
	}
	if err == nil {
		err = n.persistPushEvents(tx, pushEvents)
	}
	if !n.causalReady {
		write("settings", "causalSchema", peerProtocol)
	}
	for id, v := range edges {
		if _, ok := n.revisionEdges[id]; !ok && err == nil {
			err = persistRevisionEdge(tx, v)
		}
	}
	for id, v := range claims {
		if _, ok := n.revisionClaims[id]; !ok && err == nil {
			err = persistRevisionClaim(tx, v)
		}
	}
	for id, v := range facts {
		if _, ok := n.incidentFacts[id]; !ok && err == nil {
			err = persistIncidentFact(tx, v)
		}
	}
	for id, v := range links {
		if _, ok := n.evidenceLinks[id]; !ok {
			write("evidenceLinks", id, v)
		}
	}
	if headChanged {
		for id, v := range heads {
			if !equalJSON(n.heads[id], v) {
				write("heads", id, v)
			}
		}
	}
	if groupChanged {
		for id, v := range groups {
			if !equalJSON(n.groups[id], v) {
				write("groupHeads", id, v)
			}
		}
	}
	if resourceChanged {
		for id, v := range resources {
			if !equalJSON(n.resources[id], v) {
				write("resourceHeads", id, v)
			}
		}
	}
	for id, v := range samples {
		if !equalJSON(n.samples[id], v) {
			write("samples", id, v)
		}
	}
	for id, v := range intents {
		if _, ok := n.intents[id]; !ok {
			write("notificationIntents", id, v)
		}
	}
	for id, v := range localDeliveries {
		if _, ok := n.localIncidentDeliveries[id]; !ok {
			write("localIncidentDeliveries", id, v)
		}
	}
	for id, v := range receipts {
		if _, ok := n.receipts[id]; !ok {
			write("receipts", id, v)
		}
	}
	if projected {
		for id, v := range sources {
			if !equalJSON(n.incidentSources[id], v) {
				write("incidentSources", id, v)
			}
		}
		for id, v := range pending {
			if _, ok := n.pendingIncidents[id]; !ok {
				write("pendingIncidents", id, v)
			}
		}
		for id := range n.pendingIncidents {
			if _, ok := pending[id]; !ok && err == nil {
				err = remove(tx, "pendingIncidents", id)
			}
		}
		for id, v := range episodes {
			v = boundedIncident(v)
			if !equalJSON(boundedIncident(n.episodes[id]), v) {
				write("incidents", id, v)
			}
		}
		for id := range n.episodes {
			if _, ok := episodes[id]; !ok && err == nil {
				err = remove(tx, "incidents", id)
			}
		}
	}
	if err == nil && (headChanged || groupChanged || resourceChanged) {
		// A local interval loses authority as soon as configuration changes,
		// conflicts or incomplete proof make that exact revision unavailable.
		// This closure commits with the configuration, including peer changes.
		proof := Node{graph: graph}
		annotationChanged := n.availabilityAnnotationsChanged(&proof, groups, resources)
		at := n.now()
		for id, previous := range n.heads {
			next := heads[id]
			if annotationChanged || len(previous) != 1 || len(next) != 1 || previous[0].Monitor.Revision != next[0].Monitor.Revision ||
				next[0].Monitor.Deleted || !next[0].Monitor.Enabled || !slices.Contains(next[0].Monitor.NodeIDs, n.ID) || !proof.monitorVersionReady(next[0]) {
				err = closeLocalAvailability(tx, id, at)
				if err != nil {
					break
				}
			}
		}
	}
	if err == nil && headChanged {
		err = purgeBrowserScreenshots(tx, n.heads, heads)
	}
	if err == nil && local != nil {
		err = local(tx)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return fail(503, "Persistence unavailable")
	}
	if headChanged {
		for id, v := range heads {
			if !equalJSON(n.heads[id], v) {
				if cancel := n.probing[id]; cancel != nil {
					cancel()
				}
				delete(n.nextProbe, id)
			}
		}
	}
	n.heads, n.groups = heads, groups
	if !equalJSON(n.resources, resources) || len(edges) != len(n.revisionEdges) || len(claims) != len(n.revisionClaims) || len(links) != len(n.evidenceLinks) {
		n.maintenanceWindows = nil
	}
	n.resources = resources
	n.maintenancePlans.Store(&plans)
	n.samples, n.episodes, n.receipts = samples, episodes, receipts
	for id, intent := range intents {
		if _, ok := n.intents[id]; !ok {
			n.indexIntent(intent)
		}
	}
	n.intents = intents
	n.localIncidentDeliveries = localDeliveries
	n.revisionEdges, n.revisionClaims, n.graph = edges, claims, graph
	n.incidentFacts, n.evidenceLinks = facts, links
	n.incidentSources, n.pendingIncidents = sources, pending
	if headChanged {
		n.rebuildPushTokenIndex()
	}
	n.publishPushEvents(pushEvents)
	return nil
}

func (n *Node) pendingIncidentEvidence(monitor, generation string) bool {
	for _, i := range n.pendingIncidents {
		if i.MonitorID == monitor && i.Revision == generation {
			return true
		}
	}
	return false
}
