// Package tailscalemonitor checks a peer through the installed local Tailscale
// client. It never starts a daemon, changes configuration or runs a shell.
package tailscalemonitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"uptime-canary/apps/node-go/internal/probeworker/process"
)

const Type = "tailscale-ping"

const (
	Replied     = "Tailscale peer replied"
	NoReply     = "Tailscale peer did not reply"
	Unavailable = "Tailscale peer status unavailable"
	Invalid     = "invalid Tailscale ping configuration"
)

type Config struct {
	Name    string        `json:"name"`
	Timeout time.Duration `json:"timeout"`
}

var errConfig = errors.New(Invalid)

// Validate accepts one literal IP or ASCII DNS/MagicDNS name, never flags,
// command syntax, URLs, credentials, ports, paths or zone-qualified addresses.
func Validate(c Config) error {
	if c.Timeout < 0 || c.Timeout > 2*time.Minute || c.Name == "" || len(c.Name) > 253 || !utf8.ValidString(c.Name) {
		return errConfig
	}
	if ip, err := netip.ParseAddr(c.Name); err == nil {
		if ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() {
			return errConfig
		}
		return nil
	}
	name := strings.TrimSuffix(c.Name, ".")
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errConfig
		}
		for _, ch := range []byte(label) {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-') {
				return errConfig
			}
		}
	}
	return nil
}

func unavailable() process.Result { return process.Result{State: "pending", Message: Unavailable} }

// Check only sends the closed worker request. There is no node-process or
// ordinary ICMP fallback if the client, worker or daemon is unavailable.
func Check(ctx context.Context, c Config) process.Result {
	if Validate(c) != nil {
		return process.Result{State: "down", Message: Invalid}
	}
	if c.Timeout == 0 {
		c.Timeout = 5 * time.Second
	}
	return workerResult(process.RunConfig(ctx, c.Timeout, struct {
		Type string `json:"type"`
		Config
	}{Type, c}))
}

func workerResult(r process.Result) process.Result {
	if r.State == "pending" || r.State == "down" && (r.Message == "service check timed out" || r.Message == "service check canceled") {
		return unavailable()
	}
	return r
}

type commandResult struct {
	stdout, stderr []byte
	exit           int
	err            error
}

// The per-ping budget leaves time inside the existing total deadline for client
// startup and its final result. Neither daemon startup nor reconnect is allowed.
func arguments(c Config) []string {
	return []string{"ping", "--c=1", "--until-direct=false", "--timeout=" + (c.Timeout * 3 / 4).String(), "--", c.Name}
}

func check(ctx context.Context, c Config, run func(context.Context, []string) commandResult) process.Result {
	if Validate(c) != nil {
		return process.Result{State: "down", Message: Invalid}
	}
	if c.Timeout == 0 {
		c.Timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	if ctx.Err() != nil {
		return unavailable()
	}
	raw := run(ctx, arguments(c))
	if ctx.Err() != nil || raw.err != nil {
		return unavailable()
	}
	return parseResult(raw, c.Timeout)
}

var pong = regexp.MustCompile(`^pong from ([^\r\n()]+) \(([^\r\n()]+)\) via ([^\r\n]+) in ([0-9]+(?:\.[0-9]+)?(?:ns|us|µs|ms|s))$`)

// Admit only the one-ping output profile from Tailscale's own CLI. Daemon,
// authorization, local-target and unknown-version diagnostics remain Pending.
// Names, peer addresses, relay details and raw errors never leave this package.
func parseResult(raw commandResult, timeout time.Duration) process.Result {
	if len(raw.stdout) == 0 || len(raw.stdout) > 4096 || len(raw.stderr) > 4096 || !utf8.Valid(raw.stdout) || !utf8.Valid(raw.stderr) {
		return unavailable()
	}
	text := strings.TrimSuffix(strings.ReplaceAll(string(raw.stdout), "\r\n", "\n"), "\n")
	if strings.ContainsAny(text, "\x00\r\n") {
		return unavailable()
	}
	for _, ch := range text {
		if unicode.IsControl(ch) {
			return unavailable()
		}
	}
	if raw.exit == 0 && len(raw.stderr) == 0 {
		match := pong.FindStringSubmatch(text)
		if match == nil {
			return unavailable()
		}
		address, port, hasPort := strings.Cut(match[2], ", ")
		if ip, err := netip.ParseAddr(address); err != nil || ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() {
			return unavailable()
		}
		if hasPort {
			v, err := strconv.Atoi(port)
			if err != nil || v < 1 || v > 65535 {
				return unavailable()
			}
		}
		elapsed, err := time.ParseDuration(match[4])
		if err != nil || elapsed < 0 || elapsed > timeout {
			return unavailable()
		}
		return process.Result{State: "up", Message: Replied, LatencyMs: float64(elapsed) / float64(time.Millisecond)}
	}
	if raw.exit == 1 && strings.TrimSuffix(strings.ReplaceAll(string(raw.stderr), "\r\n", "\n"), "\n") == "no reply" {
		address, ok := strings.CutPrefix(text, `ping "`)
		address, end := strings.CutSuffix(address, `" timed out`)
		if ip, err := netip.ParseAddr(address); ok && end && err == nil && ip.Zone() == "" && !ip.IsUnspecified() && !ip.IsMulticast() {
			return process.Result{State: "down", Message: NoReply}
		}
	}
	return unavailable()
}

func decode(raw []byte) (Config, error) {
	var c Config
	if len(raw) > 2048 {
		return c, errConfig
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return c, errConfig
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] || key != "type" && key != "name" && key != "timeout" {
			return c, errConfig
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return c, errConfig
		}
		switch key {
		case "type":
			var kind string
			if json.Unmarshal(value, &kind) != nil || kind != Type {
				return c, errConfig
			}
		case "name":
			err = json.Unmarshal(value, &c.Name)
		case "timeout":
			err = json.Unmarshal(value, &c.Timeout)
		}
		if err != nil {
			return c, errConfig
		}
	}
	last, err := d.Token()
	if err != nil || last != json.Delim('}') || len(seen) != 3 || c.Timeout <= 0 || Validate(c) != nil {
		return c, errConfig
	}
	if _, err := d.Token(); err != io.EOF {
		return c, errConfig
	}
	return c, nil
}

// WorkerHandler has no executable/socket/environment option. Production only
// selects installed platform paths, and failed validation clears prior state.
func WorkerHandler() process.Handler {
	var c Config
	valid := false
	return process.Handler{
		Validate: func(raw []byte) error {
			valid = false
			var err error
			c, err = decode(raw)
			valid = err == nil
			return err
		},
		Check: func(ctx context.Context) process.Result {
			if !valid {
				return process.Result{State: "down", Message: Invalid}
			}
			return check(ctx, c, platformRun)
		},
	}
}
