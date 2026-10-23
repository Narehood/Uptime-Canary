package canary

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"uptime-canary/apps/node-go/internal/heartbeat"
)

// Independent round-two review. Only test fixtures are mutable; these tests do
// not change production clocks, SQL hooks, or publication code on disk.
type pushFinalReader struct {
	source io.Reader
	before func()
	once   sync.Once
	reads  int
}

func (r *pushFinalReader) Read(p []byte) (int, error) {
	r.reads++
	r.once.Do(func() {
		if r.before != nil {
			r.before()
		}
	})
	return r.source.Read(p)
}

func pushFinalRequest(n *Node, token string, reader io.Reader) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/push", reader)
	r.Header.Set("Authorization", "Push "+token)
	w := httptest.NewRecorder()
	n.ServeHTTP(w, r)
	return w
}

func pushFinalApply(t *testing.T, n *Node, s Snapshot) {
	t.Helper()
	n.mu.Lock()
	err := n.applySnapshot(s)
	n.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func pushFinalPeer(t *testing.T, n *Node, s Snapshot) {
	t.Helper()
	s.Protocol, s.Node = peerProtocol, Identity{"b", "b"}
	s.GroupVersions = []GroupVersion{}
	if s.Versions == nil {
		s.Versions = []Version{}
	}
	s.Observations, s.Incidents, s.Receipts = []Sample{}, []Incident{}, []Receipt{}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/internal/snapshot", strings.NewReader(string(b)))
	r.Header.Set("Authorization", "Bearer "+clusterToken)
	r.Header.Set("X-Canary-Node-Id", "b")
	w := httptest.NewRecorder()
	n.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("peer fixture rejected: %d %s", w.Code, w.Body.String())
	}
}

func pushFinalVersion(m Monitor, revision, token string, parents ...string) (Version, RevisionEdge) {
	m.Revision, m.CheckGeneration = revision, ""
	if token != "" {
		m.Push = &PushMonitorOptions{Token: token}
	}
	e := newRevisionEdge("monitor", m.ID, revision, m, parents, checkGeneration(m))
	return Version{Monitor: m, Ancestors: e.Parents, EdgeID: e.ID}, e
}

func TestPushFinalReviewTemporalCommitSweep(t *testing.T) {
	for _, mode := range []string{"up", "up-inverted", "grace", "grace-inverted", "concurrent-down"} {
		for _, freshReads := range []int64{1, 2, 3, 4, 5, 6} {
			t.Run(fmt.Sprintf("%s/fresh-reads-%d", mode, freshReads), func(t *testing.T) {
				var clock atomic.Int64
				clock.Store(3100000)
				n, _ := pushFixture(t, &clock, "a", []Peer{{ID: "b", URL: "http://127.0.0.1:1"}})
				in := pushInput([]string{"a"})
				in.UpsideDown = strings.HasSuffix(mode, "inverted")
				in.RetryIntervalMs = Ptr(int64(86400000))
				m := add(t, n, in)
				deadline := clock.Load() + m.IntervalMs
				beforeRaw, afterRaw := "up", "down"
				switch {
				case strings.HasPrefix(mode, "grace"):
					beforeRaw = "pending"
				case mode == "concurrent-down":
					beforeRaw, afterRaw = "down", "up"
					pushAccept(t, n, "down")
					c := pushConfig(m, n.known)
					b, err := heartbeat.NewEvent(c, nil, "b", 1, clock.Load()+500, heartbeat.Signal{State: "up"})
					if err != nil {
						t.Fatal(err)
					}
					pushFinalApply(t, n, Snapshot{Heartbeats: []heartbeat.Event{b}})
				default:
					pushAccept(t, n, "up")
				}
				beforeSeq := n.sequence
				var reads atomic.Int64
				n.now = func() int64 {
					if reads.Add(1) <= freshReads {
						return deadline - 1
					}
					return deadline
				}
				partitionDrive(t, n)
				first, exists := n.samples[m.ID+"/a"]
				if freshReads == 2 {
					if exists || n.sequence != beforeSeq || len(n.probePolicies) != 0 || n.nextProbe[m.ID] != 0 {
						t.Fatal("final guard crossing consumed observation/policy/sequence authority")
					}
				} else if !exists {
					t.Fatal("stable final observation was not committed")
				} else {
					want := beforeRaw
					if first.CheckedAt >= deadline {
						want = afterRaw
					}
					if first.RawState != want {
						t.Fatalf("sample timestamp and raw observation disagree: %+v want %s", first, want)
					}
					if first.CheckedAt < deadline && n.nextProbe[m.ID] > deadline {
						t.Fatalf("commit discarded captured deadline: next=%d deadline=%d", n.nextProbe[m.ID], deadline)
					}
				}
				// This includes a crossing on the history-cutoff clock read made
				// after sample/policy/sequence SQL writes and before SQL commit.
				clock.Store(deadline)
				n.now = clock.Load
				partitionDrive(t, n)
				last := n.samples[m.ID+"/a"]
				if last.RawState != afterRaw || last.CheckedAt != deadline {
					t.Fatalf("next tick did not observe exact boundary: first=%+v last=%+v", first, last)
				}
				if strings.HasPrefix(mode, "grace") && exists && first.CheckedAt < deadline && first.State != "pending" {
					t.Fatal("inversion turned initial grace into an outcome", first)
				}
				t.Logf("reads=%d first-at=%d raw=%s next-at=%d raw=%s", reads.Load(), first.CheckedAt, first.RawState, last.CheckedAt, last.RawState)
			})
		}
	}
}

