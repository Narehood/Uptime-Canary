package checks

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestValidationRejectsInvalidConfigurationsWithoutSecrets(t *testing.T) {
	secret := "do-not-echo-this-secret"
	base := func() Config {
		return Config{Type: HTTP, Target: "http://127.0.0.1/?token=" + secret, Timeout: time.Second}
	}
	cases := map[string]func(*Config){
		"no timeout":       func(c *Config) { c.Timeout = 0 },
		"large timeout":    func(c *Config) { c.Timeout = 6 * time.Minute },
		"unsupported type": func(c *Config) { c.Type = Type(secret) },
		"userinfo":         func(c *Config) { c.Target = "http://admin:" + secret + "@127.0.0.1" },
		"fragment":         func(c *Config) { c.Target += "#" + secret },
		"port":             func(c *Config) { c.Target = "http://127.0.0.1:70000" },
		"signed port":      func(c *Config) { c.Type = TCP; c.Target = "127.0.0.1:+80" },
		"unbracketed IPv6": func(c *Config) { c.Target = "http://2001:db8::1:80" },
		"scheme":           func(c *Config) { c.Target = "file:///" + secret },
		"options":          func(c *Config) { c.DNS = &DNSConfig{} },
		"cleartext TLS":    func(c *Config) { c.TLS = &TLSConfig{} },
		"regex":            func(c *Config) { c.HTTP = &HTTPConfig{Keyword: &KeywordMatcher{Value: "[" + secret, Regex: true}} },
		"empty keyword":    func(c *Config) { c.HTTP = &HTTPConfig{Keyword: &KeywordMatcher{}} },
		"JSON script": func(c *Config) {
			c.HTTP = &HTTPConfig{JSON: &JSONMatcher{Path: "$[?(@." + secret + ")]", Expected: "true"}}
		},
		"JSON value": func(c *Config) { c.HTTP = &HTTPConfig{JSON: &JSONMatcher{Path: "$", Expected: secret}} },
		"JSON exists value": func(c *Config) {
			c.HTTP = &HTTPConfig{JSON: &JSONMatcher{Path: "$", Expected: "null", ExistsOnly: true}}
		},
		"HEAD body match": func(c *Config) { c.HTTP = &HTTPConfig{Method: "HEAD", Keyword: &KeywordMatcher{Value: secret}} },
		"header injection": func(c *Config) {
			c.HTTP = &HTTPConfig{Headers: map[string]string{"X-Test": secret + "\r\nInjected: yes"}}
		},
		"duplicate header": func(c *Config) { c.HTTP = &HTTPConfig{Headers: map[string]string{"X-Test": secret, "x-test": "other"}} },
		"transport header": func(c *Config) { c.HTTP = &HTTPConfig{Headers: map[string]string{"Host": secret}} },
		"auth conflict":    func(c *Config) { c.HTTP = &HTTPConfig{BasicAuth: &BasicAuth{Username: secret}, BearerToken: secret} },
		"basic username":   func(c *Config) { c.HTTP = &HTTPConfig{BasicAuth: &BasicAuth{Username: secret + ":"}} },
		"status range":     func(c *Config) { c.HTTP = &HTTPConfig{StatusRanges: []StatusRange{{Min: 200, Max: 199}}} },
		"large body":       func(c *Config) { c.HTTP = &HTTPConfig{Body: strings.Repeat("x", MaxBodyBytes+1)} },
		"redirects":        func(c *Config) { n := 11; c.HTTP = &HTTPConfig{MaxRedirects: &n} },
		"proxy auth":       func(c *Config) { c.HTTP = &HTTPConfig{ProxyURL: "http://admin:" + secret + "@127.0.0.1:1234"} },
		"proxy query":      func(c *Config) { c.HTTP = &HTTPConfig{ProxyURL: "http://127.0.0.1:1234/?key=" + secret} },
		"proxy conflict":   func(c *Config) { c.HTTP = &HTTPConfig{ProxyURL: "http://127.0.0.1:1234", UseEnvironmentProxy: true} },
		"DNS type":         func(c *Config) { c.Type = DNS; c.Target = "fixture.invalid"; c.DNS = &DNSConfig{RecordType: secret} },
		"DNS address": func(c *Config) {
			c.Type = DNS
			c.Target = "fixture.invalid"
			c.DNS = &DNSConfig{Server: secret + ":53"}
		},
		"DNS expectation": func(c *Config) {
			c.Type = DNS
			c.Target = "fixture.invalid"
			c.DNS = &DNSConfig{Expected: []string{secret}}
		},
		"PTR name":        func(c *Config) { c.Type = DNS; c.Target = secret; c.DNS = &DNSConfig{RecordType: "PTR"} },
		"TCP URL":         func(c *Config) { c.Type = TCP },
		"SMTP mode":       func(c *Config) { c.Type = SMTP; c.Target = "127.0.0.1:25"; c.SMTP = &SMTPConfig{TLSMode: secret} },
		"PEM":             func(c *Config) { c.Target = "https://127.0.0.1"; c.TLS = &TLSConfig{CAPEM: secret} },
		"client key pair": func(c *Config) { c.Target = "https://127.0.0.1"; c.TLS = &TLSConfig{ClientKeyPEM: secret} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := base()
			mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("validation leaked secret: %v", err)
			}
			r := Check(context.Background(), c)
			requireState(t, r, Down)
			if strings.Contains(r.Message, secret) {
				t.Fatalf("result leaked secret: %+v", r)
			}
		})
	}
}

func TestPreCancelledAndNilContext(t *testing.T) {
	c := Config{Type: TCP, Target: "127.0.0.1:1", Timeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	requireState(t, Check(ctx, c), Down)
	requireState(t, Check(nil, c), Down)
}

func TestJSONSelectorAndExactNumbers(t *testing.T) {
	for _, tc := range []struct {
		body, path, expected string
		exists, negate, want bool
	}{
		{`{"a":[{"key with space":1.00}]}`, `$.a[0]["key with space"]`, `1e0`, false, false, true},
		{`{"a":null}`, `$.a`, `null`, false, false, true},
		{`{}`, `$.a`, `null`, false, false, false},
		{`{}`, `$.a`, `null`, false, true, true},
		{`{"a":false}`, `$.a`, ``, true, false, true},
		{`{"a":"1"}`, `$.a`, `1`, false, false, false},
		{`9007199254740993`, `$`, `9007199254740992`, false, false, false},
		{`[1,-0.0,{"a":2}]`, `$`, `[1.0,0,{"a":2e0}]`, false, false, true},
		{`1e99999999999999999999`, `$`, `10e99999999999999999998`, false, false, true},
	} {
		got, err := matchesJSON([]byte(tc.body), JSONMatcher{Path: tc.path, Expected: tc.expected, ExistsOnly: tc.exists, Negate: tc.negate})
		if err != nil || got != tc.want {
			t.Errorf("body %s path %s: %v %v, want %v", tc.body, tc.path, got, err, tc.want)
		}
	}
	for _, s := range []string{"", "a", "$..a", "$[*]", "$[-1]", "$[01]", "$[10000000]", `$["bad\q"]`, "$['a']"} {
		if _, err := parseSelector(s); err == nil {
			t.Errorf("accepted unsupported selector %q", s)
		}
	}
	for _, s := range []string{`{} {}`, strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66), "1e" + strings.Repeat("9", 1024)} {
		if _, err := decodeJSON([]byte(s)); err == nil {
			t.Error("accepted unbounded/malformed JSON")
		}
	}
}
