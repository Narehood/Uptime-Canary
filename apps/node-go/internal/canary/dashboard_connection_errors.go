package canary

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"syscall"
	"uptime-canary/apps/node-go/internal/nodetls"
)

// Keep transport diagnostics free of raw errors, which can contain private URLs.
func dashboardNodeConnectionError(err error) error {
	var certificateError *tls.CertificateVerificationError
	if errors.Is(err, nodetls.ErrCertificate) || errors.As(err, &certificateError) {
		return fail(502, "Cannot authenticate the node certificate; verify its fingerprint and validity")
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return fail(502, "The dashboard server could not resolve this node's host name; check its address and DNS")
	}
	var network net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &network) && network.Timeout() {
		return fail(502, "The dashboard server's connection to this node timed out; check its route, VPN permissions and firewall")
	}
	// Windows reports WSAECONNREFUSED (10061) rather than the POSIX errno.
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.Errno(10061)) {
		return fail(502, "The node address refused the connection; check the published port, listening interface and firewall")
	}
	var header tls.RecordHeaderError
	if errors.Is(err, http.ErrSchemeMismatch) || errors.As(err, &header) {
		return fail(502, "The node did not answer with HTTPS; check its TLS mode and the URL scheme")
	}
	return fail(502, "Cannot reach this node from the dashboard server; check its address, certificate and permissions")
}