func TestPushFinalReviewEnabledAdmissionMaxAndOriginIsolation(t *testing.T) {
	var clock atomic.Int64
	clock.Store(3200000)
	n, options := pushFixture(t, &clock, "a", []Peer{{ID: "b", URL: "http://127.0.0.1:1"}})
	m := add(t, n, pushInput([]string{"a"}))
	pushAccept(t, n, "up")
	m = pushEdit(t, n, m, func(in *MonitorInput) { in.IntervalMs = 2000 })
	clock.Add(100)
	pushAccept(t, n, "up")
	lastLocal := clock.Load()
	m = pushEdit(t, n, m, func(in *MonitorInput) { in.Push = &PushMonitorOptions{Token: pushRotatedSecret} })
	remote, err := heartbeat.NewEvent(pushConfig(m, n.known), nil, "b", 1, clock.Load()+10000, heartbeat.Signal{State: "down"})
	if err != nil {
		t.Fatal(err)
	}
	pushFinalApply(t, n, Snapshot{Heartbeats: []heartbeat.Event{remote}})
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	pushAttach(t, reopened)
	if reopened.pushAdmission[m.ID+"/a"] != lastLocal {
		t.Fatal("did not reconstruct maximum LOCAL timestamp over old generations", reopened.pushAdmission)
	}
	for _, delta := range []int64{0, 99, 100} {
		clock.Store(lastLocal + delta)
		w := pushFinalRequest(reopened, pushRotatedSecret, strings.NewReader(`{}`))
		want := 429
		if delta == 100 {
			want = 200
		}
		if w.Code != want {
			t.Fatalf("admission at delta %d: got %d want %d", delta, w.Code, want)
		}
	}
	if result, _ := reopened.pushEvaluation(m); result.State != "up" {
		t.Fatal("accepted local UP failed to causally dominate the current remote DOWN", result)
	}
}

