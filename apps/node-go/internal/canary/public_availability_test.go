package canary

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func publicAvailabilityFixture(t *testing.T, disk bool, opted bool) (*Node, *atomic.Int64, Monitor, StatusPage) {
	t.Helper()
	clock := new(atomic.Int64)
	clock.Store(maintenanceClock(12, 1))
	path := ":memory:"
	if disk {
		path = filepath.Join(t.TempDir(), "public-history.db")
	}
	n := statusTestNode(t, clock, path)
	in := input([]string{"a", "b"}, "http://private-history.invalid/path?secret=credential", "")
	in.Name = "Private internal monitor"
	m, err := n.writeMonitor(in, nil, "selected-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	page := statusFixture("operations")
	page.Groups[0].Monitors[0].ShowAvailability = opted
	return n, clock, m, statusCreate(t, n, page, adminToken)
}

func publicAvailabilityRead(t *testing.T, n *Node, slug string) *PublicObservedAvailability {
	t.Helper()
	page, w := statusPublic(t, n, slug)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	return page.Groups[0].Monitors[0].Availability
}

func TestPublicAvailabilityExplicitOptInStrictDecodeAndNoSQLByDefault(t *testing.T) {
	n, _, _, page := publicAvailabilityFixture(t, false, false)
	base, _ := json.Marshal(page.StatusPageInput)
	if strings.Contains(string(base), "showAvailability") {
		t.Fatal("old input acquired new immutable payload bytes")
	}
	for _, value := range []string{"null", `"true"`, "1", `true,"showAvailability":false`, `true,"ShowAvailability":true`} {
		raw := strings.Replace(string(base), `"showCertificate":false`, `"showCertificate":false,"showAvailability":`+value, 1)
		if _, err := parseStatusPage([]byte(raw)); err == nil {
			t.Fatal("ambiguous publication opt-in accepted", value)
		}
	}
	if err := n.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	if m := publicAvailabilityRead(t, n, page.Slug); m != nil {
		t.Fatal("history implied by current status")
	}
	if len(n.publicAvailability.entries) != 0 {
		t.Fatal("opt-out touched archive cache")
	}
}

func TestPublicAvailabilityMatchesPrivateArchiveAndRedactsIdentity(t *testing.T) {
	n, clock, m, page := publicAvailabilityFixture(t, false, true)
	to := clock.Load() / publicAvailabilityFrame * publicAvailabilityFrame
	from := to - publicAvailabilityWindow
	hour := int64(time.Hour / time.Millisecond)
	for i, v := range []struct {
		start, end  int64
		state       string
		maintenance bool
	}{
		{from, from + hour, "up", false}, {from + hour, from + 2*hour, "down", true},
		{from + 2*hour, from + 3*hour, "pending", true}, {from + 3*hour, from + 4*hour, "unknown", false},
		{from + 4*hour, from + 6*hour, "up", false},
	} {
		availabilityRecord(t, n, m, v.start, v.end, int64(i+1), v.state, v.maintenance)
	}
	private := availabilityRead(t, n, m.ID, from, to, hour)
	pub, w := statusPublic(t, n, page.Slug)
	metric := pub.Groups[0].Monitors[0].Availability
	if w.Code != 200 || metric == nil || metric.Status != "available" || metric.Scope != "serving-node" || metric.From != from || metric.To != to ||
		!equalJSON(metric.Summary, private.Summary) || !equalJSON(metric.Buckets, private.Buckets) {
		t.Fatal("public metric changed known time or coverage", w.Code, w.Body.String())
	}
	if metric.Summary.UpMs != 3*hour || metric.Summary.KnownMs != 4*hour || *metric.Summary.AvailabilityPercent != 75 || metric.Summary.MaintenanceMs != 2*hour || len(metric.Buckets) > 25 {
		t.Fatal("wrong independent denominators", metric)
	}
	for _, secret := range []string{m.ID, m.Name, m.Target, m.Revision, "private-history.invalid", "credential", "nodeId", "monitorId", "retainedFrom", "recordLimit"} {
		if secret != "" && strings.Contains(w.Body.String(), secret) {
			t.Fatal("public history disclosed private identity", secret)
		}
	}
	// Historical known time cannot turn the separate current-state projection up.
	if pub.Groups[0].Monitors[0].State != "unknown" {
		t.Fatal("history became current state")
	}
	if r := maintenanceAPIRequest(n, "HEAD", publicStatusPrefix+page.Slug, nil, ""); r.Code != 200 || r.Body.Len() != 0 || r.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("HEAD policy")
	}
}

