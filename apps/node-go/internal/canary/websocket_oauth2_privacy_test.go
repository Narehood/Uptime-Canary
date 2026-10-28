package canary

import (
	"bytes"
	"testing"

	"uptime-canary/apps/node-go/internal/checks"
)

func TestWebSocketOAuth2IsPrivateInViewerProjection(t *testing.T) {
	input := Monitor{MonitorInput: MonitorInput{WebSocket: &checks.WebSocketConfig{
		OAuth2:       &checks.OAuth2Config{TokenURL: "https://private-token.example.invalid/token", ClientID: "private-client", ClientSecret: "private-oauth-secret", TokenCAPEM: "private-token-ca"},
		Subprotocols: []string{"canary-status"}, RequireSubprotocol: true,
	}}}
	viewer := publicMonitor(input, "viewer")
	if viewer.WebSocket == nil || viewer.WebSocket.OAuth2 != nil {
		t.Fatal("viewer received private OAuth2 configuration")
	}
	if len(viewer.WebSocket.Subprotocols) != 1 || !viewer.WebSocket.RequireSubprotocol {
		t.Fatal("public negotiation settings were removed")
	}
	for _, role := range []string{"admin", "editor"} {
		if publicMonitor(input, role).WebSocket.OAuth2 != input.WebSocket.OAuth2 {
			t.Fatal("authorized editor lost the stored OAuth2 configuration")
		}
	}
	if input.WebSocket.OAuth2 == nil || input.WebSocket.OAuth2.ClientSecret != "private-oauth-secret" {
		t.Fatal("viewer projection mutated stored credentials")
	}
}

func TestWebSocketOAuth2IsPrivateAcrossViewerAPIs(t *testing.T) {
	n := local(t, nil)
	input := MonitorInput{Name: "OAuth2 socket", Type: "websocket", Target: "wss://127.0.0.1:9/status", Enabled: false,
		NodeIDs: []string{n.ID}, InternalOnly: true, IntervalMs: 1000, TimeoutMs: 1000,
		WebSocket: &checks.WebSocketConfig{OAuth2: &checks.OAuth2Config{
			TokenURL: "https://private-token.example.invalid/token", ClientID: "private-client", ClientSecret: "private-oauth-secret",
		}},
	}
	m := add(t, n, input)
	if status, _ := api(t, n, "/api/accounts", "POST", map[string]string{"name": "Private viewer", "role": "viewer", "token": viewerToken}, adminToken); status != 201 {
		t.Fatal("viewer setup failed", status)
	}
	for _, path := range []string{"/api/overview", "/api/monitors", "/api/monitors/" + m.ID + "/conflicts"} {
		status, body := api(t, n, path, "GET", nil, viewerToken)
		if status != 200 {
			t.Fatal("viewer read failed", path, status)
		}
		for _, forbidden := range []string{"oauth2", "private-oauth-secret", "private-client", "private-token.example.invalid"} {
			if bytes.Contains(body, []byte(forbidden)) {
				t.Error("viewer API exposed OAuth2 configuration", path)
			}
		}
	}
}
