package canary

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"
)

func TestResolutionAPIJoinsAllRestoredHeadsAtomically(t *testing.T) {
	for _, kind := range []string{"monitor", "group", resourceChannel, resourceMaintenance} {
		t.Run(kind, func(t *testing.T) {
			n := cr2New(t, cr2Options(t, true))
			server := httptest.NewServer(n)
			defer server.Close()
			n.url = server.URL
			tips := revisionJoinsSeed(t, n, kind, 71, false)
			id := "join-" + kind
			var input any
			var path string
			switch kind {
			case "monitor":
				input, path = n.heads[id][0].Monitor.MonitorInput, "/api/monitors/"+id
			case "group":
				input, path = n.groups[id][0].Group.GroupInput, "/api/groups/"+id
			case resourceChannel:
				input = channelOf(n.resources[resourceKey(kind, id)][0].Resource, "admin").ChannelInput
				path = "/api/notification-channels/" + id
			case resourceMaintenance:
				input = maintenanceOf(n.resources[resourceKey(kind, id)][0].Resource).MaintenanceInput
				path = "/api/maintenance/" + id
			}
			encoded, _ := json.Marshal(input)
			body := map[string]any{}
			if err := json.Unmarshal(encoded, &body); err != nil {
				t.Fatal(err)
			}
			body["revisions"] = tips[:len(tips)-1]
			if status, _ := api(t, n, path+"/resolve", "POST", body, adminToken); status != 409 {
				t.Fatal("incomplete resolution accepted", status)
			}
			body["revisions"] = tips
			if status, _ := api(t, n, path+"/resolve", "POST", body, "viewer"); status != 401 {
				t.Fatal("unauthenticated resolution accepted", status)
			}
			before := len(n.revisionEdges)
			status, result := api(t, n, path+"/resolve", "POST", body, adminToken)
			if status != 200 {
				t.Fatalf("actual resolution endpoint failed with %d: %s", status, result)
			}
			var revision string
			switch kind {
			case "monitor":
				if len(n.heads[id]) != 1 {
					t.Fatal("monitor remained conflicted")
				}
				revision = n.heads[id][0].Monitor.Revision
			case "group":
				if len(n.groups[id]) != 1 {
					t.Fatal("group remained conflicted")
				}
				revision = n.groups[id][0].Group.Revision
			default:
				if len(n.resources[resourceKey(kind, id)]) != 1 {
					t.Fatal("resource remained conflicted")
				}
				revision = n.resources[resourceKey(kind, id)][0].Resource.Revision
			}
			if len(n.revisionEdges)-before != 3 {
				t.Fatal("endpoint did not persist every join edge")
			}
			for _, tip := range tips {
				if !n.graph.reaches(kind, id, revision, tip) {
					t.Fatal("resolution lost an observed head", tip)
				}
			}
			peer := cr2New(t, cr2Options(t, false))
			pages := causalCapacityPages(t, n)
			slices.Reverse(pages)
			for _, page := range pages {
				revisionJoinsApply(t, peer, page)
			}
			if !equalJSON(n.revisionEdges, peer.revisionEdges) {
				t.Fatal("endpoint result did not replicate")
			}
			if status, _ := api(t, n, path+"/resolve", "POST", body, adminToken); status != 409 {
				t.Fatal("stale resolution replay accepted", status)
			}
		})
	}
}
