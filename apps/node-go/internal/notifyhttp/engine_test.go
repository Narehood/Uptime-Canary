package notifyhttp

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// All keys, certificates and HTTP bodies stay in memory. No OS trust changes,
// files, external hosts, real provider credentials or provider requests occur.
func privateCertificate(t *testing.T, wrongHost bool) ([]byte, tls.Certificate) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("fixture CA key generation failed")
	}
	now := time.Now()
	ca := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "notifyhttp fixture CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal("fixture CA certificate generation failed")
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("fixture server key generation failed")
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	if wrongHost {
		leaf.IPAddresses = []net.IP{net.ParseIP("127.0.0.2")}
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal("fixture server certificate generation failed")
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), tls.Certificate{
		Certificate: [][]byte{leafDER, caDER},
		PrivateKey:  leafKey,
	}
}

func privateTLSServer(t *testing.T, certificate tls.Certificate, handler http.Handler) *httptest.Server {
	t.Helper()
	s := httptest.NewUnstartedServer(handler)
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

func TestVerifiedTLSAndAdditiveCA(t *testing.T) {
	for _, wrongHost := range []bool{false, true} {
		t.Run("wrong-host-"+strconv.FormatBool(wrongHost), func(t *testing.T) {
			caPEM, cert := privateCertificate(t, wrongHost)
			var calls atomic.Int32
			s := privateTLSServer(t, cert, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				io.Copy(io.Discard, r.Body)
				r.Body.Close()
				io.WriteString(w, goodReply(Gotify))
			}))
			c := fixtureConfig(Gotify, s.URL)
			c.Gotify.AllowInsecureHTTP = false
			r := testEngine(t, Options{}).Send(context.Background(), c, Message{Text: "fixture"})
			requireResult(t, r, Unknown)
			if calls.Load() != 0 {
				t.Fatal("untrusted TLS reached the application")
			}
			e := testEngine(t, Options{AdditionalRootCAPEM: caPEM})
			// Mutating the option buffer cannot change an already-built engine.
			for i := range caPEM {
				caPEM[i] = 0
			}
			r = e.Send(context.Background(), c, Message{Text: "fixture"})
			if wrongHost {
				requireResult(t, r, Unknown)
				if calls.Load() != 0 {
					t.Fatal("custom CA bypassed hostname verification")
				}
			} else {
				requireResult(t, r, Accepted)
				if calls.Load() != 1 {
					t.Fatal("custom CA did not permit exactly one verified request")
				}
			}
			if e.transport.TLSClientConfig.InsecureSkipVerify || e.transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
				t.Fatal("TLS verification or minimum version changed")
			}
			freshRoots, err := x509.SystemCertPool()
			if err != nil || e.transport.TLSClientConfig.RootCAs == nil || len(e.transport.TLSClientConfig.RootCAs.Subjects()) <= len(freshRoots.Subjects()) {
				t.Fatal("configured CA did not augment system trust")
			}
		})
	}
}

func TestEngineOptionsAndTransportIsolation(t *testing.T) {
	ca, cert := privateCertificate(t, false)
	leafPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})
	for _, opts := range []Options{
		{Timeout: -time.Nanosecond},
		{Timeout: MaxTimeout + time.Nanosecond},
		{AdditionalRootCAPEM: []byte("fixture_invalid_private_CA")},
		{AdditionalRootCAPEM: leafPEM},
		{AdditionalRootCAPEM: append([]byte("fixture_garbage"), ca...)},
		{AdditionalRootCAPEM: append(append([]byte{}, ca...), []byte("fixture_garbage")...)},
		{AdditionalRootCAPEM: []byte(strings.Repeat(string(ca), 33))},
		{AdditionalRootCAPEM: make([]byte, (256<<10)+1)},
	} {
		if e, err := New(opts); e != nil || err == nil || err.Error() != errOptions.Error() {
			t.Fatal("invalid engine options did not return a fixed error")
		}
	}
	e := testEngine(t, Options{})
	if e.timeout != DefaultTimeout || e.client.Timeout != DefaultTimeout || e.client.Jar != nil || e.transport.Proxy != nil || !e.transport.DisableKeepAlives || !e.transport.DisableCompression || e.transport.MaxResponseHeaderBytes != MaxResponseHeaderBytes || e.transport.MaxConnsPerHost != 2 {
		t.Fatal("engine transport permits ambient state or unbounded behavior")
	}
	if e.client.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("engine redirect policy changed")
	}
	if testEngine(t, Options{Timeout: MaxTimeout}).timeout != MaxTimeout {
		t.Fatal("maximum timeout rejected")
	}
	var nilEngine *Engine
	nilEngine.CloseIdleConnections()
}

