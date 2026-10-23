package globalping_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gp "uptime-canary/apps/node-go/internal/globalping"
	"uptime-canary/apps/node-go/internal/jsonquery"
)

func TestValidateBoundedShape(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*gp.Config)
	}{
		{"no timeout", func(c *gp.Config) { c.Timeout = 0 }},
		{"excess timeout", func(c *gp.Config) { c.Timeout = 5*time.Minute + 1 }},
		{"no mode", func(c *gp.Config) { c.Options.Mode = "" }},
		{"unsupported mode", func(c *gp.Config) { c.Options.Mode = "mtr" }},
		{"missing options", func(c *gp.Config) { c.Options.Ping = nil }},
		{"irrelevant options", func(c *gp.Config) { c.Options.HTTP = &gp.HTTPOptions{} }},
		{"empty location", func(c *gp.Config) { c.Options.Location = " " }},
		{"long location", func(c *gp.Config) { c.Options.Location = strings.Repeat("x", 257) }},
		{"bad location", func(c *gp.Config) { c.Options.Location = "secret\nvalue" }},
		{"bad token", func(c *gp.Config) { c.Options.Token = "secret\r\nAuthorization: leaked" }},
		{"long token", func(c *gp.Config) { c.Options.Token = strings.Repeat("x", 4097) }},
		{"probe timeout min", func(c *gp.Config) { c.Options.ProbeTimeoutSeconds = 4 }},
		{"probe timeout max", func(c *gp.Config) { c.Options.ProbeTimeoutSeconds = 31 }},
		{"IP version", func(c *gp.Config) { c.Options.IPVersion = 5 }},
		{"numeric same family", func(c *gp.Config) { c.Target = "192.0.2.1"; c.Options.IPVersion = 4 }},
		{"numeric opposite family", func(c *gp.Config) { c.Target = "2001:db8::1"; c.Options.IPVersion = 4 }},
		{"packets min", func(c *gp.Config) { c.Options.Ping.Packets = -1 }},
		{"packets max", func(c *gp.Config) { c.Options.Ping.Packets = 17 }},
		{"ICMP port", func(c *gp.Config) { c.Options.Ping.Port = 443 }},
		{"UDP ping", func(c *gp.Config) { c.Options.Ping.Protocol = "UDP" }},
		{"host with port", func(c *gp.Config) { c.Target = "example.com:443" }},
		{"host with controls", func(c *gp.Config) { c.Target = "example.com\x00" }},
		{"insecure API", func(c *gp.Config) { c.Options.APIURL = "http://localhost" }},
		{"API userinfo", func(c *gp.Config) { c.Options.APIURL = "https://secret@example.com" }},
		{"API query", func(c *gp.Config) { c.Options.APIURL = "https://example.com?token=secret" }},
		{"API empty query", func(c *gp.Config) { c.Options.APIURL = "https://example.com?" }},
		{"API fragment", func(c *gp.Config) { c.Options.APIURL = "https://example.com#secret" }},
		{"API empty fragment", func(c *gp.Config) { c.Options.APIURL = "https://example.com#" }},
		{"API traversal", func(c *gp.Config) { c.Options.APIURL = "https://example.com/a/../b" }},
		{"API encoded traversal", func(c *gp.Config) { c.Options.APIURL = "https://example.com/%2e%2e" }},
		{"API port", func(c *gp.Config) { c.Options.APIURL = "https://example.com:70000" }},
		{"API backslash", func(c *gp.Config) { c.Options.APIURL = `https://example.com\secret` }},
		{"CA junk", func(c *gp.Config) { c.Options.APICA = "secret" }},
		{"CA oversized", func(c *gp.Config) { c.Options.APICA = strings.Repeat("x", 128<<10+1) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := config("ping")
			tt.change(&c)
			err := gp.Validate(c)
			if err == nil || err.Error() != "invalid Globalping configuration" {
				t.Fatal("configuration admitted or unsanitized error", err)
			}
		})
	}
	for _, tt := range []struct {
		name   string
		change func(*gp.Config)
	}{
		{"POST unsupported", func(c *gp.Config) { c.Options.HTTP.Method = "POST" }},
		{"HTTP2 requires TLS", func(c *gp.Config) { c.Target = "http://example.com"; c.Options.HTTP.Protocol = "HTTP2" }},
		{"protocol override unsupported", func(c *gp.Config) { c.Options.HTTP.Protocol = "HTTPS" }},
		{"numeric HTTP family", func(c *gp.Config) { c.Target = "http://[2001:db8::1]/"; c.Options.IPVersion = 6 }},
		{"HTTP userinfo", func(c *gp.Config) { c.Target = "https://user:secret@example.com" }},
		{"HTTP fragment", func(c *gp.Config) { c.Target = "https://example.com/#secret" }},
		{"invalid header name", func(c *gp.Config) { c.Options.HTTP.Headers = map[string]string{"bad name": "value"} }},
		{"header newline", func(c *gp.Config) { c.Options.HTTP.Headers = map[string]string{"Authorization": "secret\r\nX: y"} }},
		{"Host header", func(c *gp.Config) { c.Options.HTTP.Headers = map[string]string{"hOSt": "other.example"} }},
		{"reserved UA", func(c *gp.Config) { c.Options.HTTP.Headers = map[string]string{"User-Agent": "secret"} }},
		{"header ambiguity", func(c *gp.Config) {
			c.Options.HTTP.Headers = map[string]string{"Authorization": "a", "authorization": "b"}
		}},
		{"header total", func(c *gp.Config) { c.Options.HTTP.Headers = map[string]string{"X": strings.Repeat("x", 32<<10)} }},
		{"header count", func(c *gp.Config) {
			c.Options.HTTP.Headers = map[string]string{}
			for i := 0; i < 33; i++ {
				c.Options.HTTP.Headers[strings.Repeat("x", i+1)] = "a"
			}
		}},
		{"status lower bound", func(c *gp.Config) { c.Options.HTTP.AcceptedStatuses = []gp.StatusRange{{Min: 99, Max: 200}} }},
		{"status reversed", func(c *gp.Config) { c.Options.HTTP.AcceptedStatuses = []gp.StatusRange{{Min: 300, Max: 200}} }},
		{"empty inverted keyword", func(c *gp.Config) { c.Options.HTTP.InvertKeyword = true }},
		{"query shape", func(c *gp.Config) {
			c.Options.HTTP.JSONata = &jsonquery.Config{Expression: "ok", Operator: jsonquery.Equal, Expected: json.RawMessage(`null`)}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := config("http")
			tt.change(&c)
			if gp.Validate(c) == nil {
				t.Fatal("invalid HTTP config admitted")
			}
		})
	}
	for _, tt := range []struct {
		name   string
		change func(*gp.Config)
	}{
		{"CAA unsupported", func(c *gp.Config) { c.Options.DNS.QueryType = "CAA" }},
		{"DNS protocol", func(c *gp.Config) { c.Options.DNS.Protocol = "HTTPS" }},
		{"resolver URL", func(c *gp.Config) { c.Options.DNS.Resolver = "https://resolver.example" }},
		{"resolver port", func(c *gp.Config) { c.Options.DNS.Resolver = "1.1.1.1:53" }},
		{"PCRE unsupported", func(c *gp.Config) { c.Options.DNS.Regex = "(?=secret)" }},
		{"DNS port", func(c *gp.Config) { c.Options.DNS.Port = -1 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := config("dns")
			tt.change(&c)
			if gp.Validate(c) == nil {
				t.Fatal("invalid DNS config admitted")
			}
		})
	}
	c := config("http")
	c.Options.HTTP.JSONata = &jsonquery.Config{Expression: "(", Operator: jsonquery.Equal, Expected: json.RawMessage(`true`)}
	if gp.Validate(c) != nil {
		t.Fatal("shape validation compiled JSONata")
	}
	c = config("dns")
	c.Target = "_sip._tcp.example.com"
	if gp.Validate(c) != nil {
		t.Fatal("SRV owner name rejected")
	}
	c = config("ping")
	c.Options.Mode = " PING "
	if gp.Validate(c) != nil {
		t.Fatal("mode normalization rejected")
	}
}

