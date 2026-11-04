// Package notifyhttp performs one bounded native HTTP notification attempt.
// Acceptance means provider acknowledgement, never delivery to a person's device.
package notifyhttp

import (
	"context"
	"time"
)

type Kind string

const (
	Slack         Kind = "slack"
	Telegram      Kind = "telegram"
	Ntfy          Kind = "ntfy"
	Gotify        Kind = "gotify"
	Pushover      Kind = "pushover"
	HomeAssistant Kind = "homeassistant"
	Matrix        Kind = "matrix"
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
	// The bounded decoded fields can expand sixfold in Go's JSON encoding.
	MaxConfigBytes   = 32 << 10
	MaxRequestBytes  = 64 << 10
	MaxURLBytes      = 2048
	MaxTitleRunes    = 250
	MaxTextBytes     = 16 << 10
	MaxMetadataBytes = 256
)

// Config contains secrets. Root must store it privately and redact before display.
// Exactly one matching pointer is required; even an empty foreign pointer fails.
type Config struct {
	HomeAssistant *HomeAssistantConfig `json:"homeassistant,omitempty"`
	Matrix        *MatrixConfig        `json:"matrix,omitempty"`
	Kind          Kind                 `json:"kind"`
	Slack         *SlackConfig         `json:"slack,omitempty"`
	Telegram      *TelegramConfig      `json:"telegram,omitempty"`
	Ntfy          *NtfyConfig          `json:"ntfy,omitempty"`
	Gotify        *GotifyConfig        `json:"gotify,omitempty"`
	Pushover      *PushoverConfig      `json:"pushover,omitempty"`
}

type HomeAssistantConfig struct {
	BaseURL           string `json:"baseURL"`
	AccessToken       string `json:"accessToken"`
	EventType         string `json:"eventType"`
	AllowInsecureHTTP bool   `json:"allowInsecureHTTP,omitempty"`
}

// Matrix sends plaintext notices to rooms the token owner has joined.
type MatrixConfig struct {
	BaseURL           string `json:"baseURL"`
	AccessToken       string `json:"accessToken"`
	RoomID            string `json:"roomId"`
	AllowInsecureHTTP bool   `json:"allowInsecureHTTP,omitempty"`
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

// Metadata is bounded and never implicitly interpolated. Home Assistant sends
// event kind, monitor name/ID, incident ID and idempotency key as event data.
// Matrix uses IdempotencyKey as its transaction ID. The original five providers
// (Slack, Telegram, ntfy, Gotify, Pushover) send only title/text, not this metadata.
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
