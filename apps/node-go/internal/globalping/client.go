package globalping

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"time"
)

const (
	maxResponseBytes = 1 << 20
	maxTotalBytes    = 8 << 20
	maxPolls         = 120
	pollDelay        = 500 * time.Millisecond
	clockSkew        = 30 * time.Second
)

type apiClient struct {
	client    *http.Client
	remaining int
	polls     int
}

// Check uses the earlier of the mandatory Timeout and ctx's deadline for all
// validation, API I/O, polling, and guarded JSONata admission/execution.
// It never retries creation, follows redirects, or measures the target locally.
func Check(ctx context.Context, c Config) Result {
	start := time.Now()
	if c.Timeout <= 0 || c.Timeout > 5*time.Minute {
		return Result{State: Pending, Message: "invalid Globalping configuration"}
	}
	ctx, cancel := context.WithDeadline(ctx, start.Add(c.Timeout))
	defer cancel()
	if ctx.Err() != nil {
		return providerFailure(c.Options, "Globalping check interrupted")
	}
	p, err := prepare(c)
	if err != nil {
		return Result{State: Pending, Message: "invalid Globalping configuration"}
	}
	if ctx.Err() != nil {
		return providerFailure(c.Options, "Globalping check interrupted")
	}
	client, transport, err := newAPIClient(p)
	if err != nil {
		return providerFailure(c.Options, "Globalping API trust unavailable")
	}
	defer transport.CloseIdleConnections()
	result := runCheck(ctx, p, client, start)
	if ctx.Err() != nil {
		// Unavailable query workers remain Pending even with provider-down policy.
		if result.Message == "Globalping HTTP JSONata worker unavailable" {
			return result
		}
		return providerFailure(c.Options, "Globalping check interrupted")
	}
	return result
}

func newAPIClient(p *prepared) (*apiClient, *http.Transport, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if len(p.certs) > 0 {
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			return nil, nil, errResponse
		}
		for _, cert := range p.certs {
			roots.AddCert(cert)
		}
		tlsConfig.RootCAs = roots
	}
	transport := &http.Transport{
		Proxy: nil, DialContext: (&net.Dialer{Timeout: p.config.Timeout}).DialContext,
		TLSClientConfig: tlsConfig, TLSHandshakeTimeout: p.config.Timeout,
		ResponseHeaderTimeout: p.config.Timeout, MaxResponseHeaderBytes: 32 << 10,
		DisableCompression: true, DisableKeepAlives: true, MaxConnsPerHost: 1,
	}
	return &apiClient{remaining: maxTotalBytes, client: &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, transport, nil
}

func runCheck(ctx context.Context, p *prepared, api *apiClient, start time.Time) Result {
	failure := func(message string) Result { return providerFailure(p.config.Options, message) }
	payload, err := json.Marshal(p.payload)
	if err != nil {
		return failure("Globalping request unavailable")
	}
	status, body, _, err := api.request(ctx, p, http.MethodPost, p.api.String(), payload, "")
	if err != nil || status != http.StatusAccepted {
		return failure("Globalping API creation unavailable")
	}
	created, err := decodeObject(body)
	if err != nil {
		return failure("Globalping API response invalid")
	}
	id, ok := str(created, "id")
	_, countOK := integer(created, "probesCount", 1, 1)
	if !ok || !validID(id) || !countOK {
		return failure("Globalping API response invalid")
	}
	// Never use the server's Location header as a URL or credential destination.
	pollURL := *p.api
	pollURL.Path += "/" + id
	etag := ""
	var cached object
	var createdAt time.Time
	for poll := 0; poll < maxPolls; poll++ {
		status, body, nextETag, err := api.request(ctx, p, http.MethodGet, pollURL.String(), nil, etag)
		if err != nil {
			return failure("Globalping API polling unavailable")
		}
		if status == http.StatusNotModified {
			if cached == nil || etag == "" {
				return failure("Globalping API response invalid")
			}
		} else {
			if status != http.StatusOK {
				return failure("Globalping API polling unavailable")
			}
			m, err := decodeObject(body)
			if err != nil {
				return failure("Globalping API response invalid")
			}
			stamp, valid := envelope(m, p, id, start)
			if !valid || !createdAt.IsZero() && !stamp.Equal(createdAt) {
				return failure("Globalping API response invalid")
			}
			createdAt = stamp
			state, _ := str(m, "status")
			if state == "finished" {
				return evaluate(ctx, p, m)
			}
			cached, etag = m, nextETag
		}
		// Delay starts after reading each in-progress response, including 304.
		// No fixed ticker and no concurrent polls can create bursts.
		if poll+1 < maxPolls && !waitPoll(ctx) {
			return failure("Globalping check interrupted")
		}
	}
	return failure("Globalping polling limit reached")
}