func TestPublicAvailabilityMissingHistoryRestartAndPeerLocality(t *testing.T) {
	n, clock, m, page := publicAvailabilityFixture(t, true, true)
	empty := publicAvailabilityRead(t, n, page.Slug)
	if empty.Status != "available" || empty.Summary.KnownMs != 0 || empty.Summary.AvailabilityPercent != nil || empty.Summary.CoveragePercent != 0 {
		t.Fatal("empty archive invented coverage", empty)
	}
	to := clock.Load() / publicAvailabilityFrame * publicAvailabilityFrame
	availabilityRecord(t, n, m, to-1000, to, 1, "down", false)
	// Cached windows stay immutable for a frame; a later frame reads new history.
	clock.Add(publicAvailabilityFrame)
	path := strings.TrimSuffix(n.store.lock.Name(), ".lock")
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = statusTestNode(t, clock, path)
	metric := publicAvailabilityRead(t, n, page.Slug)
	if metric.Status != "available" || metric.Summary.DownMs != 1000 {
		t.Fatal("durable local coverage lost", metric)
	}
	opts := causalCapacityOptions("b", ":memory:")
	opts.Now = clock.Load
	other := cr2New(t, opts)
	for _, p := range causalCapacityPages(t, n) {
		raw, _ := json.Marshal(p)
		decoded, err := other.validateSnapshot(raw, "a")
		if err != nil {
			t.Fatal(err)
		}
		if err := other.applySnapshot(decoded); err != nil {
			t.Fatal(err)
		}
	}
	remote := publicAvailabilityRead(t, other, page.Slug)
	if remote.Status != "available" || remote.Summary.KnownMs != 0 {
		t.Fatal("peer snapshots imported another node's archive", remote)
	}
}

func TestPublicAvailabilityEligibilityAndFinalPublicationRecheck(t *testing.T) {
	for _, scenario := range []string{"disabled", "foreign assignment", "conflict", "missing edge", "opt out", "unpublish"} {
		t.Run(scenario, func(t *testing.T) {
			n, _, m, page := publicAvailabilityFixture(t, false, true)
			captured := n.publicStatusPage(page.Slug)
			switch scenario {
			case "disabled":
				m.Enabled = false
				if _, err := n.writeMonitor(m.MonitorInput, n.heads[m.ID], m.ID, false); err != nil {
					t.Fatal(err)
				}
			case "foreign assignment":
				m.NodeIDs = []string{"b"}
				if _, err := n.writeMonitor(m.MonitorInput, n.heads[m.ID], m.ID, false); err != nil {
					t.Fatal(err)
				}
			case "conflict":
				n.heads[m.ID] = append(n.heads[m.ID], n.heads[m.ID][0])
			case "missing edge":
				n.heads[m.ID][0].EdgeID = strings.Repeat("f", 64)
			case "opt out":
				page.Groups[0].Monitors[0].ShowAvailability = false
			case "unpublish":
				page.Enabled = false
			}
			if scenario == "opt out" || scenario == "unpublish" {
				if w := maintenanceAPIRequest(n, "PUT", "/api/status-pages/"+page.ID, statusEdit(page.StatusPageInput, page.Revision, nil), adminToken); w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
				if n.fillPublicAvailability(context.Background(), captured) {
					t.Fatal("withdrawn publication survived in-flight read")
				}
				return
			}
			if !n.fillPublicAvailability(context.Background(), captured) {
				t.Fatal("monitor eligibility should preserve page")
			}
			if v := captured.Groups[0].Monitors[0].Availability; v == nil || v.Status != "unavailable" {
				t.Fatal("stale monitor proof authorized history", v)
			}
		})
	}
}

func TestPublicAvailabilityRowBudgetIsExactAndNeverPartial(t *testing.T) {
	for _, count := range []int{publicAvailabilityRows, publicAvailabilityRows + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			n := availabilityFixture(t, ":memory:", 100000)
			availabilityWrite(t, n, func(e dbExec) error {
				for i := range count {
					if _, err := e.Exec(`INSERT INTO availability_spans VALUES(?,?,?,?,?)`, "monitor", i*2+1, i*2+2, "up", false); err != nil {
						return err
					}
				}
				return nil
			})
			tx, err := n.store.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			left := publicAvailabilityRows
			metric := readPublicAvailability(context.Background(), tx, "monitor", 0, 100000, &left)
			if count == publicAvailabilityRows {
				if metric.Status != "available" || metric.Summary.KnownMs != int64(count) || left != 0 {
					t.Fatal("exact budget rejected", metric, left)
				}
				if next := readPublicAvailability(context.Background(), tx, "another-monitor", 0, 100000, &left); next.Status != "unavailable" {
					t.Fatal("second service reset request row budget")
				}
			} else if metric.Status != "unavailable" || metric.Summary != nil || metric.Buckets != nil {
				t.Fatal("partial prefix became coverage", metric)
			}
		})
	}
}

