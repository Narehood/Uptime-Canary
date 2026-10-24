// Package process runs bounded JSON requests in disposable, resource-limited
// copies of the current executable. It has no service engine dependency.
package process

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// ModeArgument is a private CLI protocol, never a user-selected program.
	ModeArgument    = "--canary-probe-worker-v1"
	hardMemoryBytes = 128 << 20
	softMemoryBytes = 64 << 20
	maxInputBytes   = 8 << 20
	maxOutputBytes  = 64 << 10 // independently for stdout and stderr
	maxFrameBytes   = 2048
	maxMessageBytes = 256
	frameMagic      = "\x1eCANARY-PROBE/1 "
)

var errProtocol = errors.New("invalid worker protocol")

type request struct {
	Version  int             `json:"version"`
	Nonce    string          `json:"nonce"`
	Deadline int64           `json:"deadline"`
	Config   json.RawMessage `json:"config"`
}

// Result contains only validated status and fixed diagnostics, never log text.
type Result struct {
	State     string  `json:"state"`
	Message   string  `json:"message"`
	LatencyMs float64 `json:"latencyMs"`
}

type configHeader struct {
	Type    string        `json:"type"`
	Timeout time.Duration `json:"timeout"`
}

// EncodeConfig bounds ordinary typed configuration data before JSON allocation.
// It does not invoke an engine validator or accept executable configuration.
func EncodeConfig(config any) ([]byte, error) {
	budget := maxInputBytes - 1024
	if !encodedBound(reflect.ValueOf(config), &budget, 0) {
		return nil, errProtocol
	}
	b, err := json.Marshal(config)
	if err != nil || len(b) > maxInputBytes-1024 {
		return nil, errProtocol
	}
	return b, nil
}

type frame struct {
	Kind   string  `json:"kind"`
	Code   string  `json:"code,omitempty"`
	Result *Result `json:"result,omitempty"`
}

func decodeStrict(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errProtocol
	}
	// Valid additionally checks there is exactly one JSON value.
	if !json.Valid(b) {
		return errProtocol
	}
	return nil
}

func validNonce(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, ch := range s {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f') {
			return false
		}
	}
	return true
}

// encodedBound refuses large graphs before json.Marshal can allocate its
// buffer. Config's typed fields contain no recursive or interface values.
// The deliberately conservative estimate includes JSON escaping and base64.
func encodedBound(v reflect.Value, budget *int, depth int) bool {
	if depth > 32 {
		return false
	}
	*budget -= 128
	if *budget < 0 {
		return false
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return v.IsNil() || encodedBound(v.Elem(), budget, depth+1)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !encodedBound(v.Field(i), budget, depth+1) {
				return false
			}
		}
	case reflect.String:
		if v.Len() > *budget/6 {
			return false
		}
		*budget -= v.Len() * 6
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			if v.Len() > *budget/6 {
				return false
			}
			*budget -= v.Len() * 6
		} else {
			if v.Len() > *budget/128 {
				return false
			}
			for i := 0; i < v.Len(); i++ {
				if !encodedBound(v.Index(i), budget, depth+1) {
					return false
				}
			}
		}
	case reflect.Map:
		if v.Len() > *budget/256 {
			return false
		}
		i := v.MapRange()
		for i.Next() {
			if !encodedBound(i.Key(), budget, depth+1) || !encodedBound(i.Value(), budget, depth+1) {
				return false
			}
		}
	}
	return true
}

func validResult(r Result, timeout time.Duration) bool {
	if r.State != "up" && r.State != "down" {
		return false
	}
	if math.IsNaN(r.LatencyMs) || math.IsInf(r.LatencyMs, 0) || r.LatencyMs < 0 || r.LatencyMs > float64(timeout)/float64(time.Millisecond) {
		return false
	}
	if len(r.Message) == 0 || len(r.Message) > maxMessageBytes || !utf8.ValidString(r.Message) {
		return false
	}
	for _, ch := range r.Message {
		if unicode.IsControl(ch) {
			return false
		}
	}
	return true
}

// System-service is the sole engine whose manager can explicitly return
// Pending. Its messages are a closed set, never manager-provided diagnostics.
// Existing network/query engines retain their two-state result contract.
func validTypedResult(serviceType string, r Result, timeout time.Duration) bool {
	if serviceType != "system-service" {
		return validResult(r, timeout)
	}
	if !systemServiceSummary(r) {
		return false
	}
	if r.State == "pending" {
		r.State = "down" // Reuse the existing scalar/size/time validation only.
	}
	return validResult(r, timeout)
}

