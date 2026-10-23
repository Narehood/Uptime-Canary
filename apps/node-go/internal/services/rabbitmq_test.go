package services

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func rabbitOptions() RabbitMQConfig {
	return RabbitMQConfig{Mode: "amqp", TLSMode: "plain", Username: "fixture-user", Password: "fixture-secret", VirtualHost: "monitor-vhost"}
}

func rabbitAMQPFixture(conn net.Conn, mode string) error {
	var protocol [8]byte
	if _, e := io.ReadFull(conn, protocol[:]); e != nil {
		return e
	}
	if !bytes.Equal(protocol[:], []byte{'A', 'M', 'Q', 'P', 0, 0, 9, 1}) {
		return errors.New("missing AMQP 0-9-1 protocol header")
	}
	if mode == "oversized" {
		_, _ = conn.Write([]byte{1, 0, 0, 0x7f, 0xff, 0xff, 0xff})
		return nil
	}
	if mode == "truncated" {
		_, _ = conn.Write([]byte{1, 0, 0, 0})
		return nil
	}
	if mode == "garbage" {
		_, _ = conn.Write([]byte("HTTP/1.1 200 fixture-secret\r\n\r\n"))
		return nil
	}
	if mode == "silent" {
		_, _ = io.Copy(io.Discard, conn)
		return nil
	}
	capabilities := amqpShortString(nil, "connection.blocked")
	capabilities = append(capabilities, 't', 1)
	properties := amqpShortString(nil, "product")
	properties = append(properties, 'S')
	properties = amqpLongString(properties, "RabbitMQ fixture")
	properties = amqpShortString(properties, "capabilities")
	properties = append(properties, 'F')
	properties = binary.BigEndian.AppendUint32(properties, uint32(len(capabilities)))
	properties = append(properties, capabilities...)
	properties = amqpShortString(properties, "signed_short")
	properties = append(properties, 's', 0, 1)
	start := binary.BigEndian.AppendUint32([]byte{0, 9}, uint32(len(properties)))
	start = append(start, properties...)
	if mode == "badProperties" {
		start = []byte{0, 9, 0, 0, 0, 1, 0xff}
	}
	start = amqpLongString(start, "AMQPLAIN PLAIN")
	if mode == "noPLAIN" {
		start = []byte{0, 9, 0, 0, 0, 0}
		start = amqpLongString(start, "EXTERNAL")
	}
	start = amqpLongString(start, "en_US")
	if mode == "heartbeat" {
		if _, e := conn.Write([]byte{8, 0, 0, 0, 0, 0, 0, 0xce}); e != nil {
			return e
		}
	}
	if e := writeAMQPMethod(conn, 10, start); e != nil {
		return e
	}
	if mode == "badProperties" || mode == "noPLAIN" {
		return nil
	}
	auth, e := readAMQPMethod(conn, 11, rabbitMaxFrame)
	if e != nil {
		return e
	}
	// Assert the exact independent SASL PLAIN encoding, not a connection ping.
	want := []byte{0, 0, 0, 0, 5, 'P', 'L', 'A', 'I', 'N'}
	want = binary.BigEndian.AppendUint32(want, uint32(len("\x00fixture-user\x00fixture-secret")))
	want = append(want, []byte("\x00fixture-user\x00fixture-secret")...)
	want = append(want, 5, 'e', 'n', '_', 'U', 'S')
	if !bytes.Equal(auth, want) || mode == "rejectAuth" {
		args := binary.BigEndian.AppendUint16(nil, 403)
		args = amqpShortString(args, "fixture-secret")
		args = append(args, 0, 0, 0, 0)
		_ = writeAMQPMethod(conn, 50, args)
		return nil
	}
	tune := []byte{0, 0, 0, 2, 0, 0, 0, 30} // unlimited channels, frame max 128 KiB, heartbeat 30s
	if mode == "badTune" {
		tune = []byte{0, 0, 0, 0, 0, 1, 0, 0}
	}
	if e = writeAMQPMethod(conn, 30, tune); e != nil {
		return e
	}
	if mode == "badTune" {
		return nil
	}
	tuneOK, e := readAMQPMethod(conn, 31, rabbitMaxFrame)
	if e != nil {
		return e
	}
	if !bytes.Equal(tuneOK, []byte{0, 1, 0, 1, 0, 0, 0, 30}) {
		return errors.New("incorrect tune negotiation")
	}
	opened, e := readAMQPMethod(conn, 40, rabbitMaxFrame)
	if e != nil {
		return e
	}
	want = append([]byte{13}, []byte("monitor-vhost")...)
	want = append(want, 0, 0)
	if !bytes.Equal(opened, want) {
		return errors.New("incorrect configured virtual host")
	}
	if mode == "rejectVhost" {
		_ = writeAMQPMethod(conn, 50, []byte{1, 147, 0, 0, 0, 0, 0})
		return nil
	}
	if e = writeAMQPMethod(conn, 41, []byte{0}); e != nil {
		return e
	}
	closed, e := readAMQPMethod(conn, 50, rabbitMaxFrame)
	if e != nil {
		return e
	}
	if len(closed) < 7 || binary.BigEndian.Uint16(closed) != 200 {
		return errors.New("missing orderly AMQP close")
	}
	if mode == "badClose" {
		_, _ = conn.Write([]byte{1, 0, 0, 0, 0, 0, 4, 0, 10, 0, 51, 0})
		return nil
	}
	return writeAMQPMethod(conn, 51, nil)
}