func TestPublicAvailabilityBusyDatabaseAndCacheRemainBounded(t *testing.T) {
	n, clock, _, page := publicAvailabilityFixture(t, false, true)
	// No waiting for another public reader; the result says unavailable.
	n.publicAvailability.reader.Lock()
	v := publicAvailabilityRead(t, n, page.Slug)
	n.publicAvailability.reader.Unlock()
	if v.Status != "unavailable" {
		t.Fatal("contended read invented metric")
	}
	// A private transaction occupies the single SQL connection. The public
	// deadline bounds the wait without holding the node mutex or writing SQL.
	tx, err := n.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	v = publicAvailabilityRead(t, n, page.Slug)
	if time.Since(started) > time.Second || v.Status != "unavailable" {
		t.Fatal("public archive wait was unbounded", v)
	}
	_ = tx.Rollback()
	clock.Add(publicAvailabilityFrame)
	if v = publicAvailabilityRead(t, n, page.Slug); v.Status != "available" {
		t.Fatal("archive capacity did not recover", v)
	}
	captured := n.publicStatusPage(page.Slug)
	clock.Add(publicAvailabilityFrame)
	if !n.fillPublicAvailability(context.Background(), captured) || captured.Groups[0].Monitors[0].Availability.Status != "unavailable" {
		t.Fatal("cross-frame read returned a stale measurement window")
	}
	for i := range publicAvailabilityEntries * 3 {
		n.publicAvailability.remember(publicAvailabilityKey{id: fmt.Sprint(i), to: int64(i)}, unavailablePublicAvailability())
	}
	if len(n.publicAvailability.entries) > publicAvailabilityEntries {
		t.Fatal("public cache grows with retained history")
	}
}

func TestPublicAvailabilityHotCacheSurvivesUnrelatedSQLRead(t *testing.T) {
	n, clock, m, page := publicAvailabilityFixture(t, false, true)
	to := clock.Load() / publicAvailabilityFrame * publicAvailabilityFrame
	availabilityRecord(t, n, m, to-1000, to, 1, "up", false)
	hot := publicAvailabilityRead(t, n, page.Slug)
	if hot.Status != "available" || hot.Summary.KnownMs != 1000 {
		t.Fatal("hot-cache fixture was not populated", hot)
	}
	coldMonitor, err := n.writeMonitor(input([]string{"a", "b"}, "http://private-cold.invalid/", ""), nil, "cold-monitor", false)
	if err != nil {
		t.Fatal(err)
	}
	coldPage := statusFixture("cold-history")
	coldPage.Groups[0].Monitors[0].MonitorID = coldMonitor.ID
	coldPage.Groups[0].Monitors[0].ShowAvailability = true
	statusCreate(t, n, coldPage, adminToken)

	// Occupy the only SQL connection so a genuine unrelated public cache miss
	// holds the reader gate. The cache-hit assertion does not depend on which
	// mutex field implements that gate.
	tx, err := n.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	before := n.store.db.Stats().WaitCount
	done := make(chan struct{})
	go func() {
		defer close(done)
		n.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", publicStatusPrefix+coldPage.Slug, nil))
	}()
	defer func() { _ = tx.Rollback(); <-done }()
	deadline := time.Now().Add(2 * time.Second)
	for n.store.db.Stats().WaitCount == before {
		if time.Now().After(deadline) {
			t.Fatal("cold-read fixture never reached SQL")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("cold-read fixture finished before the concurrent cache lookup")
	default:
	}
	if got := publicAvailabilityRead(t, n, page.Slug); !equalJSON(got, hot) {
		t.Fatal("an unrelated SQL read hid an already cached metric", got)
	}
}

func TestPublicAvailabilityBadgesRequireExplicitUniqueSelection(t *testing.T) {
	n, clock, m, page := publicAvailabilityFixture(t, false, true)
	base := publicBadgePrefix + page.Slug + ".svg"
	q := url.Values{"group": {"public-services"}, "monitor": {"Public service"}, "metric": {"uptime"}}
	for _, path := range []string{base + "?metric=uptime", base + "?" + q.Encode() + "&metric=uptime", base + "?" + q.Encode() + "&from=0", base + "?" + strings.Replace(q.Encode(), "uptime", "availability", 1)} {
		if w := maintenanceAPIRequest(n, "GET", path, nil, ""); w.Code != 404 {
			t.Fatal("invalid metric selection", path, w.Code)
		}
	}
	to := clock.Load() / publicAvailabilityFrame * publicAvailabilityFrame
	availabilityRecord(t, n, m, to-10000, to, 1, "up", false)
	path := base + "?" + q.Encode()
	w := maintenanceAPIRequest(n, "GET", path, nil, "")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "image/svg+xml") || !strings.Contains(w.Body.String(), "coverage") || !strings.Contains(w.Body.String(), "100.00%") || strings.Contains(w.Body.String(), m.ID) {
		t.Fatal(w.Code, w.Body.String())
	}
	if h := maintenanceAPIRequest(n, "HEAD", path, nil, ""); h.Code != 200 || h.Body.Len() != 0 {
		t.Fatal("metric HEAD")
	}
	page.Groups[0].Monitors = append(page.Groups[0].Monitors, StatusPageMonitor{MonitorID: "another-monitor", Name: "Public service", ShowAvailability: true})
	if w := maintenanceAPIRequest(n, "PUT", "/api/status-pages/"+page.ID, statusEdit(page.StatusPageInput, page.Revision, nil), adminToken); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := maintenanceAPIRequest(n, "GET", path, nil, ""); w.Code != 404 {
		t.Fatal("ambiguous metric selected first match")
	}
	// Invalid methods never read history or turn into an authenticated route.
	r := httptest.NewRequest("POST", path, strings.NewReader("private input"))
	r.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	n.ServeHTTP(rec, r)
	if rec.Code != 404 || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("metric route accepted mutation")
	}
}