func systemServiceSummary(r Result) bool {
	return r.State == "up" && r.Message == "Service is running" ||
		r.State == "down" && (r.Message == "Service is not running" || r.Message == "Service is not installed") ||
		r.State == "pending" && r.Message == "Service status unavailable"
}

// Never return arbitrary child text, even when it is valid printable UTF-8.
// Oracle's diagnostics are fixed strings in the pinned service engine. Other
// engines retain their real state but use fixed summaries at this boundary.
func safeMessage(t string, r Result) string {
	if t == "system-service" && systemServiceSummary(r) {
		return r.Message
	}
	if t == "oracledb" {
		if r.State == "up" && r.Message == "Oracle monitoring query succeeded" {
			return r.Message
		}
		if r.State == "down" {
			switch r.Message {
			case "invalid service configuration", "invalid Oracle configuration", "invalid Oracle TLS configuration",
				"Oracle driver rejected a malformed reply", "Oracle connection, TLS, or authentication failed",
				"Oracle monitoring query failed", "Oracle result columns invalid or exceed limit",
				"Oracle query failed or malformed reply", "Oracle query result exceeds limit",
				"Oracle returned an unsupported scalar type", "Oracle returned multiple result sets",
				"Oracle query returned no rows", "Oracle scalar condition did not match",
				"service check canceled", "service check timed out":
				return r.Message
			}
		}
	}
	if r.State == "up" {
		return "service check succeeded"
	}
	return "service check failed"
}

// streamSink discards incidental text, retaining only a small candidate line.
// Returning an error alone does not stop exec's process, so overflow/protocol
// errors also cancel it. Both streams have a strict total byte budget.
type streamSink struct {
	mu          sync.Mutex
	prefix      string
	timeout     time.Duration
	serviceType string
	stop        func()
	total       int
	line        []byte
	dropping    bool
	failed      bool
	started     bool
	final       *frame
}

func (s *streamSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed || len(p) > maxOutputBytes-s.total {
		return 0, s.fail()
	}
	s.total += len(p)
	if s.prefix == "" {
		return len(p), nil
	} // stderr: never retained
	for _, b := range p {
		if b == '\n' {
			if !s.dropping && bytes.HasPrefix(s.line, []byte(s.prefix)) {
				if !s.accept(s.line[len(s.prefix):]) {
					return 0, s.fail()
				}
			}
			s.line = s.line[:0]
			s.dropping = false
		} else if !s.dropping {
			if len(s.line) == maxFrameBytes {
				if bytes.HasPrefix(s.line, []byte(s.prefix)) {
					return 0, s.fail()
				}
				s.line = s.line[:0]
				s.dropping = true
			} else {
				s.line = append(s.line, b)
			}
		}
	}
	return len(p), nil
}

func (s *streamSink) fail() error {
	s.failed = true
	if s.stop != nil {
		s.stop()
	}
	return errProtocol
}

func (s *streamSink) accept(b []byte) bool {
	var f frame
	if decodeStrict(b, &f) != nil || s.final != nil {
		return false
	}
	switch f.Kind {
	case "started":
		if s.started || f.Result != nil || f.Code != "" {
			return false
		}
		s.started = true
	case "result":
		if !s.started || f.Code != "" || f.Result == nil || !validTypedResult(s.serviceType, *f.Result, s.timeout) {
			return false
		}
		if f.Result.Message != safeMessage(s.serviceType, *f.Result) {
			return false
		}
		s.final = &f
	case "error":
		if s.started || f.Result != nil || f.Code != "invalid-config" {
			return false
		}
		s.final = &f
	default:
		return false
	}
	return true
}

func (s *streamSink) snapshot() (bool, bool, *frame) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed, s.started, s.final
}

// EOF is required as well as a final frame. A truncated nonce-matching frame
// after an otherwise valid result must not evade the ordered-frame validator.
func (s *streamSink) finish() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prefix != "" && !s.dropping && bytes.HasPrefix(s.line, []byte(s.prefix)) {
		return s.fail()
	}
	return nil
}

func childEnvironment() []string {
	// No PATH, HOME, driver variables, proxies, GODEBUG, node administration,
	// cluster tokens, or arbitrary inherited environment entries.
	return environmentFromLookup(os.LookupEnv)
}

func environmentFromLookup(lookup func(string) (string, bool)) []string {
	out := []string{"GOMAXPROCS=1", "GOMEMLIMIT=64MiB", "GOTRACEBACK=none"}
	for _, key := range []string{"SystemRoot", "WINDIR", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if v, ok := lookup(key); ok && len(v) <= 4096 && !strings.ContainsRune(v, 0) {
			out = append(out, key+"="+v)
		}
	}
	return out
}
