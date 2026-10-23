package canary

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"uptime-canary/apps/node-go/internal/checks"
	"uptime-canary/apps/node-go/internal/jsonquery"
)

func TestOAuth2OptionsReplicatePersistAndRemainPrivate(t *testing.T) {
	dir := t.TempDir()
	c := cluster(t, []string{"a", "b"}, func(o *Options) { o.DBPath = filepath.Join(dir, o.NodeID+".sqlite") })
	n := c.nodes[0]
	in := input([]string{"a", "b"}, "https://127.0.0.1/status", "")
	in.Enabled = false
	in.HTTP = &checks.HTTPConfig{OAuth2: &checks.OAuth2Config{
		TokenURL: "https://127.0.0.1/private-oauth-token",
		ClientID: "private-oauth-client", ClientSecret: "private-oauth-secret",
		Scope: "private-oauth-scope", Audience: "private-oauth-audience", AuthMethod: "client_secret_post",
	}}
	in.HTTP.JSONata = &jsonquery.Config{Expression: "private_query", Operator: jsonquery.Equal, Expected: json.RawMessage(`"private-jsonata-expectation"`)}
	m := add(t, n, in)
	assertPeer := func(expected *checks.OAuth2Config) {
		t.Helper()
		eventually(t, func() bool {
			peer := c.nodes[1]
			peer.mu.Lock()
			defer peer.mu.Unlock()
			saved := peer.current(m.ID)
			return saved != nil && saved.HTTP != nil && equalJSON(saved.HTTP.OAuth2, expected) && equalJSON(saved.HTTP.JSONata, in.HTTP.JSONata)
		})
	}
	assertPeer(in.HTTP.OAuth2)
	code, result := api(t, n, "/api/accounts", "POST", map[string]string{"name": "oauth-viewer", "role": "viewer", "token": viewerToken}, adminToken)
	if code != 201 {
		t.Fatal("create local viewer", code)
	}
	for _, path := range []string{"/api/overview", "/api/monitors"} {
		code, result = api(t, n, path, "GET", nil, viewerToken)
		if code != 200 || strings.Contains(string(result), "private-oauth") || strings.Contains(string(result), "private_query") || strings.Contains(string(result), "private-jsonata") {
			t.Fatal("viewer received OAuth2 grant configuration", path, code)
		}
	}
	code, result = api(t, n, "/api/monitors/"+m.ID, "GET", nil, viewerToken)
	if code != 403 || strings.Contains(string(result), "private-oauth") {
		t.Fatal("viewer gained management monitor access")
	}
	code, result = api(t, n, "/api/monitors", "GET", nil, adminToken)
	if code != 200 || !strings.Contains(string(result), "private-oauth-secret") {
		t.Fatal("viewer projection mutated stored credentials")
	}
	meta := m.MonitorInput
	meta.Name = "renamed OAuth2 check"
	code, result = api(t, n, "/api/monitors/"+m.ID, "PUT", struct {
		MonitorInput
		Revision string `json:"revision"`
	}{MonitorInput: meta, Revision: m.Revision}, adminToken)
	if code != 200 {
		t.Fatal("metadata edit", code, string(result))
	}
	var renamed Monitor
	if json.Unmarshal(result, &renamed) != nil || checkGeneration(renamed) != checkGeneration(m) {
		t.Fatal("metadata edit changed OAuth2 generation")
	}
	rotated := *renamed.HTTP
	grant := *rotated.OAuth2
	grant.ClientSecret = "private-oauth-rotated"
	rotated.OAuth2 = &grant
	renamed.HTTP = &rotated
	code, result = api(t, n, "/api/monitors/"+m.ID, "PUT", struct {
		MonitorInput
		Revision string `json:"revision"`
	}{MonitorInput: renamed.MonitorInput, Revision: renamed.Revision}, adminToken)
	if code != 200 {
		t.Fatal("credential rotation", code, string(result))
	}
	var changed Monitor
	if json.Unmarshal(result, &changed) != nil || checkGeneration(changed) == checkGeneration(m) {
		t.Fatal("credential rotation retained obsolete check semantics")
	}
	assertPeer(&grant)
	if err := c.nodes[1].Stop(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{NodeID: "b", DBPath: filepath.Join(dir, "b.sqlite"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop()
	saved := reopened.current(m.ID)
	if saved == nil || saved.HTTP == nil || !equalJSON(saved.HTTP.OAuth2, &grant) || !equalJSON(saved.HTTP.JSONata, in.HTTP.JSONata) {
		t.Fatal("OAuth2 configuration failed restart")
	}
}