func TestMalformedMeasurementAdmission(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"wrong ID", func(m map[string]any) { m["id"] = "other" }},
		{"wrong type", func(m map[string]any) { m["type"] = "http" }},
		{"wrong target", func(m map[string]any) { m["target"] = "attacker.example" }},
		{"wrong probe count", func(m map[string]any) { m["probesCount"] = 2 }},
		{"missing probe count", func(m map[string]any) { delete(m, "probesCount") }},
		{"empty results", func(m map[string]any) { m["results"] = []any{} }},
		{"extra result", func(m map[string]any) { m["results"] = append(m["results"].([]any), m["results"].([]any)[0]) }},
		{"missing result", func(m map[string]any) { delete(m["results"].([]any)[0].(map[string]any), "result") }},
		{"missing createdAt", func(m map[string]any) { delete(m, "createdAt") }},
		{"invalid createdAt", func(m map[string]any) { m["createdAt"] = "tomorrow" }},
		{"stale createdAt", func(m map[string]any) {
			m["createdAt"] = time.Now().Add(-31 * time.Second).UTC().Format(time.RFC3339Nano)
		}},
		{"future createdAt", func(m map[string]any) {
			m["createdAt"] = time.Now().Add(31 * time.Second).UTC().Format(time.RFC3339Nano)
		}},
		{"unknown status", func(m map[string]any) { m["status"] = "complete" }},
		{"invalid failure source", func(m map[string]any) {
			r := m["results"].([]any)[0].(map[string]any)["result"].(map[string]any)
			r["failureSource"] = "internal"
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := envelope("ping", "example.com", ping())
			tt.change(m)
			c, _, _ := fixture(t, config("ping"), m, nil)
			r := gp.Check(context.Background(), c)
			assertSafe(t, r)
			if r.State != gp.Pending {
				t.Fatalf("%+v", r)
			}
		})
	}
	for _, tt := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing stats", func(r map[string]any) { delete(r, "stats") }},
		{"missing avg", func(r map[string]any) { delete(r["stats"].(map[string]any), "avg") }},
		{"null avg", func(r map[string]any) { r["stats"].(map[string]any)["avg"] = nil }},
		{"negative avg", func(r map[string]any) { r["stats"].(map[string]any)["avg"] = -1 }},
		{"string avg", func(r map[string]any) { r["stats"].(map[string]any)["avg"] = "8.5" }},
		{"string NaN", func(r map[string]any) { r["stats"].(map[string]any)["avg"] = "NaN" }},
		{"bad packet counts", func(r map[string]any) { r["stats"].(map[string]any)["drop"] = 2 }},
		{"bad loss", func(r map[string]any) { r["stats"].(map[string]any)["loss"] = 101 }},
		{"missing timings", func(r map[string]any) { delete(r, "timings") }},
		{"negative RTT", func(r map[string]any) { r["timings"].([]any)[0].(map[string]any)["rtt"] = -1 }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := ping()
			tt.change(r)
			c, _, _ := fixture(t, config("ping"), envelope("ping", "example.com", r), nil)
			got := gp.Check(context.Background(), c)
			if got.State != gp.Pending || got.LatencyAvailable {
				t.Fatalf("%+v", got)
			}
		})
	}
	valid, _ := json.Marshal(envelope("ping", "example.com", ping()))
	for name, raw := range map[string][]byte{
		"trailing":               append(append([]byte{}, valid...), []byte(` {}`)...),
		"truncated":              valid[:len(valid)-3],
		"duplicate ID":           []byte(strings.Replace(string(valid), `"id":"fixture_ID-1"`, `"id":"other","id":"fixture_ID-1"`, 1)),
		"duplicate escaped ID":   []byte(strings.Replace(string(valid), `"id":"fixture_ID-1"`, `"\u0069d":"other","id":"fixture_ID-1"`, 1)),
		"duplicate status case":  []byte(strings.Replace(string(valid), `"status":"finished"`, `"Status":"failed","status":"finished"`, 1)),
		"duplicate nested stats": []byte(strings.Replace(string(valid), `"avg":8.5`, `"avg":0,"avg":8.5`, 1)),
		"wrong case critical":    []byte(strings.Replace(string(valid), `"id":`, `"ID":`, 1)),
		"not UTF8":               append(append([]byte{}, valid...), 0xff),
		"unpaired surrogate":     []byte(strings.Replace(string(valid), targetSecret, `\ud800`, 1)),
		"infinite number":        []byte(strings.Replace(string(valid), `"avg":8.5`, `"avg":1e999`, 1)),
		"bare NaN":               []byte(strings.Replace(string(valid), `"avg":8.5`, `"avg":NaN`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			c, _, _ := fixture(t, config("ping"), raw, nil)
			if r := gp.Check(context.Background(), c); r.State != gp.Pending {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestCreationAndPollRejection(t *testing.T) {
	for _, phase := range []string{"create", "poll"} {
		for _, status := range []int{200, 201, 202, 204, 301, 302, 307, 308, 400, 401, 403, 404, 422, 429, 500, 503} {
			if phase == "create" && status == 202 || phase == "poll" && status == 200 {
				continue
			}
			t.Run(phase+"/"+http.StatusText(status), func(t *testing.T) {
				var posts, gets, sinkHits atomic.Int32
				sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					sinkHits.Add(1)
					t.Error("redirect followed with credentials")
				}))
				defer sink.Close()
				s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = io.Copy(io.Discard, r.Body)
					if r.Method == "POST" {
						posts.Add(1)
						if phase == "poll" {
							sendJSON(w, 202, map[string]any{"id": "fixture_ID-1", "probesCount": 1})
							return
						}
					} else {
						gets.Add(1)
					}
					w.Header().Set("Location", sink.URL)
					sendJSON(w, status, map[string]any{"error": map[string]any{"message": apiSecret + targetSecret}})
				}))
				defer s.Close()
				c := config("ping")
				c.Options.APIURL, c.Options.AllowInsecureAPI = s.URL, true
				for _, down := range []bool{false, true} {
					c.Options.APIFailureIsDown = down
					got := gp.Check(context.Background(), c)
					assertSafe(t, got)
					want := gp.Pending
					if down {
						want = gp.Down
					}
					if got.State != want {
						t.Fatalf("%+v", got)
					}
				}
				if posts.Load() != 2 || gets.Load() > 2 || sinkHits.Load() != 0 {
					t.Fatal("unexpected retry, poll, or redirect")
				}
			})
		}
	}
	for name, raw := range map[string]string{"empty": `{}`, "blank ID": `{"id":"","probesCount":1}`, "path ID": `{"id":"../secret","probesCount":1}`, "URL ID": `{"id":"https://example.com","probesCount":1}`, "extra probes": `{"id":"fixture_ID-1","probesCount":2}`, "duplicate": `{"id":"bad","id":"fixture_ID-1","probesCount":1}`, "trailing": `{"id":"fixture_ID-1","probesCount":1}null`, "junk": "secret", "long ID": `{"id":"` + strings.Repeat("x", 129) + `","probesCount":1}`} {
		t.Run(name, func(t *testing.T) {
			var hits atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(202)
				_, _ = io.WriteString(w, raw)
			}))
			defer s.Close()
			c := config("ping")
			c.Options.APIURL, c.Options.AllowInsecureAPI = s.URL, true
			if got := gp.Check(context.Background(), c); got.State != gp.Pending || hits.Load() != 1 {
				t.Fatalf("%+v requests=%d", got, hits.Load())
			}
		})
	}
}

