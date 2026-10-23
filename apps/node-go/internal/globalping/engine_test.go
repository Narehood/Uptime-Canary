package globalping_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gp "uptime-canary/apps/node-go/internal/globalping"
	"uptime-canary/apps/node-go/internal/jsonquery"
	"uptime-canary/apps/node-go/internal/probeworker"
)

func TestMain(m *testing.M) {
	if handled, code := probeworker.Dispatch(os.Args[1:]); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

const apiSecret = "provider-token-secret"
const targetSecret = "target-credential-secret"

func config(mode string) gp.Config {
	c := gp.Config{Target: "example.com", Timeout: 4 * time.Second, Options: gp.Options{Mode: mode, Location: "New York + US + eyeball-network", Token: apiSecret}}
	switch mode {
	case "ping":
		c.Options.Ping = &gp.PingOptions{}
	case "http":
		c.Target = "https://example.com"
		c.Options.HTTP = &gp.HTTPOptions{}
	case "dns":
		c.Options.DNS = &gp.DNSOptions{}
	}
	return c
}

func ping() map[string]any {
	return map[string]any{"status": "finished", "rawOutput": targetSecret,
		"stats":   map[string]any{"min": 7.25, "avg": 8.5, "max": 9.75, "total": 3, "rcv": 3, "drop": 0, "loss": 0},
		"timings": []any{map[string]any{"rtt": 7.25, "ttl": 64}, map[string]any{"rtt": 8.5, "ttl": 64}, map[string]any{"rtt": 9.75, "ttl": 64}}}
}

func httpResult() map[string]any {
	return map[string]any{"status": "finished", "rawOutput": targetSecret, "rawBody": `{"ok":true,"word":"healthy"}`, "truncated": false,
		"statusCode": 200, "tls": map[string]any{"authorized": true}, "timings": map[string]any{"total": 17, "dns": 1, "tcp": 2, "tls": 3, "firstByte": 4, "download": 7}}
}

func dnsResult() map[string]any {
	return map[string]any{"status": "finished", "rawOutput": targetSecret, "statusCode": 0, "statusCodeName": "NOERROR", "resolver": "1.1.1.1", "timings": map[string]any{"total": 4.5},
		"answers": []any{map[string]any{"name": "example.com.", "type": "A", "ttl": 300, "class": "IN", "value": "192.0.2.1"}}}
}

func envelope(mode, target string, result map[string]any) map[string]any {
	return map[string]any{"id": "fixture_ID-1", "type": mode, "target": target, "createdAt": time.Now().UTC().Format(time.RFC3339Nano), "status": "finished", "probesCount": 1,
		"results": []any{map[string]any{"probe": map[string]any{"city": targetSecret}, "result": result}}}
}

func sendJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Every production Check in this suite uses a loopback server through public
// APIURL/APICA options. No transport, global DNS, or system trust is replaced.
func fixture(t *testing.T, c gp.Config, response any, inspect func(*http.Request, map[string]any)) (gp.Config, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	posts, gets := &atomic.Int32{}, &atomic.Int32{}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+apiSecret {
			t.Error("provider credential missing or mixed")
		}
		if r.Header.Get("X-Target-Secret") != "" {
			t.Error("target header escaped JSON namespace")
		}
		if r.Method == "POST" {
			posts.Add(1)
			if r.URL.Path != "/proxy/v1/measurements" {
				t.Errorf("creation path %q", r.URL.Path)
			}
			var posted map[string]any
			b, _ := io.ReadAll(io.LimitReader(r.Body, 128<<10))
			if json.Unmarshal(b, &posted) != nil {
				t.Error("invalid creation JSON")
			}
			if strings.Contains(string(b), apiSecret) {
				t.Error("provider credential in payload")
			}
			if posted["limit"] != float64(1) || posted["inProgressUpdates"] != false {
				t.Error("probe budget or updates wrong")
			}
			locations, ok := posted["locations"].([]any)
			if !ok || len(locations) != 1 || locations[0].(map[string]any)["magic"] != c.Options.Location {
				t.Error("location mapping wrong")
			}
			if inspect != nil {
				inspect(r, posted)
			}
			sendJSON(w, 202, map[string]any{"id": "fixture_ID-1", "probesCount": 1})
			return
		}
		gets.Add(1)
		if r.Method != "GET" || r.URL.Path != "/proxy/v1/measurements/fixture_ID-1" {
			t.Errorf("poll path/method wrong")
		}
		if raw, ok := response.([]byte); ok {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(raw)
			return
		}
		sendJSON(w, 200, response)
	}))
	t.Cleanup(s.Close)
	c.Options.APIURL, c.Options.AllowInsecureAPI = s.URL+"/proxy", true
	return c, posts, gets
}

