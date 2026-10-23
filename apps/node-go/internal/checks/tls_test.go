package checks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureHTTPS(t *testing.T, config *tls.Config, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(handler)
	s.TLS = config
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

func TestTCPAndTLSConnectionFixtures(t *testing.T) {
	pki := newFixturePKI(t, time.Now().Add(-time.Hour), time.Now().Add(72*time.Hour))
	address := fixtureTCP(t, func(c net.Conn) {
		tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{pki.server}})
		_ = tc.Handshake()
	})
	requireState(t, Check(context.Background(), Config{Type: TCP, Target: address, Timeout: time.Second}), Up)
	for _, tc := range []struct {
		name, state string
		options     *TLSConfig
	}{
		{"trusted", Up, &TLSConfig{CAPEM: pki.caPEM}},
		{"system trust rejects local CA", Down, nil},
		{"wrong hostname", Down, &TLSConfig{CAPEM: pki.caPEM, ServerName: "wrong.invalid"}},
		{"server name override", Up, &TLSConfig{CAPEM: pki.caPEM, ServerName: "fixture.local"}},
		{"expiry warning threshold", Down, &TLSConfig{CAPEM: pki.caPEM, MinValidity: 96 * time.Hour}},
		{"explicit ignore", Up, &TLSConfig{IgnoreVerification: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Check(context.Background(), Config{Type: TLS, Target: address, Timeout: 5 * time.Second, TLS: tc.options})
			requireState(t, r, tc.state)
			if tc.state == Up {
				if r.Certificate == nil || r.Certificate.DaysRemaining < 2 || r.Certificate.Verified == (tc.options != nil && tc.options.IgnoreVerification) {
					t.Fatalf("bad certificate summary: %+v", r.Certificate)
				}
			}
		})
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddress := listener.Addr().String()
	listener.Close()
	requireState(t, Check(context.Background(), Config{Type: TCP, Target: closedAddress, Timeout: time.Second}), Down)
}

func TestExpiredAndFutureCertificatesCannotPassIgnoreOption(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after time.Time
	}{
		{"expired", time.Now().Add(-48 * time.Hour), time.Now().Add(-time.Hour)},
		{"future", time.Now().Add(time.Hour), time.Now().Add(48 * time.Hour)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pki := newFixturePKI(t, tc.before, tc.after)
			server := fixtureHTTPS(t, &tls.Config{Certificates: []tls.Certificate{pki.server}}, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })
			for _, ignore := range []bool{false, true} {
				requireState(t, Check(context.Background(), Config{Type: HTTP, Target: server.URL, Timeout: time.Second, TLS: &TLSConfig{CAPEM: pki.caPEM, IgnoreVerification: ignore}}), Down)
			}
		})
	}
}

func TestHTTPSCertificateMTLSAndDowngrade(t *testing.T) {
	pki := newFixturePKI(t, time.Now().Add(-time.Hour), time.Now().Add(72*time.Hour))
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM([]byte(pki.caPEM))
	var clientCertificateLeaked atomic.Bool
	other := fixtureHTTPS(t, &tls.Config{Certificates: []tls.Certificate{pki.server}, ClientAuth: tls.RequestClientCert}, func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates) > 0 {
			clientCertificateLeaked.Store(true)
		}
		fmt.Fprint(w, "ok")
	})
	var downgraded atomic.Bool
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { downgraded.Store(true) }))
	defer plain.Close()
	server := fixtureHTTPS(t, &tls.Config{Certificates: []tls.Certificate{pki.server}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots}, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cross":
			http.Redirect(w, r, other.URL, 302)
		case "/downgrade":
			http.Redirect(w, r, plain.URL, 302)
		default:
			fmt.Fprint(w, "ok")
		}
	})
	o := &TLSConfig{CAPEM: pki.caPEM, ClientCertPEM: pki.clientPEM, ClientKeyPEM: pki.clientKeyPEM}
	r := Check(context.Background(), Config{Type: HTTP, Target: server.URL, Timeout: 5 * time.Second, TLS: o})
	requireState(t, r, Up)
	if r.Certificate == nil || !r.Certificate.Verified {
		t.Fatal("missing verified certificate summary")
	}
	requireState(t, Check(context.Background(), Config{Type: HTTP, Target: server.URL, Timeout: time.Second, TLS: &TLSConfig{CAPEM: pki.caPEM}}), Down)
	requireState(t, Check(context.Background(), Config{Type: HTTP, Target: server.URL + "/cross", Timeout: time.Second, TLS: o}), Up)
	if clientCertificateLeaked.Load() {
		t.Fatal("mTLS credential crossed origins")
	}
	requireState(t, Check(context.Background(), Config{Type: HTTP, Target: server.URL + "/downgrade", Timeout: time.Second, TLS: o}), Down)
	if downgraded.Load() {
		t.Fatal("HTTPS downgrade followed")
	}
	// Every TLS hop enforces minimum validity, not just the initial target.
	soon := newFixturePKI(t, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	expiring := fixtureHTTPS(t, &tls.Config{Certificates: []tls.Certificate{soon.server}}, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") })
	redirect := fixtureHTTPS(t, &tls.Config{Certificates: []tls.Certificate{pki.server}}, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, expiring.URL, 302) })
	requireState(t, Check(context.Background(), Config{Type: HTTP, Target: redirect.URL, Timeout: time.Second, TLS: &TLSConfig{CAPEM: pki.caPEM + soon.caPEM, MinValidity: 24 * time.Hour}}), Down)
}

func TestTLSStalledHandshakeAndCancellation(t *testing.T) {
	entered := make(chan struct{}, 2)
	address := fixtureTCP(t, func(c net.Conn) {
		var buf [4096]byte
		_, _ = c.Read(buf[:])
		entered <- struct{}{}
		_, _ = c.Read(buf[:])
	})
	start := time.Now()
	requireState(t, Check(context.Background(), Config{Type: TLS, Target: address, Timeout: 50 * time.Millisecond}), Down)
	if time.Since(start) > time.Second {
		t.Fatal("TLS timeout not bounded")
	}
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 1)
	go func() { done <- Check(ctx, Config{Type: TLS, Target: address, Timeout: time.Minute}) }()
	<-entered
	cancel()
	select {
	case r := <-done:
		requireState(t, r, Down)
		if strings.Contains(r.Message, address) {
			t.Fatal("target leaked")
		}
	case <-time.After(time.Second):
		t.Fatal("TLS handshake ignored cancellation")
	}
}
