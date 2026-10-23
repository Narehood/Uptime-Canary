package checks

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"strconv"
	"strings"
)

func checkSMTP(ctx context.Context, c Config) Result {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", c.Target)
	if err != nil {
		return down("SMTP connection failed")
	}
	defer bindConnection(ctx, conn)()
	mode := smtpMode(c)
	tlsOpts := tlsOptions(c)
	var summary *CertificateSummary
	upgrade := func() bool {
		config, err := makeTLSConfig(tlsOpts)
		if err != nil {
			return false
		}
		if config.ServerName == "" {
			config.ServerName, _, _ = net.SplitHostPort(c.Target)
		}
		tconn := tls.Client(conn, config)
		if err := tconn.HandshakeContext(ctx); err != nil {
			summary = certificateSummary(tconn.ConnectionState(), tlsOpts)
			return false
		}
		summary = certificateSummary(tconn.ConnectionState(), tlsOpts)
		conn = tconn
		return true
	}
	if mode == "implicit" && !upgrade() {
		return Result{State: Down, Message: "SMTP TLS handshake or certificate check failed", Certificate: summary}
	}
	reader := bufio.NewReaderSize(conn, 4096)
	if _, err := readSMTPReply(reader, 220); err != nil {
		return Result{State: Down, Message: "SMTP greeting validation failed", Certificate: summary}
	}
	ehlo := func() ([]string, error) {
		if _, err := io.WriteString(conn, "EHLO localhost\r\n"); err != nil {
			return nil, err
		}
		return readSMTPReply(reader, 250)
	}
	caps, err := ehlo()
	if err != nil {
		return Result{State: Down, Message: "SMTP EHLO validation failed", Certificate: summary}
	}
	if mode == "starttls" {
		advertised := false
		// The first EHLO line is the greeting, not a capability.
		for _, line := range caps[1:] {
			if strings.EqualFold(strings.TrimSpace(line), "STARTTLS") {
				advertised = true
			}
		}
		if !advertised {
			return down("SMTP STARTTLS is not advertised")
		}
		if _, err := io.WriteString(conn, "STARTTLS\r\n"); err != nil {
			return down("SMTP STARTTLS write failed")
		}
		if _, err := readSMTPReply(reader, 220); err != nil || reader.Buffered() != 0 {
			return down("SMTP STARTTLS upgrade refused")
		}
		if !upgrade() {
			return Result{State: Down, Message: "SMTP TLS handshake or certificate check failed", Certificate: summary}
		}
		reader = bufio.NewReaderSize(conn, 4096)
		if _, err := ehlo(); err != nil {
			return Result{State: Down, Message: "SMTP encrypted EHLO validation failed", Certificate: summary}
		}
	}
	// No QUIT reply is required for availability; a bounded best-effort write
	// lets servers release the session promptly.
	_, _ = io.WriteString(conn, "QUIT\r\n")
	return Result{State: Up, Message: "SMTP greeting and EHLO checks passed", Certificate: summary}
}

func readSMTPReply(reader *bufio.Reader, expected int) ([]string, error) {
	var lines []string
	total := 0
	for n := 0; n < 100; n++ {
		line, err := reader.ReadSlice('\n')
		if err != nil {
			return nil, invalid("SMTP reply line read failed or exceeds limit")
		}
		total += len(line)
		if len(line) > 4096 || total > 64<<10 || len(line) < 6 || !strings.HasSuffix(string(line), "\r\n") {
			return nil, invalid("SMTP reply framing or size is invalid")
		}
		code, err := strconv.Atoi(string(line[:3]))
		if err != nil || code != expected || line[3] != ' ' && line[3] != '-' {
			return nil, invalid("SMTP reply code or continuation is invalid")
		}
		lines = append(lines, string(line[4:len(line)-2]))
		if line[3] == ' ' {
			return lines, nil
		}
	}
	return nil, invalid("SMTP reply line count exceeds limit")
}
