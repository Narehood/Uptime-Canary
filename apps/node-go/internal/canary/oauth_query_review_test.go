package canary

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"uptime-canary/apps/node-go/internal/checks"
	"uptime-canary/apps/node-go/internal/jsonquery"
	"uptime-canary/apps/node-go/internal/services"
)

func oauthQueryReviewMQTTPacket(header byte, body []byte) []byte {
	out := []byte{header}
	for n := len(body); ; n /= 128 {
		b := byte(n % 128)
		if n >= 128 {
			b |= 128
		}
		out = append(out, b)
		if n < 128 {
			break
		}
	}
	return append(out, body...)
}

func oauthQueryReviewMQTTRead(r io.Reader) (byte, []byte, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, nil, err
	}
	header, size, multiplier := b[0], 0, 1
	for i := 0; i < 4; i++ {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		size += int(b[0]&127) * multiplier
		if size > 65536 {
			return 0, nil, fmt.Errorf("fixture packet too large")
		}
		if b[0]&128 == 0 {
			body := make([]byte, size)
			_, err := io.ReadFull(r, body)
			return header, body, err
		}
		multiplier *= 128
	}
	return 0, nil, fmt.Errorf("fixture packet malformed")
}

// Both publications are already queued before the client evaluates either.
// A condition mismatch must leave the subscription available to find a match.
func oauthQueryReviewMQTTFixture(t *testing.T, payloads ...string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
		header, _, err := oauthQueryReviewMQTTRead(conn)
		if err != nil || header != 0x10 {
			done <- fmt.Errorf("fixture expected CONNECT: %v", err)
			return
		}
		if _, err = conn.Write([]byte{0x20, 2, 0, 0}); err != nil {
			done <- err
			return
		}
		header, _, err = oauthQueryReviewMQTTRead(conn)
		if err != nil || header != 0x82 {
			done <- fmt.Errorf("fixture expected SUBSCRIBE: %v", err)
			return
		}
		packets := []byte{0x90, 3, 0, 1, 0}
		for _, payload := range payloads {
			body := make([]byte, 2)
			binary.BigEndian.PutUint16(body, uint16(len("status/node")))
			body = append(body, "status/node"...)
			body = append(body, payload...)
			packets = append(packets, oauthQueryReviewMQTTPacket(0x31, body)...)
		}
		_, err = io.Copy(conn, bytes.NewReader(packets))
		if err == nil {
			_, _ = io.Copy(io.Discard, conn)
		}
		done <- err
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error("MQTT review fixture:", err)
			}
		case <-time.After(9 * time.Second):
			t.Error("MQTT review fixture did not stop")
		}
	})
	return listener.Addr().String()
}

func TestOAuthQueryReviewMQTTWaitsForMatchingPublication(t *testing.T) {
	for _, useJSONata := range []bool{false, true} {
		t.Run(fmt.Sprintf("jsonata=%t", useJSONata), func(t *testing.T) {
			address := oauthQueryReviewMQTTFixture(t, `{"state":"starting"}`, `{"state":"ready"}`)
			options := &services.MQTTConfig{Address: address, Topic: "status/+", TLSMode: "plain"}
			if useJSONata {
				options.JSONata = &jsonquery.Config{Expression: "state", Operator: jsonquery.Equal, Expected: json.RawMessage(`"ready"`)}
			} else {
				options.Condition = &services.Condition{Operator: "contains", Expected: json.RawMessage(`"ready"`)}
			}
			result := ServiceProbe(context.Background(), Monitor{MonitorInput: MonitorInput{
				Type: "mqtt", TimeoutMs: 5000, Service: &ServiceMonitorOptions{MQTT: options},
			}})
			if result.State != "up" {
				t.Fatalf("a later queued publication satisfies the condition; expected up, got %+v", result)
			}
		})
	}
}

// Every invocation has an independent trust anchor. The certificate is also a
// valid client identity so the target can require mutual TLS in the same test.
func oauthQueryReviewTLS(t *testing.T) (tls.Certificate, string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: serial, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true,
		KeyUsage:    x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	return pair, certPEM, keyPEM
}

