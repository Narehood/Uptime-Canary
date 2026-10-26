package canary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uptime-canary/apps/node-go/internal/updatecheck"
)

type updateDoer func(*http.Request) (*http.Response, error)

func (f updateDoer) Do(r *http.Request) (*http.Response, error) { return f(r) }
func updateDigest(body []byte) string {
	value := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(value[:])
}
func updateResponse(status int, body []byte, headers http.Header) *http.Response {
	if headers == nil {
		headers = http.Header{}
	}
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(string(body))), ContentLength: int64(len(body))}
}

func updateRegistry(t *testing.T) (updateDoer, *atomic.Int32, *atomic.Bool) {
	t.Helper()
	calls, conditional := &atomic.Int32{}, &atomic.Bool{}
	config, _ := json.Marshal(map[string]any{"config": map[string]any{"Labels": map[string]string{"org.opencontainers.image.source": "https://github.com/Narehood/Uptime-Canary", "org.opencontainers.image.revision": strings.Repeat("b", 40), "org.opencontainers.image.created": "2025-02-02T00:00:00Z"}}})
	image, _ := json.Marshal(map[string]any{"schemaVersion": 2, "config": map[string]any{"size": len(config), "digest": updateDigest(config)}})
	return updateDoer(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		if r.URL.Host == "api.github.com" {
			return updateResponse(200, []byte(`{"status":"ahead"}`), nil), nil
		}
		if r.URL.Host != "ghcr.io" {
			t.Error("nonofficial update origin")
		}
		if r.URL.Path == "/token" {
			return updateResponse(200, []byte(`{"token":"anonymous-pull-only"}`), nil), nil
		}
		if strings.HasSuffix(r.URL.Path, "/manifests/latest") {
			if conditional.Load() {
				if r.Header.Get("If-None-Match") != `"known"` {
					t.Error("conditional metadata header lost")
				}
				return updateResponse(304, nil, nil), nil
			}
			return updateResponse(200, image, http.Header{"Etag": []string{`"known"`}}), nil
		}
		if strings.HasSuffix(r.URL.Path, "/blobs/"+updateDigest(config)) {
			return updateResponse(200, config, nil), nil
		}
		t.Error("unexpected image layer or metadata request")
		return nil, updatecheck.ErrUnavailable
	}), calls, conditional
}

