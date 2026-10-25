package nodeconnection

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const registration = "0123456789abcdef0123456789abcdef"

func frame(body string) Request {
	return Request{Method: "POST", Path: "/api/monitors", NodeID: "node-a", Authorization: "Bearer private-fixture-capability", ContentType: "application/json", Body: []byte(body)}
}

func reply(request *Request) Response {
	return Response{ID: request.ID, Status: 201, NodeID: request.NodeID, ContentType: "application/json", Body: []byte(`{"ok":true}`)}
}

func until(t *testing.T, f func() bool) {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	for time.Now().Before(end) {
		if f() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("Bounded transport condition did not complete")
}

func pendingCount(h *Hub) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.sessions[registration]; s != nil {
		return len(s.pending)
	}
	return 0
}

func TestHubPreservesAuthorizedRequestAndBoundedResponse(t *testing.T) {
	h := NewHub()
	defer h.Close()
	epoch, err := h.Open(registration)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	type answer struct {
		value Response
		err   error
	}
	done := make(chan answer, 1)
	input := frame(`{"name":"private fixture"}`)
	go func() { value, err := h.Request(ctx, registration, input); done <- answer{value, err} }()
	job, err := h.Poll(ctx, registration, epoch)
	if err != nil || job == nil {
		t.Fatal("Missing authorized job", err)
	}
	if job.Method != input.Method || job.Path != input.Path || job.Authorization != input.Authorization || job.NodeID != input.NodeID || !bytes.Equal(job.Body, input.Body) || job.TimeoutMs < 1 || job.TimeoutMs > 3000 {
		t.Fatal("Transport changed request authority or deadline")
	}
	if err = h.Complete(registration, epoch, reply(job)); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if result.err != nil || result.value.Status != 201 || !bytes.Equal(result.value.Body, reply(job).Body) {
		t.Fatal("Response did not complete exactly its request")
	}
	// Accepted distinction: a duplicate remains refused, now as a stale
	// request rather than incorrectly claiming that the epoch changed.
	if err = h.Complete(registration, epoch, reply(job)); !errors.Is(err, ErrStale) {
		t.Fatal("Duplicate result was admitted")
	}
	if pendingCount(h) != 0 {
		t.Fatal("Completed request retained admission")
	}
}

func TestCanceledQueuedWorkReleasesSlotWithOtherSlotOccupied(t *testing.T) {
	h := NewHub()
	defer h.Close()
	epoch, _ := h.Open(registration)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := h.Request(ctx, registration, frame("hold")); first <- err }()
	held, err := h.Poll(ctx, registration, epoch)
	if err != nil || held == nil {
		t.Fatal(err)
	}
	queuedCtx, cancelQueued := context.WithCancel(ctx)
	second := make(chan error, 1)
	go func() { _, err := h.Request(queuedCtx, registration, frame("cancel")); second <- err }()
	until(t, func() bool { return pendingCount(h) == 2 })
	if _, err = h.Request(ctx, registration, frame("over capacity")); !errors.Is(err, ErrBusy) {
		t.Fatal("Per-node capacity was not bounded")
	}
	cancelQueued()
	if err = <-second; !errors.Is(err, context.Canceled) {
		t.Fatal("Cancellation was not propagated")
	}
	third := make(chan error, 1)
	go func() { _, err := h.Request(ctx, registration, frame("replacement")); third <- err }()
	job, err := h.Poll(ctx, registration, epoch)
	if err != nil || job == nil || string(job.Body) != "replacement" {
		t.Fatal("Canceled work blocked replacement capacity", err)
	}
	if err = h.Complete(registration, epoch, reply(job)); err != nil {
		t.Fatal(err)
	}
	if err = <-third; err != nil {
		t.Fatal(err)
	}
	if err = h.Complete(registration, epoch, reply(held)); err != nil {
		t.Fatal(err)
	}
	if err = <-first; err != nil {
		t.Fatal(err)
	}
}

func TestReconnectRejectsOldResultAndDoesNotReplayMutation(t *testing.T) {
	h := NewHub()
	defer h.Close()
	old, _ := h.Open(registration)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := h.Request(ctx, registration, frame("mutation")); done <- err }()
	job, err := h.Poll(ctx, registration, old)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	current, err := h.Open(registration)
	if err != nil || current == old {
		t.Fatal("Session epoch was reused")
	}
	if err = <-done; !errors.Is(err, ErrDisconnected) {
		t.Fatal("A dispatched request was not reported as unknown after reconnect")
	}
	if err = h.Complete(registration, old, reply(job)); !errors.Is(err, ErrSession) {
		t.Fatal("Old result crossed into the new session")
	}
	probe, stop := context.WithCancel(ctx)
	stop()
	if job, err := h.Poll(probe, registration, current); job != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("Mutation was replayed after reconnect")
	}
}

