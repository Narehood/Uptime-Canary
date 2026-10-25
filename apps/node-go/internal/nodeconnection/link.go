// Package nodeconnection carries bounded management messages over a connection
// initiated by a node. Authentication and application authorization remain the
// caller's responsibility; a transport session is never an application role.
package nodeconnection

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	MaxRequestBody    = 256 * 1024
	MaxResponseBody   = 4 * 1024 * 1024
	MaxResultEnvelope = 6 * 1024 * 1024
	PollWait          = 15 * time.Second
	CallTimeout       = 12 * time.Second
	SessionFreshness  = 45 * time.Second
	MaxSessions       = 80 // 64 registered nodes plus 16 unconfirmed invitations.
	MaxPending        = 2  // The dashboard also retains its global eight-call limit.
)

var (
	ErrDisconnected = errors.New("Node connection is unavailable; a dispatched request may have completed")
	ErrSession      = errors.New("Node connection session changed")
	ErrStale        = errors.New("Node connection request is no longer pending")
	ErrBusy         = errors.New("Node connection is busy")
	ErrMessage      = errors.New("Invalid node connection message")
)

type Request struct {
	ID            string `json:"id"`
	Method        string `json:"method"`
	Path          string `json:"path"`
	Authorization string `json:"authorization,omitempty"`
	ContentType   string `json:"contentType,omitempty"`
	NodeID        string `json:"nodeId"`
	Body          []byte `json:"body,omitempty"`
	TimeoutMs     int64  `json:"timeoutMs"`
}

type Response struct {
	ID          string `json:"id"`
	Status      int    `json:"status"`
	ContentType string `json:"contentType,omitempty"`
	NodeID      string `json:"nodeId"`
	Body        []byte `json:"body,omitempty"`
}

func Identifier(value string, size int) bool {
	return len(value) == size && strings.Trim(value, "0123456789abcdef") == ""
}

func RandomKey() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", errors.New("Connection randomness unavailable")
	}
	return hex.EncodeToString(value[:]), nil
}

func cleanHeader(value string, limit int) bool {
	if len(value) > limit {
		return false
	}
	for _, c := range value {
		if c < 32 || c > 126 {
			return false
		}
	}
	return true
}

func (r Request) Valid() bool {
	if !Identifier(r.ID, 64) || len(r.Body) > MaxRequestBody || r.TimeoutMs < 1 || r.TimeoutMs > int64(CallTimeout/time.Millisecond) || !cleanHeader(r.Authorization, 8192) || !cleanHeader(r.ContentType, 256) || !cleanHeader(r.NodeID, 128) || r.NodeID == "" {
		return false
	}
	if r.Method != "GET" && r.Method != "HEAD" && r.Method != "POST" && r.Method != "PUT" && r.Method != "PATCH" && r.Method != "DELETE" {
		return false
	}
	// The application supplies a stricter path allowlist at both ends. Refuse
	// absolute/network-path targets and control bytes before any dispatch.
	if !strings.HasPrefix(r.Path, "/") || strings.HasPrefix(r.Path, "//") || len(r.Path) > 16384 || strings.ContainsAny(r.Path, "\\\r\n\x00#") {
		return false
	}
	for _, c := range r.Path {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}

func (r Response) Valid() bool {
	return Identifier(r.ID, 64) && r.Status >= 200 && r.Status <= 599 && len(r.Body) <= MaxResponseBody && cleanHeader(r.ContentType, 256) && cleanHeader(r.NodeID, 128) && r.NodeID != ""
}

type job struct {
	request    Request
	ctx        context.Context
	result     chan Response
	dispatched bool
}

type session struct {
	epoch   string
	seen    time.Time
	polling bool
	queue   chan *job
	pending map[string]*job
	done    chan struct{}
}

type Hub struct {
	mu       sync.Mutex
	sessions map[string]*session
	closed   bool
}

func NewHub() *Hub { return &Hub{sessions: make(map[string]*session)} }

func (h *Hub) Open(id string) (string, error) {
	if !Identifier(id, 32) {
		return "", ErrMessage
	}
	epoch, err := RandomKey()
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return "", ErrDisconnected
	}
	previous := h.sessions[id]
	if previous == nil && len(h.sessions) >= MaxSessions {
		return "", ErrBusy
	}
	if previous != nil {
		close(previous.done)
	}
	h.sessions[id] = &session{epoch: epoch, seen: time.Now(), queue: make(chan *job, MaxPending), pending: make(map[string]*job), done: make(chan struct{})}
	return epoch, nil
}

func (h *Hub) Disconnect(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.sessions[id]; s != nil {
		close(s.done)
		delete(h.sessions, id)
	}
}

func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	for id, s := range h.sessions {
		close(s.done)
		delete(h.sessions, id)
	}
}

