package canary

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"
	"sync"
	"time"
)

func readBounded(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if int64(len(b)) > max {
		return nil, fail(413, "Request body too large")
	}
	if err != nil {
		return nil, fail(400, "Request interrupted")
	}
	return b, nil
}
func validIDs(ids []string, limit int) bool {
	if ids == nil || len(ids) > limit {
		return false
	}
	for _, id := range ids {
		if identifier(id, "id") != nil {
			return false
		}
	}
	return true
}
func validTime(v int64) bool { return v >= 0 && v <= maxSafe }
func (n *Node) validateSnapshot(b []byte, expected string) (Snapshot, error) {
	bad := func() (Snapshot, error) { return Snapshot{}, fail(400, "Invalid peer snapshot") }
	if len(b) > maxBody {
		return bad()
	}
	raw, err := jsonObject(b)
	if err != nil {
		return bad()
	}
	if required(raw, "protocol", "node", "versions", "observations", "incidents", "receipts") != nil {
		return bad()
	}
	var s Snapshot
	if json.Unmarshal(b, &s) != nil {
		return bad()
	}
	if value, present := raw["heartbeats"]; present && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return bad()
	}
	if s.Protocol != peerProtocol || s.Node.ID != expected || validText(s.Node.Name, "peer name", 128) != nil {
		return bad()
	}
	if len(s.Heartbeats) > pageRecords || n.validatePushEvents(s.Heartbeats) != nil {
		return bad()
	}
	if n.validateCausalFacts(s) != nil {
		return bad()
	}
	// Only omission denotes the self-contained prototype representation. Null
	// must not erase a commitment and turn incomplete proof into legacy authority.
	for _, domain := range []string{"versions", "groupVersions", "resources", "incidents"} {
		if raw[domain] == nil {
			continue
		}
		var records []map[string]json.RawMessage
		if json.Unmarshal(raw[domain], &records) != nil {
			return bad()
		}
		field := "edgeId"
		if domain == "incidents" {
			field = "evidenceRoot"
		}
		for _, record := range records {
			if value, present := record[field]; present {
				var id string
				if json.Unmarshal(value, &id) != nil || !validCommitment(id) {
					return bad()
				}
			}
		}
	}
	for _, cursor := range []string{s.After, s.Next} {
		if cursor != "" && validText(cursor, "page cursor", 512) != nil {
			return bad()
		}
	}
	if s.Versions == nil || s.Observations == nil || s.Incidents == nil || s.Receipts == nil || len(s.Versions) > 1000 || len(s.GroupVersions) > 1000 || len(s.Observations) > 6400 || len(s.Incidents) > 5000 || len(s.Receipts) > 10000 {
		return bad()
	}
	var rawVersions []struct {
		Monitor json.RawMessage `json:"monitor"`
	}
	if len(s.Resources) > 1000 {
		return bad()
	}
	for _, v := range s.Resources {
		if validateResource(v) != nil {
			return bad()
		}
		if v.Resource.Kind == resourceMaintenance && !v.Resource.Deleted {
			in, err := parseMaintenance(v.Resource.Data)
			if err != nil {
				return bad()
			}
			p, err := n.compileMaintenance(context.Background(), in.Schedule)
			if err != nil {
				return Snapshot{}, err
			}
			if s.preparedMaintenance == nil {
				s.preparedMaintenance = compiledMaintenance{}
			}
			s.preparedMaintenance[maintenancePlanKey(in.Schedule)] = p
		}
	}
	if len(s.Intents) > 10000 {
		return bad()
	}
	for _, i := range s.Intents {
		if identifier(i.ID, "intent id") != nil || identifier(i.MonitorID, "monitorId") != nil || identifier(i.IncidentID, "incidentId") != nil || i.DestinationID != "" && identifier(i.DestinationID, "destinationId") != nil || i.ID != deliveryIntent(i.MonitorID, i.IncidentID, i.DestinationID).ID {
			return bad()
		}
	}
	if json.Unmarshal(raw["versions"], &rawVersions) != nil {
		return bad()
	}
	for i := range s.Versions {
		v := &s.Versions[i]
		input, err := parseMonitor(rawVersions[i].Monitor, n.known)
		if err != nil {
			return bad()
		}
		v.Monitor.MonitorInput = input
		if v.Monitor.Type == "push" && (!validTime(v.Monitor.PushStartedAt) || v.Monitor.PushStartedAt <= 0 || v.Monitor.PushStartedAt > n.now()+30000) || v.Monitor.Type != "push" && v.Monitor.PushStartedAt != 0 {
			return bad()
		}
		if identifier(v.Monitor.ID, "id") != nil || identifier(v.Monitor.Revision, "revision") != nil || !validTime(v.Monitor.UpdatedAt) || !validIDs(v.Ancestors, 10000) || slices.Contains(v.Ancestors, v.Monitor.Revision) {
			return bad()
		}
		if v.EdgeID != "" && (!validCommitment(v.EdgeID) || len(v.Ancestors) > maxClusterMembers) {
			return bad()
		}
		if v.Monitor.CheckGeneration != "" && (identifier(v.Monitor.CheckGeneration, "checkGeneration") != nil || v.EdgeID == "" && !slices.Contains(v.Ancestors, v.Monitor.CheckGeneration)) {
			return bad()
		}
	}
	if s.GroupVersions == nil {
		s.GroupVersions = []GroupVersion{}
	}
	for _, key := range []string{"versions", "groupVersions"} {
		if raw[key] == nil {
			continue
		}
		var versions []map[string]json.RawMessage
		if json.Unmarshal(raw[key], &versions) != nil {
			return bad()
		}
		for _, v := range versions {
			field := "monitor"
			if key == "groupVersions" {
				field = "group"
			}
			obj, err := jsonObject(v[field])
			if err != nil || required(obj, "id", "revision", "updatedAt") != nil || optionalNonNull(obj, "deleted") != nil {
				return bad()
			}
		}
	}
	for _, key := range []string{"observations", "incidents", "receipts"} {
		var records []map[string]json.RawMessage
		if json.Unmarshal(raw[key], &records) != nil {
			return bad()
		}
		for _, record := range records {
			var fields, nullable []string
			switch key {
			case "observations":
				fields = []string{"monitorId", "nodeId", "state", "checkedAt", "latencyMs", "message", "sequence", "revision"}
				nullable = []string{"downSince"}
				if optionalNonNull(record, "latencyAvailable") != nil {
					return bad()
				}
			case "incidents":
				fields = []string{"id", "aliases", "monitorId", "monitorName", "revision", "openedAt", "runs", "predecessors"}
				nullable = []string{"closed", "resolvedAt"}
			case "receipts":
				fields = []string{"id", "monitorId", "incidentId", "kind", "nodeId", "sentAt", "slot"}
			}
			if required(record, fields...) != nil {
				return bad()
			}
			for _, f := range nullable {
				if _, ok := record[f]; !ok {
					return bad()
				}
			}
		}
	}
	var rawGroups []struct {
		Group json.RawMessage `json:"group"`
	}
	if b, ok := raw["groupVersions"]; ok {
		if string(b) == "null" || json.Unmarshal(b, &rawGroups) != nil {
			return bad()
		}
	}
	for i := range s.GroupVersions {
		v := &s.GroupVersions[i]
		input, err := parseGroup(rawGroups[i].Group, n.known)
		if err != nil {
			return bad()
		}
		v.Group.GroupInput = input
		if identifier(v.Group.ID, "id") != nil || identifier(v.Group.Revision, "revision") != nil || !validTime(v.Group.UpdatedAt) || !validIDs(v.Ancestors, 10000) || slices.Contains(v.Ancestors, v.Group.Revision) {
			return bad()
		}
		if v.EdgeID != "" && (!validCommitment(v.EdgeID) || len(v.Ancestors) > maxClusterMembers) {
			return bad()
		}
	}
	for _, o := range s.Observations {
		if o.StatusCode != 0 && (o.StatusCode < 100 || o.StatusCode > 599) {
			return bad()
		}
		if c := o.Certificate; c != nil && (c.NotBefore.IsZero() || c.NotAfter.IsZero() || c.NotAfter.Before(c.NotBefore) || c.DaysRemaining < -3650000 || c.DaysRemaining > 3650000) {
			return bad()
		}
		if o.CheckGeneration != "" && identifier(o.CheckGeneration, "checkGeneration") != nil {
			return bad()
		}
		if identifier(o.MonitorID, "monitorId") != nil || identifier(o.Revision, "revision") != nil || !n.known[o.NodeID] || (o.State != "up" && o.State != "down" && o.State != "pending" && o.State != "unknown") || o.Sequence < 1 || o.Sequence > maxSafe || !validTime(o.CheckedAt) || o.CheckedAt > n.now()+30000 || !finite(o.LatencyMs) || o.LatencyMs < 0 || o.LatencyMs > 3600000 || validText(o.Message, "observation message", 256) != nil {
			return bad()
		}
		if (o.State == "down") != (o.DownSince != nil) {
			return bad()
		}
		if o.DownSince != nil && (*o.DownSince < 1 || *o.DownSince > o.Sequence) {
			return bad()
		}
		if o.ConsecutiveFailures < 0 || o.ConsecutiveFailures > 101 || o.RawState == "" && o.ConsecutiveFailures != 0 || o.RawState != "" && o.RawState != "up" && o.RawState != "down" && o.RawState != "pending" && o.RawState != "unknown" || o.RawState == "pending" && (o.State != "pending" || o.ConsecutiveFailures != 0) || o.State == "up" && o.ConsecutiveFailures != 0 || o.State == "down" && o.RawState != "" && o.ConsecutiveFailures == 0 || o.RawState == "unknown" && (o.State != "unknown" || o.ConsecutiveFailures != 0) || o.State == "unknown" && (o.RawState != "unknown" || o.LatencyAvailable == nil || *o.LatencyAvailable || o.LatencyMs != 0) {
			return bad()
		}
	}
	for _, i := range s.Incidents {
		if i.MaintenanceComplete && (i.Closed == nil || i.ResolvedAt == nil || i.MaintenanceStartedAt == nil || *i.MaintenanceStartedAt > i.OpenedAt || i.MaintenanceReleasedAt != nil && *i.MaintenanceReleasedAt < *i.ResolvedAt) {
			return bad()
		}
		if i.EvidenceRoot != "" {
			if !validCommitment(i.EvidenceRoot) || len(i.Aliases) > inlineIncidentIDs || len(i.Predecessors) > inlineIncidentIDs {
				return bad()
			}
			for _, seqs := range i.Runs {
				if len(seqs) > inlineIncidentRuns {
					return bad()
				}
			}
		}
		if len(i.MaintenanceIDs) > 0 && !validIDs(i.MaintenanceIDs, maxMaintenances) || i.MaintenanceComplete && i.Closed == nil {
			return bad()
		}
		for _, stamp := range []*int64{i.MaintenanceStartedAt, i.MaintenanceReleasedAt} {
			if stamp != nil && !validTime(*stamp) {
				return bad()
			}
		}
		if len(i.DeliveryTargets) > 801 {
			return bad()
		}
		for _, id := range i.DeliveryTargets {
			if id != "" && identifier(id, "delivery destination") != nil {
				return bad()
			}
		}
		if i.RetiredBy != "" && (identifier(i.RetiredBy, "retiredBy") != nil || i.Closed == nil) {
			return bad()
		}
		if identifier(i.ID, "id") != nil || identifier(i.MonitorID, "monitorId") != nil || identifier(i.Revision, "revision") != nil || validText(i.MonitorName, "monitorName", 128) != nil || !validTime(i.OpenedAt) || !validIDs(i.Aliases, 10000) || !validIDs(i.Predecessors, 10000) || len(i.Runs) < 1 || len(i.Runs) > 32 || (i.Closed == nil) != (i.ResolvedAt == nil) {
			return bad()
		}
		if i.ResolvedAt != nil && !validTime(*i.ResolvedAt) {
			return bad()
		}
		for node, seqs := range i.Runs {
			if !n.known[node] || seqs == nil || len(seqs) > 1000 {
				return bad()
			}
			for _, seq := range seqs {
				if seq < 1 || seq > maxSafe {
					return bad()
				}
			}
		}
		if len(i.Closed) > 32 {
			return bad()
		}
		for node, seq := range i.Closed {
			if !n.known[node] || seq < 1 || seq > maxSafe {
				return bad()
			}
		}
		if i.SuppressedByGroupID != "" && identifier(i.SuppressedByGroupID, "groupId") != nil {
			return bad()
		}
		if i.SuppressionReason != "" && validText(i.SuppressionReason, "reason", 256) != nil {
			return bad()
		}
		for _, t := range []*int64{i.SuppressedAt, i.SuppressionReleasedAt} {
			if t != nil && !validTime(*t) {
				return bad()
			}
		}
	}
	for _, r := range s.Receipts {
		if r.DestinationID != "" && identifier(r.DestinationID, "destinationId") != nil || r.ChannelID != "" && identifier(r.ChannelID, "channelId") != nil || r.Provider != "" && !slices.Contains([]string{"generic", "webhook", "discord", "smtp", "mailgun", "brevo", "mandrill", "slack", "telegram", "ntfy", "gotify", "pushover"}, r.Provider) {
			return bad()
		}
		if identifier(r.ID, "id") != nil || identifier(r.MonitorID, "monitorId") != nil || identifier(r.IncidentID, "incidentId") != nil || !n.known[r.NodeID] || (r.Kind != "down" && r.Kind != "recovery" && r.Kind != "repeat") || !validTime(r.SentAt) || r.Slot < 0 || r.Slot > maxSafe {
			return bad()
		}
	}
	return s, nil
}
func mergeVersions(local map[string][]Version, incoming []Version, graphs ...*revisionGraph) (map[string][]Version, error) {
	out := maps.Clone(local)
	grouped := map[string][]Version{}
	if len(graphs) > 0 {
		for id, vs := range local {
			grouped[id] = append([]Version{}, vs...)
		}
	}
	for _, v := range incoming {
		if _, ok := grouped[v.Monitor.ID]; !ok {
			grouped[v.Monitor.ID] = append([]Version{}, local[v.Monitor.ID]...)
		}
		grouped[v.Monitor.ID] = append(grouped[v.Monitor.ID], v)
	}
	for id, vs := range grouped {
		unique := map[string]Version{}
		for _, v := range vs {
			if old, ok := unique[v.Monitor.Revision]; ok && !equalJSON(old, v) {
				return nil, fail(400, "Immutable revision collision")
			}
			unique[v.Monitor.Revision] = v
		}
		heads := []Version{}
		for rev, v := range unique {
			dominated := false
			for _, other := range unique {
				if other.Monitor.Revision == rev {
					continue
				}
				proof := slices.Contains(other.Ancestors, rev)
				if len(graphs) > 0 {
					proof = graphs[0].reaches("monitor", other.Monitor.ID, other.Monitor.Revision, rev)
				}
				if proof {
					dominated = true
					break
				}
			}
			if !dominated {
				heads = append(heads, v)
			}
		}
		if len(heads) == 0 || len(graphs) == 0 && len(heads) > 32 {
			return nil, fail(400, "Invalid revision graph")
		}
		slices.SortFunc(heads, func(a, b Version) int { return stringsCompare(a.Monitor.Revision, b.Monitor.Revision) })
		out[id] = heads
	}
	if len(out) > maxMonitors*maxClusterMembers {
		return nil, fail(409, "Monitor capacity exceeded")
	}
	return out, nil
}
func mergeGroupVersions(local map[string][]GroupVersion, incoming []GroupVersion, graphs ...*revisionGraph) (map[string][]GroupVersion, error) {
	out := maps.Clone(local)
	grouped := map[string][]GroupVersion{}
	if len(graphs) > 0 {
		for id, vs := range local {
			grouped[id] = append([]GroupVersion{}, vs...)
		}
	}
	for _, v := range incoming {
		if _, ok := grouped[v.Group.ID]; !ok {
			grouped[v.Group.ID] = append([]GroupVersion{}, local[v.Group.ID]...)
		}
		grouped[v.Group.ID] = append(grouped[v.Group.ID], v)
	}
	for id, vs := range grouped {
		unique := map[string]GroupVersion{}
		for _, v := range vs {
			if old, ok := unique[v.Group.Revision]; ok && !equalJSON(old, v) {
				return nil, fail(400, "Immutable revision collision")
			}
			unique[v.Group.Revision] = v
		}
		heads := []GroupVersion{}
		for rev, v := range unique {
			dominated := false
			for _, other := range unique {
				if other.Group.Revision == rev {
					continue
				}
				proof := slices.Contains(other.Ancestors, rev)
				if len(graphs) > 0 {
					proof = graphs[0].reaches("group", other.Group.ID, other.Group.Revision, rev)
				}
				if proof {
					dominated = true
					break
				}
			}
			if !dominated {
				heads = append(heads, v)
			}
		}
		if len(heads) == 0 || len(graphs) == 0 && len(heads) > 32 {
			return nil, fail(400, "Invalid revision graph")
		}
		slices.SortFunc(heads, func(a, b GroupVersion) int { return stringsCompare(a.Group.Revision, b.Group.Revision) })
		out[id] = heads
	}
	if len(out) > maxGroups*maxClusterMembers {
		return nil, fail(409, "Group capacity exceeded")
	}
	return out, nil
}
func (n *Node) mergeSnapshot(s Snapshot) error {
	if err := n.applySnapshot(s); err != nil {
		return err
	}
	n.evaluate()
	return nil
}
func (n *Node) startSyncLocked() {
	if n.syncing || n.stopped || n.fault != nil {
		return
	}
	n.syncing = true
	n.nextSync = n.now() + n.settings.SyncIntervalMs
	n.launchLocked(n.syncPeers)
}
func (n *Node) syncPeers() {
	defer func() { n.mu.Lock(); n.syncing = false; n.mu.Unlock() }()
	var wg sync.WaitGroup
	for _, peer := range n.settings.Peers {
		wg.Add(1)
		go func(p Peer) {
			defer wg.Done()
			// A large backlog continues next tick, rather than occupying the
			// synchronizer indefinitely or restarting at the first page.
			for range syncPagesPerRound {
				if !n.exchangePage(p) {
					return
				}
			}
		}(peer)
	}
	wg.Wait()
}

