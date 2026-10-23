package checks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func oauthFixture(t *testing.T, handler http.HandlerFunc, client bool) (*httptest.Server, fixturePKI) {
	t.Helper()
	pki := newFixturePKI(t, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	s := httptest.NewUnstartedServer(handler)
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.TLS = &tls.Config{Certificates: []tls.Certificate{pki.server}, ClientAuth: tls.RequestClientCert}
	if client {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM([]byte(pki.caPEM))
		s.TLS.ClientCAs, s.TLS.ClientAuth = pool, tls.RequireAndVerifyClientCert
	}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s, pki
}

func TestOAuth2ClientCredentialsSeparateOriginsAndTrust(t *testing.T) {
	for _, method := range []string{"", "client_secret_basic", "client_secret_post"} {
		t.Run("method-"+method, func(t *testing.T) {
			const client, secret = "client: with+percent%", "secret:/ +percent%"
			var grants, targets atomic.Int64
			tokens, tokenPKI := oauthFixture(t, func(w http.ResponseWriter, r *http.Request) {
				grants.Add(1)
				if r.Method != "POST" || r.Header.Get("X-Monitor-Secret") != "" || len(r.TLS.PeerCertificates) != 0 {
					t.Error("target headers or client certificate reached token endpoint")
				}
				if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "client_credentials" || r.Form.Get("scope") != "read write" || r.Form.Get("audience") != "private-api" {
					t.Error("grant fields do not match configured client credentials")
				}
				if method == "client_secret_post" {
					if r.Header.Get("Authorization") != "" || r.Form.Get("client_id") != client || r.Form.Get("client_secret") != secret {
						t.Error("client_secret_post did not use only encoded form credentials")
					}
				} else {
					user, password, ok := r.BasicAuth()
					if !ok || user != url.QueryEscape(client) || password != url.QueryEscape(secret) || r.Form.Has("client_id") || r.Form.Has("client_secret") {
						t.Error("Basic grant did not follow RFC 6749 credential encoding")
					}
				}
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				fmt.Fprint(w, `{"access_token":"fixture-token._~+/==","token_type":"bearer","expires_in":3600}`)
			}, false)
			target, targetPKI := oauthFixture(t, func(w http.ResponseWriter, r *http.Request) {
				targets.Add(1)
				body, _ := io.ReadAll(r.Body)
				if r.Header.Get("Authorization") != "Bearer fixture-token._~+/==" || r.Header.Get("X-Monitor-Secret") != "private-header" || string(body) != "private-body" || len(r.TLS.PeerCertificates) != 1 {
					t.Error("target lost its independent HTTP configuration or received wrong credentials")
				}
				fmt.Fprint(w, `{"ok":true}`)
			}, true)
			options := &HTTPConfig{Method: "POST", Body: "private-body", Headers: map[string]string{"X-Monitor-Secret": "private-header"}, JSON: &JSONMatcher{Path: "$.ok", Expected: "true"},
				OAuth2: &OAuth2Config{TokenURL: tokens.URL, ClientID: client, ClientSecret: secret, Scope: "read write", Audience: "private-api", AuthMethod: method, TokenCAPEM: tokenPKI.caPEM}}
			c := Config{Type: HTTP, Target: target.URL, Timeout: 3 * time.Second, HTTP: options, TLS: &TLSConfig{CAPEM: targetPKI.caPEM, ClientCertPEM: targetPKI.clientPEM, ClientKeyPEM: targetPKI.clientKeyPEM}}
			for range 2 {
				r := Check(context.Background(), c)
				if r.State != Up || r.StatusCode != 200 {
					t.Fatal("authenticated check failed", r)
				}
				if strings.Contains(r.Message, secret) || strings.Contains(r.Message, "fixture-token") {
					t.Fatal("credentials reached check result")
				}
			}
			if grants.Load() != 2 || targets.Load() != 2 || options.BearerToken != "" {
				t.Fatal("tokens were reused or written into immutable configuration")
			}
		})
	}
}

func TestOAuth2RejectsResponseErrorsWithoutTargetAccess(t *testing.T) {
	for name, body := range map[string]string{
		"missing type":     `{"access_token":"fixture-token"}`,
		"wrong type":       `{"access_token":"fixture-token","token_type":"MAC"}`,
		"duplicate token":  `{"access_token":"one","access_token":"two","token_type":"Bearer"}`,
		"case alias":       `{"Access_Token":"fixture-token","token_type":"Bearer"}`,
		"null token":       `{"access_token":null,"token_type":"Bearer"}`,
		"header injection": `{"access_token":"private-secret\r\nX-Injected: yes","token_type":"Bearer"}`,
		"padding only":     `{"access_token":"==","token_type":"Bearer"}`,
		"space":            `{"access_token":"private secret","token_type":"Bearer"}`,
		"oversize":         strings.Repeat("x", (64<<10)+1),
		"depth":            strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66),
		"trailing":         `{"access_token":"x","token_type":"Bearer"} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			var targets atomic.Int64
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targets.Add(1) }))
			defer target.Close()
			token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, body)
			}))
			defer token.Close()
			c := Config{Type: HTTP, Target: target.URL, Timeout: time.Second, HTTP: &HTTPConfig{OAuth2: &OAuth2Config{TokenURL: token.URL, ClientID: "client", AllowInsecureHTTP: true}}}
			r := Check(context.Background(), c)
			if r.State != Down || targets.Load() != 0 || strings.Contains(r.Message, "private") || strings.Contains(r.Message, body) {
				t.Fatal("malformed grant was used or leaked", r)
			}
		})
	}
}

func TestOAuth2RefusesTokenRedirectAndStripsTargetRedirectCredentials(t *testing.T) {
	var redirects, targets atomic.Int64
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirects.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Private") != "" {
			t.Error("credentials crossed the monitored origin")
		}
	}))
	defer other.Close()
	var redirectGrant atomic.Bool
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if redirectGrant.Load() {
			http.Redirect(w, r, other.URL, 307)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"fixture-token","token_type":"Bearer"}`)
	}))
	defer token.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targets.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Error("first origin lost bearer token")
		}
		http.Redirect(w, r, other.URL, 302)
	}))
	defer target.Close()
	c := Config{Type: HTTP, Target: target.URL, Timeout: time.Second, HTTP: &HTTPConfig{Headers: map[string]string{"X-Private": "private"}, OAuth2: &OAuth2Config{TokenURL: token.URL, ClientID: "client", ClientSecret: "private", AllowInsecureHTTP: true}}}
	if result := Check(context.Background(), c); result.State != Up || redirects.Load() != 1 || targets.Load() != 1 {
		t.Fatal("ordinary monitored redirect did not work without credentials", result)
	}
	redirectGrant.Store(true)
	if result := Check(context.Background(), c); result.State != Down || redirects.Load() != 1 || targets.Load() != 1 {
		t.Fatal("token grant followed a redirect", result)
	}
}