// Required admission behavior includes temporary passive configuration. A
// retained local acceptance must not disappear merely because current() is nil
// (or disabled) at restart, or another monitor causes an admission-map prune.
func TestPushFinalReviewAdmissionAcrossPassiveLifecycle(t *testing.T) {
	for _, mode := range []string{"disabled-restart", "unready-restart", "conflict-restart", "disabled-live-prune", "disabled-live-retained", "unready-live-retained", "conflict-live-retained"} {
		t.Run(mode, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(3300000)
			n, options := pushFixture(t, &clock, "a", []Peer{{ID: "b", URL: "http://127.0.0.1:1"}})
			m := add(t, n, pushInput([]string{"a"}))
			originalGeneration := checkGeneration(m)
			pushAccept(t, n, "up")
			acceptedAt := clock.Load()
			clock.Add(50)
			var missing RevisionEdge
			switch {
			case strings.HasPrefix(mode, "disabled"):
				m = pushEdit(t, n, m, func(in *MonitorInput) { in.Enabled = false })
			case strings.HasPrefix(mode, "unready"):
				v, e := pushFinalVersion(m, "final-admission-unready", pushRotatedSecret, m.Revision)
				missing = e
				pushFinalPeer(t, n, Snapshot{Versions: []Version{v}})
			case strings.HasPrefix(mode, "conflict"):
				v1, e1 := pushFinalVersion(m, "final-admission-left", "", m.Revision)
				v2, e2 := pushFinalVersion(m, "final-admission-right", pushRotatedSecret, m.Revision)
				pushFinalPeer(t, n, Snapshot{Versions: []Version{v1, v2}, RevisionEdges: []RevisionEdge{e1, e2}})
			}
			if strings.HasSuffix(mode, "restart") {
				if err := n.Stop(); err != nil {
					t.Fatal(err)
				}
				var err error
				n, err = New(options)
				if err != nil {
					t.Fatal(err)
				}
				pushAttach(t, n)
			} else if mode == "disabled-live-prune" {
				other := pushInput([]string{"a"})
				other.Push = &PushMonitorOptions{Token: pushRotatedSecret}
				_ = add(t, n, other)
				if w := pushFinalRequest(n, pushRotatedSecret, strings.NewReader(`{}`)); w.Code != 200 {
					t.Fatal("other-monitor pruning control", w.Code)
				}
			}
			if strings.HasPrefix(mode, "unready") {
				pushFinalPeer(t, n, Snapshot{RevisionEdges: []RevisionEdge{missing}})
			} else {
				chosen := m.MonitorInput
				chosen.Enabled = true
				n.mu.Lock()
				_, err := n.writeMonitor(chosen, n.heads[m.ID], m.ID, false)
				n.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			current := n.current(m.ID)
			if current == nil || !current.Enabled || checkGeneration(*current) == originalGeneration {
				t.Fatal("fixture failed to restore unique enabled new-generation config")
			}
			if result, _ := n.pushEvaluation(*current); result.State != "pending" {
				t.Fatal("old-generation health escaped generation isolation", result)
			}
			before := n.sequence
			w := pushFinalRequest(n, current.Push.Token, strings.NewReader(`{}`))
			stored, err := loadAll[heartbeat.Event](n.store, "heartbeatEvents")
			if err != nil {
				t.Fatal(err)
			}
			var localTimes []int64
			for _, e := range stored {
				if e.MonitorID == m.ID && e.OriginID == n.ID {
					localTimes = append(localTimes, e.ReceivedAt)
				}
			}
			t.Logf("mode=%s elapsed=%dms HTTP=%d local-events=%v sequence-before=%d after=%d", mode, clock.Load()-acceptedAt, w.Code, localTimes, before, n.sequence)
			if w.Code != 429 || n.sequence != before || len(localTimes) != 1 {
				t.Fatalf("passive lifecycle bypassed durable per-monitor/origin admission at +50ms: HTTP=%d", w.Code)
			}
			clock.Store(acceptedAt + 99)
			if w := pushFinalRequest(n, current.Push.Token, strings.NewReader(`{}`)); w.Code != 429 {
				t.Fatal("admission opened before exact 100ms", w.Code)
			}
			clock.Store(acceptedAt + 100)
			if w := pushFinalRequest(n, current.Push.Token, strings.NewReader(`{}`)); w.Code != 200 {
				t.Fatal("admission failed to open at exact 100ms", w.Code)
			}
		})
	}
}

