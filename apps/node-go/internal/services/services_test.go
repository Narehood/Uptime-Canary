package services

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, handler func(net.Conn) error) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		c, e := ln.Accept()
		if e != nil {
			done <- nil
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(4 * time.Second))
		done <- handler(c)
	}()
	t.Cleanup(func() {
		ln.Close()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(5 * time.Second):
			t.Error("fixture did not stop")
		}
	})
	return ln.Addr().String()
}
func testTLS(t *testing.T) (*tls.Config, *TLSConfig) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}, &TLSConfig{CAPEM: string(certPEM)}
}
func expectState(t *testing.T, c Config, state string) Result {
	t.Helper()
	r := Check(context.Background(), c)
	if r.State != state {
		t.Fatalf("state=%s message=%s want %s", r.State, r.Message, state)
	}
	if r.LatencyMs < 0 || r.LatencyMs > 6000 {
		t.Fatalf("invalid latency: %v", r.LatencyMs)
	}
	for _, secret := range []string{"fixture-secret", "private-value", "postgres://", "mysql://", "password="} {
		if strings.Contains(r.Message, secret) {
			t.Fatalf("secret in result %q", r.Message)
		}
	}
	return r
}
func TestValidationAndTypedConditions(t *testing.T) {
	for _, c := range []Config{{}, {Type: "unknown", Timeout: time.Second}, {Type: Redis, Timeout: time.Second, Redis: &RedisConfig{Address: "bad"}}, {Type: Redis, Timeout: time.Second, Redis: &RedisConfig{Address: "127.0.0.1:1"}, Docker: &DockerConfig{}}, {Type: Redis, Timeout: 6 * time.Minute, Redis: &RedisConfig{Address: "127.0.0.1:1"}}} {
		if Validate(c) == nil {
			t.Fatal("accepted invalid config")
		}
		expectState(t, c, "down")
	}
	for _, q := range []string{" ", ";", "SELECT 1; DROP TABLE x", "INSERT INTO x VALUES(1)", "-- comment\nSELECT 1"} {
		if validateQuery(q) == nil {
			t.Fatalf("accepted query %q", q)
		}
	}
	for _, q := range []string{"SELECT 1", "WITH x AS (SELECT 1) SELECT * FROM x", "SHOW STATUS", "VALUES (1)", "EXPLAIN SELECT 1;"} {
		if e := validateQuery(q); e != nil {
			t.Fatal(e)
		}
	}
	for _, tc := range []struct {
		op, expected, actual string
		null, want           bool
	}{
		{"eq", `1`, "1.0", false, true}, {"eq", `9007199254740993`, "9007199254740992", false, false},
		{"gt", `1`, "1.00001", false, true}, {"ge", `1`, "1e0", false, true}, {"lt", `2`, "1", false, true}, {"le", `1`, "1", false, true},
		{"eq", `true`, "t", false, true}, {"eq", `false`, "0", false, true}, {"eq", `null`, "", true, true}, {"eq", `""`, "", true, false},
		{"contains", `"ok"`, "looks ok", false, true}, {"prefix", `"ok"`, "okay", false, true}, {"suffix", `"ok"`, "is ok", false, true}, {"ne", `"a"`, "b", false, true},
		{"gt", `1`, "1e9999999999", false, false},
	} {
		t.Run(fmt.Sprintf("%s/%s/%s", tc.op, tc.expected, tc.actual), func(t *testing.T) {
			c := Condition{Operator: tc.op, Expected: []byte(tc.expected)}
			if c.validate() != nil {
				t.Fatal("invalid condition")
			}
			if c.matches([]byte(tc.actual), tc.null) != tc.want {
				t.Fatal("unexpected condition result")
			}
		})
	}
	for _, c := range []Condition{{Operator: "gt", Expected: []byte(`"1"`)}, {Operator: "eq", Expected: []byte(`[]`)}, {Operator: "eq", Expected: []byte(`1 2`)}, {Operator: "?", Expected: []byte(`1`)}} {
		if c.validate() == nil {
			t.Fatal("accepted invalid condition")
		}
	}
}
func TestTLSValidationAndCanceledCaller(t *testing.T) {
	for _, c := range []*TLSConfig{{CAPEM: "bad"}, {ClientCertPEM: "bad"}, {ClientCertPEM: "bad", ClientKeyPEM: "bad"}, {ServerName: "bad\nname"}} {
		if _, e := tlsSettings(c, "localhost"); e == nil {
			t.Fatal("accepted invalid TLS options")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := Check(ctx, Config{Type: Redis, Timeout: time.Second, Redis: &RedisConfig{Address: "127.0.0.1:1"}})
	if r.Message != "service check canceled" {
		t.Fatal(r)
	}
}
func TestBlockedHandshakeCancellationAllServices(t *testing.T) {
	for _, typ := range []Type{Redis, Postgres, MySQL, MQTT, SFTP} {
		t.Run(string(typ), func(t *testing.T) {
			connected := make(chan struct{})
			address := fixture(t, func(c net.Conn) error { close(connected); _, _ = io.Copy(io.Discard, c); return nil })
			c := Config{Type: typ, Timeout: 2 * time.Second}
			switch typ {
			case Redis:
				c.Redis = &RedisConfig{Address: address}
			case Postgres:
				c.Postgres = &SQLConfig{DSN: "postgres://monitor:fixture-secret@" + address + "/monitor"}
			case MySQL:
				c.MySQL = &SQLConfig{DSN: "monitor:fixture-secret@tcp(" + address + ")/monitor"}
			case MQTT:
				c.MQTT = &MQTTConfig{Address: address, Topic: "status"}
			case SFTP:
				c.SFTP = &SFTPConfig{Address: address, Username: "monitor", Password: "fixture-secret", UnsafeIgnoreHostKey: true}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan Result, 1)
			go func() { done <- Check(ctx, c) }()
			select {
			case <-connected:
			case <-time.After(time.Second):
				t.Fatal("did not connect")
			}
			start := time.Now()
			cancel()
			select {
			case r := <-done:
				if r.State != "down" || r.Message != "service check canceled" {
					t.Fatal(r)
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation did not close handshake")
			}
			if time.Since(start) > time.Second {
				t.Fatal("slow cancellation")
			}
		})
	}
}

func TestMandatoryDeadlineAndConnectionRefusal(t *testing.T) {
	for _, typ := range []Type{Docker, Redis, Postgres, MySQL, MQTT, SFTP} {
		t.Run(string(typ), func(t *testing.T) {
			config := func(address string) Config {
				c := Config{Type: typ, Timeout: 50 * time.Millisecond}
				switch typ {
				case Docker:
					c.Docker = &DockerConfig{Endpoint: "http://" + address, Container: "monitor"}
				case Redis:
					c.Redis = &RedisConfig{Address: address, TLSMode: "plain"}
				case Postgres:
					c.Postgres = &SQLConfig{DSN: "postgres://monitor:fixture-secret@" + address + "/monitor", TLSMode: "plain"}
				case MySQL:
					c.MySQL = &SQLConfig{DSN: "monitor:fixture-secret@tcp(" + address + ")/monitor", TLSMode: "plain"}
				case MQTT:
					c.MQTT = &MQTTConfig{Address: address, Topic: "status", TLSMode: "plain"}
				case SFTP:
					c.SFTP = &SFTPConfig{Address: address, Username: "monitor", Password: "fixture-secret", UnsafeIgnoreHostKey: true}
				}
				return c
			}
			address := fixture(t, func(c net.Conn) error { _, _ = io.Copy(io.Discard, c); return nil })
			r := expectState(t, config(address), "down")
			if r.LatencyMs > 1000 {
				t.Fatal("deadline did not bound protocol I/O")
			}
			ln, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			address = ln.Addr().String()
			ln.Close()
			expectState(t, config(address), "down")
		})
	}
}
