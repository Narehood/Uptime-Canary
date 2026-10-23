package steam

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"uptime-canary/apps/node-go/internal/icmp"
)

const privateKeyFixture = "fictional-key-never-send-to-Steam"

func fixtureConfig(base string) Config {
	return Config{Target: "127.0.0.9:27015", Timeout: 2 * time.Second,
		Options: Options{APIKey: privateKeyFixture, APIURL: base, AllowInsecureAPI: true, SkipLatency: true}}
}

func TestSteamConfigurationAndExactOptions(t *testing.T) {
	base := fixtureConfig("http://127.0.0.1:1")
	if Validate(base) != nil {
		t.Fatal("valid bounded configuration refused")
	}
	for _, edit := range []func(*Config){
		func(c *Config) { c.Target = "127.0.0.1" }, func(c *Config) { c.Target = "-switch:1" },
		func(c *Config) { c.Target = "127.0.0.1:0" }, func(c *Config) { c.Target = "127.0.0.1:65536" },
		func(c *Config) { c.Target = "127.0.0.1:00080" }, func(c *Config) { c.Target = "[::1%zone]:1" },
		func(c *Config) { c.Timeout = 0 }, func(c *Config) { c.Timeout = 2*time.Minute + 1 },
		func(c *Config) { c.Options.APIKey = "" }, func(c *Config) { c.Options.APIKey = "private\nkey" },
		func(c *Config) { c.Options.APIKey = strings.Repeat("x", 257) },
		func(c *Config) { c.Options.AllowInsecureAPI = false },
		func(c *Config) { c.Options.APIURL = "https://user:private@example.test" },
		func(c *Config) { c.Options.APIURL = "https://example.test?key=private" },
		func(c *Config) { c.Options.APIURL = "https://example.test#private" },
		func(c *Config) { c.Options.APIURL = "https://example.test/a/../b" },
		func(c *Config) { c.Options.APIURL = "https://example.test/a%2fb" },
		func(c *Config) { c.Options.APICA = "malformed private CA" },
		func(c *Config) { v := -1; c.Options.PacketSize = &v },
		func(c *Config) { v := 65501; c.Options.PacketSize = &v },
	} {
		c := base
		edit(&c)
		if err := Validate(c); err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("invalid configuration or unsafe diagnostics", err)
		}
	}
	for _, value := range []string{`{"apiKey":"fixture"}`, `{"apiKey":"fixture","packetSize":0,"skipLatency":false}`} {
		var o Options
		if json.Unmarshal([]byte(value), &o) != nil {
			t.Fatal("valid options rejected")
		}
	}
	for _, value := range []string{`null`, `[]`, `{}`, `{"APIKey":"x"}`, `{"apiKey":"x","apiKey":"y"}`,
		`{"apiKey":null}`, `{"apiKey":"x","packetSize":null}`, `{"apiKey":"x","packetSize":0.5}`,
		`{"apiKey":"x","unknown":true}`, `{"apiKey":"x","skipLatency":"true"}`, `{"apiKey":"x"}{}`} {
		var o Options
		if json.Unmarshal([]byte(value), &o) == nil {
			t.Fatal("ambiguous options accepted", value)
		}
	}
}

