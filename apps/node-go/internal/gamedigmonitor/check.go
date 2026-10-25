// Package gamedigmonitor asks a dedicated, authenticated GameDig query service
// for one bounded game-server query. The Go/SQLite node owns policy and HA;
// optional GameDig's JavaScript protocols run in the separate service.
package gamedigmonitor

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"
	"unicode"
	"unicode/utf8"

	"uptime-canary/apps/node-go/internal/jsonshape"
	"uptime-canary/apps/node-go/internal/tailscalemonitor"
)

const Type = "gamedig"
const Profile = "gamedig/5.3.3"
const Available = "Game server replied"
const Failed = "Game server query failed"
const Unavailable = "Game query service unavailable"

//go:embed catalogue.json
var catalogue []byte

var games = func() map[string]int {
	var input struct {
		Version string `json:"version"`
		Games   []struct {
			ID          string `json:"id"`
			DefaultPort int    `json:"defaultPort"`
		} `json:"games"`
	}
	if json.Unmarshal(catalogue, &input) != nil || input.Version != "5.3.3" {
		panic("invalid embedded game catalogue")
	}
	out := map[string]int{}
	for _, game := range input.Games {
		_, exists := out[game.ID]
		if game.ID == "" || exists || game.DefaultPort < 0 || game.DefaultPort > 65535 {
			panic("invalid embedded game catalogue")
		}
		out[game.ID] = game.DefaultPort
	}
	if len(out) < 100 || len(out) > 1024 {
		panic("invalid embedded game catalogue")
	}
	return out
}()

type Options struct {
	Game              string `json:"game"`
	Port              int    `json:"port,omitempty"`
	GivenPortOnly     bool   `json:"givenPortOnly,omitempty"`
	Token             string `json:"token,omitempty"`
	Endpoint          string `json:"endpoint"`
	BearerToken       string `json:"bearerToken"`
	AllowInsecureHTTP bool   `json:"allowInsecureHTTP,omitempty"`
}
type Config struct {
	Target  string
	Timeout time.Duration
	Options
}
type Result struct {
	State, Message string
	LatencyMs      float64
}

func KnownGame(id string) bool { _, exists := games[id]; return exists }

func Validate(c Config) error {
	// Both engines accept one literal IP or ASCII DNS name, without flags,
	// ports, credentials or command syntax. This call performs no I/O.
	if tailscalemonitor.Validate(tailscalemonitor.Config{Name: c.Target, Timeout: c.Timeout}) != nil || c.Timeout <= 0 || !KnownGame(c.Game) || c.Port < 0 || c.Port > 65535 || c.GivenPortOnly && c.Port == 0 || c.Port == 0 && games[c.Game] == 0 {
		return errConfig
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Path != "" && u.Path != "/" || len(c.Endpoint) > 2048 || u.Scheme != "https" && (u.Scheme != "http" || !c.AllowInsecureHTTP) {
		return errConfig
	}
	if len(c.BearerToken) < 24 || len(c.BearerToken) > 256 {
		return errConfig
	}
	for _, ch := range c.BearerToken {
		if ch < 33 || ch > 126 {
			return errConfig
		}
	}
	if !utf8.ValidString(c.Token) || len(c.Token) > 1024 {
		return errConfig
	}
	for _, ch := range c.Token {
		if unicode.IsControl(ch) {
			return errConfig
		}
	}
	return nil
}

var slots = make(chan struct{}, 2)

func unavailable() Result { return Result{State: "pending", Message: Unavailable} }

func Check(ctx context.Context, c Config) Result {
	if Validate(c) != nil {
		return Result{State: "down", Message: "invalid game query configuration"}
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	if ctx.Err() != nil {
		return unavailable()
	}
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	default:
		return unavailable()
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return unavailable()
	}
	id := hex.EncodeToString(nonce[:])
	request := struct {
		ID            string `json:"id"`
		Game          string `json:"game"`
		Host          string `json:"host"`
		Port          int    `json:"port,omitempty"`
		GivenPortOnly bool   `json:"givenPortOnly,omitempty"`
		Token         string `json:"token,omitempty"`
		TimeoutMs     int64  `json:"timeoutMs"`
	}{id, c.Game, c.Target, c.Port, c.GivenPortOnly, c.Token, (c.Timeout * 3 / 4).Milliseconds()}
	body, err := json.Marshal(request)
	if err != nil || len(body) > 8192 {
		return unavailable()
	}
	u, _ := url.Parse(c.Endpoint)
	u.Path = "/v1/query"
	r, err := http.NewRequestWithContext(ctx, "POST", u.String(), bytes.NewReader(body))
	if err != nil {
		return unavailable()
	}
	r.Header.Set("Authorization", "Bearer "+c.BearerToken)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Cache-Control", "no-store")
	t := &http.Transport{MaxResponseHeaderBytes: 16 << 10, DisableKeepAlives: true, DisableCompression: true}
	defer t.CloseIdleConnections()
	client := &http.Client{Transport: t, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(r)
	if err != nil {
		return unavailable()
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2049))
	if err != nil || len(raw) > 2048 || response.StatusCode != 200 || jsonshape.Validate(raw) != nil {
		return unavailable()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 4 || fields["id"] == nil || fields["profile"] == nil || fields["state"] == nil || fields["latencyMs"] == nil {
		return unavailable()
	}
	for _, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return unavailable()
		}
	}
	var result struct {
		ID      string  `json:"id"`
		Profile string  `json:"profile"`
		State   string  `json:"state"`
		Latency float64 `json:"latencyMs"`
	}
	if ctx.Err() != nil || json.Unmarshal(raw, &result) != nil || result.ID != id || result.Profile != Profile || result.Latency < 0 || result.Latency > float64(c.Timeout)/float64(time.Millisecond) {
		return unavailable()
	}
	switch result.State {
	case "up":
		return Result{State: "up", Message: Available, LatencyMs: result.Latency}
	case "down":
		if result.Latency == 0 {
			return Result{State: "down", Message: Failed}
		}
	}
	return unavailable()
}

func (o *Options) UnmarshalJSON(raw []byte) error {
	if jsonshape.Validate(raw) != nil {
		return errConfig
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields["game"] == nil || fields["endpoint"] == nil || fields["bearerToken"] == nil {
		return errConfig
	}
	for key, value := range fields {
		if key != "game" && key != "port" && key != "givenPortOnly" && key != "token" && key != "endpoint" && key != "bearerToken" && key != "allowInsecureHTTP" || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errConfig
		}
	}
	type plain Options
	var out plain
	if json.Unmarshal(raw, &out) != nil {
		return errConfig
	}
	*o = Options(out)
	return nil
}
