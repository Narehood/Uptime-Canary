package services

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"testing"
	"time"
)

// Only loopback peers are used by specialist protocol tests.
func udpServiceFixture(t *testing.T, handle func([]byte) ([]byte, error)) string {
	t.Helper()
	c, e := net.ListenPacket("udp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() {
		var b [65536]byte
		for {
			n, peer, e := c.ReadFrom(b[:])
			if e != nil {
				done <- nil
				return
			}
			p, e := handle(b[:n])
			if e != nil {
				done <- e
				return
			}
			if p != nil {
				if _, e = c.WriteTo(p, peer); e != nil {
					done <- e
					return
				}
			}
		}
	}()
	t.Cleanup(func() {
		c.Close()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(time.Second):
			t.Error("UDP fixture did not stop")
		}
	})
	return c.LocalAddr().String()
}

func specialistMTLS(t *testing.T) (*tls.Config, *TLSConfig) {
	t.Helper()
	server, client := testTLS(t)
	server.ClientAuth = tls.RequireAndVerifyClientCert
	server.ClientCAs = x509.NewCertPool()
	server.ClientCAs.AppendCertsFromPEM([]byte(client.CAPEM))
	client.ClientCertPEM = client.CAPEM
	key, e := x509.MarshalPKCS8PrivateKey(server.Certificates[0].PrivateKey)
	if e != nil {
		t.Fatal(e)
	}
	client.ClientKeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}))
	return server, client
}