// exchangePage returns whether either direction has more data in this sweep.
// Only an acknowledged, durably merged page advances either cursor.
func (n *Node) exchangePage(p Peer) bool {
	n.mu.Lock()
	if n.stopped || n.fault != nil {
		n.mu.Unlock()
		return false
	}
	progress := n.peerProgress[p.ID]
	page, err := n.snapshotPage(progress.Send, progress.Receive)
	var body []byte
	if err == nil {
		body, err = json.Marshal(page)
	}
	n.mu.Unlock()
	if err != nil || len(body) > maxBody {
		return false
	}
	ctx, cancel := context.WithTimeout(n.ctx, time.Duration(n.settings.PeerTimeoutMs)*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", p.URL+"/internal/snapshot", bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+n.clusterToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Canary-Node-Id", n.ID)
	resp, err := n.peerClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	b, err := readBounded(resp.Body, maxBody)
	if err != nil {
		return false
	}
	incoming, err := n.validateSnapshot(b, p.ID)
	if err != nil || incoming.Protocol != peerProtocol || incoming.Next != "" && incoming.Next <= progress.Receive {
		return false
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stopped || n.fault != nil || n.mergeSnapshot(incoming) != nil {
		return false
	}
	n.peerSeen[p.ID] = n.now()
	n.peerProgress[p.ID] = syncProgress{page.Next, incoming.Next}
	return page.Next != "" || incoming.Next != ""
}
