package browsermonitor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"uptime-canary/apps/node-go/internal/jsonshape"
)

type envelope struct {
	ID      int             `json:"id,omitempty"`
	Session string          `json:"sessionId,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

var errWaitEnd = errors.New("browser wait complete")

type eventRead struct {
	message envelope
	err     error
}
type pageState struct {
	status int
	idle   bool
}
type client struct {
	ctx     context.Context
	ws      *socket
	cancel  context.CancelFunc
	stop    func() bool
	closed  chan struct{}
	read    chan eventRead
	done    chan struct{}
	next    int
	pending map[int]string
	session string
	pages   map[string]pageState
	loaders map[string]string
	failed  bool
}

func endpoint(ctx context.Context, c Config) (string, error) {
	u, _ := url.Parse(c.Endpoint)
	if u.Scheme == "ws" || u.Scheme == "wss" {
		return u.String(), nil
	}
	u.Path = "/json/version"
	r, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return "", errProtocol
	}
	if c.BearerToken != "" {
		r.Header.Set("Authorization", "Bearer "+c.BearerToken)
	}
	t := &http.Transport{MaxResponseHeaderBytes: 16 << 10, DisableKeepAlives: true}
	defer t.CloseIdleConnections()
	h := &http.Client{Transport: t, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := h.Do(r)
	if err != nil {
		return "", errProtocol
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	var version struct {
		WS string `json:"webSocketDebuggerUrl"`
	}
	if err != nil || response.StatusCode != 200 || len(raw) > 8192 || jsonshape.Validate(raw) != nil || json.Unmarshal(raw, &version) != nil {
		return "", errProtocol
	}
	w, err := url.Parse(version.WS)
	want := "ws"
	if u.Scheme == "https" {
		want = "wss"
	}
	// Discovery cannot move a browser credential to another origin.
	if err != nil || w.Scheme != want || !strings.EqualFold(w.Host, u.Host) || w.User != nil || w.Fragment != "" || len(version.WS) > 2048 {
		return "", errProtocol
	}
	return version.WS, nil
}

func connect(ctx context.Context, c Config) (*client, error) {
	address, err := endpoint(ctx, c)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	ws, err := dialSocket(ctx, address, c.BearerToken)
	if err != nil {
		cancel()
		return nil, errProtocol
	}
	p := &client{ctx: ctx, cancel: cancel, ws: ws, read: make(chan eventRead, 32), done: make(chan struct{}), closed: make(chan struct{}), pending: map[int]string{}, pages: map[string]pageState{}, loaders: map[string]string{}}
	p.stop = context.AfterFunc(ctx, func() { ws.close(); close(p.closed) })
	go func() {
		defer close(p.done)
		total := 0
		for count := 0; count < 8192; count++ {
			raw, err := ws.receive()
			total += len(raw)
			var message envelope
			if err == nil && (total > 8<<20 || jsonshape.Validate(raw) != nil || json.Unmarshal(raw, &message) != nil) {
				err = errProtocol
			}
			select {
			case p.read <- eventRead{message, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
		select {
		case p.read <- eventRead{err: errProtocol}:
		case <-ctx.Done():
		}
	}()
	return p, nil
}

func (p *client) send(method, session string, params any) (int, error) {
	if len(p.pending) >= 512 {
		return 0, errProtocol
	}
	p.next++
	request := struct {
		ID      int    `json:"id"`
		Method  string `json:"method"`
		Session string `json:"sessionId,omitempty"`
		Params  any    `json:"params"`
	}{p.next, method, session, params}
	if p.ws.sendJSON(request) != nil {
		return 0, errProtocol
	}
	p.pending[p.next] = session
	return p.next, nil
}

func (p *client) call(method, session string, params, out any) error {
	id, err := p.send(method, session, params)
	if err != nil {
		return err
	}
	for {
		message, err := p.receive()
		if err != nil {
			return err
		}
		if message.ID == id {
			if message.Session != session || len(message.Error) != 0 || len(message.Result) == 0 {
				return errProtocol
			}
			if out != nil && json.Unmarshal(message.Result, out) != nil {
				return errProtocol
			}
			return nil
		}
	}
}

func (p *client) receive() (envelope, error) { return p.receiveUntil(nil) }

func (p *client) receiveUntil(stop <-chan time.Time) (envelope, error) {
	select {
	case <-stop:
		return envelope{}, errWaitEnd
	case <-p.ctx.Done():
		return envelope{}, errProtocol
	case item := <-p.read:
		if item.err != nil {
			return envelope{}, errProtocol
		}
		m := item.message
		if m.ID != 0 {
			expected, exists := p.pending[m.ID]
			if !exists || expected != m.Session || len(m.Result) == 0 {
				return envelope{}, errProtocol
			}
			delete(p.pending, m.ID)
			if len(m.Error) != 0 {
				return envelope{}, errProtocol
			}
			return m, nil
		}
		if m.Session == p.session && p.session != "" {
			if p.event(m) != nil {
				return envelope{}, errProtocol
			}
		}
		return m, nil
	}
}

func (p *client) event(m envelope) error {
	switch m.Method {
	case "Fetch.requestPaused":
		var e struct {
			ID      string `json:"requestId"`
			Request struct {
				URL string `json:"url"`
			} `json:"request"`
		}
		if json.Unmarshal(m.Params, &e) != nil || e.ID == "" {
			return errProtocol
		}
		method := "Fetch.continueRequest"
		params := map[string]any{"requestId": e.ID}
		if _, ok := webURL(e.Request.URL); !ok {
			method = "Fetch.failRequest"
			params["errorReason"] = "BlockedByClient"
		}
		_, err := p.send(method, p.session, params)
		return err
	case "Page.frameNavigated":
		var e struct {
			Frame struct {
				ID     string `json:"id"`
				Parent string `json:"parentId"`
				Loader string `json:"loaderId"`
				URL    string `json:"url"`
			} `json:"frame"`
		}
		if json.Unmarshal(m.Params, &e) != nil {
			return errProtocol
		}
		if e.Frame.Parent == "" && e.Frame.Loader != "" && e.Frame.URL != "about:blank" {
			if _, ok := webURL(e.Frame.URL); !ok {
				p.failed = true
				return nil
			}
			p.loaders[e.Frame.ID] = e.Frame.Loader
		}
	case "Network.responseReceived":
		var e struct {
			Frame    string `json:"frameId"`
			Loader   string `json:"loaderId"`
			Type     string `json:"type"`
			Response struct {
				Status float64 `json:"status"`
				URL    string  `json:"url"`
			} `json:"response"`
		}
		if json.Unmarshal(m.Params, &e) != nil {
			return errProtocol
		}
		if e.Type == "Document" {
			if _, ok := webURL(e.Response.URL); !ok || e.Response.Status < 100 || e.Response.Status > 599 || float64(int(e.Response.Status)) != e.Response.Status {
				return errProtocol
			}
			key := e.Frame + "/" + e.Loader
			state := p.pages[key]
			state.status = int(e.Response.Status)
			p.pages[key] = state
		}
	case "Page.lifecycleEvent":
		var e struct {
			Frame  string `json:"frameId"`
			Loader string `json:"loaderId"`
			Name   string `json:"name"`
		}
		if json.Unmarshal(m.Params, &e) != nil {
			return errProtocol
		}
		if e.Name == "networkIdle" {
			key := e.Frame + "/" + e.Loader
			state := p.pages[key]
			state.idle = true
			p.pages[key] = state
		}
	case "Inspector.targetCrashed", "Inspector.detached":
		p.failed = true
	}
	if len(p.pages) > 64 || len(p.loaders) > 64 {
		return errProtocol
	}
	return nil
}

// Closing the debugging connection disposes the context even if the browser
// cannot acknowledge an explicit disposal before the total deadline.
func (p *client) close() {
	p.cancel()
	p.ws.close()
	if !p.stop() {
		<-p.closed
	}
	<-p.done
}

// Cleanup has its own short cap inside the original deadline. If it cannot be
// acknowledged, closing the socket invokes the required disposeOnDetach rule.
func (p *client) dispose(id string) error {
	ctx, cancel := context.WithTimeout(p.ctx, 250*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { p.ws.close(); close(done) })
	err := p.call("Target.disposeBrowserContext", "", map[string]any{"browserContextId": id}, nil)
	if !stop() {
		<-done
	}
	return err
}
