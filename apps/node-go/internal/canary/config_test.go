package canary

import (
	"encoding/json"
	"testing"
)

func TestMonitorConflictsCompleteResolutionAndTombstone(t *testing.T) {
	c := cluster(t, []string{"a", "b"}, nil)
	a, b := c.nodes[0], c.nodes[1]
	in := input([]string{"a", "b"}, "http://127.0.0.1:1", "")
	in.Enabled = false
	m := add(t, a, in)
	eventually(t, func() bool {
		v := getOverview(t, b)
		return len(v.Monitors) == 1 && v.Monitors[0].Monitor.Revision == m.Revision
	})
	c.split([]string{"a"}, []string{"b"})
	for i, name := range []string{"East edit", "West edit"} {
		edit := m
		edit.Name = name
		code, body := api(t, c.nodes[i], "/api/monitors/"+m.ID, "PUT", edit, adminToken)
		if code != 200 {
			t.Fatal(code, string(body))
		}
	}
	c.heal()
	eventually(t, func() bool {
		for _, n := range c.nodes {
			if !getOverview(t, n).Monitors[0].ConfigConflict {
				return false
			}
		}
		return true
	})
	code, body := api(t, a, "/api/monitors/"+m.ID+"/conflicts", "GET", nil, adminToken)
	var heads []Monitor
	if code != 200 || json.Unmarshal(body, &heads) != nil || len(heads) != 2 {
		t.Fatal(code, string(body))
	}
	code, _ = api(t, a, "/api/monitors/"+m.ID, "PUT", heads[0], adminToken)
	if code != 409 {
		t.Fatal(code)
	}
	encoded, _ := json.Marshal(heads[0])
	var resolution map[string]any
	_ = json.Unmarshal(encoded, &resolution)
	resolution["revisions"] = []string{heads[0].Revision}
	code, _ = api(t, a, "/api/monitors/"+m.ID+"/resolve", "POST", resolution, adminToken)
	if code != 409 {
		t.Fatal(code)
	}
	resolution["revisions"] = []string{heads[0].Revision, heads[1].Revision}
	code, body = api(t, a, "/api/monitors/"+m.ID+"/resolve", "POST", resolution, adminToken)
	var resolved Monitor
	if code != 200 || json.Unmarshal(body, &resolved) != nil {
		t.Fatal(code, string(body))
	}
	eventually(t, func() bool { return getOverview(t, b).Monitors[0].Monitor.Revision == resolved.Revision })
	code, _ = api(t, b, "/api/monitors/"+m.ID+"/resolve", "POST", resolution, adminToken)
	if code != 409 {
		t.Fatal(code)
	}
	code, _ = api(t, a, "/api/monitors/"+m.ID, "DELETE", map[string]string{"revision": resolved.Revision}, adminToken)
	if code != 204 {
		t.Fatal(code)
	}
	eventually(t, func() bool { return len(getOverview(t, b).Monitors) == 0 })
	code, body = api(t, b, "/api/monitors/"+m.ID+"/conflicts", "GET", nil, adminToken)
	_ = json.Unmarshal(body, &heads)
	if code != 200 || len(heads) != 1 || !heads[0].Deleted {
		t.Fatal(code, string(body))
	}
	code, _ = api(t, b, "/api/monitors/"+m.ID, "PUT", resolved, adminToken)
	if code != 409 {
		t.Fatal(code)
	}
}
func TestConcurrentDeleteAndEditRequireExplicitResolution(t *testing.T) {
	c := cluster(t, []string{"a", "b"}, nil)
	a, b := c.nodes[0], c.nodes[1]
	in := input([]string{"a", "b"}, "http://127.0.0.1:1", "")
	in.Enabled = false
	m := add(t, a, in)
	eventually(t, func() bool { return len(getOverview(t, b).Monitors) == 1 })
	c.split([]string{"a"}, []string{"b"})
	code, _ := api(t, a, "/api/monitors/"+m.ID, "DELETE", map[string]string{"revision": m.Revision}, adminToken)
	if code != 204 {
		t.Fatal(code)
	}
	m.Name = "Keep edit"
	code, _ = api(t, b, "/api/monitors/"+m.ID, "PUT", m, adminToken)
	if code != 200 {
		t.Fatal(code)
	}
	c.heal()
	eventually(t, func() bool {
		for _, n := range c.nodes {
			v := getOverview(t, n)
			if len(v.Monitors) != 1 || !v.Monitors[0].ConfigConflict {
				return false
			}
		}
		return true
	})
	_, body := api(t, a, "/api/monitors/"+m.ID+"/conflicts", "GET", nil, adminToken)
	var heads []Monitor
	_ = json.Unmarshal(body, &heads)
	deleted := 0
	revs := []string{}
	for _, h := range heads {
		if h.Deleted {
			deleted++
		}
		revs = append(revs, h.Revision)
	}
	if deleted != 1 {
		t.Fatal(heads)
	}
	encoded, _ := json.Marshal(m)
	var resolution map[string]any
	_ = json.Unmarshal(encoded, &resolution)
	resolution["revisions"] = revs
	code, _ = api(t, a, "/api/monitors/"+m.ID+"/resolve", "POST", resolution, adminToken)
	if code != 200 {
		t.Fatal(code)
	}
}
