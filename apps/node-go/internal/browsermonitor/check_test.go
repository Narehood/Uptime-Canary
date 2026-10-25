package browsermonitor

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

type browserFixture struct {
	server         *httptest.Server
	opened, closed atomic.Int32
	ready          chan struct{}
	violations     atomic.Int32
}

func fixture(t *testing.T, mode string) *browserFixture {
	t.Helper()
	f := &browserFixture{ready: make(chan struct{}, 2)}
	f.server = httptest.NewServer(websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(ws *websocket.Conn) {
		f.opened.Add(1)
		defer f.closed.Add(1)
		defer ws.Close()
		if ws.Request().Header.Get("Authorization") != "Bearer private-browser-credential" {
			f.violations.Add(1)
			return
		}
		for {
			var request struct {
				ID      int                        `json:"id"`
				Method  string                     `json:"method"`
				Session string                     `json:"sessionId"`
				Params  map[string]json.RawMessage `json:"params"`
			}
			if websocket.JSON.Receive(ws, &request) != nil {
				return
			}
			result := map[string]any{}
			event := func(method string, params any) {
				_ = websocket.JSON.Send(ws, map[string]any{"sessionId": "private-session", "method": method, "params": params})
			}
			switch request.Method {
			case "Target.createBrowserContext":
				if string(request.Params["disposeOnDetach"]) != "true" {
					f.violations.Add(1)
					return
				}
				result["browserContextId"] = "private-context"
			case "Browser.setDownloadBehavior":
				if string(request.Params["browserContextId"]) != `"private-context"` || string(request.Params["behavior"]) != `"deny"` {
					f.violations.Add(1)
					return
				}
			case "Target.createTarget":
				if string(request.Params["url"]) != `"about:blank"` || string(request.Params["browserContextId"]) != `"private-context"` {
					f.violations.Add(1)
					return
				}
				result["targetId"] = "private-page"
			case "Target.attachToTarget":
				result["sessionId"] = "private-session"
			case "Page.enable", "Page.setLifecycleEventsEnabled", "Network.enable", "Network.setBlockedURLs", "Emulation.setDeviceMetricsOverride", "Fetch.enable", "Fetch.continueRequest", "Fetch.failRequest":
			case "Target.disposeBrowserContext":
				if mode == "cleanup-stall" {
					continue
				}
			case "Page.navigate":
				result["frameId"], result["loaderId"] = "main", "fresh"
				if mode == "navigation-error" {
					result["errorText"] = "private-address-and-error"
					break
				}
				select {
				case f.ready <- struct{}{}:
				default:
				}
				if mode == "stall" {
					continue
				}
				loader := "fresh"
				if mode == "stale-loader" {
					loader = "old"
				}
				event("Page.frameNavigated", map[string]any{"frame": map[string]any{"id": "main", "loaderId": "fresh", "url": "https://private-target.example/"}})
				status := 200
				if mode == "http-error" {
					status = 503
				}
				event("Network.responseReceived", map[string]any{"frameId": "main", "loaderId": loader, "type": "Document", "response": map[string]any{"status": status, "url": "https://private-target.example/", "headers": map[string]any{"Set-Cookie": "private-target-cookie"}}})
				event("Page.lifecycleEvent", map[string]any{"frameId": "main", "loaderId": loader, "name": "networkIdle"})
				if mode == "new-navigation" {
					event("Page.frameNavigated", map[string]any{"frame": map[string]any{"id": "main", "loaderId": "newer", "url": "https://private-target.example/other"}})
				}
			case "Page.captureScreenshot":
				var output bytes.Buffer
				_ = png.Encode(&output, image.NewRGBA(image.Rect(0, 0, 1024, 576)))
				result["data"] = base64.StdEncoding.EncodeToString(output.Bytes())
				if mode == "bad-screenshot" {
					result["data"] = "not-image"
				}
			default:
				f.violations.Add(1)
				return
			}
			response := map[string]any{"id": request.ID, "result": result}
			if request.Session != "" {
				response["sessionId"] = request.Session
			}
			if websocket.JSON.Send(ws, response) != nil {
				return
			}
		}
	}})
	t.Cleanup(f.server.Close)
	return f
}

