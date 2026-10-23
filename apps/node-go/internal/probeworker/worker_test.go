package probeworker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"uptime-canary/apps/node-go/internal/services"
)

func TestMain(m *testing.M) {
	if handled, code := Dispatch(os.Args[1:]); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func supported(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("kernel resource control unsupported on this OS")
	}
}

func fixture(t *testing.T, serve func(net.Conn) error) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		defer close(done)
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			go func() {
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(6 * time.Second))
				if err := serve(c); err != nil {
					t.Errorf("loopback fixture: %v", err)
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = l.Close()
		<-done
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return l.Addr().String()
}

func TestRunRealLoopbackEngine(t *testing.T) {
	supported(t)
	address := fixture(t, func(c net.Conn) error {
		want := "*1\r\n$4\r\nPING\r\n"
		b := make([]byte, len(want))
		if _, err := io.ReadFull(c, b); err != nil {
			return err
		}
		if string(b) != want {
			return errors.New("real Redis PING missing")
		}
		_, err := io.WriteString(c, "+PONG\r\n")
		return err
	})
	r := Run(context.Background(), services.Config{Type: services.Redis, Timeout: 3 * time.Second, Redis: &services.RedisConfig{Address: address, TLSMode: "plain"}})
	if r.State != "up" || r.Message != "service check succeeded" || r.LatencyMs <= 0 {
		t.Fatalf("real engine result: %+v", r)
	}
}

func testTLS(t *testing.T, mutual bool) (*tls.Config, *services.TLSConfig) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "worker-test-ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	issue := func(n int64, client bool) (tls.Certificate, string, string) {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		cert := &x509.Certificate{SerialNumber: big.NewInt(n), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		if client {
			cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		}
		der, e := x509.CreateCertificate(rand.Reader, cert, ca, &key.PublicKey, caKey)
		if e != nil {
			t.Fatal(e)
		}
		keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
		certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
		keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
		pair, e := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
		if e != nil {
			t.Fatal(e)
		}
		return pair, certPEM, keyPEM
	}
	serverPair, _, _ := issue(2, false)
	server := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverPair}}
	client := &services.TLSConfig{CAPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))}
	if mutual {
		_, client.ClientCertPEM, client.ClientKeyPEM = issue(3, true)
		server.ClientAuth = tls.RequireAndVerifyClientCert
		server.ClientCAs = x509.NewCertPool()
		server.ClientCAs.AppendCertsFromPEM([]byte(client.CAPEM))
	}
	return server, client
}

func TestOracleDriverQueryTLSAndConditionsInWorker(t *testing.T) {
	supported(t)
	for _, name := range []string{"plain", "tls", "mtls", "mismatch", "untrusted"} {
		t.Run(name, func(t *testing.T) {
			o := oracleFixtureOptions{value: []byte("private-value"), query: "SELECT STATUS FROM FIXTURE"}
			var clientTLS *services.TLSConfig
			if name == "tls" || name == "mtls" || name == "untrusted" {
				o.tls, clientTLS = testTLS(t, name == "mtls")
			}
			if name == "untrusted" {
				clientTLS.CAPEM = ""
			}
			var seen atomic.Int32
			o.seen = &seen
			address := oracleFixture(t, o)
			cfg := services.Config{Type: services.Oracle, Timeout: 4 * time.Second, Oracle: &services.SQLConfig{DSN: "oracle://fixture-user:fixture-secret@" + address + "/fixture-service", TLSMode: "plain", Query: o.query, Condition: &services.Condition{Operator: "eq", Expected: json.RawMessage(`"private-value"`)}}}
			if clientTLS != nil {
				cfg.Oracle.TLSMode = "tls"
				cfg.Oracle.TLS = clientTLS
			}
			if name == "mismatch" {
				cfg.Oracle.Condition.Expected = json.RawMessage(`"other"`)
			}
			r := Run(context.Background(), cfg)
			want := "up"
			if name == "mismatch" || name == "untrusted" {
				want = "down"
			}
			if r.State != want {
				t.Fatalf("Oracle child: %+v", r)
			}
			if name != "untrusted" && seen.Load() != 2 {
				t.Fatal("real authentication/query was not reached")
			}
			if strings.Contains(r.Message, "private") || strings.Contains(r.Message, "fixture-secret") {
				t.Fatal("Oracle credential/result escaped")
			}
		})
	}
}