func TestOAuth2StrictConfigurationAndPlaintextOptIn(t *testing.T) {
	for _, body := range []string{
		`{"tokenUrl":"https://example.test","tokenUrl":"https://other.test"}`,
		`{"TokenURL":"https://example.test"}`,
		`{"clientSecret":null}`,
		`{"allowInsecureHttp":"true"}`,
		`{"tokenUrl":"https://example.test","unknown":1}`,
	} {
		var o OAuth2Config
		if json.Unmarshal([]byte(body), &o) == nil {
			t.Fatal("ambiguous OAuth2 configuration accepted")
		}
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.HTTP.OAuth2.TokenURL = "http://issuer.test/token" },
		func(c *Config) { c.Target = "http://target.test" },
		func(c *Config) { c.Target = "HTTP://target.test" },
		func(c *Config) { c.HTTP.BasicAuth = &BasicAuth{} },
		func(c *Config) { c.HTTP.BearerToken = "private" },
		func(c *Config) { c.HTTP.Headers = map[string]string{"authorization": "private"} },
		func(c *Config) { c.HTTP.OAuth2.AuthMethod = "client_secret_jwt" },
		func(c *Config) { c.HTTP.OAuth2.TokenCAPEM = "private-invalid-ca" },
	} {
		c := Config{Type: HTTP, Target: "https://target.test", Timeout: time.Second, HTTP: &HTTPConfig{OAuth2: &OAuth2Config{TokenURL: "https://issuer.test/token", ClientID: "client"}}}
		mutate(&c)
		if err := Validate(c); err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("invalid or ambiguous OAuth2 configuration accepted, or its values leaked")
		}
	}
}

func TestOAuth2UsesWholeCheckDeadlineAndDoesNotTrustTargetCA(t *testing.T) {
	var targets atomic.Int64
	token, _ := oauthFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"fixture-token","token_type":"Bearer"}`)
	}, false)
	target, pki := oauthFixture(t, func(w http.ResponseWriter, r *http.Request) { targets.Add(1) }, false)
	c := Config{Type: HTTP, Target: target.URL, Timeout: time.Second, TLS: &TLSConfig{CAPEM: pki.caPEM}, HTTP: &HTTPConfig{OAuth2: &OAuth2Config{TokenURL: token.URL, ClientID: "client"}}}
	if result := Check(context.Background(), c); result.State != Down || targets.Load() != 0 {
		t.Fatal("target CA implicitly trusted a different token endpoint", result)
	}
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer blocked.Close()
	c.HTTP.OAuth2.TokenURL, c.HTTP.OAuth2.AllowInsecureHTTP, c.Timeout = blocked.URL, true, 60*time.Millisecond
	start := time.Now()
	if result := Check(context.Background(), c); result.State != Down || time.Since(start) > time.Second || targets.Load() != 0 {
		t.Fatal("token request escaped whole-check deadline", result)
	}
}
