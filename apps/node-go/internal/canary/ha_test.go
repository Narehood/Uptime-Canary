package canary

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func allState(t *testing.T, c *testCluster, state string) bool {
	for _, n := range c.nodes {
		v := getOverview(t, n)
		if len(v.Monitors) != 1 || v.Monitors[0].State != state {
			return false
		}
	}
	return true
}
func TestThreeHTTPNodesSuccessiveOutages(t *testing.T) {
	var status atomic.Int64
	status.Store(200)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(int(status.Load())) }))
	defer target.Close()
	r := webhook(t)
	c := cluster(t, []string{"a", "b", "c"}, nil)
	add(t, c.nodes[0], input([]string{"a", "b", "c"}, target.URL, r.URL))
	eventually(t, func() bool { return allState(t, c, "up") })
	status.Store(503)
	eventually(t, func() bool { return r.count("down") == 1 })
	time.Sleep(500 * time.Millisecond)
	if r.count("") != 1 {
		t.Fatal(r.all())
	}
	eventually(t, func() bool {
		for _, n := range c.nodes {
			if len(getOverview(t, n).Notifications) != 1 {
				return false
			}
		}
		return true
	})
	first := r.all()[0].IncidentID
	status.Store(200)
	eventually(t, func() bool { return r.count("recovery") == 1 })
	status.Store(503)
	eventually(t, func() bool { return r.count("down") == 2 })
	time.Sleep(450 * time.Millisecond)
	ds := r.all()
	if len(ds) != 3 || ds[2].IncidentID == first {
		t.Fatal(ds)
	}
}
func TestPreferredSenderDeathAndProviderIsolation(t *testing.T) {
	for _, kill := range []bool{true, false} {
		t.Run(fmt.Sprint(kill), func(t *testing.T) {
			r := webhook(t)
			var down atomic.Bool
			c := cluster(t, []string{"a", "b", "c"}, func(o *Options) {
				o.Probe = controlled(&down)
				if !kill && o.NodeID == "a" {
					o.Client = doFunc(func(*http.Request) (*http.Response, error) { return nil, fmt.Errorf("Provider isolated") })
				}
			})
			add(t, c.nodes[0], input([]string{"a", "b", "c"}, "http://127.0.0.1:1", r.URL))
			eventually(t, func() bool { return allState(t, c, "up") })
			if kill {
				if err := c.nodes[0].Stop(); err != nil {
					t.Fatal(err)
				}
			}
			down.Store(true)
			eventually(t, func() bool { return r.count("down") == 1 })
			if r.all()[0].Node.ID != "b" {
				t.Fatal(r.all())
			}
			time.Sleep(650 * time.Millisecond)
			if len(r.all()) != 1 {
				t.Fatal(r.all())
			}
		})
	}
}
func TestPartitionReconcilesAliasesAndRecovery(t *testing.T) {
	var down atomic.Bool
	r := webhook(t)
	c := cluster(t, []string{"a", "b", "c"}, func(o *Options) { o.Probe = controlled(&down) })
	add(t, c.nodes[0], input([]string{"a", "b", "c"}, "http://127.0.0.1:1", r.URL))
	eventually(t, func() bool { return allState(t, c, "up") })
	c.split([]string{"a"}, []string{"b", "c"})
	time.Sleep(200 * time.Millisecond)
	down.Store(true)
	eventually(t, func() bool { return r.count("down") == 2 })
	c.heal()
	eventually(t, func() bool {
		for _, n := range c.nodes {
			v := getOverview(t, n)
			if len(v.Incidents) != 1 || len(v.Notifications) != 2 {
				return false
			}
		}
		return true
	})
	time.Sleep(450 * time.Millisecond)
	if r.count("") != 2 {
		t.Fatal(r.all())
	}
	down.Store(false)
	eventually(t, func() bool { return r.count("recovery") == 1 })
	ds := r.all()
	for _, d := range ds[:2] {
		found := false
		for _, alias := range ds[2].Incident.Aliases {
			found = found || alias == d.IncidentID
		}
		if !found {
			b, _ := json.Marshal(ds)
			t.Fatal(string(b))
		}
	}
	down.Store(true)
	eventually(t, func() bool { return r.count("down") == 3 })
	time.Sleep(500 * time.Millisecond)
	if r.count("") != 4 {
		t.Fatal(r.all())
	}
}
func TestUnknownAssignedNodeCannotAuthorizeRecovery(t *testing.T) {
	var down atomic.Bool
	down.Store(true)
	r := webhook(t)
	n := local(t, func(o *Options) { o.Peers = []Peer{{ID: "b", URL: "http://127.0.0.1:1"}}; o.Probe = controlled(&down) })
	m := add(t, n, input([]string{"a", "b"}, "http://127.0.0.1:1", r.URL))
	eventually(t, func() bool { return r.count("down") == 1 })
	down.Store(false)
	eventually(t, func() bool {
		v := getOverview(t, n)
		return v.Monitors[0].State == "unknown" && v.Monitors[0].UnknownNodes == 1 && v.Monitors[0].Observations[0].State == "up"
	})
	code, b := api(t, n, "/api/monitors/"+m.ID+"/history", "GET", nil, adminToken)
	if code != 200 {
		t.Fatal(code)
	}
	var history []Observation
	if json.Unmarshal(b, &history) != nil || history[0].State != "up" {
		t.Fatal(string(b))
	}
	time.Sleep(300 * time.Millisecond)
	if len(r.all()) != 1 || getOverview(t, n).Incidents[0].ResolvedAt != nil {
		t.Fatal("Missing node authorized recovery")
	}
}
