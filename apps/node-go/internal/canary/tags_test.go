package canary

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func createTag(t *testing.T, n *Node, in TagInput) Tag {
	t.Helper()
	w := maintenanceAPIRequest(n, "POST", "/api/tags", in, adminToken)
	var tag Tag
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &tag) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	return tag
}

func TestTagMetadataAndPublicProjectionRequireBothOptIns(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 0))
	r := webhook(t)
	o := cr2Options(t, true)
	o.Now = clock.Load
	o.Probe = func(context.Context, Monitor) ProbeResult { return ProbeResult{State: "down", Message: "fixture"} }
	n := cr2New(t, o)
	tag := createTag(t, n, TagInput{Name: "Environment", Color: "#AABBCC"})
	private := createTag(t, n, TagInput{Name: "Internal routing", Color: "#123456"})
	if tag.Color != "#aabbcc" {
		t.Fatal("tag color not canonical", tag)
	}
	in := input([]string{"a"}, "http://127.0.0.1:1/", r.URL)
	w := maintenanceAPIRequest(n, "POST", "/api/monitors", in, adminToken)
	var monitor Monitor
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &monitor) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	partitionDrive(t, n)
	if r.count("down") != 1 {
		t.Fatal("missing fixture outage")
	}
	generation := checkGeneration(monitor)
	monitor.Tags = []MonitorTag{{TagID: tag.ID, Value: "prod", Public: true}, {TagID: private.ID, Value: "private-rack"}}
	body, _ := json.Marshal(monitor)
	var edit map[string]any
	_ = json.Unmarshal(body, &edit)
	w = maintenanceAPIRequest(n, "PUT", "/api/monitors/"+monitor.ID, edit, adminToken)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &monitor) != nil || checkGeneration(monitor) != generation {
		t.Fatal("tag edit reset outage", w.Code, w.Body.String())
	}
	clock.Add(100)
	partitionDrive(t, n)
	if r.count("down") != 1 || r.count("recovery") != 0 {
		t.Fatal("metadata tag edit replayed notifications")
	}
	pageInput := statusFixture("tag-public")
	pageInput.Groups[0].Monitors[0].MonitorID = monitor.ID
	page := statusCreate(t, n, pageInput, adminToken)
	public, _ := statusPublic(t, n, page.Slug)
	if len(public.Groups[0].Monitors[0].Tags) != 0 {
		t.Fatal("assignment flag alone published tags")
	}
	pageInput.Groups[0].Monitors[0].ShowTags = true
	w = maintenanceAPIRequest(n, "PUT", "/api/status-pages/"+page.ID, statusEdit(pageInput, page.Revision, nil), adminToken)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	checkPublic := func(node *Node, want int) {
		t.Helper()
		public, w := statusPublic(t, node, page.Slug)
		if w.Code != 200 || len(public.Groups[0].Monitors[0].Tags) != want || strings.Contains(w.Body.String(), "private-rack") || strings.Contains(w.Body.String(), "Internal routing") {
			t.Fatal("tag publication exposed private values or lost opt-in", w.Code, w.Body.String())
		}
		if want == 1 {
			got := public.Groups[0].Monitors[0].Tags[0]
			if got.Name != "Environment" || got.Value != "prod" || got.Color != "#aabbcc" {
				t.Fatal(got)
			}
		}
	}
	checkPublic(n, 1)
	peerOptions := o
	peerOptions.NodeID, peerOptions.DBPath, peerOptions.Peers = "b", filepath.Join(t.TempDir(), "tag-peer.db"), []Peer{{ID: "a", URL: "http://127.0.0.1:1"}}
	peer := cr2New(t, peerOptions)
	transfer := func() {
		for _, page := range causalCapacityPages(t, n) {
			b, _ := json.Marshal(page)
			decoded, err := peer.validateSnapshot(b, "a")
			if err != nil {
				t.Fatal(err)
			}
			if err := peer.mergeSnapshot(decoded); err != nil {
				t.Fatal(err)
			}
		}
	}
	transfer()
	checkPublic(peer, 1)
	w = maintenanceAPIRequest(n, "DELETE", "/api/tags/"+tag.ID, map[string]string{"revision": tag.Revision}, adminToken)
	if w.Code != 204 || n.current(monitor.ID) == nil || checkGeneration(*n.current(monitor.ID)) != generation {
		t.Fatal("tag deletion changed monitoring", w.Code, w.Body.String())
	}
	checkPublic(n, 0)
	transfer()
	checkPublic(peer, 0)
	if err := peer.Stop(); err != nil {
		t.Fatal(err)
	}
	peer = cr2New(t, peerOptions)
	checkPublic(peer, 0)
}

