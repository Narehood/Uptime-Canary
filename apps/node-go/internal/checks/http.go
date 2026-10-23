package checks

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"uptime-canary/apps/node-go/internal/jsonquery"
)

func makeTransport(tlsOptions TLSConfig, proxy string, env bool) (*http.Transport, error) {
	tc, err := makeTLSConfig(tlsOptions)
	if err != nil {
		return nil, err
	}
	t := &http.Transport{
		DialContext:            (&net.Dialer{}).DialContext,
		TLSClientConfig:        tc,
		MaxResponseHeaderBytes: MaxResponseHeaderBytes,
		DisableKeepAlives:      true,
		// Upgrade checks require HTTP/1.1; using one protocol also keeps the
		// response/header resource bounds consistent between HTTP and WS.
		TLSNextProto:    map[string]func(string, *tls.Conn) http.RoundTripper{},
		MaxConnsPerHost: 1,
	}
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil {
			return nil, invalid("proxy URL is invalid")
		}
		t.Proxy = http.ProxyURL(u)
	} else if env {
		t.Proxy = http.ProxyFromEnvironment
	}
	return t, nil
}

func applyHeaders(req *http.Request, headers map[string]string, basic *BasicAuth, bearer string) {
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if basic != nil {
		req.SetBasicAuth(basic.Username, basic.Password)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
}

func sameOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if u.Port() != "" {
			return u.Port()
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return a.Scheme == b.Scheme && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

func isRedirect(code int) bool {
	return code == 301 || code == 302 || code == 303 || code == 307 || code == 308
}

func checkHTTP(ctx context.Context, c Config) Result {
	o := httpOptions(c)
	if o.OAuth2 != nil {
		if err := validateOAuth2(*o.OAuth2, c.Target); err != nil {
			return down("Invalid OAuth2 transport configuration")
		}
		token, err := oauth2Token(ctx, o)
		if err != nil {
			return down("OAuth2 token request failed")
		}
		o.BearerToken = token
	}
	method := o.Method
	if method == "" {
		method = "GET"
	}
	body := o.Body
	limit := 5
	if o.MaxRedirects != nil {
		limit = *o.MaxRedirects
	}
	current, _ := parseTargetURL(c.Target, false)
	credentials := true
	tlsOpts := tlsOptions(c)
	for redirects := 0; ; redirects++ {
		transport, err := makeTransport(tlsOpts, o.ProxyURL, o.UseEnvironmentProxy)
		if err != nil {
			return down("HTTP transport configuration failed")
		}
		req, err := http.NewRequestWithContext(ctx, method, current.String(), strings.NewReader(body))
		if err != nil {
			transport.CloseIdleConnections()
			return down("HTTP request could not be constructed")
		}
		if credentials {
			applyHeaders(req, o.Headers, o.BasicAuth, o.BearerToken)
		}
		client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			transport.CloseIdleConnections()
			return down("HTTP request or TLS verification failed")
		}
		r := Result{State: Down, StatusCode: resp.StatusCode}
		if resp.TLS != nil {
			r.Certificate = certificateSummary(*resp.TLS, tlsOpts)
		}
		if isRedirect(resp.StatusCode) && resp.Header.Get("Location") != "" && limit != 0 {
			resp.Body.Close()
			transport.CloseIdleConnections()
			if redirects >= limit {
				r.Message = "HTTP redirect limit exceeded"
				return r
			}
			location := resp.Header.Get("Location")
			if len(location) > 8192 {
				r.Message = "HTTP redirect URL exceeds size limit"
				return r
			}
			next, err := current.Parse(location)
			if err != nil {
				r.Message = "HTTP redirect URL is invalid"
				return r
			}
			next, err = parseTargetURL(next.String(), false)
			if err != nil {
				r.Message = "HTTP redirect URL refused"
				return r
			}
			if current.Scheme == "https" && next.Scheme != "https" {
				r.Message = "HTTPS downgrade redirect refused"
				return r
			}
			if resp.StatusCode == 303 && method != "HEAD" || (resp.StatusCode == 301 || resp.StatusCode == 302) && method == "POST" {
				method = "GET"
				body = ""
			}
			if !sameOrigin(current, next) {
				if body != "" {
					r.Message = "HTTP cross-origin request body replay refused"
					return r
				}
				credentials = false
				// Client certificates are credentials too. ServerName overrides
				// apply only to the original origin.
				tlsOpts.ClientCertPEM = ""
				tlsOpts.ClientKeyPEM = ""
				tlsOpts.ServerName = ""
			}
			current = next
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
		resp.Body.Close()
		transport.CloseIdleConnections()
		if err != nil {
			r.Message = "HTTP response body read failed"
			return r
		}
		if len(data) > MaxBodyBytes {
			r.Message = "HTTP response body exceeds one MiB"
			return r
		}
		if !acceptedStatus(resp.StatusCode, o.StatusRanges) {
			r.Message = fmt.Sprintf("HTTP status %d is outside accepted ranges", resp.StatusCode)
			return r
		}
		if o.Keyword != nil {
			matched := strings.Contains(string(data), o.Keyword.Value)
			if o.Keyword.Regex {
				re, _ := regexp.Compile(o.Keyword.Value)
				matched = re.Match(data)
			}
			if o.Keyword.Negate {
				matched = !matched
			}
			if !matched {
				r.Message = "HTTP keyword condition not met"
				return r
			}
		}
		if o.JSON != nil {
			matched, err := matchesJSON(data, *o.JSON)
			if err != nil {
				r.Message = "HTTP response is not valid bounded JSON"
				return r
			}
			if !matched {
				r.Message = "HTTP JSON condition not met"
				return r
			}
		}
		if o.JSONata != nil {
			document := json.RawMessage(data)
			if !json.Valid(document) {
				if !utf8.Valid(data) {
					r.Message = "HTTP JSONata response is not valid UTF-8"
					return r
				}
				document, _ = json.Marshal(string(data))
			}
			query := jsonquery.Check(ctx, jsonquery.Request{Type: jsonquery.Type, Timeout: c.Timeout, Query: *o.JSONata, Document: document})
			if query.State != Up {
				r.State = query.State
				r.Message = "HTTP JSONata evaluation failed or condition not met"
				if query.State == Pending {
					r.Message = "HTTP JSONata worker unavailable"
				}
				return r
			}
		}
		r.State = Up
		r.Message = fmt.Sprintf("HTTP %d checks passed", resp.StatusCode)
		return r
	}
}

func acceptedStatus(code int, ranges []StatusRange) bool {
	if len(ranges) == 0 {
		return code >= 200 && code <= 299
	}
	for _, r := range ranges {
		if code >= r.Min && code <= r.Max {
			return true
		}
	}
	return false
}
