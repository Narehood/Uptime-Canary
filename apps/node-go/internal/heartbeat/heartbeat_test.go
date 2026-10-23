package heartbeat

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func config() Config {
	return Config{"monitor", "generation", 100000, 1000, map[string]bool{"a": true, "b": true, "c": true}}
}
func event(t *testing.T, c Config, events map[string]Event, actor string, seq, now int64, state string) Event {
	t.Helper()
	e, err := NewEvent(c, events, actor, seq, now, Signal{State: state, Message: state})
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func merge(t *testing.T, events map[string]Event, incoming ...Event) map[string]Event {
	t.Helper()
	next, err := Merge(events, incoming, config().Known, 200000)
	if err != nil {
		t.Fatal(err)
	}
	return next
}
func state(t *testing.T, c Config, events map[string]Event, now int64) Evaluation {
	t.Helper()
	r, err := Evaluate(c, events, now)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestConcurrentFailureAndCausalRecovery(t *testing.T) {
	c := config()
	a := event(t, c, nil, "a", 10, 100001, "down")
	b := event(t, c, nil, "b", 50, 100010, "up")
	ab := merge(t, nil, a, b)
	ba := merge(t, nil, b, a)
	if !reflect.DeepEqual(ab, ba) || state(t, c, ab, 100020).State != "down" {
		t.Fatal("concurrent up erased failure")
	}
	// A later wall-clock UP still cannot erase a concurrent fresh DOWN.
	newB := event(t, c, merge(t, nil, b), "b", 51, 100500, "up")
	ab = merge(t, ab, newB)
	if state(t, c, ab, 100501).State != "down" {
		t.Fatal("wall clock became authority")
	}
	recovery := event(t, c, ab, "c", 100, 100600, "up")
	ab = merge(t, ab, recovery)
	if r := state(t, c, ab, 100601); r.State != "up" || r.Message != "up" {
		t.Fatal(r)
	}
	// Replayed/expired causally dominated DOWN cannot become effective again.
	ab = merge(t, ab, a, b)
	if state(t, c, ab, 101601).State != "down" {
		t.Fatal("expired recovery stayed healthy")
	}
}

func TestPermutationReplayAndPartitionJoin(t *testing.T) {
	c := config()
	a := event(t, c, nil, "a", 1, 100001, "down")
	b := event(t, c, nil, "b", 2, 100002, "up")
	joined := merge(t, nil, a, b)
	cEvent := event(t, c, joined, "c", 9, 100010, "up")
	after := event(t, c, merge(t, joined, cEvent), "a", 20, 100020, "up")
	input := []Event{a, b, cEvent, after}
	var expected map[string]Event
	var visit func(int)
	visit = func(i int) {
		if i == len(input) {
			var got map[string]Event
			for _, e := range input {
				got = merge(t, got, e, e)
			}
			if expected == nil {
				expected = got
			} else if !reflect.DeepEqual(expected, got) {
				t.Fatal("noncommutative merge")
			}
			return
		}
		for j := i; j < len(input); j++ {
			input[i], input[j] = input[j], input[i]
			visit(i + 1)
			input[i], input[j] = input[j], input[i]
		}
	}
	visit(0)
	if state(t, c, expected, 100021).State != "up" {
		t.Fatal("partition recovery did not dominate")
	}
}

func TestGenerationGraceExpiryAndWitness(t *testing.T) {
	c := config()
	empty := state(t, c, nil, 100999)
	if empty.State != "pending" || empty.Deadline != 101000 {
		t.Fatal(empty)
	}
	if state(t, c, nil, 101000).State != "down" {
		t.Fatal("deadline not exact")
	}
	e := event(t, c, nil, "a", 1, 100050, "up")
	events := merge(t, nil, e)
	if empty.Witness == Witness(c, events) {
		t.Fatal("arrival did not invalidate witness")
	}
	if state(t, c, events, 101049).State != "up" || state(t, c, events, 101050).State != "down" {
		t.Fatal("signal expiry wrong")
	}
	rotated := c
	rotated.Generation = "rotated"
	rotated.StartedAt = 101100
	if state(t, rotated, events, 101101).State != "pending" {
		t.Fatal("old generation contaminated new")
	}
	newEvent := event(t, rotated, events, "a", 5, 101101, "up")
	events = merge(t, events, newEvent, e)
	if len(events) != 2 || state(t, rotated, events, 101102).State != "up" {
		t.Fatal("out of order generations displaced state")
	}
	encoded, _ := json.Marshal(events)
	var restarted map[string]Event
	if json.Unmarshal(encoded, &restarted) != nil {
		t.Fatal("restart decode")
	}
	if state(t, rotated, restarted, 102101).State != "down" {
		t.Fatal("restart reset expiry")
	}
}

func TestMergeCollisionAndCausalClosureAtomic(t *testing.T) {
	c := config()
	a := event(t, c, nil, "a", 1, 100001, "down")
	events := merge(t, nil, a)
	before := Witness(c, events)
	collision := clone(a)
	collision.State = "up"
	if _, err := Merge(events, []Event{collision}, c.Known, 100100); err == nil {
		t.Fatal("equal dot collision accepted")
	}
	if Witness(c, events) != before {
		t.Fatal("failed merge mutated input")
	}
	b := event(t, c, events, "b", 2, 100002, "up")
	bad := event(t, c, nil, "c", 3, 100003, "up")
	bad.Context["b"] = 2
	if _, err := Merge(merge(t, events, b), []Event{bad}, c.Known, 100100); err == nil {
		t.Fatal("missing transitive context accepted")
	}
	regressed := clone(a)
	regressed.Sequence = 4
	regressed.Context = map[string]int64{}
	if _, err := Merge(events, []Event{regressed}, c.Known, 100100); err == nil {
		t.Fatal("origin causal regression accepted")
	}
	cycleA := clone(a)
	cycleA.Context["b"] = 2
	if _, err := Merge(nil, []Event{cycleA, b}, c.Known, 100100); err == nil {
		t.Fatal("causal cycle accepted")
	}
	// A collision cannot disappear through same-page latest compaction.
	later := event(t, c, events, "a", 8, 100010, "up")
	if _, err := Merge(nil, []Event{a, later, collision}, c.Known, 100100); err == nil {
		t.Fatal("page hid collision")
	}
}

func TestImmutableCopiesAndSkewBounds(t *testing.T) {
	c := config()
	latency := 0.0
	e, err := NewEvent(c, nil, "a", 1, 100001, Signal{"up", "okay", &latency})
	if err != nil {
		t.Fatal(err)
	}
	latency = 99
	events := merge(t, nil, e)
	e.Context["b"] = 7
	*e.LatencyMs = 55
	r := state(t, c, events, 100002)
	if r.LatencyMs == nil || *r.LatencyMs != 0 {
		t.Fatal("caller mutated stored latency")
	}
	*r.LatencyMs = 88
	if *state(t, c, events, 100002).LatencyMs != 0 {
		t.Fatal("evaluation aliased stored event")
	}
	skew := clone(events[Key(e)])
	skew.ReceivedAt = 130000
	if ValidateEvent(skew, c.Known, 100000) != nil {
		t.Fatal("exact skew boundary rejected")
	}
	skew.ReceivedAt++
	if ValidateEvent(skew, c.Known, 100000) == nil {
		t.Fatal("future clock bound bypassed")
	}
}

func TestRejectInvalidEventsAndWire(t *testing.T) {
	c := config()
	base := event(t, c, nil, "a", 1, 100001, "up")
	for _, mutate := range []func(*Event){func(e *Event) { e.OriginID = "unknown" }, func(e *Event) { e.Sequence = 0 }, func(e *Event) { e.Sequence = MaxSequence + 1 }, func(e *Event) { e.Context = nil }, func(e *Event) { e.Context["a"] = 1 }, func(e *Event) { e.Context["z"] = 1 }, func(e *Event) { e.Context["b"] = 0 }, func(e *Event) { e.Generation = "../bad" }, func(e *Event) { e.Message = strings.Repeat("x", 257) }, func(e *Event) { e.Message = "bad\nmessage" }, func(e *Event) { e.Message = string([]byte{0xff}) }, func(e *Event) { v := math.NaN(); e.LatencyMs = &v }, func(e *Event) { v := -1.0; e.LatencyMs = &v }, func(e *Event) { e.State = "pending" }} {
		e := clone(base)
		mutate(&e)
		if ValidateEvent(e, c.Known, 100100) == nil {
			t.Fatal("invalid event accepted", e)
		}
	}
	valid, _ := json.Marshal(base)
	prefix := strings.TrimSuffix(string(valid), "}")
	for _, raw := range []string{`null`, `[]`, prefix + `,"state":"down"}`, prefix + `,"State":"up"}`, prefix + `,"extra":1}`, strings.Replace(string(valid), `"sequence":1`, `"sequence":1.0`, 1), strings.Replace(string(valid), `"context":{}`, `"context":null`, 1), strings.Replace(string(valid), `"context":{}`, `"context":{"b":1,"b":2}`, 1), strings.Replace(string(valid), `"message":"up"`, `"message":null`, 1), prefix + `,"latencyMs":null}`, string(valid) + ` {}`} {
		var decoded Event
		if json.Unmarshal([]byte(raw), &decoded) == nil {
			t.Fatal("invalid wire accepted", raw)
		}
	}
}

func TestExistingColonIdentifiersAndMembershipLimit(t *testing.T) {
	c := Config{"monitor:batch", "generation:one", 100000, 1000, map[string]bool{"node:home": true}}
	e := event(t, c, nil, "node:home", 1, 100001, "up")
	events, err := Merge(nil, []Event{e}, c.Known, 100002)
	if err != nil {
		t.Fatal(err)
	}
	if state(t, c, events, 100002).State != "up" || Key(e) != "monitor:batch/generation:one/node:home" {
		t.Fatal("existing identifier contract changed")
	}
	for i := 0; i < 33; i++ {
		c.Known[string(rune('A'+i))] = true
	}
	if ValidateConfig(c) == nil {
		t.Fatal("membership limit ignored")
	}
}
