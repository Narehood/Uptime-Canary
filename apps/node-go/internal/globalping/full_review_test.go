package globalping_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	gp "uptime-canary/apps/node-go/internal/globalping"
)

func TestFullReviewCARejectsSkippedCertificateBlocks(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
		NotBefore: time.Unix(0, 0), NotAfter: time.Unix(4102444800, 0)}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, public, private)
	if err != nil {
		t.Fatal(err)
	}
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	// All material is generated in memory; Validate never contacts the API.
	for _, tc := range []struct {
		name, ca string
		valid    bool
	}{
		{"no-custom-trust", "", true},
		{"valid-certificate", ca, true},
		{"valid-whitespace", " \r\n\t" + ca + "\n\t ", true},
		{"valid-bundle", ca + ca, true},
		{"maximum-bundle", strings.Repeat(ca, 32), true},
		{"malformed-prefix", "-----BEGIN CERTIFICATE-----\n!\n-----END CERTIFICATE-----\n" + ca, false},
		{"unmatched-begin-prefix", "-----BEGIN CERTIFICATE-----\n" + ca, false},
		{"malformed-suffix", ca + "-----BEGIN CERTIFICATE-----\n!\n-----END CERTIFICATE-----\n", false},
		{"junk-prefix", "junk\n" + ca, false},
		{"junk-suffix", ca + "junk", false},
		{"non-certificate", string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})) + ca, false},
		{"certificate-headers", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: map[string]string{"Fixture": "only"}, Bytes: der})), false},
		{"invalid-der", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("fixture")})) + ca, false},
		{"overlimit-bundle", strings.Repeat(ca, 33), false},
		{"overlimit-bytes", ca + strings.Repeat(" ", 128<<10), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := config("ping")
			c.Options.APICA = tc.ca
			err := gp.Validate(c)
			if tc.valid {
				if err != nil {
					t.Fatalf("valid certificate-only trust rejected: %v", err)
				}
			} else if err == nil || err.Error() != "invalid Globalping configuration" {
				t.Fatalf("invalid trust input admitted or error was unsanitized: %v", err)
			}
		})
	}
}
