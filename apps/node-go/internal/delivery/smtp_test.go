package delivery

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
	"time"
)

type smtpFixtureOptions struct {
	mode                string
	tlsConfig           *tls.Config
	noSTARTTLS          bool
	rcptRefusal         int
	rcptCode            int
	authCode            int
	authChallenge       bool
	hang                string
	greeting            string
	finalReply          string
	disconnectAfterDATA bool
}

type smtpTrace struct {
	commands []string
	body     string
	secure   bool
	auth     string
	err      error
}

// A real TCP server with strict line framing; test passwords are generated
// fixture literals and never leave loopback.
func startSMTPFixture(t *testing.T, opts smtpFixtureOptions) (Config, <-chan smtpTrace) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	traceCh := make(chan smtpTrace, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		trace := smtpTrace{}
		defer func() { traceCh <- trace }()
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		_ = raw.SetDeadline(time.Now().Add(3 * time.Second))
		var conn net.Conn = raw
		if opts.mode == "implicit" {
			secure := tls.Server(raw, opts.tlsConfig)
			if err := secure.Handshake(); err != nil {
				trace.err = err
				return
			}
			conn, trace.secure = secure, true
		}
		if opts.hang == "greeting" {
			_, trace.err = conn.Read(make([]byte, 1))
			return
		}
		greeting := opts.greeting
		if greeting == "" {
			greeting = "220 fixture.example ESMTP\r\n"
		}
		if _, err := io.WriteString(conn, greeting); err != nil {
			trace.err = err
			return
		}
		reader := bufio.NewReader(conn)
		rcpts := 0
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				trace.err = err
				return
			}
			if !strings.HasSuffix(line, "\r\n") {
				trace.err = fmt.Errorf("invalid command framing")
				return
			}
			line = strings.TrimSuffix(line, "\r\n")
			trace.commands = append(trace.commands, line)
			command, _, _ := strings.Cut(line, " ")
			if opts.hang == command {
				_, trace.err = conn.Read(make([]byte, 1))
				return
			}
			switch command {
			case "EHLO":
				if line != "EHLO localhost" {
					trace.err = fmt.Errorf("unexpected EHLO")
					return
				}
				response := "250-fixture.example\r\n"
				if !opts.noSTARTTLS && !trace.secure && opts.mode != "plain" {
					response += "250-STARTTLS\r\n"
				}
				response += "250 AUTH PLAIN\r\n"
				_, _ = io.WriteString(conn, response)
			case "STARTTLS":
				_, _ = io.WriteString(conn, "220 Begin TLS\r\n")
				if opts.hang == "tls" {
					_, trace.err = io.Copy(io.Discard, conn)
					return
				}
				secure := tls.Server(conn, opts.tlsConfig)
				if err := secure.Handshake(); err != nil {
					trace.err = err
					return
				}
				conn, trace.secure = secure, true
				reader = bufio.NewReader(conn)
			case "AUTH":
				if !trace.secure {
					trace.err = fmt.Errorf("plaintext AUTH leaked")
					return
				}
				auth := strings.TrimPrefix(line, "AUTH PLAIN ")
				if opts.authChallenge {
					_, _ = io.WriteString(conn, "334 \r\n")
					response, err := reader.ReadString('\n')
					if err != nil {
						trace.err = err
						return
					}
					auth = strings.TrimSuffix(response, "\r\n")
				}
				decoded, err := base64.StdEncoding.DecodeString(auth)
				if err != nil {
					trace.err = err
					return
				}
				trace.auth = string(decoded)
				code := opts.authCode
				if code == 0 {
					code = 235
				}
				_, _ = fmt.Fprintf(conn, "%d fixture-secret auth reply\r\n", code)
			case "MAIL":
				if line != "MAIL FROM:<canary@example.test>" {
					trace.err = fmt.Errorf("wrong envelope sender")
					return
				}
				_, _ = io.WriteString(conn, "250 Sender OK\r\n")
			case "RCPT":
				rcpts++
				if rcpts == opts.rcptRefusal {
					code := opts.rcptCode
					if code == 0 {
						code = 550
					}
					_, _ = fmt.Fprintf(conn, "%d fixture-secret rejected recipient\r\n", code)
				} else {
					_, _ = io.WriteString(conn, "250 Recipient OK\r\n")
				}
			case "RSET":
				_, _ = io.WriteString(conn, "250 Transaction reset\r\n")
			case "DATA":
				if rcpts != 2 || opts.rcptRefusal != 0 {
					trace.err = fmt.Errorf("DATA sent after recipient refusal")
					return
				}
				_, _ = io.WriteString(conn, "354 Send message\r\n")
				body, err := io.ReadAll(textproto.NewReader(reader).DotReader())
				trace.body = string(body)
				if err != nil {
					trace.err = err
					return
				}
				if opts.disconnectAfterDATA {
					return
				}
				if opts.hang == "final" {
					_, trace.err = conn.Read(make([]byte, 1))
					return
				}
				response := opts.finalReply
				if response == "" {
					response = "250 Queued\r\n"
				}
				_, _ = io.WriteString(conn, response)
			case "QUIT":
				_, _ = io.WriteString(conn, "221 Bye\r\n")
				return
			default:
				trace.err = fmt.Errorf("unexpected command %q", command)
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			t.Error("SMTP fixture did not stop")
		}
	})
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	portNumber, _ := strconv.Atoi(port)
	config := fixtureConfig(SMTP, "")
	config.SMTP = SMTPConfig{Server: "127.0.0.1", Port: portNumber, TLSMode: opts.mode}
	return config, traceCh
}