func TestSteamDirectoryRequiresExactCompleteMembershipEvidence(t *testing.T) {
	ip := net.ParseIP("127.0.0.9")
	for _, test := range []struct {
		raw           string
		listed, valid bool
	}{
		{`{"response":{"servers":[{"addr":"127.0.0.9:27015","name":"private metadata"}]}}`, true, true},
		{`{"response":{"servers":[]}}`, false, true}, {`{"response":{}}`, false, true},
		{`{"response":{"servers":[{"addr":"127.0.0.8:27015"}]}}`, false, false},
		{`{"response":{"servers":[{"addr":"127.0.0.9:27016"}]}}`, false, false},
		{`{"response":{"servers":[{"addr":"127.0.0.9:27015"},null]}}`, false, false},
		{`{"response":{"servers":[{"Addr":"127.0.0.9:27015"}]}}`, false, false},
		{`{"response":{"servers":null}}`, false, false}, {`{"response":null}`, false, false},
		{`{"response":{"error":"private"}}`, false, false}, {`{}`, false, false},
		{`{"Response":{"servers":[]}}`, false, false},
		{`{"response":{"servers":[],"error":"private"}}`, false, false},
		{`{"response":{"servers":[],"servers":[{"addr":"127.0.0.9:27015"}]}}`, false, false},
		{`{"response":{"servers":[{"addr":"127.0.0.8:27015","addr":"127.0.0.9:27015"}]}}`, false, false},
		{`{"response":{"servers":[]}}{}`, false, false},
	} {
		listed, valid := directoryResult([]byte(test.raw), ip, "27015")
		if valid != test.valid || valid && listed != test.listed {
			t.Fatalf("membership evidence: listed=%v valid=%v expected=%v/%v", listed, valid, test.listed, test.valid)
		}
	}
	for _, raw := range []string{strings.Repeat(" ", 65537), `{"response":` + strings.Repeat("[", 40) + "0" + strings.Repeat("]", 40) + "}",
		`{"response":{"servers":[` + strings.Repeat(`{"addr":"127.0.0.9:27015"},`, 128) + `{"addr":"127.0.0.9:27015"}]}}`} {
		if _, valid := directoryResult([]byte(raw), ip, "27015"); valid {
			t.Fatal("oversized or excessively nested authority accepted")
		}
	}
	for _, addr := range []string{"[2001:db8::9]:27015", "2001:db8::9:27015"} {
		raw, _ := json.Marshal(map[string]any{"response": map[string]any{"servers": []any{map[string]string{"addr": addr}}}})
		if listed, valid := directoryResult(raw, net.ParseIP("2001:db8::9"), "27015"); !listed || !valid {
			t.Fatal("IPv6 membership refused", addr)
		}
	}
}

func TestSteamRequestContractPrivacyAndRealRTTOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/prefix"+endpoint || r.URL.Query().Get("key") != privateKeyFixture ||
			r.URL.Query().Get("filter") != "addr\\127.0.0.9:27015" || r.URL.Query().Get("limit") != "1" || len(r.URL.Query()) != 3 ||
			r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Accept") != "application/json" {
			t.Error("unexpected directory request contract")
		}
		_, _ = fmt.Fprintf(w, `{"response":{"servers":[{"addr":"127.0.0.9:27015","name":%q}]}}`, privateKeyFixture)
	}))
	defer server.Close()
	c := fixtureConfig(server.URL + "/prefix")
	c.Options.SkipLatency = false
	zero := 0
	c.Options.PacketSize = &zero
	var called bool
	deps := dependencies{
		lookup: func(context.Context, string) ([]net.IPAddr, error) {
			t.Fatal("literal IP performed DNS")
			return nil, nil
		},
		ping: func(ctx context.Context, config icmp.Config) icmp.Result {
			called = true
			if config.Target != "127.0.0.9" || config.Count != 1 || config.Size == nil || *config.Size != 0 || config.Timeout <= 0 || config.Timeout > time.Second {
				t.Fatal("invalid bounded ICMP measurement", config)
			}
			return icmp.Result{State: icmp.Up, LatencyMs: 7.25, LatencyAvailable: true}
		},
	}
	r := check(context.Background(), c, deps)
	if r.State != "up" || !called || !r.LatencyAvailable || r.LatencyMs != 7.25 || strings.Contains(r.Message, privateKeyFixture) {
		t.Fatal("positive membership or RTT/privacy was lost", r)
	}
	deps.ping = func(context.Context, icmp.Config) icmp.Result { return icmp.Result{State: icmp.Unknown} }
	if r := check(context.Background(), c, deps); r.State != "up" || r.LatencyAvailable || r.LatencyMs != 0 {
		t.Fatal("unavailable ICMP changed directory membership or invented latency", r)
	}
	c.Options.SkipLatency = true
	deps.ping = func(context.Context, icmp.Config) icmp.Result {
		t.Fatal("disabled measurement ran")
		return icmp.Result{}
	}
	if r := check(context.Background(), c, deps); r.State != "up" || r.LatencyAvailable {
		t.Fatal(r)
	}
}

