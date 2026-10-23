package checks

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func invalid(message string) error { return errors.New(message) }

// Validate returns field-level descriptions containing no configured values.
func Validate(c Config) error {
	if c.Timeout <= 0 || c.Timeout > 5*time.Minute {
		return invalid("timeout must be positive and at most five minutes")
	}
	if len(c.Target) == 0 || len(c.Target) > 8192 || hasControl(c.Target) {
		return invalid("target is missing, too long, or contains control characters")
	}
	if c.HTTP != nil && c.Type != HTTP || c.DNS != nil && c.Type != DNS || c.WebSocket != nil && c.Type != WebSocket || c.SMTP != nil && c.Type != SMTP {
		return invalid("options do not match check type")
	}
	secure := false
	switch c.Type {
	case HTTP:
		u, err := parseTargetURL(c.Target, false)
		if err != nil {
			return err
		}
		secure = u.Scheme == "https"
		if c.HTTP != nil && c.HTTP.OAuth2 != nil && !c.HTTP.OAuth2.AllowInsecureHTTP && !secure {
			return invalid("OAuth2 requires HTTPS unless insecure HTTP is explicitly enabled")
		}
		if err := validateHTTP(httpOptions(c)); err != nil {
			return err
		}
	case TCP, TLS, SMTP:
		if err := validateAddress(c.Target, false); err != nil {
			return err
		}
		secure = c.Type == TLS
		if c.Type == SMTP {
			mode := smtpMode(c)
			if mode != "plain" && mode != "implicit" && mode != "starttls" {
				return invalid("SMTP TLS mode is invalid")
			}
			secure = mode != "plain"
		}
	case DNS:
		o := dnsOptions(c)
		rt := dnsType(o)
		if rt != "A" && rt != "AAAA" && rt != "CNAME" && rt != "MX" && rt != "TXT" && rt != "NS" && rt != "PTR" {
			return invalid("DNS record type is unsupported")
		}
		if rt == "PTR" {
			if net.ParseIP(c.Target) == nil {
				return invalid("PTR target must be an IP address")
			}
		} else if !validDNSName(c.Target) {
			return invalid("DNS query name is invalid")
		}
		if o.Server != "" {
			if err := validateAddress(o.Server, true); err != nil {
				return invalid("DNS server must be a literal IP and port")
			}
		}
		if len(o.Expected) > 128 {
			return invalid("too many DNS expectations")
		}
		total := 0
		for _, v := range o.Expected {
			total += len(v)
			if len(v) > 8192 || total > maxDNSBytes {
				return invalid("DNS expectations exceed size limit")
			}
			if _, ok := canonicalRecord(rt, v); !ok {
				return invalid("DNS expected record is invalid")
			}
		}
	case WebSocket:
		u, err := parseTargetURL(c.Target, true)
		if err != nil {
			return err
		}
		secure = u.Scheme == "wss"
		o := wsOptions(c)
		if err := validateRequestOptions(o.Headers, o.BasicAuth, o.BearerToken, o.ProxyURL, o.UseEnvironmentProxy); err != nil {
			return err
		}
	default:
		return invalid("check type is unsupported")
	}
	if c.TLS != nil {
		if !secure {
			return invalid("TLS options require an encrypted check")
		}
		if err := validateTLS(*c.TLS); err != nil {
			return err
		}
	}
	return nil
}

func httpOptions(c Config) HTTPConfig {
	if c.HTTP != nil {
		return *c.HTTP
	}
	return HTTPConfig{}
}
func wsOptions(c Config) WebSocketConfig {
	if c.WebSocket != nil {
		return *c.WebSocket
	}
	return WebSocketConfig{}
}
func dnsOptions(c Config) DNSConfig {
	if c.DNS != nil {
		return *c.DNS
	}
	return DNSConfig{}
}
func dnsType(c DNSConfig) string {
	if c.RecordType == "" {
		return "A"
	}
	return strings.ToUpper(c.RecordType)
}
func smtpMode(c Config) string {
	if c.SMTP != nil && c.SMTP.TLSMode != "" {
		return c.SMTP.TLSMode
	}
	return "plain"
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 32 || r == 127 {
			return true
		}
	}
	return false
}
func validToken(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(b)) {
			continue
		}
		return false
	}
	return true
}
func validDNSName(s string) bool {
	s = strings.TrimSuffix(s, ".")
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := range label {
			b := label[i]
			if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_' {
				continue
			}
			return false
		}
	}
	return true
}

