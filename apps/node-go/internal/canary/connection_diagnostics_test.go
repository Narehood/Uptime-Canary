package canary

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
	"time"
	"uptime-canary/apps/node-go/internal/nodetls"
)

func TestNodeListeningURLReportsActualBindingWithoutChangingClientURL(t *testing.T) {
	for _, host := range []string{"0.0.0.0", "127.0.0.1"} {
		for _, mode := range []string{"off", "auto"} {
			t.Run(host+"/"+mode, func(t *testing.T) {
				options := nativeTLSOptions(t, "listen-fixture")
				options.Host, options.TLSMode = host, mode
				node, err := New(options)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = node.Stop() })
				if node.ListeningURL() != "" || node.Start() != nil {
					t.Fatal("listener did not start from an empty address")
				}
				bind, err := url.Parse(node.ListeningURL())
				// Go may implement an IPv4 wildcard with a dual-stack IPv6 socket.
				wildcard := host == "0.0.0.0" && (bind.Hostname() == "0.0.0.0" || bind.Hostname() == "::")
				if err != nil || !wildcard && bind.Hostname() != host || bind.Port() == "" || bind.Port() == "0" {
					t.Fatalf("listener log must identify its actual interface and allocated port: %s", node.ListeningURL())
				}
				clientURL, _ := url.Parse(node.URL())
				if clientURL.Hostname() != "127.0.0.1" || clientURL.Port() != bind.Port() {
					t.Fatal("the connectable loopback URL changed")
				}
				scheme := "http"
				if mode == "auto" {
					scheme = "https"
				}
				if bind.Scheme != scheme || clientURL.Scheme != scheme {
					t.Fatal("listener and client URL lost their actual TLS mode")
				}
				config, err := nodetls.Client(node.TLSFingerprint())
				if err != nil {
					t.Fatal(err)
				}
				transport := &http.Transport{Proxy: nil, TLSClientConfig: config}
				t.Cleanup(transport.CloseIdleConnections)
				client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
				req, _ := http.NewRequestWithContext(t.Context(), "GET", node.URL()+"/readyz", nil)
				response, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode != 200 {
					t.Fatal("the bound listener is not healthy")
				}
			})
		}
	}
}

func TestDashboardNodeConnectionErrorsExplainFailuresWithoutRawDetails(t *testing.T) {
	private := "secret-value@private-node.invalid/internal"
	for _, fixture := range []struct {
		name string
		err  error
		want string
	}{
		{"dns", &net.DNSError{Err: private, Name: private, IsNotFound: true}, "resolve"},
		{"timeout", fmt.Errorf("%s: %w", private, context.DeadlineExceeded), "timed out"},
		{"refused", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, "refused the connection"},
		{"windows-refused", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.Errno(10061)}, "refused the connection"},
		{"http-on-https", tls.RecordHeaderError{Msg: private}, "did not answer with HTTPS"},
		{"http-client-scheme", http.ErrSchemeMismatch, "did not answer with HTTPS"},
		{"pin", fmt.Errorf("%s: %w", private, nodetls.ErrCertificate), "authenticate the node certificate"},
		{"ca", &tls.CertificateVerificationError{Err: errors.New(private)}, "authenticate the node certificate"},
		{"unknown", errors.New(private), "check its address, certificate and permissions"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			err := dashboardNodeConnectionError(&url.Error{Op: "Get", URL: private, Err: fixture.err})
			var api *apiError
			if !errors.As(err, &api) || api.Status != 502 || !strings.Contains(api.Message, fixture.want) {
				t.Fatalf("wrong diagnostic: %v", err)
			}
			if strings.Contains(api.Message, private) || strings.Contains(api.Message, "secret-value") {
				t.Fatal("transport diagnostic exposed raw private details")
			}
		})
	}
}

func TestDashboardVerifyReportsHTTPSMismatchWithoutSendingCredential(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("TLS failed but an HTTP handler received the request")
	}))
	t.Cleanup(plain.Close)
	dashboard, owner, _ := dashboardRegistryFixture(t)
	response := dashboardHTTP(dashboard, "POST", "/api/dashboard/verify", owner, "", map[string]string{
		"label": "Wrong scheme", "url": "https://" + strings.TrimPrefix(plain.URL, "http://"), "token": adminToken,
	})
	if response.Code != 502 || !strings.Contains(response.Body.String(), "did not answer with HTTPS") {
		t.Fatalf("unexpected verification diagnostic: %d %s", response.Code, response.Body.String())
	}
	if len(dashboard.registry) != 0 || len(dashboard.tickets) != 0 || strings.Contains(response.Body.String(), adminToken) {
		t.Fatal("failed verification changed registration state or disclosed its credential")
	}
}
