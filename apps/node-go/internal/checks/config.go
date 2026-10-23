// Package checks implements bounded, independent active network checks.
// Configuration may contain secrets; errors and results never echo its values.
package checks

import (
	"time"

	"uptime-canary/apps/node-go/internal/jsonquery"
)

type Type string

const (
	HTTP      Type = "http"
	TCP       Type = "tcp"
	TLS       Type = "tls"
	DNS       Type = "dns"
	WebSocket Type = "websocket"
	SMTP      Type = "smtp"
	Up             = "up"
	Down           = "down"
	Pending        = "pending"
)

const (
	MaxBodyBytes           = 1 << 20
	MaxResponseHeaderBytes = 64 << 10
	maxHeaderBytes         = 32 << 10
	maxPEMBytes            = 256 << 10
	maxDNSBytes            = 64 << 10
	maxWSBytes             = 64 << 10
	maxWSFrames            = 64
)

// Config.Timeout is mandatory. The earlier of this timeout and the caller's
// deadline applies. Options for unrelated protocols are rejected.
type Config struct {
	Type      Type
	Target    string
	Timeout   time.Duration
	HTTP      *HTTPConfig
	TLS       *TLSConfig
	DNS       *DNSConfig
	WebSocket *WebSocketConfig
	SMTP      *SMTPConfig
}

type Result struct {
	State       string              `json:"state"`
	LatencyMs   float64             `json:"latencyMs"`
	Message     string              `json:"message"`
	StatusCode  int                 `json:"statusCode,omitempty"`
	Certificate *CertificateSummary `json:"certificate,omitempty"`
}

// CertificateSummary contains only validity metadata, never names or raw PEM.
// Verified is false when IgnoreVerification was explicitly enabled.
type CertificateSummary struct {
	NotBefore     time.Time `json:"notBefore"`
	NotAfter      time.Time `json:"notAfter"`
	DaysRemaining int       `json:"daysRemaining"`
	Verified      bool      `json:"verified"`
}

type BasicAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
}
type StatusRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}
type KeywordMatcher struct {
	Value  string `json:"value"`
	Regex  bool   `json:"regex,omitempty"`
	Negate bool   `json:"negate,omitempty"`
}
type JSONMatcher struct {
	Path       string `json:"path"`
	Expected   string `json:"expected,omitempty"` // JSON-encoded value, not a JavaScript expression
	ExistsOnly bool   `json:"existsOnly,omitempty"`
	Negate     bool   `json:"negate,omitempty"`
}

type HTTPConfig struct {
	Method              string            `json:"method,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
	Body                string            `json:"body,omitempty"`
	BasicAuth           *BasicAuth        `json:"basicAuth,omitempty"`
	BearerToken         string            `json:"bearerToken,omitempty"`
	OAuth2              *OAuth2Config     `json:"oauth2,omitempty"`
	StatusRanges        []StatusRange     `json:"statusRanges,omitempty"`
	MaxRedirects        *int              `json:"maxRedirects,omitempty"` // nil = 5, 0 = return the first redirect response
	Keyword             *KeywordMatcher   `json:"keyword,omitempty"`
	JSON                *JSONMatcher      `json:"json,omitempty"`
	JSONata             *jsonquery.Config `json:"jsonata,omitempty"`
	ProxyURL            string            `json:"proxyUrl,omitempty"`
	UseEnvironmentProxy bool              `json:"useEnvironmentProxy,omitempty"`
}

type TLSConfig struct {
	ServerName    string        `json:"serverName,omitempty"`
	CAPEM         string        `json:"caPem,omitempty"` // additional trusted CA certificates
	ClientCertPEM string        `json:"clientCertPem,omitempty"`
	ClientKeyPEM  string        `json:"clientKeyPem,omitempty"`
	MinValidity   time.Duration `json:"minValidity,omitempty"`
	// IgnoreVerification skips chain/hostname verification. Dates and
	// MinValidity are still enforced. Results explicitly mark this as unverified.
	IgnoreVerification bool `json:"ignoreVerification,omitempty"`
}

type DNSConfig struct {
	RecordType string   `json:"recordType,omitempty"` // default A
	Server     string   `json:"server,omitempty"`     // optional literal IP:port
	Expected   []string `json:"expected,omitempty"`   // all expected records must be present; extras are allowed
}

type WebSocketConfig struct {
	Headers             map[string]string `json:"headers,omitempty"`
	BasicAuth           *BasicAuth        `json:"basicAuth,omitempty"`
	BearerToken         string            `json:"bearerToken,omitempty"`
	ProxyURL            string            `json:"proxyUrl,omitempty"`
	UseEnvironmentProxy bool              `json:"useEnvironmentProxy,omitempty"`
}

type SMTPConfig struct {
	TLSMode string `json:"tlsMode,omitempty"` // default plain; also implicit or starttls
}

func (c Config) Validate() error { return Validate(c) }
