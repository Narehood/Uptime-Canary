package canary

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIManagementCommandsAndGroups(t *testing.T) {
	n := local(t, nil)
	env := map[string]string{"CANARY_URL": n.URL(), "CANARY_TOKEN": adminToken}
	get := func(k string) string { return env[k] }
	run := func(args ...string) any {
		t.Helper()
		v, err := RunCLI(context.Background(), args, get, nil)
		if err != nil {
			t.Fatal(args, err)
		}
		return v
	}
	v := run("status").(map[string]any)
	if v["node"].(map[string]any)["id"] != "a" {
		t.Fatal(v)
	}
	in := input([]string{"a"}, "http://127.0.0.1:1", "")
	in.Enabled = false
	dir := t.TempDir()
	write := func(name string, v any) string {
		t.Helper()
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, name)
		if err = os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	m := run("add", write("monitor.json", in)).(map[string]any)
	id := m["id"].(string)
	if len(run("monitors").([]any)) != 1 {
		t.Fatal("Missing CLI monitor")
	}
	m["name"] = "Changed via CLI"
	m = run("update", id, write("edit.json", m)).(map[string]any)
	run("history", id)
	run("conflicts", id)
	run("delete", id, m["revision"].(string))
	g := run("add-group", write("group.json", GroupInput{Name: "User group", NodeIDs: []string{"a"}, GraceMs: 10000})).(map[string]any)
	gid := g["id"].(string)
	if len(run("groups").([]any)) != 1 {
		t.Fatal("Missing CLI group")
	}
	g["name"] = "Updated group"
	g = run("update-group", gid, write("group-edit.json", g)).(map[string]any)
	run("group-conflicts", gid)
	run("delete-group", gid, g["revision"].(string))
	receiver := webhook(t)
	channelInput := hookChannel("CLI channel", receiver.URL)
	channel := run("add-channel", write("channel.json", channelInput)).(map[string]any)
	cid := channel["id"].(string)
	listed := run("channels").([]any)
	printed, _ := json.Marshal(listed)
	if len(listed) != 1 || strings.Contains(string(printed), "fixture-only-bearer") || !strings.Contains(string(printed), `"redacted":true`) {
		t.Fatal("channel CLI leaked provider configuration or lost the channel")
	}
	run("test-channel", cid, channel["revision"].(string))
	eventually(t, func() bool { return receiver.count("test") == 1 })
	run("deliveries")
	channelInput.Name = "Changed CLI channel"
	channelEdit := map[string]any{"name": channelInput.Name, "enabled": channelInput.Enabled, "config": channelInput.Config, "revision": channel["revision"]}
	channel = run("update-channel", cid, write("channel-edit.json", channelEdit)).(map[string]any)
	run("channel-conflicts", cid)
	run("delete-channel", cid, channel["revision"].(string))
	run("add-account", write("account.json", map[string]string{"name": "Reader", "role": "viewer", "token": viewerToken}))
	if len(run("accounts").([]any)) != 2 {
		t.Fatal("Missing CLI account")
	}
	env["CANARY_TOKEN"] = viewerToken
	if _, err := RunCLI(context.Background(), []string{"accounts"}, get, nil); err == nil || err.Error() != "Canary API returned HTTP 403" {
		t.Fatal(err)
	}
}
func TestCLIRedactsUntrustedBodiesAndErrors(t *testing.T) {
	env := func(k string) string {
		if k == "CANARY_TOKEN" {
			return adminToken
		}
		return ""
	}
	client := doFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"token":"%s","hash":"private-hash","message":"echo %s","nested":[{"accessToken":"secret","ok":true}]}`, adminToken, adminToken))), Header: make(http.Header)}, nil
	})
	v, err := RunCLI(context.Background(), []string{"status"}, env, client)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), adminToken) || strings.Contains(string(b), "private-hash") || strings.Contains(string(b), "accessToken") {
		t.Fatal(string(b))
	}
	failing := doFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(adminToken))}, nil
	})
	_, err = RunCLI(context.Background(), []string{"status"}, env, failing)
	if err == nil || strings.Contains(err.Error(), adminToken) {
		t.Fatal(err)
	}
	oversized := doFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxBody+1)))}, nil
	})
	_, err = RunCLI(context.Background(), []string{"status"}, env, oversized)
	if err == nil {
		t.Fatal("Oversized response accepted")
	}
}
func TestEnvironmentParsingAndBootstrapRequirements(t *testing.T) {
	valid := map[string]string{"CANARY_NODE_ID": "custom-id", "CANARY_NODE_NAME": "Custom name", "CANARY_HOST": "127.0.0.1", "CANARY_PORT": "0", "CANARY_DB": ":memory:", "CANARY_ADMIN_TOKEN": adminToken, "CANARY_CLUSTER_TOKEN": clusterToken, "CANARY_PEERS": `[{"id":"b","url":"http://127.0.0.1:1"}]`, "CANARY_CORS_ORIGINS": "http://localhost:5173", "CANARY_SYNC_INTERVAL_MS": "60", "CANARY_NOTIFICATION_DELAY_MS": "0", "CANARY_PEER_TIMEOUT_MS": "60"}
	get := func(k string) string { return valid[k] }
	valid["CANARY_HISTORY_LIMIT"], valid["CANARY_HISTORY_RETENTION_MS"] = "50", "86400000"
	options, err := OptionsFromEnvironment(get)
	if err != nil {
		t.Fatal(err)
	}
	n, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != "custom-id" || n.Name != "Custom name" || n.settings.NotificationDelayMs != 0 || n.historyLimit != 50 || n.historyRetention.Milliseconds() != 86400000 {
		t.Fatal(n.settings)
	}
	_ = n.Stop()
	for _, entry := range []struct{ k, v string }{{"CANARY_PORT", "NaN"}, {"CANARY_SYNC_INTERVAL_MS", "1.5"}, {"CANARY_NOTIFICATION_DELAY_MS", "-1"}, {"CANARY_PEER_TIMEOUT_MS", "1e4"}, {"CANARY_PEERS", "null"}, {"CANARY_PEERS", "{}"}} {
		original := valid[entry.k]
		valid[entry.k] = entry.v
		if _, err = OptionsFromEnvironment(get); err == nil {
			t.Fatalf("Accepted invalid %s", entry.k)
		}
		valid[entry.k] = original
	}
	for _, entry := range []struct{ k, v string }{{"CANARY_HISTORY_LIMIT", "0"}, {"CANARY_HISTORY_LIMIT", "100001"}, {"CANARY_HISTORY_LIMIT", "1.5"}, {"CANARY_HISTORY_RETENTION_MS", "999"}, {"CANARY_HISTORY_RETENTION_MS", "31536000001"}, {"CANARY_HISTORY_RETENTION_MS", "999999999999999999999"}, {"CANARY_HISTORY_RETENTION_MS", "private-invalid-value"}} {
		original := valid[entry.k]
		valid[entry.k] = entry.v
		if _, err = OptionsFromEnvironment(get); err == nil || strings.Contains(err.Error(), entry.v) {
			t.Fatalf("Invalid history retention setting was accepted or echoed: %s", entry.k)
		}
		valid[entry.k] = original
	}
	options.AdminToken = ""
	if _, err = New(options); err == nil {
		t.Fatal("Missing bootstrap accepted")
	}
	options.AdminToken = clusterToken
	if _, err = New(options); err == nil {
		t.Fatal("Shared peer/admin credential accepted")
	}
	options.AdminToken = adminToken
	options.ClusterToken = "short"
	if _, err = New(options); err == nil {
		t.Fatal("Weak cluster credential accepted")
	}
}
