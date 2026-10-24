package canary

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDashboardRegistryPushCapabilityRoutesOnlyToSelectedNode(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	n := dashboardRemoteFixture(t, "node-a")
	in := pushInput([]string{n.ID})
	if _, err := n.writeMonitor(in, nil, "push-post", false); err != nil {
		t.Fatal(err)
	}
	in.Push = &PushMonitorOptions{Token: pushRotatedSecret, AllowGET: true}
	if _, err := n.writeMonitor(in, nil, "push-get", false); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/push" || strings.HasPrefix(r.URL.Path, "/api/push/") {
			calls.Add(1)
			if r.Header.Get("Cookie") != "" || r.Header.Get("X-Canary-Registered-Node") != "" || r.URL.Query().Has("registration") {
				t.Error("dashboard routing or proxy credentials reached node push admission")
			}
			if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				t.Error("dashboard bearer reached push receiver")
			}
		}
		n.ServeHTTP(w, r)
	}))
	defer server.Close()
	row := enrollDashboardNode(t, d, owner, server.URL)
	send := func(method, path, authorization, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if authorization != "" {
			r.Header.Set("Authorization", authorization)
		}
		r.Header.Set("Cookie", "proxy-session=must-not-reach-node")
		w := httptest.NewRecorder()
		d.ServeHTTP(w, r)
		return w
	}
	w := send("POST", "/api/push?registration="+row.ID, "Push "+pushFixtureSecret, `{"status":"up","msg":"private fixture"}`)
	if w.Code != 200 || len(n.pushEvents) != 1 {
		t.Fatalf("registered POST heartbeat failed: %d %s", w.Code, w.Body.String())
	}
	w = send("GET", "/api/push/"+pushRotatedSecret+"?registration="+row.ID+"&status=up", "", "")
	if w.Code != 200 || len(n.pushEvents) != 2 {
		t.Fatalf("registered GET heartbeat failed: %d %s", w.Code, w.Body.String())
	}
	// GET capabilities intentionally reject even an empty Authorization header.
	// Generated curl GET requests omit it rather than setting an empty value.
	emptyHeader := httptest.NewRequest("GET", "/api/push/"+pushRotatedSecret+"?registration="+row.ID+"&status=up", nil)
	emptyHeader.Header.Set("Authorization", "")
	emptyReply := httptest.NewRecorder()
	d.ServeHTTP(emptyReply, emptyHeader)
	if emptyReply.Code != 404 {
		t.Fatal("empty GET authorization header was admitted")
	}
	before := calls.Load()
	for _, path := range []string{"/api/push", "/api/push?registration=" + row.ID + "&registration=" + row.ID, "/api/push?registration=unknown"} {
		if w = send("POST", path, "Push "+pushFixtureSecret, `{"status":"up"}`); w.Code != 404 {
			t.Fatalf("ambiguous heartbeat destination accepted: %d", w.Code)
		}
	}
	if w = send("POST", "/api/push?registration="+row.ID, "Bearer "+owner, `{"status":"up"}`); w.Code != 400 {
		t.Fatal("dashboard bearer used as a push credential")
	}
	if calls.Load() != before || len(n.pushEvents) != 2 {
		t.Fatal("invalid routing reached a node or changed heartbeats")
	}
	if w = send("POST", "/api/push?registration="+row.ID, "Push wrong-private-fixture-token-0123456789", `{"status":"up"}`); w.Code != 404 {
		t.Fatalf("unknown push credential was accepted: %d", w.Code)
	}
}
