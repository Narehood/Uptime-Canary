// Package nodetls authenticates private Canary transports without a trust-all
// option. A configured fingerprint is an explicit trust anchor for one leaf.
package nodetls

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"strings"
	"time"
)

var ErrCertificate = errors.New("Cannot authenticate the Canary TLS certificate")

// Fingerprint accepts only a complete SHA256 certificate hash, with no
// separators. Empty means ordinary operating-system CA and hostname checks.
func Fingerprint(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return "", ErrCertificate
	}
	return strings.ToLower(value), nil
}

func CertificateFingerprint(certificate *x509.Certificate) string {
	value := sha256.Sum256(certificate.Raw)
	return hex.EncodeToString(value[:])
}

// Generate creates an independent retained key. The fingerprint, rather than
// a mutable private address, names this certificate when clients pin it.
func Generate(nodeID string, now time.Time) ([]byte, []byte, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, ErrCertificate
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, ErrCertificate
	}
	serial.Add(serial, big.NewInt(1))
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Uptime-Canary " + nodeID},
		NotBefore: now.UTC().Add(-5 * time.Minute), NotAfter: now.UTC().AddDate(10, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		return nil, nil, ErrCertificate
	}
	key, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return nil, nil, ErrCertificate
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), nil
}

// Inspect checks the matching key and single certificate without checking its
// lifetime. Offline renewal and inspection must also work after expiry.
func Inspect(certificatePEM, keyPEM []byte) (*tls.Config, *x509.Certificate, error) {
	if len(certificatePEM) > 16384 || len(keyPEM) > 8192 {
		return nil, nil, ErrCertificate
	}
	pair, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil || len(pair.Certificate) != 1 {
		return nil, nil, ErrCertificate
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, nil, ErrCertificate
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}}, leaf, nil
}

// Server requires a single currently valid server certificate and matching key.
func Server(certificatePEM, keyPEM []byte, now time.Time) (*tls.Config, *x509.Certificate, error) {
	config, leaf, err := Inspect(certificatePEM, keyPEM)
	if err != nil || verifyPinned(leaf, CertificateFingerprint(leaf), now) != nil {
		return nil, nil, ErrCertificate
	}
	return config, leaf, nil
}

func verifyPinned(leaf *x509.Certificate, fingerprint string, now time.Time) error {
	expected, err := hex.DecodeString(fingerprint)
	if err != nil || len(expected) != sha256.Size {
		return ErrCertificate
	}
	actual := sha256.Sum256(leaf.Raw)
	if subtle.ConstantTimeCompare(expected, actual[:]) != 1 {
		return ErrCertificate
	}
	// Explicitly trusting this exact leaf deliberately replaces DNS/CA lookup.
	// x509 still enforces validity, supported critical extensions and server EKU.
	roots := x509.NewCertPool()
	roots.AddCert(leaf)
	if _, err = leaf.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return ErrCertificate
	}
	return nil
}

// Client preserves ordinary hostname/CA verification unless the administrator
// supplied an exact pin. Pinning never falls back to system roots or HTTP.
func Client(fingerprint string) (*tls.Config, error) {
	pin, err := Fingerprint(fingerprint)
	if err != nil {
		return nil, err
	}
	config := &tls.Config{MinVersion: tls.VersionTLS13}
	if pin != "" {
		// Go's default verifier cannot use a not-yet-received leaf as its root.
		// VerifyConnection completely replaces it with the explicit pin verifier.
		config.InsecureSkipVerify = true
		config.VerifyConnection = func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return ErrCertificate
			}
			return verifyPinned(state.PeerCertificates[0], pin, time.Now())
		}
	}
	return config, nil
}
