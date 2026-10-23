package checks

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPMethodsAuthStatusAndBodyMatchers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/basic":
			user, password, ok := r.BasicAuth()
			if !ok || user != "admin" || password != "private" {
				w.WriteHeader(401)
				return
			}
		case "/request":
			body, _ := io.ReadAll(r.Body)
			if r.Method != "PATCH" || string(body) != "payload" || r.Header.Get("X-Check") != "yes" || r.Header.Get("Authorization") != "Bearer private" {
				w.WriteHeader(400)
				return
			}
		case "/status":
			w.WriteHeader(418)
		case "/json":
			fmt.Fprint(w, `{"state":{"ok":true,"items":[{"a b":2.0}]},"nil":null}`)
			return
		case "/invalid-json":
			fmt.Fprint(w, `{"broken":`)
			return
		case "/head":
			if r.Method != "HEAD" {
				w.WriteHeader(400)
			}
			return
		}
		fmt.Fprint(w, "healthy sequence 123")
	}))
	defer server.Close()
	cases := []struct {
		name, path, state string
		options           HTTPConfig
	}{
		{"default", "/", Up, HTTPConfig{}},
		{"basic", "/basic", Up, HTTPConfig{BasicAuth: &BasicAuth{"admin", "private"}}},
		{"method headers body bearer", "/request", Up, HTTPConfig{Method: "PATCH", Body: "payload", Headers: map[string]string{"X-Check": "yes"}, BearerToken: "private"}},
		{"status default", "/status", Down, HTTPConfig{}},
		{"status range", "/status", Up, HTTPConfig{StatusRanges: []StatusRange{{200, 299}, {400, 499}}}},
		{"status exact", "/status", Up, HTTPConfig{StatusRanges: []StatusRange{{418, 418}}}},
		{"keyword", "/", Up, HTTPConfig{Keyword: &KeywordMatcher{Value: "healthy"}}},
		{"keyword absent", "/", Down, HTTPConfig{Keyword: &KeywordMatcher{Value: "missing"}}},
		{"negative keyword", "/", Up, HTTPConfig{Keyword: &KeywordMatcher{Value: "failure", Negate: true}}},
		{"negative keyword fails", "/", Down, HTTPConfig{Keyword: &KeywordMatcher{Value: "healthy", Negate: true}}},
		{"regex", "/", Up, HTTPConfig{Keyword: &KeywordMatcher{Value: `sequence \d+`, Regex: true}}},
		{"negative regex", "/", Up, HTTPConfig{Keyword: &KeywordMatcher{Value: `failure \d+`, Regex: true, Negate: true}}},
		{"JSON bool", "/json", Up, HTTPConfig{JSON: &JSONMatcher{Path: "$.state.ok", Expected: "true"}}},
		{"JSON quoted key", "/json", Up, HTTPConfig{JSON: &JSONMatcher{Path: `$.state.items[0]["a b"]`, Expected: "2"}}},
		{"JSON missing", "/json", Down, HTTPConfig{JSON: &JSONMatcher{Path: "$.absent", ExistsOnly: true}}},
		{"JSON null exists", "/json", Up, HTTPConfig{JSON: &JSONMatcher{Path: "$.nil", ExistsOnly: true}}},
		{"JSON negative", "/json", Up, HTTPConfig{JSON: &JSONMatcher{Path: "$.state.ok", Expected: "false", Negate: true}}},
		{"JSON malformed even negative", "/invalid-json", Down, HTTPConfig{JSON: &JSONMatcher{Path: "$", ExistsOnly: true, Negate: true}}},
		{"HEAD", "/head", Up, HTTPConfig{Method: "HEAD"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Check(context.Background(), Config{Type: HTTP, Target: server.URL + tc.path + "?token=do-not-echo", Timeout: time.Second, HTTP: &tc.options})
			requireState(t, r, tc.state)
			if r.StatusCode == 0 || strings.Contains(r.Message, "do-not-echo") {
				t.Fatalf("unexpected result: %+v", r)
			}
		})
	}
}

