// Package steam checks Steam directory membership and optionally measures ICMP
// RTT. A directory listing is not a direct game-protocol handshake.
package steam

import (
	"bytes"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"uptime-canary/apps/node-go/internal/icmp"
)

const DefaultAPIURL = "https://api.steampowered.com"

type Options struct {
	APIKey           string `json:"apiKey"`
	APIURL           string `json:"apiUrl,omitempty"`
	APICA            string `json:"apiCa,omitempty"`
	AllowInsecureAPI bool   `json:"allowInsecureApi,omitempty"`
	APIFailureIsDown bool   `json:"apiFailureIsDown,omitempty"`
	SkipLatency      bool   `json:"skipLatency,omitempty"`
	PacketSize       *int   `json:"packetSize,omitempty"`
}

type Config struct {
	Target  string
	Timeout time.Duration
	Options Options
}

type Result struct {
	State            string
	Message          string
	LatencyMs        float64
	LatencyAvailable bool
}

var errConfig = errors.New("invalid Steam configuration")

// UnmarshalJSON preserves the same exact-key/non-null contract for API and
// peer input. The leaf's Options is also the root's monitor-options type.
func (o *Options) UnmarshalJSON(raw []byte) error {
	if len(raw) > 140<<10 {
		return errConfig
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return errConfig
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return errConfig
		}
		switch key {
		case "apiKey", "apiUrl", "apiCa", "allowInsecureApi", "apiFailureIsDown", "skipLatency", "packetSize":
		default:
			return errConfig
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errConfig
		}
	}
	if _, err = d.Token(); err != nil {
		return errConfig
	}
	if _, err = d.Token(); err != io.EOF || !seen["apiKey"] {
		return errConfig
	}
	type plain Options
	var decoded plain
	if json.Unmarshal(raw, &decoded) != nil {
		return errConfig
	}
	*o = Options(decoded)
	return nil
}

type prepared struct {
	host  string
	port  string
	base  *url.URL
	certs []*x509.Certificate
}

func Validate(c Config) error { _, err := prepare(c); return err }

func prepare(c Config) (*prepared, error) {
	if c.Timeout <= 0 || c.Timeout > 2*time.Minute || len(c.Target) > 320 ||
		len(c.Options.APIKey) == 0 || len(c.Options.APIKey) > 256 || len(c.Options.APICA) > 128<<10 {
		return nil, errConfig
	}
	for _, ch := range []byte(c.Options.APIKey) {
		if ch < 33 || ch > 126 {
			return nil, errConfig
		}
	}
	host, port, err := net.SplitHostPort(c.Target)
	if err != nil || host == "" || strings.Contains(host, "%") {
		return nil, errConfig
	}
	if icmp.Validate(icmp.Config{Target: host, Timeout: c.Timeout, Count: 1, Size: c.Options.PacketSize}) != nil {
		return nil, errConfig
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != port {
		return nil, errConfig
	}
	base := c.Options.APIURL
	if base == "" {
		base = DefaultAPIURL
	}
	if len(base) > 2048 || strings.ContainsAny(base, " \t\r\n\\") {
		return nil, errConfig
	}
	u, err := url.Parse(base)
	if err != nil || u == nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || u.Hostname() == "" || strings.HasSuffix(u.Host, ":") || u.RawPath != "" ||
		u.Scheme != "https" && !(u.Scheme == "http" && c.Options.AllowInsecureAPI) ||
		strings.Contains(u.Hostname(), "%") || icmp.Validate(icmp.Config{Target: u.Hostname(), Timeout: time.Second}) != nil {
		return nil, errConfig
	}
	if u.Port() != "" {
		p, err := strconv.ParseUint(u.Port(), 10, 16)
		if err != nil || p == 0 {
			return nil, errConfig
		}
	}
	if u.Path != "" && path.Clean(u.Path) != strings.TrimSuffix(u.Path, "/") {
		if u.Path != "/" {
			return nil, errConfig
		}
	}
	for _, ch := range u.Path {
		if ch < 33 || ch > 126 {
			return nil, errConfig
		}
	}
	result := &prepared{host: host, port: port, base: u}
	rest := []byte(c.Options.APICA)
	for len(bytes.TrimSpace(rest)) != 0 {
		rest = bytes.TrimSpace(rest)
		if !bytes.HasPrefix(rest, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, errConfig
		}
		end := bytes.Index(rest, []byte("-----END CERTIFICATE-----"))
		if end < 0 || bytes.Contains(rest[len("-----BEGIN CERTIFICATE-----"):end], []byte("-----BEGIN")) {
			return nil, errConfig
		}
		end += len("-----END CERTIFICATE-----")
		block, leftover := pem.Decode(rest[:end])
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, errConfig
		}
		if len(bytes.TrimSpace(leftover)) != 0 {
			return nil, errConfig
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errConfig
		}
		result.certs = append(result.certs, certificate)
		rest = rest[end:]
	}
	return result, nil
}
