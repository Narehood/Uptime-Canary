// Package icmp runs the system ping utility with validated, bounded options.
package icmp

import (
	"context"
	"errors"
	"net"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	Up      = "up"
	Down    = "down"
	Unknown = "unknown"
)

// Config describes one check. Timeout is the budget for the entire check,
// including name resolution and all packets. Count zero means one. A nil Size
// means 32 data bytes; a non-nil Size can explicitly request zero bytes.
type Config struct {
	Target  string
	Timeout time.Duration
	Count   int
	Size    *int
	Family  string
}

// Result never contains subprocess output or configured values. LatencyMs is
// meaningful only when LatencyAvailable is true; it is never process wall time.
type Result struct {
	State            string
	LatencyMs        float64
	LatencyAvailable bool
	Message          string
}

// Validate applies the same ASCII hostname/IP grammar as Canary's ping target
// validator, plus bounds for this engine's options. It performs no DNS lookup.
func Validate(c Config) error {
	if !validTarget(c.Target) {
		return errors.New("ICMP target must be a hostname or IP address")
	}
	if c.Timeout <= 0 || c.Timeout > 120*time.Second {
		return errors.New("ICMP timeout must be positive and at most 120 seconds")
	}
	if c.Count < 0 || c.Count > 10 {
		return errors.New("ICMP count must be between 1 and 10, or zero for default")
	}
	if c.Size != nil && (*c.Size < 0 || *c.Size > 65500) {
		return errors.New("ICMP size must be between 0 and 65500 bytes")
	}
	if c.Family != "" && c.Family != "auto" && c.Family != "ipv4" && c.Family != "ipv6" {
		return errors.New("ICMP family must be auto, ipv4, or ipv6")
	}
	if ip := net.ParseIP(c.Target); ip != nil {
		if c.Family == "ipv4" && ip.To4() == nil || c.Family == "ipv6" && ip.To4() != nil {
			return errors.New("ICMP literal address does not match family")
		}
	}
	return nil
}

func validTarget(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	if net.ParseIP(s) != nil {
		return true
	}
	for _, label := range strings.Split(strings.TrimSuffix(s, "."), ".") {
		if len(label) == 0 || len(label) > 63 || !alphaNumeric(label[0]) || !alphaNumeric(label[len(label)-1]) {
			return false
		}
		for i := range len(label) {
			if !alphaNumeric(label[i]) && label[i] != '-' {
				return false
			}
		}
	}
	return true
}

func alphaNumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func normalized(c Config) Config {
	if c.Count == 0 {
		c.Count = 1
	}
	size := 32
	if c.Size != nil {
		size = *c.Size
	}
	// Copy the pointed value so execution does not retain the caller's pointer.
	c.Size = &size
	if c.Family == "" {
		c.Family = "auto"
	}
	return c
}

func ceilUnits(d, unit time.Duration) string {
	return strconv.FormatInt(int64((d+unit-1)/unit), 10)
}

// Arguments produces only engine-owned switches and one validated destination.
// Platforms are linux (BusyBox fancy ping or iputils), windows, and darwin
// (IPv4 only). Call Check for the independent whole-check deadline enforcement.
func Arguments(c Config, platform string) ([]string, error) {
	if err := Validate(c); err != nil {
		return nil, err
	}
	c = normalized(c)
	count, size := strconv.Itoa(c.Count), strconv.Itoa(*c.Size)
	var args []string
	switch platform {
	case "linux":
		args = []string{"-n", "-c", count, "-s", size, "-i", "0.2", "-W", ceilUnits(c.Timeout, time.Second), "-w", ceilUnits(c.Timeout, time.Second)}
	case "windows":
		args = []string{"-n", count, "-l", size, "-w", ceilUnits(c.Timeout, time.Millisecond)}
	case "darwin":
		if c.Family == "ipv6" || strings.Contains(c.Target, ":") {
			return nil, errors.New("ICMP IPv6 is unsupported on Darwin")
		}
		// Retain the portable, unprivileged one-second Darwin spacing.
		args = []string{"-n", "-c", count, "-s", size, "-i", "1", "-W", ceilUnits(c.Timeout, time.Millisecond), "-t", ceilUnits(c.Timeout, time.Second)}
	default:
		return nil, errors.New("ICMP platform is unsupported")
	}
	if platform != "darwin" {
		switch c.Family {
		case "ipv4":
			args = append(args, "-4")
		case "ipv6":
			args = append(args, "-6")
		}
	}
	if platform != "windows" {
		args = append(args, "--")
	}
	return append(args, c.Target), nil
}

// Check reports up after at least one verified reply, even with packet loss or
// a configured deadline. Caller cancellation and local failures are unknown.
// No reply alone is insufficient evidence of down: a parsed loss/timeout or
// unreachable report is required. See README.md for platform/parser limits.
func Check(ctx context.Context, c Config) Result {
	return check(ctx, c, runtime.GOOS, execute)
}

type runner func(context.Context, []string) execution

func unknown(message string) Result { return Result{State: Unknown, Message: message} }

func check(ctx context.Context, c Config, platform string, run runner) Result {
	if ctx == nil {
		return unknown("ICMP context is required")
	}
	c = normalized(c)
	args, err := Arguments(c, platform)
	if err != nil {
		return unknown(err.Error())
	}
	if ctx.Err() != nil {
		return unknown("ICMP check canceled")
	}
	budget, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	e := run(budget, args)
	if ctx.Err() != nil {
		return unknown("ICMP check canceled")
	}
	if !e.started {
		return unknown("ICMP system ping could not start")
	}
	if e.overflow {
		return unknown("ICMP output exceeded limit")
	}
	if localFailure(e.stdout, e.stderr, platform) {
		return unknown("ICMP system ping could not send reliably")
	}
	p := parseOutput(e.stdout, platform)
	if p.replies {
		r := Result{State: Up, LatencyMs: p.avg, LatencyAvailable: p.latency, Message: "ICMP reply"}
		if !p.latency {
			r.Message = "ICMP reply; RTT unavailable"
		}
		return r
	}
	// Errors other than normal no-reply exit status must not become target down.
	if (e.err == nil || e.exitCode == 1 || budget.Err() != nil) && p.noReply {
		return Result{State: Down, Message: "ICMP no reply"}
	}
	if budget.Err() != nil {
		return unknown("ICMP deadline expired without a verifiable result")
	}
	return unknown("ICMP system ping result is unrecognized")
}