func TestPublicAvailabilityPublicationRollbackCannotGrantHistory(t *testing.T) {
	n, _, _, page := publicAvailabilityFixture(t, false, false)
	sequence := n.sequence
	if _, err := n.store.db.Exec(`CREATE TRIGGER refuse_publication BEFORE INSERT ON records
 WHEN NEW.category='resourceHeads' BEGIN SELECT RAISE(FAIL,'fixture rollback'); END`); err != nil {
		t.Fatal(err)
	}
	page.Groups[0].Monitors[0].ShowAvailability = true
	write := func() *httptest.ResponseRecorder {
		return maintenanceAPIRequest(n, "PUT", "/api/status-pages/"+page.ID, statusEdit(page.StatusPageInput, page.Revision, nil), adminToken)
	}
	if w := write(); w.Code < 400 {
		t.Fatal("injected transaction failure ignored", w.Code)
	}
	if n.sequence != sequence || publicAvailabilityRead(t, n, page.Slug) != nil || len(n.publicAvailability.entries) != 0 {
		t.Fatal("rolled-back opt-in became authority")
	}
	if _, err := n.store.db.Exec(`DROP TRIGGER refuse_publication`); err != nil {
		t.Fatal(err)
	}
	if w := write(); w.Code != 200 {
		t.Fatal("publication retry failed", w.Code, w.Body.String())
	}
	if v := publicAvailabilityRead(t, n, page.Slug); v == nil || v.Status != "available" {
		t.Fatal("committed opt-in did not take effect", v)
	}
}

func TestPublicAvailabilityBadgeTruncationAndInvalidTotals(t *testing.T) {
	day := publicAvailabilityWindow
	for _, v := range []struct {
		up, known, window int64
		available         bool
		word              string
	}{
		{day, day, day, true, "100.00% up"}, {day - 1, day, day, true, "99.99% up"},
		{0, 0, day, true, "unknown, 0.00% coverage"}, {0, day, day, true, "0.00% up"},
		{0, 0, 0, true, "unavailable"}, {-1, day, day, true, "unavailable"},
		{2, 1, day, true, "unavailable"}, {day, day + 1, day, true, "unavailable"},
		{day, day, day, false, "unavailable"},
	} {
		svg := renderObservedBadge(v.up, v.known, v.window, v.available)
		if !strings.Contains(svg, "24h observed: "+v.word) || strings.Contains(svg, "<script") || strings.Contains(svg, "href=") {
			t.Fatal("unsafe or rounded badge", v, svg)
		}
		var document struct{ XMLName xml.Name }
		if xml.Unmarshal([]byte(svg), &document) != nil {
			t.Fatal("invalid metric SVG")
		}
	}
}
