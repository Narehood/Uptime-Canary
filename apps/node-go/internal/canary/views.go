package canary

import "slices"

func publicMonitor(m Monitor, role string) Monitor {
	m.NodeIDs = append([]string{}, m.NodeIDs...)
	if role == "viewer" {
		m.Target = globalpingPublicTarget(m)
		m.Globalping = nil
		m.Steam = nil
		m.PM2 = nil
		m.WebhookURL = ""
		m.Service = nil
		if m.Push != nil {
			o := *m.Push
			o.Token = ""
			m.Push = &o
		}
		if m.HTTP != nil {
			o := *m.HTTP
			o.Headers = nil
			o.Body = ""
			o.BasicAuth = nil
			o.BearerToken = ""
			o.OAuth2 = nil
			o.JSONata = nil
			o.ProxyURL = ""
			m.HTTP = &o
		}
		if m.WebSocket != nil {
			o := *m.WebSocket
			o.Headers = nil
			o.BasicAuth = nil
			o.BearerToken = ""
			o.ProxyURL = ""
			m.WebSocket = &o
		}
		if m.TLS != nil {
			o := *m.TLS
			o.ClientCertPEM = ""
			o.ClientKeyPEM = ""
			o.CAPEM = ""
			m.TLS = &o
		}
	}
	return m
}
func (n *Node) monitorViews(role string) []MonitorView {
	out := []MonitorView{}
	for _, heads := range n.heads {
		var m *Monitor
		for _, v := range heads {
			if !v.Monitor.Deleted {
				copy := v.Monitor
				m = &copy
				break
			}
		}
		if m == nil {
			continue
		}
		configConflict := len(heads) > 1 || !n.monitorVersionReady(heads[0])
		fresh := n.fresh(*m)
		observations := []Observation{}
		for _, id := range m.NodeIDs {
			var valid *Observation
			for _, s := range fresh {
				if s.NodeID == id {
					copy := s.Observation
					valid = &copy
				}
			}
			if valid != nil {
				observations = append(observations, *valid)
				continue
			}
			o := Observation{MonitorID: m.ID, NodeID: id, State: "unknown", Message: "No observation yet"}
			if prev, ok := n.samples[m.ID+"/"+id]; ok {
				o.CheckedAt = prev.CheckedAt
				o.LatencyMs = prev.LatencyMs
				o.Message = "Observation is stale or belongs to an earlier revision"
			}
			observations = append(observations, o)
		}
		unknown := len(m.NodeIDs) - len(fresh)
		for _, s := range fresh {
			if s.State == "unknown" {
				unknown++
			}
		}
		state := "up"
		if configConflict {
			state = "unknown"
		} else if !m.Enabled {
			state = "paused"
		} else {
			if unknown > 0 {
				state = "unknown"
			}
			for _, s := range fresh {
				if s.State == "pending" && state == "up" {
					state = "pending"
				}
				if s.State == "down" {
					state = "down"
				}
			}
		}
		group, reason, _ := n.suppression(*m)
		if state != "down" {
			group = ""
			reason = ""
		}
		out = append(out, MonitorView{Monitor: publicMonitor(*m, role), State: state, Observations: observations, UnknownNodes: unknown, ConfigConflict: configConflict, SuppressedByGroupID: group, SuppressionReason: reason, MaintenanceIDs: n.activeMaintenance(*m)})
	}
	slices.SortFunc(out, func(a, b MonitorView) int { return stringsCompare(a.Monitor.Name, b.Monitor.Name) })
	return out
}
func (n *Node) overview(a account) Overview {
	now := n.now()
	monitors := n.monitorViews(a.Role)
	out := Overview{Node: Identity{n.ID, n.Name}, Now: now, Monitors: monitors, Groups: n.groupViews(monitors), Nodes: []NodeView{{ID: n.ID, Name: n.Name, State: "local", LastSeenAt: Ptr(now)}}, Incidents: []IncidentView{}, Notifications: []NotificationView{}, Maintenance: n.maintenanceViews()}
	out.User.Name = a.Name
	out.User.Role = a.Role
	for _, p := range n.settings.Peers {
		name := p.Name
		if name == "" {
			name = p.ID
		}
		state := "unreachable"
		if n.connected(p.ID) {
			state = "connected"
		}
		var last *int64
		if v, ok := n.peerSeen[p.ID]; ok {
			last = Ptr(v)
		}
		out.Nodes = append(out.Nodes, NodeView{p.ID, name, p.URL, state, last})
	}
	for _, i := range n.episodes {
		var down, recovery *int64
		for _, r := range n.forEpisode(i) {
			if r.Kind == "down" && (down == nil || r.SentAt < *down) {
				down = Ptr(r.SentAt)
			}
			if r.Kind == "recovery" && (recovery == nil || r.SentAt < *recovery) {
				recovery = Ptr(r.SentAt)
			}
		}
		group, reason := "", ""
		if i.SuppressedComplete && down == nil {
			group = i.SuppressedByGroupID
			reason = i.SuppressionReason
		} else if m := n.current(i.MonitorID); m != nil && i.ResolvedAt == nil {
			group, reason, _ = n.suppression(*m)
		}
		retirementReason := ""
		if i.RetiredBy != "" {
			retirementReason = "Monitoring semantics changed; incident retired without recovery"
		}
		out.Incidents = append(out.Incidents, IncidentView{ID: i.ID, MonitorID: i.MonitorID, MonitorName: i.MonitorName, OpenedAt: i.OpenedAt, ResolvedAt: i.ResolvedAt, NotifiedAt: down, RecoveryNotifiedAt: recovery, SuppressedByGroupID: group, SuppressionReason: reason, RetiredBy: i.RetiredBy, RetirementReason: retirementReason, MaintenanceIDs: i.MaintenanceIDs, MaintenanceComplete: i.MaintenanceComplete})
	}
	for _, r := range n.receipts {
		v := r.NotificationView
		for _, i := range n.episodes {
			if slices.Contains(episodeIDs(i), r.IncidentID) {
				v.IncidentID = i.ID
				break
			}
		}
		out.Notifications = append(out.Notifications, v)
	}
	slices.SortFunc(out.Incidents, func(a, b IncidentView) int {
		if a.OpenedAt > b.OpenedAt {
			return -1
		}
		if a.OpenedAt < b.OpenedAt {
			return 1
		}
		return stringsCompare(a.ID, b.ID)
	})
	slices.SortFunc(out.Notifications, func(a, b NotificationView) int {
		if a.SentAt > b.SentAt {
			return -1
		}
		if a.SentAt < b.SentAt {
			return 1
		}
		return stringsCompare(a.ID, b.ID)
	})
	return out
}
