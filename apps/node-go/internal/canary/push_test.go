package canary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"uptime-canary/apps/node-go/internal/heartbeat"
)

const pushFixtureSecret = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const pushRotatedSecret = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

func pushFixture(t *testing.T, clock *atomic.Int64, id string, peers []Peer) (*Node, Options) {
	t.Helper()
	o := Options{NodeID: id, DBPath: filepath.Join(t.TempDir(), "push.db"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: clock.Load, Peers: peers}
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	pushAttach(t, n)
	return n, o
}
func pushAttach(t *testing.T, n *Node) {
	t.Helper()
	server := httptest.NewServer(n)
	n.url = server.URL
	n.nextSync = maxSafe
	t.Cleanup(func() { server.Close(); _ = n.Stop() })
}
func pushInput(ids []string) MonitorInput {
	in := input(ids, "", "")
	in.Type = "push"
	in.IntervalMs = 1000
	in.TimeoutMs = 100
	in.Push = &PushMonitorOptions{Token: pushFixtureSecret}
	return in
}
func pushCall(t *testing.T, n *Node, method, path, auth, body string) (int, []byte) {
	t.Helper()
	r, err := http.NewRequest(method, n.URL()+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	r.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	b, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, b
}
func pushAccept(t *testing.T, n *Node, state string) {
	t.Helper()
	status, b := pushCall(t, n, "POST", "/api/push", "Push "+pushFixtureSecret, `{"status":"`+state+`","msg":"fixture `+state+`","ping":0}`)
	if status != 200 {
		t.Fatalf("push accept: %d %s", status, b)
	}
}
func pushEdit(t *testing.T, n *Node, m Monitor, edit func(*MonitorInput)) Monitor {
	t.Helper()
	in := m.MonitorInput
	edit(&in)
	b, _ := json.Marshal(in)
	body := map[string]any{}
	_ = json.Unmarshal(b, &body)
	body["revision"] = m.Revision
	status, out := api(t, n, "/api/monitors/"+m.ID, "PUT", body, adminToken)
	if status != 200 {
		t.Fatalf("push edit: %d %s", status, out)
	}
	var next Monitor
	if json.Unmarshal(out, &next) != nil {
		t.Fatal("push edit decode")
	}
	return next
}

func TestPushAPIStatePersistenceRestartAndMetadataGrace(t *testing.T) {
	var clock atomic.Int64
	clock.Store(100000)
	n, o := pushFixture(t, &clock, "a", nil)
	m := add(t, n, pushInput([]string{"a"}))
	generation, anchor := checkGeneration(m), m.PushStartedAt
	partitionDrive(t, n)
	if n.samples[m.ID+"/a"].State != "pending" {
		t.Fatal("initial grace not pending")
	}
	clock.Add(700)
	tag := createTag(t, n, TagInput{Name: "Push environment", Color: "#123456"})
	m = pushEdit(t, n, m, func(in *MonitorInput) {
		in.Name = "Renamed heartbeat"
		in.Tags = []MonitorTag{{TagID: tag.ID, Value: "test"}}
	})
	if m.PushStartedAt != anchor || checkGeneration(m) != generation {
		t.Fatal("metadata reset generation grace")
	}
	partitionDrive(t, n)
	clock.Store(anchor + 1000)
	partitionDrive(t, n)
	if n.samples[m.ID+"/a"].State != "down" {
		t.Fatal("metadata deferred original heartbeat deadline")
	}
	beforeRevision := m.Revision
	beforeEdges := len(n.revisionEdges)
	clock.Add(100)
	pushAccept(t, n, "up")
	partitionDrive(t, n)
	sample := n.samples[m.ID+"/a"]
	if sample.State != "up" || sample.LatencyAvailable == nil || !*sample.LatencyAvailable || sample.LatencyMs != 0 {
		t.Fatal("measured zero latency not preserved", sample)
	}
	if n.current(m.ID).Revision != beforeRevision || len(n.revisionEdges) != beforeEdges || len(n.pushEvents) != 1 {
		t.Fatal("heartbeat mutated configuration")
	}
	stored, ok, err := loadOne[int64](n.store, "settings", "sequence")
	if err != nil || !ok || stored != n.sequence {
		t.Fatal("heartbeat/sample sequence not durable")
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	clock.Add(1000)
	restarted, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	pushAttach(t, restarted)
	partitionDrive(t, restarted)
	if restarted.current(m.ID).PushStartedAt != anchor || len(restarted.pushEvents) != 1 || restarted.samples[m.ID+"/a"].State != "down" {
		t.Fatal("restart reset heartbeat state/expiry")
	}
	if status, _ := pushCall(t, restarted, "POST", "/api/push", "Push "+pushFixtureSecret, `{"status":"up"}`); status != 200 {
		t.Fatal("restart heartbeat refused", status)
	}
	partitionDrive(t, restarted)
	if restarted.samples[m.ID+"/a"].LatencyAvailable == nil || *restarted.samples[m.ID+"/a"].LatencyAvailable {
		t.Fatal("omitted latency fabricated")
	}
}

func TestPushAPIAuthenticationBoundsRedactionAndRotation(t *testing.T) {
	var clock atomic.Int64
	clock.Store(200000)
	n, _ := pushFixture(t, &clock, "a", nil)
	m := add(t, n, pushInput([]string{"a"}))
	for _, auth := range []string{"", "Bearer " + adminToken, "Bearer " + clusterToken, "Bearer " + pushFixtureSecret, "Push " + pushRotatedSecret} {
		status, b := pushCall(t, n, "POST", "/api/push", auth, `{"status":"up"}`)
		if status != 404 || bytes.Contains(b, []byte(pushFixtureSecret)) {
			t.Fatal("token boundary or error reflection", status)
		}
	}
	if status, _ := pushCall(t, n, "GET", "/api/push/"+pushFixtureSecret+"?status=up", "", ""); status != 404 {
		t.Fatal("GET enabled by default")
	}
	for _, body := range []string{`null`, `{"status":null}`, `{"Status":"up"}`, `{"status":"up","status":"down"}`, `{"status":"pending"}`, `{"msg":2}`, `{"ping":null}`, `{"ping":-1}`, `{"ping":1e309}`, `{"ping":3600001}`, `{"context":{}}`, `{"monitorId":"arbitrary"}`, `{"receivedAt":1}`, `{"msg":"` + strings.Repeat("x", 257) + `"}`} {
		if status, _ := pushCall(t, n, "POST", "/api/push", "Push "+pushFixtureSecret, body); status != 400 {
			t.Fatal("bad payload accepted", status, body)
		}
	}
	if status, _ := pushCall(t, n, "POST", "/api/push", "Push "+pushFixtureSecret, strings.Repeat("x", 4097)); status != 413 {
		t.Fatal("body cap not enforced", status)
	}
	pushAccept(t, n, "up")
	if status, _ := pushCall(t, n, "POST", "/api/push", "Push "+pushFixtureSecret, `{}`); status != 429 {
		t.Fatal("admission limit not enforced")
	}
	clock.Add(100)
	if status, _ := pushCall(t, n, "POST", "/api/push", "Push "+pushFixtureSecret, `{"msg":"`+pushFixtureSecret+`"}`); status != 200 {
		t.Fatal("message fixture failed")
	}
	for _, e := range n.pushEvents {
		if strings.Contains(e.Message, pushFixtureSecret) {
			t.Fatal("secret reflected in heartbeat state")
		}
	}
	if status, _ := api(t, n, "/api/accounts", "POST", map[string]string{"name": "push-viewer", "role": "viewer", "token": viewerToken}, adminToken); status != 201 {
		t.Fatal("viewer creation")
	}
	if status, b := api(t, n, "/api/overview", "GET", nil, viewerToken); status != 200 || bytes.Contains(b, []byte(pushFixtureSecret)) {
		t.Fatal("viewer saw heartbeat secret")
	}
	oldGeneration := checkGeneration(m)
	clock.Add(100)
	m = pushEdit(t, n, m, func(in *MonitorInput) { in.Push = &PushMonitorOptions{Token: pushRotatedSecret, AllowGET: true} })
	if checkGeneration(m) == oldGeneration || m.PushStartedAt != clock.Load() {
		t.Fatal("token/GET rotation did not restart generation")
	}
	if status, _ := pushCall(t, n, "POST", "/api/push", "Push "+pushFixtureSecret, `{}`); status != 404 {
		t.Fatal("retired token still accepted")
	}
	if status, _ := pushCall(t, n, "GET", "/api/push/"+pushRotatedSecret+"?status=down&msg=job+failed&ping=12.5", "", ""); status != 200 {
		t.Fatal("opt-in GET failed", status)
	}
	for _, query := range []string{"status=up&status=down", "other=x", "ping=NaN", "ping=-1", "msg=" + strings.Repeat("x", 257), "status=%zz"} {
		if status, _ := pushCall(t, n, "GET", "/api/push/"+pushRotatedSecret+"?"+query, "", ""); status != 400 {
			t.Fatal("bad GET query", status, query)
		}
	}
}

func TestPushReplicatedSignalsAnyIngressAndPartitionRecovery(t *testing.T) {
	var clock atomic.Int64
	clock.Store(300000)
	a, _ := pushFixture(t, &clock, "a", []Peer{{ID: "b", URL: "http://127.0.0.1:1"}})
	b, _ := pushFixture(t, &clock, "b", []Peer{{ID: "a", URL: "http://127.0.0.1:1"}})
	m := add(t, a, pushInput([]string{"a"}))
	for _, page := range causalCapacityPages(t, a) {
		revisionJoinsApply(t, b, page)
	}
	// b is not assigned to observe this monitor, but remains valid ingress.
	pushAccept(t, b, "up")
	for _, page := range causalCapacityPages(t, b) {
		revisionJoinsApply(t, a, page)
	}
	partitionDrive(t, a)
	if a.samples[m.ID+"/a"].State != "up" || len(b.samples) != 0 {
		t.Fatal("connected assigned observer expired remote ingress")
	}
	clock.Add(100)
	pushAccept(t, a, "down")
	pushAccept(t, b, "up")
	for _, page := range causalCapacityPages(t, b) {
		revisionJoinsApply(t, a, page)
	}
	if result, _ := a.pushEvaluation(*a.current(m.ID)); result.State != "down" {
		t.Fatal("partition concurrent UP erased DOWN")
	}
	// Complete ordinary HTTP peer protocol, then accept a causally later UP.
	for _, page := range causalCapacityPages(t, a) {
		data, _ := json.Marshal(page)
		r, _ := http.NewRequest("POST", b.URL()+"/internal/snapshot", bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer "+clusterToken)
		r.Header.Set("X-Canary-Node-Id", "a")
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatal("peer HTTP rejected heartbeat page", res.StatusCode)
		}
	}
	clock.Add(100)
	pushAccept(t, b, "up")
	for _, page := range causalCapacityPages(t, b) {
		revisionJoinsApply(t, a, page)
	}
	partitionDrive(t, a)
	if a.samples[m.ID+"/a"].State != "up" {
		t.Fatal("causally later accepted UP did not recover")
	}
	clock.Add(1000)
	partitionDrive(t, a)
	if a.samples[m.ID+"/a"].State != "down" {
		t.Fatal("replicated heartbeat did not expire")
	}
}

func TestPushOutOfOrderPagesAndStrictEventWire(t *testing.T) {
	var clock atomic.Int64
	clock.Store(400000)
	a, _ := pushFixture(t, &clock, "a", []Peer{{ID: "b", URL: "http://127.0.0.1:1"}})
	b, _ := pushFixture(t, &clock, "b", []Peer{{ID: "a", URL: "http://127.0.0.1:1"}})
	m := add(t, a, pushInput([]string{"b"}))
	pushAccept(t, a, "up")
	eventPage := Snapshot{Heartbeats: []heartbeat.Event{}}
	for _, e := range a.pushEvents {
		eventPage.Heartbeats = append(eventPage.Heartbeats, e)
	}
	revisionJoinsApply(t, b, eventPage)
	if b.current(m.ID) != nil || len(b.samples) != 0 {
		t.Fatal("heartbeat page invented ready config")
	}
	for _, page := range causalCapacityPages(t, a) {
		revisionJoinsApply(t, b, page)
	}
	partitionDrive(t, b)
	if b.samples[m.ID+"/b"].State != "up" {
		t.Fatal("passive early heartbeat did not activate with correct config")
	}
	clock.Add(100)
	m = pushEdit(t, a, m, func(in *MonitorInput) { in.IntervalMs = 2000 })
	pushAccept(t, a, "down")
	newPage := Snapshot{Heartbeats: []heartbeat.Event{}}
	for _, e := range a.pushEvents {
		if e.Generation == checkGeneration(m) {
			newPage.Heartbeats = append(newPage.Heartbeats, e)
		}
	}
	revisionJoinsApply(t, b, newPage)
	if result, _ := b.pushEvaluation(*b.current(m.ID)); result.State != "up" {
		t.Fatal("future generation contaminated old config")
	}
	for _, page := range causalCapacityPages(t, a) {
		revisionJoinsApply(t, b, page)
	}
	revisionJoinsApply(t, b, eventPage)
	partitionDrive(t, b)
	if b.samples[m.ID+"/b"].State != "down" {
		t.Fatal("old generation overwrote current failure")
	}
	wire := Snapshot{Protocol: peerProtocol, Node: Identity{"a", "a"}, Versions: []Version{}, Observations: []Sample{}, Incidents: []Incident{}, Receipts: []Receipt{}, Heartbeats: newPage.Heartbeats}
	encoded, _ := json.Marshal(wire)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(encoded, &fields)
	rawEvent, _ := json.Marshal(newPage.Heartbeats[0])
	mutated := strings.TrimSuffix(string(rawEvent), "}") + `,"state":"up"}`
	fields["heartbeats"] = json.RawMessage("[" + mutated + "]")
	bad, _ := json.Marshal(fields)
	if _, err := b.validateSnapshot(bad, "a"); err == nil {
		t.Fatal("duplicate heartbeat wire field accepted")
	}
}

func TestPushTemporalAndArrivalWitnessGuards(t *testing.T) {
	var clock atomic.Int64
	clock.Store(500000)
	n, _ := pushFixture(t, &clock, "a", nil)
	m := add(t, n, pushInput([]string{"a"}))
	_, pendingWitness := n.pushEvaluation(m)
	clock.Add(1000)
	if n.pushWitnessCurrent(m, pendingWitness) {
		t.Fatal("pending witness survived grace expiry")
	}
	_, expiredWitness := n.pushEvaluation(m)
	pushAccept(t, n, "up")
	if n.pushWitnessCurrent(m, expiredWitness) {
		t.Fatal("precomputed expiry survived heartbeat arrival")
	}
	_, freshWitness := n.pushEvaluation(m)
	clock.Add(999)
	if !n.pushWitnessCurrent(m, freshWitness) {
		t.Fatal("stable heartbeat witness changed before deadline")
	}
	clock.Add(1)
	if n.pushWitnessCurrent(m, freshWitness) {
		t.Fatal("fresh witness survived heartbeat expiry")
	}
	// Publishing while a push evaluation is in flight cancels its context and
	// makes the scheduler immediately eligible to recompute.
	ctx, cancel := context.WithCancel(n.ctx)
	n.probing[m.ID] = cancel
	n.nextProbe[m.ID] = maxSafe
	clock.Add(100)
	pushAccept(t, n, "down")
	if ctx.Err() != context.Canceled || n.nextProbe[m.ID] != 0 {
		t.Fatal("arrival did not cancel/reschedule stale push work")
	}
	delete(n.probing, m.ID)
}

func TestPushGenerationAnchorAcrossLargeConflictJoin(t *testing.T) {
	var clock atomic.Int64
	clock.Store(600000)
	n, _ := pushFixture(t, &clock, "a", nil)
	m := add(t, n, pushInput([]string{"a"}))
	s := Snapshot{}
	for i := range 71 {
		tip := m
		tip.Revision = fmt.Sprintf("push-tip-%03d", i)
		tip.CheckGeneration = checkGeneration(m)
		tip.Name = tip.Revision
		tip.PushStartedAt += int64(i % 2)
		edge := newRevisionEdge("monitor", m.ID, tip.Revision, tip, []string{m.Revision}, checkGeneration(tip))
		s.RevisionEdges = append(s.RevisionEdges, edge)
		s.Versions = append(s.Versions, Version{Monitor: tip, Ancestors: edge.Parents, EdgeID: edge.ID})
	}
	revisionJoinsApply(t, n, s)
	chosen := m
	chosen.Name = "Resolved"
	chosen.Revision = "push-71-resolution"
	chosen.UpdatedAt = clock.Add(500)
	v, edges, err := n.joinMonitorRevision(chosen, n.heads[m.ID])
	if err != nil {
		t.Fatal(err)
	}
	if checkGeneration(v.Monitor) == checkGeneration(m) || v.Monitor.PushStartedAt != chosen.UpdatedAt {
		t.Fatal("different inherited anchors did not force fresh generation")
	}
	revisionJoinsApply(t, n, Snapshot{Versions: []Version{v}, RevisionEdges: edges})
	if len(n.heads[m.ID]) != 1 {
		t.Fatal("large anchor conflict did not resolve")
	}
}

func TestPushSQLFailureAndReservedSequenceFailClosed(t *testing.T) {
	var clock atomic.Int64
	clock.Store(700000)
	n, _ := pushFixture(t, &clock, "a", nil)
	m := add(t, n, pushInput([]string{"a"}))
	before := n.sequence
	if _, err := n.store.db.Exec(`CREATE TRIGGER push_reject BEFORE INSERT ON records WHEN NEW.category='heartbeatEvents' BEGIN SELECT RAISE(FAIL,'fixture denied'); END`); err != nil {
		t.Fatal(err)
	}
	status, b := pushCall(t, n, "POST", "/api/push", "Push "+pushFixtureSecret, `{"status":"up"}`)
	if status != 503 || len(n.pushEvents) != 0 || n.sequence != before || n.Error() == nil || bytes.Contains(b, []byte(pushFixtureSecret)) {
		t.Fatal("failed heartbeat publish was not atomic", status)
	}
	persisted, _, err := loadOne[int64](n.store, "settings", "sequence")
	if err != nil || persisted != before {
		t.Fatal("failed heartbeat committed sequence without register")
	}
	if n.current(m.ID) == nil {
		t.Fatal("failed heartbeat removed config")
	}
}

func TestPushRestoreStartsNewGraceWithRetainedOrigin(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	n, o := pushFixture(t, &clock, "a", nil)
	m := add(t, n, pushInput([]string{"a"}))
	pushAccept(t, n, "up")
	staged := originRestoreReviewStage(t, o.DBPath)
	identity := filepath.Join(filepath.Dir(o.DBPath), ".canary-identities")
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	activation, err := ActivateRestoredDatabase(context.Background(), staged, identity)
	if err != nil {
		t.Fatal(err)
	}
	clock.Store(activation.ActivatedAt)
	o.DBPath, o.IdentityDirectory = staged, identity
	restored, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	pushAttach(t, restored)
	current := restored.current(m.ID)
	if current == nil || checkGeneration(*current) == checkGeneration(m) || current.PushStartedAt != current.UpdatedAt || current.PushStartedAt > activation.ActivatedAt {
		if current == nil {
			t.Fatal("restored monitor not ready")
		}
		t.Fatalf("restore generation changed=%v anchor=%d activation=%d updated=%d", checkGeneration(*current) != checkGeneration(m), current.PushStartedAt, activation.ActivatedAt, current.UpdatedAt)
	}
	if result, _ := restored.pushEvaluation(*current); result.State != "pending" {
		t.Fatal("restore replayed old UP")
	}
	clock.Add(current.IntervalMs)
	partitionDrive(t, restored)
	if restored.samples[m.ID+"/a"].State != "down" {
		t.Fatal("restored generation never missed heartbeat")
	}
}

func TestPushRetriesInversionAndDeadlineEdits(t *testing.T) {
	var clock atomic.Int64
	clock.Store(800000)
	n, _ := pushFixture(t, &clock, "a", nil)
	in := pushInput([]string{"a"})
	in.Retries = 1
	in.RetryIntervalMs = Ptr(int64(100))
	m := add(t, n, in)
	pushAccept(t, n, "down")
	partitionDrive(t, n)
	if s := n.samples[m.ID+"/a"]; s.State != "pending" || s.RawState != "down" || s.ConsecutiveFailures != 1 {
		t.Fatal("push bypassed retry policy", s)
	}
	clock.Add(99)
	partitionDrive(t, n)
	if n.samples[m.ID+"/a"].ConsecutiveFailures != 1 {
		t.Fatal("push retry ignored interval")
	}
	clock.Add(1)
	partitionDrive(t, n)
	if n.samples[m.ID+"/a"].State != "down" {
		t.Fatal("push retry did not confirm failure")
	}
	pushAccept(t, n, "up")
	partitionDrive(t, n)
	if n.samples[m.ID+"/a"].State != "up" {
		t.Fatal("up did not reset retry state")
	}
	previous := checkGeneration(m)
	clock.Add(100)
	m = pushEdit(t, n, m, func(in *MonitorInput) { in.IntervalMs = 2000; in.UpsideDown = true; in.Retries = 0 })
	if checkGeneration(m) == previous || m.PushStartedAt != clock.Load() {
		t.Fatal("deadline/inversion edit did not reset generation")
	}
	pushAccept(t, n, "down")
	partitionDrive(t, n)
	if s := n.samples[m.ID+"/a"]; s.State != "up" || s.RawState != "down" {
		t.Fatal("inversion was not applied", s)
	}
}

func TestPushAmbiguousRemoteTokensAndLocalReuseFailClosed(t *testing.T) {
	var clock atomic.Int64
	clock.Store(900000)
	n, _ := pushFixture(t, &clock, "a", nil)
	m := add(t, n, pushInput([]string{"a"}))
	if status, _ := api(t, n, "/api/monitors", "POST", pushInput([]string{"a"}), adminToken); status != 409 {
		t.Fatal("local token reuse accepted", status)
	}
	other := m
	other.ID = "remote-duplicate"
	other.Revision = "remote-duplicate-revision"
	other.CheckGeneration = ""
	edge := newRevisionEdge("monitor", other.ID, other.Revision, other, nil, other.Revision)
	if err := n.applySnapshot(Snapshot{Versions: []Version{{Monitor: other, Ancestors: []string{}, EdgeID: edge.ID}}, RevisionEdges: []RevisionEdge{edge}}); err != nil {
		t.Fatal(err)
	}
	if status, _ := pushCall(t, n, "POST", "/api/push", "Push "+pushFixtureSecret, `{}`); status != 404 {
		t.Fatal("remote ambiguous token chose an arbitrary monitor", status)
	}
}

func TestPushExternalOriginFailureAndRestartAdmission(t *testing.T) {
	var clock atomic.Int64
	clock.Store(1000000)
	n, o := pushFixture(t, &clock, "a", nil)
	_ = add(t, n, pushInput([]string{"a"}))
	pushAccept(t, n, "up")
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	pushAttach(t, restarted)
	if status, _ := pushCall(t, restarted, "POST", "/api/push", "Push "+pushFixtureSecret, `{}`); status != 429 {
		t.Fatal("restart reset acceptance admission")
	}
	clock.Add(100)
	previous := restarted.sequence
	if err := restarted.store.origin.Close(); err != nil {
		t.Fatal(err)
	}
	if status, _ := pushCall(t, restarted, "POST", "/api/push", "Push "+pushFixtureSecret, `{}`); status != 503 || restarted.Error() == nil || restarted.sequence != previous {
		t.Fatal("origin failure did not fail closed", status)
	}
}
