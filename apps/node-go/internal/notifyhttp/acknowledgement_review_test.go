package notifyhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Exact counterexamples from the independent source review. Existing native
// contract fixtures remain unchanged.
func TestAcknowledgementReviewGotifyRequiresCompleteReceipt(t *testing.T) {
	for _, body := range []string{
		`{"id":1}`,
		`{"id":1,"appid":"wrong-type","message":null,"date":null}`,
		`{"id":1,"appid":4,"message":"fixture","date":"invalid"}`,
		`{"id":1,"appid":0,"message":"fixture","date":"2026-10-22T00:00:00Z"}`,
	} {
		t.Run(body, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				io.WriteString(w, body)
			}))
			defer s.Close()
			got := testEngine(t, Options{}).Send(context.Background(), fixtureConfig(Gotify, s.URL), Message{Text: "fixture"})
			requireResult(t, got, Unknown)
			if got.ProviderID != "" || got.HTTPStatus != 200 || calls.Load() != 1 {
				t.Fatal("incomplete receipt gained authority or caused another request")
			}
		})
	}
}

func TestAcknowledgementReviewTelegramPermitsSuccessDescription(t *testing.T) {
	sum := sha256.Sum256([]byte("-1000123456789:123"))
	for _, description := range []string{`"Message sent"`, `""`, `null`, `123`, `{}`} {
		t.Run(description, func(t *testing.T) {
			var calls atomic.Int32
			body := `{"ok":true,"description":` + description + `,"result":{"message_id":123,"chat":{"id":-1000123456789,"type":"supergroup"}}}`
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				io.WriteString(w, body)
			}))
			defer s.Close()
			got := testEngine(t, Options{}).Send(context.Background(), fixtureConfig(Telegram, s.URL), Message{Text: "fixture"})
			want := Unknown
			if strings.HasPrefix(description, `"`) {
				want = Accepted
			}
			requireResult(t, got, want)
			if calls.Load() != 1 || got.HTTPStatus != 200 || got.RetryAfter != 0 {
				t.Fatal("acknowledgement produced a retry or changed HTTP evidence")
			}
			if want == Accepted && (got.ProviderID != hex.EncodeToString(sum[:]) || got.ErrorCode != "" || got.ErrorMessage != "") {
				t.Fatal("valid native receipt lost its message identity")
			}
		})
	}
}

func TestAcknowledgementReviewPEMDoesNotSkipMalformedBlocks(t *testing.T) {
	ca, _ := privateCertificate(t, false)
	for _, prefix := range []string{
		"-----BEGIN CERTIFICATE-----\n!\n-----END CERTIFICATE-----\n",
		"-----BEGIN CERTIFICATE-----\n",
	} {
		opts := Options{AdditionalRootCAPEM: append([]byte(prefix), ca...)}
		engine, err := New(opts)
		if engine != nil {
			engine.CloseIdleConnections()
		}
		if engine != nil || err != errOptions {
			t.Fatal("malformed prefix was skipped instead of rejecting trust input")
		}
	}
	engine, err := New(Options{AdditionalRootCAPEM: ca})
	if err != nil || engine == nil {
		t.Fatal("valid isolated CA no longer constructs an engine")
	}
	engine.CloseIdleConnections()
}

func TestAcknowledgementReviewGotifyStrictDate(t *testing.T) {
	cases := []struct {
		date string
		want Status
	}{
		{"2026-10-22T0:00:00Z", Unknown},
		{"2026-10-22T00:00:00,5Z", Unknown},
		{"2026-10-22T00:00:00+24:00", Unknown},
		{"2026-10-22T00:00:00Z", Accepted},
		{"2026-10-22T00:00:00.000Z", Accepted},
		{"2018-02-27T19:36:10.5045044+01:00", Accepted},
	}
	for _, tc := range cases {
		t.Run(tc.date, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				io.WriteString(w, `{"id":1,"appid":4,"message":"fixture","date":"`+tc.date+`"}`)
			}))
			defer s.Close()
			got := testEngine(t, Options{}).Send(context.Background(), fixtureConfig(Gotify, s.URL), Message{Text: "fixture"})
			requireResult(t, got, tc.want)
			if calls.Load() != 1 || got.HTTPStatus != 200 || tc.want == Unknown && got.ProviderID != "" {
				t.Fatal("receipt date changed request count or gained unexpected authority")
			}
		})
	}
}

func TestAcknowledgementReviewConnectionQueueHonorsCancellation(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		io.WriteString(w, goodReply(Gotify))
	}))
	defer s.Close()
	defer close(release)
	engine := testEngine(t, Options{Timeout: time.Second})
	config := fixtureConfig(Gotify, s.URL)
	results := make(chan Result, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- engine.Send(context.Background(), config, Message{Text: "fixture"}) }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("fixture did not occupy the two connection slots")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	got := engine.Send(ctx, config, Message{Text: "queued fixture"})
	requireResult(t, got, Unknown)
	if ctx.Err() != context.DeadlineExceeded || calls.Load() != 2 || got.HTTPStatus != 0 {
		t.Fatal("queued cancellation reached a provider or escaped its parent deadline")
	}
	// Releasing occurs in the deferred cleanup so both blocked requests can exit;
	// their completion is observed by a cleanup registered after that release.
	t.Cleanup(func() {
		for i := 0; i < 2; i++ {
			select {
			case result := <-results:
				requireResult(t, result, Accepted)
			case <-time.After(time.Second):
				t.Fatal("fixture request remained blocked after cleanup")
			}
		}
	})
}