func TestWireBodyAndHeaderBounds(t *testing.T) {
	for _, kind := range []string{"content length", "streamed oversized", "truncated wire", "headers", "non JSON type", "gzip"} {
		t.Run(kind, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/json")
				switch kind {
				case "content length":
					w.Header().Set("Content-Length", "1048577")
				case "headers":
					w.Header().Set("X-Large", strings.Repeat("x", 40<<10))
				case "non JSON type":
					w.Header().Set("Content-Type", "text/plain")
				case "gzip":
					w.Header().Set("Content-Encoding", "gzip")
				case "truncated wire":
					w.Header().Set("Content-Length", "1000")
				}
				w.WriteHeader(202)
				if kind == "streamed oversized" {
					w.(http.Flusher).Flush()
					_, _ = io.WriteString(w, strings.Repeat("x", 1048577))
					return
				}
				_, _ = io.WriteString(w, `{"id":"fixture_ID-1","probesCount":1}`)
			}))
			defer s.Close()
			c := config("ping")
			c.Options.APIURL, c.Options.AllowInsecureAPI = s.URL, true
			if got := gp.Check(context.Background(), c); got.State != gp.Pending {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestUnsolicited304AndStalePoll(t *testing.T) {
	for _, kind := range []string{"304 without cache", "changed timestamp", "cached never completes"} {
		t.Run(kind, func(t *testing.T) {
			var gets atomic.Int32
			m := envelope("ping", "example.com", map[string]any{"status": "in-progress"})
			m["status"] = "in-progress"
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					sendJSON(w, 202, map[string]any{"id": "fixture_ID-1", "probesCount": 1})
					return
				}
				n := gets.Add(1)
				if kind == "304 without cache" || kind == "cached never completes" && n > 1 {
					w.WriteHeader(304)
					return
				}
				w.Header().Set("ETag", `"progress"`)
				if n > 1 {
					m["createdAt"] = time.Now().UTC().Format(time.RFC3339Nano)
					m["status"] = "finished"
					m["results"].([]any)[0].(map[string]any)["result"] = ping()
				}
				sendJSON(w, 200, m)
			}))
			defer s.Close()
			c := config("ping")
			c.Timeout = 800 * time.Millisecond
			c.Options.APIURL, c.Options.AllowInsecureAPI = s.URL, true
			got := gp.Check(context.Background(), c)
			if got.State != gp.Pending {
				t.Fatalf("%+v", got)
			}
		})
	}
}
