package canary

import "slices"

func checkGeneration(m Monitor) string {
	if m.CheckGeneration != "" {
		return m.CheckGeneration
	}
	return m.Revision
}

func sampleGeneration(s Sample) string {
	if s.CheckGeneration != "" {
		return s.CheckGeneration
	}
	return s.Revision
}

// Scheduling and delivery metadata do not change what is being checked.
// Timeout changes can change the result, so they intentionally start anew.
// Manual status/message edits are observations chosen by an operator; they
// preserve continuity so setting UP can recover the existing manual outage.
func sameCheck(a, b MonitorInput) bool {
	return a.Type == b.Type && a.Target == b.Target && a.TimeoutMs == b.TimeoutMs &&
		(a.Type != "push" || a.IntervalMs == b.IntervalMs) && equalJSON(a.Push, b.Push) &&
		a.Enabled == b.Enabled && slices.Equal(a.NodeIDs, b.NodeIDs) &&
		slices.Equal(a.ExpectedStatusCodes, b.ExpectedStatusCodes) && a.UpsideDown == b.UpsideDown && a.Retries == b.Retries &&
		equalJSON(a.HTTP, b.HTTP) && equalJSON(a.TLS, b.TLS) && equalJSON(a.DNS, b.DNS) && equalJSON(a.WebSocket, b.WebSocket) && equalJSON(a.SMTP, b.SMTP) && equalJSON(a.Service, b.Service) && equalJSON(a.Ping, b.Ping) && equalJSON(a.Globalping, b.Globalping) && equalJSON(a.Steam, b.Steam) && equalJSON(a.PM2, b.PM2)
}

// Only a unique, causally later head can retire an old generation. A conflict
// suspends evaluation; metadata-only conflict resolution can retain continuity.
// Repeating this after every merge also retires delayed old incident pages.
func (n *Node) retireSuperseded() {
	for id, heads := range n.heads {
		if len(heads) != 1 || !n.monitorVersionReady(heads[0]) {
			continue
		}
		v := heads[0]
		for _, i := range n.episodes {
			if i.MonitorID != id || i.Closed != nil || i.Revision == checkGeneration(v.Monitor) || !n.graph.reaches("monitor", id, v.Monitor.Revision, i.Revision) {
				continue
			}
			i.RetiredBy = v.Monitor.Revision
			i.ResolvedAt = Ptr(n.now())
			i.Closed = map[string]int64{}
			for node, runs := range i.Runs {
				for _, run := range runs {
					i.Closed[node] = max(i.Closed[node], run)
				}
			}
			n.saveEpisode(i)
		}
	}
}