func TestPushFinalReviewReverseCredentialCandidateMatrix(t *testing.T) {
	for _, kind := range []string{"ready", "disabled", "unready", "conflicted", "duplicate", "live-with-tombstone", "deleted"} {
		t.Run(kind, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(3400000)
			n, _ := pushFixture(t, &clock, "a", nil)
			m := add(t, n, pushInput([]string{"a"}))
			s := Snapshot{}
			switch kind {
			case "disabled":
				m = pushEdit(t, n, m, func(in *MonitorInput) { in.Enabled = false })
			case "unready":
				v, _ := pushFinalVersion(m, "final-reserved-unready", "", m.Revision)
				s.Versions = []Version{v}
			case "conflicted", "live-with-tombstone":
				v, e := pushFinalVersion(m, "final-reserved-live", "", m.Revision)
				dead := m
				dead.Deleted = kind == "live-with-tombstone"
				v2, e2 := pushFinalVersion(dead, "final-reserved-sibling", pushRotatedSecret, m.Revision)
				s = Snapshot{Versions: []Version{v, v2}, RevisionEdges: []RevisionEdge{e, e2}}
			case "duplicate":
				other := m
				other.ID = "final-reserved-other"
				v, e := pushFinalVersion(other, "final-reserved-other-root", "")
				s = Snapshot{Versions: []Version{v}, RevisionEdges: []RevisionEdge{e}}
			case "deleted":
				n.mu.Lock()
				_, err := n.writeMonitor(m.MonitorInput, n.heads[m.ID], m.ID, true)
				n.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			}
			pushFinalApply(t, n, s)
			for _, operation := range []string{"create", "update", "legacy"} {
				body := map[string]string{"name": "final-" + operation, "role": "editor", "token": pushFixtureSecret}
				if kind == "deleted" {
					// One creation is sufficient to prove tombstones release their reservation.
					if operation != "create" {
						continue
					}
				}
				path, method := "/api/accounts", "POST"
				if operation == "update" {
					code, b := api(t, n, path, method, map[string]string{"name": body["name"], "role": "editor", "token": editorToken}, adminToken)
					if code != 201 {
						t.Fatal("distinct editor account control", code, string(b))
					}
					var a AccountView
					if err := json.Unmarshal(b, &a); err != nil {
						t.Fatal(err)
					}
					path, method = path+"/"+a.ID, "PUT"
				}
				before := len(n.accounts)
				code := 0
				if operation == "legacy" {
					b, _ := json.Marshal(body)
					n.mu.Lock()
					_, _, err := n.apiLocked(httptest.NewRequest(method, path, nil), account{AccountView: AccountView{Role: "admin"}}, b)
					n.mu.Unlock()
					if err != nil {
						w := httptest.NewRecorder()
						errorReply(w, err)
						code = w.Code
					}
				} else {
					code, _ = api(t, n, path, method, body, adminToken)
				}
				if kind == "deleted" {
					if code != 201 {
						t.Fatal("tombstone failed to release credential", code)
					}
				} else if code != 400 || len(n.accounts) != before {
					t.Fatalf("%s %s failed reservation: status=%d accounts=%d before=%d", kind, operation, code, len(n.accounts), before)
				}
				if kind != "deleted" {
					if status, _ := api(t, n, "/api/overview", "GET", nil, pushFixtureSecret); status != 401 {
						t.Fatal("reserved push secret gained management authority", status)
					}
				}
			}
		})
	}
}

