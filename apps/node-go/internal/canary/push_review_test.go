package canary

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"uptime-canary/apps/node-go/internal/heartbeat"
)

// These independent review regressions assert the required behavior. A failure
// is retained as evidence against the captured candidate, not patched around.
func TestPushReviewAdmissionAcrossGenerationRestart(t *testing.T) {
	for _, change := range []string{"token", "interval", "allowGet"} {
		t.Run(change, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(1100000)
			n, options := pushFixture(t, &clock, "a", nil)
			m := add(t, n, pushInput([]string{"a"}))
			pushAccept(t, n, "up")
			acceptedAt := clock.Load()
			clock.Add(50)
			m = pushEdit(t, n, m, func(in *MonitorInput) {
				switch change {
				case "token":
					in.Push = &PushMonitorOptions{Token: pushRotatedSecret}
				case "interval":
					in.IntervalMs = 2000
				case "allowGet":
					in.Push = &PushMonitorOptions{Token: pushFixtureSecret, AllowGET: true}
				}
			})
			auth := "Push " + m.Push.Token
			before, _ := pushCall(t, n, "POST", "/api/push", auth, `{}`)
			if before != http.StatusTooManyRequests {
				t.Fatalf("live-node control: got %d, want 429", before)
			}
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			restarted, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			pushAttach(t, restarted)
			after, body := pushCall(t, restarted, "POST", "/api/push", auth, `{}`)
			persisted, err := loadAll[heartbeat.Event](restarted.store, "heartbeatEvents")
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("elapsed=%dms live=%d restart=%d retained-generation-events=%d body=%s", clock.Load()-acceptedAt, before, after, len(persisted), body)
			if after != http.StatusTooManyRequests {
				t.Errorf("restart bypassed per-monitor 100ms admission after %s edit: got %d", change, after)
			}
		})
	}
}

func TestPushReviewManagementTokenCannotReusePushSecret(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(1200000)
			n, _ := pushFixture(t, &clock, "a", nil)
			_ = add(t, n, pushInput([]string{"a"}))
			pushAccept(t, n, "up")
			bearerBefore, _ := api(t, n, "/api/overview", "GET", nil, pushFixtureSecret)
			if bearerBefore != http.StatusUnauthorized {
				t.Fatalf("push-only control unexpectedly authorized management: %d", bearerBefore)
			}
			path, method := "/api/accounts", "POST"
			if operation == "update" {
				code, body := api(t, n, path, method, map[string]string{"name": "review-editor", "role": "editor", "token": editorToken}, adminToken)
				if code != http.StatusCreated {
					t.Fatalf("create distinct account control: %d %s", code, body)
				}
				var account AccountView
				if err := json.Unmarshal(body, &account); err != nil {
					t.Fatal(err)
				}
				path, method = path+"/"+account.ID, "PUT"
			}
			code, body := api(t, n, path, method, map[string]string{"name": "review-editor", "role": "editor", "token": pushFixtureSecret}, adminToken)
			bearerAfter, _ := api(t, n, "/api/overview", "GET", nil, pushFixtureSecret)
			clock.Add(100)
			pushAfter, _ := pushCall(t, n, "POST", "/api/push", "Push "+pushFixtureSecret, `{}`)
			t.Logf("account-%s=%d bearer-before=%d bearer-after=%d push-after=%d body=%s", operation, code, bearerBefore, bearerAfter, pushAfter, body)
			if code < 400 || bearerAfter != http.StatusUnauthorized || pushAfter != http.StatusOK {
				t.Errorf("account %s accepted an existing push credential as a management bearer and disabled push ingress", operation)
			}
		})
	}
}