func (f *browserFixture) config() Config {
	return Config{Target: "https://private-target.example/", Timeout: time.Second, Options: Options{Endpoint: "ws" + strings.TrimPrefix(f.server.URL, "http"), BearerToken: "private-browser-credential"}}
}

func TestNavigationEvidenceAndPrivateResults(t *testing.T) {
	for _, item := range []struct {
		mode, state string
		screenshot  bool
	}{
		{"ok", "up", false}, {"ok", "up", true}, {"http-error", "down", false}, {"navigation-error", "down", false},
		{"stale-loader", "pending", false}, {"new-navigation", "pending", false}, {"bad-screenshot", "pending", true}, {"cleanup-stall", "pending", false},
	} {
		t.Run(item.mode+map[bool]string{false: "", true: "/screenshot"}[item.screenshot], func(t *testing.T) {
			f := fixture(t, item.mode)
			c := f.config()
			c.Screenshot = item.screenshot
			if item.state == "pending" {
				c.Timeout = 250 * time.Millisecond
			}
			r := Check(context.Background(), c)
			if r.State != item.state {
				t.Fatal("wrong browser outcome", r.State, r.Message)
			}
			if f.violations.Load() != 0 {
				t.Fatal("check used a forbidden command or context")
			}
			if item.state == "up" && (r.StatusCode != 200 || item.screenshot && len(r.Screenshot) == 0) {
				t.Fatal("missing page evidence")
			}
			if r.Message != Loaded && r.Message != Failed && r.Message != Unavailable {
				t.Fatal("raw browser output escaped result")
			}
			if item.state == "pending" && (r.LatencyMs != 0 || len(r.Screenshot) != 0) {
				t.Fatal("unavailable browser retained success evidence")
			}
		})
	}
}

func TestBrowserCancellationReleasesBothSlots(t *testing.T) {
	f := fixture(t, "stall")
	c := f.config()
	c.Timeout = 5 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Result, 2)
	for i := 0; i < 2; i++ {
		go func() { done <- Check(ctx, c) }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-f.ready:
		case <-time.After(time.Second):
			t.Fatal("private browser did not enter navigation")
		}
	}
	if r := Check(context.Background(), c); r.State != "pending" {
		t.Fatal("browser capacity not bounded")
	}
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case r := <-done:
			if r.State != "pending" {
				t.Fatal("canceled browser became outage")
			}
		case <-time.After(time.Second):
			t.Fatal("browser work not joined")
		}
	}
	good := fixture(t, "ok")
	if r := Check(context.Background(), good.config()); r.State != "up" {
		t.Fatal("canceled browser admission leaked")
	}
}

func TestBrowserConfigurationAndDiscoveryScope(t *testing.T) {
	c := Config{Target: "https://target.example", Timeout: time.Second, Options: Options{Endpoint: "http://127.0.0.1:9222"}}
	if Validate(c) != nil {
		t.Fatal("valid browser profile refused")
	}
	for _, target := range []string{"file:///etc/passwd", "data:text/html,test", "chrome://version", "https://user:pass@target/"} {
		bad := c
		bad.Target = target
		if Validate(bad) == nil {
			t.Fatal("unsupported navigation admitted")
		}
	}
	for _, address := range []string{"file:///tmp/socket", "http://user:pass@host/", "http://host/path", "https://host/?token=secret", "ws://host/#fragment"} {
		bad := c
		bad.Endpoint = address
		if Validate(bad) == nil {
			t.Fatal("unsupported browser endpoint admitted")
		}
	}
	var contacted atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contacted.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"webSocketDebuggerUrl":"ws://other-origin.example/private"}`))
	}))
	defer s.Close()
	c.Endpoint = s.URL
	if _, err := endpoint(context.Background(), c); err == nil || contacted.Load() != 1 {
		t.Fatal("discovery moved browser authority to another origin")
	}
}