func TestPushFinalReviewIndexLateSQLFailureAndEdgePublication(t *testing.T) {
	var clock atomic.Int64
	clock.Store(3500000)
	n, options := pushFixture(t, &clock, "a", nil)
	m := add(t, n, pushInput([]string{"a"}))
	before := maps.Clone(n.pushTokenIndex)
	v, edge := pushFinalVersion(m, "final-index-rotated", pushRotatedSecret, m.Revision)
	e, err := heartbeat.NewEvent(pushConfig(v.Monitor, n.known), nil, "a", n.sequence+1, clock.Load(), heartbeat.Signal{State: "up"})
	if err != nil {
		t.Fatal(err)
	}
	s := Snapshot{Versions: []Version{v}, RevisionEdges: []RevisionEdge{edge}, Heartbeats: []heartbeat.Event{e}}
	// The heartbeat and immutable edge have been written when the head
	// statement fails. Rollback must cover them and leave the index unchanged.
	if _, err := n.store.db.Exec(`CREATE TRIGGER push_final_late_fail BEFORE INSERT ON records WHEN NEW.category='heads' BEGIN SELECT RAISE(FAIL,'private late head failure'); END`); err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	err = n.applySnapshot(s)
	n.mu.Unlock()
	if err == nil || !reflect.DeepEqual(before, n.pushTokenIndex) || n.current(m.ID).Revision != m.Revision || len(n.pushEvents) != 0 {
		t.Fatal("failed late SQL published heads, register, or derived index", err)
	}
	stored, ok, err := loadOne[[]Version](n.store, "heads", m.ID)
	if err != nil || !ok || len(stored) != 1 || stored[0].Monitor.Revision != m.Revision {
		t.Fatal("failed late SQL left new persisted heads", err)
	}
	storedEvents, err := loadAll[heartbeat.Event](n.store, "heartbeatEvents")
	if err != nil || len(storedEvents) != 0 {
		t.Fatal("failed late SQL retained its earlier heartbeat insert", err)
	}
	if _, found, err := loadOne[RevisionEdge](n.store, "revisionEdges", edge.ID); err != nil || found {
		t.Fatal("failed late SQL retained its earlier immutable edge", err)
	}
	if _, err := n.store.db.Exec(`DROP TRIGGER push_final_late_fail`); err != nil {
		t.Fatal(err)
	}
	pushFinalApply(t, n, Snapshot{Versions: s.Versions, Heartbeats: s.Heartbeats})
	if id, exists := n.pushTokenIndex[digest(pushRotatedSecret)]; !exists || id != "" || n.current(m.ID) != nil {
		t.Fatal("unready descendant did not reserve an ambiguous binding alongside its retained parent")
	}
	for _, token := range []string{pushFixtureSecret, pushRotatedSecret} {
		r := &pushFinalReader{source: strings.NewReader(`{}`)}
		if w := pushFinalRequest(n, token, r); w.Code != 404 || r.reads != 0 {
			t.Fatal("old/unready credential did not reject before body", w.Code)
		}
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	pushAttach(t, n)
	index := maps.Clone(n.pushTokenIndex)
	pushFinalApply(t, n, Snapshot{RevisionEdges: []RevisionEdge{edge}})
	if reflect.DeepEqual(index, n.pushTokenIndex) || n.current(m.ID) == nil || n.pushTokenIndex[digest(pushRotatedSecret)] != m.ID {
		t.Fatal("edge-only completion failed to publish the now-unique credential binding")
	}
	clock.Add(100) // The earlier private local-origin row also reserves admission.
	if w := pushFinalRequest(n, pushRotatedSecret, strings.NewReader(`{}`)); w.Code != 200 {
		t.Fatal("ready rotated credential rejected", w.Code)
	}
}

func TestPushFinalReviewPeerCollisionPreservesManagementAuthority(t *testing.T) {
	var clock atomic.Int64
	clock.Store(3550000)
	n, _ := pushFixture(t, &clock, "a", []Peer{{ID: "b", URL: "http://127.0.0.1:1"}})
	m := add(t, n, pushInput([]string{"a"}))
	if code, b := api(t, n, "/api/accounts", "POST", map[string]string{"name": "final-independent-editor", "role": "editor", "token": pushRotatedSecret}, adminToken); code != 201 {
		t.Fatal("independent management account fixture", code, string(b))
	}
	v, e := pushFinalVersion(m, "final-peer-credential-collision", pushRotatedSecret, m.Revision)
	pushFinalPeer(t, n, Snapshot{Versions: []Version{v}, RevisionEdges: []RevisionEdge{e}})
	if code, b := api(t, n, "/api/overview", "GET", nil, pushRotatedSecret); code != 200 {
		t.Fatal("peer push configuration revoked local management authority", code, string(b))
	}
	body := &pushFinalReader{source: strings.NewReader(`{}`)}
	if w := pushFinalRequest(n, pushRotatedSecret, body); w.Code != 404 || body.reads != 0 {
		t.Fatal("peer credential collision authorized public ingress", w.Code)
	}
}

func TestPushFinalReviewFinalReauthorization(t *testing.T) {
	for _, change := range []string{"rotate", "duplicate-unready", "conflict", "cluster", "account", "session", "get-policy"} {
		t.Run(change, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(3600000)
			n, _ := pushFixture(t, &clock, "a", nil)
			in := pushInput([]string{"a"})
			in.Push = &PushMonitorOptions{Token: pushFixtureSecret, AllowGET: true}
			m := add(t, n, in)
			before := n.sequence
			r := &pushFinalReader{source: strings.NewReader(`{}`), before: func() {
				switch change {
				case "rotate":
					pushEdit(t, n, m, func(in *MonitorInput) { in.Push = &PushMonitorOptions{Token: pushRotatedSecret} })
				case "duplicate-unready":
					other := m
					other.ID = "final-body-duplicate"
					v, _ := pushFinalVersion(other, "final-body-duplicate-root", "")
					pushFinalApply(t, n, Snapshot{Versions: []Version{v}})
				case "conflict":
					v1, e1 := pushFinalVersion(m, "final-body-left", "", m.Revision)
					v2, e2 := pushFinalVersion(m, "final-body-right", pushRotatedSecret, m.Revision)
					pushFinalApply(t, n, Snapshot{Versions: []Version{v1, v2}, RevisionEdges: []RevisionEdge{e1, e2}})
				case "get-policy":
					pushEdit(t, n, m, func(in *MonitorInput) { in.Push = &PushMonitorOptions{Token: pushFixtureSecret} })
				default:
					// Private peer configuration can overlap independently valid
					// local credentials; final ingress must still exclude them.
					n.mu.Lock()
					switch change {
					case "cluster":
						n.clusterHash = digest(pushFixtureSecret)
					case "account":
						n.accounts["final-local"] = account{Hash: digest(pushFixtureSecret)}
					case "session":
						n.sessions[digest(pushFixtureSecret)] = loginSession{}
					}
					n.mu.Unlock()
				}
			}}
			var w *httptest.ResponseRecorder
			if change == "get-policy" {
				r.source = strings.NewReader("")
				req := httptest.NewRequest("GET", "/api/push/"+pushFixtureSecret, r)
				w = httptest.NewRecorder()
				n.ServeHTTP(w, req)
			} else {
				w = pushFinalRequest(n, pushFixtureSecret, r)
			}
			if w.Code != 404 || r.reads == 0 || len(n.pushEvents) != 0 || len(n.pushAdmission) != 0 || n.sequence != before {
				t.Fatalf("%s during body kept obsolete authority: HTTP=%d reads=%d seq=%d", change, w.Code, r.reads, n.sequence)
			}
		})
	}
}

