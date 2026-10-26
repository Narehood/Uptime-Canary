package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const placementFixtureID = "0123456789abcdef0123456789abcdef"
const placementFixtureOwner = "abcdef0123456789abcdef0123456789"

func placementFixtureNode(t *testing.T, path string) *Node {
	t.Helper()
	n, err := New(Options{NodeID: "node-a", DBPath: path, AdminToken: adminToken, ClusterToken: clusterToken})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	// This is the real bundle admission path, not a hand-made account map.
	tokens := dashboardRoleTokens{NodeID: n.ID, Admin: "placement-admin-fixture-token", Editor: editorToken, Viewer: viewerToken}
	w := dashboardHTTP(n, "POST", "/api/dashboard-enrollments/"+placementFixtureOwner, adminToken, "", tokens)
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("delegation fixture: %d", w.Code)
	}
	return n
}

func placementFixtureInput() MonitorInput {
	in := input([]string{"node-a"}, "http://127.0.0.1:9", "")
	in.Enabled = false
	return in
}

func placementFixtureCommand(t *testing.T, sequence int64, revision string, deleted bool) fleetPlacementCommand {
	t.Helper()
	command := fleetPlacementCommand{Sequence: sequence, Revision: revision, Deleted: deleted}
	if !deleted {
		var err error
		command.Monitor, err = json.Marshal(placementFixtureInput())
		if err != nil {
			t.Fatal(err)
		}
	}
	return command
}

func placementFixtureWrite(t *testing.T, n *Node, command fleetPlacementCommand, want int) fleetPlacementResult {
	t.Helper()
	w := dashboardHTTP(n, "PUT", "/api/fleet-placements/"+placementFixtureID, editorToken, "", command)
	if w.Code != want {
		t.Fatalf("placement command returned %d, wanted %d: %s", w.Code, want, w.Body.String())
	}
	var result fleetPlacementResult
	if want < 300 && json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal("invalid placement acknowledgement")
	}
	return result
}

func TestFleetPlacementLostAnswerRetryAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private-placement.db")
	n := placementFixtureNode(t, path)
	command := placementFixtureCommand(t, 1, "", false)
	first := placementFixtureWrite(t, n, command, 201)
	if first.MonitorID != "fleet-"+placementFixtureID || first.Sequence != 1 || first.Deleted || len(n.heads) != 1 {
		t.Fatal("placement did not produce one stable monitor")
	}
	edges := len(n.revisionEdges)
	for range 3 {
		if got := placementFixtureWrite(t, n, command, 200); got != first {
			t.Fatal("retry changed the original acknowledgement")
		}
	}
	if len(n.revisionEdges) != edges || len(n.heads[first.MonitorID]) != 1 {
		t.Fatal("retry manufactured another revision")
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	restarted := placementFixtureNode(t, path)
	if got := placementFixtureWrite(t, restarted, command, 200); got != first || len(restarted.heads) != 1 {
		t.Fatal("restart lost the retry receipt or duplicated the monitor")
	}
	read := dashboardHTTP(restarted, "GET", "/api/fleet-placements/"+placementFixtureID, editorToken, "", nil)
	var got fleetPlacementResult
	if read.Code != 200 || json.Unmarshal(read.Body.Bytes(), &got) != nil || got != first {
		t.Fatal("receipt lookup did not recover an unknown result")
	}
}

func TestFleetPlacementFencesCommandsAndPreservesLocalEdits(t *testing.T) {
	n := placementFixtureNode(t, ":memory:")
	create := placementFixtureCommand(t, 1, "", false)
	first := placementFixtureWrite(t, n, create, 201)
	different := create
	different.Monitor = []byte(strings.Replace(string(create.Monitor), "Service", "Different", 1))
	placementFixtureWrite(t, n, different, 409)
	placementFixtureWrite(t, n, placementFixtureCommand(t, 3, first.Revision, false), 409)
	secondCommand := placementFixtureCommand(t, 2, first.Revision, false)
	second := placementFixtureWrite(t, n, secondCommand, 200)
	placementFixtureWrite(t, n, create, 409)
	in := n.heads[first.MonitorID][0].Monitor.MonitorInput
	in.Name = "Edited directly on the node"
	local, err := n.writeMonitor(in, n.heads[first.MonitorID], first.MonitorID, false)
	if err != nil {
		t.Fatal(err)
	}
	// An exact retry reports its old acknowledgement, not a claim that it is
	// still current. It cannot undo an edit after the response was lost.
	if got := placementFixtureWrite(t, n, secondCommand, 200); got != second || n.current(first.MonitorID).Revision != local.Revision {
		t.Fatal("idempotent acknowledgement overwrote a later local edit")
	}
	placementFixtureWrite(t, n, placementFixtureCommand(t, 3, second.Revision, false), 409)
	if n.current(first.MonitorID).Name != in.Name {
		t.Fatal("stale fleet command overwrote a local edit")
	}
}