func fixtureCertificate(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "delivery fixture"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA: true, BasicConstraintsValid: true, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certPEM) {
		t.Fatal("fixture root")
	}
	return &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}, roots
}

func awaitTrace(t *testing.T, ch <-chan smtpTrace) smtpTrace {
	t.Helper()
	select {
	case trace := <-ch:
		return trace
	case <-time.After(2 * time.Second):
		t.Fatal("SMTP fixture did not finish")
		return smtpTrace{}
	}
}

func TestSMTPPlainSTARTTLSAndImplicitTLS(t *testing.T) {
	tlsConfig, roots := fixtureCertificate(t)
	for _, mode := range []string{"plain", "", "starttls", "implicit"} {
		t.Run("mode-"+mode, func(t *testing.T) {
			config, traces := startSMTPFixture(t, smtpFixtureOptions{mode: mode, tlsConfig: tlsConfig, authChallenge: mode == "implicit"})
			if mode != "plain" {
				config.SMTP.Username, config.SMTP.Password = "fixture-user", "fixture-password"
			}
			result := New(Options{SMTPRootCAs: roots, Timeout: time.Second}).Send(context.Background(), config, fixtureMessage())
			trace := awaitTrace(t, traces)
			if !result.Accepted || result.AcceptedRecipients != 2 || trace.err != nil {
				t.Fatalf("result=%+v trace=%+v", result, trace)
			}
			if trace.secure != (mode != "plain") {
				t.Fatal("incorrect TLS state", trace.secure)
			}
			if mode != "plain" && trace.auth != "\x00fixture-user\x00fixture-password" {
				t.Fatal("incorrect AUTH PLAIN")
			}
			if mode == "plain" && trace.auth != "" {
				t.Fatal("unexpected plaintext credentials")
			}
			parsed, err := mail.ReadMessage(strings.NewReader(trace.body))
			if err != nil {
				t.Fatal("invalid MIME", err)
			}
			if parsed.Header.Get("Subject") != "API is down" || len(parsed.Header["To"]) != 1 {
				t.Fatal("invalid mail headers", parsed.Header)
			}
			media, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
			if err != nil || media != "multipart/alternative" {
				t.Fatal("invalid MIME multipart", media, err)
			}
			reader := multipart.NewReader(parsed.Body, params["boundary"])
			for _, want := range []string{"Connection failed", "<p>Connection failed</p>"} {
				part, err := reader.NextPart()
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(part)
				if err != nil || string(body) != want {
					t.Fatalf("body=%q err=%v", body, err)
				}
			}
		})
	}
}

func TestSMTPRCPTRefusalResetsBeforeDATA(t *testing.T) {
	for _, code := range []int{450, 550} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			config, traces := startSMTPFixture(t, smtpFixtureOptions{mode: "plain", rcptRefusal: 2, rcptCode: code})
			result := New(Options{Timeout: time.Second}).Send(context.Background(), config, fixtureMessage())
			trace := awaitTrace(t, traces)
			want := Rejected
			if code == 450 {
				want = Retryable
			}
			if result.Accepted || result.Status != want || result.RejectedRecipients != 2 || result.AcceptedRecipients != 0 {
				t.Fatal(result)
			}
			if trace.body != "" || len(trace.commands) == 0 || trace.commands[len(trace.commands)-1] != "RSET" || strings.Contains(strings.Join(trace.commands, " "), "DATA") {
				t.Fatalf("unsafe transaction: %+v", trace)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "fixture-secret") {
				t.Fatal("SMTP error leaked")
			}
		})
	}
}

