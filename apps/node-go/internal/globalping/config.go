// Package globalping performs one remote Globalping measurement per Check.
// It owns no scheduler, persistence, HA coordination, or monitor state.
package globalping

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"uptime-canary/apps/node-go/internal/jsonquery"
)

const (
	Up              = "up"
	Down            = "down"
	Pending         = "pending"
	DefaultAPIURL   = "https://api.globalping.io"
	maxHeadersBytes = 32 << 10
	maxCABytes      = 128 << 10
)

type Options struct {
	Mode                string       `json:"mode"`
	Location            string       `json:"location"`
	APIURL              string       `json:"apiUrl,omitempty"`
	Token               string       `json:"token,omitempty"`
	AllowInsecureAPI    bool         `json:"allowInsecureApi,omitempty"`
	APICA               string       `json:"apiCa,omitempty"`
	ProbeTimeoutSeconds int          `json:"probeTimeoutSeconds,omitempty"`
	IPVersion           int          `json:"ipVersion,omitempty"`
	Ping                *PingOptions `json:"ping,omitempty"`
	HTTP                *HTTPOptions `json:"http,omitempty"`
	DNS                 *DNSOptions  `json:"dns,omitempty"`
	APIFailureIsDown    bool         `json:"apiFailureIsDown,omitempty"`
}

type PingOptions struct {
	Packets  int    `json:"packets,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Port     int    `json:"port,omitempty"`
}

type StatusRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type HTTPOptions struct {
	Method           string            `json:"method,omitempty"`
	Protocol         string            `json:"protocol,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	AcceptedStatuses []StatusRange     `json:"acceptedStatuses,omitempty"`
	Keyword          string            `json:"keyword,omitempty"`
	InvertKeyword    bool              `json:"invertKeyword,omitempty"`
	JSONata          *jsonquery.Config `json:"jsonata,omitempty"`
	IgnoreTLS        bool              `json:"ignoreTls,omitempty"`
}

type DNSOptions struct {
	QueryType         string `json:"queryType,omitempty"`
	Protocol          string `json:"protocol,omitempty"`
	Port              int    `json:"port,omitempty"`
	Resolver          string `json:"resolver,omitempty"`
	Regex             string `json:"regex,omitempty"`
	AllowEmptyAnswers bool   `json:"allowEmptyAnswers,omitempty"`
}

type Config struct {
	Target  string        `json:"target"`
	Timeout time.Duration `json:"timeout"`
	Options Options       `json:"options"`
}

// LatencyMs is meaningful only when LatencyAvailable is true. It is always a
// provider-reported remote timing, never elapsed API or worker time.
type Result struct {
	State            string  `json:"state"`
	LatencyMs        float64 `json:"latencyMs"`
	LatencyAvailable bool    `json:"latencyAvailable"`
	Message          string  `json:"message"`
	StatusCode       int     `json:"statusCode,omitempty"`
}

var errConfig = errors.New("invalid Globalping configuration")

type prepared struct {
	config   Config
	api      *url.URL
	certs    []*x509.Certificate
	payload  createRequest
	https    bool
	dnsRegex *regexp.Regexp
}

// Validate checks bounded data and RE2 syntax. It never compiles or evaluates
// JSONata, performs DNS resolution, or contacts either API or target.
func Validate(c Config) error {
	_, err := prepare(c)
	return err
}

