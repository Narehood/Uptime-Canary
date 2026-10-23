package notifyhttp

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Engine struct {
	client    *http.Client
	transport *http.Transport
	timeout   time.Duration
}

var defaultEngine, _ = New(Options{})

// New owns its transport and snapshots additive CA trust. No ambient proxy,
// cookies, credentials, redirect, compression or HTTP/2 replay behavior applies.
// Timeout covers DNS, TLS, request, headers and the entire bounded response read.
func New(opts Options) (*Engine, error) {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	if timeout < 0 || timeout > MaxTimeout || len(opts.AdditionalRootCAPEM) > 256<<10 {
		return nil, errOptions
	}
	var roots *x509.CertPool
	if len(opts.AdditionalRootCAPEM) != 0 {
		var err error
		roots, err = x509.SystemCertPool()
		if err != nil {
			return nil, errOptions
		}
		remaining := opts.AdditionalRootCAPEM
		count := 0
		for len(strings.TrimSpace(string(remaining))) != 0 {
			remaining = bytes.TrimSpace(remaining)
			if !bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----")) {
				return nil, errOptions
			}
			// pem.Decode searches past malformed material. Isolate exactly one
			// block first so a later valid certificate cannot conceal it.
			endMarker := []byte("-----END CERTIFICATE-----")
			end := bytes.Index(remaining, endMarker)
			if end < 0 {
				return nil, errOptions
			}
			end += len(endMarker)
			encoded := remaining[:end]
			if bytes.Count(encoded, []byte("-----BEGIN CERTIFICATE-----")) != 1 {
				return nil, errOptions
			}
			block, rest := pem.Decode(encoded)
			if block == nil || len(bytes.TrimSpace(rest)) != 0 || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || count >= 32 {
				return nil, errOptions
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil || !cert.IsCA || !cert.BasicConstraintsValid {
				return nil, errOptions
			}
			roots.AddCert(cert)
			remaining = remaining[end:]
			count++
		}
		if count == 0 {
			return nil, errOptions
		}
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
		TLSHandshakeTimeout:    timeout,
		ResponseHeaderTimeout:  timeout,
		MaxResponseHeaderBytes: MaxResponseHeaderBytes,
		DisableCompression:     true,
		// Fresh HTTP/1 connections avoid net/http's stale pooled-connection retry.
		DisableKeepAlives: true,
		TLSNextProto:      map[string]func(string, *tls.Conn) http.RoundTripper{},
		MaxConnsPerHost:   2,
	}
	client := &http.Client{
		Transport:     transport,
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &Engine{client: client, transport: transport, timeout: timeout}, nil
}

func (e *Engine) CloseIdleConnections() {
	if e != nil && e.transport != nil {
		e.transport.CloseIdleConnections()
	}
}

// Send performs exactly one attempt. Root owns retry scheduling and concurrency
// across engine instances. A nil/zero Engine uses the bounded default engine.
func (e *Engine) Send(ctx context.Context, c Config, m Message) Result {
	if e == nil || e.client == nil {
		e = defaultEngine
	}
	if Validate(c) != nil {
		return failure(c.Kind, Rejected, "invalid_config")
	}
	if validateMessage(c, m) != nil {
		return failure(c.Kind, Rejected, "invalid_message")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	if ctx.Err() != nil {
		return failure(c.Kind, Unknown, "canceled")
	}
	req, err := request(ctx, c, m)
	if err != nil {
		return failure(c.Kind, Rejected, "invalid_message")
	}
	response, err := e.client.Do(req)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		code := "transport"
		if ctx.Err() != nil {
			code = "canceled"
		}
		return failure(c.Kind, Unknown, code)
	}
	defer response.Body.Close()
	result := classifyHTTP(c.Kind, response.StatusCode)
	result.RetryAfter = retryHints(c.Kind, response.Header, nil, time.Now())
	if !boundedHeaders(response.Header) {
		return responseFailure(result, "response_headers")
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if readErr != nil {
		code := "response"
		if ctx.Err() != nil {
			code = "canceled"
		}
		return responseFailure(result, code)
	}
	if len(body) > MaxResponseBytes {
		return responseFailure(result, "response_too_large")
	}
	if !boundedHeaders(response.Header, response.Trailer) {
		return responseFailure(result, "response_headers")
	}
	if ctx.Err() != nil {
		return responseFailure(result, "canceled")
	}
	if c.Kind == Slack {
		if result.HTTPStatus == http.StatusOK && string(body) == "ok" {
			return accepted(result, "")
		}
		return result // Non-exact acknowledgements remain unknown on HTTP 200.
	}
	object, parseErr := strictObject(body)
	if parseErr == nil {
		result.RetryAfter = retryHints(c.Kind, response.Header, object, time.Now())
	}
	if result.HTTPStatus != http.StatusOK || parseErr != nil {
		return result
	}
	return acknowledgement(c, result, object)
}

func boundedHeaders(headers ...http.Header) bool {
	count, size := 0, 0
	for _, h := range headers {
		for key, values := range h {
			for _, value := range values {
				count++
				size += len(key) + len(value) + 4
			}
		}
	}
	return count <= MaxResponseHeaders && size <= MaxResponseHeaderBytes
}

func classifyHTTP(kind Kind, status int) Result {
	var result Result
	switch {
	case status == 429:
		result = failure(kind, Retryable, "rate_limited")
	case status >= 500 && status <= 599:
		result = failure(kind, Retryable, "unavailable")
	case status >= 300 && status <= 399:
		result = failure(kind, Rejected, "redirect_refused")
	case status >= 400 && status <= 499:
		result = failure(kind, Rejected, "provider_rejected")
	default:
		result = failure(kind, Unknown, "response")
	}
	result.HTTPStatus = status
	return result
}

// A known HTTP error stays authoritative even if its error body is unreadable.
func responseFailure(result Result, code string) Result {
	if result.Status != Unknown {
		return result
	}
	next := failure(result.Kind, Unknown, code)
	next.HTTPStatus, next.RetryAfter = result.HTTPStatus, result.RetryAfter
	return next
}

func accepted(result Result, rawID string) Result {
	result.Status, result.Accepted = Accepted, true
	result.ErrorCode, result.ErrorMessage, result.RetryAfter = "", "", 0
	if rawID != "" {
		result.ProviderID = digest(rawID)
	}
	return result
}

func failure(kind Kind, status Status, code string) Result {
	switch kind {
	case Slack, Telegram, Ntfy, Gotify, Pushover:
	default:
		kind = ""
	}
	messages := map[string]string{
		"invalid_config":     "Notification configuration is invalid",
		"invalid_message":    "Notification message is invalid",
		"canceled":           "Notification attempt was canceled or timed out; acceptance is unknown",
		"transport":          "Provider connection failed; acceptance is unknown",
		"response":           "Provider acknowledgement is invalid; acceptance is unknown",
		"response_too_large": "Provider response exceeds the size limit; acceptance is unknown",
		"response_headers":   "Provider response headers exceed limits; acceptance is unknown",
		"rate_limited":       "Provider rate limit reached",
		"unavailable":        "Provider is temporarily unavailable",
		"redirect_refused":   "Provider redirect was refused",
		"provider_rejected":  "Provider rejected the request",
	}
	return Result{Kind: kind, Status: status, ErrorCode: code, ErrorMessage: messages[code]}
}

func retryHints(kind Kind, headers http.Header, object map[string]any, now time.Time) time.Duration {
	var delay time.Duration
	for _, raw := range headers.Values("Retry-After") {
		if len(raw) > 128 {
			continue
		}
		value := strings.TrimSpace(raw)
		if seconds, ok := retrySeconds(value); ok {
			delay = max(delay, seconds)
		} else if date, err := http.ParseTime(value); err == nil {
			delay = max(delay, min(max(date.Sub(now), 0), MaxRetryAfter))
		}
	}
	if kind == Telegram {
		if seconds, ok := integerField(objectField(object, "parameters"), "retry_after"); ok && seconds > 0 {
			delay = max(delay, boundedSeconds(uint64(seconds)))
		}
	}
	if kind == Pushover {
		if reset, err := strconv.ParseInt(headers.Get("X-Limit-App-Reset"), 10, 64); err == nil && reset > 0 && headers.Get("X-Limit-App-Remaining") == "0" {
			delay = max(delay, min(max(time.Unix(reset, 0).Sub(now), 0), MaxRetryAfter))
		}
	}
	return delay
}

// Saturation avoids turning a very large positive decimal Retry-After into no
// delay merely because it overflows an integer. Non-decimal hints are ignored.
func retrySeconds(value string) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	seconds := uint64(0)
	for _, b := range []byte(value) {
		if b < '0' || b > '9' {
			return 0, false
		}
		seconds = min(seconds*10+uint64(b-'0'), uint64(MaxRetryAfter/time.Second))
	}
	return boundedSeconds(seconds), true
}

func boundedSeconds(seconds uint64) time.Duration {
	if seconds >= uint64(MaxRetryAfter/time.Second) {
		return MaxRetryAfter
	}
	return time.Duration(seconds) * time.Second
}
