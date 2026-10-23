package canary

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAuthenticationRBACRedactionAndCORS(t *testing.T) {
	n := local(t, func(o *Options) {
		o.Peers = []Peer{{ID: "b", URL: "http://127.0.0.1:1"}}
		o.CORSOrigins = []string{"http://localhost:5173"}
	})
	code, b := api(t, n, "/healthz", "GET", nil, "")
	if code != 200 || string(b) != `{"ok":true}` {
		t.Fatal(code, string(b))
	}
	for _, token := range []string{"", clusterToken} {
		code, _ = api(t, n, "/api/overview", "GET", nil, token)
		if code != 401 {
			t.Fatal(code)
		}
	}
	for _, token := range []string{"", adminToken} {
		req, _ := http.NewRequest("POST", n.URL()+"/internal/snapshot", strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Canary-Node-Id", "b")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Fatal(res.StatusCode)
		}
	}
	for _, a := range []struct{ name, role, token string }{{"Reader", "viewer", viewerToken}, {"Operator", "editor", editorToken}} {
		code, b = api(t, n, "/api/accounts", "POST", map[string]string{"name": a.name, "role": a.role, "token": a.token}, adminToken)
		if code != 201 || strings.Contains(string(b), a.token) || strings.Contains(string(b), "hash") {
			t.Fatal(code, string(b))
		}
		code, _ = api(t, n, "/api/accounts", "GET", nil, a.token)
		if code != 403 {
			t.Fatal(code)
		}
	}
	in := input([]string{"a"}, "https://example.com", "https://hooks.example.com/private-secret")
	in.Enabled = false
	code, _ = api(t, n, "/api/monitors", "POST", in, viewerToken)
	if code != 403 {
		t.Fatal(code)
	}
	code, b = api(t, n, "/api/monitors", "POST", in, editorToken)
	if code != 201 {
		t.Fatal(code, string(b))
	}
	var m Monitor
	_ = json.Unmarshal(b, &m)
	for _, action := range []struct {
		method, suffix string
		body           any
	}{{"PUT", "", m}, {"DELETE", "", map[string]string{"revision": m.Revision}}, {"POST", "/resolve", m}} {
		code, _ = api(t, n, "/api/monitors/"+m.ID+action.suffix, action.method, action.body, viewerToken)
		if code != 403 {
			t.Fatal(code)
		}
	}
	for _, path := range []string{"/api/overview", "/api/monitors", "/api/monitors/" + m.ID + "/conflicts"} {
		code, b = api(t, n, path, "GET", nil, viewerToken)
		if code != 200 || strings.Contains(string(b), "private-secret") || strings.Contains(string(b), adminToken) || strings.Contains(string(b), clusterToken) {
			t.Fatal(code, string(b))
		}
	}
	code, b = api(t, n, "/api/monitors", "GET", nil, editorToken)
	if code != 200 || !strings.Contains(string(b), "private-secret") {
		t.Fatal(code, string(b))
	}
	for _, origin := range []string{"http://localhost:5173", "https://evil.example"} {
		req, _ := http.NewRequest("GET", n.URL()+"/api/overview", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		req.Header.Set("Origin", origin)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if origin == "http://localhost:5173" {
			if res.Header.Get("Access-Control-Allow-Origin") != origin || res.StatusCode != 200 {
				t.Fatal(res.StatusCode)
			}
		} else if res.StatusCode != 403 {
			t.Fatal(res.StatusCode)
		}
	}
	req, _ := http.NewRequest("OPTIONS", n.URL()+"/api/groups", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatal(res.StatusCode)
	}
	code, _ = api(t, n, "/api/groups", "POST", GroupInput{Name: "Readers cannot edit", NodeIDs: []string{"a"}, GraceMs: 0}, viewerToken)
	if code != 403 {
		t.Fatal(code)
	}
}
func TestInputValidationAndBodyLimits(t *testing.T) {
	n := local(t, nil)
	base := input([]string{"a"}, "http://127.0.0.1:1", "")
	base.Enabled = false
	encoded, _ := json.Marshal(base)
	var object map[string]any
	_ = json.Unmarshal(encoded, &object)
	invalid := []map[string]any{{"target": "file:///etc/passwd"}, {"target": "ftp://example.com"}, {"target": "http://user:secret@example.com"}, {"intervalMs": nil}, {"intervalMs": 99}, {"intervalMs": 1.25}, {"timeoutMs": 121000}, {"timeoutMs": 1000}, {"nodeIds": []string{"missing"}}, {"nodeIds": []string{"a", "a"}}, {"expectedStatusCodes": []int{999}}, {"repeatAfterMs": 1}, {"webhookUrl": "javascript:alert(1)"}, {"type": "ping", "target": "-n 99 example.com"}, {"type": "ping", "target": "localhost; echo secret"}, {"type": "ping", "target": "example.com\nwhoami"}, {"type": "ping", "target": "example.com && dir"}, {"type": "ping", "target": "fe80::1%eth0"}}
	for _, change := range invalid {
		v := map[string]any{}
		for k, x := range object {
			v[k] = x
		}
		for k, x := range change {
			v[k] = x
		}
		code, b := api(t, n, "/api/monitors", "POST", v, adminToken)
		if code != 400 || strings.Contains(string(b), "user:secret") {
			t.Fatalf("Accepted %+v: %d %s", change, code, b)
		}
	}
	for _, target := range []string{"localhost", "example.com.", "127.0.0.1", "::1", "2001:db8::1"} {
		in := base
		in.Type = "ping"
		in.Target = target
		add(t, n, in)
	}
	code, _ := api(t, n, "/api/monitors", "POST", map[string]string{"padding": strings.Repeat("x", maxAPIBody)}, adminToken)
	if code != 413 {
		t.Fatal(code)
	}
	code, _ = apiBytes(t, n, "/api/monitors", "POST", []byte("{invalid"), adminToken)
	if code != 400 {
		t.Fatal(code)
	}
}
func TestSnapshotIsolationValidationAndEmptyArrays(t *testing.T) {
	n := local(t, func(o *Options) { o.Peers = []Peer{{ID: "b", URL: "http://127.0.0.1:1"}} })
	code, b := api(t, n, "/api/overview", "GET", nil, adminToken)
	if code != 200 {
		t.Fatal(code)
	}
	for _, key := range []string{"monitors", "groups", "incidents", "notifications"} {
		if !strings.Contains(string(b), `"`+key+`":[]`) {
			t.Fatal(string(b))
		}
	}
	in := input([]string{"a"}, "http://127.0.0.1:1", "")
	in.Enabled = false
	add(t, n, in)
	s := Snapshot{Protocol: peerProtocol, Node: Identity{"b", "Peer B"}, Versions: []Version{}, GroupVersions: []GroupVersion{}, Observations: []Sample{}, Incidents: []Incident{}, Receipts: []Receipt{}}
	call := func(value any) (int, string) {
		raw, _ := json.Marshal(value)
		req, _ := http.NewRequest("POST", n.URL()+"/internal/snapshot", strings.NewReader(string(raw)))
		req.Header.Set("Authorization", "Bearer "+clusterToken)
		req.Header.Set("X-Canary-Node-Id", "b")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, _ := readBounded(res.Body, maxBody)
		return res.StatusCode, string(out)
	}
	code, text := call(s)
	if code != 200 {
		t.Fatal(code, text)
	}
	for _, secret := range []string{adminToken, clusterToken, `"accounts"`, `"history"`, `"hash"`} {
		if strings.Contains(text, secret) {
			t.Fatal("Snapshot leaks local data", text)
		}
	}
	malformed := map[string]any{"protocol": peerProtocol, "node": s.Node, "versions": []any{}, "observations": []any{map[string]string{"secret": adminToken}}, "incidents": []any{}, "receipts": []any{}}
	code, text = call(malformed)
	if code != 400 || strings.Contains(text, adminToken) {
		t.Fatal(code, text)
	}
}
