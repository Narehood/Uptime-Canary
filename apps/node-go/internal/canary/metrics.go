package canary

import (
	"slices"
	"strconv"
	"strings"
)

type metricsResponse string

// Text exposition 0.0.4 has different escapes from JSON/Go string literals.
func metricLabel(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(value) + `"`
}

// Called under the same state lock as overview. Metrics contain identifiers and
// fixed state labels, never endpoint URLs, provider responses or credentials.
func (n *Node) metrics() metricsResponse {
	var b strings.Builder
	help := func(name, description string) {
		b.WriteString("# HELP uptime_canary_" + name + " " + description + "\n# TYPE uptime_canary_" + name + " gauge\n")
	}
	sample := func(name, labels string, value float64) {
		b.WriteString("uptime_canary_" + name + "{" + labels + "} " + strconv.FormatFloat(value, 'g', -1, 64) + "\n")
	}
	view := "view_node=" + metricLabel(n.ID)
	monitors := n.monitorViews("viewer")
	help("monitor_state", "One current state per configured monitor, as observed by this node.")
	for _, v := range monitors {
		sample("monitor_state", view+",monitor_id="+metricLabel(v.Monitor.ID)+",state="+metricLabel(v.State), 1)
	}
	help("observation_state", "Latest fresh assigned-node measurement; missing or stale is unknown.")
	for _, v := range monitors {
		for _, o := range v.Observations {
			sample("observation_state", view+",monitor_id="+metricLabel(v.Monitor.ID)+",probe_node="+metricLabel(o.NodeID)+",state="+metricLabel(o.State), 1)
		}
	}
	help("observation_latency_milliseconds", "Latency of fresh measurements only; unknown measurements have no latency sample.")
	for _, v := range monitors {
		for _, o := range v.Observations {
			if o.State != "unknown" && (o.LatencyAvailable == nil || *o.LatencyAvailable) {
				sample("observation_latency_milliseconds", view+",monitor_id="+metricLabel(v.Monitor.ID)+",probe_node="+metricLabel(o.NodeID), o.LatencyMs)
			}
		}
	}
	help("certificate_days_remaining", "Remaining certificate lifetime from a fresh measurement; verified identifies trust validation.")
	for _, v := range monitors {
		for _, o := range v.Observations {
			if o.State != "unknown" && o.Certificate != nil {
				sample("certificate_days_remaining", view+",monitor_id="+metricLabel(v.Monitor.ID)+",probe_node="+metricLabel(o.NodeID)+",verified="+metricLabel(strconv.FormatBool(o.Certificate.Verified)), float64(o.Certificate.DaysRemaining))
			}
		}
	}
	help("monitor_maintenance_active", "One when planned work currently holds this monitor's notifications.")
	for _, v := range monitors {
		active := float64(0)
		if len(v.MaintenanceIDs) != 0 {
			active = 1
		}
		sample("monitor_maintenance_active", view+",monitor_id="+metricLabel(v.Monitor.ID), active)
	}
	help("peer_reachable", "Whether this node recently exchanged an authenticated page with the configured peer.")
	peers := append([]Peer{}, n.settings.Peers...)
	slices.SortFunc(peers, func(a, b Peer) int { return stringsCompare(a.ID, b.ID) })
	for _, peer := range peers {
		reachable := float64(0)
		if n.connected(peer.ID) {
			reachable = 1
		}
		sample("peer_reachable", view+",peer_node="+metricLabel(peer.ID), reachable)
	}
	return metricsResponse(b.String())
}
