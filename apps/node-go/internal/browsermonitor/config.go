// Package browsermonitor navigates a fresh context in a dedicated remote
// Chromium browser. Browser management credentials never enter probe results.
package browsermonitor

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

const (
	Type          = "real-browser"
	Unavailable   = "Browser service unavailable"
	Failed        = "Browser navigation failed"
	Loaded        = "Browser page loaded"
	MaxScreenshot = 192 << 10
)

type Options struct {
	Endpoint    string `json:"endpoint"`
	BearerToken string `json:"bearerToken,omitempty"`
	Screenshot  bool   `json:"screenshot,omitempty"`
	DelayMs     int    `json:"delayMs,omitempty"`
}

type Config struct {
	Target  string
	Timeout time.Duration
	Options
}

type Result struct {
	State      string
	Message    string
	StatusCode int
	LatencyMs  float64
	Screenshot []byte
}

var errProtocol = errors.New("browser protocol unavailable")

func webURL(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	return u, err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && len(raw) <= 4096
}

func Validate(c Config) error {
	if _, ok := webURL(c.Target); !ok || c.Timeout <= 0 || c.Timeout > 2*time.Minute || c.DelayMs < 0 || c.DelayMs > 30000 || time.Duration(c.DelayMs)*time.Millisecond >= c.Timeout {
		return errProtocol
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || len(c.Endpoint) > 2048 || u.Hostname() == "" || u.User != nil || u.Fragment != "" || strings.ContainsAny(c.Endpoint, "\r\n\x00") {
		return errProtocol
	}
	if u.Scheme != "ws" && u.Scheme != "wss" && u.Scheme != "http" && u.Scheme != "https" {
		return errProtocol
	}
	if (u.Scheme == "http" || u.Scheme == "https") && (u.Path != "" && u.Path != "/" || u.RawQuery != "") {
		return errProtocol
	}
	if len(c.BearerToken) > 4096 || strings.ContainsAny(c.BearerToken, "\r\n\x00") {
		return errProtocol
	}
	for _, ch := range c.BearerToken {
		if ch < 33 || ch > 126 {
			return errProtocol
		}
	}
	return nil
}