func prepare(c Config) (*prepared, error) {
	o := c.Options
	if c.Timeout <= 0 || c.Timeout > 5*time.Minute || !text(c.Target, 4096, false) ||
		!text(o.Mode, 16, false) || !text(o.Location, 256, false) || strings.TrimSpace(o.Location) == "" ||
		len(o.APIURL) > 2048 || len(o.Token) > 4096 || len(o.APICA) > maxCABytes ||
		o.ProbeTimeoutSeconds != 0 && (o.ProbeTimeoutSeconds < 5 || o.ProbeTimeoutSeconds > 30) ||
		o.IPVersion != 0 && o.IPVersion != 4 && o.IPVersion != 6 {
		return nil, errConfig
	}
	for _, b := range []byte(o.Token) {
		if b < 0x21 || b > 0x7e {
			return nil, errConfig
		}
	}
	o.Mode = strings.ToLower(strings.TrimSpace(o.Mode))
	api, err := apiURL(o.APIURL, o.AllowInsecureAPI)
	if err != nil {
		return nil, errConfig
	}
	certs, err := parseCA(o.APICA)
	if err != nil || len(certs) > 0 && api.Scheme != "https" {
		return nil, errConfig
	}
	p := &prepared{config: c, api: api, certs: certs}
	p.config.Options = o
	p.payload = createRequest{Type: o.Mode, Target: c.Target, Limit: 1,
		Locations: []location{{Magic: o.Location}}, Timeout: o.ProbeTimeoutSeconds,
		MeasurementOptions: measurementOptions{IPVersion: o.IPVersion}}
	m := &p.payload.MeasurementOptions
	switch o.Mode {
	case "ping":
		if o.Ping == nil || o.HTTP != nil || o.DNS != nil || !host(c.Target, false) {
			return nil, errConfig
		}
		v := *o.Ping
		if len(v.Protocol) > 16 {
			return nil, errConfig
		}
		v.Protocol = upperDefault(v.Protocol, "ICMP")
		if v.Packets == 0 {
			v.Packets = 3
		}
		if v.Packets < 1 || v.Packets > 16 || v.Protocol != "ICMP" && v.Protocol != "TCP" ||
			v.Port < 0 || v.Port > 65535 || v.Protocol != "TCP" && v.Port != 0 {
			return nil, errConfig
		}
		if v.Protocol == "TCP" && v.Port == 0 {
			v.Port = 80
		}
		p.config.Options.Ping = &v
		m.Packets, m.Protocol, m.Port = v.Packets, v.Protocol, v.Port
	case "http":
		if o.HTTP == nil || o.Ping != nil || o.DNS != nil {
			return nil, errConfig
		}
		u, err := targetURL(c.Target)
		if err != nil {
			return nil, errConfig
		}
		v := *o.HTTP
		if len(v.Method) > 16 || len(v.Protocol) > 16 || len(v.AcceptedStatuses) > 32 ||
			len(v.Keyword) > 4096 || !utf8.ValidString(v.Keyword) || v.InvertKeyword && v.Keyword == "" || !headers(v.Headers) {
			return nil, errConfig
		}
		v.Method = upperDefault(v.Method, "GET")
		if v.Method != "GET" && v.Method != "HEAD" && v.Method != "OPTIONS" {
			return nil, errConfig
		}
		v.Protocol = strings.ToUpper(v.Protocol)
		if v.Protocol != "" && (v.Protocol != "HTTP2" || u.Scheme != "https") {
			return nil, errConfig
		}
		for _, r := range v.AcceptedStatuses {
			if r.Min < 100 || r.Max > 599 || r.Min > r.Max {
				return nil, errConfig
			}
		}
		if v.JSONata != nil && v.JSONata.Validate() != nil {
			return nil, errConfig
		}
		p.config.Options.HTTP = &v
		p.https = u.Scheme == "https"
		m.Protocol = strings.ToUpper(u.Scheme)
		if v.Protocol != "" {
			m.Protocol = v.Protocol
		}
		m.Port = 80
		if p.https {
			m.Port = 443
		}
		if u.Port() != "" {
			m.Port, _ = strconv.Atoi(u.Port())
		}
		path := u.EscapedPath()
		if path == "" {
			path = "/"
		}
		m.Request = &requestOptions{Method: v.Method, Path: path, Query: u.RawQuery, Headers: v.Headers}
		p.payload.Target = u.Hostname()
	case "dns":
		if o.DNS == nil || o.Ping != nil || o.HTTP != nil || !host(c.Target, true) {
			return nil, errConfig
		}
		v := *o.DNS
		if len(v.QueryType) > 16 || len(v.Protocol) > 16 || len(v.Resolver) > 253 || len(v.Regex) > 4096 ||
			!utf8.ValidString(v.Regex) || v.Port < 0 || v.Port > 65535 {
			return nil, errConfig
		}
		v.QueryType, v.Protocol = upperDefault(v.QueryType, "A"), upperDefault(v.Protocol, "UDP")
		if !queryType(v.QueryType) || v.Protocol != "TCP" && v.Protocol != "UDP" || v.Resolver != "" && !host(v.Resolver, false) {
			return nil, errConfig
		}
		if v.Port == 0 {
			v.Port = 53
		}
		if v.Regex != "" {
			p.dnsRegex, err = regexp.Compile("(?i)" + v.Regex)
			if err != nil {
				return nil, errConfig
			}
		}
		p.config.Options.DNS = &v
		m.Query, m.Protocol, m.Port, m.Resolver = &queryOptions{Type: v.QueryType}, v.Protocol, v.Port, v.Resolver
	default:
		return nil, errConfig
	}
	if net.ParseIP(p.payload.Target) != nil && o.IPVersion != 0 {
		return nil, errConfig
	}
	return p, nil
}

func upperDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return strings.ToUpper(s)
}

func queryType(s string) bool {
	switch s {
	case "A", "AAAA", "ANY", "CNAME", "DNSKEY", "DS", "HTTPS", "MX", "NS", "NSEC", "PTR", "RRSIG", "SOA", "TXT", "SRV", "SVCB":
		return true
	}
	return false
}

func text(s string, max int, empty bool) bool {
	if len(s) > max || !empty && s == "" || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func host(s string, underscore bool) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	if net.ParseIP(s) != nil {
		return true
	}
	s = strings.TrimSuffix(s, ".")
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, b := range []byte(label) {
			if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || underscore && b == '_') {
				return false
			}
		}
	}
	return true
}

func baseURL(s string) (*url.URL, error) {
	if !text(s, 4096, false) || strings.Contains(s, "\\") {
		return nil, errConfig
	}
	u, err := url.Parse(s)
	if err != nil || u.Opaque != "" || u.User != nil || u.Fragment != "" || strings.Contains(s, "#") ||
		u.Scheme != "https" && u.Scheme != "http" || !host(u.Hostname(), false) || strings.HasSuffix(u.Host, ":") {
		return nil, errConfig
	}
	if u.Port() != "" {
		n, err := strconv.Atoi(u.Port())
		if err != nil || n < 1 || n > 65535 {
			return nil, errConfig
		}
	}
	return u, nil
}

func targetURL(s string) (*url.URL, error) { return baseURL(s) }

func apiURL(s string, insecure bool) (*url.URL, error) {
	if s == "" {
		s = DefaultAPIURL
	}
	u, err := baseURL(s)
	if err != nil || u.Scheme == "http" && !insecure || u.RawQuery != "" || u.ForceQuery || u.RawPath != "" {
		return nil, errConfig
	}
	for _, part := range strings.Split(strings.Trim(u.Path, "/"), "/") {
		if part == "." || part == ".." {
			return nil, errConfig
		}
		for _, b := range []byte(part) {
			if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("-._~", rune(b))) {
				return nil, errConfig
			}
		}
	}
	if strings.Contains(u.Path, "//") {
		return nil, errConfig
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/measurements"
	return u, nil
}

func headers(h map[string]string) bool {
	if len(h) > 32 {
		return false
	}
	seen := make(map[string]bool, len(h))
	size := 0
	for k, v := range h {
		if len(k) == 0 || len(k) > 256 || len(v) > maxHeadersBytes || !utf8.ValidString(v) {
			return false
		}
		for _, b := range []byte(k) {
			if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(b))) {
				return false
			}
		}
		for _, r := range v {
			if unicode.IsControl(r) && r != '\t' {
				return false
			}
		}
		lower := strings.ToLower(k)
		if seen[lower] || lower == "host" || lower == "user-agent" {
			return false
		}
		seen[lower] = true
		size += len(k) + len(v)
		if size > maxHeadersBytes {
			return false
		}
	}
	return true
}

func parseCA(s string) ([]*x509.Certificate, error) {
	var certs []*x509.Certificate
	for strings.TrimSpace(s) != "" {
		// pem.Decode may skip leading junk. Trust input must consist of certs only.
		s = strings.TrimSpace(s)
		if !strings.HasPrefix(s, "-----BEGIN CERTIFICATE-----") || len(certs) >= 32 {
			return nil, errConfig
		}
		block, rest := pem.Decode([]byte(s))
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errConfig
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errConfig
		}
		certs = append(certs, cert)
		s = string(rest)
	}
	return certs, nil
}
