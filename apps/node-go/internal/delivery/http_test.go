package delivery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureConfig(kind Kind, root string) Config {
	return Config{
		Kind: kind, Sender: Address{Email: "canary@example.test", Name: "Canary"},
		Recipients: []Address{{Email: "first@example.test", Name: "First"}, {Email: "second@example.test"}},
		Mailgun:    MailgunConfig{APIKey: "fixture-mailgun-key", Domain: "mg.example.test", BaseURL: root, AllowInsecureHTTP: true},
		Brevo:      BrevoConfig{APIKey: "fixture-brevo-key", BaseURL: root, AllowInsecureHTTP: true},
		Mandrill:   MandrillConfig{APIKey: "fixture-mandrill-key", BaseURL: root, AllowInsecureHTTP: true},
		Webhook:    WebhookConfig{URL: root + "/hook", BearerToken: "fixture-webhook-key", AllowInsecureHTTP: true},
	}
}

func fixtureMessage() Message {
	return Message{EventKind: "down", MonitorName: "API", MonitorID: "m-1", IncidentID: "i-1",
		ObservationsSummary: "Two nodes observed failure", IdempotencyKey: "receipt-1",
		Subject: "API is down", Text: "Connection failed", HTML: "<p>Connection failed</p>"}
}

func TestHTTPProviderRequestsAndAcknowledgements(t *testing.T) {
	for _, kind := range []Kind{Mailgun, Brevo, Mandrill, Webhook} {
		t.Run(string(kind), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "POST" || r.Header.Get("Accept") != "application/json" {
					t.Errorf("invalid method/accept: %s %v", r.Method, r.Header)
				}
				if r.URL.RawQuery != "" || r.Header.Get("Cookie") != "" {
					t.Error("unexpected query or ambient cookies")
				}
				if kind == Mailgun {
					if r.URL.Path != "/prefix/v3/mg.example.test/messages" {
						t.Errorf("wrong path: %s", r.URL.Path)
					}
					user, password, ok := r.BasicAuth()
					if !ok || user != "api" || password != "fixture-mailgun-key" {
						t.Error("invalid Mailgun basic auth")
					}
					if err := r.ParseMultipartForm(1 << 20); err != nil {
						t.Errorf("multipart: %v", err)
						return
					}
					defer r.MultipartForm.RemoveAll()
					want := map[string][]string{"from": {"\"Canary\" <canary@example.test>"}, "to": {"\"First\" <first@example.test>", "<second@example.test>"},
						"subject": {"API is down"}, "text": {"Connection failed"}, "html": {"<p>Connection failed</p>"}, "v:canary-idempotency-key": {"receipt-1"}}
					if !reflect.DeepEqual(r.MultipartForm.Value, want) {
						t.Errorf("multipart = %#v", r.MultipartForm.Value)
					}
					_, _ = io.WriteString(w, `{"id":"opaque-mailgun-id","message":"Queued. Thank you."}`)
					return
				}
				if r.Header.Get("Content-Type") != "application/json" {
					t.Error("missing JSON content type")
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("JSON: %v", err)
					return
				}
				switch kind {
				case Brevo:
					if r.URL.Path != "/prefix/v3/smtp/email" || r.Header.Get("api-key") != "fixture-brevo-key" || r.Header.Get("Authorization") != "" {
						t.Error("invalid Brevo routing/auth")
					}
					if body["subject"] != "API is down" || body["htmlContent"] != "<p>Connection failed</p>" || body["textContent"] != nil {
						t.Errorf("invalid Brevo content: %#v", body)
					}
					if body["sender"].(map[string]any)["email"] != "canary@example.test" || len(body["to"].([]any)) != 2 {
						t.Error("invalid Brevo addresses")
					}
					if body["headers"].(map[string]any)["X-Canary-Idempotency-Key"] != "receipt-1" {
						t.Error("missing Brevo correlation")
					}
					w.WriteHeader(201)
					_, _ = io.WriteString(w, `{"messageId":"<fixture@relay.example.test>"}`)
				case Mandrill:
					if r.URL.Path != "/prefix/api/1.0/messages/send" || body["key"] != "fixture-mandrill-key" || body["async"] != false {
						t.Error("invalid Mandrill routing/auth")
					}
					message := body["message"].(map[string]any)
					if message["from_email"] != "canary@example.test" || message["from_name"] != "Canary" || message["subject"] != "API is down" || message["text"] != "Connection failed" || message["html"] != "<p>Connection failed</p>" {
						t.Error("invalid Mandrill message")
					}
					to := message["to"].([]any)
					if len(to) != 2 || to[0].(map[string]any)["type"] != "to" || to[1].(map[string]any)["email"] != "second@example.test" {
						t.Error("invalid Mandrill recipients")
					}
					_, _ = io.WriteString(w, `[{"email":"second@example.test","status":"queued","_id":"second"},{"email":"first@example.test","status":"sent","_id":"first"}]`)
				case Webhook:
					if r.URL.Path != "/prefix/hook" || r.Header.Get("Authorization") != "Bearer fixture-webhook-key" || r.Header.Get("Idempotency-Key") != "receipt-1" {
						t.Error("invalid webhook routing/auth")
					}
					if body["monitorId"] != "m-1" || body["incidentId"] != "i-1" || body["eventKind"] != "down" || body["observationsSummary"] != "Two nodes observed failure" {
						t.Error("missing webhook metadata")
					}
					w.WriteHeader(204)
				}
			}))
			defer server.Close()
			result := New(Options{}).Send(context.Background(), fixtureConfig(kind, server.URL+"/prefix"), fixtureMessage())
			wantRecipients := 2
			if kind == Webhook {
				wantRecipients = 0
			}
			if !result.Accepted || result.Status != Accepted || result.AcceptedRecipients != wantRecipients || calls.Load() != 1 {
				t.Fatalf("unexpected result: %+v calls=%d", result, calls.Load())
			}
			encoded, _ := json.Marshal(result)
			for _, private := range []string{"fixture-mailgun-key", "fixture-brevo-key", "fixture-mandrill-key", "fixture-webhook-key", "@", "opaque-mailgun-id"} {
				if strings.Contains(string(encoded), private) {
					t.Errorf("private data leaked: %s", encoded)
				}
			}
		})
	}
}