func TestFleetPlacementReceiptAndConfigurationCommitTogether(t *testing.T) {
	for _, initial := range []bool{true, false} {
		t.Run(map[bool]string{true: "create", false: "update"}[initial], func(t *testing.T) {
			n := placementFixtureNode(t, ":memory:")
			command := placementFixtureCommand(t, 1, "", false)
			var before fleetPlacementResult
			if !initial {
				before = placementFixtureWrite(t, n, command, 201)
				command = placementFixtureCommand(t, 2, before.Revision, false)
			}
			edges, heads := len(n.revisionEdges), len(n.heads)
			if _, err := n.store.db.Exec(`CREATE TRIGGER placement_receipt_failure BEFORE INSERT ON records WHEN NEW.category='fleetPlacements' BEGIN SELECT RAISE(ABORT,'private fixture failure'); END`); err != nil {
				t.Fatal(err)
			}
			placementFixtureWrite(t, n, command, 503)
			if len(n.revisionEdges) != edges || len(n.heads) != heads {
				t.Fatal("failed receipt commit published configuration in memory")
			}
			receipt, found, err := loadOne[fleetPlacementReceipt](n.store, fleetPlacementCategory, "fleet-"+placementFixtureID)
			if err != nil || found == initial || found && receipt.Revision != before.Revision {
				t.Fatal("receipt transaction did not roll back")
			}
			stored, found, err := loadOne[[]Version](n.store, "heads", "fleet-"+placementFixtureID)
			if err != nil || found == initial || found && stored[0].Monitor.Revision != before.Revision {
				t.Fatal("configuration transaction did not roll back")
			}
			if _, err = n.store.db.Exec(`DROP TRIGGER placement_receipt_failure`); err != nil {
				t.Fatal(err)
			}
			status := 200
			if initial {
				status = 201
			}
			placementFixtureWrite(t, n, command, status)
		})
	}
}

func TestFleetPlacementRetirementCannotResurrect(t *testing.T) {
	n := placementFixtureNode(t, ":memory:")
	first := placementFixtureWrite(t, n, placementFixtureCommand(t, 1, "", false), 201)
	command := placementFixtureCommand(t, 2, first.Revision, true)
	deleted := placementFixtureWrite(t, n, command, 200)
	if !deleted.Deleted || n.current(first.MonitorID) != nil || !n.heads[first.MonitorID][0].Monitor.Deleted {
		t.Fatal("placement retirement did not retain a tombstone")
	}
	if retry := placementFixtureWrite(t, n, command, 200); retry != deleted {
		t.Fatal("retirement retry changed its acknowledgement")
	}
	placementFixtureWrite(t, n, placementFixtureCommand(t, 3, deleted.Revision, false), 409)
}

func TestFleetPlacementAuthorityAndBodyValidation(t *testing.T) {
	n := placementFixtureNode(t, ":memory:")
	path := "/api/fleet-placements/" + placementFixtureID
	command := placementFixtureCommand(t, 1, "", false)
	for _, token := range []string{"", adminToken, viewerToken} {
		for _, method := range []string{"GET", "PUT"} {
			w := dashboardHTTP(n, method, path, token, "", command)
			if token == "" && w.Code != 401 || token != "" && w.Code != 403 {
				t.Fatalf("non-delegated management or viewer accepted: %s %d", method, w.Code)
			}
		}
	}
	placementFixtureWrite(t, n, command, 201)
	otherID := strings.Repeat("e", 32)
	other := dashboardRoleTokens{NodeID: n.ID, Admin: "other-placement-admin-token", Editor: "other-placement-editor-token", Viewer: "other-placement-viewer-token"}
	if w := dashboardHTTP(n, "POST", "/api/dashboard-enrollments/"+otherID, adminToken, "", other); w.Code != 201 && w.Code != 200 {
		t.Fatal("second registration fixture refused")
	}
	for _, method := range []string{"GET", "PUT"} {
		if w := dashboardHTTP(n, method, path, other.Editor, "", command); w.Code != 409 {
			t.Fatal("another registration acquired placement ownership")
		}
	}
	for _, raw := range []string{
		`{"sequence":1,"Sequence":1}`, `{"sequence":1,"sequence":1}`, `{"sequence":null}`,
		`{"sequence":1,"revision":null}`, `{"sequence":1,"monitor":null}`, `{"sequence":1,"deleted":null}`,
		`{"sequence":1.5}`, `{"sequence":9007199254740992}`, `{"sequence":0}`, `{"sequence":1,"unknown":true}`,
		`{"sequence":2,"deleted":true,"monitor":{}}`, `{"sequence":1,"monitor":{}}`,
	} {
		r := httptest.NewRequest("PUT", path, strings.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+editorToken)
		w := httptest.NewRecorder()
		n.ServeHTTP(w, r)
		if w.Code != 400 && w.Code != 409 {
			t.Fatalf("invalid command accepted: %d", w.Code)
		}
	}
	for _, suffix := range []string{"?x=1", "?"} {
		if w := dashboardHTTP(n, "PUT", path+suffix, editorToken, "", command); w.Code != 400 {
			t.Fatal("placement query accepted")
		}
	}
}