func TestRabbitMQActualLocalAMQPConnection(t *testing.T) {
	for _, mode := range []string{"plain", "tls", "mtls", "heartbeat"} {
		t.Run(mode, func(t *testing.T) {
			c := rabbitOptions()
			var server *tls.Config
			if mode == "tls" {
				server, c.TLS = testTLS(t)
				c.TLSMode = ""
			}
			if mode == "mtls" {
				server, c.TLS = specialistMTLS(t)
				c.TLSMode = "tls"
			}
			c.Address = fixture(t, func(raw net.Conn) error {
				conn := raw
				if server != nil {
					conn = tls.Server(raw, server)
				}
				return rabbitAMQPFixture(conn, mode)
			})
			expectState(t, Config{Type: RabbitMQ, Timeout: time.Second, RabbitMQ: &c}, "up")
		})
	}
}

func TestRabbitMQAMQPRejectsInvalidPeersAndCredentials(t *testing.T) {
	for _, mode := range []string{"oversized", "truncated", "garbage", "badProperties", "noPLAIN", "rejectAuth", "badTune", "rejectVhost", "badClose", "wrongPassword"} {
		t.Run(mode, func(t *testing.T) {
			c := rabbitOptions()
			c.Address = fixture(t, func(conn net.Conn) error { return rabbitAMQPFixture(conn, mode) })
			if mode == "wrongPassword" {
				c.Password = "private-value"
			}
			expectState(t, Config{Type: RabbitMQ, Timeout: time.Second, RabbitMQ: &c}, "down")
		})
	}
}