func (h *Hub) LastSeen(id string) int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.sessions[id]; s != nil {
		return s.seen.UnixMilli()
	}
	return 0
}

// Request never replays work. Losing a session after dispatch returns an
// unknown-result error, which does not imply that a mutation did not commit.
func (h *Hub) Request(ctx context.Context, id string, request Request) (Response, error) {
	var zero Response
	key, err := RandomKey()
	if err != nil {
		return zero, err
	}
	request.ID = key
	ctx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()
	deadline, _ := ctx.Deadline()
	request.TimeoutMs = max(1, time.Until(deadline).Milliseconds())
	if !request.Valid() {
		return zero, ErrMessage
	}
	if ctx.Err() != nil {
		return zero, ctx.Err()
	}
	h.mu.Lock()
	s := h.sessions[id]
	if h.closed || s == nil || time.Since(s.seen) > SessionFreshness {
		h.mu.Unlock()
		return zero, ErrDisconnected
	}
	if len(s.pending) >= MaxPending {
		h.mu.Unlock()
		return zero, ErrBusy
	}
	// Canceled jobs may still occupy queue entries before the next poll. Prune
	// them without waiting for a receiver, so cancellation releases admission.
	var live []*job
	for range MaxPending {
		select {
		case queued := <-s.queue:
			if queued.ctx.Err() == nil && s.pending[queued.request.ID] == queued {
				live = append(live, queued)
			}
		default:
		}
	}
	for _, queued := range live {
		s.queue <- queued
	}
	pending := &job{request: request, ctx: ctx, result: make(chan Response, 1)}
	s.pending[key] = pending
	select {
	case s.queue <- pending:
	default:
		delete(s.pending, key)
		h.mu.Unlock()
		return zero, ErrBusy
	}
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(s.pending, key); h.mu.Unlock() }()
	select {
	case result := <-pending.result:
		return result, nil
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-s.done:
		return zero, ErrDisconnected
	}
}

func (h *Hub) Poll(ctx context.Context, id, epoch string) (*Request, error) {
	h.mu.Lock()
	s := h.sessions[id]
	if s == nil || s.epoch != epoch || h.closed {
		h.mu.Unlock()
		return nil, ErrSession
	}
	if s.polling {
		h.mu.Unlock()
		return nil, ErrBusy
	}
	s.polling, s.seen = true, time.Now()
	h.mu.Unlock()
	defer func() { h.mu.Lock(); s.polling = false; s.seen = time.Now(); h.mu.Unlock() }()
	timer := time.NewTimer(PollWait)
	defer timer.Stop()
	for {
		select {
		case pending := <-s.queue:
			h.mu.Lock()
			valid := h.sessions[id] == s && s.pending[pending.request.ID] == pending && pending.ctx.Err() == nil
			if valid {
				deadline, _ := pending.ctx.Deadline()
				pending.request.TimeoutMs = max(1, time.Until(deadline).Milliseconds())
				pending.dispatched = true
			}
			h.mu.Unlock()
			if valid {
				result := pending.request
				return &result, nil
			}
		case <-timer.C:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.done:
			return nil, ErrSession
		}
	}
}

func (h *Hub) Complete(id, epoch string, result Response) error {
	if !result.Valid() {
		return ErrMessage
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessions[id]
	if s == nil || s.epoch != epoch || h.closed {
		return ErrSession
	}
	pending := s.pending[result.ID]
	if pending == nil || pending.ctx.Err() != nil {
		return ErrStale
	}
	if !pending.dispatched {
		return ErrSession
	}
	delete(s.pending, result.ID)
	s.seen = time.Now()
	pending.result <- result
	return nil
}

// Writer captures at most MaxResponseBody bytes; exceeding it drops the whole
// result. It cannot flush, hijack or turn the connection into a general tunnel.
type Writer struct {
	header   http.Header
	status   int
	body     []byte
	overflow bool
}

func NewWriter() *Writer              { return &Writer{header: make(http.Header)} }
func (w *Writer) Header() http.Header { return w.header }
func (w *Writer) WriteHeader(status int) {
	if status >= 200 && w.status == 0 {
		w.status = status
	}
}
func (w *Writer) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	if w.overflow || len(value) > MaxResponseBody-len(w.body) {
		w.overflow = true
		w.body = nil
		return 0, ErrMessage
	}
	w.body = append(w.body, value...)
	return len(value), nil
}
func (w *Writer) Response(id string) (Response, error) {
	if w.status == 0 {
		w.status = 200
	}
	result := Response{ID: id, Status: w.status, Body: w.body, ContentType: w.header.Get("Content-Type"), NodeID: w.header.Get("X-Canary-Node-Id")}
	if w.overflow || !result.Valid() {
		return Response{}, ErrMessage
	}
	return result, nil
}
