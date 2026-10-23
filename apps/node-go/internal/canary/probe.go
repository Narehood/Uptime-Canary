package canary

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"uptime-canary/apps/node-go/internal/checks"
	"uptime-canary/apps/node-go/internal/icmp"
)

func newHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConns: 64, MaxIdleConnsPerHost: 4, MaxConnsPerHost: 32, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 120 * time.Second, MaxResponseHeaderBytes: 64 * 1024}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func HTTPProbe(ctx context.Context, m Monitor, client Doer) ProbeResult {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(m.TimeoutMs)*time.Millisecond)
	defer cancel()
	target := m.Target
	result := func(state, msg string) ProbeResult {
		return ProbeResult{State: state, LatencyMs: float64(time.Since(start).Microseconds()) / 1000, Message: msg}
	}
	for redirects := 0; redirects <= 5; redirects++ {
		req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
		if err != nil {
			return result("down", "HTTP request failed")
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return result("down", "HTTP timeout or cancelled")
			}
			return result("down", "HTTP request failed")
		}
		resp.Body.Close()
		location := resp.Header.Get("Location")
		if (resp.StatusCode == 301 || resp.StatusCode == 302 || resp.StatusCode == 303 || resp.StatusCode == 307 || resp.StatusCode == 308) && location != "" {
			if redirects == 5 {
				return result("down", "HTTP redirect limit exceeded")
			}
			base, _ := url.Parse(target)
			next, err := base.Parse(location)
			if err != nil {
				return result("down", "HTTP redirect refused")
			}
			valid, err := httpURL(next.String(), "redirect")
			if err != nil {
				return result("down", "HTTP redirect refused")
			}
			if base.Scheme == "https" && next.Scheme == "http" {
				return result("down", "HTTPS downgrade redirect refused")
			}
			target = valid
			continue
		}
		up := resp.StatusCode >= 200 && resp.StatusCode < 300
		var cert *checks.CertificateSummary
		if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
			c := resp.TLS.PeerCertificates[0]
			cert = &checks.CertificateSummary{NotBefore: c.NotBefore.UTC(), NotAfter: c.NotAfter.UTC(), DaysRemaining: int(math.Floor(time.Until(c.NotAfter).Hours() / 24)), Verified: len(resp.TLS.VerifiedChains) > 0}
		}
		if m.ExpectedStatusCodes != nil {
			up = false
			for _, v := range m.ExpectedStatusCodes {
				if v == resp.StatusCode {
					up = true
				}
			}
		}
		if up {
			r := result("up", fmt.Sprintf("HTTP %d", resp.StatusCode))
			r.StatusCode = resp.StatusCode
			r.Certificate = cert
			return r
		}
		r := result("down", fmt.Sprintf("HTTP %d", resp.StatusCode))
		r.StatusCode = resp.StatusCode
		r.Certificate = cert
		return r
	}
	return result("down", "HTTP request failed")
}
func PingArguments(target string, timeout int64, platform string) ([]string, error) {
	if err := pingTarget(target); err != nil {
		return nil, err
	}
	switch platform {
	case "windows":
		return []string{"-n", "1", "-w", strconv.FormatInt(timeout, 10), target}, nil
	case "darwin":
		return []string{"-n", "-c", "1", "-W", strconv.FormatInt(timeout, 10), target}, nil
	default:
		return []string{"-n", "-c", "1", "-W", strconv.FormatInt(max(1, (timeout+999)/1000), 10), "--", target}, nil
	}
}

func PingProbe(ctx context.Context, m Monitor) ProbeResult {
	r := icmp.Check(ctx, pingConfig(m.MonitorInput))
	return ProbeResult{State: r.State, LatencyMs: r.LatencyMs, LatencyAvailable: Ptr(r.LatencyAvailable), Message: r.Message}
}