func TestOAuthQueryReviewTLSGrantAndWholeBodyAtRootAdapter(t *testing.T) {
	for _, method := range []string{"client_secret_basic", "client_secret_post"} {
		t.Run(method, func(t *testing.T) {
			const client, secret = "review-client: +/%", "review-secret: +/%"
			issuerCert, issuerPEM, _ := oauthQueryReviewTLS(t)
			targetCert, targetPEM, targetKey := oauthQueryReviewTLS(t)
			var grants, targets atomic.Int64
			issuer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := grants.Add(1)
				if r.Method != "POST" || r.Header.Get("X-Review-Private") != "" || len(r.TLS.PeerCertificates) != 0 {
					t.Error("target configuration reached issuer")
				}
				if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("scope") != "review scope" || r.Form.Get("audience") != "review audience" || r.Form.Has("review-target-body") {
					t.Error("issuer did not receive the independent grant form")
				}
				if method == "client_secret_basic" {
					u, p, ok := r.BasicAuth()
					if !ok || u != url.QueryEscape(client) || p != url.QueryEscape(secret) || r.Form.Has("client_id") || r.Form.Has("client_secret") {
						t.Error("encoded Basic grant credentials changed")
					}
				} else if r.Header.Get("Authorization") != "" || r.Form.Get("client_id") != client || r.Form.Get("client_secret") != secret {
					t.Error("POST grant credentials changed")
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"access_token":"review-token-%d","token_type":"Bearer"}`, n)
			}))
			issuer.Config.ErrorLog = log.New(io.Discard, "", 0)
			issuer.TLS = &tls.Config{Certificates: []tls.Certificate{issuerCert}, ClientAuth: tls.RequestClientCert}
			issuer.StartTLS()
			defer issuer.Close()
			pool := x509.NewCertPool()
			pool.AppendCertsFromPEM([]byte(targetPEM))
			target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := targets.Add(1)
				body, _ := io.ReadAll(r.Body)
				if r.Method != "POST" || string(body) != "review-target-body" || r.Header.Get("X-Review-Private") != "review-header" || len(r.TLS.PeerCertificates) != 1 || r.Header.Get("Authorization") != fmt.Sprintf("Bearer review-token-%d", n) {
					t.Error("target transport or issued credential changed")
				}
				w.WriteHeader(201)
				fmt.Fprint(w, `{"ready":true,"items":[{"value":2},{"value":5}]}`)
			}))
			target.Config.ErrorLog = log.New(io.Discard, "", 0)
			target.TLS = &tls.Config{Certificates: []tls.Certificate{targetCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
			target.StartTLS()
			defer target.Close()
			o := &checks.HTTPConfig{Method: "POST", Body: "review-target-body", Headers: map[string]string{"X-Review-Private": "review-header"},
				OAuth2:  &checks.OAuth2Config{TokenURL: issuer.URL, TokenCAPEM: issuerPEM, ClientID: client, ClientSecret: secret, AuthMethod: method, Scope: "review scope", Audience: "review audience"},
				Keyword: &checks.KeywordMatcher{Value: "ready"}, JSON: &checks.JSONMatcher{Path: "$.ready", Expected: "true"},
				JSONata: &jsonquery.Config{Expression: "$sum(items.value)", Operator: jsonquery.Equal, Expected: json.RawMessage(`7`)}}
			m := Monitor{MonitorInput: MonitorInput{Type: "http", Target: target.URL, TimeoutMs: 5000, ExpectedStatusCodes: []int{201}, HTTP: o,
				TLS: &TLSMonitorOptions{CAPEM: targetPEM, ClientCertPEM: targetPEM, ClientKeyPEM: targetKey}}}
			for range 2 {
				r := ActiveProbe(context.Background(), m)
				if r.State != "up" || r.StatusCode != 201 || r.Certificate == nil || !r.Certificate.Verified {
					t.Fatalf("combined root adapter check failed: %+v", r)
				}
				if strings.Contains(r.Message, "review-") {
					t.Fatal("private fixture material reached check result")
				}
			}
			if grants.Load() != 2 || targets.Load() != 2 || o.BearerToken != "" {
				t.Fatal("grant reused or immutable options mutated")
			}
			// Target trust and its unsafe opt-in must not authorize the issuer.
			o.OAuth2.TokenCAPEM = ""
			m.TLS.IgnoreVerification = true
			if r := ActiveProbe(context.Background(), m); r.State != "down" || targets.Load() != 2 || grants.Load() != 2 {
				t.Fatalf("issuer inherited target trust or unsafe TLS flag: %+v", r)
			}
		})
	}
}

func TestOAuthQueryReviewGrantAndTargetShareDeadline(t *testing.T) {
	var grants, targets atomic.Int64
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		grants.Add(1)
		select {
		case <-time.After(300 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"review-token","token_type":"Bearer"}`)
	}))
	defer issuer.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targets.Add(1)
		select {
		case <-time.After(300 * time.Millisecond):
		case <-r.Context().Done():
		}
	}))
	defer target.Close()
	m := Monitor{MonitorInput: MonitorInput{Type: "http", Target: target.URL, TimeoutMs: 500,
		HTTP: &checks.HTTPConfig{OAuth2: &checks.OAuth2Config{TokenURL: issuer.URL, ClientID: "review-client", AllowInsecureHTTP: true}}}}
	start := time.Now()
	r := ActiveProbe(context.Background(), m)
	if r.State != "down" || grants.Load() != 1 || targets.Load() != 1 || time.Since(start) > 2*time.Second {
		t.Fatalf("grant and target did not share the check deadline: %+v", r)
	}
}