func TestTLSBelow12IsRefused(t *testing.T) {
	caPEM, cert := privateCertificate(t, false)
	var calls atomic.Int32
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, goodReply(Gotify))
	}))
	s.Config.ErrorLog = log.New(io.Discard, "", 0)
	s.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS10,
		MaxVersion:   tls.VersionTLS11,
		Certificates: []tls.Certificate{cert},
	}
	s.StartTLS()
	defer s.Close()
	r := testEngine(t, Options{AdditionalRootCAPEM: caPEM}).Send(context.Background(), fixtureConfig(Gotify, s.URL), Message{Text: "fixture"})
	requireResult(t, r, Unknown)
	if calls.Load() != 0 {
		t.Fatal("TLS below 1.2 reached the provider handler")
	}
}

func TestRedirectsNeverForwardSecrets(t *testing.T) {
	for _, kind := range kinds {
		for _, status := range []int{301, 302, 303, 307, 308} {
			t.Run(string(kind)+"-"+strconv.Itoa(status), func(t *testing.T) {
				var sinkCalls, sourceCalls atomic.Int32
				sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					sinkCalls.Add(1)
					io.WriteString(w, goodReply(kind))
				}))
				defer sink.Close()
				source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					sourceCalls.Add(1)
					io.Copy(io.Discard, r.Body)
					r.Body.Close()
					w.Header().Set("Location", sink.URL+"/sink?fixture_redirect_key=fixture_secret")
					w.WriteHeader(status)
					io.WriteString(w, goodReply(kind))
				}))
				defer source.Close()
				r := Send(context.Background(), fixtureConfig(kind, source.URL), Message{Text: "fixture"})
				requireResult(t, r, Rejected)
				if r.ErrorCode != "redirect_refused" || r.HTTPStatus != status || sinkCalls.Load() != 0 || sourceCalls.Load() != 1 {
					t.Fatal("redirect policy forwarded a request or lost the original status")
				}
			})
		}
	}
}

func TestAttemptTimeoutCoversHeadersBodyAndParent(t *testing.T) {
	for _, stage := range []string{"headers", "body", "parent-cancel", "parent-deadline"} {
		t.Run(stage, func(t *testing.T) {
			stop := make(chan struct{})
			arrived := make(chan struct{}, 1)
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				r.Body.Close()
				arrived <- struct{}{}
				if stage == "body" {
					// A complete-looking acknowledgement without response EOF must
					// still time out, rather than being prematurely accepted.
					io.WriteString(w, goodReply(Gotify))
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-stop:
				}
			}))
			t.Cleanup(func() { close(stop); s.Close() })
			timeout := 60 * time.Millisecond
			ctx := context.Background()
			cancel := func() {}
			if stage == "parent-deadline" {
				timeout = 2 * time.Second
				ctx, cancel = context.WithTimeout(ctx, 60*time.Millisecond)
			} else if stage == "parent-cancel" {
				timeout = 2 * time.Second
				ctx, cancel = context.WithCancel(ctx)
				go func() {
					select {
					case <-arrived:
						cancel()
					case <-stop:
					}
				}()
			}
			defer cancel()
			start := time.Now()
			r := testEngine(t, Options{Timeout: timeout}).Send(ctx, fixtureConfig(Gotify, s.URL), Message{Text: "fixture"})
			requireResult(t, r, Unknown)
			if time.Since(start) > time.Second {
				t.Fatal("attempt did not honor its earlier deadline/cancellation")
			}
		})
	}
}

// rawLoopbackFixture handles only fixture connections on a fresh ephemeral port.
// Cleanup closes all live connections and waits for every owned goroutine.
func rawLoopbackFixture(t *testing.T, handler func(net.Conn)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("loopback fixture listener failed")
	}
	var mu sync.Mutex
	connections := make(map[net.Conn]bool)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				defer func() {
					mu.Lock()
					delete(connections, conn)
					mu.Unlock()
				}()
				conn.SetDeadline(time.Now().Add(2 * time.Second))
				handler(conn)
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		for conn := range connections {
			conn.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return "http://" + listener.Addr().String()
}

func TestTruncatedResponseAndAmbiguousNetworkOutcome(t *testing.T) {
	for _, stage := range []string{"during-request", "after-submission", "truncated-response"} {
		t.Run(stage, func(t *testing.T) {
			var calls atomic.Int32
			base := rawLoopbackFixture(t, func(conn net.Conn) {
				calls.Add(1)
				if stage == "during-request" {
					// Close after only a prefix has reached the fixture. The
					// client cannot infer whether an intermediary accepted it.
					var prefix [8]byte
					conn.Read(prefix[:])
					if tcp, ok := conn.(*net.TCPConn); ok {
						tcp.SetLinger(0)
					}
					return
				}
				r, err := http.ReadRequest(bufio.NewReader(conn))
				if err != nil {
					return
				}
				io.Copy(io.Discard, r.Body)
				r.Body.Close()
				if stage == "truncated-response" {
					body := goodReply(Gotify)
					io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: "+strconv.Itoa(len(body)+10)+"\r\nConnection: close\r\n\r\n"+body)
				}
			})
			r := Send(context.Background(), fixtureConfig(Gotify, base), Message{Text: strings.Repeat("fixture", 2000)})
			requireResult(t, r, Unknown)
			if calls.Load() != 1 {
				t.Fatal("ambiguous connection failure was silently replayed")
			}
			if stage == "truncated-response" && r.HTTPStatus != 200 {
				t.Fatal("truncated acknowledgement lost the known status")
			}
		})
	}
}