func assertSafe(t *testing.T, r gp.Result) {
	t.Helper()
	b, _ := json.Marshal(r)
	for _, secret := range []string{apiSecret, targetSecret, "example.com", "New York"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("result leaked input/provider content")
		}
	}
	if r.LatencyMs < 0 {
		t.Fatal("negative latency")
	}
}

func TestPingPayloadAndRemoteRTT(t *testing.T) {
	for _, tc := range []struct {
		name, target, protocol string
		version, port          int
	}{
		{"icmp4", "example.com", "ICMP", 4, 0}, {"tcp6", "example.com", "TCP", 6, 8443},
		{"numeric4", "192.0.2.1", "ICMP", 0, 0}, {"numeric6", "2001:db8::1", "TCP", 0, 443},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := config("ping")
			c.Target = tc.target
			c.Options.IPVersion = tc.version
			c.Options.ProbeTimeoutSeconds = 5
			c.Options.Ping = &gp.PingOptions{Packets: 3, Protocol: tc.protocol, Port: tc.port}
			c, posts, gets := fixture(t, c, envelope("ping", tc.target, ping()), func(_ *http.Request, p map[string]any) {
				o := p["measurementOptions"].(map[string]any)
				if p["target"] != tc.target || p["type"] != "ping" || p["timeout"] != float64(5) || o["packets"] != float64(3) || o["protocol"] != tc.protocol {
					t.Error("ping payload wrong")
				}
				if tc.version != 0 && o["ipVersion"] != float64(tc.version) {
					t.Error("IP family missing")
				}
				if tc.version == 0 {
					if _, ok := o["ipVersion"]; ok {
						t.Error("IP family invented")
					}
				}
				if tc.port != 0 && o["port"] != float64(tc.port) {
					t.Error("TCP port missing")
				}
				if tc.port == 0 {
					if _, ok := o["port"]; ok {
						t.Error("ICMP port set")
					}
				}
			})
			r := gp.Check(context.Background(), c)
			assertSafe(t, r)
			if r.State != gp.Up || !r.LatencyAvailable || r.LatencyMs != 8.5 || posts.Load() != 1 || gets.Load() != 1 {
				t.Fatalf("result %+v requests %d/%d", r, posts.Load(), gets.Load())
			}
		})
	}
}

