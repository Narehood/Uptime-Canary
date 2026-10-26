package nodetls

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetainedCertificateHasTenYearLifetimeAndIndependentKeys(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	a, ak, err := Generate("node-a", now)
	if err != nil {
		t.Fatal(err)
	}
	b, bk, err := Generate("node-a", now)
	if err != nil {
		t.Fatal(err)
	}
	config, leaf, err := Server(a, ak, now)
	if err != nil || config.MinVersion != tls.VersionTLS13 || !leaf.NotAfter.Equal(now.AddDate(10, 0, 0)) || !leaf.NotBefore.Before(now) {
		t.Fatal("invalid server identity or lifetime", err)
	}
	_, other, err := Server(b, bk, now)
	if err != nil || CertificateFingerprint(leaf) == CertificateFingerprint(other) {
		t.Fatal("independent nodes reused TLS identity", err)
	}
	if _, _, err = Server(a, bk, now); err == nil {
		t.Fatal("mismatched private key accepted")
	}
	if _, _, err = Server(a, ak, leaf.NotAfter.Add(time.Second)); err == nil {
		t.Fatal("expired retained identity accepted")
	}
}

func TestPinnedTLSAuthenticatesBeforeSendingCredentialsAndRefusesDowngrade(t *testing.T) {
	certificate, key, err := Generate("private-node", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	config, leaf, err := Server(certificate, key, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.TLS == nil || r.TLS.Version != tls.VersionTLS13 {
			t.Error("request not encrypted with TLS1.3")
		}
		w.WriteHeader(204)
	}))
	server.TLS = config
	server.StartTLS()
	defer server.Close()
	for _, test := range []struct {
		pin string
		ok  bool
	}{{CertificateFingerprint(leaf), true}, {strings.Repeat("0", 64), false}, {"", false}} {
		clientConfig, err := Client(test.pin)
		if err != nil {
			t.Fatal(err)
		}
		transport := &http.Transport{TLSClientConfig: clientConfig}
		client := &http.Client{Transport: transport, Timeout: time.Second}
		before := calls.Load()
		r, _ := http.NewRequest("GET", server.URL, nil)
		r.Header.Set("Authorization", "Bearer private-test-credential")
		response, err := client.Do(r)
		if response != nil {
			response.Body.Close()
		}
		transport.CloseIdleConnections()
		if (err == nil) != test.ok {
			t.Fatalf("pin acceptance changed, wanted success=%v", test.ok)
		}
		if !test.ok && calls.Load() != before {
			t.Fatal("credential reached an untrusted server")
		}
	}
	legacy, err := tls.Dial("tcp", server.Listener.Addr().String(), &tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS12})
	if legacy != nil {
		legacy.Close()
	}
	if err == nil {
		t.Fatal("TLS1.2 downgrade accepted")
	}
}

func TestFingerprintNeverFallsBackAndCertificateConstraintsRemainEnforced(t *testing.T) {
	for _, pin := range []string{"x", strings.Repeat("a", 63), strings.Repeat("g", 64), " " + strings.Repeat("a", 64), strings.Repeat("a:", 32)} {
		if _, err := Client(pin); err == nil {
			t.Fatal("malformed pin accepted")
		}
	}
	normalized, err := Fingerprint(strings.Repeat("A", 64))
	if err != nil || normalized != strings.Repeat("a", 64) {
		t.Fatal("hex normalization failed")
	}
	now := time.Now()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		from, to time.Time
		eku      x509.ExtKeyUsage
	}{{now.Add(time.Hour), now.Add(2 * time.Hour), x509.ExtKeyUsageServerAuth}, {now.Add(-2 * time.Hour), now.Add(-time.Hour), x509.ExtKeyUsageServerAuth}, {now.Add(-time.Hour), now.Add(time.Hour), x509.ExtKeyUsageClientAuth}} {
		cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: test.from, NotAfter: test.to, ExtKeyUsage: []x509.ExtKeyUsage{test.eku}, KeyUsage: x509.KeyUsageDigitalSignature}
		der, err := x509.CreateCertificate(rand.Reader, cert, cert, public, private)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		if !errors.Is(verifyPinned(leaf, CertificateFingerprint(leaf), now), ErrCertificate) {
			t.Fatal("certificate time or server-purpose constraint bypassed")
		}
	}
}