func TestTLSHandshakeDeadline(t *testing.T) {
	base := rawLoopbackFixture(t, func(conn net.Conn) {
		// Consume ClientHello, then send no TLS handshake or HTTP response.
		io.Copy(io.Discard, conn)
	})
	c := fixtureConfig(Gotify, strings.Replace(base, "http://", "https://", 1))
	start := time.Now()
	r := testEngine(t, Options{Timeout: 60 * time.Millisecond}).Send(context.Background(), c, Message{Text: "fixture"})
	requireResult(t, r, Unknown)
	if time.Since(start) > time.Second {
		t.Fatal("TLS handshake ignored the attempt deadline")
	}
}

func TestResponseHeaderBodyAndJSONResourceBounds(t *testing.T) {
	for _, stage := range []string{"header-bytes", "header-count", "trailer-count", "combined-header-trailer", "body-bytes", "json-depth", "json-array", "json-object", "json-nodes", "json-utf8", "json-key"} {
		t.Run(stage, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body := goodReply(Gotify)
				switch stage {
				case "header-bytes":
					w.Header().Set("X-Fixture-Large", strings.Repeat("x", MaxResponseHeaderBytes+1))
				case "header-count":
					for i := range MaxResponseHeaders + 1 {
						w.Header().Add("X-Fixture", strconv.Itoa(i))
					}
				case "trailer-count":
					for i := range MaxResponseHeaders + 1 {
						w.Header().Add("Trailer", "X-Fixture-"+strconv.Itoa(i))
					}
				case "combined-header-trailer":
					w.Header().Set("X-Fixture-Large", strings.Repeat("x", 20<<10))
					w.Header().Set("Trailer", "X-Fixture-Trailer")
				case "body-bytes":
					body += strings.Repeat(" ", MaxResponseBytes)
				case "json-depth":
					body = `{"id":1,"extra":` + strings.Repeat("[", 17) + `0` + strings.Repeat("]", 17) + `}`
				case "json-array":
					body = `{"id":1,"extra":[` + strings.Repeat("0,", 512) + `0]}`
				case "json-object":
					body = `{"id":1`
					for i := range 256 {
						body += `,"field` + strconv.Itoa(i) + `":0`
					}
					body += `}`
				case "json-nodes":
					row := "[" + strings.Repeat("0,", 511) + "0]"
					body = `{"id":1,"extra":[` + strings.Repeat(row+",", 8) + row + `]}`
				case "json-utf8":
					body = "{\"id\":1,\"extra\":\"" + string([]byte{0xff}) + "\"}"
				case "json-key":
					body = `{"id":1,"` + strings.Repeat("x", 257) + `":0}`
				}
				io.WriteString(w, body)
				if stage == "trailer-count" {
					for i := range MaxResponseHeaders + 1 {
						w.Header().Set("X-Fixture-"+strconv.Itoa(i), "value")
					}
				} else if stage == "combined-header-trailer" {
					w.Header().Set("X-Fixture-Trailer", strings.Repeat("y", 20<<10))
				}
			}))
			defer s.Close()
			r := Send(context.Background(), fixtureConfig(Gotify, s.URL), Message{Text: "fixture"})
			requireResult(t, r, Unknown)
		})
	}
}

func TestRetryHintBoundariesPure(t *testing.T) {
	now := time.Unix(1792700000, 0)
	for _, test := range []struct {
		value string
		want  time.Duration
	}{
		{"0", 0},
		{"00009", 9 * time.Second},
		{"+9", 0},
		{"-9", 0},
		{"9.5", 0},
		{"86401", MaxRetryAfter},
		{strings.Repeat("9", 128), MaxRetryAfter},
		{strings.Repeat("9", 129), 0},
		{now.Add(-time.Hour).UTC().Format(http.TimeFormat), 0},
		{now.Add(48 * time.Hour).UTC().Format(http.TimeFormat), MaxRetryAfter},
	} {
		headers := http.Header{"Retry-After": []string{test.value}}
		if retryHints(Ntfy, headers, nil, now) != test.want {
			t.Fatal("retry boundary was not handled safely")
		}
	}
	headers := http.Header{"Retry-After": []string{"2", "7", "invalid"}}
	if retryHints(Ntfy, headers, nil, now) != 7*time.Second {
		t.Fatal("largest valid retry hint was not selected")
	}
}

func TestDefaultAndZeroEngineAreUsable(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, goodReply(Gotify))
	}))
	defer s.Close()
	c := fixtureConfig(Gotify, s.URL)
	var zero Engine
	var nilEngine *Engine
	requireResult(t, zero.Send(nil, c, Message{Text: "fixture"}), Accepted)
	requireResult(t, nilEngine.Send(nil, c, Message{Text: "fixture"}), Accepted)
}