func TestFleetPlacementReferencesAndCollisionAreNotSilentlyChanged(t *testing.T) {
	n := placementFixtureNode(t, ":memory:")
	command := placementFixtureCommand(t, 1, "", false)
	for _, mutate := range []func(*MonitorInput){
		func(in *MonitorInput) { in.NodeIDs = []string{"unknown-node"} },
		func(in *MonitorInput) { in.GroupID = "missing-group" },
		func(in *MonitorInput) { in.NotificationIDs = []string{"missing-channel"} },
		func(in *MonitorInput) { in.Tags = []MonitorTag{{TagID: "missing-tag"}} },
	} {
		in := placementFixtureInput()
		mutate(&in)
		command.Monitor, _ = json.Marshal(in)
		placementFixtureWrite(t, n, command, 400)
	}
	if len(n.heads) != 0 {
		t.Fatal("refused references created a partial monitor")
	}
	if _, err := n.writeMonitor(placementFixtureInput(), nil, "fleet-"+placementFixtureID, false); err != nil {
		t.Fatal(err)
	}
	placementFixtureWrite(t, n, placementFixtureCommand(t, 1, "", false), 409)
}

func TestFleetPlacementConcurrentRetriesCommitOnce(t *testing.T) {
	n := placementFixtureNode(t, ":memory:")
	command := placementFixtureCommand(t, 1, "", false)
	results := make(chan *httptest.ResponseRecorder, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			results <- dashboardHTTP(n, "PUT", "/api/fleet-placements/"+placementFixtureID, editorToken, "", command)
		})
	}
	workers.Wait()
	close(results)
	created, revision := 0, ""
	for w := range results {
		if w.Code == 201 {
			created++
		} else if w.Code != 200 {
			t.Fatalf("concurrent retry refused: %d", w.Code)
		}
		var got fleetPlacementResult
		if json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatal("invalid concurrent acknowledgement")
		}
		if revision != "" && revision != got.Revision {
			t.Fatal("concurrent retry changed revision")
		}
		revision = got.Revision
	}
	if created != 1 || len(n.heads) != 1 || len(n.heads["fleet-"+placementFixtureID]) != 1 {
		t.Fatal("concurrent retries created more than once")
	}
}

func TestFleetPlacementCancellationAndPrivateReceipt(t *testing.T) {
	n := placementFixtureNode(t, ":memory:")
	command := placementFixtureCommand(t, 1, "", false)
	b, _ := json.Marshal(command)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("PUT", "/api/fleet-placements/"+placementFixtureID, strings.NewReader(string(b))).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer "+editorToken)
	w := httptest.NewRecorder()
	n.ServeHTTP(w, r)
	if w.Code != 408 || len(n.heads) != 0 {
		t.Fatal("cancelled request created a monitor")
	}
	first := placementFixtureWrite(t, n, command, http.StatusCreated)
	receipt, _, err := loadOne[fleetPlacementReceipt](n.store, fleetPlacementCategory, first.MonitorID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(receipt)
	if strings.Contains(string(encoded), placementFixtureInput().Target) || strings.Contains(string(encoded), editorToken) {
		t.Fatal("receipt retained private input or credentials")
	}
	for _, page := range causalCapacityPages(t, n) {
		encoded, _ = json.Marshal(page)
		if strings.Contains(string(encoded), receipt.CommandHash) || strings.Contains(string(encoded), receipt.Owner) {
			t.Fatal("local placement authorization leaked into peer snapshot")
		}
	}
}
