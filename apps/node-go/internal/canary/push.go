package canary

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
	"uptime-canary/apps/node-go/internal/heartbeat"
)

const pushBodyBytes = 4096
const pushAdmissionMs int64 = 100

func (p *PushMonitorOptions) UnmarshalJSON(b []byte) error {
	fields, err := heartbeat.DecodeObject(b, map[string]bool{"token": true, "allowGet": true})
	if err != nil {
		return fail(400, "Invalid push options")
	}
	var v PushMonitorOptions
	if raw, ok := fields["token"]; !ok || json.Unmarshal(raw, &v.Token) != nil {
		return fail(400, "Invalid push token")
	}
	if raw, ok := fields["allowGet"]; ok && json.Unmarshal(raw, &v.AllowGET) != nil {
		return fail(400, "Invalid push options")
	}
	*p = v
	return nil
}

func (n *Node) loadPushEvents() error {
	loaded, err := loadAll[heartbeat.Event](n.store, "heartbeatEvents")
	if err != nil {
		return err
	}
	events := make([]heartbeat.Event, 0, len(loaded))
	for key, e := range loaded {
		if key != heartbeat.Key(e) {
			return errors.New("Invalid persisted heartbeat key")
		}
		events = append(events, e)
	}
	next, err := heartbeat.Merge(nil, events, n.known, n.now())
	if err != nil {
		return errors.New("Invalid persisted heartbeat state")
	}
	n.pushEvents = next
	n.pushAdmission = map[string]int64{}
	// Admission belongs to the monitor/origin, not a replaceable credential or
	// check generation or its current readiness. Retained rows preserve it
	// through temporary disabled/conflicted configurations and restart.
	for _, event := range next {
		if event.OriginID == n.ID {
			key := event.MonitorID + "/" + n.ID
			n.pushAdmission[key] = max(n.pushAdmission[key], event.ReceivedAt)
		}
	}
	n.rebuildPushTokenIndex()
	return nil
}

// Wire validation deliberately does not inspect mutable Node maps or require a
// matching config: a valid event page may precede its immutable config proof.
func (n *Node) validatePushEvents(events []heartbeat.Event) error {
	if len(events) > pageRecords {
		return fail(400, "Too many heartbeat events")
	}
	if len(events) == 0 {
		return nil
	}
	if _, err := heartbeat.Merge(nil, events, n.known, n.now()); err != nil {
		return fail(400, "Invalid heartbeat events")
	}
	return nil
}

func (n *Node) mergePushEvents(incoming []heartbeat.Event) (map[string]heartbeat.Event, error) {
	if len(incoming) == 0 {
		return n.pushEvents, nil
	}
	next, err := heartbeat.Merge(n.pushEvents, incoming, n.known, n.now())
	if err != nil {
		return nil, fail(400, "Invalid heartbeat events")
	}
	if reflect.DeepEqual(next, n.pushEvents) {
		return n.pushEvents, nil
	}
	return next, nil
}

