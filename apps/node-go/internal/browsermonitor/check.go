package browsermonitor

import (
	"bytes"
	"context"
	"encoding/base64"
	"image/png"
	"time"
)

var slots = make(chan struct{}, 2)

func unavailable() Result { return Result{State: "pending", Message: Unavailable} }

// Check uses its caller's total deadline, one fresh disposable browser context
// and a private page. It never invokes an operating-system browser or attaches
// to an existing page. The endpoint must be a dedicated, trusted Chromium CDP
// service; its own sandbox and target-network limits remain deployment duties.
func Check(ctx context.Context, c Config) (result Result) {
	if Validate(c) != nil {
		return Result{State: "down", Message: "invalid browser configuration"}
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
	p, err := connect(ctx, c)
	if err != nil {
		return unavailable()
	}
	defer p.close()
	var contextReply struct {
		ID string `json:"browserContextId"`
	}
	if p.call("Target.createBrowserContext", "", map[string]any{"disposeOnDetach": true}, &contextReply) != nil || contextReply.ID == "" {
		return unavailable()
	}
	defer func() {
		if p.dispose(contextReply.ID) != nil {
			result = unavailable()
		}
	}()
	if p.call("Browser.setDownloadBehavior", "", map[string]any{"behavior": "deny", "browserContextId": contextReply.ID}, nil) != nil {
		return unavailable()
	}
	var page struct {
		ID string `json:"targetId"`
	}
	if p.call("Target.createTarget", "", map[string]any{"url": "about:blank", "browserContextId": contextReply.ID, "background": true}, &page) != nil || page.ID == "" {
		return unavailable()
	}
	var attached struct {
		ID string `json:"sessionId"`
	}
	if p.call("Target.attachToTarget", "", map[string]any{"targetId": page.ID, "flatten": true}, &attached) != nil || attached.ID == "" {
		return unavailable()
	}
	p.session = attached.ID
	for _, command := range []struct {
		method string
		params any
	}{
		{"Page.enable", map[string]any{}},
		{"Page.setLifecycleEventsEnabled", map[string]any{"enabled": true}},
		{"Network.enable", map[string]any{"maxTotalBufferSize": 65536, "maxResourceBufferSize": 16384, "maxPostDataSize": 0}},
		{"Network.setBlockedURLs", map[string]any{"urls": []string{"file:*", "ftp:*", "chrome:*", "devtools:*"}}},
		{"Emulation.setDeviceMetricsOverride", map[string]any{"width": 1024, "height": 576, "deviceScaleFactor": 1, "mobile": false}},
		{"Fetch.enable", map[string]any{"patterns": []map[string]any{{"urlPattern": "*", "requestStage": "Request"}}}},
	} {
		if p.call(command.method, p.session, command.params, nil) != nil {
			return unavailable()
		}
	}
	var navigation struct {
		Frame    string `json:"frameId"`
		Loader   string `json:"loaderId"`
		Error    string `json:"errorText"`
		Download bool   `json:"isDownload"`
	}
	start := time.Now()
	if p.call("Page.navigate", p.session, map[string]any{"url": c.Target}, &navigation) != nil {
		return unavailable()
	}
	if navigation.Error != "" || navigation.Download {
		return Result{State: "down", Message: Failed}
	}
	if navigation.Frame == "" || navigation.Loader == "" {
		return unavailable()
	}
	key := ""
	var readyAt time.Time
	for {
		if p.failed {
			return unavailable()
		}
		current := navigation.Frame + "/" + p.loaders[navigation.Frame]
		state := p.pages[current]
		if p.loaders[navigation.Frame] != "" && state.idle && state.status != 0 {
			if key != current {
				key = current
				readyAt = time.Now().Add(time.Duration(c.DelayMs) * time.Millisecond)
			}
			if !time.Now().Before(readyAt) {
				break
			}
			wait := time.NewTimer(time.Until(readyAt))
			_, err := p.receiveUntil(wait.C)
			if !wait.Stop() {
				select {
				case <-wait.C:
				default:
				}
			}
			if err != nil && err != errWaitEnd {
				return unavailable()
			}
		} else {
			key = ""
			if _, err := p.receive(); err != nil {
				return unavailable()
			}
		}
	}
	status := p.pages[key].status
	r := Result{State: "up", Message: Loaded, StatusCode: status, LatencyMs: float64(time.Since(start)) / float64(time.Millisecond)}
	if status < 200 || status >= 400 {
		r.State, r.Message = "down", Failed
	}
	if c.Screenshot {
		var image struct {
			Data string `json:"data"`
		}
		if p.call("Page.captureScreenshot", p.session, map[string]any{"format": "png", "captureBeyondViewport": false, "clip": map[string]any{"x": 0, "y": 0, "width": 1024, "height": 576, "scale": 1}}, &image) != nil || len(image.Data) > base64.StdEncoding.EncodedLen(MaxScreenshot) {
			return unavailable()
		}
		raw, err := base64.StdEncoding.Strict().DecodeString(image.Data)
		if err != nil || len(raw) > MaxScreenshot {
			return unavailable()
		}
		config, err := png.DecodeConfig(bytes.NewReader(raw))
		if err != nil || config.Width != 1024 || config.Height != 576 {
			return unavailable()
		}
		if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
			return unavailable()
		}
		if navigation.Frame+"/"+p.loaders[navigation.Frame] != key || !p.pages[key].idle || p.failed {
			return unavailable()
		}
		r.Screenshot = raw
	}
	return r
}
