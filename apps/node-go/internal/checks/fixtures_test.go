package checks

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"
)

func fixtureTCP(t *testing.T, handler func(net.Conn)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var workers sync.WaitGroup
	connections := map[net.Conn]bool{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer conn.Close()
				defer func() { mu.Lock(); delete(connections, conn); mu.Unlock() }()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				handler(conn)
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-done
		mu.Lock()
		for c := range connections {
			c.Close()
		}
		mu.Unlock()
		workers.Wait()
	})
	return listener.Addr().String()
}

type fixturePKI struct {
	caPEM, serverPEM, serverKeyPEM, clientPEM, clientKeyPEM string
	server                                                  tls.Certificate
}

func newFixturePKI(t *testing.T, notBefore, notAfter time.Time) fixturePKI {
	t.Helper()
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture CA"}, NotBefore: time.Now().Add(-24 * time.Hour), NotAfter: time.Now().Add(365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}))
	issue := func(serial int64, client bool) (string, string) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		cert := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "fixture"}, NotBefore: notBefore, NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"localhost", "fixture.local"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
		if client {
			cert.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		}
		der, err := x509.CreateCertificate(rand.Reader, cert, root, &key.PublicKey, rootKey)
		if err != nil {
			t.Fatal(err)
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	}
	serverPEM, serverKeyPEM := issue(2, false)
	clientPEM, clientKeyPEM := issue(3, true)
	server, err := tls.X509KeyPair([]byte(serverPEM), []byte(serverKeyPEM))
	if err != nil {
		t.Fatal(err)
	}
	return fixturePKI{caPEM, serverPEM, serverKeyPEM, clientPEM, clientKeyPEM, server}
}

func requireState(t *testing.T, r Result, want string) {
	t.Helper()
	if r.State != want || r.Message == "" || r.LatencyMs < 0 {
		t.Fatalf("result = %+v; want state %s with a description", r, want)
	}
}
