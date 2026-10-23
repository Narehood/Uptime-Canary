package delivery

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

type smtpSession struct {
	conn   net.Conn
	reader *bufio.Reader
}

func (s *smtpSession) reply() (int, []string, error) {
	var lines []string
	code, total := 0, 0
	for len(lines) < 100 {
		line, err := s.reader.ReadSlice('\n')
		if err != nil {
			return 0, nil, err
		}
		total += len(line)
		if len(line) < 6 || len(line) > 4096 || total > MaxResponseBytes || !bytes.HasSuffix(line, []byte("\r\n")) {
			break
		}
		n, err := strconv.Atoi(string(line[:3]))
		if err != nil || n < 200 || n > 599 || code != 0 && code != n || line[3] != '-' && line[3] != ' ' {
			break
		}
		code = n
		lines = append(lines, string(line[4:len(line)-2]))
		if line[3] == ' ' {
			return code, lines, nil
		}
	}
	return 0, nil, errors.New("invalid SMTP reply")
}

func (s *smtpSession) command(command string) (int, []string, error) {
	if _, err := io.WriteString(s.conn, command+"\r\n"); err != nil {
		return 0, nil, err
	}
	return s.reply()
}

func hasCapability(lines []string, name string) bool {
	// The first EHLO line is the server identification, not an extension.
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) != 0 && strings.EqualFold(fields[0], name) {
			return true
		}
	}
	return false
}

func hasPlainAuth(lines []string) bool {
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "AUTH") {
			continue
		}
		for _, method := range fields[1:] {
			if strings.EqualFold(method, "PLAIN") {
				return true
			}
		}
	}
	return false
}

func smtpFailure(ctx context.Context, code int, err error, fallback string) Result {
	if contextExpired(ctx) {
		return failure(SMTP, Unknown, "canceled")
	}
	if err != nil {
		return failure(SMTP, Unknown, fallback)
	}
	if code >= 400 && code <= 499 {
		return failure(SMTP, Retryable, "unavailable")
	}
	return failure(SMTP, Rejected, fallback)
}

func (e *Engine) sendSMTP(ctx context.Context, c Config, m Message) Result {
	config := c.SMTP
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(config.Server, strconv.Itoa(config.Port)))
	if err != nil {
		return transportFailure(ctx, SMTP)
	}
	defer conn.Close()
	// Capture the raw connection: TLS upgrades must not race a cancellation
	// callback reading a mutable connection variable.
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return failure(SMTP, Unknown, "transport")
	}
	upgrade := func() error {
		secure := tls.Client(conn, &tls.Config{ServerName: config.Server, MinVersion: tls.VersionTLS12, RootCAs: e.roots})
		if err := secure.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = secure
		return nil
	}
	if config.TLSMode == "implicit" {
		if err := upgrade(); err != nil {
			return smtpFailure(ctx, 0, err, "smtp_tls")
		}
	}
	s := &smtpSession{conn: conn, reader: bufio.NewReaderSize(conn, 4096)}
	code, _, err := s.reply()
	if err != nil || code != 220 {
		return smtpFailure(ctx, code, err, "smtp_protocol")
	}
	code, caps, err := s.command("EHLO localhost")
	if err != nil || code != 250 {
		return smtpFailure(ctx, code, err, "smtp_protocol")
	}
	if config.TLSMode == "" || config.TLSMode == "starttls" {
		if !hasCapability(caps, "STARTTLS") {
			return failure(SMTP, Rejected, "smtp_tls")
		}
		code, _, err = s.command("STARTTLS")
		if err != nil || code != 220 {
			return smtpFailure(ctx, code, err, "smtp_tls")
		}
		if s.reader.Buffered() != 0 {
			return failure(SMTP, Rejected, "smtp_tls")
		}
		if err := upgrade(); err != nil {
			return smtpFailure(ctx, 0, err, "smtp_tls")
		}
		s = &smtpSession{conn: conn, reader: bufio.NewReaderSize(conn, 4096)}
		code, caps, err = s.command("EHLO localhost")
		if err != nil || code != 250 {
			return smtpFailure(ctx, code, err, "smtp_protocol")
		}
	}
	if config.Username != "" {
		if _, secure := conn.(*tls.Conn); !secure || !hasPlainAuth(caps) {
			return failure(SMTP, Rejected, "smtp_auth")
		}
		auth := base64.StdEncoding.EncodeToString([]byte("\x00" + config.Username + "\x00" + config.Password))
		code, _, err = s.command("AUTH PLAIN " + auth)
		if err == nil && code == 334 {
			code, _, err = s.command(auth)
		}
		if err != nil || code != 235 {
			return smtpFailure(ctx, code, err, "smtp_auth")
		}
	}
	code, _, err = s.command("MAIL FROM:<" + c.Sender.Email + ">")
	if err != nil || code != 250 {
		return smtpFailure(ctx, code, err, "provider_rejected")
	}
	for _, recipient := range c.Recipients {
		code, _, err = s.command("RCPT TO:<" + recipient.Email + ">")
		if err != nil || code != 250 && code != 251 {
			result := smtpFailure(ctx, code, err, "recipient_rejected")
			// No DATA was sent. All accepted envelope recipients are discarded.
			_, _, _ = s.command("RSET")
			result.RejectedRecipients = len(c.Recipients)
			return result
		}
	}
	data := smtpMessage(c, m)
	code, _, err = s.command("DATA")
	if err != nil || code != 354 {
		return smtpFailure(ctx, code, err, "provider_rejected")
	}
	writer := bufio.NewWriter(conn)
	dot := textproto.NewWriter(writer).DotWriter()
	if _, err := dot.Write(data); err != nil {
		return transportFailure(ctx, SMTP)
	}
	if err := dot.Close(); err != nil {
		return transportFailure(ctx, SMTP)
	}
	code, _, err = s.reply()
	if err != nil {
		return transportFailure(ctx, SMTP)
	}
	if code != 250 {
		return smtpFailure(ctx, code, nil, "provider_rejected")
	}
	// The final DATA acknowledgement is authoritative; QUIT failure cannot undo it.
	_, _ = io.WriteString(conn, "QUIT\r\n")
	return accepted(c, "")
}