func samePushMap(a, b map[string]heartbeat.Event) bool {
	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

func (n *Node) persistPushEvents(e dbExec, events map[string]heartbeat.Event) error {
	if samePushMap(events, n.pushEvents) {
		return nil
	}
	for key, event := range events {
		if old, ok := n.pushEvents[key]; ok && equalJSON(old, event) {
			continue
		}
		if err := put(e, "heartbeatEvents", key, event); err != nil {
			return err
		}
	}
	return nil
}

func pushConfig(m Monitor, known map[string]bool) heartbeat.Config {
	return heartbeat.Config{MonitorID: m.ID, Generation: checkGeneration(m), StartedAt: m.PushStartedAt, IntervalMs: checkInterval(m.MonitorInput), Known: known}
}

func (n *Node) publishPushEvents(events map[string]heartbeat.Event) {
	if samePushMap(events, n.pushEvents) {
		return
	}
	previous := n.pushEvents
	n.pushEvents = events
	for id := range n.heads {
		m := n.current(id)
		if m == nil || m.Type != "push" {
			continue
		}
		c := pushConfig(*m, n.known)
		if heartbeat.Witness(c, previous) == heartbeat.Witness(c, events) {
			continue
		}
		if cancel := n.probing[id]; cancel != nil {
			cancel()
		}
		if n.nextProbe != nil {
			n.nextProbe[id] = 0
		}
	}
}

// These methods are called under n.mu both at dispatch and before committing
// the scheduler sample. A newer signal invalidates even a precomputed expiry.
func (n *Node) pushEvaluation(m Monitor) (ProbeResult, string) {
	result, witness, _ := n.pushEvaluationAt(m, n.now())
	return result, witness
}

// The result, validity deadline and sample timestamp share a single clock
// observation. Later SQL work cannot turn a pre-expiry UP into a sample that
// claims to have been measured after expiry.
func (n *Node) pushEvaluationAt(m Monitor, at int64) (ProbeResult, string, int64) {
	r, err := heartbeat.Evaluate(pushConfig(m, n.known), n.pushEvents, at)
	if err != nil {
		return ProbeResult{State: "unknown", Message: "Heartbeat clock or state unavailable", LatencyAvailable: Ptr(false)}, contentID(struct{ Register, State string }{heartbeat.Witness(pushConfig(m, n.known), n.pushEvents), "unknown"}), 0
	}
	result := ProbeResult{State: r.State, Message: r.Message, LatencyAvailable: Ptr(r.LatencyMs != nil)}
	if r.LatencyMs != nil {
		result.LatencyMs = *r.LatencyMs
	}
	return result, contentID(struct {
		Register string
		Result   ProbeResult
		Deadline int64
	}{r.Witness, result, r.Deadline}), r.Deadline
}

func (n *Node) pushWitnessCurrent(m Monitor, witness string) bool {
	_, current := n.pushEvaluation(m)
	return witness == current
}

// pushNextDeadline lets the scheduler observe the persisted initial grace and
// remote-origin expiry promptly, even after a metadata edit or a delayed page.
func (n *Node) pushNextDeadline(m Monitor) int64 {
	r, err := heartbeat.Evaluate(pushConfig(m, n.known), n.pushEvents, n.now())
	if err != nil {
		return 0
	}
	return r.Deadline
}

func parsePushSignal(body []byte, query url.Values, get bool) (heartbeat.Signal, error) {
	s := heartbeat.Signal{State: "up"}
	bad := func() (heartbeat.Signal, error) { return heartbeat.Signal{}, fail(400, "Invalid push payload") }
	if get {
		for key, values := range query {
			if len(values) != 1 {
				return bad()
			}
			switch key {
			case "status":
				s.State = values[0]
			case "msg":
				s.Message = values[0]
			case "ping":
				v, err := strconv.ParseFloat(values[0], 64)
				if err != nil || strings.TrimSpace(values[0]) != values[0] {
					return bad()
				}
				s.LatencyMs = &v
			default:
				return bad()
			}
		}
	} else {
		if len(query) != 0 {
			return bad()
		}
		fields, err := heartbeat.DecodeObject(body, map[string]bool{"status": true, "msg": true, "ping": true})
		if err != nil {
			return bad()
		}
		if raw, ok := fields["status"]; ok && json.Unmarshal(raw, &s.State) != nil {
			return bad()
		}
		if raw, ok := fields["msg"]; ok && json.Unmarshal(raw, &s.Message) != nil {
			return bad()
		}
		if raw, ok := fields["ping"]; ok {
			var latency float64
			if json.Unmarshal(raw, &latency) != nil {
				return bad()
			}
			s.LatencyMs = &latency
		}
	}
	if len(s.Message) > heartbeat.MaxMessageBytes || !utf8.ValidString(s.Message) {
		return bad()
	}
	if s.Message == "" {
		s.Message = "Heartbeat: " + s.State
	} else {
		s.Message = safeMessage(s.Message)
	}
	if heartbeat.ValidateSignal(s) != nil {
		return bad()
	}
	return s, nil
}

func validPushSecret(token string) bool {
	if len(token) != 64 {
		return false
	}
	for _, c := range token {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return tokenValue(token) == nil
}

// pushIngressMonitor is called under n.mu. The derived index contains every
// live candidate, including disabled/unready/conflicted heads. An unknown or
// ambiguous digest ends here without work proportional to retained head count.
func (n *Node) pushIngressMonitor(token, hash string, get bool) *Monitor {
	id := n.pushTokenIndex[hash]
	if id == "" {
		return nil
	}
	m := n.current(id)
	if m == nil || !m.Enabled || m.Type != "push" || m.Push == nil || get && !m.Push.AllowGET || !matches(hash, m.Push.Token) {
		return nil
	}
	// Credential separation is also checked at ingress in case private peer
	// configuration overlaps an independently authorized local credential.
	if matches(n.clusterHash, token) {
		return nil
	}
	for _, account := range n.accounts {
		if account.Hash != "" && matches(account.Hash, token) {
			return nil
		}
	}
	if _, exists := n.sessions[hash]; exists {
		return nil
	}
	return m
}

// servePush accepts POST /api/push with Authorization: Push <token> and opt-in
// GET /api/push/<token>. Responses never reflect the URL, token, or payload.
// The caller invokes it before management authentication.
func (n *Node) servePush(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/api/push" && !strings.HasPrefix(r.URL.Path, "/api/push/") {
		return false
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	missing := func() { reply(w, 404, map[string]string{"error": "Push endpoint unavailable"}) }
	token := ""
	get := r.Method == http.MethodGet
	if get {
		if strings.HasPrefix(r.URL.Path, "/api/push/") {
			token = strings.TrimPrefix(r.URL.Path, "/api/push/")
		}
		if len(r.Header.Values("Authorization")) != 0 {
			missing()
			return true
		}
	} else if r.URL.Path == "/api/push" && len(r.Header.Values("Authorization")) == 1 {
		authorization := r.Header.Get("Authorization")
		if strings.HasPrefix(authorization, "Push ") {
			token = strings.TrimPrefix(authorization, "Push ")
		}
	}
	if !validPushSecret(token) {
		missing()
		return true
	}
	hash := digest(token)
	n.mu.Lock()
	authorized := n.pushIngressMonitor(token, hash, get) != nil
	n.mu.Unlock()
	if !authorized {
		missing()
		return true
	}
	// Read a small bounded body before taking the commit mutex; a slow client
	// cannot keep probes, replication, and delivery behind the reader.
	var body []byte
	var payloadErr error
	if len(r.URL.RawQuery) > pushBodyBytes || r.ContentLength > pushBodyBytes {
		payloadErr = fail(413, "Push payload too large")
	} else if r.Body != nil {
		var err error
		body, err = io.ReadAll(io.LimitReader(r.Body, pushBodyBytes+1))
		if err != nil {
			payloadErr = fail(400, "Invalid push payload")
		} else if len(body) > pushBodyBytes {
			payloadErr = fail(413, "Push payload too large")
		}
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		payloadErr = fail(400, "Invalid push payload")
	}
	var signal heartbeat.Signal
	if payloadErr == nil {
		if get && len(bytes.TrimSpace(body)) != 0 {
			payloadErr = fail(400, "GET push must not have a body")
		} else {
			signal, payloadErr = parsePushSignal(body, query, get)
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	// Revalidate after reading: rotation, deletion, conflicts, or credential
	// changes during a slow body must not authorize the earlier binding.
	monitor := n.pushIngressMonitor(token, hash, get)
	if monitor == nil {
		missing()
		return true
	}
	if r.Method != http.MethodPost && !get {
		reply(w, 405, map[string]string{"error": "Push method unavailable"})
		return true
	}
	if n.stopped || n.fault != nil {
		reply(w, 503, map[string]string{"error": "Push persistence unavailable"})
		return true
	}
	if payloadErr != nil {
		errorReply(w, payloadErr)
		return true
	}
	signal.Message = strings.ReplaceAll(signal.Message, token, "[redacted]")
	if n.pushAdmission == nil {
		n.pushAdmission = map[string]int64{}
	}
	now := n.now()
	key := monitor.ID + "/" + n.ID
	last := n.pushAdmission[key]
	if previous, ok := n.pushEvents[heartbeat.Key(heartbeat.Event{MonitorID: monitor.ID, Generation: checkGeneration(*monitor), OriginID: n.ID})]; ok {
		last = max(last, previous.ReceivedAt)
	}
	if last > 0 && now < last+pushAdmissionMs {
		w.Header().Set("Retry-After", "1")
		reply(w, 429, map[string]string{"error": "Push accepted too recently"})
		return true
	}
	sequence, err := n.nextProbeSequence()
	if err != nil {
		n.pushFault()
		reply(w, 503, map[string]string{"error": "Push persistence unavailable"})
		return true
	}
	event, err := heartbeat.NewEvent(pushConfig(*monitor, n.known), n.pushEvents, n.ID, sequence, now, signal)
	if err != nil {
		reply(w, 503, map[string]string{"error": "Push state unavailable"})
		return true
	}
	events, err := n.mergePushEvents([]heartbeat.Event{event})
	if err != nil {
		reply(w, 503, map[string]string{"error": "Push state unavailable"})
		return true
	}
	tx, err := n.store.db.Begin()
	if err == nil {
		err = n.persistPushEvents(tx, events)
		if err == nil {
			err = put(tx, "settings", "sequence", sequence)
		}
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
	}
	if err != nil {
		n.pushFault()
		reply(w, 503, map[string]string{"error": "Push persistence unavailable"})
		return true
	}
	n.sequence = sequence
	// One entry per retained monitor/local origin. Configuration changes must
	// not erase an acceptance that still protects the admission interval.
	n.pushAdmission[key] = now
	n.publishPushEvents(events)
	reply(w, 200, map[string]bool{"ok": true})
	return true
}

func (n *Node) pushFault() {
	n.fault = errors.New("Heartbeat persistence failed")
	if n.cancel != nil {
		n.cancel()
	}
}
