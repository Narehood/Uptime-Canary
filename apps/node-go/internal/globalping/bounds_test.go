package globalping

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStrictJSONBoundsAndUnicode(t *testing.T) {
	for _, s := range []string{`{"a":1,"a":2}`, `{"a":1,"A":2}`, `{"\u0061":1,"a":2}`, `[1,]`, `{"a":}`, `true false`,
		`"\ud800"`, `"\udc00"`, `"\ud800\u0041"`, strings.Repeat("[", 34) + `0` + strings.Repeat("]", 34), `"` + string([]byte{0xff}) + `"`, strings.Repeat("[0,", 34000) + `0` + strings.Repeat("]", 34000)} {
		if _, err := decodeJSON([]byte(s)); err == nil {
			t.Fatal("malformed or unbounded JSON admitted")
		}
	}
	for _, s := range []string{`null`, `true`, `[]`, `{}`, `"\ud83d\ude00"`, `"\\ud800"`, `{"literal":"escaped \" quote"}`, `{"a":1,"b":[2,3]}`} {
		if _, err := decodeJSON([]byte(s)); err != nil {
			t.Fatalf("valid JSON rejected %s", s)
		}
	}
	// Separate node admission from depth admission.
	flat := `[` + strings.Repeat(`0,`, 100000) + `0]`
	if _, err := decodeJSON([]byte(flat)); err == nil {
		t.Fatal("JSON node budget not enforced")
	}
}

func TestPrivateAPIBudgetsAtBoundary(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer s.Close()
	p, err := prepare(Config{Target: "example.com", Timeout: time.Second, Options: Options{Mode: "ping", Location: "US", Ping: &PingOptions{}, APIURL: s.URL, AllowInsecureAPI: true}})
	if err != nil {
		t.Fatal(err)
	}
	a, tr, err := newAPIClient(p)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.CloseIdleConnections()
	if a.remaining != 8<<20 || maxPolls != 120 || maxResponseBytes != 1<<20 {
		t.Fatal("resource contract changed")
	}
	a.polls = maxPolls - 1
	if _, _, _, err := a.request(context.Background(), p, "GET", p.api.String(), nil, ""); err != nil {
		t.Fatal("last admitted poll failed")
	}
	if _, _, _, err := a.request(context.Background(), p, "GET", p.api.String(), nil, ""); err == nil || hits.Load() != 1 {
		t.Fatal("poll cap exceeded")
	}
	a.polls = 0
	a.remaining = 2
	if _, _, _, err := a.request(context.Background(), p, "GET", p.api.String(), nil, ""); err == nil {
		t.Fatal("aggregate body bound exceeded")
	}
	if a.remaining < 0 {
		t.Fatal("read crossed aggregate budget")
	}
	a.remaining = 0
	before := hits.Load()
	if _, _, _, err := a.request(context.Background(), p, "GET", p.api.String(), nil, ""); err == nil || hits.Load() != before {
		t.Fatal("network work after body budget exhaustion")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.remaining = 100
	if _, _, _, err := a.request(ctx, p, "POST", p.api.String(), []byte(`{}`), ""); err == nil || hits.Load() != before {
		t.Fatal("network work after cancellation")
	}
}

func TestETagValidation(t *testing.T) {
	for _, s := range []string{`"opaque"`, `W/"opaque"`, `""`} {
		if !validETag(s) {
			t.Fatal("valid ETag rejected")
		}
	}
	for _, s := range []string{"", "unquoted", "\"secret\r\n\"", `"one","two"`, `"a"b"`, strings.Repeat("x", 257)} {
		if validETag(s) {
			t.Fatal("unsafe ETag admitted")
		}
	}
}

func TestZeroIsMeasuredOnlyWhenReported(t *testing.T) {
	p, err := prepare(Config{Target: "example.com", Timeout: time.Second, Options: Options{Mode: "ping", Location: "US", Ping: &PingOptions{Packets: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := decodeObject([]byte(`{"stats":{"min":0,"avg":0,"max":0,"total":1,"rcv":1,"drop":0,"loss":0},"timings":[{"rtt":0,"ttl":64}]}`))
	if err != nil {
		t.Fatal(err)
	}
	r := pingResult(p, v)
	if r.State != Up || !r.LatencyAvailable || r.LatencyMs != 0 {
		t.Fatalf("reported zero lost %+v", r)
	}
	delete(v["stats"].(object), "avg")
	r = pingResult(p, v)
	if r.State != Pending || r.LatencyAvailable {
		t.Fatalf("missing timing became zero measurement %+v", r)
	}
}

func TestResultJSONContract(t *testing.T) {
	b, _ := json.Marshal(Options{Mode: "ping", Location: "US", APIURL: "https://example.com", Token: "token", APICA: "cert", AllowInsecureAPI: true, APIFailureIsDown: true, Ping: &PingOptions{}})
	for _, key := range []string{`"mode"`, `"location"`, `"apiUrl"`, `"token"`, `"apiCa"`, `"allowInsecureApi"`, `"apiFailureIsDown"`, `"ping"`} {
		if !strings.Contains(string(b), key) {
			t.Fatal("camelCase option contract changed")
		}
	}
}
