package canary

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"time"
	maintenanceEngine "uptime-canary/apps/node-go/internal/maintenance"
)

const resourceMaintenance = "maintenance"
const maxMaintenances = 64

type MaintenanceInput struct {
	Name        string                   `json:"name"`
	Enabled     bool                     `json:"enabled"`
	AllMonitors bool                     `json:"allMonitors,omitempty"`
	MonitorIDs  []string                 `json:"monitorIds,omitempty"`
	GroupIDs    []string                 `json:"groupIds,omitempty"`
	Schedule    maintenanceEngine.Config `json:"schedule"`
}

type Maintenance struct {
	MaintenanceInput
	ID        string `json:"id"`
	Revision  string `json:"revision"`
	UpdatedAt int64  `json:"updatedAt"`
	Deleted   bool   `json:"deleted,omitempty"`
}

type MaintenanceView struct {
	Maintenance Maintenance `json:"maintenance"`
	maintenanceEngine.State
	ConfigConflict bool   `json:"configConflict"`
	Reason         string `json:"reason"`
}

type compiledMaintenance map[string]*maintenanceEngine.Plan
type maintenanceRequestPlansKey struct{}
type maintenanceWindowCache struct {
	At, Next int64
	Views    []MaintenanceView
}

// Shape-only: expensive feasibility/recurrence validation is performed by
// Compile outside the node lock, before a resource is admitted or merged.
func parseMaintenance(b []byte) (MaintenanceInput, error) {
	var in MaintenanceInput
	raw, err := jsonObject(b)
	if err != nil || required(raw, "name", "enabled", "schedule") != nil || optionalNonNull(raw, "allMonitors", "monitorIds", "groupIds") != nil {
		return in, fail(400, "Invalid maintenance input")
	}
	if json.Unmarshal(raw["name"], &in.Name) != nil || json.Unmarshal(raw["enabled"], &in.Enabled) != nil || validText(in.Name, "maintenance name", 128) != nil {
		return in, fail(400, "Invalid maintenance name or enabled flag")
	}
	if raw["allMonitors"] != nil && json.Unmarshal(raw["allMonitors"], &in.AllMonitors) != nil {
		return in, fail(400, "Invalid allMonitors flag")
	}
	for _, entry := range []struct {
		key    string
		target *[]string
	}{{"monitorIds", &in.MonitorIDs}, {"groupIds", &in.GroupIDs}} {
		if raw[entry.key] != nil && (json.Unmarshal(raw[entry.key], entry.target) != nil || !validIDs(*entry.target, 200) || len(unique(*entry.target)) != len(*entry.target)) {
			return in, fail(400, "Invalid maintenance selection")
		}
	}
	if in.AllMonitors && len(in.MonitorIDs)+len(in.GroupIDs) > 0 || !in.AllMonitors && len(in.MonitorIDs)+len(in.GroupIDs) == 0 {
		return in, fail(400, "Choose allMonitors or explicit monitor/group IDs")
	}
	d := json.NewDecoder(bytes.NewReader(raw["schedule"]))
	d.DisallowUnknownFields()
	if d.Decode(&in.Schedule) != nil {
		return in, fail(400, "Invalid maintenance schedule")
	}
	return in, nil
}

func maintenancePlanKey(c maintenanceEngine.Config) string {
	b, _ := json.Marshal(c)
	return digest(string(b))
}
func (n *Node) currentMaintenancePlans() compiledMaintenance {
	if p := n.maintenancePlans.Load(); p != nil {
		return *p
	}
	return compiledMaintenance{}
}

func (n *Node) compileMaintenance(ctx context.Context, c maintenanceEngine.Config) (*maintenanceEngine.Plan, error) {
	if p := n.currentMaintenancePlans()[maintenancePlanKey(c)]; p != nil {
		return p, nil
	}
	select {
	case n.maintenanceCompiler <- struct{}{}:
		defer func() { <-n.maintenanceCompiler }()
	case <-ctx.Done():
		return nil, fail(503, "Maintenance validation cancelled")
	default:
		return nil, fail(503, "Maintenance validator is busy; retry shortly")
	}
	p, err := maintenanceEngine.Compile(c)
	if err != nil {
		return nil, fail(400, err.Error())
	}
	return p, nil
}

