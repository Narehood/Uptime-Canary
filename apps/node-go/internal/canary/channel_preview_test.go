package canary

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestUnsavedChannelPreviewUsesAdapterWithoutSavingConfiguration(t *testing.T) {
	var accepted atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Kind    string   `json:"kind"`
			Monitor Identity `json:"monitor"`
		}
		if request.Header.Get("Authorization") != "Bearer fixture-only-bearer" || json.NewDecoder(request.Body).Decode(&body) != nil || body.Kind != "test" || body.Monitor.ID != "" {
			writer.WriteHeader(400)
			return
		}
		accepted.Add(1)
		writer.WriteHeader(202)
	}))
	defer sink.Close()
	node := local(t, nil)
	if status, _ := api(t, node, "/api/accounts", "POST", map[string]string{"name": "Preview viewer", "role": "viewer", "token": viewerToken}, adminToken); status != 201 {
		t.Fatal("viewer fixture failed")
	}
	channel := hookChannel("Unsaved private configuration", sink.URL)
	channel.Enabled = false // A test does not enable or save the destination.
	path := "/api/notification-channels/preview-test"
	if status, _ := api(t, node, path, "POST", channel, viewerToken); status != 403 || accepted.Load() != 0 {
		t.Fatal("viewer initiated an unsaved send", status)
	}
	status, raw := api(t, node, path, "POST", channel, adminToken)
	var response struct {
		Preview  bool                  `json:"preview"`
		Attempts []DeliveryAttemptView `json:"attempts"`
	}
	if status != 202 || json.Unmarshal(raw, &response) != nil || !response.Preview || len(response.Attempts) != 1 {
		t.Fatalf("preview: %d %s", status, raw)
	}
	for _, secret := range []string{channel.Name, sink.URL, "fixture-only-bearer"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("preview response exposed input settings")
		}
	}
	eventually(t, func() bool {
		node.mu.Lock()
		defer node.mu.Unlock()
		for _, attempt := range node.attempts {
			if attempt.Kind == "test" && attempt.State == "accepted" {
				return true
			}
		}
		return false
	})
	node.mu.Lock()
	resources, invented := len(node.resources), len(node.receipts)+len(node.episodes)+len(node.samples)+len(node.heads)
	rows, err := loadAll[ResourceVersion](node.store, "resources")
	node.mu.Unlock()
	if err != nil || resources != 0 || len(rows) != 0 || invented != 0 || accepted.Load() != 1 {
		t.Fatal("preview saved settings or invented monitoring evidence")
	}
	if status, _ := api(t, node, path, "POST", channel, adminToken); status != 429 || accepted.Load() != 1 {
		t.Fatal("unsaved sends bypassed the shared notification test throttle")
	}
}
