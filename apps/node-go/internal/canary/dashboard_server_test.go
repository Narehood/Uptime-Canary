package canary

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDashboardServerStandaloneSetupAndMissingKeyRestore(t *testing.T) {
	base := t.TempDir()
	data, identity, assets := filepath.Join(base, "data"), filepath.Join(base, "identity"), filepath.Join(base, "assets")
	if err := os.Mkdir(assets, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assets, "index.html"), []byte(`<html><body><div id="root">private fixture</div></body></html>`), 0600); err != nil {
		t.Fatal(err)
	}
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := strings.TrimPrefix(reservation.Addr().String(), "127.0.0.1:")
	_ = reservation.Close()
	address := "http://127.0.0.1:" + port
	values := map[string]string{"CANARY_DASHBOARD_DATA": data, "CANARY_DASHBOARD_IDENTITY": identity, "CANARY_DASHBOARD_ASSETS": assets, "CANARY_DASHBOARD_HOST": "127.0.0.1", "CANARY_DASHBOARD_PORT": port, "CANARY_DASHBOARD_ORIGINS": address}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- RunDashboard(ctx, func(k string) string { return values[k] }, io.Discard) }()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(5 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		response, e := client.Get(address + "/api/auth/status")
		if e == nil {
			var status struct{ Dashboard, SetupRequired bool }
			_ = json.NewDecoder(response.Body).Decode(&status)
			_ = response.Body.Close()
			ready = response.StatusCode == 200 && status.Dashboard && status.SetupRequired
			if ready {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		cancel()
		t.Fatal("dashboard listener/setup did not start without any monitoring node")
	}
	index, err := client.Get(address + "/")
	if err != nil {
		t.Fatal(err)
	}
	html, err := io.ReadAll(index.Body)
	_ = index.Body.Close()
	if err != nil || !strings.Contains(string(html), `data-canary-dashboard="control-plane"`) {
		t.Fatal("server-owned onboarding mode missing from HTML")
	}
	code, err := os.ReadFile(filepath.Join(identity, "setup.code"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"name": "fixture-owner", "password": dashboardPasswordFixture, "code": strings.TrimSpace(string(code))})
	request, _ := http.NewRequest("POST", address+"/api/dashboard/setup", strings.NewReader(string(body)))
	request.Header.Set("Origin", address)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 201 {
		t.Fatal(response.StatusCode)
	}
	cancel()
	select {
	case err = <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dashboard HTTP/poll workers did not stop")
	}
	// Simulate only the fixture's lost identity volume; no real deployment path
	// is ever opened. Startup must not generate a replacement encryption key.
	key := filepath.Join(identity, "dashboard.key")
	if err = os.Remove(key); err != nil {
		t.Fatal(err)
	}
	if err = RunDashboard(context.Background(), func(k string) string { return values[k] }, io.Discard); err == nil {
		t.Fatal("database restore without retained key started")
	}
	if _, err = os.Lstat(key); !os.IsNotExist(err) {
		t.Fatal("missing key was silently regenerated")
	}
}