func TestMailgunRegionsAndDefaultRoutes(t *testing.T) {
	for _, tc := range []struct{ base, fallback, path, want string }{
		{"", "https://api.mailgun.net", "/v3/mg.example/messages", "https://api.mailgun.net/v3/mg.example/messages"},
		{"", "https://api.eu.mailgun.net", "/v3/mg.example/messages", "https://api.eu.mailgun.net/v3/mg.example/messages"},
		{"https://private.example/prefix/", "ignored", "/v3/smtp/email", "https://private.example/prefix/v3/smtp/email"},
	} {
		if got := endpoint(tc.base, tc.fallback, tc.path); got != tc.want {
			t.Errorf("endpoint=%s want=%s", got, tc.want)
		}
	}
	// Route through a real local protocol fixture while keeping official URLs
	// observable. No official service is contacted.
	for _, region := range []string{"", "US", "EU"} {
		t.Run("region-"+region, func(t *testing.T) {
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"id":"ack"}`) }))
			defer fixture.Close()
			baseTransport := http.DefaultTransport.(*http.Transport).Clone()
			defer baseTransport.CloseIdleConnections()
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				want := "api.mailgun.net"
				if region == "EU" {
					want = "api.eu.mailgun.net"
				}
				if r.URL.Host != want || r.URL.Scheme != "https" {
					t.Errorf("wrong regional route: %s", r.URL)
				}
				r.URL.Scheme = "http"
				r.URL.Host = strings.TrimPrefix(fixture.URL, "http://")
				return baseTransport.RoundTrip(r)
			})}
			config := fixtureConfig(Mailgun, "")
			config.Mailgun.Region = region
			if result := New(Options{HTTPClient: client}).Send(context.Background(), config, fixtureMessage()); !result.Accepted {
				t.Fatal(result)
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHTTPFailuresAndRetryHints(t *testing.T) {
	for _, kind := range []Kind{Mailgun, Brevo, Mandrill, Webhook} {
		for _, tc := range []struct {
			name   string
			status int
			retry  string
			want   Status
			delay  time.Duration
		}{
			{"terminal", 400, "", Rejected, 0},
			{"unauthorized", 401, "", Rejected, 0},
			{"rate-limit", 429, "17", Retryable, 17 * time.Second},
			{"huge-delay", 429, "18446744073709551615", Retryable, MaxRetryAfter},
			{"invalid-delay", 429, "-1", Retryable, 0},
			{"server-error", 503, "9", Retryable, 9 * time.Second},
		} {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Retry-After", tc.retry)
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, `{"error":"fixture-secret https://user:password@private.example first@example.test"}`)
				}))
				defer server.Close()
				result := Send(context.Background(), fixtureConfig(kind, server.URL), fixtureMessage())
				if result.Accepted || result.Status != tc.want || result.HTTPStatus != tc.status || result.RetryAfter != tc.delay || result.ErrorCode == "" || result.ErrorMessage == "" {
					t.Fatalf("result=%+v", result)
				}
				encoded, _ := json.Marshal(result)
				if strings.Contains(string(encoded), "fixture-secret") || strings.Contains(string(encoded), "password") || strings.Contains(string(encoded), "@") {
					t.Fatal("unsafe result", string(encoded))
				}
			})
		}
	}
}

func TestRetryAfterDatesAndBrevoHint(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		standard, brevo string
		want            time.Duration
	}{
		{now.Add(42 * time.Second).Format(http.TimeFormat), "", 42 * time.Second},
		{now.Add(-time.Hour).Format(http.TimeFormat), "", 0},
		{now.Add(48 * time.Hour).Format(http.TimeFormat), "", MaxRetryAfter},
		{"", "31", 31 * time.Second}, {"invalid", "12", 12 * time.Second}, {"20", "99", 20 * time.Second}, {"1.5", "bad", 0},
	} {
		header := http.Header{}
		header.Set("Retry-After", tc.standard)
		header.Set("x-sib-ratelimit-reset", tc.brevo)
		if got := retryAfter(header, now); got != tc.want {
			t.Errorf("retryAfter=%v want=%v", got, tc.want)
		}
	}
}

func TestMandrillRecipientOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, body         string
		status             Status
		accepted, rejected int
	}{
		{"partial", `[{"email":"first@example.test","status":"sent","_id":"one"},{"email":"second@example.test","status":"invalid","reject_reason":"fixture-secret"}]`, Partial, 1, 1},
		{"all-rejected", `[{"email":"first@example.test","status":"rejected"},{"email":"second@example.test","status":"invalid"}]`, Rejected, 0, 2},
		{"scheduled", `[{"email":"first@example.test","status":"scheduled","_id":"one"},{"email":"second@example.test","status":"queued","_id":"two"}]`, Accepted, 2, 0},
		{"missing", `[{"email":"first@example.test","status":"sent","_id":"one"}]`, Unknown, 0, 0},
		{"duplicate", `[{"email":"first@example.test","status":"sent","_id":"one"},{"email":"first@example.test","status":"sent","_id":"two"}]`, Unknown, 0, 0},
		{"unknown-recipient", `[{"email":"first@example.test","status":"sent","_id":"one"},{"email":"unknown@example.test","status":"sent","_id":"two"}]`, Unknown, 0, 0},
		{"unknown-status", `[{"email":"first@example.test","status":"sent","_id":"one"},{"email":"second@example.test","status":"mystery"}]`, Unknown, 0, 0},
		{"missing-id", `[{"email":"first@example.test","status":"sent"},{"email":"second@example.test","status":"queued","_id":"two"}]`, Unknown, 0, 0},
		{"error-envelope", `{"status":"error","message":"fixture-secret"}`, Unknown, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, tc.body) }))
			defer server.Close()
			result := Send(context.Background(), fixtureConfig(Mandrill, server.URL), fixtureMessage())
			if result.Status != tc.status || result.Accepted != (tc.status == Accepted) || result.AcceptedRecipients != tc.accepted || result.RejectedRecipients != tc.rejected {
				t.Fatalf("result=%+v", result)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "fixture-secret") || strings.Contains(string(encoded), "@") {
				t.Fatal("unsafe result", string(encoded))
			}
			if result.Status == Partial && (result.Recipients[0].Index != 0 || !result.Recipients[0].Accepted || result.Recipients[1].Index != 1 || result.Recipients[1].Accepted) {
				t.Fatal("incorrect recipient mapping", result)
			}
		})
	}
}

func TestHTTPRedirectRefusedAndClientPreserved(t *testing.T) {
	var forwarded atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(307)
	}))
	defer origin.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { t.Error("injected redirect policy invoked"); return nil }}
	result := New(Options{HTTPClient: client}).Send(context.Background(), fixtureConfig(Mailgun, origin.URL), fixtureMessage())
	if result.Accepted || result.HTTPStatus != 307 || forwarded.Load() != 0 || client.CheckRedirect == nil {
		t.Fatalf("redirect escaped: %+v forwarded=%d", result, forwarded.Load())
	}
}

func TestHTTPBoundedResponseAndMalformedAcknowledgement(t *testing.T) {
	for _, tc := range []struct{ body, code string }{
		{strings.Repeat("x", MaxResponseBytes+1), "response_too_large"},
		{`{"message":"success without ID"}`, "response"}, {`not JSON`, "response"},
		{`{"id":"` + strings.Repeat("x", 1025) + `"}`, "response"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, tc.body) }))
		result := Send(context.Background(), fixtureConfig(Mailgun, server.URL), fixtureMessage())
		server.Close()
		if result.Status != Unknown || result.ErrorCode != tc.code {
			t.Fatalf("result=%+v", result)
		}
	}
}

func TestHTTPTimeoutCancellationAndTLS(t *testing.T) {
	for _, callerDeadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "engine-timeout", true: "caller-timeout"}[callerDeadline], func(t *testing.T) {
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				<-r.Context().Done()
				close(closed)
			}))
			defer server.Close()
			engine := New(Options{Timeout: 100 * time.Millisecond})
			ctx := context.Background()
			if callerDeadline {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
				engine = New(Options{Timeout: time.Second})
			}
			start := time.Now()
			result := engine.Send(ctx, fixtureConfig(Mailgun, server.URL), fixtureMessage())
			if result.Accepted || result.ErrorCode != "canceled" || time.Since(start) > time.Second {
				t.Fatal(result)
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("connection not canceled")
			}
		})
	}
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_, _ = io.WriteString(w, `{"messageId":"tls-ack"}`)
	}))
	defer secure.Close()
	c := fixtureConfig(Brevo, secure.URL)
	if result := New(Options{HTTPClient: secure.Client()}).Send(context.Background(), c, fixtureMessage()); !result.Accepted {
		t.Fatalf("trusted TLS fixture: %+v", result)
	}
	if result := Send(context.Background(), c, fixtureMessage()); result.Accepted || result.Status != Unknown {
		t.Fatalf("untrusted TLS fixture: %+v", result)
	}
}

func TestWebhookCustomBodyAndTextOnlyBrevo(t *testing.T) {
	for _, kind := range []Kind{Webhook, Brevo} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			if kind == Webhook {
				if len(body) != 1 || body["content"] != "fixture" {
					t.Error("wrong custom body", body)
				}
				w.WriteHeader(202)
			} else {
				if body["textContent"] != "Connection failed" || body["htmlContent"] != nil {
					t.Error("wrong text body", body)
				}
				w.WriteHeader(201)
				_, _ = io.WriteString(w, `{"messageId":"ack"}`)
			}
		}))
		message := fixtureMessage()
		message.HTML = ""
		if kind == Webhook {
			message.APIBody = json.RawMessage(`{"content":"fixture"}`)
		}
		result := Send(context.Background(), fixtureConfig(kind, server.URL), message)
		server.Close()
		if !result.Accepted {
			t.Fatal(result)
		}
	}
}

func TestTimeoutDefaultsAndNoBodyReplay(t *testing.T) {
	if New(Options{}).timeout != DefaultTimeout || New(Options{Timeout: 2 * MaxTimeout}).timeout != MaxTimeout {
		t.Fatal("timeout bounds")
	}
	var inspected bool
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		inspected = true
		if r.GetBody != nil {
			t.Error("automatic replay enabled")
		}
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("missing request deadline")
		}
		return &http.Response{StatusCode: 204, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})}
	result := New(Options{HTTPClient: client}).Send(context.Background(), fixtureConfig(Webhook, "https://fixture.example"), fixtureMessage())
	if !inspected || !result.Accepted {
		t.Fatal(result)
	}
}
