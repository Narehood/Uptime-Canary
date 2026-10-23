// Package notifyhttp performs one bounded native HTTP notification attempt.
// Acceptance means provider acknowledgement, never delivery to a person's device.
package notifyhttp

import (
	"context"
	"time"
)

type Kind string

const (
	Slack    Kind = "slack"
	Telegram Kind = "telegram"
	Ntfy     Kind = "ntfy"
	Gotify   Kind = "gotify"
	Pushover Kind = "pushover"
)

type Status string

const (
	Accepted  Status = "accepted"
	Retryable Status = "retryable"
	Rejected  Status = "rejected"
	Unknown   Status = "unknown"
)

const (
	DefaultTimeout         = 5 * time.Second
	MaxTimeout             = 60 * time.Second
	MaxRetryAfter          = 24 * time.Hour
	MaxResponseHeaderBytes = 32 << 10
	MaxResponseBytes       = 64 << 10
	MaxResponseHeaders     = 64
	MaxConfigBytes         = 16 << 10
	MaxRequestBytes        = 64 << 10
	MaxURLBytes            = 2048
	MaxTitleRunes          = 250
	MaxTextBytes           = 16 << 10
	MaxMetadataBytes       = 256
)

// Config contains secrets. Root must store it privately and redact before display.
// Exactly one matching pointer is required; even an empty foreign pointer fails.
type Config struct {
	Kind     Kind            `json:"kind"`
	Slack    *SlackConfig    `json:"slack,omitempty"`
	Telegram *TelegramConfig `json:"telegram,omitempty"`
	Ntfy     *NtfyConfig     `json:"ntfy,omitempty"`
	Gotify   *GotifyConfig   `json:"gotify,omitempty"`
	Pushover *PushoverConfig `json:"pushover,omitempty"`
}

type SlackConfig struct {
	WebhookURL        string `json:"webhookURL"`
	AllowInsecureHTTP bool   `json:"allowInsecureHTTP,omitempty"`
}

type TelegramConfig struct {
	BotToken           string `json:"botToken"`
	ChatID             string `json:"chatId"`
	MessageThreadID    int64  `json:"messageThreadId,omitempty"`
	Silent             bool   `json:"silent,omitempty"`
	ProtectContent     bool   `json:"protectContent,omitempty"`
	DisableLinkPreview bool   `json:"disableLinkPreview,omitempty"`
	BaseURL            string `json:"baseURL,omitempty"`
	AllowInsecureHTTP  bool   `json:"allowInsecureHTTP,omitempty"`
}

type NtfyConfig struct {
	Topic             string   `json:"topic"`
	Token             string   `json:"token,omitempty"`
	Username          string   `json:"username,omitempty"`
	Password          string   `json:"password,omitempty"`
	Priority          int      `json:"priority,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	BaseURL           string   `json:"baseURL,omitempty"`
	AllowInsecureHTTP bool     `json:"allowInsecureHTTP,omitempty"`
}

type GotifyConfig struct {
	AppToken          string `json:"appToken"`
	Priority          int    `json:"priority,omitempty"`
	BaseURL           string `json:"baseURL"`
	AllowInsecureHTTP bool   `json:"allowInsecureHTTP,omitempty"`
}

type PushoverConfig struct {
	AppToken          string `json:"appToken"`
	UserKey           string `json:"userKey"`
	Device            string `json:"device,omitempty"`
	Priority          int    `json:"priority,omitempty"`
	Sound             string `json:"sound,omitempty"`
	RetrySeconds      int    `json:"retrySeconds,omitempty"`
	ExpireSeconds     int    `json:"expireSeconds,omitempty"`
	BaseURL           string `json:"baseURL,omitempty"`
	AllowInsecureHTTP bool   `json:"allowInsecureHTTP,omitempty"`
}

// Metadata is bounded but never implicitly interpolated or sent to a provider.
// IdempotencyKey is reserved for root's persisted attempt scheduler. These five
// send contracts do not provide a generic deduplication key, so it stays local.
type Message struct {
	Title          string `json:"title,omitempty"`
	Text           string `json:"text"`
	EventKind      string `json:"eventKind,omitempty"`
	MonitorName    string `json:"monitorName,omitempty"`
	MonitorID      string `json:"monitorId,omitempty"`
	IncidentID     string `json:"incidentId,omitempty"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

// Result is safe for logs: only fixed strings, bounded numbers and SHA256 IDs.
type Result struct {
	Kind         Kind          `json:"kind"`
	Status       Status        `json:"status"`
	Accepted     bool          `json:"accepted"`
	HTTPStatus   int           `json:"httpStatus,omitempty"`
	RetryAfter   time.Duration `json:"retryAfter,omitempty"`
	ProviderID   string        `json:"providerId,omitempty"`
	ErrorCode    string        `json:"errorCode,omitempty"`
	ErrorMessage string        `json:"errorMessage,omitempty"`
}

// Options are process-level trust settings, never untrusted channel options.
// AdditionalRootCAPEM augments system trust without changing OS/user trust.
type Options struct {
	Timeout             time.Duration
	AdditionalRootCAPEM []byte
}

// Send uses the shared default engine. No retry, sleep, fallback or persistence.
func Send(ctx context.Context, config Config, message Message) Result {
	return defaultEngine.Send(ctx, config, message)
}