func TestPushReviewPeerPageRollbackAndReadiness(t *testing.T) {
	var clock atomic.Int64
	clock.Store(1300000)
	a, _ := pushFixture(t, &clock, "a", []Peer{{ID: "b", URL: "http://127.0.0.1:1"}})
	b, options := pushFixture(t, &clock, "b", []Peer{{ID: "a", URL: "http://127.0.0.1:1"}})
	m := add(t, a, pushInput([]string{"b"}))
	pushAccept(t, a, "up")
	pages := causalCapacityPages(t, a)
	if len(pages) != 1 || len(pages[0].Versions) != 1 || len(pages[0].Heartbeats) != 1 {
		t.Fatal("expected a single real config/event page")
	}
	page := pages[0]
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.store.db.Exec(`CREATE TRIGGER push_review_fail_config BEFORE INSERT ON records WHEN NEW.category='heads' BEGIN SELECT RAISE(FAIL,'private review fixture'); END`); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	err = b.applySnapshot(page)
	b.mu.Unlock()
	if err == nil || b.current(m.ID) != nil || len(b.pushEvents) != 0 {
		t.Fatal("failed peer page published config or heartbeat in memory", err)
	}
	for _, category := range []string{"heads", "heartbeatEvents", "revisionEdges"} {
		var count int
		if err := b.store.db.QueryRow(`SELECT COUNT(*) FROM records WHERE category=?`, category).Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed peer transaction retained %s: count=%d err=%v", category, count, err)
		}
	}
	unchanged, _ := json.Marshal(page)
	if !bytes.Equal(encoded, unchanged) {
		t.Fatal("failed apply mutated retryable peer page")
	}
	if _, err := b.store.db.Exec(`DROP TRIGGER push_review_fail_config`); err != nil {
		t.Fatal(err)
	}
	// Without its separately paged edge, a valid config and event are passive.
	partial := page
	partial.RevisionEdges = nil
	revisionJoinsApply(t, b, partial)
	if b.current(m.ID) != nil || len(b.pushEvents) != 1 {
		t.Fatal("incomplete config proof became active or lost passive heartbeat")
	}
	if status, body := pushCall(t, b, "POST", "/api/push", "Push "+pushFixtureSecret, `{}`); status != 404 || !strings.Contains(string(body), "Push endpoint unavailable") {
		t.Fatalf("unready config was not uniformly unavailable: %d %s", status, body)
	}
	revisionJoinsApply(t, b, Snapshot{RevisionEdges: page.RevisionEdges})
	partitionDrive(t, b)
	if b.current(m.ID) == nil || b.samples[m.ID+"/b"].State != "up" {
		t.Fatal("edge completion did not activate the correct heartbeat")
	}
	if err := b.Stop(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	pushAttach(t, restarted)
	if restarted.current(m.ID) == nil || len(restarted.pushEvents) != 1 {
		t.Fatal("committed peer config/event did not survive restart")
	}
}

func TestPushReviewDeadlineCrossingDuringCommit(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		upsideDown bool
		freshReads int64
	}{
		{"ordinary-before-sample", false, 3},
		{"inverted-before-sample", true, 3},
		{"inverted-after-deadline-calculation", true, 6},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(1400000)
			n, _ := pushFixture(t, &clock, "a", nil)
			in := pushInput([]string{"a"})
			in.UpsideDown = scenario.upsideDown
			in.RetryIntervalMs = Ptr(int64(86400000))
			m := add(t, n, in)
			pushAccept(t, n, "up")
			deadline := clock.Load() + m.IntervalMs
			// The controlled clock advances exactly one millisecond, either
			// after the final witness read or after post-commit deadline
			// calculation. The production allocator/SQL need no test hooks.
			var reads atomic.Int64
			n.now = func() int64 {
				if reads.Add(1) <= scenario.freshReads {
					return deadline - 1
				}
				return deadline
			}
			partitionDrive(t, n)
			first := n.samples[m.ID+"/a"]
			next := n.nextProbe[m.ID]
			current, _ := n.pushEvaluation(m)
			partitionDrive(t, n)
			second := n.samples[m.ID+"/a"]
			t.Logf("clock-reads=%d expiry=%d sample-at=%d raw=%s effective=%s current-raw=%s next=%d delay=%d second-sequence=%d first-sequence=%d", reads.Load(), deadline, first.CheckedAt, first.RawState, first.State, current.State, next, next-deadline, second.Sequence, first.Sequence)
			if first.CheckedAt >= deadline && first.RawState == "up" {
				t.Errorf("committed a fresh-UP sample timestamped at or after exact heartbeat expiry")
			}
			if second.RawState != "down" {
				t.Errorf("next scheduler tick kept expired raw UP; next reevaluation is deferred by %dms", next-deadline)
			}
		})
	}
}

