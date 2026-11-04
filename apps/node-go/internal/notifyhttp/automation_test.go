package notifyhttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func automationConfig(kind Kind, base string) Config {
	if kind == HomeAssistant {
		return Config{Kind: kind, HomeAssistant: &HomeAssistantConfig{BaseURL: base, AccessToken: "fixture-secret", EventType: "uptime_canary", AllowInsecureHTTP: true}}
	}
	return Config{Kind: kind, Matrix: &MatrixConfig{BaseURL: base, AccessToken: "fixture-secret", RoomID: "!ops:example.test", AllowInsecureHTTP: true}}
}

func TestAutomationProviderWireAndAcknowledgement(t *testing.T) {
	for _, kind := range []Kind{HomeAssistant, Matrix} {
		t.Run(string(kind), func(t *testing.T) {
			status, response := 200, `{"message":"Event uptime_canary fired."}`
			if kind == Matrix {
				response = `{"event_id":"$fixture-event"}`
			}
			calls := 0
			sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer fixture-secret" || r.URL.RawQuery != "" {
					t.Error("invalid authentication")
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("invalid JSON")
				}
				if kind == HomeAssistant {
					if r.Method != "POST" || r.URL.Path != "/prefix/api/events/uptime_canary" || body["kind"] != "down" || body["monitor_id"] != "monitor" || body["idempotency_key"] != "stable-attempt" {
						t.Error("invalid Home Assistant event", r.Method, r.URL.Path, body)
					}
				} else {
					if r.Method != "PUT" || r.URL.Path != "/prefix/_matrix/client/v3/rooms/!ops:example.test/send/m.room.message/stable-attempt" || body["body"] != "Down\nLocal fixture" || body["msgtype"] != "m.notice" {
						t.Error("invalid Matrix event", r.Method, r.URL.Path, body)
					}
				}
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(status)
				io.WriteString(w, response)
			}))
			defer sink.Close()
			c := automationConfig(kind, sink.URL+"/prefix")
			m := Message{Title: "Down", Text: "Local fixture", EventKind: "down", MonitorID: "monitor", IdempotencyKey: "stable-attempt"}
			if r := Send(context.Background(), c, m); !r.Accepted || r.ErrorCode != "" {
				t.Fatal(r)
			}
			// An HTTP success with a missing/foreign acknowledgement is ambiguous.
			response = `{}`
			if r := Send(context.Background(), c, m); r.Accepted || r.Status != Unknown {
				t.Fatal(r)
			}
			status, response = 429, `{"errcode":"M_LIMIT_EXCEEDED","retry_after_ms":3500}`
			r := Send(context.Background(), c, m)
			want := 2 * time.Second
			if kind == Matrix {
				want = 3500 * time.Millisecond
			}
			if r.Accepted || r.Status != Retryable || r.RetryAfter != want {
				t.Fatal(r)
			}
			status, response = 302, "fixture-secret"
			if r = Send(context.Background(), c, m); r.Accepted || r.ErrorCode != "redirect_refused" || strings.Contains(r.ErrorMessage, "fixture-secret") {
				t.Fatal(r)
			}
			if calls != 4 {
				t.Fatal("implicit replay", calls)
			}
		})
	}
}

func TestAutomationConfigurationAndRouteIdentity(t *testing.T) {
	for _, kind := range []Kind{HomeAssistant, Matrix} {
		c := automationConfig(kind, "https://automation.example.test")
		before, err := Identity(c)
		if err != nil {
			t.Fatal(err)
		}
		if kind == HomeAssistant {
			c.HomeAssistant.AccessToken = "rotated"
		} else {
			c.Matrix.AccessToken = "rotated"
		}
		after, _ := Identity(c)
		if before != after {
			t.Fatal("credential rotation changed destination")
		}
		if kind == HomeAssistant {
			c.HomeAssistant.EventType = "other"
		} else {
			c.Matrix.RoomID = "!other:example.test"
		}
		after, _ = Identity(c)
		if before == after {
			t.Fatal("different route inherited destination receipts")
		}
		data, _ := json.Marshal(c)
		var decoded Config
		if json.Unmarshal(data, &decoded) != nil {
			t.Fatal("valid config failed round trip")
		}
		for _, bad := range []string{
			strings.Replace(string(data), `"accessToken":"rotated"`, `"accessToken":"rotated","accessToken":"duplicate"`, 1),
			strings.Replace(string(data), `"accessToken"`, `"AccessToken"`, 1),
			strings.Replace(string(data), `"accessToken":"rotated"`, `"accessToken":null`, 1),
		} {
			if json.Unmarshal([]byte(bad), &decoded) == nil {
				t.Fatal("accepted invalid config", bad)
			}
		}
		if kind == HomeAssistant {
			c.HomeAssistant.EventType = "../services/light/turn_on"
		} else {
			c.Matrix.RoomID = "!room:example.test/other"
		}
		if Validate(c) == nil {
			t.Fatal("accepted path injection")
		}
	}
}

// Matrix v1.16+ room versions can use a domainless create-event hash.
func TestMatrixDomainlessRoomID(t *testing.T) {
	const room = "!Abcdef0123456789_hash"
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" || r.URL.Path != "/_matrix/client/v3/rooms/"+room+"/send/m.room.message/stable" {
			t.Error("wrong room route", r.URL.Path)
		}
		io.WriteString(w, `{"event_id":"$fixture"}`)
	}))
	defer sink.Close()
	c := automationConfig(Matrix, sink.URL)
	c.Matrix.RoomID = room
	data, _ := json.Marshal(c)
	var decoded Config
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if result := Send(context.Background(), decoded, Message{Text: "fixture", IdempotencyKey: "stable"}); !result.Accepted {
		t.Fatal(result)
	}
	c.Matrix.RoomID = "!"
	if Validate(c) == nil {
		t.Fatal("empty opaque room ID accepted")
	}
}
