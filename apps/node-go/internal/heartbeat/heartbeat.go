// Package heartbeat is the pure causal register for passive push monitors.
// Callers serialize acceptance, reserve durable origin sequences, and atomically
// persist the returned register before making it visible to readers.
package heartbeat

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"math"
	"regexp"
	"slices"
	"unicode"
	"unicode/utf8"
)

const (
	MaxActors             = 32
	MaxMessageBytes       = 256
	MaxSequence     int64 = 9007199254740991
	MaxFutureMs     int64 = 30000
	MaxIntervalMs   int64 = 86400000
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type Config struct {
	MonitorID  string
	Generation string
	StartedAt  int64
	IntervalMs int64
	Known      map[string]bool
}

type Signal struct {
	State     string
	Message   string
	LatencyMs *float64
}

// Context is the accepted event's causal past; its own dot is not included.
// Every event is scoped to exactly one monitor generation.
type Event struct {
	MonitorID  string           `json:"monitorId"`
	Generation string           `json:"generation"`
	OriginID   string           `json:"originId"`
	Sequence   int64            `json:"sequence"`
	ReceivedAt int64            `json:"receivedAt"`
	State      string           `json:"state"`
	Message    string           `json:"message"`
	LatencyMs  *float64         `json:"latencyMs,omitempty"`
	Context    map[string]int64 `json:"context"`
}

type Evaluation struct {
	State     string
	Message   string
	LatencyMs *float64
	// Deadline is the next time a heartbeat can change this evaluation.
	// Zero means all maximal events have expired.
	Deadline int64
	Witness  string
}

func validID(v string) bool {
	return idPattern.MatchString(v) && v != "constructor" && v != "prototype" && v != "__proto__"
}

func validKnown(known map[string]bool) bool {
	if len(known) == 0 || len(known) > MaxActors {
		return false
	}
	for id, yes := range known {
		if !yes || !validID(id) {
			return false
		}
	}
	return true
}

func validClock(now int64) bool { return now > 0 && now <= MaxSequence-MaxIntervalMs-MaxFutureMs }

func ValidateConfig(c Config) error {
	if !validID(c.MonitorID) || !validID(c.Generation) || !validClock(c.StartedAt) || c.IntervalMs < 100 || c.IntervalMs > MaxIntervalMs || !validKnown(c.Known) {
		return errors.New("Invalid heartbeat configuration")
	}
	return nil
}

func ValidateSignal(s Signal) error {
	if s.State != "up" && s.State != "down" {
		return errors.New("Heartbeat status must be up or down")
	}
	if len(s.Message) > MaxMessageBytes || !utf8.ValidString(s.Message) {
		return errors.New("Invalid heartbeat message")
	}
	for _, r := range s.Message {
		if unicode.IsControl(r) {
			return errors.New("Invalid heartbeat message")
		}
	}
	if s.LatencyMs != nil && (math.IsNaN(*s.LatencyMs) || math.IsInf(*s.LatencyMs, 0) || *s.LatencyMs < 0 || *s.LatencyMs > 3600000) {
		return errors.New("Invalid heartbeat latency")
	}
	return nil
}

func ValidateEvent(e Event, known map[string]bool, now int64) error {
	if !validKnown(known) || !validClock(now) || !validID(e.MonitorID) || !validID(e.Generation) || !known[e.OriginID] || e.Sequence < 1 || e.Sequence > MaxSequence || !validClock(e.ReceivedAt) || e.ReceivedAt > now+MaxFutureMs || e.Context == nil || len(e.Context) > MaxActors {
		return errors.New("Invalid heartbeat event")
	}
	if err := ValidateSignal(Signal{e.State, e.Message, e.LatencyMs}); err != nil {
		return err
	}
	for actor, seq := range e.Context {
		if !known[actor] || seq < 1 || seq > MaxSequence || actor == e.OriginID && seq >= e.Sequence {
			return errors.New("Invalid heartbeat causality")
		}
	}
	return nil
}

func Key(e Event) string { return e.MonitorID + "/" + e.Generation + "/" + e.OriginID }

func clone(e Event) Event {
	e.Context = maps.Clone(e.Context)
	if e.LatencyMs != nil {
		v := *e.LatencyMs
		e.LatencyMs = &v
	}
	return e
}

func sameScope(a, b Event) bool { return a.MonitorID == b.MonitorID && a.Generation == b.Generation }
func vector(e Event) map[string]int64 {
	v := maps.Clone(e.Context)
	v[e.OriginID] = e.Sequence
	return v
}
func dominates(a, b map[string]int64) bool {
	for actor, seq := range b {
		if a[actor] < seq {
			return false
		}
	}
	return true
}

// compatible validates causal closure even when an observed event arrived later
// than its observer. Missing actors are allowed: peer pages can arrive in any order.
func compatible(a, b Event) error {
	if !sameScope(a, b) {
		return nil
	}
	if a.OriginID == b.OriginID {
		if a.Sequence == b.Sequence {
			x, _ := json.Marshal(a)
			y, _ := json.Marshal(b)
			if !bytes.Equal(x, y) {
				return errors.New("Immutable heartbeat event collision")
			}
			return nil
		}
		if a.Sequence < b.Sequence {
			a, b = b, a
		}
		if !dominates(a.Context, vector(b)) {
			return errors.New("Heartbeat origin causality regressed")
		}
		return nil
	}
	ab, ba := a.Context[b.OriginID] >= b.Sequence, b.Context[a.OriginID] >= a.Sequence
	if ab && ba || ab && !dominates(a.Context, vector(b)) || ba && !dominates(b.Context, vector(a)) {
		return errors.New("Invalid heartbeat causal context")
	}
	return nil
}

// Merge is atomic and leaves all caller-owned maps and events unchanged. There
// is one latest slot per monitor/generation/origin, independent of heartbeat count.
// Superseded historical dots are not an immutable event archive; replay cannot
// change the latest slot, and known older events still undergo causality checks.
func Merge(current map[string]Event, incoming []Event, known map[string]bool, now int64) (map[string]Event, error) {
	if !validKnown(known) || !validClock(now) {
		return nil, errors.New("Invalid heartbeat membership or clock")
	}
	next := make(map[string]Event, len(current)+len(incoming))
	for key, e := range current {
		next[key] = clone(e)
	}
	// Check incoming pairs before compaction so an invalid equal dot cannot hide
	// behind a later event in the same page.
	for i, e := range incoming {
		if err := ValidateEvent(e, known, now); err != nil {
			return nil, err
		}
		for j := 0; j < i; j++ {
			if err := compatible(e, incoming[j]); err != nil {
				return nil, err
			}
		}
		for _, old := range current {
			if err := compatible(e, old); err != nil {
				return nil, err
			}
		}
		key := Key(e)
		if old, ok := next[key]; !ok || e.Sequence > old.Sequence {
			next[key] = clone(e)
		}
	}
	return next, nil
}

func scoped(c Config, events map[string]Event) []Event {
	out := []Event{}
	for _, e := range events {
		if e.MonitorID == c.MonitorID && e.Generation == c.Generation {
			out = append(out, clone(e))
		}
	}
	slices.SortFunc(out, func(a, b Event) int {
		if a.OriginID < b.OriginID {
			return -1
		}
		if a.OriginID > b.OriginID {
			return 1
		}
		return 0
	})
	return out
}

// NewEvent observes all known register contexts, including expired signals.
// Sequence is reserved by the caller's retained external origin allocator.
func NewEvent(c Config, events map[string]Event, origin string, sequence, now int64, s Signal) (Event, error) {
	if err := ValidateConfig(c); err != nil {
		return Event{}, err
	}
	if !validClock(now) || now < c.StartedAt-MaxFutureMs {
		return Event{}, errors.New("Invalid heartbeat acceptance clock")
	}
	context := map[string]int64{}
	for _, e := range scoped(c, events) {
		for actor, seq := range vector(e) {
			context[actor] = max(context[actor], seq)
		}
	}
	e := Event{c.MonitorID, c.Generation, origin, sequence, now, s.State, s.Message, s.LatencyMs, context}
	if err := ValidateEvent(e, c.Known, now); err != nil {
		return Event{}, err
	}
	if _, err := Merge(events, []Event{e}, c.Known, now); err != nil {
		return Event{}, err
	}
	return clone(e), nil
}

// Witness identifies the complete exact-generation register and persisted
// deadline anchor. Callers recheck it under their commit lock before publishing
// a precomputed result. Wall time is deliberately excluded.
func Witness(c Config, events map[string]Event) string {
	b, _ := json.Marshal(struct {
		MonitorID, Generation string
		StartedAt, IntervalMs int64
		Events                []Event
	}{c.MonitorID, c.Generation, c.StartedAt, c.IntervalMs, scoped(c, events)})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func Evaluate(c Config, events map[string]Event, now int64) (Evaluation, error) {
	if err := ValidateConfig(c); err != nil {
		return Evaluation{}, err
	}
	if !validClock(now) || c.StartedAt > now+MaxFutureMs {
		return Evaluation{}, errors.New("Invalid heartbeat evaluation clock")
	}
	all := scoped(c, events)
	for i, e := range all {
		if err := ValidateEvent(e, c.Known, now); err != nil {
			return Evaluation{}, err
		}
		for j := 0; j < i; j++ {
			if err := compatible(e, all[j]); err != nil {
				return Evaluation{}, err
			}
		}
	}
	r := Evaluation{State: "pending", Message: "Waiting for first heartbeat", Deadline: c.StartedAt + c.IntervalMs, Witness: Witness(c, events)}
	if len(all) == 0 {
		if now >= r.Deadline {
			r.State, r.Message, r.Deadline = "down", "Heartbeat missed", 0
		}
		return r, nil
	}
	var selected *Event
	r.Deadline = 0
	for _, e := range all {
		dominated := false
		for _, other := range all {
			if other.OriginID != e.OriginID && other.Context[e.OriginID] >= e.Sequence {
				dominated = true
				break
			}
		}
		if dominated || now >= e.ReceivedAt+c.IntervalMs {
			continue
		}
		deadline := e.ReceivedAt + c.IntervalMs
		if r.Deadline == 0 || deadline < r.Deadline {
			r.Deadline = deadline
		}
		// DOWN wins concurrent fresh signals; origin order only selects a
		// deterministic diagnostic, never health or causal authority.
		if selected == nil || e.State == "down" && selected.State != "down" {
			v := clone(e)
			selected = &v
		}
	}
	if selected == nil {
		r.State, r.Message = "down", "Heartbeat missed"
		return r, nil
	}
	r.State, r.Message, r.LatencyMs = selected.State, selected.Message, selected.LatencyMs
	if r.Message == "" {
		r.Message = "Heartbeat: " + r.State
	}
	return r, nil
}

// DecodeObject rejects duplicate, unknown, null, case-folded, and malformed
// fields. It is shared by the wire event and the public payload parser.
func DecodeObject(b []byte, allowed map[string]bool) (map[string]json.RawMessage, error) {
	bad := func() (map[string]json.RawMessage, error) { return nil, errors.New("Invalid heartbeat object") }
	d := json.NewDecoder(bytes.NewReader(b))
	if t, err := d.Token(); err != nil || t != json.Delim('{') {
		return bad()
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		t, err := d.Token()
		if err != nil {
			return bad()
		}
		key, ok := t.(string)
		if !ok || allowed != nil && !allowed[key] {
			return bad()
		}
		if _, ok := fields[key]; ok {
			return bad()
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return bad()
		}
		fields[key] = raw
	}
	if t, err := d.Token(); err != nil || t != json.Delim('}') {
		return bad()
	}
	if _, err := d.Token(); err != io.EOF {
		return bad()
	}
	return fields, nil
}

func (e *Event) UnmarshalJSON(b []byte) error {
	fields, err := DecodeObject(b, map[string]bool{"monitorId": true, "generation": true, "originId": true, "sequence": true, "receivedAt": true, "state": true, "message": true, "latencyMs": true, "context": true})
	if err != nil {
		return err
	}
	var v Event
	for key, target := range map[string]any{"monitorId": &v.MonitorID, "generation": &v.Generation, "originId": &v.OriginID, "sequence": &v.Sequence, "receivedAt": &v.ReceivedAt, "state": &v.State, "message": &v.Message} {
		raw, ok := fields[key]
		if !ok || json.Unmarshal(raw, target) != nil {
			return errors.New("Invalid heartbeat event fields")
		}
	}
	if raw, ok := fields["latencyMs"]; ok {
		var latency float64
		if json.Unmarshal(raw, &latency) != nil {
			return errors.New("Invalid heartbeat latency")
		}
		v.LatencyMs = &latency
	}
	context, err := DecodeObject(fields["context"], nil)
	if err != nil {
		return err
	}
	v.Context = map[string]int64{}
	for actor, raw := range context {
		var seq int64
		if json.Unmarshal(raw, &seq) != nil {
			return errors.New("Invalid heartbeat context")
		}
		v.Context[actor] = seq
	}
	*e = v
	return nil
}
