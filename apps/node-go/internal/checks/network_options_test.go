package checks

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebSocketNegotiatesOnlyOfferedProtocols(t *testing.T) {
	for _, tc := range []struct {
		mode     string
		required bool
		want     string
	}{
		{"negotiated", true, Up}, {"negotiated", false, Up}, {"good", false, Up}, {"good", true, Down},
		{"subprotocol", false, Down}, {"duplicate-protocol", false, Down},
	} {
		t.Run(fmt.Sprintf("%s-%t", tc.mode, tc.required), func(t *testing.T) {
			server := httptest.NewServer(fixtureWebSocketHandler(t, tc.mode))
			defer server.Close()
			result := Check(context.Background(), Config{Type: WebSocket, Target: "ws" + strings.TrimPrefix(server.URL, "http"), Timeout: time.Second,
				WebSocket: &WebSocketConfig{BearerToken: "private", Subprotocols: []string{"chat", "v2"}, RequireSubprotocol: tc.required}})
			requireState(t, result, tc.want)
		})
	}
	for _, protocols := range [][]string{{"chat", "chat"}, {""}, {"chat, v2"}, {"secret\r\nvalue"}, {strings.Repeat("a", 129)}} {
		if Validate(Config{Type: WebSocket, Target: "wss://fixture.invalid", Timeout: time.Second, WebSocket: &WebSocketConfig{Subprotocols: protocols}}) == nil {
			t.Fatal("malformed protocol offer accepted")
		}
	}
	if Validate(Config{Type: WebSocket, Target: "wss://fixture.invalid", Timeout: time.Second, WebSocket: &WebSocketConfig{RequireSubprotocol: true}}) == nil {
		t.Fatal("required negotiation without an offer accepted")
	}
}

func TestWebSocketOAuthKeepsGrantAndTargetCredentialsSeparate(t *testing.T) {
	var grants atomic.Int32
	authority, authorityPKI := oauthFixture(t, func(writer http.ResponseWriter, request *http.Request) {
		grants.Add(1)
		if request.Header.Get("X-Private-Target") != "" || request.Header.Get("Sec-WebSocket-Protocol") != "" || len(request.TLS.PeerCertificates) != 0 {
			t.Error("target settings leaked to token authority")
		}
		username, password, ok := request.BasicAuth()
		if !ok || username != "client" || password != "private-grant-secret" {
			t.Error("incorrect grant credentials")
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"access_token":"private","token_type":"Bearer"}`)
	}, false)
	target, targetPKI := oauthFixture(t, fixtureWebSocketHandler(t, "negotiated"), false)
	config := Config{Type: WebSocket, Target: "wss" + strings.TrimPrefix(target.URL, "https"), Timeout: 2 * time.Second,
		TLS: &TLSConfig{CAPEM: targetPKI.caPEM}, WebSocket: &WebSocketConfig{Subprotocols: []string{"chat", "v2"}, RequireSubprotocol: true,
			Headers: map[string]string{"X-Private-Target": "value"}, OAuth2: &OAuth2Config{TokenURL: authority.URL, ClientID: "client", ClientSecret: "private-grant-secret", TokenCAPEM: authorityPKI.caPEM}}}
	for i := 0; i < 2; i++ {
		requireState(t, Check(context.Background(), config), Up)
	}
	if grants.Load() != 2 {
		t.Fatal("each query did not acquire its own grant")
	}
	for _, change := range []func(*Config){
		func(c *Config) { c.Target = strings.Replace(c.Target, "wss:", "ws:", 1) },
		func(c *Config) { c.WebSocket.BearerToken = "conflicting" },
		func(c *Config) { c.WebSocket.BasicAuth = &BasicAuth{Username: "conflicting"} },
		func(c *Config) { c.WebSocket.Headers = map[string]string{"Authorization": "conflicting"} },
	} {
		copy := config
		options := *config.WebSocket
		copy.WebSocket = &options
		change(&copy)
		if Validate(copy) == nil {
			t.Fatal("insecure or ambiguous OAuth configuration admitted")
		}
	}
}

func TestTCPOptionalTLSPerformsCertificateVerification(t *testing.T) {
	pki := newFixturePKI(t, time.Now().Add(-time.Hour), time.Now().Add(72*time.Hour))
	server := fixtureTCP(t, func(connection net.Conn) {
		_ = tls.Server(connection, &tls.Config{Certificates: []tls.Certificate{pki.server}}).Handshake()
	})
	for _, tc := range []struct {
		options *TLSConfig
		want    string
	}{
		{nil, Up}, {&TLSConfig{CAPEM: pki.caPEM}, Up}, {&TLSConfig{}, Down}, {&TLSConfig{CAPEM: pki.caPEM, ServerName: "wrong.invalid"}, Down},
	} {
		result := Check(context.Background(), Config{Type: TCP, Target: server, Timeout: time.Second, TLS: tc.options})
		requireState(t, result, tc.want)
		if tc.options != nil && tc.want == Up && (result.Certificate == nil || !result.Certificate.Verified) {
			t.Fatal("TLS-on-TCP did not prove the certificate")
		}
	}
}