func TestOAuthQueryReviewIssuerFramingNeverReachesTarget(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		status                  int
	}{
		{"non200", "application/json", `{"access_token":"review-token","token_type":"Bearer"}`, 201},
		{"wrongMediaType", "text/plain", `{"access_token":"review-token","token_type":"Bearer"}`, 200},
		{"issuerError", "application/json", `{"error":"invalid_client","error_description":"review-private-secret"}`, 200},
		{"duplicateEscapedKey", "application/json", `{"access_token":"review-token","access_\u0074oken":"other","token_type":"Bearer"}`, 200},
		{"invalidUTF8", "application/json", "{\"access_token\":\"review-token\",\"token_type\":\"Bearer\",\"other\":\"\xff\"}", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var targets atomic.Int64
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targets.Add(1) }))
			defer target.Close()
			issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer issuer.Close()
			r := ActiveProbe(context.Background(), Monitor{MonitorInput: MonitorInput{Type: "http", Target: target.URL, TimeoutMs: 2000,
				HTTP: &checks.HTTPConfig{OAuth2: &checks.OAuth2Config{TokenURL: issuer.URL, ClientID: "review-client", AllowInsecureHTTP: true}}}})
			if r.State != "down" || targets.Load() != 0 || strings.Contains(r.Message, "review-") {
				t.Fatalf("invalid issuer framing accepted or leaked: %+v", r)
			}
		})
	}
}

