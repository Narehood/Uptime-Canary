// Package delivery performs a single bounded outbound provider attempt.
package delivery

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"uptime-canary/apps/node-go/internal/notifyhttp"
)

type Kind string

const (
	SMTP     Kind = "smtp"
	Mailgun  Kind = "mailgun"
	Brevo    Kind = "brevo"
	Mandrill Kind = "mandrill"
	Webhook  Kind = "webhook"
	Slack    Kind = "slack"
	Telegram Kind = "telegram"
	Ntfy     Kind = "ntfy"
	Gotify   Kind = "gotify"
	Pushover Kind = "pushover"
)

type Status string

const (
	Accepted  Status = "accepted"
	Partial   Status = "partial"
	Rejected  Status = "rejected"
	Retryable Status = "retryable"
	Unknown   Status = "unknown"
)

const (
	MaxRecipients    = 50
	MaxBodyBytes     = 64 << 10
	MaxResponseBytes = 64 << 10
	DefaultTimeout   = 5 * time.Second
	MaxTimeout       = 60 * time.Second
	MaxRetryAfter    = 24 * time.Hour
)

type Address struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

type SMTPConfig struct {
	Server   string `json:"server"`
	Port     int    `json:"port"`
	TLSMode  string `json:"tlsMode,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type MailgunConfig struct {
	APIKey            string `json:"apiKey"`
	Domain            string `json:"domain"`
	Region            string `json:"region,omitempty"`
	BaseURL           string `json:"baseURL,omitempty"`
	AllowInsecureHTTP bool   `json:"allowInsecureHTTP,omitempty"`
}

type BrevoConfig struct {
	APIKey            string `json:"apiKey"`
	BaseURL           string `json:"baseURL,omitempty"`
	AllowInsecureHTTP bool   `json:"allowInsecureHTTP,omitempty"`
}

type MandrillConfig struct {
	APIKey            string `json:"apiKey"`
	BaseURL           string `json:"baseURL,omitempty"`
	AllowInsecureHTTP bool   `json:"allowInsecureHTTP,omitempty"`
}

type WebhookConfig struct {
	URL               string `json:"url"`
	BearerToken       string `json:"bearerToken,omitempty"`
	AllowInsecureHTTP bool   `json:"allowInsecureHTTP,omitempty"`
}

type Templates struct {
	Subject string `json:"subject,omitempty"`
	Text    string `json:"text,omitempty"`
	HTML    string `json:"html,omitempty"`
}

// Config contains credentials. Store privately and redact it before display.
type Config struct {
	Kind       Kind                       `json:"kind"`
	Sender     Address                    `json:"sender,omitempty"`
	Recipients []Address                  `json:"recipients,omitempty"`
	SMTP       SMTPConfig                 `json:"smtp,omitempty"`
	Mailgun    MailgunConfig              `json:"mailgun,omitempty"`
	Brevo      BrevoConfig                `json:"brevo,omitempty"`
	Mandrill   MandrillConfig             `json:"mandrill,omitempty"`
	Webhook    WebhookConfig              `json:"webhook,omitempty"`
	Templates  Templates                  `json:"templates,omitempty"`
	Slack      *notifyhttp.SlackConfig    `json:"slack,omitempty"`
	Telegram   *notifyhttp.TelegramConfig `json:"telegram,omitempty"`
	Ntfy       *notifyhttp.NtfyConfig     `json:"ntfy,omitempty"`
	Gotify     *notifyhttp.GotifyConfig   `json:"gotify,omitempty"`
	Pushover   *notifyhttp.PushoverConfig `json:"pushover,omitempty"`
}

type Message struct {
	EventKind           string          `json:"eventKind,omitempty"`
	MonitorName         string          `json:"monitorName,omitempty"`
	MonitorID           string          `json:"monitorId,omitempty"`
	IncidentID          string          `json:"incidentId,omitempty"`
	ObservationsSummary string          `json:"observationsSummary,omitempty"`
	IdempotencyKey      string          `json:"idempotencyKey,omitempty"`
	Subject             string          `json:"subject,omitempty"`
	Text                string          `json:"text,omitempty"`
	HTML                string          `json:"html,omitempty"`
	APIBody             json.RawMessage `json:"apiBody,omitempty"`
}

type RecipientResult struct {
	Index      int    `json:"index"`
	Accepted   bool   `json:"accepted"`
	Status     Status `json:"status"`
	ProviderID string `json:"providerId,omitempty"`
}

// Result contains only fixed error strings, counts, indices and hashed IDs.
// Accepted acknowledges outbound acceptance, never delivery to an inbox.
type Result struct {
	Kind               Kind              `json:"kind"`
	Accepted           bool              `json:"accepted"`
	Status             Status            `json:"status"`
	HTTPStatus         int               `json:"httpStatus,omitempty"`
	ProviderID         string            `json:"providerId,omitempty"`
	RetryAfter         time.Duration     `json:"retryAfter,omitempty"`
	ErrorCode          string            `json:"errorCode,omitempty"`
	ErrorMessage       string            `json:"errorMessage,omitempty"`
	AcceptedRecipients int               `json:"acceptedRecipients,omitempty"`
	RejectedRecipients int               `json:"rejectedRecipients,omitempty"`
	Recipients         []RecipientResult `json:"recipients,omitempty"`
}

// Options are trusted process-level settings, not per-channel input.
type Options struct {
	HTTPClient  *http.Client
	Timeout     time.Duration
	SMTPRootCAs *x509.CertPool
}

type Engine struct {
	client    *http.Client
	timeout   time.Duration
	roots     *x509.CertPool
	messaging *notifyhttp.Engine
}

// Reuse connection pools for the convenience Send and zero-value Engine forms.
var defaultEngine = New(Options{})

// New snapshots a client and overrides its redirect policy. A supplied Transport
// must honor request contexts. Timeout is capped at MaxTimeout.
func New(opts Options) *Engine {
	client := http.Client{}
	if opts.HTTPClient != nil {
		client = *opts.HTTPClient
	} else {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.MaxResponseHeaderBytes = MaxResponseBytes
		client.Transport = transport
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Jar = nil // No ambient session credentials or cookies.
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	if timeout > MaxTimeout {
		timeout = MaxTimeout
	}
	var roots *x509.CertPool
	if opts.SMTPRootCAs != nil {
		roots = opts.SMTPRootCAs.Clone()
	}
	// Only the validated timeout is passed; these leaf options cannot fail.
	messaging, _ := notifyhttp.New(notifyhttp.Options{Timeout: timeout})
	return &Engine{client: &client, timeout: timeout, roots: roots, messaging: messaging}
}

// Send performs exactly one attempt. There is no sleep, fallback or retry.
func (e *Engine) Send(ctx context.Context, config Config, message Message) Result {
	// A zero Engine is usable with the same defaults as New(Options{}).
	if e == nil || e.client == nil {
		e = defaultEngine
	}
	if err := Validate(config); err != nil {
		return failure(config.Kind, Rejected, "invalid_config")
	}
	defaultTitle := message.Subject == "" && config.Templates.Subject == ""
	defaultText := message.Text == "" && config.Templates.Text == ""
	message, err := prepareMessage(config, message)
	if err != nil {
		return failure(config.Kind, Rejected, "invalid_message")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	if ctx.Err() != nil {
		return failure(config.Kind, Unknown, "canceled")
	}
	if IsMessaging(config.Kind) {
		return e.sendMessaging(ctx, config, message, defaultTitle, defaultText)
	}
	if config.Kind == SMTP {
		return e.sendSMTP(ctx, config, message)
	}
	return e.sendHTTP(ctx, config, message)
}

// Send is the convenience form using default engine options.
func Send(ctx context.Context, config Config, message Message) Result {
	return defaultEngine.Send(ctx, config, message)
}

func failure(kind Kind, status Status, code string) Result {
	switch kind {
	case SMTP, Mailgun, Brevo, Mandrill, Webhook, Slack, Telegram, Ntfy, Gotify, Pushover:
	default:
		kind = ""
	}
	messages := map[string]string{
		"invalid_config":     "Delivery configuration is invalid",
		"invalid_message":    "Delivery message is invalid",
		"canceled":           "Delivery attempt was canceled or timed out",
		"transport":          "Provider connection failed; acceptance is unknown",
		"response":           "Provider acknowledgement is invalid; acceptance is unknown",
		"response_too_large": "Provider response exceeds the size limit; acceptance is unknown",
		"rate_limited":       "Provider rate limit reached",
		"unavailable":        "Provider is temporarily unavailable",
		"provider_rejected":  "Provider rejected the request",
		"recipient_rejected": "Provider rejected one or more recipients",
		"partial_acceptance": "Provider accepted only some recipients",
		"smtp_protocol":      "SMTP protocol negotiation failed",
		"smtp_tls":           "SMTP verified TLS negotiation failed",
		"smtp_auth":          "SMTP authentication failed",
	}
	return Result{Kind: kind, Status: status, ErrorCode: code, ErrorMessage: messages[code]}
}

// Provider IDs are always digested so malicious provider data cannot echo a
// credential or recipient through a field otherwise intended for opaque IDs.
func providerID(raw string) string {
	if raw == "" || len(raw) > 1024 {
		return ""
	}
	digest := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func accepted(config Config, id string) Result {
	count := len(config.Recipients)
	if config.Kind == Webhook {
		count = 0
	}
	return Result{Kind: config.Kind, Accepted: true, Status: Accepted,
		ProviderID: providerID(id), AcceptedRecipients: count}
}