func TestPingLoss(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rcv   int
		loss  float64
		state string
	}{{"partial", 2, 33.333, gp.Up}, {"all", 0, 100, gp.Down}} {
		t.Run(tc.name, func(t *testing.T) {
			r := ping()
			s := r["stats"].(map[string]any)
			s["rcv"], s["drop"], s["loss"] = tc.rcv, 3-tc.rcv, tc.loss
			r["timings"] = r["timings"].([]any)[:tc.rcv]
			if tc.rcv == 0 {
				s["min"], s["avg"], s["max"] = nil, nil, nil
			}
			c, _, _ := fixture(t, config("ping"), envelope("ping", "example.com", r), nil)
			got := gp.Check(context.Background(), c)
			if got.State != tc.state || got.LatencyAvailable != (tc.rcv > 0) {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestHTTPPayloadAndConditions(t *testing.T) {
	c := config("http")
	c.Target = "https://example.com:8443/a%2Fb?q=a%2Bb&x=2"
	c.Options.HTTP = &gp.HTTPOptions{Protocol: "HTTP2", Headers: map[string]string{"Authorization": "Bearer " + targetSecret, "X-Target-Secret": targetSecret}, Keyword: "healthy"}
	c, _, _ = fixture(t, c, envelope("http", "example.com", httpResult()), func(_ *http.Request, p map[string]any) {
		o := p["measurementOptions"].(map[string]any)
		req := o["request"].(map[string]any)
		if p["target"] != "example.com" || o["protocol"] != "HTTP2" || o["port"] != float64(8443) || req["path"] != "/a%2Fb" || req["query"] != "q=a%2Bb&x=2" || req["method"] != "GET" {
			t.Error("HTTP mapping wrong")
		}
		if req["headers"].(map[string]any)["Authorization"] != "Bearer "+targetSecret {
			t.Error("target credential missing")
		}
	})
	r := gp.Check(context.Background(), c)
	assertSafe(t, r)
	if r.State != gp.Up || r.StatusCode != 200 || r.LatencyMs != 17 || !r.LatencyAvailable {
		t.Fatalf("%+v", r)
	}
}

func TestHTTPResults(t *testing.T) {
	tests := []struct {
		name   string
		change func(*gp.Config, map[string]any)
		state  string
	}{
		{"status mismatch", func(c *gp.Config, r map[string]any) { r["statusCode"] = 500 }, gp.Down},
		{"ranges", func(c *gp.Config, r map[string]any) {
			r["statusCode"] = 418
			c.Options.HTTP.AcceptedStatuses = []gp.StatusRange{{Min: 418, Max: 418}, {Min: 500, Max: 502}}
		}, gp.Up},
		{"keyword mismatch", func(c *gp.Config, r map[string]any) { c.Options.HTTP.Keyword = "absent" }, gp.Down},
		{"inverted keyword", func(c *gp.Config, r map[string]any) {
			c.Options.HTTP.Keyword = "absent"
			c.Options.HTTP.InvertKeyword = true
		}, gp.Up},
		{"inverted present", func(c *gp.Config, r map[string]any) {
			c.Options.HTTP.Keyword = "healthy"
			c.Options.HTTP.InvertKeyword = true
		}, gp.Down},
		{"truncated keyword", func(c *gp.Config, r map[string]any) { c.Options.HTTP.Keyword = "healthy"; r["truncated"] = true }, gp.Down},
		{"truncated inverted", func(c *gp.Config, r map[string]any) {
			c.Options.HTTP.Keyword = "absent"
			c.Options.HTTP.InvertKeyword = true
			r["truncated"] = true
		}, gp.Down},
		{"rawOutput cannot match", func(c *gp.Config, r map[string]any) { c.Options.HTTP.Keyword = targetSecret; r["rawBody"] = "nothing" }, gp.Down},
		{"missing body", func(c *gp.Config, r map[string]any) { delete(r, "rawBody") }, gp.Pending},
		{"null body condition", func(c *gp.Config, r map[string]any) { c.Options.HTTP.Keyword = "healthy"; r["rawBody"] = nil }, gp.Down},
		{"null body status only", func(c *gp.Config, r map[string]any) { r["rawBody"] = nil }, gp.Up},
		{"untrusted target TLS", func(c *gp.Config, r map[string]any) {
			r["tls"] = map[string]any{"authorized": false, "error": targetSecret}
		}, gp.Down},
		{"ignore target TLS", func(c *gp.Config, r map[string]any) {
			c.Options.HTTP.IgnoreTLS = true
			r["tls"] = map[string]any{"authorized": false}
		}, gp.Up},
		{"missing target TLS", func(c *gp.Config, r map[string]any) { delete(r, "tls") }, gp.Pending},
		{"null HTTPS TLS", func(c *gp.Config, r map[string]any) { r["tls"] = nil }, gp.Pending},
		{"plain HTTP", func(c *gp.Config, r map[string]any) { c.Target = "http://example.com/"; r["tls"] = nil }, gp.Up},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := config("http")
			r := httpResult()
			tt.change(&c, r)
			c, _, _ = fixture(t, c, envelope("http", "example.com", r), nil)
			got := gp.Check(context.Background(), c)
			assertSafe(t, got)
			if got.State != tt.state {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestHTTPMethods(t *testing.T) {
	for _, method := range []string{"GET", "HEAD", "OPTIONS"} {
		t.Run(method, func(t *testing.T) {
			c := config("http")
			c.Options.HTTP.Method = method
			c, _, _ = fixture(t, c, envelope("http", "example.com", httpResult()), func(_ *http.Request, p map[string]any) {
				if p["measurementOptions"].(map[string]any)["request"].(map[string]any)["method"] != method {
					t.Error("wrong method")
				}
			})
			if r := gp.Check(context.Background(), c); r.State != gp.Up {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestDNSPayloadAndSemantics(t *testing.T) {
	for _, q := range []string{"A", "AAAA", "ANY", "CNAME", "DNSKEY", "DS", "HTTPS", "MX", "NS", "NSEC", "PTR", "RRSIG", "SOA", "TXT", "SRV", "SVCB"} {
		t.Run(q, func(t *testing.T) {
			c := config("dns")
			c.Options.DNS = &gp.DNSOptions{QueryType: q, Protocol: "TCP", Resolver: "resolver.example.com", Port: 5353, Regex: `^192\.0\.2\.1$`}
			c, _, _ = fixture(t, c, envelope("dns", "example.com", dnsResult()), func(_ *http.Request, p map[string]any) {
				o := p["measurementOptions"].(map[string]any)
				if o["query"].(map[string]any)["type"] != q || o["protocol"] != "TCP" || o["port"] != float64(5353) || o["resolver"] != "resolver.example.com" {
					t.Error("DNS mapping wrong")
				}
			})
			if r := gp.Check(context.Background(), c); r.State != gp.Up || r.LatencyMs != 4.5 || !r.LatencyAvailable {
				t.Fatalf("%+v", r)
			}
		})
	}
	for _, tt := range []struct {
		name   string
		change func(*gp.Config, map[string]any)
		state  string
	}{
		{"empty", func(c *gp.Config, r map[string]any) { r["answers"] = []any{} }, gp.Down},
		{"allow empty", func(c *gp.Config, r map[string]any) { r["answers"] = []any{}; c.Options.DNS.AllowEmptyAnswers = true }, gp.Up},
		{"empty never regex", func(c *gp.Config, r map[string]any) {
			r["answers"] = []any{}
			c.Options.DNS.AllowEmptyAnswers = true
			c.Options.DNS.Regex = ".*"
		}, gp.Down},
		{"NXDOMAIN", func(c *gp.Config, r map[string]any) {
			r["answers"] = []any{}
			r["statusCode"], r["statusCodeName"] = 3, "NXDOMAIN"
			c.Options.DNS.AllowEmptyAnswers = true
		}, gp.Down},
		{"SERVFAIL", func(c *gp.Config, r map[string]any) { r["statusCode"], r["statusCodeName"] = 2, "SERVFAIL" }, gp.Down},
		{"case insensitive", func(c *gp.Config, r map[string]any) {
			r["answers"].([]any)[0].(map[string]any)["value"] = "MiXeD.Example."
			c.Options.DNS.Regex = `^mixed\.example\.$`
		}, gp.Up},
		{"regex mismatch", func(c *gp.Config, r map[string]any) { c.Options.DNS.Regex = "absent" }, gp.Down},
		{"raw output ignored", func(c *gp.Config, r map[string]any) { c.Options.DNS.Regex = targetSecret }, gp.Down},
		{"invalid answer value", func(c *gp.Config, r map[string]any) { r["answers"].([]any)[0].(map[string]any)["value"] = 123 }, gp.Pending},
		{"missing answer value", func(c *gp.Config, r map[string]any) { delete(r["answers"].([]any)[0].(map[string]any), "value") }, gp.Pending},
		{"oversized answer value", func(c *gp.Config, r map[string]any) {
			r["answers"].([]any)[0].(map[string]any)["value"] = strings.Repeat("a", 8193)
		}, gp.Pending},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := config("dns")
			r := dnsResult()
			tt.change(&c, r)
			c, _, _ = fixture(t, c, envelope("dns", "example.com", r), nil)
			got := gp.Check(context.Background(), c)
			assertSafe(t, got)
			if got.State != tt.state {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestProviderAndTargetFailureStates(t *testing.T) {
	for _, tt := range []struct{ status, source, state string }{{"failed", "internal", gp.Pending}, {"failed", "target", gp.Down}, {"failed", "resolver", gp.Down}, {"failed", "", gp.Down}, {"offline", "", gp.Pending}, {"in-progress", "", gp.Pending}, {"mystery", "", gp.Pending}, {"failed", "mystery", gp.Pending}} {
		for _, down := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%s/%t", tt.status, tt.source, down), func(t *testing.T) {
				r := map[string]any{"status": tt.status, "rawOutput": targetSecret}
				if tt.source != "" {
					r["failureSource"] = tt.source
				}
				c := config("ping")
				c.Options.APIFailureIsDown = down
				c, _, _ = fixture(t, c, envelope("ping", "example.com", r), nil)
				got := gp.Check(context.Background(), c)
				assertSafe(t, got)
				want := tt.state
				if down && want == gp.Pending {
					want = gp.Down
				}
				if got.State != want || got.LatencyAvailable {
					t.Fatalf("%+v want %s", got, want)
				}
			})
		}
	}
}

func TestJSONataAND(t *testing.T) {
	for _, tt := range []struct {
		name, expression, body, keyword string
		code                            int
		state                           string
	}{
		{"match", "ok", `{"ok":true,"word":"healthy"}`, "healthy", 200, gp.Up},
		{"query mismatch", "ok", `{"ok":false,"word":"healthy"}`, "healthy", 200, gp.Down},
		{"keyword AND", "ok", `{"ok":true}`, "healthy", 200, gp.Down},
		{"status AND", "ok", `{"ok":true,"word":"healthy"}`, "healthy", 500, gp.Down},
		{"malformed body", "$", `not JSON`, "", 200, gp.Down},
		{"duplicate body", "ok", `{"ok":false,"ok":true}`, "", 200, gp.Down},
		{"invalid expression", "(", `{"ok":true}`, "", 200, gp.Down},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := config("http")
			c.Options.HTTP.Keyword = tt.keyword
			c.Options.HTTP.JSONata = &jsonquery.Config{Expression: tt.expression, Operator: jsonquery.Equal, Expected: json.RawMessage(`true`)}
			r := httpResult()
			r["rawBody"], r["statusCode"] = tt.body, tt.code
			c, _, _ = fixture(t, c, envelope("http", "example.com", r), nil)
			got := gp.Check(context.Background(), c)
			assertSafe(t, got)
			if got.State == gp.Pending && got.Message == "Globalping HTTP JSONata worker unavailable" {
				t.Fatalf("guarded worker unavailable on this host; no match evidence: %+v", got)
			}
			if got.State != tt.state {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestJSONataAdmissionStaysPendingWithProviderDown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := make(chan struct{}, 2)
	done := make(chan struct{}, 2)
	var workers sync.WaitGroup
	defer workers.Wait()
	defer cancel()
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			started <- struct{}{}
			jsonquery.Check(ctx, jsonquery.Request{Type: jsonquery.Type, Timeout: 3 * time.Second,
				Query: jsonquery.Config{Expression: `$sum($map([1..10000], function($v) { $sum([1..10000]) }))`, Operator: jsonquery.Equal, Expected: json.RawMessage(`0`)}, Document: json.RawMessage(`{}`)})
			done <- struct{}{}
		}()
	}
	<-started
	<-started
	select {
	case <-done:
		t.Fatal("capacity fixture finished before admission could be exercised")
	case <-time.After(100 * time.Millisecond):
	}
	c := config("http")
	c.Options.APIFailureIsDown = true
	c.Options.HTTP.JSONata = &jsonquery.Config{Expression: "ok", Operator: jsonquery.Equal, Expected: json.RawMessage(`true`)}
	c, _, _ = fixture(t, c, envelope("http", "example.com", httpResult()), nil)
	r := gp.Check(context.Background(), c)
	if r.State != gp.Pending || r.Message != "Globalping HTTP JSONata worker unavailable" || !r.LatencyAvailable || r.LatencyMs != 17 {
		t.Fatalf("unavailable worker was not Pending with remote latency: %+v", r)
	}
}

func TestPollingETagAndResponseDelay(t *testing.T) {
	var posts, gets atomic.Int32
	var mu sync.Mutex
	var finished []time.Time
	var arrivals []time.Time
	m := envelope("ping", "example.com", map[string]any{"status": "in-progress"})
	m["status"] = "in-progress"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			w.Header().Set("Location", "https://elsewhere.invalid/credential-sink")
			sendJSON(w, 202, map[string]any{"id": "fixture_ID-1", "probesCount": 1})
			return
		}
		n := gets.Add(1)
		mu.Lock()
		arrivals = append(arrivals, time.Now())
		mu.Unlock()
		if n == 1 {
			time.Sleep(120 * time.Millisecond)
			w.Header().Set("ETag", `"progress-1"`)
			sendJSON(w, 200, m)
		} else if n == 2 {
			if r.Header.Get("If-None-Match") != `"progress-1"` {
				t.Error("ETag not reused")
			}
			w.WriteHeader(304)
		} else {
			m["status"] = "finished"
			m["results"].([]any)[0].(map[string]any)["result"] = ping()
			sendJSON(w, 200, m)
		}
		mu.Lock()
		finished = append(finished, time.Now())
		mu.Unlock()
	}))
	defer s.Close()
	c := config("ping")
	c.Options.APIURL, c.Options.AllowInsecureAPI = s.URL, true
	r := gp.Check(context.Background(), c)
	if r.State != gp.Up || posts.Load() != 1 || gets.Load() != 3 {
		t.Fatalf("%+v counts %d/%d", r, posts.Load(), gets.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	for i := 1; i < len(arrivals); i++ {
		if arrivals[i].Sub(finished[i-1]) < 480*time.Millisecond {
			t.Fatal("poll delay started before response completed")
		}
	}
}

func TestAPITLSAndTrust(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			sendJSON(w, 202, map[string]any{"id": "fixture_ID-1", "probesCount": 1})
		} else {
			sendJSON(w, 200, envelope("ping", "example.com", ping()))
		}
	})
	s := httptest.NewUnstartedServer(handler)
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.StartTLS()
	defer s.Close()
	c := config("ping")
	c.Options.APIURL = s.URL
	if r := gp.Check(context.Background(), c); r.State != gp.Pending {
		t.Fatalf("untrusted API accepted %+v", r)
	}
	c.Options.APICA = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.Certificate().Raw}))
	if r := gp.Check(context.Background(), c); r.State != gp.Up {
		t.Fatalf("custom trust failed %+v", r)
	}
	// Same CA with a mismatched IP must still fail verified hostname validation.
	if s.Certificate().VerifyHostname("localhost") == nil {
		t.Fatal("fixture certificate unexpectedly covers localhost")
	}
	c.Options.APIURL = strings.Replace(s.URL, "127.0.0.1", "localhost", 1)
	if r := gp.Check(context.Background(), c); r.State != gp.Pending {
		t.Fatalf("mismatched hostname accepted %+v", r)
	}
}

func TestDeadlineCancellationClosesBody(t *testing.T) {
	for _, phase := range []string{"create", "poll", "body", "wait"} {
		t.Run(phase, func(t *testing.T) {
			var posts, gets atomic.Int32
			cancelled := make(chan struct{}, 1)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if r.Method == "POST" {
					posts.Add(1)
					if phase != "create" {
						sendJSON(w, 202, map[string]any{"id": "fixture_ID-1", "probesCount": 1})
						return
					}
				} else {
					gets.Add(1)
					if phase == "wait" {
						m := envelope("ping", "example.com", map[string]any{"status": "in-progress"})
						m["status"] = "in-progress"
						sendJSON(w, 200, m)
						return
					}
					if phase == "body" {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"id":`)
						w.(http.Flusher).Flush()
					}
				}
				<-r.Context().Done()
				cancelled <- struct{}{}
			}))
			defer s.Close()
			c := config("ping")
			c.Timeout = 140 * time.Millisecond
			c.Options.APIURL, c.Options.AllowInsecureAPI = s.URL, true
			start := time.Now()
			got := gp.Check(context.Background(), c)
			if got.State != gp.Pending || time.Since(start) > time.Second || posts.Load() != 1 || gets.Load() > 1 {
				t.Fatalf("%+v elapsed %s", got, time.Since(start))
			}
			if phase != "wait" {
				select {
				case <-cancelled:
				case <-time.After(time.Second):
					t.Fatal("API request/body not closed")
				}
			}
			before := gets.Load()
			time.Sleep(30 * time.Millisecond)
			if gets.Load() != before {
				t.Fatal("work started after cancellation")
			}
		})
	}
	c := config("ping")
	c.Options.APIURL = "http://127.0.0.1:1"
	c.Options.AllowInsecureAPI = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := gp.Check(ctx, c); r.State != gp.Pending {
		t.Fatalf("%+v", r)
	}
}
