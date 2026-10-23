package services

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"
)

type RabbitMQConfig struct {
	Mode           string     `json:"mode"`
	Address        string     `json:"address,omitempty"`
	Username       string     `json:"username"`
	Password       string     `json:"password"`
	VirtualHost    string     `json:"virtualHost,omitempty"`
	TLSMode        string     `json:"tlsMode,omitempty"`
	TLS            *TLSConfig `json:"tls,omitempty"`
	ManagementURLs []string   `json:"managementUrls,omitempty"`
}

func rabbitManagementURL(raw string, c RabbitMQConfig) (*url.URL, error) {
	bad := errors.New("invalid RabbitMQ management URL")
	u, e := url.Parse(raw)
	if e != nil || len(raw) > 2048 || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" {
		return nil, bad
	}
	if (u.Scheme != "https" && c.TLSMode != "plain") || (u.Scheme != "http" && c.TLSMode == "plain") {
		return nil, errors.New("RabbitMQ management URL scheme disagrees with TLS mode")
	}
	if u.Path != "" && path.Clean(u.Path) != strings.TrimSuffix(u.Path, "/") && u.Path != "/" {
		return nil, bad
	}
	port := u.Port()
	if port == "" {
		port = "443"
		if u.Scheme == "http" {
			port = "80"
		}
	}
	if validAddress(net.JoinHostPort(u.Hostname(), port)) != nil {
		return nil, bad
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/api/health/checks/alarms/"
	return u, nil
}

func (c RabbitMQConfig) validate() error {
	if len(c.Username) == 0 || len(c.Username) > 255 || len(c.Password) > 4096 || strings.ContainsRune(c.Username, 0) || strings.ContainsRune(c.Password, 0) || !utf8.ValidString(c.Username) || !utf8.ValidString(c.Password) {
		return errors.New("invalid RabbitMQ credentials")
	}
	if e := validMode(c.TLSMode, c.TLS); e != nil {
		return e
	}
	switch c.Mode {
	case "amqp":
		if e := validAddress(c.Address); e != nil {
			return e
		}
		if len(c.ManagementURLs) > 0 || len(c.VirtualHost) > 255 || strings.ContainsRune(c.VirtualHost, 0) || !utf8.ValidString(c.VirtualHost) {
			return errors.New("invalid RabbitMQ AMQP options")
		}
		h, _, _ := net.SplitHostPort(c.Address)
		_, e := tlsSettings(c.TLS, h)
		return e
	case "management":
		if c.Address != "" || c.VirtualHost != "" || len(c.ManagementURLs) == 0 || len(c.ManagementURLs) > 8 || strings.Contains(c.Username, ":") {
			return errors.New("invalid RabbitMQ management options")
		}
		for _, raw := range c.ManagementURLs {
			u, e := rabbitManagementURL(raw, c)
			if e != nil {
				return e
			}
			if _, e = tlsSettings(c.TLS, u.Hostname()); e != nil {
				return e
			}
		}
		return nil
	default:
		return errors.New("RabbitMQ mode must be amqp or management")
	}
}

func checkRabbitMQ(ctx context.Context, c RabbitMQConfig) Result {
	if c.Mode == "management" {
		return checkRabbitManagement(ctx, c)
	}
	return checkRabbitAMQP(ctx, c)
}

func checkRabbitManagement(ctx context.Context, c RabbitMQConfig) Result {
	for _, raw := range c.ManagementURLs {
		if ctx.Err() != nil {
			break
		}
		u, _ := rabbitManagementURL(raw, c)
		tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 16 << 10, DialContext: dial}
		tr.TLSClientConfig, _ = tlsSettings(c.TLS, u.Hostname())
		client := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if e != nil {
			tr.CloseIdleConnections()
			continue
		}
		req.SetBasicAuth(c.Username, c.Password)
		req.Header.Set("Accept", "application/json")
		resp, e := client.Do(req)
		if e != nil {
			tr.CloseIdleConnections()
			continue
		}
		body, e := boundedRead(resp.Body, 64<<10)
		resp.Body.Close()
		tr.CloseIdleConnections()
		if e != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		var status struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(body, &status) == nil && status.Status == "ok" {
			return up("RabbitMQ management alarm check passed")
		}
	}
	return down("RabbitMQ management alarm checks failed")
}

