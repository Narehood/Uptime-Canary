package gamedigmonitor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func input(endpoint string) Config {
	return Config{Target: "127.0.0.1", Timeout: time.Second, Options: Options{Game: "minecraft", Port: 25565, GivenPortOnly: true, Endpoint: endpoint, BearerToken: "private-query-fixture-0123456789", AllowInsecureHTTP: true}}
}

func TestGameCatalogueAndConfigurationAdmission(t *testing.T) {
	if len(games) != 358 || !KnownGame("minecraft") || !KnownGame("csgo") || KnownGame("protocol-minecraftvanilla") {
		t.Fatal("pinned game catalogue drifted")
	}
	c := input("http://127.0.0.1:3121")
	if Validate(c) != nil {
		t.Fatal("valid query profile refused")
	}
	missingDefault := 0
	for game, port := range games {
		candidate := c
		candidate.Game, candidate.Port, candidate.GivenPortOnly = game, 0, false
		if port == 0 {
			missingDefault++
			if Validate(candidate) == nil {
				t.Fatal("game without a declared default admitted an omitted port")
			}
			candidate.Port = 12345
			if Validate(candidate) != nil {
				t.Fatal("explicit port did not satisfy the missing default")
			}
		} else if Validate(candidate) != nil {
			t.Fatal("declared upstream default was refused")
		}
	}
	if missingDefault == 0 {
		t.Fatal("missing-default negative control disappeared")
	}
	for _, edit := range []func(*Config){func(c *Config) { c.Game = "unknown" }, func(c *Config) { c.Target = "host:123" }, func(c *Config) { c.Port = 65536 }, func(c *Config) { c.Port = 0 }, func(c *Config) { c.Endpoint = "https://user:pass@service" }, func(c *Config) { c.Endpoint = "https://service/path" }, func(c *Config) { c.AllowInsecureHTTP = false }, func(c *Config) { c.BearerToken = "short" }} {
		bad := c
		edit(&bad)
		if Validate(bad) == nil {
			t.Fatal("invalid game query profile admitted")
		}
	}
	raw, _ := json.Marshal(c.Options)
	for _, field := range []string{`,"Game":"minecraft"}`, `,"game":"minecraft"}`, `,"unknown":true}`, `,"game|port":1}`, `,"token":null}`} {
		var out Options
		if json.Unmarshal(append(append([]byte{}, raw[:len(raw)-1]...), []byte(field)...), &out) == nil {
			t.Fatal("ambiguous game options admitted", field)
		}
	}
}

func TestGameServiceRequiresFreshExactBoundedEvidence(t *testing.T) {
	for _, mode := range []string{"up", "down", "pending", "wrong-id", "wrong-version", "missing-latency", "null-latency", "alias", "oversize", "redirect", "refused"} {
		t.Run(mode, func(t *testing.T) {
			service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/v1/query" || r.Header.Get("Authorization") != "Bearer private-query-fixture-0123456789" || r.Header.Get("Cookie") != "" {
					t.Error("query request authority changed")
					w.WriteHeader(400)
					return
				}
				var fields map[string]json.RawMessage
				if json.NewDecoder(r.Body).Decode(&fields) != nil || string(fields["game"]) != `"minecraft"` || string(fields["host"]) != `"127.0.0.1"` || string(fields["givenPortOnly"]) != `true` {
					t.Error("query protocol changed")
					w.WriteHeader(400)
					return
				}
				var id string
				json.Unmarshal(fields["id"], &id)
				value := map[string]any{"id": id, "profile": Profile, "state": "up", "latencyMs": 2.5}
				switch mode {
				case "down", "pending":
					value["state"], value["latencyMs"] = mode, 0
				case "wrong-id":
					value["id"] = "stale-result"
				case "wrong-version":
					value["profile"] = "future"
				case "missing-latency":
					delete(value, "latencyMs")
				case "null-latency":
					value["latencyMs"] = nil
				case "alias":
					delete(value, "state")
					value["State"] = "up"
				case "oversize":
					w.Write([]byte(strings.Repeat("x", 2049)))
					return
				case "redirect":
					w.Header().Set("Location", "/other")
					w.WriteHeader(302)
					return
				case "refused":
					w.WriteHeader(401)
					return
				}
				json.NewEncoder(w).Encode(value)
			}))
			defer service.Close()
			r := Check(context.Background(), input(service.URL))
			want := "pending"
			if mode == "up" || mode == "down" {
				want = mode
			}
			if r.State != want || mode == "up" && r.LatencyMs != 2.5 || mode != "up" && r.LatencyMs != 0 {
				t.Fatal("wrong query disposition", r)
			}
			if r.Message != Available && r.Message != Failed && r.Message != Unavailable {
				t.Fatal("raw game/provider data leaked")
			}
		})
	}
}
