package checks

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func fixtureSMTP(t *testing.T, mode, behavior string, pki fixturePKI) string {
	t.Helper()
	return fixtureTCP(t, func(conn net.Conn) {
		upgrade := func() bool {
			tc := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{pki.server}})
			if tc.Handshake() != nil {
				return false
			}
			conn = tc
			return true
		}
		if mode == "implicit" && !upgrade() {
			return
		}
		switch behavior {
		case "greeting error":
			io.WriteString(conn, "554 fixture unavailable\r\n")
			return
		case "long line":
			io.WriteString(conn, "220 "+strings.Repeat("x", 4096)+"\r\n")
			return
		case "many lines":
			io.WriteString(conn, strings.Repeat("220-continue\r\n", 100))
			return
		case "bad framing":
			io.WriteString(conn, "220 missing CRLF\n")
			return
		}
		io.WriteString(conn, "220-fixture\r\n220 ready\r\n")
		reader := bufio.NewReader(conn)
		readEHLO := func() bool {
			line, err := reader.ReadString('\n')
			if err != nil {
				return false
			}
			if line != "EHLO localhost\r\n" {
				t.Errorf("unexpected SMTP command: %q", line)
				return false
			}
			return true
		}
		if !readEHLO() {
			return
		}
		switch behavior {
		case "EHLO error":
			io.WriteString(conn, "550 unavailable\r\n")
			return
		case "mixed reply":
			io.WriteString(conn, "250-fixture\r\n251 wrong code\r\n")
			return
		}
		if mode == "starttls" {
			if behavior == "no STARTTLS" {
				io.WriteString(conn, "250 fixture\r\n")
				return
			}
			io.WriteString(conn, "250-fixture\r\n250-STARTTLS\r\n250 SIZE 1024\r\n")
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if line != "STARTTLS\r\n" {
				t.Errorf("expected STARTTLS, got %q", line)
				return
			}
			if behavior == "STARTTLS refused" {
				io.WriteString(conn, "454 TLS unavailable\r\n")
				return
			}
			io.WriteString(conn, "220 begin TLS\r\n")
			if !upgrade() {
				return
			}
			reader = bufio.NewReader(conn)
			if !readEHLO() {
				return
			}
			if behavior == "encrypted EHLO error" {
				io.WriteString(conn, "550 unavailable\r\n")
				return
			}
		}
		io.WriteString(conn, "250 fixture\r\n")
		line, err := reader.ReadString('\n')
		if err == nil && line != "QUIT\r\n" {
			t.Errorf("unexpected SMTP final command: %q", line)
		}
	})
}

func TestSMTPGreetingEHLOAndTLSFixtures(t *testing.T) {
	pki := newFixturePKI(t, time.Now().Add(-time.Hour), time.Now().Add(72*time.Hour))
	for _, tc := range []struct{ mode, behavior, state string }{
		{"plain", "good", Up}, {"implicit", "good", Up}, {"starttls", "good", Up},
		{"plain", "greeting error", Down}, {"plain", "EHLO error", Down}, {"plain", "mixed reply", Down}, {"plain", "long line", Down}, {"plain", "many lines", Down}, {"plain", "bad framing", Down},
		{"starttls", "no STARTTLS", Down}, {"starttls", "STARTTLS refused", Down}, {"starttls", "encrypted EHLO error", Down},
	} {
		t.Run(tc.mode+"/"+tc.behavior, func(t *testing.T) {
			address := fixtureSMTP(t, tc.mode, tc.behavior, pki)
			c := Config{Type: SMTP, Target: address, Timeout: 5 * time.Second, SMTP: &SMTPConfig{TLSMode: tc.mode}}
			if tc.mode != "plain" {
				c.TLS = &TLSConfig{CAPEM: pki.caPEM}
			}
			r := Check(context.Background(), c)
			requireState(t, r, tc.state)
			if tc.state == Up && tc.mode != "plain" && (r.Certificate == nil || !r.Certificate.Verified) {
				t.Fatal("encrypted SMTP certificate summary missing")
			}
		})
	}
	address := fixtureSMTP(t, "implicit", "good", pki)
	requireState(t, Check(context.Background(), Config{Type: SMTP, Target: address, Timeout: time.Second, SMTP: &SMTPConfig{TLSMode: "implicit"}, TLS: &TLSConfig{CAPEM: pki.caPEM, MinValidity: 96 * time.Hour}}), Down)
}

func TestSMTPTimeoutAndCancellation(t *testing.T) {
	entered := make(chan struct{}, 2)
	address := fixtureTCP(t, func(conn net.Conn) { entered <- struct{}{}; var b [1]byte; _, _ = conn.Read(b[:]) })
	start := time.Now()
	requireState(t, Check(context.Background(), Config{Type: SMTP, Target: address, Timeout: 50 * time.Millisecond}), Down)
	if time.Since(start) > time.Second {
		t.Fatal("SMTP timeout ignored")
	}
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	go func() { done <- Check(ctx, Config{Type: SMTP, Target: address, Timeout: time.Minute}) }()
	<-entered
	cancel()
	select {
	case r := <-done:
		requireState(t, r, Down)
	case <-time.After(time.Second):
		t.Fatal("SMTP cancellation ignored")
	}
}

func TestSMTPReplyByteLimit(t *testing.T) {
	data := strings.Repeat("250-"+strings.Repeat("x", 1020)+"\r\n", 70) + "250 done\r\n"
	if _, err := readSMTPReply(bufio.NewReaderSize(strings.NewReader(data), 4096), 250); err == nil {
		t.Fatal("SMTP aggregate reply limit not enforced")
	}
	if _, err := readSMTPReply(bufio.NewReader(strings.NewReader(fmt.Sprint("220 ready\r\n"))), 220); err != nil {
		t.Fatal(err)
	}
}