func TestPushReviewDispatchGuardRejectsAlreadyExpiredWitness(t *testing.T) {
	var clock atomic.Int64
	clock.Store(1500000)
	n, _ := pushFixture(t, &clock, "a", nil)
	m := add(t, n, pushInput([]string{"a"}))
	pushAccept(t, n, "up")
	beforeSequence := n.sequence
	deadline := clock.Load() + m.IntervalMs
	var reads atomic.Int64
	n.now = func() int64 {
		if reads.Add(1) <= 2 {
			return deadline - 1
		}
		return deadline
	}
	partitionDrive(t, n)
	if _, exists := n.samples[m.ID+"/a"]; exists || n.sequence != beforeSequence || n.nextProbe[m.ID] != 0 || len(n.probePolicies) != 0 {
		t.Fatal("expiry before final guard changed sample, sequence, policy, or immediate rescheduling")
	}
	partitionDrive(t, n)
	if s := n.samples[m.ID+"/a"]; s.RawState != "down" || s.State != "down" {
		t.Fatal("fresh dispatch did not commit missed heartbeat", s)
	}
}

func TestPushReviewLocalSequenceWriteRollback(t *testing.T) {
	var clock atomic.Int64
	clock.Store(1600000)
	n, options := pushFixture(t, &clock, "a", nil)
	m := add(t, n, pushInput([]string{"a"}))
	beforeSequence := n.sequence
	n.nextProbe[m.ID] = maxSafe
	if _, err := n.store.db.Exec(`CREATE TRIGGER push_review_fail_sequence BEFORE INSERT ON records WHEN NEW.category='settings' AND NEW.id='sequence' BEGIN SELECT RAISE(FAIL,'private review sequence failure'); END`); err != nil {
		t.Fatal(err)
	}
	code, _ := pushCall(t, n, "POST", "/api/push", "Push "+pushFixtureSecret, `{}`)
	if code != 503 || len(n.pushEvents) != 0 || n.sequence != beforeSequence || len(n.pushAdmission) != 0 || n.nextProbe[m.ID] != maxSafe || n.Error() == nil {
		t.Fatal("failed settings write published heartbeat, admission, sequence, or schedule", code)
	}
	stored, err := loadAll[heartbeat.Event](n.store, "heartbeatEvents")
	if err != nil || len(stored) != 0 {
		t.Fatal("settings failure did not roll back already-written heartbeat", err)
	}
	if _, err := n.store.db.Exec(`DROP TRIGGER push_review_fail_sequence`); err != nil {
		t.Fatal(err)
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	pushAttach(t, restarted)
	pushAccept(t, restarted, "up")
	if restarted.sequence <= beforeSequence+1 {
		t.Fatal("failed SQL reservation was reused after restart")
	}
}

func TestPushReviewExpiredMaximaAndConcurrentExpiry(t *testing.T) {
	c := heartbeat.Config{MonitorID: "review-monitor", Generation: "review-generation", StartedAt: 1700000, IntervalMs: 1000, Known: map[string]bool{"a": true, "b": true}}
	makeEvent := func(events map[string]heartbeat.Event, origin string, sequence, now int64, state string) heartbeat.Event {
		t.Helper()
		e, err := heartbeat.NewEvent(c, events, origin, sequence, now, heartbeat.Signal{State: state})
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	merge := func(events ...heartbeat.Event) map[string]heartbeat.Event {
		t.Helper()
		register, err := heartbeat.Merge(nil, events, c.Known, 1701000)
		if err != nil {
			t.Fatal(err)
		}
		return register
	}
	upA := makeEvent(nil, "a", 1, 1700100, "up")
	// The observing node's clock is 100ms earlier, inside the accepted skew.
	upB := makeEvent(merge(upA), "b", 2, 1700000, "up")
	for _, register := range []map[string]heartbeat.Event{merge(upA, upB), merge(upB, upA)} {
		result, err := heartbeat.Evaluate(c, register, 1701000)
		if err != nil || result.State != "down" || result.Message != "Heartbeat missed" || result.Deadline != 0 {
			t.Fatal("expired causal maximum resurrected still-fresh dominated UP", result, err)
		}
	}
	downA := makeEvent(nil, "a", 1, 1700000, "down")
	concurrentB := makeEvent(nil, "b", 2, 1700500, "up")
	register := merge(downA, concurrentB)
	for _, check := range []struct {
		now      int64
		state    string
		deadline int64
	}{{1700999, "down", 1701000}, {1701000, "up", 1701500}, {1701500, "down", 0}} {
		result, err := heartbeat.Evaluate(c, register, check.now)
		if err != nil || result.State != check.state || result.Deadline != check.deadline {
			t.Fatalf("concurrent expiry at %d: %+v, err=%v", check.now, result, err)
		}
	}
}

// This records request-work scaling without inventing a timing threshold or a
// replication/head limit. Each fixture is persisted and reopened through New.
func TestPushReviewUnauthenticatedHeadFanoutWork(t *testing.T) {
	const unknownSecret = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	for _, count := range []int{64, 4096, 16385} {
		t.Run(fmt.Sprintf("heads-%d", count), func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(1800000)
			n, options := pushFixture(t, &clock, "a", []Peer{{ID: "b", URL: "http://127.0.0.1:1"}})
			root := add(t, n, pushInput([]string{"a"}))
			otherInput := pushInput([]string{"a"})
			otherInput.Push = &PushMonitorOptions{Token: pushRotatedSecret}
			other := add(t, n, otherInput)
			versions := make([]Version, 0, count)
			edges := make([]RevisionEdge, 0, count)
			for i := range count {
				m := root
				m.Revision = fmt.Sprintf("review-fanout-%06d", i)
				m.CheckGeneration = checkGeneration(root)
				if i == count-1 {
					m.Push = &PushMonitorOptions{Token: pushRotatedSecret}
					m.CheckGeneration = ""
				}
				e := newRevisionEdge("monitor", m.ID, m.Revision, m, []string{root.Revision}, checkGeneration(m))
				versions = append(versions, Version{Monitor: m, Ancestors: e.Parents, EdgeID: e.ID})
				edges = append(edges, e)
			}
			// Validate every header/edge through the actual peer wire boundary.
			for start := 0; start < count; start += pageRecords {
				end := min(start+pageRecords, count)
				page := Snapshot{Protocol: peerProtocol, Node: Identity{"b", "b"}, Versions: versions[start:end], GroupVersions: []GroupVersion{}, RevisionEdges: edges[start:end], Observations: []Sample{}, Incidents: []Incident{}, Receipts: []Receipt{}}
				encoded, err := json.Marshal(page)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := n.validateSnapshot(encoded, "b"); err != nil {
					t.Fatal("invalid fanout wire fixture", err)
				}
			}
			// Store a valid retained snapshot directly to avoid benchmarking the
			// unrelated quadratic sibling-head merge. The final edge remains a
			// separately paged, unavailable proof, as permitted by replication.
			tx, err := n.store.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			for _, e := range edges[:len(edges)-1] {
				if err := persistRevisionEdge(tx, e); err != nil {
					t.Fatal(err)
				}
			}
			if err := put(tx, "heads", root.ID, versions); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			reopened, err := New(options)
			if err != nil {
				t.Fatal("runtime rejected persisted fanout", err)
			}
			pushAttach(t, reopened)
			if len(reopened.heads) != 2 || len(reopened.heads[root.ID]) != count || !reopened.monitorVersionReady(versions[0]) || reopened.monitorVersionReady(versions[count-1]) || reopened.current(other.ID) == nil {
				t.Fatal("fanout/recovery/readiness fixture was not retained")
			}
			request := func(token string) (int, string) {
				r := httptest.NewRequest("POST", "/api/push", strings.NewReader(`{}`))
				r.Header.Set("Authorization", "Push "+token)
				w := httptest.NewRecorder()
				reopened.ServeHTTP(w, r)
				return w.Code, w.Body.String()
			}
			unknownCode, unknownBody := request(unknownSecret)
			ambiguousCode, ambiguousBody := request(pushRotatedSecret)
			if unknownCode != 404 || ambiguousCode != 404 || unknownBody != ambiguousBody {
				t.Fatal("unknown secret and late unready duplicate did not fail uniformly", unknownCode, ambiguousCode)
			}
			allocs := testing.AllocsPerRun(5, func() {
				if code, _ := request(unknownSecret); code != 404 {
					panic("unknown credential unexpectedly accepted")
				}
			})
			started := time.Now()
			for range 20 {
				request(unknownSecret)
			}
			t.Logf("retained-monitor-IDs=2 candidate-heads=%d unknown-secret-allocs/request=%.0f elapsed/request=%s unknown=%d late-unready-duplicate=%d", count+1, allocs, time.Since(started)/20, unknownCode, ambiguousCode)
			if len(reopened.pushAdmission) != 0 || len(reopened.pushEvents) != 0 {
				t.Fatal("invalid credentials created admission or heartbeat state")
			}
		})
	}
}