func TestOAuthQueryReviewConfigGenerationAndViewerProjections(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "review.sqlite")
	n := local(t, func(o *Options) { o.DBPath = dbPath })
	code, _ := api(t, n, "/api/accounts", "POST", map[string]string{"name": "query-review-viewer", "role": "viewer", "token": viewerToken}, adminToken)
	if code != 201 {
		t.Fatalf("create viewer: %d", code)
	}
	saved := map[string]Monitor{}
	for _, kind := range []string{"http", "mqtt", "snmp", "mongodb"} {
		t.Run(kind, func(t *testing.T) {
			in := input([]string{"a"}, "https://127.0.0.1:1/review", "")
			in.Name, in.Type, in.Enabled = kind, kind, false
			// Malformed language syntax passes public shape validation. All
			// monitors are disabled, so this test cannot contact these endpoints.
			q := &jsonquery.Config{Expression: "(private-review-query", Operator: jsonquery.Equal, Expected: json.RawMessage(`"private-review-expectation"`)}
			switch kind {
			case "http":
				in.HTTP = &checks.HTTPConfig{JSONata: q, OAuth2: &checks.OAuth2Config{TokenURL: "https://127.0.0.1:1/private-review-issuer", ClientID: "private-review-client", ClientSecret: ""}}
			case "mqtt":
				in.Service = &ServiceMonitorOptions{MQTT: &services.MQTTConfig{Address: "127.0.0.1:1", Topic: "private-review-topic", TLSMode: "plain", JSONata: q}}
			case "snmp":
				in.Service = &ServiceMonitorOptions{SNMP: &services.SNMPConfig{Address: "127.0.0.1:1", Version: "2c", Community: "private-review-community", OID: "1.3.6.1.2.1.1.1.0", JSONata: q}}
			case "mongodb":
				in.Service = &ServiceMonitorOptions{MongoDB: &services.MongoDBConfig{DSN: "mongodb://127.0.0.1:1/review", Collection: "private-review-collection", TLSMode: "plain", JSONata: q}}
			}
			m := add(t, n, in)
			put := func(next MonitorInput, revision string) Monitor {
				t.Helper()
				code, body := api(t, n, "/api/monitors/"+m.ID, "PUT", struct {
					MonitorInput
					Revision string `json:"revision"`
				}{next, revision}, adminToken)
				var out Monitor
				if code != 200 || json.Unmarshal(body, &out) != nil {
					t.Fatalf("update query monitor: %d", code)
				}
				return out
			}
			meta := m.MonitorInput
			meta.Name += " renamed"
			renamed := put(meta, m.Revision)
			if checkGeneration(renamed) != checkGeneration(m) {
				t.Fatal("metadata-only edit changed query generation")
			}
			var changed MonitorInput
			encoded, _ := json.Marshal(renamed.MonitorInput)
			if json.Unmarshal(bytes.ReplaceAll(encoded, []byte("private-review-expectation"), []byte("private-review-changed")), &changed) != nil {
				t.Fatal("clone query monitor")
			}
			updated := put(changed, renamed.Revision)
			if checkGeneration(updated) == checkGeneration(renamed) {
				t.Fatal("query expectation edit retained obsolete generation")
			}
			for _, path := range []string{"/api/overview", "/api/monitors", "/api/monitors/" + m.ID + "/conflicts"} {
				code, body := api(t, n, path, "GET", nil, viewerToken)
				if code != 200 || bytes.Contains(body, []byte("private-review")) || bytes.Contains(body, []byte(`"oauth2"`)) || bytes.Contains(body, []byte(`"jsonata"`)) {
					t.Fatalf("viewer projection exposed query/issuer/service options at %s: %d", path, code)
				}
			}
			saved[m.ID] = updated
		})
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(Options{NodeID: "a", DBPath: dbPath, Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Stop()
	for id, before := range saved {
		after := reopened.current(id)
		if after == nil || !equalJSON(after.HTTP, before.HTTP) || !equalJSON(after.Service, before.Service) || checkGeneration(*after) != checkGeneration(before) {
			t.Fatal("query config or generation changed at restart", before.Type)
		}
	}
}

func TestOAuthQueryReviewWorkerCapacityRemainsPending(t *testing.T) {
	// Two loopback-only Oracle handshakes hold the shared worker slots. No
	// arbitrary program, environment hook, or evaluator runs in the parent.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	accepted := make(chan net.Conn, 2)
	workers := make(chan services.Result, 2)
	go func() {
		for range 2 {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}
	}()
	connections := []net.Conn{}
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		for _, conn := range connections {
			_ = conn.Close()
		}
		for range 2 {
			select {
			case <-workers:
			case <-time.After(3 * time.Second):
				t.Error("worker capacity fixture did not stop")
			}
		}
	})
	for range 2 {
		go func() {
			workers <- services.Check(ctx, services.Config{Type: services.Oracle, Timeout: 15 * time.Second,
				Oracle: &services.SQLConfig{DSN: "oracle://review:fixture-secret@" + listener.Addr().String() + "/review", TLSMode: "plain"}})
		}()
	}
	for range 2 {
		select {
		case conn := <-accepted:
			connections = append(connections, conn)
		case <-ctx.Done():
			t.Fatal("could not occupy shared worker slots")
		}
	}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"ready":true}`) }))
	defer target.Close()
	query := &jsonquery.Config{Expression: "ready", Operator: jsonquery.Equal, Expected: json.RawMessage(`true`)}
	r := ActiveProbe(context.Background(), Monitor{MonitorInput: MonitorInput{Type: "http", Target: target.URL, TimeoutMs: 5000, HTTP: &checks.HTTPConfig{JSONata: query}}})
	if r.State != "pending" || r.StatusCode != 200 || r.Message != "HTTP JSONata worker unavailable" {
		t.Fatalf("HTTP query capacity became a service outcome: %+v", r)
	}
	address := oauthQueryReviewMQTTFixture(t, `{"ready":true}`)
	start := time.Now()
	r = ServiceProbe(context.Background(), Monitor{MonitorInput: MonitorInput{Type: "mqtt", TimeoutMs: 5000,
		Service: &ServiceMonitorOptions{MQTT: &services.MQTTConfig{Address: address, Topic: "status/+", TLSMode: "plain", JSONata: query}}}})
	if r.State != "pending" || r.Message != "MQTT JSONata worker unavailable" || time.Since(start) > 3*time.Second {
		t.Fatalf("MQTT query capacity did not promptly remain pending: %+v", r)
	}
}