func TestHTTPBodyAndHeaderBounds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/exact":
			io.WriteString(w, strings.Repeat("x", MaxBodyBytes))
		case "/body":
			io.WriteString(w, strings.Repeat("x", MaxBodyBytes+1))
		case "/gzip":
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			io.WriteString(gz, strings.Repeat("x", MaxBodyBytes+1))
			gz.Close()
		case "/header":
			w.Header().Set("X-Large", strings.Repeat("x", MaxResponseHeaderBytes+1))
			fmt.Fprint(w, "ok")
		}
	}))
	defer server.Close()
	for _, tc := range []struct{ path, state string }{{"/exact", Up}, {"/body", Down}, {"/gzip", Down}, {"/header", Down}} {
		t.Run(tc.path, func(t *testing.T) {
			requireState(t, Check(context.Background(), Config{Type: HTTP, Target: server.URL + tc.path, Timeout: 2 * time.Second}), tc.state)
		})
	}
	address := fixtureTCP(t, func(conn net.Conn) {
		buf := make([]byte, 4096)
		_, _ = conn.Read(buf)
		io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nshort")
	})
	requireState(t, Check(context.Background(), Config{Type: HTTP, Target: "http://" + address, Timeout: time.Second}), Down)
}

func TestHTTPRedirectOriginAndBodyRules(t *testing.T) {
	var leaked atomic.Bool
	var bodyReplayed atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("X-Secret") != "" {
			leaked.Store(true)
		}
		data, _ := io.ReadAll(r.Body)
		if len(data) > 0 {
			bodyReplayed.Store(true)
		}
		fmt.Fprint(w, "ok")
	}))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/loop":
			http.Redirect(w, r, "/loop", 302)
		case "/same":
			http.Redirect(w, r, "/verify", 307)
		case "/verify":
			body, _ := io.ReadAll(r.Body)
			if r.Method != "POST" || string(body) != "secret-body" || r.Header.Get("Authorization") != "Bearer secret" {
				w.WriteHeader(400)
			}
		case "/cross307":
			http.Redirect(w, r, other.URL, 307)
		case "/cross302":
			http.Redirect(w, r, other.URL, 302)
		case "/cross":
			http.Redirect(w, r, other.URL, 302)
		}
	}))
	defer server.Close()
	for _, tc := range []struct{ path, body, method, state string }{{"/same", "secret-body", "POST", Up}, {"/cross307", "secret-body", "POST", Down}, {"/cross302", "secret-body", "POST", Up}, {"/cross", "", "GET", Up}, {"/loop", "", "GET", Down}} {
		t.Run(tc.path, func(t *testing.T) {
			o := &HTTPConfig{Method: tc.method, Body: tc.body, BearerToken: "secret", Headers: map[string]string{"Cookie": "private=secret", "X-Secret": "secret"}}
			requireState(t, Check(context.Background(), Config{Type: HTTP, Target: server.URL + tc.path, Timeout: time.Second, HTTP: o}), tc.state)
		})
	}
	if leaked.Load() || bodyReplayed.Load() {
		t.Fatal("credentials or body crossed origins")
	}
	zero := 0
	r := Check(context.Background(), Config{Type: HTTP, Target: server.URL + "/loop", Timeout: time.Second, HTTP: &HTTPConfig{MaxRedirects: &zero, StatusRanges: []StatusRange{{302, 302}}}})
	requireState(t, r, Up)
}

func TestHTTPTimeoutCancellationAndExplicitProxy(t *testing.T) {
	entered := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	start := time.Now()
	r := Check(context.Background(), Config{Type: HTTP, Target: server.URL, Timeout: 50 * time.Millisecond})
	requireState(t, r, Down)
	if time.Since(start) > time.Second {
		t.Fatal("timeout was not bounded")
	}
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	go func() { done <- Check(ctx, Config{Type: HTTP, Target: server.URL, Timeout: time.Minute}) }()
	<-entered
	cancel()
	select {
	case r := <-done:
		requireState(t, r, Down)
	case <-time.After(time.Second):
		t.Fatal("blocked body read ignored cancellation")
	}
	var proxySeen atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxySeen.Store(r.URL.IsAbs() && r.URL.Host == "fixture.invalid")
		fmt.Fprint(w, "proxied")
	}))
	defer proxy.Close()
	r = Check(context.Background(), Config{Type: HTTP, Target: "http://fixture.invalid/check", Timeout: time.Second, HTTP: &HTTPConfig{ProxyURL: proxy.URL, Keyword: &KeywordMatcher{Value: "proxied"}}})
	requireState(t, r, Up)
	if !proxySeen.Load() {
		t.Fatal("explicit proxy not used")
	}
}