func TestTagPermissionsValidationAndConflicts(t *testing.T) {
	n := cr2New(t, cr2Options(t, false))
	w := maintenanceAPIRequest(n, "POST", "/api/accounts", map[string]string{"name": "Tag reader", "role": "viewer", "token": viewerToken}, adminToken)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, body := range []any{map[string]any{"name": "Missing color"}, map[string]any{"name": "Tag", "color": "red"}, map[string]any{"name": "Tag", "color": "#123456;display:none"}, map[string]any{"name": "Tag", "color": "#ffffff", "Color": "#000000"}} {
		if w := maintenanceAPIRequest(n, "POST", "/api/tags", body, adminToken); w.Code != 400 {
			t.Fatal("invalid tag accepted", w.Code, w.Body.String())
		}
	}
	in := TagInput{Name: "Host", Color: "#112233"}
	if w := maintenanceAPIRequest(n, "POST", "/api/tags", in, viewerToken); w.Code != 403 {
		t.Fatal("viewer created tag", w.Code)
	}
	tag := createTag(t, n, in)
	if w := maintenanceAPIRequest(n, "GET", "/api/tags", nil, viewerToken); w.Code != 200 {
		t.Fatal("viewer cannot list tags")
	}
	prior := n.resources[resourceKey(resourceTag, tag.ID)][0]
	other := prior.Resource
	other.Revision, other.UpdatedAt = "tag-other-branch", n.now()
	other.Data, _ = json.Marshal(TagInput{Name: "Other", Color: "#334455"})
	e := newRevisionEdge(resourceTag, tag.ID, other.Revision, other, nil, "")
	if err := n.applySnapshot(Snapshot{Resources: []ResourceVersion{{Resource: other, Ancestors: e.Parents, EdgeID: e.ID}}, RevisionEdges: []RevisionEdge{e}}); err != nil {
		t.Fatal(err)
	}
	if w := maintenanceAPIRequest(n, "GET", "/api/tags/"+tag.ID, nil, adminToken); w.Code != 409 {
		t.Fatal("conflicting tag chose arbitrary label")
	}
	resolve := map[string]any{"name": "Chosen", "color": "#abcdef", "revisions": []string{tag.Revision, other.Revision}}
	w = maintenanceAPIRequest(n, "POST", "/api/tags/"+tag.ID+"/resolve", resolve, adminToken)
	if w.Code != 200 || len(n.resources[resourceKey(resourceTag, tag.ID)]) != 1 {
		t.Fatal("tag conflict did not resolve", w.Code, w.Body.String())
	}
	inMonitor := cr2Input(t, []string{"a"})
	inMonitor.Tags = []MonitorTag{{TagID: tag.ID}, {TagID: tag.ID}}
	if validateMonitor(&inMonitor, n.known) == nil {
		t.Fatal("duplicate tag assignment accepted")
	}
	inMonitor.Tags = []MonitorTag{{TagID: "missing"}}
	if n.validateReferences(&Monitor{MonitorInput: inMonitor, ID: "new-tagged-monitor"}, nil) == nil {
		t.Fatal("unknown tag accepted on local edit")
	}
}