func TestSMTPSTARTTLSRequiredAndTLSVerification(t *testing.T) {
	tlsConfig, roots := fixtureCertificate(t)
	for _, tc := range []struct {
		name, mode             string
		noTLS, trusted, oldTLS bool
	}{
		{"no-starttls", "", true, true, false},
		{"untrusted-starttls", "starttls", false, false, false},
		{"untrusted-implicit", "implicit", false, false, false},
		{"tls-1.1-refused", "starttls", false, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serverTLS := tlsConfig.Clone()
			if tc.oldTLS {
				serverTLS.MinVersion, serverTLS.MaxVersion = tls.VersionTLS10, tls.VersionTLS11
			}
			config, traces := startSMTPFixture(t, smtpFixtureOptions{mode: tc.mode, tlsConfig: serverTLS, noSTARTTLS: tc.noTLS})
			config.SMTP.Username, config.SMTP.Password = "fixture-user", "fixture-password"
			opts := Options{Timeout: time.Second}
			if tc.trusted {
				opts.SMTPRootCAs = roots
			}
			result := New(opts).Send(context.Background(), config, fixtureMessage())
			trace := awaitTrace(t, traces)
			if result.Accepted || result.ErrorCode != "smtp_tls" || trace.auth != "" || trace.body != "" {
				t.Fatalf("result=%+v trace=%+v", result, trace)
			}
		})
	}
}

func TestSMTPTimeoutClosesConnectionAtEveryStage(t *testing.T) {
	tlsConfig, roots := fixtureCertificate(t)
	for _, stage := range []string{"greeting", "EHLO", "STARTTLS", "tls", "AUTH", "MAIL", "RCPT", "DATA", "final"} {
		t.Run(stage, func(t *testing.T) {
			config, traces := startSMTPFixture(t, smtpFixtureOptions{mode: "starttls", tlsConfig: tlsConfig, hang: stage})
			config.SMTP.Username, config.SMTP.Password = "fixture-user", "fixture-password"
			start := time.Now()
			result := New(Options{Timeout: 80 * time.Millisecond, SMTPRootCAs: roots}).Send(context.Background(), config, fixtureMessage())
			trace := awaitTrace(t, traces)
			if result.Accepted || result.Status != Unknown || result.ErrorCode != "canceled" || time.Since(start) > time.Second {
				t.Fatalf("result=%+v elapsed=%v trace=%+v", result, time.Since(start), trace)
			}
		})
	}
}

func TestSMTPAuthFailureAndAmbiguousDATA(t *testing.T) {
	tlsConfig, roots := fixtureCertificate(t)
	config, traces := startSMTPFixture(t, smtpFixtureOptions{mode: "starttls", tlsConfig: tlsConfig, authCode: 535})
	config.SMTP.Username, config.SMTP.Password = "fixture-user", "fixture-password"
	result := New(Options{SMTPRootCAs: roots, Timeout: time.Second}).Send(context.Background(), config, fixtureMessage())
	trace := awaitTrace(t, traces)
	if result.Accepted || result.Status != Rejected || result.ErrorCode != "smtp_auth" || trace.body != "" {
		t.Fatalf("result=%+v trace=%+v", result, trace)
	}
	config, traces = startSMTPFixture(t, smtpFixtureOptions{mode: "plain", disconnectAfterDATA: true})
	result = New(Options{Timeout: time.Second}).Send(context.Background(), config, fixtureMessage())
	trace = awaitTrace(t, traces)
	if result.Accepted || result.Status != Unknown || trace.body == "" {
		t.Fatalf("disconnect must be ambiguous: result=%+v trace=%+v", result, trace)
	}
}