func TestPushFinalReviewBoundedUnknownPathWithFullLocalAccounts(t *testing.T) {
	const unknown = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
	var costs []float64
	for _, count := range []int{31, 16385} {
		// The earlier builder validates all headers/edges on bounded real
		// wire pages, persists privately, then calls New. Reuse its data
		// construction; request and account-pressure assertions are independent.
		n := pushIngressRepairFanout(t, count)
		request := func() {
			r := &pushFinalReader{source: strings.NewReader(strings.Repeat("x", 8192))}
			w := pushFinalRequest(n, unknown, r)
			if w.Code != 404 || r.reads != 0 {
				panic("unknown request touched body or was authorized")
			}
		}
		request()
		// Measure the actual ingress lookup separately from net/http request
		// construction: race-runtime GC clears standard-library pools, causing
		// 42/43/44 allocations even on the identical rejection path. The lookup
		// itself must allocate nothing, independent of accounts or head count.
		hash := digest(unknown)
		lookup := func() {
			n.mu.Lock()
			m := n.pushIngressMonitor(unknown, hash, false)
			n.mu.Unlock()
			if m != nil {
				panic("unknown ingress lookup was authorized")
			}
		}
		empty := testing.AllocsPerRun(100, lookup)
		n.mu.Lock()
		for i := 0; len(n.accounts) < 64; i++ {
			n.accounts[fmt.Sprintf("final-account-%d", i)] = account{Hash: digest(fmt.Sprintf("unrelated-account-%d", i))}
		}
		n.mu.Unlock()
		request()
		full := testing.AllocsPerRun(100, lookup)
		if empty != 0 || full != 0 {
			t.Fatalf("unknown credential scanned accounts: before=%.0f after=%.0f", empty, full)
		}
		costs = append(costs, full)
		t.Logf("heads=%d accounts=%d allocations/ingress-lookup=%.0f", count+1, len(n.accounts), full)
	}
	if costs[0] != costs[1] {
		t.Fatalf("retained-head work leaked into unknown path: %v", costs)
	}
}
