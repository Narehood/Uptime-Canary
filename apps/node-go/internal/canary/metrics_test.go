package canary

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"uptime-canary/apps/node-go/internal/checks"
)

func TestMetricsAuthenticationAndUnknownStateDoNotExposeSecrets(t *testing.T) {
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	n := maintenanceNode(t, &clock, ":memory:")
	in := input([]string{"a"}, "https://private.example.test", "https://private-hook.invalid/secret-token")
	m, err := n.writeMonitor(in, nil, "metric-service", false)
	if err != nil {
		t.Fatal(err)
	}
	n.accounts["metrics-viewer"] = account{AccountView: AccountView{ID: "metrics-viewer", Name: "Metric reader", Role: "viewer"}, Hash: digest(viewerToken)}
	server := httptest.NewServer(n)
	defer server.Close()
	get := func(token string) (int, http.Header, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", server.URL+"/api/metrics", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, res.Header, string(b)
	}
	for _, token := range []string{"", clusterToken} {
		if code, _, _ := get(token); code != 401 {
			t.Fatal("metrics accepted absent or peer credential", code)
		}
	}
	code, headers, body := get(viewerToken)
	if code != 200 || headers.Get("Content-Type") != "text/plain; version=0.0.4; charset=utf-8" || headers.Get("Cache-Control") != "no-store" {
		t.Fatal("invalid authenticated exposition", code, headers)
	}
	if !strings.Contains(body, `monitor_id="metric-service",state="unknown"} 1`) || strings.Contains(body, "observation_latency_milliseconds{") {
		t.Fatal("missing observation looked healthy", body)
	}
	for _, secret := range []string{in.Target, in.WebhookURL, adminToken, viewerToken, clusterToken, "secret-token"} {
		if strings.Contains(body, secret) {
			t.Fatal("metrics disclosed sensitive configuration")
		}
	}
	n.mu.Lock()
	n.samples[m.ID+"/a"] = Sample{Observation: Observation{MonitorID: m.ID, NodeID: "a", State: "pending", CheckedAt: clock.Load(), LatencyMs: 42, Certificate: &checks.CertificateSummary{DaysRemaining: 3, Verified: false}}, Sequence: 1, Revision: m.Revision}
	n.mu.Unlock()
	_, _, body = get(viewerToken)
	if !strings.Contains(body, `state="pending"} 1`) || !strings.Contains(body, "} 42\n") || !strings.Contains(body, `verified="false"} 3`) {
		t.Fatal("fresh pending or certificate measurement lost", body)
	}
	clock.Add(10000)
	_, _, body = get(viewerToken)
	if !strings.Contains(body, `state="unknown"} 1`) || strings.Contains(body, "observation_latency_milliseconds{") || strings.Contains(body, "certificate_days_remaining{") {
		t.Fatal("stale latency/certificate remained current", body)
	}
}