func TestSMTPTrustedCertificateStillRequiresMatchingHostname(t *testing.T) {
	tlsConfig, _ := fixtureCertificate(t)
	// Trust the fixture certificate while giving it names that do not match
	// the dialed loopback address. Verification must still fail.
	cert, err := x509.ParseCertificate(tlsConfig.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	key := tlsConfig.Certificates[0].PrivateKey.(*ecdsa.PrivateKey)
	cert.IPAddresses = nil
	cert.DNSNames = []string{"other.example.test"}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	newTLS := tlsConfig.Clone()
	newTLS.Certificates = []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	config, traces := startSMTPFixture(t, smtpFixtureOptions{mode: "implicit", tlsConfig: newTLS})
	result := New(Options{SMTPRootCAs: roots, Timeout: time.Second}).Send(context.Background(), config, fixtureMessage())
	trace := awaitTrace(t, traces)
	if result.Accepted || result.ErrorCode != "smtp_tls" || trace.auth != "" || trace.body != "" {
		t.Fatalf("result=%+v trace=%+v", result, trace)
	}
}

func TestSMTPCallerCancellationAndDotFraming(t *testing.T) {
	config, traces := startSMTPFixture(t, smtpFixtureOptions{mode: "plain", hang: "greeting"})
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()
	defer cancel()
	start := time.Now()
	result := New(Options{Timeout: time.Second}).Send(ctx, config, fixtureMessage())
	_ = awaitTrace(t, traces)
	if result.Accepted || result.ErrorCode != "canceled" || time.Since(start) > time.Second {
		t.Fatal(result)
	}
	config, traces = startSMTPFixture(t, smtpFixtureOptions{mode: "plain"})
	m := Message{Subject: "dot framing", Text: "line one\r\n.\r\n..second\r\nMAIL FROM:<injected@example.test>\r\n"}
	result = Send(context.Background(), config, m)
	trace := awaitTrace(t, traces)
	if !result.Accepted || trace.err != nil {
		t.Fatalf("result=%+v trace=%+v", result, trace)
	}
	parsed, err := mail.ReadMessage(strings.NewReader(trace.body))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
	if err != nil || strings.ReplaceAll(string(body), "\r\n", "\n") != strings.ReplaceAll(m.Text, "\r\n", "\n") {
		t.Fatalf("dot framing changed content: %q err=%v", body, err)
	}
}

func TestSMTPGreetingBoundsAndFinalRejection(t *testing.T) {
	for _, greeting := range []string{"garbage\r\n", "220 " + strings.Repeat("x", 4096) + "\r\n", strings.Repeat("220-more\r\n", 101), "550 fixture-secret refused\r\n"} {
		config, traces := startSMTPFixture(t, smtpFixtureOptions{mode: "plain", greeting: greeting})
		result := New(Options{Timeout: time.Second}).Send(context.Background(), config, fixtureMessage())
		_ = awaitTrace(t, traces)
		if result.Accepted || result.ErrorCode != "smtp_protocol" {
			t.Fatal(result)
		}
	}
	config, traces := startSMTPFixture(t, smtpFixtureOptions{mode: "plain", finalReply: "554 fixture-secret policy refusal\r\n"})
	result := New(Options{Timeout: time.Second}).Send(context.Background(), config, fixtureMessage())
	_ = awaitTrace(t, traces)
	if result.Accepted || result.Status != Rejected {
		t.Fatal(result)
	}
}

func TestSMTPSinglePartMIMEAndLineBounds(t *testing.T) {
	config := fixtureConfig(SMTP, "")
	for _, htmlOnly := range []bool{false, true} {
		m := Message{Subject: strings.Repeat("é", 200), Text: "first\n.dot\r\n" + strings.Repeat("x", 1000), IdempotencyKey: "r1"}
		want := m.Text
		if htmlOnly {
			m.HTML, m.Text = m.Text, ""
		}
		data := smtpMessage(config, m)
		for _, line := range strings.Split(string(data), "\r\n") {
			if len(line) > 998 {
				t.Fatal("oversized MIME line", len(line))
			}
		}
		parsed, err := mail.ReadMessage(strings.NewReader(string(data)))
		if err != nil {
			t.Fatal(err)
		}
		subject, err := (&mime.WordDecoder{}).DecodeHeader(parsed.Header.Get("Subject"))
		if err != nil || subject != m.Subject {
			t.Fatal("invalid MIME subject", err)
		}
		body, err := io.ReadAll(quotedprintable.NewReader(parsed.Body))
		if err != nil || strings.ReplaceAll(string(body), "\r\n", "\n") != strings.ReplaceAll(want, "\r\n", "\n") {
			t.Fatal("invalid MIME body", err)
		}
	}
}