// Build the next immutable cache under mu using only precompiled plans. The
// atomic published map is never mutated and can be consulted during preflight.
func (n *Node) maintenancePlansFor(resources map[string][]ResourceVersion, prepared compiledMaintenance) (compiledMaintenance, error) {
	old := n.currentMaintenancePlans()
	out := compiledMaintenance{}
	count := 0
	for _, heads := range resources {
		if len(heads) == 0 || heads[0].Resource.Kind != resourceMaintenance {
			continue
		}
		count++
		if count > maxMaintenances*maxClusterMembers {
			return nil, fail(409, "Maintenance capacity exceeded")
		}
		if len(heads) != 1 || heads[0].Resource.Deleted {
			continue
		}
		in, err := parseMaintenance(heads[0].Resource.Data)
		if err != nil {
			return nil, err
		}
		key := maintenancePlanKey(in.Schedule)
		p := prepared[key]
		if p == nil {
			p = old[key]
		}
		if p == nil {
			return nil, fail(400, "Maintenance schedule requires preflight validation")
		}
		out[key] = p
	}
	return out, nil
}

func (n *Node) initializeMaintenancePlans() error {
	n.maintenanceCompiler = make(chan struct{}, 1)
	prepared := compiledMaintenance{}
	for _, heads := range n.resources {
		if len(heads) != 1 || heads[0].Resource.Kind != resourceMaintenance || heads[0].Resource.Deleted {
			continue
		}
		in, err := parseMaintenance(heads[0].Resource.Data)
		if err != nil {
			return err
		}
		key := maintenancePlanKey(in.Schedule)
		if prepared[key] != nil {
			continue
		}
		p, err := n.compileMaintenance(context.Background(), in.Schedule)
		if err != nil {
			return err
		}
		prepared[key] = p
	}
	cache, err := n.maintenancePlansFor(n.resources, prepared)
	if err == nil {
		n.maintenancePlans.Store(&cache)
	}
	return err
}

func maintenanceOf(r Resource) Maintenance {
	in, _ := parseMaintenance(r.Data)
	return Maintenance{MaintenanceInput: in, ID: r.ID, Revision: r.Revision, UpdatedAt: r.UpdatedAt, Deleted: r.Deleted}
}

func (n *Node) maintenanceViews() []MaintenanceView {
	now := n.now()
	if cache := n.maintenanceWindows; cache != nil && now >= cache.At && now < cache.Next {
		return cache.Views
	}
	cache := &maintenanceWindowCache{At: now, Next: maxSafe, Views: []MaintenanceView{}}
	plans := n.currentMaintenancePlans()
	for _, heads := range n.resources {
		var r *Resource
		for _, h := range heads {
			if h.Resource.Kind == resourceMaintenance && !h.Resource.Deleted {
				copy := h.Resource
				r = &copy
				break
			}
		}
		if r == nil {
			continue
		}
		m := maintenanceOf(*r)
		ready := len(heads) == 1 && n.resourceVersionReady(heads[0])
		v := MaintenanceView{Maintenance: m, ConfigConflict: len(heads) != 1 || !ready}
		switch {
		case len(heads) == 1 && !ready:
			v.Reason = "Maintenance is awaiting causal revision evidence and is not applied"
		case v.ConfigConflict:
			v.Reason = "Maintenance has conflicting revisions and is not applied"
		case !m.Enabled:
			v.Reason = "Maintenance is disabled"
		default:
			p := plans[maintenancePlanKey(m.Schedule)]
			if p == nil {
				v.Reason = "Maintenance schedule is unavailable"
				break
			}
			state, err := p.Evaluate(time.UnixMilli(now))
			if err != nil {
				v.Reason = "Maintenance cannot be evaluated"
				break
			}
			v.State = state
			if state.Active {
				v.Reason = "Scheduled maintenance is active"
			} else {
				v.Reason = "Outside scheduled maintenance"
			}
			for _, boundary := range []*time.Time{state.End, state.NextStart} {
				if boundary != nil && boundary.UnixMilli() > now {
					cache.Next = min(cache.Next, boundary.UnixMilli())
				}
			}
		}
		cache.Views = append(cache.Views, v)
	}
	slices.SortFunc(cache.Views, func(a, b MaintenanceView) int { return stringsCompare(a.Maintenance.Name, b.Maintenance.Name) })
	n.maintenanceWindows = cache
	return cache.Views
}

func (n *Node) activeMaintenance(m Monitor) []string {
	ids := []string{}
	for _, v := range n.maintenanceViews() {
		if !v.Active {
			continue
		}
		in := v.Maintenance
		if n.maintenanceSelects(in, m) {
			ids = append(ids, in.ID)
		}
	}
	return ids
}