const rabbitMaxFrame = 64 << 10

func amqpLongString(p []byte, s string) []byte {
	p = binary.BigEndian.AppendUint32(p, uint32(len(s)))
	return append(p, s...)
}
func amqpShortString(p []byte, s string) []byte { p = append(p, byte(len(s))); return append(p, s...) }

func writeAMQPMethod(w io.Writer, method uint16, args []byte) error {
	p := []byte{1, 0, 0}
	p = binary.BigEndian.AppendUint32(p, uint32(4+len(args)))
	p = binary.BigEndian.AppendUint16(p, 10)
	p = binary.BigEndian.AppendUint16(p, method)
	p = append(p, args...)
	p = append(p, 0xce)
	for len(p) > 0 {
		n, e := w.Write(p)
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}

// Frame size is checked before allocation, independent of server tune values.
func readAMQPMethod(r io.Reader, expected uint16, frameMax uint32) ([]byte, error) {
	bad := errors.New("AMQP method rejected")
	for range 16 {
		var header [7]byte
		if _, e := io.ReadFull(r, header[:]); e != nil {
			return nil, e
		}
		n := binary.BigEndian.Uint32(header[3:])
		channel := binary.BigEndian.Uint16(header[1:3])
		if n+8 < n || n+8 > frameMax || n > rabbitMaxFrame-8 || channel != 0 {
			return nil, bad
		}
		p := make([]byte, int(n)+1)
		if _, e := io.ReadFull(r, p); e != nil {
			return nil, e
		}
		if p[len(p)-1] != 0xce {
			return nil, bad
		}
		if header[0] == 8 && n == 0 {
			continue
		}
		if header[0] != 1 || n < 4 || binary.BigEndian.Uint16(p) != 10 || binary.BigEndian.Uint16(p[2:]) != expected {
			return nil, bad
		}
		return p[4 : len(p)-1], nil
	}
	return nil, bad
}

type amqpReader struct {
	p   []byte
	pos int
}

func (r *amqpReader) take(n int) ([]byte, bool) {
	if n < 0 || n > len(r.p)-r.pos {
		return nil, false
	}
	p := r.p[r.pos : r.pos+n]
	r.pos += n
	return p, true
}
func (r *amqpReader) long() ([]byte, bool) {
	p, ok := r.take(4)
	if !ok {
		return nil, false
	}
	n := binary.BigEndian.Uint32(p)
	if uint64(n) > uint64(len(r.p)-r.pos) {
		return nil, false
	}
	return r.take(int(n))
}

// Validate server field-table framing with bounded depth and element count.
// Values are discarded; no advertised product/capability alters this probe.
func amqpTable(p []byte, depth int, count *int) bool {
	if depth > 8 {
		return false
	}
	r := amqpReader{p: p}
	for r.pos < len(p) {
		*count++
		if *count > 256 {
			return false
		}
		n, ok := r.take(1)
		if !ok || n[0] == 0 {
			return false
		}
		name, ok := r.take(int(n[0]))
		if !ok || !utf8.Valid(name) {
			return false
		}
		if !amqpValue(&r, depth, count) {
			return false
		}
	}
	return true
}
func amqpValue(r *amqpReader, depth int, count *int) bool {
	t, ok := r.take(1)
	if !ok {
		return false
	}
	n := 0
	switch t[0] {
	case 't', 'b', 'B':
		n = 1
	case 's', 'U', 'u':
		n = 2
	case 'I', 'i', 'f':
		n = 4
	case 'L', 'l', 'd', 'T':
		n = 8
	case 'D':
		n = 5
	case 'V':
		return true
	case 'S', 'x':
		_, ok := r.long()
		return ok
	case 'F':
		p, ok := r.long()
		return ok && amqpTable(p, depth+1, count)
	case 'A':
		if depth >= 8 {
			return false
		}
		p, ok := r.long()
		if !ok {
			return false
		}
		a := amqpReader{p: p}
		for a.pos < len(p) {
			*count++
			if *count > 256 || !amqpValue(&a, depth+1, count) {
				return false
			}
		}
		return true
	default:
		return false
	}
	_, ok = r.take(n)
	return ok
}

func checkRabbitAMQP(ctx context.Context, c RabbitMQConfig) Result {
	conn, e := dialService(ctx, c.Address, c.TLSMode, c.TLS)
	if e != nil {
		return down("RabbitMQ AMQP connection failed")
	}
	defer conn.Close()
	if n, e := conn.Write([]byte{'A', 'M', 'Q', 'P', 0, 0, 9, 1}); e != nil || n != 8 {
		return down("RabbitMQ AMQP protocol request failed")
	}
	start, e := readAMQPMethod(conn, 10, rabbitMaxFrame)
	if e != nil || len(start) < 2 || start[0] != 0 || start[1] != 9 {
		return down("RabbitMQ AMQP start rejected")
	}
	r := amqpReader{p: start, pos: 2}
	properties, ok := r.long()
	count := 0
	if !ok || !amqpTable(properties, 0, &count) {
		return down("RabbitMQ AMQP server properties rejected")
	}
	mechanisms, ok := r.long()
	if !ok || len(mechanisms) > 1024 {
		return down("RabbitMQ AMQP authentication mechanisms rejected")
	}
	locales, ok := r.long()
	if !ok || len(locales) > 256 || r.pos != len(start) {
		return down("RabbitMQ AMQP locales rejected")
	}
	if !amqpToken(mechanisms, "PLAIN") || !amqpToken(locales, "en_US") {
		return down("RabbitMQ AMQP requires PLAIN and en_US")
	}
	args := []byte{0, 0, 0, 0} // empty client field table
	args = amqpShortString(args, "PLAIN")
	args = amqpLongString(args, "\x00"+c.Username+"\x00"+c.Password)
	args = amqpShortString(args, "en_US")
	if writeAMQPMethod(conn, 11, args) != nil {
		return down("RabbitMQ AMQP authentication request failed")
	}
	tune, e := readAMQPMethod(conn, 30, rabbitMaxFrame)
	if e != nil || len(tune) != 8 {
		return down("RabbitMQ AMQP authentication or tuning rejected")
	}
	frameMax := binary.BigEndian.Uint32(tune[2:6])
	if frameMax != 0 && frameMax < 4096 {
		return down("RabbitMQ AMQP frame limit rejected")
	}
	if frameMax == 0 || frameMax > rabbitMaxFrame {
		frameMax = rabbitMaxFrame
	}
	args = binary.BigEndian.AppendUint16(nil, 1) // at most one channel (none is opened)
	args = binary.BigEndian.AppendUint32(args, frameMax)
	args = append(args, tune[6:8]...)
	if writeAMQPMethod(conn, 31, args) != nil {
		return down("RabbitMQ AMQP tune request failed")
	}
	vhost := c.VirtualHost
	if vhost == "" {
		vhost = "/"
	}
	args = amqpShortString(nil, vhost)
	args = append(args, 0, 0) // capabilities, insist
	if writeAMQPMethod(conn, 40, args) != nil {
		return down("RabbitMQ AMQP open request failed")
	}
	opened, e := readAMQPMethod(conn, 41, frameMax)
	if e != nil || len(opened) < 1 || int(opened[0]) != len(opened)-1 {
		return down("RabbitMQ AMQP virtual host open rejected")
	}
	args = binary.BigEndian.AppendUint16(nil, 200)
	args = amqpShortString(args, "monitor complete")
	args = append(args, 0, 0, 0, 0)
	if writeAMQPMethod(conn, 50, args) != nil {
		return down("RabbitMQ AMQP close request failed")
	}
	closed, e := readAMQPMethod(conn, 51, frameMax)
	if e != nil || len(closed) != 0 {
		return down("RabbitMQ AMQP close response rejected")
	}
	return up("RabbitMQ AMQP authenticated virtual host connection passed")
}
func amqpToken(p []byte, token string) bool {
	for _, part := range bytes.Split(p, []byte{' '}) {
		if string(part) == token {
			return true
		}
	}
	return false
}