func validateAddress(address string, literal bool) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || hasControl(address) || strings.ContainsAny(address, " /\\@?#") {
		return invalid("target must be a valid host and numeric port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || len(port) > 5 || n < 1 || n > 65535 {
		return invalid("target port must be between one and 65535")
	}
	for i := range port {
		if port[i] < '0' || port[i] > '9' {
			return invalid("target port must contain only decimal digits")
		}
	}
	if net.ParseIP(host) == nil && (literal || !validDNSName(host)) {
		return invalid("target host is invalid")
	}
	return nil
}

func parseTargetURL(raw string, ws bool) (*url.URL, error) {
	if len(raw) > 8192 || hasControl(raw) {
		return nil, invalid("URL is invalid or too long")
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Host == "" {
		return nil, invalid("URL must have a host and no userinfo or fragment")
	}
	if ws {
		if u.Scheme != "ws" && u.Scheme != "wss" {
			return nil, invalid("WebSocket URL must use ws or wss")
		}
	} else if u.Scheme != "http" && u.Scheme != "https" {
		return nil, invalid("HTTP URL must use http or https")
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" || u.Scheme == "wss" {
			port = "443"
		}
	}
	if strings.HasSuffix(u.Host, ":") {
		return nil, invalid("URL port is invalid")
	}
	if strings.Contains(u.Hostname(), ":") && !strings.HasPrefix(u.Host, "[") {
		return nil, invalid("IPv6 URL hosts must use brackets")
	}
	if err := validateAddress(net.JoinHostPort(u.Hostname(), port), false); err != nil {
		return nil, invalid("URL host or port is invalid")
	}
	return u, nil
}

func validateRequestOptions(headers map[string]string, basic *BasicAuth, bearer, proxy string, env bool) error {
	if len(headers) > 100 {
		return invalid("too many request headers")
	}
	total := 0
	seen := map[string]bool{}
	authHeader := false
	for k, v := range headers {
		key := strings.ToLower(k)
		if !validToken(k) || seen[key] {
			return invalid("request header name is invalid or duplicated")
		}
		seen[key] = true
		if len(v) > 8192 || hasControl(v) {
			return invalid("request header value is invalid or too long")
		}
		switch key {
		case "host", "connection", "content-length", "transfer-encoding", "upgrade", "proxy-authorization", "proxy-connection", "trailer", "te":
			return invalid("transport-managed request header cannot be configured")
		}
		if strings.HasPrefix(key, "sec-websocket-") {
			return invalid("WebSocket protocol headers cannot be configured")
		}
		if key == "authorization" {
			authHeader = true
		}
		total += len(k) + len(v) + 4
	}
	if total > maxHeaderBytes {
		return invalid("request headers exceed size limit")
	}
	if basic != nil {
		if len(basic.Username)+len(basic.Password) > 8192 || strings.Contains(basic.Username, ":") || hasControl(basic.Username) || hasControl(basic.Password) {
			return invalid("basic authentication fields are invalid or too long")
		}
	}
	if len(bearer) > 8192 || hasControl(bearer) || strings.Contains(bearer, " ") {
		return invalid("bearer token is invalid or too long")
	}
	if basic != nil && bearer != "" || authHeader && (basic != nil || bearer != "") {
		return invalid("configure only one HTTP authentication source")
	}
	if proxy != "" {
		if env {
			return invalid("configure either proxy URL or environment proxy")
		}
		u, err := parseTargetURL(proxy, false)
		if err != nil || u.RawQuery != "" || u.ForceQuery || u.Path != "" && u.Path != "/" {
			return invalid("proxy URL must be HTTP(S) with no credentials, query, or non-root path")
		}
	}
	return nil
}

