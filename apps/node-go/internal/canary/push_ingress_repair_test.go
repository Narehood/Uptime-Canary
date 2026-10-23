package canary

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type pushIngressRepairReader struct {
	body   *strings.Reader
	before func()
	once   sync.Once
	reads  int
}

func (r *pushIngressRepairReader) Read(p []byte) (int, error) {
	r.reads++
	r.once.Do(func() {
		if r.before != nil {
			r.before()
		}
	})
	return r.body.Read(p)
}

func pushIngressRepairRequest(n *Node, token string, body *pushIngressRepairReader) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/push", body)
	r.Header.Set("Authorization", "Push "+token)
	w := httptest.NewRecorder()
	n.ServeHTTP(w, r)
	return w
}

func pushIngressRepairApply(t *testing.T, n *Node, page Snapshot) {
	t.Helper()
	n.mu.Lock()
	err := n.applySnapshot(page)
	n.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

// Seed only private retained snapshot state so this test measures public
// ingress, not the unrelated sibling-union merge cost. Every candidate and edge
// passes the real bounded wire validator, and New rebuilds the derived index.
func pushIngressRepairFanout(t *testing.T, count int) *Node {
	t.Helper()
	var clock atomic.Int64
	clock.Store(2100000)
	n, options := pushFixture(t, &clock, "a", []Peer{{ID: "b", URL: "http://127.0.0.1:1"}})
	root := add(t, n, pushInput([]string{"a"}))
	otherInput := pushInput([]string{"a"})
	otherInput.Push = &PushMonitorOptions{Token: pushRotatedSecret}
	other := add(t, n, otherInput)
	versions := make([]Version, 0, count)
	edges := make([]RevisionEdge, 0, count)
	for i := range count {
		m := root
		m.Revision = fmt.Sprintf("ingress-repair-fanout-%06d", i)
		m.CheckGeneration = checkGeneration(root)
		if i == count-1 {
			m.Push = &PushMonitorOptions{Token: pushRotatedSecret}
			m.CheckGeneration = ""
		}
		e := newRevisionEdge("monitor", m.ID, m.Revision, m, []string{root.Revision}, checkGeneration(m))
		versions = append(versions, Version{Monitor: m, Ancestors: e.Parents, EdgeID: e.ID})
		edges = append(edges, e)
	}
	for start := 0; start < count; start += pageRecords {
		end := min(start+pageRecords, count)
		page := Snapshot{Protocol: peerProtocol, Node: Identity{"b", "b"}, Versions: versions[start:end], GroupVersions: []GroupVersion{}, RevisionEdges: edges[start:end], Observations: []Sample{}, Incidents: []Incident{}, Receipts: []Receipt{}}
		encoded, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := n.validateSnapshot(encoded, "b"); err != nil {
			t.Fatal("invalid ingress fanout fixture", err)
		}
	}
	tx, err := n.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, edge := range edges[:len(edges)-1] {
		if err := persistRevisionEdge(tx, edge); err != nil {
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
		t.Fatal(err)
	}
	pushAttach(t, reopened)
	if len(reopened.heads) != 2 || len(reopened.heads[root.ID]) != count || reopened.current(other.ID) == nil || !reopened.monitorVersionReady(versions[0]) || reopened.monitorVersionReady(versions[count-1]) {
		t.Fatal("retained fanout/proof fixture changed")
	}
	for _, secret := range []string{pushFixtureSecret, pushRotatedSecret} {
		if id, present := reopened.pushTokenIndex[digest(secret)]; !present || id != "" {
			t.Fatal("late/conflicted token was not indexed as ambiguous")
		}
		if _, present := reopened.pushTokenIndex[secret]; present {
			t.Fatal("index retained plaintext token key")
		}
	}
	return reopened
}

func TestPushIngressRepairUnknownWorkIndependentOfRetainedFanout(t *testing.T) {
	const unknown = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	allocations := []float64{}
	for _, count := range []int{31, 16385} {
		n := pushIngressRepairFanout(t, count)
		request := func(token string) (int, string, int) {
			body := &pushIngressRepairReader{body: strings.NewReader(`{}`)}
			w := pushIngressRepairRequest(n, token, body)
			return w.Code, w.Body.String(), body.reads
		}
		unknownCode, unknownBody, reads := request(unknown)
		ambiguousCode, ambiguousBody, ambiguousReads := request(pushRotatedSecret)
		if unknownCode != 404 || ambiguousCode != 404 || unknownBody != ambiguousBody || reads != 0 || ambiguousReads != 0 {
			t.Fatal("unknown/late unready duplicate did not fail before body read")
		}
		allocs := testing.AllocsPerRun(20, func() {
			code, _, reads := request(unknown)
			if code != 404 || reads != 0 {
				panic("unknown token authorized or read body")
			}
		})
		allocations = append(allocations, allocs)
		t.Logf("candidate-heads=%d monitor-IDs=%d unknown-allocations/request=%.0f index-entries=%d", count+1, len(n.heads), allocs, len(n.pushTokenIndex))
		if len(n.pushAdmission) != 0 || len(n.pushEvents) != 0 {
			t.Fatal("rejected ingress created state")
		}
	}
	// Allocation count is deterministic work evidence, not a timing or retained
	// history ceiling. Small fixed noise is allowed; per-head allocation is not.
	if allocations[1] > allocations[0]+8 {
		t.Fatalf("unknown lookup work grew with retained heads: small=%.0f large=%.0f", allocations[0], allocations[1])
	}
}

func TestPushIngressRepairDisabledConflictedUnreadyAndTombstones(t *testing.T) {
	for _, kind := range []string{"disabled", "unready", "conflicted", "live-with-tombstone"} {
		t.Run(kind, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(2200000)
			n, _ := pushFixture(t, &clock, "a", nil)
			ready := add(t, n, pushInput([]string{"a"}))
			other := ready
			other.ID = "ingress-reserved"
			other.Revision = "ingress-reserved-root"
			other.CheckGeneration = ""
			if kind == "disabled" {
				other.Enabled = false
			}
			edge := newRevisionEdge("monitor", other.ID, other.Revision, other, []string{}, other.Revision)
			page := Snapshot{Versions: []Version{{Monitor: other, Ancestors: edge.Parents, EdgeID: edge.ID}}}
			if kind != "unready" {
				page.RevisionEdges = []RevisionEdge{edge}
			}
			if kind == "conflicted" || kind == "live-with-tombstone" {
				sibling := other
				sibling.Revision = "ingress-reserved-sibling"
				sibling.Push = &PushMonitorOptions{Token: pushRotatedSecret}
				if kind == "live-with-tombstone" {
					sibling.Deleted = true
				}
				siblingEdge := newRevisionEdge("monitor", sibling.ID, sibling.Revision, sibling, []string{}, sibling.Revision)
				page.Versions = append(page.Versions, Version{Monitor: sibling, Ancestors: siblingEdge.Parents, EdgeID: siblingEdge.ID})
				page.RevisionEdges = append(page.RevisionEdges, siblingEdge)
			}
			pushIngressRepairApply(t, n, page)
			if id, exists := n.pushTokenIndex[digest(pushFixtureSecret)]; !exists || id != "" {
				t.Fatal("passive candidate failed to reserve duplicate secret")
			}
			if status, _ := pushCall(t, n, "POST", "/api/push", "Push "+pushFixtureSecret, `{}`); status != 404 {
				t.Fatal("passive duplicate selected ready candidate", status)
			}
			if kind == "conflicted" {
				if id, exists := n.pushTokenIndex[digest(pushRotatedSecret)]; !exists || id != "" {
					t.Fatal("single matching candidate in conflicted entity was authorized")
				}
				if status, _ := pushCall(t, n, "POST", "/api/push", "Push "+pushRotatedSecret, `{}`); status != 404 {
					t.Fatal("conflicted token selected one candidate")
				}
			}
			if kind == "unready" {
				if n.current(other.ID) != nil {
					t.Fatal("missing proof became ready")
				}
				pushIngressRepairApply(t, n, Snapshot{RevisionEdges: []RevisionEdge{edge}})
			}
			n.mu.Lock()
			_, err := n.writeMonitor(other.MonitorInput, n.heads[other.ID], other.ID, true)
			n.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			if id := n.pushTokenIndex[digest(pushFixtureSecret)]; id != ready.ID {
				t.Fatal("tombstone did not release duplicate token")
			}
			if status, _ := pushCall(t, n, "POST", "/api/push", "Push "+pushFixtureSecret, `{}`); status != 200 {
				t.Fatal("unique ready binding not restored", status)
			}
		})
	}
}

func TestPushIngressRepairProofSQLRollbackRestartAndRotation(t *testing.T) {
	var clock atomic.Int64
	clock.Store(2300000)
	a, _ := pushFixture(t, &clock, "a", []Peer{{ID: "b", URL: "http://127.0.0.1:1"}})
	b, options := pushFixture(t, &clock, "b", []Peer{{ID: "a", URL: "http://127.0.0.1:1"}})
	m := add(t, a, pushInput([]string{"b"}))
	pages := causalCapacityPages(t, a)
	if len(pages) != 1 {
		t.Fatal("fixture page count")
	}
	page := pages[0]
	partial := page
	partial.RevisionEdges = nil
	before := maps.Clone(b.pushTokenIndex)
	if _, err := b.store.db.Exec(`CREATE TRIGGER push_ingress_reject_heads BEFORE INSERT ON records WHEN NEW.category='heads' BEGIN SELECT RAISE(FAIL,'private fixture'); END`); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	err := b.applySnapshot(partial)
	b.mu.Unlock()
	if err == nil || !reflect.DeepEqual(before, b.pushTokenIndex) || b.current(m.ID) != nil {
		t.Fatal("failed SQL published derived token binding")
	}
	if _, err := b.store.db.Exec(`DROP TRIGGER push_ingress_reject_heads`); err != nil {
		t.Fatal(err)
	}
	pushIngressRepairApply(t, b, partial)
	if b.pushTokenIndex[digest(pushFixtureSecret)] != m.ID || b.current(m.ID) != nil {
		t.Fatal("unready unique candidate did not reserve token")
	}
	if status, _ := pushCall(t, b, "POST", "/api/push", "Push "+pushFixtureSecret, `{}`); status != 404 {
		t.Fatal("unready token authorized")
	}
	if status, _ := api(t, b, "/api/accounts", "POST", map[string]string{"name": "reserved-unready", "role": "editor", "token": pushFixtureSecret}, adminToken); status < 400 {
		t.Fatal("unready push token promoted to management")
	}
	pushIngressRepairApply(t, b, Snapshot{RevisionEdges: page.RevisionEdges})
	if status, _ := pushCall(t, b, "POST", "/api/push", "Push "+pushFixtureSecret, `{}`); status != 200 {
		t.Fatal("edge-only completion did not authorize ready candidate", status)
	}
	if err := b.Stop(); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	pushAttach(t, restarted)
	if restarted.pushTokenIndex[digest(pushFixtureSecret)] != m.ID {
		t.Fatal("restart lost derived token binding")
	}
	clock.Add(100)
	old := restarted.current(m.ID)
	rotated := old.MonitorInput
	rotated.Push = &PushMonitorOptions{Token: pushRotatedSecret}
	before = maps.Clone(restarted.pushTokenIndex)
	if _, err := restarted.store.db.Exec(`CREATE TRIGGER push_ingress_reject_rotation BEFORE INSERT ON records WHEN NEW.category='heads' BEGIN SELECT RAISE(FAIL,'private fixture'); END`); err != nil {
		t.Fatal(err)
	}
	restarted.mu.Lock()
	_, err = restarted.writeMonitor(rotated, restarted.heads[m.ID], m.ID, false)
	restarted.mu.Unlock()
	if err == nil || !reflect.DeepEqual(before, restarted.pushTokenIndex) || restarted.current(m.ID).Push.Token != pushFixtureSecret {
		t.Fatal("failed rotation published new index or credential")
	}
	if status, _ := pushCall(t, restarted, "POST", "/api/push", "Push "+pushFixtureSecret, `{}`); status != 200 {
		t.Fatal("failed rotation revoked valid token", status)
	}
	if _, err := restarted.store.db.Exec(`DROP TRIGGER push_ingress_reject_rotation`); err != nil {
		t.Fatal(err)
	}
	clock.Add(100)
	next := pushEdit(t, restarted, *restarted.current(m.ID), func(in *MonitorInput) { in.Push = &PushMonitorOptions{Token: pushRotatedSecret} })
	if _, exists := restarted.pushTokenIndex[digest(pushFixtureSecret)]; exists || restarted.pushTokenIndex[digest(pushRotatedSecret)] != next.ID {
		t.Fatal("committed rotation left stale token index")
	}
	if status, _ := pushCall(t, restarted, "POST", "/api/push", "Push "+pushFixtureSecret, `{}`); status != 404 {
		t.Fatal("old token survived successful rotation")
	}
	if status, _ := pushCall(t, restarted, "POST", "/api/push", "Push "+pushRotatedSecret, `{}`); status != 200 {
		t.Fatal("new token not authorized", status)
	}
	var persistedIndexes int
	if err := restarted.store.db.QueryRow(`SELECT COUNT(*) FROM records WHERE category='pushTokenIndex'`).Scan(&persistedIndexes); err != nil || persistedIndexes != 0 {
		t.Fatal("derived index leaked into persisted authority")
	}
}

func TestPushIngressRepairRevalidatesAfterBodyRead(t *testing.T) {
	for _, change := range []string{"rotation", "disable", "delete"} {
		t.Run(change, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(2400000)
			n, _ := pushFixture(t, &clock, "a", nil)
			m := add(t, n, pushInput([]string{"a"}))
			body := &pushIngressRepairReader{body: strings.NewReader(`{}`), before: func() {
				if change == "delete" {
					n.mu.Lock()
					_, err := n.writeMonitor(m.MonitorInput, n.heads[m.ID], m.ID, true)
					n.mu.Unlock()
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				_ = pushEdit(t, n, m, func(in *MonitorInput) {
					if change == "rotation" {
						in.Push = &PushMonitorOptions{Token: pushRotatedSecret}
					} else {
						in.Enabled = false
					}
				})
			}}
			w := pushIngressRepairRequest(n, pushFixtureSecret, body)
			if w.Code != 404 || body.reads == 0 || len(n.pushEvents) != 0 || len(n.pushAdmission) != 0 {
				t.Fatal("body-time configuration change retained earlier authorization", w.Code)
			}
		})
	}
}

func TestPushIngressRepairCurrentCredentialAndLocalExclusions(t *testing.T) {
	for _, kind := range []string{"stale-index", "cluster", "account", "session"} {
		t.Run(kind, func(t *testing.T) {
			var clock atomic.Int64
			clock.Store(2500000)
			n, _ := pushFixture(t, &clock, "a", nil)
			m := add(t, n, pushInput([]string{"a"}))
			token := pushFixtureSecret
			switch kind {
			case "stale-index":
				token = pushRotatedSecret
				n.pushTokenIndex[digest(token)] = m.ID
			case "cluster":
				n.clusterHash = digest(token)
			case "account":
				n.accounts["defensive-fixture"] = account{Hash: digest(token)}
			case "session":
				n.sessions[digest(token)] = loginSession{}
			}
			body := &pushIngressRepairReader{body: strings.NewReader(`{}`)}
			w := pushIngressRepairRequest(n, token, body)
			if w.Code != 404 || body.reads != 0 || len(n.pushEvents) != 0 {
				t.Fatal("index bypassed current-secret/local-credential defense", w.Code)
			}
		})
	}
}
