package checks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"uptime-canary/apps/node-go/internal/jsonquery"
)

func TestHTTPJSONataUsesWholeResponseAndGuardedWorker(t *testing.T) {
	for _, tc := range []struct {
		name, body, expression, expected, state string
		operator                                jsonquery.Operator
	}{
		{"aggregate", `{"items":[{"value":2},{"value":5}]}`, `$sum(items.value)`, `6`, Up, jsonquery.Greater},
		{"predicate", `{"items":[{"value":2},{"value":5}]}`, `$count(items[value > 3])`, `1`, Up, jsonquery.Equal},
		{"text", "service ready now", `$`, `"ready"`, Up, jsonquery.Contains},
		{"false condition", `{"state":"failed"}`, `state`, `"ready"`, Down, jsonquery.Equal},
		{"null is not inequality success", `null`, `$`, `"ready"`, Down, jsonquery.NotEqual},
		{"non scalar", `{"items":[1,2]}`, `items`, `2`, Down, jsonquery.Equal},
		{"syntax error", `{"private":"private-response-secret"}`, `($private-query-secret`, `true`, Down, jsonquery.Equal},
		{"invalid utf8", string([]byte{0xff}), `$`, `true`, Down, jsonquery.Equal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) }))
			defer server.Close()
			c := Config{Type: HTTP, Target: server.URL, Timeout: 4 * time.Second, HTTP: &HTTPConfig{JSONata: &jsonquery.Config{Expression: tc.expression, Operator: tc.operator, Expected: json.RawMessage(tc.expected)}}}
			if err := Validate(c); err != nil {
				t.Fatal("shape validation unexpectedly compiled the expression", err)
			}
			r := Check(context.Background(), c)
			if r.State != tc.state || strings.Contains(r.Message, "private-response-secret") || strings.Contains(r.Message, "private-query-secret") {
				t.Fatal("wrong or secret-bearing query result", r)
			}
		})
	}
}

func TestHTTPJSONataPreservesStatusAndOtherMatchers(t *testing.T) {
	var status atomic.Int64
	status.Store(503)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(status.Load()))
		fmt.Fprint(w, `{"ready":false,"count":5}`)
	}))
	defer server.Close()
	c := Config{Type: HTTP, Target: server.URL, Timeout: 4 * time.Second, HTTP: &HTTPConfig{JSONata: &jsonquery.Config{Expression: "count", Operator: jsonquery.Greater, Expected: []byte(`2`)}}}
	if r := Check(context.Background(), c); r.State != Down || r.StatusCode != 503 || !strings.Contains(r.Message, "status 503") {
		t.Fatal("query bypassed accepted status policy", r)
	}
	status.Store(200)
	c.HTTP.JSON = &JSONMatcher{Path: "$.ready", Expected: "true"}
	if r := Check(context.Background(), c); r.State != Down || r.Message != "HTTP JSON condition not met" {
		t.Fatal("new query bypassed existing selector", r)
	}
	c.HTTP.Method = "HEAD"
	if Validate(c) == nil {
		t.Fatal("HEAD accepted a body-dependent query")
	}
	c.HTTP.Method, c.HTTP.JSON = "GET", nil
	c.HTTP.JSONata.Expected = []byte(`null`)
	if Validate(c) == nil {
		t.Fatal("query accepted unsupported null expectation")
	}
}