func TestDashboardUpdateReadsAreCachedAndOnlyAdminsCanSchedule(t *testing.T) {
	d, owner, _ := dashboardRegistryFixture(t)
	fake, calls, _ := updateRegistry(t)
	u, err := newDashboardUpdates(d, true, fake)
	if err != nil {
		t.Fatal(err)
	}
	d.updates.client.Close()
	d.updates = u
	for range 20 {
		if dashboardHTTP(d, "GET", "/api/dashboard/updates", owner, "", nil).Code != 200 {
			t.Fatal("cached GET failed")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("GET performed external I/O")
	}
	if dashboardHTTP(d, "POST", "/api/dashboard/updates/check", owner, "", nil).Code != 202 {
		t.Fatal("admin check was not queued")
	}
	if dashboardHTTP(d, "POST", "/api/dashboard/updates/check", owner, "", nil).Code != 409 {
		t.Fatal("duplicate manual check queued")
	}
	if calls.Load() != 0 {
		t.Fatal("POST synchronously performed network I/O")
	}
	u.check(context.Background())
	if u.status().State != "ok" || calls.Load() != 6 {
		t.Fatal("bounded metadata check failed", u.status().State, calls.Load())
	}
	if dashboardHTTP(d, "POST", "/api/dashboard/updates/check", owner, "", nil).Code != 429 {
		t.Fatal("manual cooldown not enforced")
	}
	w := dashboardHTTP(d, "POST", "/api/dashboard/updates/check", "bad-token", "", nil)
	if w.Code != 401 {
		t.Fatal("unauthenticated update check admitted")
	}
	for _, role := range []string{"viewer", "editor"} {
		created := dashboardHTTP(d, "POST", "/api/accounts", owner, "", map[string]string{"name": role, "role": role, "password": dashboardPasswordFixture})
		if created.Code != 201 {
			t.Fatal("cannot create update-permission fixture")
		}
		token := dashboardPasswordLogin(t, d.accounts, role, dashboardPasswordFixture)
		if dashboardHTTP(d, "GET", "/api/dashboard/updates", token, "", nil).Code != 200 {
			t.Fatal("nonadministrator cannot read cached status")
		}
		if dashboardHTTP(d, "POST", "/api/dashboard/updates/check", token, "", nil).Code != 403 {
			t.Fatal("nonadministrator scheduled an external check")
		}
	}
	if dashboardHTTP(d, "GET", "/api/dashboard/updates?anything=1", owner, "", nil).Code != 400 {
		t.Fatal("unexpected query accepted")
	}
}

func TestDashboardUpdateCacheSurvivesRestartAndCoalescesImageReads(t *testing.T) {
	d, _, _ := dashboardRegistryFixture(t)
	fake, calls, conditional := updateRegistry(t)
	u, err := newDashboardUpdates(d, true, fake)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now()
	u.now = func() time.Time { return clock }
	d.registry[strings.Repeat("1", 32)] = registeredNodeRecord{RegisteredNode: RegisteredNode{ID: strings.Repeat("1", 32), Label: "A", NodeID: "a"}, build: &BinaryIdentity{ImageRevision: strings.Repeat("a", 40)}}
	u.check(context.Background())
	first := u.status()
	if first.State != "ok" || first.IntervalHours != 6 || first.NextCheckAt-first.CheckedAt != updateInterval.Milliseconds() || first.Images[1].Status != "update-available" {
		t.Fatal("scheduled comparison result incorrect")
	}
	if calls.Load() != 7 {
		t.Fatal("unique source comparison not bounded", calls.Load())
	}
	before := calls.Load()
	for range 100 {
		u.status()
	}
	if calls.Load() != before {
		t.Fatal("browser reads queried GitHub or GHCR")
	}
	conditional.Store(true)
	clock = clock.Add(updateInterval)
	u.check(context.Background())
	if calls.Load()-before != 4 {
		t.Fatal("unchanged manifests or ancestry were refetched", calls.Load()-before)
	}
	reopened, err := newDashboardUpdates(d, true, fake)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.client.Close()
	if reopened.cache.NextCheckAt != u.cache.NextCheckAt || reopened.cache.LastAttemptAt != u.cache.LastAttemptAt {
		t.Fatal("restart forgot the shared registry cooldown")
	}
}

func TestDashboardUpdateBuildTimesDetectSameSourceRebuildWithoutDowngrade(t *testing.T) {
	d, _, _ := dashboardRegistryFixture(t)
	u := d.updates
	u.enabled = true
	u.cache = updateCache{Web: updatecheck.Cache{Revision: strings.Repeat("b", 40), CreatedAt: "2025-02-02T00:00:00Z"}, Node: updatecheck.Cache{Revision: strings.Repeat("b", 40), CreatedAt: "2025-02-02T00:00:00Z"}, NodeAvailable: true, WebAvailable: true, CheckedAt: time.Now().UnixMilli(), LastAttemptAt: time.Now().UnixMilli(), Relations: map[string]string{}}
	id := strings.Repeat("1", 32)
	for _, test := range []struct{ builtAt, want string }{{"2025-02-01T00:00:00Z", "update-available"}, {"2025-02-02T00:00:00Z", "current"}, {"2025-02-03T00:00:00Z", "ahead"}, {"", "unknown"}, {"not-a-time", "unknown"}} {
		d.registry[id] = registeredNodeRecord{RegisteredNode: RegisteredNode{ID: id, Label: "Node", NodeID: "a"}, build: &BinaryIdentity{ImageRevision: strings.Repeat("b", 40), ImageBuiltAt: test.builtAt}}
		if got := u.status().Images[1].Status; got != test.want {
			t.Fatal("same-source build comparison failed", got, test.want)
		}
	}
	if installedRevision(&BinaryIdentity{SourceCommit: strings.Repeat("a", 40), Modified: Ptr(true)}) != "" {
		t.Fatal("dirty source treated as a published image")
	}
	if installedRevision(&BinaryIdentity{SourceCommit: strings.Repeat("a", 40)}) != "" {
		t.Fatal("unknown source cleanliness treated as a published image")
	}
}

func TestDashboardUpdatesHonorRateLimitsAndCancellation(t *testing.T) {
	d, _, _ := dashboardRegistryFixture(t)
	var calls atomic.Int32
	u, err := newDashboardUpdates(d, true, updateDoer(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return updateResponse(429, nil, http.Header{"Retry-After": []string{"86400"}}), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer u.client.Close()
	u.check(context.Background())
	if calls.Load() != 1 || u.status().State != "unavailable" || u.cache.ThrottledUntil <= time.Now().Add(23*time.Hour).UnixMilli() {
		t.Fatal("registry throttle was ignored")
	}
	if u.request() == nil {
		t.Fatal("manual check bypassed registry throttle")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var done sync.WaitGroup
	done.Add(1)
	go func() { defer done.Done(); u.run(ctx) }()
	done.Wait()
	if calls.Load() != 1 {
		t.Fatal("cancelled scheduler performed another request")
	}
}