func smtpMessage(c Config, m Message) []byte {
	var data bytes.Buffer
	fmt.Fprintf(&data, "From: %s\r\n", addressString(c.Sender))
	for i, recipient := range c.Recipients {
		if i == 0 {
			data.WriteString("To: ")
		} else {
			data.WriteString(",\r\n ")
		}
		data.WriteString(addressString(recipient))
	}
	data.WriteString("\r\n")
	subject := mime.QEncoding.Encode("utf-8", m.Subject)
	if strings.HasPrefix(subject, "=?") {
		// mime's encoder separates encoded words with spaces but does not fold
		// header lines. Fold only those separators, preserving decoded content.
		subject = strings.ReplaceAll(subject, " ", "\r\n ")
	}
	fmt.Fprintf(&data, "Subject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\n", subject, time.Now().UTC().Format(time.RFC1123Z))
	if m.IdempotencyKey != "" {
		// Digest metadata rather than placing arbitrary Unicode into a header.
		fmt.Fprintf(&data, "X-Canary-Idempotency-Key: %s\r\n", providerID(m.IdempotencyKey))
	}
	if m.Text != "" && m.HTML != "" {
		multi := multipart.NewWriter(&data)
		fmt.Fprintf(&data, "Content-Type: multipart/alternative; boundary=%q\r\n\r\n", multi.Boundary())
		for _, part := range []struct{ kind, text string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
			writer, _ := multi.CreatePart(textproto.MIMEHeader{"Content-Type": {part.kind + "; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"}})
			quoted := quotedprintable.NewWriter(writer)
			_, _ = io.WriteString(quoted, part.text)
			_ = quoted.Close()
		}
		_ = multi.Close()
	} else {
		kind, text := "text/plain", m.Text
		if m.HTML != "" {
			kind, text = "text/html", m.HTML
		}
		fmt.Fprintf(&data, "Content-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n", kind)
		quoted := quotedprintable.NewWriter(&data)
		_, _ = io.WriteString(quoted, text)
		_ = quoted.Close()
	}
	return data.Bytes()
}
