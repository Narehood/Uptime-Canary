package checks

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func readClientControl(r io.Reader) (byte, []byte, error) {
	var h [6]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	if h[0]&0x80 == 0 || h[1]&0x80 == 0 || h[1]&127 > 125 {
		return 0, nil, fmt.Errorf("invalid client control frame")
	}
	p := make([]byte, int(h[1]&127))
	if _, err := io.ReadFull(r, p); err != nil {
		return 0, nil, err
	}
	for i := range p {
		p[i] ^= h[2+i%4]
	}
	return h[0] & 15, p, nil
}

func fixtureWebSocketHandler(t *testing.T, mode string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" || r.Header.Get("Sec-WebSocket-Version") != "13" || r.Header.Get("Authorization") != "Bearer private" {
			w.WriteHeader(400)
			return
		}
		key, err := base64.StdEncoding.DecodeString(r.Header.Get("Sec-WebSocket-Key"))
		if err != nil || len(key) != 16 {
			w.WriteHeader(400)
			return
		}
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		digest := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + websocketGUID))
		accept := base64.StdEncoding.EncodeToString(digest[:])
		if mode == "badaccept" {
			accept = "invalid"
		}
		extra := ""
		if mode == "subprotocol" {
			extra = "Sec-WebSocket-Protocol: unexpected\r\n"
		}
		fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: keep-alive, Upgrade\r\nSec-WebSocket-Accept: %s\r\n%s\r\n", accept, extra)
		if buffer.Flush() != nil {
			return
		}
		if mode == "badaccept" || mode == "subprotocol" {
			return
		}
		opcode, nonce, err := readClientControl(buffer)
		if err != nil {
			return
		}
		if opcode != 9 || len(nonce) != 16 {
			t.Error("expected masked random protocol ping")
			return
		}
		send := func(first byte, p []byte) {
			frame := []byte{first, byte(len(p))}
			frame = append(frame, p...)
			_, _ = conn.Write(frame)
		}
		switch mode {
		case "nopong":
			_, _ = buffer.ReadByte()
			return
		case "wrongpong":
			send(0x8a, []byte("wrong"))
			_, _ = buffer.ReadByte()
			return
		case "close":
			send(0x88, []byte{3, 232})
			return
		case "masked":
			_, _ = conn.Write([]byte{0x8a, 0x80})
			return
		case "badcontrol":
			send(0x09, []byte("fragmented"))
			return
		case "rsv":
			send(0xca, nonce)
			return
		case "oversized":
			var frame [10]byte
			frame[0] = 0x82
			frame[1] = 127
			binary.BigEndian.PutUint64(frame[2:], maxWSBytes+1)
			_, _ = conn.Write(frame[:])
			return
		case "noncanonical":
			_, _ = conn.Write([]byte{0x82, 126, 0, 1, 'x'})
			return
		case "continuation":
			send(0x80, []byte("unexpected"))
			return
		case "manyframes":
			_, _ = conn.Write(bytes.Repeat([]byte{0x81, 0}, maxWSFrames))
			return
		case "fragmented":
			send(0x01, []byte("hello"))
			send(0x80, []byte("world"))
		case "serverping":
			send(0x89, []byte("server-challenge"))
			op, p, err := readClientControl(buffer)
			if err != nil || op != 10 || string(p) != "server-challenge" {
				t.Error("server ping did not receive a matching masked pong")
				return
			}
		case "data":
			send(0x81, []byte("application data"))
		}
		send(0x8a, nonce)
		_, _, _ = readClientControl(buffer)
	}
}

func TestWebSocketUpgradeAndProtocolPingPong(t *testing.T) {
	for _, tc := range []struct{ mode, state string }{
		{"good", Up}, {"data", Up}, {"fragmented", Up}, {"serverping", Up},
		{"badaccept", Down}, {"subprotocol", Down}, {"nopong", Down}, {"wrongpong", Down}, {"close", Down}, {"masked", Down}, {"badcontrol", Down}, {"rsv", Down}, {"oversized", Down}, {"noncanonical", Down}, {"continuation", Down}, {"manyframes", Down},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			server := httptest.NewServer(fixtureWebSocketHandler(t, tc.mode))
			defer server.Close()
			timeout := 2 * time.Second
			if tc.mode == "nopong" || tc.mode == "wrongpong" {
				timeout = 150 * time.Millisecond
			}
			r := Check(context.Background(), Config{Type: WebSocket, Target: "ws" + strings.TrimPrefix(server.URL, "http"), Timeout: timeout, WebSocket: &WebSocketConfig{BearerToken: "private"}})
			requireState(t, r, tc.state)
			if r.StatusCode != 101 {
				t.Fatalf("missing upgrade status: %+v", r)
			}
		})
	}
}

func TestWSSCertificateAndCancellation(t *testing.T) {
	pki := newFixturePKI(t, time.Now().Add(-time.Hour), time.Now().Add(72*time.Hour))
	server := fixtureHTTPS(t, &tls.Config{Certificates: []tls.Certificate{pki.server}}, fixtureWebSocketHandler(t, "good"))
	c := Config{Type: WebSocket, Target: "wss" + strings.TrimPrefix(server.URL, "https"), Timeout: 5 * time.Second, TLS: &TLSConfig{CAPEM: pki.caPEM}, WebSocket: &WebSocketConfig{BearerToken: "private"}}
	r := Check(context.Background(), c)
	requireState(t, r, Up)
	if r.Certificate == nil || !r.Certificate.Verified {
		t.Fatal("WSS certificate summary missing")
	}
	c.TLS.MinValidity = 96 * time.Hour
	requireState(t, Check(context.Background(), c), Down)
	entered := make(chan struct{}, 1)
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		digest := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + websocketGUID))
		fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(digest[:]))
		_ = buffer.Flush()
		_, _, _ = readClientControl(buffer)
		entered <- struct{}{}
		_, _ = bufio.NewReader(conn).ReadByte()
	}))
	defer stalled.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	go func() {
		done <- Check(ctx, Config{Type: WebSocket, Target: "ws" + strings.TrimPrefix(stalled.URL, "http"), Timeout: time.Minute})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("WebSocket ping not received")
	}
	cancel()
	select {
	case r := <-done:
		requireState(t, r, Down)
	case <-time.After(time.Second):
		t.Fatal("WebSocket read ignored cancellation")
	}
}