func TestSteamProviderFailuresNeverImplyMembership(t *testing.T) {
	var code atomic.Int32
	code.Store(429)
	var body atomic.Value
	body.Store(`{"error":"` + privateKeyFixture + `"}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(code.Load()))
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	defer server.Close()
	c := fixtureConfig(server.URL)
	for _, status := range []int{400, 401, 403, 429, 500, 503} {
		code.Store(int32(status))
		for _, policy := range []bool{false, true} {
			c.Options.APIFailureIsDown = policy
			want := "pending"
			if policy {
				want = "down"
			}
			r := Check(context.Background(), c)
			if r.State != want || r.LatencyAvailable || strings.Contains(r.Message, privateKeyFixture) {
				t.Fatal("provider failure attribution/privacy", status, r)
			}
		}
	}
	code.Store(200)
	c.Options.APIFailureIsDown = false
	for _, malformed := range []string{`{"response":{"servers":null}}`, `{"response":{"servers":[{"addr":"127.0.0.8:27015"}]}}`,
		`{"response":{"servers":[]`, strings.Repeat(" ", 65537)} {
		body.Store(malformed)
		if r := Check(context.Background(), c); r.State != "pending" || r.LatencyAvailable {
			t.Fatal("invalid response invented authority", r)
		}
	}
	body.Store(`{"response":{}}`)
	if r := Check(context.Background(), c); r.State != "down" || r.LatencyAvailable {
		t.Fatal("valid empty directory did not report absence", r)
	}
}

func TestSteamTLSRootsAndRedirectIsolation(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"response":{"servers":[{"addr":"127.0.0.9:27015"}]}}`))
	})
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	c := fixtureConfig(server.URL)
	if r := Check(context.Background(), c); r.State != "pending" {
		t.Fatal("untrusted fixture CA was accepted", r)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	c.Options.APICA = string(ca)
	if r := Check(context.Background(), c); r.State != "up" || r.LatencyAvailable {
		t.Fatal("additive trusted CA failed", r)
	}
	for _, malformed := range []string{"garbage\n" + string(ca), "-----BEGIN CERTIFICATE-----\n!broken\n" + string(ca), string(ca) + "trailing"} {
		copy := c
		copy.Options.APICA = malformed
		if Validate(copy) == nil {
			t.Fatal("malformed PEM was skipped")
		}
	}
	var leaked atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer sink.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, sink.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	c = fixtureConfig(redirect.URL)
	if r := Check(context.Background(), c); r.State != "pending" || leaked.Load() != 0 {
		t.Fatal("redirect forwarded directory credentials", r, leaked.Load())
	}
}

func TestSteamDeadlineCancellationAndDNSPreference(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := fixtureConfig("http://127.0.0.1:1")
	c.Options.APIFailureIsDown = true
	if r := Check(ctx, c); r.State != "pending" {
		t.Fatal("local cancellation became target failure", r)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	c = fixtureConfig(server.URL)
	c.Timeout = 50 * time.Millisecond
	started := time.Now()
	if r := Check(context.Background(), c); r.State != "pending" || time.Since(started) > time.Second {
		t.Fatal("directory deadline or pending policy failed", r)
	}
	chosen, err := resolve(context.Background(), "server.example.test", func(ctx context.Context, host string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("2001:db8::9")}, {IP: net.ParseIP("192.0.2.9")}}, nil
	})
	if err != nil || chosen.String() != "192.0.2.9" {
		t.Fatal("directory filter no longer prefers IPv4", chosen, err)
	}
	if _, err := resolve(context.Background(), "server.example.test", func(context.Context, string) ([]net.IPAddr, error) { return nil, errors.New("private resolver error") }); err == nil {
		t.Fatal("failed resolver produced an address")
	}
}
