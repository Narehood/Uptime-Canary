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
)

// Creation may consume a provider credit even if its response is lost. A fresh
// TCP connection must never be used to replay that ambiguous POST.
func TestGlobalpingReviewAmbiguousCreationNeverReplayed(t *testing.T) {
	for _, down := range []bool{false, true} {
		t.Run(map[bool]string{false: "default pending", true: "explicit down"}[down], func(t *testing.T) {
			var posts, gets atomic.Int64
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" {
					gets.Add(1)
					t.Error("poll after ambiguous creation")
					return
				}
				posts.Add(1)
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					t.Error(err)
				}
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
			}))
			defer s.Close()
			c := config("ping")
			c.Options.APIURL = s.URL
			c.Options.AllowInsecureAPI = true
			c.Options.APIFailureIsDown = down
			got := gp.Check(context.Background(), c)
			want := gp.Pending
			if down {
				want = gp.Down
			}
			if got.State != want || posts.Load() != 1 || gets.Load() != 0 || got.LatencyAvailable {
				t.Fatalf("ambiguous creation replayed or invented evidence: %+v posts=%d gets=%d", got, posts.Load(), gets.Load())
			}
			assertSafe(t, got)
		})
	}
}

func TestGlobalpingReviewChangedTaskAfterValidatedETag(t *testing.T) {
	var posts, gets atomic.Int64
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			sendJSON(w, 202, map[string]any{"id": "fixture_ID-1", "probesCount": 1})
			return
		}
		poll := gets.Add(1)
		m := envelope("ping", "example.com", ping())
		m["createdAt"] = stamp
		if poll == 1 {
			if r.Header.Get("If-None-Match") != "" {
				t.Error("unsolicited conditional GET")
			}
			m["status"] = "in-progress"
			m["results"] = []any{map[string]any{"result": map[string]any{"status": "in-progress"}}}
			w.Header().Set("ETag", `"review-progress"`)
		} else {
			if r.Header.Get("If-None-Match") != `"review-progress"` {
				t.Error("validated in-progress ETag not retained")
			}
			// A successful-looking result for another target must invalidate the task.
			m["target"] = "other.example"
		}
		sendJSON(w, 200, m)
	}))
	defer s.Close()
	c := config("ping")
	c.Options.APIURL = s.URL
	c.Options.AllowInsecureAPI = true
	got := gp.Check(context.Background(), c)
	if got.State != gp.Pending || got.LatencyAvailable || posts.Load() != 1 || gets.Load() != 2 {
		t.Fatalf("wrong task became target evidence: %+v", got)
	}
	assertSafe(t, got)
}

func TestGlobalpingReviewHTTPLatencyNeverIncludesAPIDelay(t *testing.T) {
	c := config("http")
	c.Options.HTTP.Method = "HEAD"
	c.Options.HTTP.Headers = map[string]string{"Authorization": "Bearer " + targetSecret}
	r := httpResult()
	r["rawBody"] = nil
	r["timings"] = map[string]any{"total": 0, "dns": nil, "tls": 0}
	c, posts, gets := fixture(t, c, envelope("http", "example.com", r), func(req *http.Request, p map[string]any) {
		// The actual remote operation is still encoded in JSON, never local target I/O.
		b, _ := json.Marshal(p)
		if !strings.Contains(string(b), `"method":"HEAD"`) || !strings.Contains(string(b), targetSecret) {
			t.Error("remote HEAD/auth options missing")
		}
		time.Sleep(80 * time.Millisecond)
	})
	start := time.Now()
	got := gp.Check(context.Background(), c)
	if time.Since(start) < 80*time.Millisecond {
		t.Fatal("fixture API delay not exercised")
	}
	if got.State != gp.Up || !got.LatencyAvailable || got.LatencyMs != 0 || posts.Load() != 1 || gets.Load() != 1 {
		t.Fatalf("reported zero replaced by API time or HEAD null body rejected: %+v", got)
	}
	assertSafe(t, got)
}
