package services

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sipFixtureReply(request []byte, status int, mutate func(map[string]string)) ([]byte, error) {
	lines := strings.Split(string(request), "\r\n")
	if len(lines) < 8 || !strings.HasPrefix(lines[0], "OPTIONS ") || !strings.HasSuffix(lines[0], " SIP/2.0") {
		return nil, errors.New("invalid OPTIONS request")
	}
	h := map[string]string{}
	for _, line := range lines[1:] {
		k, v, ok := strings.Cut(line, ":")
		if ok {
			h[strings.ToLower(k)] = strings.TrimSpace(v)
		}
	}
	if h["content-length"] != "0" || !strings.HasSuffix(h["cseq"], " OPTIONS") || !strings.Contains(h["via"], ";branch=z9hG4bK") || !strings.Contains(h["from"], ";tag=") {
		return nil, errors.New("missing transaction fields")
	}
	h["to"] += ";tag=fixture-server"
	if mutate != nil {
		mutate(h)
	}
	return []byte(fmt.Sprintf("SIP/2.0 %d fixture-secret\r\nVia: %s\r\nFrom: %s\r\nTo: %s\r\nCall-ID: %s\r\nCSeq: %s\r\nContent-Length: 0\r\n\r\n", status, h["via"], h["from"], h["to"], h["call-id"], h["cseq"])), nil
}
func sipReadRequest(conn net.Conn) ([]byte, error) {
	r := bufio.NewReader(conn)
	var b strings.Builder
	for b.Len() < 8192 {
		line, e := r.ReadString('\n')
		if e != nil {
			return nil, e
		}
		b.WriteString(line)
		if line == "\r\n" {
			return []byte(b.String()), nil
		}
	}
	return nil, errors.New("request exceeded fixture limit")
}

func TestSIPLocalUDPAndTCPAndTLS(t *testing.T) {
	for _, transport := range []string{"udp", "tcp", "tls", "mtls"} {
		t.Run(transport, func(t *testing.T) {
			c := SIPConfig{Transport: transport}
			if transport == "udp" {
				c.Address = udpServiceFixture(t, func(p []byte) ([]byte, error) { return sipFixtureReply(p, 200, nil) })
			} else {
				var server *tls.Config
				if transport == "tls" {
					server, c.TLS = testTLS(t)
				}
				if transport == "mtls" {
					server, c.TLS = specialistMTLS(t)
					c.Transport = "tls"
				}
				c.Address = fixture(t, func(raw net.Conn) error {
					conn := raw
					if server != nil {
						conn = tls.Server(raw, server)
					}
					request, e := sipReadRequest(conn)
					if e != nil {
						return e
					}
					// Coalesced provisional and final responses exercise stream framing.
					trying, e := sipFixtureReply(request, 100, nil)
					if e != nil {
						return e
					}
					ok, e := sipFixtureReply(request, 200, nil)
					if e != nil {
						return e
					}
					wire := append(trying, ok...)
					// Fragment the status line and headers across multiple socket writes.
					for len(wire) > 0 {
						n := 7
						if len(wire) < n {
							n = len(wire)
						}
						if _, e = conn.Write(wire[:n]); e != nil {
							return e
						}
						wire = wire[n:]
					}
					return nil
				})
			}
			expectState(t, Config{Type: SIP, Timeout: time.Second, SIP: &c}, "up")
		})
	}
}

func TestSIPRejectsInvalidTransactionAndResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]string)
		raw    func([]byte) []byte
		status int
	}{
		{"branch", func(h map[string]string) { h["via"] = strings.Replace(h["via"], "z9hG4bK", "wrong-", 1) }, nil, 200},
		{"sentBy", func(h map[string]string) { h["via"] = strings.Replace(h["via"], "127.0.0.1", "127.0.0.2", 1) }, nil, 200},
		{"callID", func(h map[string]string) { h["call-id"] = "fixture-secret" }, nil, 200},
		{"cseq", func(h map[string]string) { h["cseq"] = "42 OPTIONS" }, nil, 200},
		{"method", func(h map[string]string) { h["cseq"] = strings.Replace(h["cseq"], "OPTIONS", "INVITE", 1) }, nil, 200},
		{"fromTag", func(h map[string]string) { h["from"] = "<sip:canary@localhost>;tag=wrong" }, nil, 200},
		{"toURI", func(h map[string]string) { h["to"] = "<sip:wrong@localhost>;tag=server" }, nil, 200},
		{"missingToTag", func(h map[string]string) { h["to"], _, _ = strings.Cut(h["to"], ";tag=") }, nil, 200},
		{"unauthorized", nil, nil, 401}, {"proxyAuth", nil, nil, 407}, {"failure", nil, nil, 503},
		{"badLength", nil, func(p []byte) []byte {
			return []byte(strings.Replace(string(p), "Content-Length: 0", "Content-Length: 9000", 1))
		}, 200},
		{"duplicateLength", nil, func(p []byte) []byte {
			return []byte(strings.Replace(string(p), "Content-Length: 0", "Content-Length: 0\r\nContent-Length: 0", 1))
		}, 200},
		{"extraBody", nil, func(p []byte) []byte { return append(p, 'x') }, 200},
		{"oversizedLine", nil, func([]byte) []byte { return []byte("SIP/2.0 200 " + strings.Repeat("x", 5000) + "\r\n\r\n") }, 200},
		{"badStatus", nil, func([]byte) []byte { return []byte("SIP/2.0 bad fixture-secret\r\n\r\n") }, 200},
		{"truncated", nil, func(p []byte) []byte { return p[:len(p)-2] }, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := SIPConfig{Transport: "udp"}
			c.Address = udpServiceFixture(t, func(p []byte) ([]byte, error) {
				out, e := sipFixtureReply(p, tc.status, tc.mutate)
				if tc.raw != nil {
					out = tc.raw(out)
				}
				return out, e
			})
			expectState(t, Config{Type: SIP, Timeout: time.Second, SIP: &c}, "down")
		})
	}
}

