package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
)

func maintenanceNeedsPreflight(path, method string) bool {
	if path == "/api/maintenance" {
		return method == "POST"
	}
	if !strings.HasPrefix(path, "/api/maintenance/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/maintenance/"), "/")
	return len(parts) == 1 && method == "PUT" || len(parts) == 2 && parts[1] == "resolve" && method == "POST"
}

func (n *Node) prepareMaintenanceRequest(ctx context.Context, b []byte) (compiledMaintenance, error) {
	in, err := parseMaintenance(b)
	if err != nil {
		return nil, err
	}
	p, err := n.compileMaintenance(ctx, in.Schedule)
	if err != nil {
		return nil, err
	}
	return compiledMaintenance{maintenancePlanKey(in.Schedule): p}, nil
}

func (n *Node) maintenanceReferences(in MaintenanceInput) error {
	for _, id := range in.MonitorIDs {
		if n.current(id) == nil {
			return fail(400, "monitorIds must reference existing unconflicted monitors")
		}
	}
	for _, id := range in.GroupIDs {
		if n.currentGroup(id) == nil {
			return fail(400, "groupIds must reference existing unconflicted groups")
		}
	}
	return nil
}

func (n *Node) maintenanceAPI(r *http.Request, a account, b []byte) (int, any, error) {
	path, method := r.URL.Path, r.Method
	prepared, _ := r.Context().Value(maintenanceRequestPlansKey{}).(compiledMaintenance)
	if path == "/api/maintenance" {
		if method == "GET" {
			return 200, n.maintenanceViews(), nil
		}
		if method != "POST" {
			return 0, nil, fail(404, "Not found")
		}
		if len(n.resources) >= maxResources {
			return 0, nil, fail(409, "Resource capacity exceeded")
		}
		count := 0
		for _, heads := range n.resources {
			if len(heads) != 0 && heads[0].Resource.Kind == resourceMaintenance {
				count++
			}
		}
		if count >= maxMaintenances {
			return 0, nil, fail(409, "Maintenance capacity exceeded")
		}
		in, err := parseMaintenance(b)
		if err != nil {
			return 0, nil, err
		}
		if err = n.maintenanceReferences(in); err != nil {
			return 0, nil, err
		}
		saved, err := n.writeResource(resourceMaintenance, "", in, false, prepared)
		return 201, maintenanceOf(saved), err
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/maintenance/"), "/")
	if len(parts) < 1 || len(parts) > 2 || identifier(parts[0], "id") != nil {
		return 0, nil, fail(404, "Not found")
	}
	id := parts[0]
	heads := n.resources[resourceKey(resourceMaintenance, id)]
	if len(heads) == 0 {
		return 0, nil, fail(404, "Maintenance not found")
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	if method == "GET" && action == "conflicts" {
		out := []Maintenance{}
		for _, h := range heads {
			out = append(out, maintenanceOf(h.Resource))
		}
		return 200, out, nil
	}
	if method == "GET" && action == "" && len(heads) == 1 && !heads[0].Resource.Deleted {
		return 200, maintenanceOf(heads[0].Resource), nil
	}
	if a.Role == "viewer" {
		return 0, nil, fail(403, "Insufficient permissions")
	}
	if !(action == "resolve" && method == "POST" || action == "" && (method == "PUT" || method == "DELETE")) {
		return 0, nil, fail(404, "Not found")
	}
	var rev struct {
		Revision  string   `json:"revision"`
		Revisions []string `json:"revisions"`
	}
	if json.Unmarshal(b, &rev) != nil {
		return 0, nil, fail(400, "Invalid maintenance revision")
	}
	if action == "resolve" {
		if len(heads) < 2 || len(rev.Revisions) != len(heads) || len(unique(rev.Revisions)) != len(heads) {
			return 0, nil, fail(409, "Resolution must name every current head exactly once")
		}
		for _, h := range heads {
			if !slices.Contains(rev.Revisions, h.Resource.Revision) {
				return 0, nil, fail(409, "Resolution must name every current head exactly once")
			}
		}
	} else if len(heads) != 1 || heads[0].Resource.Revision != rev.Revision {
		return 0, nil, fail(409, "Maintenance revision is stale or conflicting")
	} else if heads[0].Resource.Deleted {
		return 0, nil, fail(404, "Maintenance was deleted")
	}
	if method == "DELETE" {
		_, err := n.writeResource(resourceMaintenance, id, maintenanceOf(heads[0].Resource).MaintenanceInput, true)
		return 204, nil, err
	}
	in, err := parseMaintenance(b)
	if err != nil {
		return 0, nil, err
	}
	if err = n.maintenanceReferences(in); err != nil {
		return 0, nil, err
	}
	saved, err := n.writeResource(resourceMaintenance, id, in, false, prepared)
	return 200, maintenanceOf(saved), err
}