func TestOnlyOnePollOwnsSessionAndCloseJoinsIt(t *testing.T) {
	h := NewHub()
	epoch, _ := h.Open(registration)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := h.Poll(ctx, registration, epoch); done <- err }()
	until(t, func() bool { h.mu.Lock(); defer h.mu.Unlock(); return h.sessions[registration].polling })
	if _, err := h.Poll(ctx, registration, epoch); !errors.Is(err, ErrBusy) {
		t.Fatal("Duplicate poll was admitted")
	}
	h.Close()
	if err := <-done; !errors.Is(err, ErrSession) {
		t.Fatal("Close retained a poll")
	}
	h.Close()
	if _, err := h.Open(registration); !errors.Is(err, ErrDisconnected) {
		t.Fatal("Closed transport reopened")
	}
}

func TestResultMustBelongToADispatchedCurrentJob(t *testing.T) {
	h := NewHub()
	defer h.Close()
	epoch, _ := h.Open(registration)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := h.Request(ctx, registration, frame("queued")); done <- err }()
	until(t, func() bool { return pendingCount(h) == 1 })
	h.mu.Lock()
	var queued Request
	for _, value := range h.sessions[registration].pending {
		queued = value.request
	}
	h.mu.Unlock()
	if err := h.Complete(registration, epoch, reply(&queued)); !errors.Is(err, ErrSession) {
		t.Fatal("Undispatched response was admitted")
	}
	job, err := h.Poll(ctx, registration, epoch)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	if err = h.Complete(registration, epoch, reply(job)); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestTransportMessageAndWriterCaps(t *testing.T) {
	id := strings.Repeat("a", 64)
	base := frame("{}")
	base.ID, base.TimeoutMs = id, 1000
	if !base.Valid() {
		t.Fatal("Valid control was rejected")
	}
	for _, change := range []func(*Request){
		func(r *Request) { r.Method = "CONNECT" },
		func(r *Request) { r.Path = "https://example.invalid/internal" },
		func(r *Request) { r.Path = "//example.invalid/internal" },
		func(r *Request) { r.Authorization += "\r\nCookie: refused" },
		func(r *Request) { r.Body = make([]byte, MaxRequestBody+1) },
		func(r *Request) { r.TimeoutMs = 12001 },
	} {
		value := base
		change(&value)
		if value.Valid() {
			t.Fatal("Unsafe or oversized request was admitted")
		}
	}
	w := NewWriter()
	w.Header().Set("X-Canary-Node-Id", "node-a")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	if _, err := w.Write(make([]byte, MaxResponseBody)); err != nil {
		t.Fatal("Boundary response failed")
	}
	if value, err := w.Response(id); err != nil || len(value.Body) != MaxResponseBody {
		t.Fatal("Boundary response was not retained")
	}
	if _, err := w.Write([]byte{1}); !errors.Is(err, ErrMessage) {
		t.Fatal("Overflow was not refused")
	}
	if _, err := w.Response(id); !errors.Is(err, ErrMessage) || len(w.body) != 0 {
		t.Fatal("Overflow published or retained a truncated response")
	}
}

func TestSessionCapacityRejectsGrowthAndReleasesOnlyOwnedEntries(t *testing.T) {
	h := NewHub()
	defer h.Close()
	ids := make([]string, 0, MaxSessions)
	for range MaxSessions {
		key, err := RandomKey()
		if err != nil {
			t.Fatal(err)
		}
		id := key[:32]
		if _, err := h.Open(id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	extra, _ := RandomKey()
	if _, err := h.Open(extra[:32]); !errors.Is(err, ErrBusy) {
		t.Fatal("session capacity was not bounded")
	}
	if _, err := h.Open(ids[0]); err != nil {
		t.Fatal("replacing an owned session consumed additional capacity")
	}
	h.Disconnect(ids[0])
	if _, err := h.Open(extra[:32]); err != nil {
		t.Fatal("disconnect did not release session capacity")
	}
	h.mu.Lock()
	count := len(h.sessions)
	h.mu.Unlock()
	if count != MaxSessions {
		t.Fatal("session map grew beyond its bound")
	}
	h.Close()
	if _, err := h.Open(ids[0]); !errors.Is(err, ErrDisconnected) {
		t.Fatal("closed hub admitted a new session")
	}
}