func validateHTTP(o HTTPConfig) error {
	method := o.Method
	if method == "" {
		method = "GET"
	}
	if len(method) > 32 || !validToken(method) || strings.EqualFold(method, "CONNECT") {
		return invalid("HTTP method is invalid or unsupported")
	}
	if len(o.Body) > MaxBodyBytes {
		return invalid("HTTP request body exceeds one MiB")
	}
	if err := validateRequestOptions(o.Headers, o.BasicAuth, o.BearerToken, o.ProxyURL, o.UseEnvironmentProxy); err != nil {
		return err
	}
	if o.OAuth2 != nil {
		if o.BasicAuth != nil || o.BearerToken != "" {
			return invalid("configure only one HTTP authentication source")
		}
		for key := range o.Headers {
			if strings.EqualFold(key, "Authorization") {
				return invalid("configure only one HTTP authentication source")
			}
		}
		if err := validateOAuth2(*o.OAuth2, ""); err != nil {
			return err
		}
	}
	if len(o.StatusRanges) > 100 {
		return invalid("too many HTTP status ranges")
	}
	for _, r := range o.StatusRanges {
		if r.Min < 100 || r.Max > 599 || r.Min > r.Max {
			return invalid("HTTP status range is invalid")
		}
	}
	if o.MaxRedirects != nil && (*o.MaxRedirects < 0 || *o.MaxRedirects > 10) {
		return invalid("HTTP redirect limit must be between zero and ten")
	}
	if method == "HEAD" && (o.Keyword != nil || o.JSON != nil || o.JSONata != nil) {
		return invalid("HEAD cannot use body matchers")
	}
	if o.Keyword != nil {
		if o.Keyword.Value == "" || len(o.Keyword.Value) > 16384 {
			return invalid("keyword must be nonempty and at most 16 KiB")
		}
		if o.Keyword.Regex {
			if len(o.Keyword.Value) > 1024 {
				return invalid("keyword regular expression exceeds size limit")
			}
			if _, err := regexp.Compile(o.Keyword.Value); err != nil {
				return invalid("keyword regular expression is invalid")
			}
		}
	}
	if o.JSON != nil {
		if _, err := parseSelector(o.JSON.Path); err != nil {
			return err
		}
		if o.JSON.ExistsOnly {
			if o.JSON.Expected != "" {
				return invalid("JSON existence matcher cannot specify an expected value")
			}
		} else {
			if len(o.JSON.Expected) > 64<<10 {
				return invalid("JSON expected value exceeds size limit")
			}
			if _, err := decodeJSON([]byte(o.JSON.Expected)); err != nil {
				return invalid("JSON expected value is invalid or exceeds nesting/number limits")
			}
		}
	}
	if o.JSONata != nil && o.JSONata.Validate() != nil {
		return invalid("JSONata condition shape, scalar expectation, or size is invalid")
	}
	return nil
}

func validateTLS(o TLSConfig) error {
	if len(o.ServerName) > 253 || o.ServerName != "" && net.ParseIP(o.ServerName) == nil && !validDNSName(o.ServerName) {
		return invalid("TLS server name is invalid")
	}
	if o.MinValidity < 0 || o.MinValidity > 10*365*24*time.Hour {
		return invalid("TLS minimum validity must be between zero and ten years")
	}
	if len(o.CAPEM) > maxPEMBytes || len(o.ClientCertPEM) > maxPEMBytes || len(o.ClientKeyPEM) > maxPEMBytes {
		return invalid("TLS PEM input exceeds size limit")
	}
	if o.CAPEM != "" && !x509.NewCertPool().AppendCertsFromPEM([]byte(o.CAPEM)) {
		return invalid("TLS CA PEM has no valid certificates")
	}
	if (o.ClientCertPEM == "") != (o.ClientKeyPEM == "") {
		return invalid("TLS client certificate and key must both be configured")
	}
	if o.ClientCertPEM != "" {
		if _, err := tls.X509KeyPair([]byte(o.ClientCertPEM), []byte(o.ClientKeyPEM)); err != nil {
			return invalid("TLS client certificate or key is invalid")
		}
	}
	return nil
}