func TestSIPRetransmitExpectedStatusAndCompactHeaders(t *testing.T) {
	var calls atomic.Int32
	c := SIPConfig{Transport: "udp", ExpectedStatus: []int{404}, RequestURI: "sip:monitor@example.test"}
	c.Address = udpServiceFixture(t, func(p []byte) ([]byte, error) {
		if calls.Add(1) == 1 {
			return nil, nil
		}
		out, e := sipFixtureReply(p, 404, nil)
		out = []byte(strings.NewReplacer("Via:", "v:", "From:", "f:", "To:", "t:", "Call-ID:", "i:", "Content-Length:", "l:").Replace(string(out)))
		return out, e
	})
	expectState(t, Config{Type: SIP, Timeout: 2 * time.Second, SIP: &c}, "up")
	if calls.Load() != 2 {
		t.Fatal("missing non-INVITE UDP retransmission")
	}
}

func TestSIPTLSVerificationAndCanceledHandshake(t *testing.T) {
	for _, mode := range []string{"untrusted", "hostname", "missingClientCertificate"} {
		t.Run(mode, func(t *testing.T) {
			server, client := testTLS(t)
			if mode == "missingClientCertificate" {
				server, client = specialistMTLS(t)
				client.ClientCertPEM = ""
				client.ClientKeyPEM = ""
			}
			if mode == "untrusted" {
				client = nil
			} else if mode == "hostname" {
				client.ServerName = "wrong.example"
			}
			c := SIPConfig{Transport: "tls", TLS: client}
			c.Address = fixture(t, func(raw net.Conn) error {
				s := tls.Server(raw, server)
				_ = s.Handshake()
				_, _ = io.Copy(io.Discard, s)
				return nil
			})
			expectState(t, Config{Type: SIP, Timeout: 200 * time.Millisecond, SIP: &c}, "down")
		})
	}
	c := SIPConfig{Transport: "tls"}
	c.Address = fixture(t, func(raw net.Conn) error { _, _ = io.Copy(io.Discard, raw); return nil })
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(25*time.Millisecond, cancel)
	if r := Check(ctx, Config{Type: SIP, Timeout: time.Second, SIP: &c}); !strings.Contains(r.Message, "canceled") {
		t.Fatal(r)
	}
}

func TestSIPTimeoutAndValidation(t *testing.T) {
	c := SIPConfig{Transport: "udp"}
	c.Address = udpServiceFixture(t, func([]byte) ([]byte, error) { return nil, nil })
	expectState(t, Config{Type: SIP, Timeout: 35 * time.Millisecond, SIP: &c}, "down")
	for _, mutate := range []func(*SIPConfig){func(c *SIPConfig) { c.Transport = "" }, func(c *SIPConfig) { c.RequestURI = "sip:user:fixture-secret@localhost" }, func(c *SIPConfig) { c.RequestURI = "sip:localhost\r\nsecret" }, func(c *SIPConfig) { c.RequestURI = "sips:localhost" }, func(c *SIPConfig) { c.ExpectedStatus = []int{100} }, func(c *SIPConfig) { c.TLS = &TLSConfig{} }} {
		bad := c
		mutate(&bad)
		e := Validate(Config{Type: SIP, Timeout: time.Second, SIP: &bad})
		if e == nil || strings.Contains(e.Error(), "fixture-secret") {
			t.Fatal("invalid SIP configuration")
		}
	}
}
