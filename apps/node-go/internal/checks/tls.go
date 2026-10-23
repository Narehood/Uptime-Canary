package checks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"math"
	"net"
	"time"
)

var errCertificateValidity = errors.New("certificate validity condition failed")

func tlsOptions(c Config) TLSConfig {
	if c.TLS != nil {
		return *c.TLS
	}
	return TLSConfig{}
}

func makeTLSConfig(o TLSConfig) (*tls.Config, error) {
	c := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: o.ServerName, InsecureSkipVerify: o.IgnoreVerification} // explicit admin option, dates still checked below
	if o.CAPEM != "" {
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM([]byte(o.CAPEM)) {
			return nil, invalid("TLS CA PEM has no valid certificates")
		}
		c.RootCAs = roots
	}
	if o.ClientCertPEM != "" {
		cert, err := tls.X509KeyPair([]byte(o.ClientCertPEM), []byte(o.ClientKeyPEM))
		if err != nil {
			return nil, invalid("TLS client certificate or key is invalid")
		}
		c.Certificates = []tls.Certificate{cert}
	}
	c.VerifyConnection = func(s tls.ConnectionState) error {
		if len(s.PeerCertificates) == 0 {
			return errCertificateValidity
		}
		now := time.Now()
		for _, cert := range s.PeerCertificates {
			if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
				return errCertificateValidity
			}
		}
		if s.PeerCertificates[0].NotAfter.Sub(now) < o.MinValidity {
			return errCertificateValidity
		}
		return nil
	}
	return c, nil
}

func certificateSummary(state tls.ConnectionState, o TLSConfig) *CertificateSummary {
	if len(state.PeerCertificates) == 0 {
		return nil
	}
	c := state.PeerCertificates[0]
	return &CertificateSummary{NotBefore: c.NotBefore.UTC(), NotAfter: c.NotAfter.UTC(), DaysRemaining: int(math.Floor(time.Until(c.NotAfter).Hours() / 24)), Verified: !o.IgnoreVerification && len(state.VerifiedChains) > 0}
}

// bindConnection bounds all reads/writes and interrupts blocked operations on
// cancellation, including cancellation earlier than the absolute deadline.
func bindConnection(ctx context.Context, conn net.Conn) func() {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	return func() { stop(); _ = conn.Close() }
}

func checkConnection(ctx context.Context, c Config) Result {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", c.Target)
	if err != nil {
		return down("TCP connection failed")
	}
	defer bindConnection(ctx, conn)()
	if c.Type == TCP {
		return up("TCP connection established")
	}
	o := tlsOptions(c)
	config, err := makeTLSConfig(o)
	if err != nil {
		return down("TLS configuration failed")
	}
	if config.ServerName == "" {
		config.ServerName, _, _ = net.SplitHostPort(c.Target)
	}
	tconn := tls.Client(conn, config)
	if err := tconn.HandshakeContext(ctx); err != nil {
		r := down(tlsFailure(err))
		r.Certificate = certificateSummary(tconn.ConnectionState(), o)
		return r
	}
	r := up("TLS connection and certificate checks passed")
	r.Certificate = certificateSummary(tconn.ConnectionState(), o)
	return r
}

func tlsFailure(err error) string {
	var invalidCert x509.CertificateInvalidError
	var unknownCA x509.UnknownAuthorityError
	var badName x509.HostnameError
	if errors.Is(err, errCertificateValidity) {
		return "TLS certificate validity condition failed"
	}
	if errors.As(err, &invalidCert) || errors.As(err, &unknownCA) || errors.As(err, &badName) {
		return "TLS certificate verification failed"
	}
	return "TLS connection or handshake failed"
}