func (a *apiClient) request(ctx context.Context, p *prepared, method, endpoint string, payload []byte, etag string) (int, []byte, string, error) {
	if ctx.Err() != nil || a.remaining <= 0 {
		return 0, nil, "", errResponse
	}
	if method == http.MethodGet {
		if a.polls >= maxPolls {
			return 0, nil, "", errResponse
		}
		a.polls++
	}
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return 0, nil, "", errResponse
	}
	// Explicitly disable GetBody so creation cannot acquire a replay mechanism.
	req.GetBody = nil
	req.Header.Set("Accept", "application/json")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.config.Options.Token != "" {
		req.Header.Set("Authorization", "Bearer "+p.config.Options.Token)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		if resp != nil {
			resp.Body.Close()
		}
		return 0, nil, "", errResponse
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return resp.StatusCode, nil, "", nil
	}
	if method == http.MethodPost && resp.StatusCode != http.StatusAccepted || method == http.MethodGet && resp.StatusCode != http.StatusOK {
		// Do not retain or interpret potentially secret-bearing error bodies.
		return resp.StatusCode, nil, "", nil
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || resp.Header.Get("Content-Encoding") != "" {
		return 0, nil, "", errResponse
	}
	limit := min(maxResponseBytes, a.remaining)
	// Count the oversize-detection byte within the aggregate read budget.
	if limit == a.remaining {
		limit--
	}
	if resp.ContentLength > int64(limit) {
		return 0, nil, "", errResponse
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	a.remaining -= len(b)
	if err != nil || len(b) > limit || ctx.Err() != nil {
		return 0, nil, "", errResponse
	}
	nextETag := ""
	if values := resp.Header.Values("ETag"); len(values) == 1 && validETag(values[0]) {
		nextETag = values[0]
	}
	return resp.StatusCode, b, nextETag, nil
}

func validETag(s string) bool {
	if len(s) > 256 {
		return false
	}
	if len(s) > 2 && s[:2] == "W/" {
		s = s[2:]
	}
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return false
	}
	for _, b := range []byte(s[1 : len(s)-1]) {
		if b < 0x21 || b > 0x7e || b == '"' {
			return false
		}
	}
	return true
}

func waitPoll(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}
	timer := time.NewTimer(pollDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return ctx.Err() == nil
	}
}

func providerFailure(o Options, message string) Result {
	state := Pending
	if o.APIFailureIsDown {
		state = Down
	}
	return Result{State: state, Message: message}
}

func envelope(m object, p *prepared, id string, start time.Time) (time.Time, bool) {
	gotID, _ := str(m, "id")
	typeName, _ := str(m, "type")
	target, _ := str(m, "target")
	status, _ := str(m, "status")
	_, probes := integer(m, "probesCount", 1, 1)
	results, resultsOK := arr(m, "results")
	stamp, stampOK := str(m, "createdAt")
	if gotID != id || typeName != p.payload.Type || target != p.payload.Target || !probes ||
		!resultsOK || len(results) != 1 || !stampOK || len(stamp) > 64 || status != "finished" && status != "in-progress" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil || t.Before(start.Add(-clockSkew)) || t.After(start.Add(clockSkew)) {
		return time.Time{}, false
	}
	entry, ok := results[0].(object)
	if !ok {
		return time.Time{}, false
	}
	r, ok := obj(entry, "result")
	if !ok {
		return time.Time{}, false
	}
	testStatus, _ := str(r, "status")
	switch testStatus {
	case "finished", "failed", "offline", "in-progress":
	default:
		return time.Time{}, false
	}
	if source, exists := r["failureSource"]; exists {
		s, ok := source.(string)
		if !ok || testStatus != "failed" || s != "target" && s != "resolver" && s != "internal" {
			return time.Time{}, false
		}
	}
	return t, true
}