func (n *Node) maintenanceSelects(in Maintenance, m Monitor) bool {
	return in.AllMonitors || slices.Contains(in.MonitorIDs, m.ID) ||
		m.GroupID != "" && n.currentGroup(m.GroupID) != nil && slices.Contains(in.GroupIDs, m.GroupID)
}

// Completion is conservative: only unchanged schedules already configured at
// opening can prove that the entire observed outage was planned. A new or edited
// schedule still holds delivery now, but cannot erase an earlier owed outage.
// Chained overlapping schedules are bounded; uncertain history fails open.
func (n *Node) maintenanceCovers(m Monitor, i Incident, end int64) bool {
	if i.MaintenanceStartedAt == nil || *i.MaintenanceStartedAt > i.OpenedAt || end < i.OpenedAt ||
		i.MaintenanceReleasedAt != nil && *i.MaintenanceReleasedAt < end {
		return false
	}
	plans := []*maintenanceEngine.Plan{}
	cache := n.currentMaintenancePlans()
	for _, heads := range n.resources {
		if len(heads) != 1 || !n.resourceVersionReady(heads[0]) || heads[0].Resource.Kind != resourceMaintenance || heads[0].Resource.Deleted || heads[0].Resource.UpdatedAt > i.OpenedAt {
			continue
		}
		in := maintenanceOf(heads[0].Resource)
		if in.Enabled && n.maintenanceSelects(in, m) {
			if p := cache[maintenancePlanKey(in.Schedule)]; p != nil {
				plans = append(plans, p)
			}
		}
	}
	cursor := i.OpenedAt
	for range 128 {
		next := cursor
		for _, p := range plans {
			state, err := p.Evaluate(time.UnixMilli(cursor))
			if err == nil && state.Active && state.End != nil {
				next = max(next, state.End.UnixMilli())
			}
		}
		if next <= cursor {
			return false
		}
		if next >= end {
			return true
		}
		cursor = next
	}
	return false
}

func (n *Node) recordMaintenance(m Monitor, i Incident, opening bool) Incident {
	ids := n.activeMaintenance(m)
	if len(ids) != 0 {
		i.MaintenanceIDs = unique(append(append([]string{}, i.MaintenanceIDs...), ids...))
		if len(i.MaintenanceIDs) > maxMaintenances {
			i.MaintenanceIDs = i.MaintenanceIDs[:maxMaintenances]
		}
		if i.MaintenanceStartedAt == nil {
			i.MaintenanceStartedAt = Ptr(n.now())
			if opening {
				candidate := i
				candidate.MaintenanceStartedAt = Ptr(i.OpenedAt)
				if n.maintenanceCovers(m, candidate, n.now()) {
					i.MaintenanceStartedAt = candidate.MaintenanceStartedAt
				}
			}
		}
	} else if i.MaintenanceStartedAt != nil && (i.MaintenanceReleasedAt == nil || *i.MaintenanceReleasedAt < *i.MaintenanceStartedAt) {
		i.MaintenanceReleasedAt = Ptr(n.now())
	}
	return i
}

func maintenanceCombine(a, b Incident, out *Incident) {
	out.MaintenanceIDs = unique(append(append([]string{}, a.MaintenanceIDs...), b.MaintenanceIDs...))
	if len(out.MaintenanceIDs) > maxMaintenances {
		out.MaintenanceIDs = out.MaintenanceIDs[:maxMaintenances]
	}
	if b.MaintenanceStartedAt != nil && (out.MaintenanceStartedAt == nil || *b.MaintenanceStartedAt < *out.MaintenanceStartedAt) {
		out.MaintenanceStartedAt = Ptr(*b.MaintenanceStartedAt)
	}
	if b.MaintenanceReleasedAt != nil && (out.MaintenanceReleasedAt == nil || *b.MaintenanceReleasedAt > *out.MaintenanceReleasedAt) {
		out.MaintenanceReleasedAt = Ptr(*b.MaintenanceReleasedAt)
	}
	// Earlier sibling evidence can expand the outage beyond the interval for
	// which a completion was established. Such a claim cannot discard owed work.
	out.MaintenanceComplete = (a.MaintenanceComplete || b.MaintenanceComplete) &&
		out.ResolvedAt != nil && out.MaintenanceStartedAt != nil && *out.MaintenanceStartedAt <= out.OpenedAt &&
		(out.MaintenanceReleasedAt == nil || *out.MaintenanceReleasedAt >= *out.ResolvedAt)
}