func TestRabbitMQManagementAlarmsAndFallback(t *testing.T) {
	for _, mode := range []string{"ok", "alarm", "auth", "malformed", "oversized", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				user, password, ok := r.BasicAuth()
				if r.Method != "GET" || r.URL.Path != "/prefix/api/health/checks/alarms/" || r.URL.RawQuery != "" || !ok || user != "fixture-user" || password != "fixture-secret" {
					t.Error("incorrect management request")
				}
				switch mode {
				case "ok":
					io.WriteString(w, `{"status":"ok"}`)
				case "alarm":
					w.WriteHeader(503)
					io.WriteString(w, `{"status":"failed","reason":"fixture-secret"}`)
				case "auth":
					w.WriteHeader(401)
					io.WriteString(w, "fixture-secret")
				case "malformed":
					io.WriteString(w, `{"status":`)
				case "oversized":
					io.WriteString(w, strings.Repeat("x", 65537))
				case "redirect":
					w.Header().Set("Location", "/fixture-secret")
					w.WriteHeader(302)
				}
			}))
			defer s.Close()
			c := RabbitMQConfig{Mode: "management", Username: "fixture-user", Password: "fixture-secret", TLSMode: "plain", ManagementURLs: []string{s.URL + "/prefix/"}}
			state := "down"
			if mode == "ok" {
				state = "up"
			}
			expectState(t, Config{Type: RabbitMQ, Timeout: time.Second, RabbitMQ: &c}, state)
			if calls.Load() != 1 {
				t.Fatal("redirect or repeated request")
			}
		})
	}
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer failed.Close()
	passed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"status":"ok"}`) }))
	defer passed.Close()
	c := RabbitMQConfig{Mode: "management", Username: "fixture-user", Password: "fixture-secret", TLSMode: "plain", ManagementURLs: []string{failed.URL, passed.URL}}
	expectState(t, Config{Type: RabbitMQ, Timeout: time.Second, RabbitMQ: &c}, "up")
}

func TestRabbitMQTLSVerificationAndManagementMTLS(t *testing.T) {
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
			c := rabbitOptions()
			c.TLSMode = "tls"
			c.TLS = client
			c.Address = fixture(t, func(raw net.Conn) error {
				s := tls.Server(raw, server)
				_ = s.Handshake()
				_, _ = io.Copy(io.Discard, s)
				return nil
			})
			expectState(t, Config{Type: RabbitMQ, Timeout: 200 * time.Millisecond, RabbitMQ: &c}, "down")
		})
	}
	server, client := specialistMTLS(t)
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"status":"ok"}`) }))
	s.TLS = server
	s.StartTLS()
	defer s.Close()
	c := RabbitMQConfig{Mode: "management", Username: "fixture-user", Password: "fixture-secret", TLS: client, ManagementURLs: []string{s.URL}}
	expectState(t, Config{Type: RabbitMQ, Timeout: time.Second, RabbitMQ: &c}, "up")
}

func TestRabbitMQTimeoutCancellationAndValidation(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			c := rabbitOptions()
			c.Address = fixture(t, func(conn net.Conn) error { return rabbitAMQPFixture(conn, "silent") })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			timeout := 40 * time.Millisecond
			if mode == "cancel" {
				timeout = time.Second
				time.AfterFunc(20*time.Millisecond, cancel)
			}
			if r := Check(ctx, Config{Type: RabbitMQ, Timeout: timeout, RabbitMQ: &c}); r.State != "down" {
				t.Fatal(r)
			}
		})
	}
	c := rabbitOptions()
	c.Address = "127.0.0.1:5672"
	for _, mutate := range []func(*RabbitMQConfig){func(c *RabbitMQConfig) { c.Mode = "" }, func(c *RabbitMQConfig) { c.ManagementURLs = []string{"http://localhost"} }, func(c *RabbitMQConfig) { c.VirtualHost = strings.Repeat("x", 256) }, func(c *RabbitMQConfig) { c.Username = "x\x00fixture-secret" }, func(c *RabbitMQConfig) { c.TLS = &TLSConfig{} }, func(c *RabbitMQConfig) {
		c.Mode = "management"
		c.Address = ""
		c.ManagementURLs = []string{"http://user:fixture-secret@localhost"}
	}, func(c *RabbitMQConfig) {
		c.Mode = "management"
		c.Address = ""
		c.TLSMode = ""
		c.ManagementURLs = []string{"http://localhost"}
	}} {
		bad := c
		mutate(&bad)
		e := Validate(Config{Type: RabbitMQ, Timeout: time.Second, RabbitMQ: &bad})
		if e == nil || strings.Contains(e.Error(), "fixture-secret") {
			t.Fatal("invalid RabbitMQ configuration")
		}
	}
}
