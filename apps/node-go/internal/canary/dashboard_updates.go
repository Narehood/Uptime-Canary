package canary

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"
	"uptime-canary/apps/node-go/internal/updatecheck"
)

const updateInterval = 6 * time.Hour
const updateManualInterval = 15 * time.Minute

type softwareUpdate struct {
	Component      string `json:"component"`
	RegistrationID string `json:"registrationId,omitempty"`
	Label          string `json:"label"`
	Image          string `json:"image"`
	CurrentCommit  string `json:"currentCommit,omitempty"`
	CurrentVersion string `json:"currentVersion,omitempty"`
	LatestVersion  string `json:"latestVersion,omitempty"`
	LatestCommit   string `json:"latestCommit,omitempty"`
	CurrentBuiltAt int64  `json:"currentBuiltAt,omitempty"`
	LatestBuiltAt  int64  `json:"latestBuiltAt,omitempty"`
	Status         string `json:"status"`
}

type updateStatus struct {
	Enabled       bool             `json:"enabled"`
	IntervalHours int              `json:"intervalHours"`
	State         string           `json:"state"`
	CheckedAt     int64            `json:"checkedAt,omitempty"`
	NextCheckAt   int64            `json:"nextCheckAt,omitempty"`
	Images        []softwareUpdate `json:"images"`
}

type updateCache struct {
	Node           updatecheck.Cache `json:"node"`
	Web            updatecheck.Cache `json:"web"`
	Relations      map[string]string `json:"relations"`
	CheckedAt      int64             `json:"checkedAt"`
	LastAttemptAt  int64             `json:"lastAttemptAt"`
	NextCheckAt    int64             `json:"nextCheckAt"`
	ThrottledUntil int64             `json:"throttledUntil"`
	NodeAvailable  bool              `json:"nodeAvailable"`
	WebAvailable   bool              `json:"webAvailable"`
}

type dashboardUpdates struct {
	mu               sync.Mutex
	dashboard        *Dashboard
	client           *updatecheck.Client
	enabled          bool
	checking, queued bool
	cache            updateCache
	wake             chan struct{}
	now              func() time.Time
}

func newDashboardUpdates(d *Dashboard, enabled bool, client updatecheck.Doer) (*dashboardUpdates, error) {
	u := &dashboardUpdates{dashboard: d, enabled: enabled, client: updatecheck.New(client), wake: make(chan struct{}, 1), now: time.Now}
	n := d.accounts.node
	n.mu.Lock()
	cache, _, err := loadOne[updateCache](n.store, "settings", "updateChecks")
	n.mu.Unlock()
	invalid := err != nil || len(cache.Relations) > 128 || cache.CheckedAt < 0 || cache.LastAttemptAt < 0 || cache.NextCheckAt < 0 || cache.ThrottledUntil < 0
	for _, c := range []updatecheck.Cache{cache.Node, cache.Web} {
		if c.Revision != "" && !updatecheck.Commit(c.Revision) || len(c.ETag) > 200 || len(c.Digest) > 71 || len(c.Version) > 128 {
			invalid = true
		}
	}
	for key, relation := range cache.Relations {
		if len(key) != 81 || !updatecheck.Commit(key[:40]) || key[40] != ':' || !updatecheck.Commit(key[41:]) || !slices.Contains([]string{"current", "update-available", "ahead", "unknown", "unavailable"}, relation) {
			invalid = true
		}
	}
	// This optional cache must never prevent dashboard login. Invalid cache
	// waits a full interval instead of retrying the registry on every restart.
	if invalid {
		cache = updateCache{NextCheckAt: u.now().Add(updateInterval).UnixMilli()}
	}
	if cache.Relations == nil {
		cache.Relations = map[string]string{}
	}
	if cache.NextCheckAt == 0 {
		cache.NextCheckAt = u.now().Add(time.Minute).UnixMilli()
	}
	u.cache = cache
	return u, nil
}

func installedRevision(build *BinaryIdentity) string {
	if build == nil {
		return ""
	}
	if updatecheck.Commit(build.ImageRevision) {
		return build.ImageRevision
	}
	if build.Modified != nil && !*build.Modified && updatecheck.Commit(build.SourceCommit) {
		return build.SourceCommit
	}
	return ""
}

func imageBuildTime(value string) int64 {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil || len(value) > 40 || parsed.UnixMilli() <= 0 {
		return 0
	}
	return parsed.UnixMilli()
}

func (u *dashboardUpdates) installed() []softwareUpdate {
	local := LocalBinaryIdentity()
	items := []softwareUpdate{{Component: "dashboard", Label: "Dashboard", Image: "ghcr.io/" + updatecheck.WebRepository, CurrentCommit: installedRevision(&local), CurrentVersion: local.Version, CurrentBuiltAt: imageBuildTime(local.ImageBuiltAt)}}
	d := u.dashboard
	d.mu.Lock()
	for _, row := range d.registry {
		builtAt := int64(0)
		version := ""
		if row.build != nil {
			builtAt = imageBuildTime(row.build.ImageBuiltAt)
			version = row.build.Version
		}
		items = append(items, softwareUpdate{Component: "node", RegistrationID: row.ID, Label: row.Label, Image: "ghcr.io/" + updatecheck.NodeRepository, CurrentCommit: installedRevision(row.build), CurrentVersion: version, CurrentBuiltAt: builtAt})
	}
	d.mu.Unlock()
	slices.SortFunc(items[1:], func(a, b softwareUpdate) int { return stringsCompare(a.Label, b.Label) })
	return items
}

func (u *dashboardUpdates) status() updateStatus {
	items := u.installed()
	u.mu.Lock()
	defer u.mu.Unlock()
	cache := u.cache
	result := updateStatus{Enabled: u.enabled, IntervalHours: 6, State: "not-checked", CheckedAt: cache.CheckedAt, NextCheckAt: cache.NextCheckAt, Images: items}
	if !u.enabled {
		result.State = "disabled"
		result.NextCheckAt = 0
	} else if u.checking || u.queued {
		result.State = "checking"
	} else if cache.LastAttemptAt > 0 {
		result.State = "ok"
		if !cache.NodeAvailable || !cache.WebAvailable {
			result.State = "unavailable"
		}
	}
	for i := range result.Images {
		item := &result.Images[i]
		latest, available := cache.Node, cache.NodeAvailable
		if item.Component == "dashboard" {
			latest, available = cache.Web, cache.WebAvailable
		}
		item.LatestCommit = latest.Revision
		item.LatestVersion = latest.Version
		item.LatestBuiltAt = imageBuildTime(latest.CreatedAt)
		item.Status = "unknown"
		if cache.LastAttemptAt > 0 && !available {
			item.Status = "unavailable"
			continue
		}
		if item.CurrentCommit == "" || !updatecheck.Commit(latest.Revision) {
			continue
		}
		if item.CurrentCommit == latest.Revision {
			if item.CurrentBuiltAt > 0 && item.LatestBuiltAt > 0 {
				item.Status = "current"
				if item.LatestBuiltAt > item.CurrentBuiltAt {
					item.Status = "update-available"
				} else if item.LatestBuiltAt < item.CurrentBuiltAt {
					item.Status = "ahead"
				}
			}
			continue
		}
		if relation := cache.Relations[item.CurrentCommit+":"+latest.Revision]; relation != "" {
			item.Status = relation
		}
	}
	return result
}

func (u *dashboardUpdates) request() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.enabled {
		return fail(409, "Update checks are disabled in deployment settings")
	}
	if u.checking || u.queued {
		return fail(409, "An update check is already running")
	}
	now := u.now().UnixMilli()
	if now < u.cache.ThrottledUntil || u.cache.LastAttemptAt > 0 && now < u.cache.LastAttemptAt+updateManualInterval.Milliseconds() {
		return fail(429, "Update checks are cached; wait at least fifteen minutes between manual checks")
	}
	u.queued = true
	select {
	case u.wake <- struct{}{}:
	default:
	}
	return nil
}

func (u *dashboardUpdates) persist(cache updateCache) error {
	n := u.dashboard.accounts.node
	n.mu.Lock()
	defer n.mu.Unlock()
	// Optional update-cache failure never faults monitoring or dashboard auth.
	// A failed preflight write prevents external I/O, avoiding restart storms.
	return put(n.store.db, "settings", "updateChecks", cache)
}

func (u *dashboardUpdates) check(ctx context.Context) {
	u.mu.Lock()
	if !u.enabled || u.checking {
		u.mu.Unlock()
		return
	}
	u.checking, u.queued = true, false
	cache := u.cache
	cache.Relations = make(map[string]string, len(u.cache.Relations))
	for key, value := range u.cache.Relations {
		cache.Relations[key] = value
	}
	u.cache.LastAttemptAt = u.now().UnixMilli()
	u.cache.NextCheckAt = u.now().Add(updateInterval).UnixMilli()
	cache.LastAttemptAt, cache.NextCheckAt = u.cache.LastAttemptAt, u.cache.NextCheckAt
	cache.NodeAvailable, cache.WebAvailable = false, false
	if err := u.persist(cache); err != nil {
		u.cache.NodeAvailable, u.cache.WebAvailable = false, false
		u.checking = false
		u.mu.Unlock()
		return
	}
	u.mu.Unlock()
	defer func() { u.mu.Lock(); u.checking = false; u.mu.Unlock() }()
	op, cancel := context.WithTimeout(ctx, 50*time.Second)
	defer cancel()
	cache.WebAvailable, cache.NodeAvailable = false, false
	for _, component := range []string{"web", "node"} {
		repository, previous := updatecheck.WebRepository, cache.Web
		if component == "node" {
			repository, previous = updatecheck.NodeRepository, cache.Node
		}
		latest, err := u.client.Registry(op, repository, previous)
		if err == nil {
			if component == "web" {
				cache.Web, cache.WebAvailable = latest, true
			} else {
				cache.Node, cache.NodeAvailable = latest, true
			}
		}
		var throttle *updatecheck.Throttled
		if errors.As(err, &throttle) {
			cache.ThrottledUntil = u.now().Add(throttle.After).UnixMilli()
			cache.NextCheckAt = max(cache.NextCheckAt, cache.ThrottledUntil)
			break
		}
	}
	items := u.installed()
	needed := map[string]bool{}
	for _, item := range items {
		latest := cache.Node
		if item.Component == "dashboard" {
			latest = cache.Web
		}
		if item.CurrentCommit != "" && latest.Revision != "" {
			needed[item.CurrentCommit+":"+latest.Revision] = true
		}
	}
	for key := range cache.Relations {
		if !needed[key] {
			delete(cache.Relations, key)
		}
	}
	comparisons := 0
	for _, item := range items {
		latest, available := cache.Node, cache.NodeAvailable
		if item.Component == "dashboard" {
			latest, available = cache.Web, cache.WebAvailable
		}
		if !available || item.CurrentCommit == "" || item.CurrentCommit == latest.Revision {
			continue
		}
		key := item.CurrentCommit + ":" + latest.Revision
		if relation := cache.Relations[key]; relation != "" && relation != "unknown" && relation != "unavailable" {
			continue
		}
		if comparisons >= 16 || op.Err() != nil || cache.ThrottledUntil > u.now().UnixMilli() {
			continue
		}
		comparisons++
		relation, err := u.client.Relation(op, item.CurrentCommit, latest.Revision)
		cache.Relations[key] = relation
		var throttle *updatecheck.Throttled
		if errors.As(err, &throttle) {
			cache.ThrottledUntil = u.now().Add(throttle.After).UnixMilli()
			cache.NextCheckAt = max(cache.NextCheckAt, cache.ThrottledUntil)
		}
	}
	u.mu.Lock()
	cache.CheckedAt = u.now().UnixMilli()
	u.cache = cache
	_ = u.persist(cache)
	u.mu.Unlock()
}

func (u *dashboardUpdates) run(ctx context.Context) {
	for {
		u.mu.Lock()
		next := u.cache.NextCheckAt
		u.mu.Unlock()
		wait := max(time.Second, time.UnixMilli(next).Sub(u.now()))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-u.wake:
			timer.Stop()
		case <-timer.C:
		}
		if ctx.Err() != nil {
			return
		}
		if !u.dashboard.accounts.Initialized() {
			u.mu.Lock()
			u.queued = false
			u.cache.NextCheckAt = u.now().Add(time.Minute).UnixMilli()
			u.mu.Unlock()
			continue
		}
		u.check(ctx)
	}
}

func (d *Dashboard) updateRequest(w http.ResponseWriter, r *http.Request, actor AccountView) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		errorReply(w, fail(400, "Update checks do not accept query parameters"))
		return
	}
	if r.URL.Path == "/api/dashboard/updates" && r.Method == "GET" {
		reply(w, 200, d.updates.status())
		return
	}
	if r.URL.Path == "/api/dashboard/updates/check" && r.Method == "POST" {
		if err := d.currentActor(r, actor, true); err != nil {
			errorReply(w, err)
			return
		}
		if r.ContentLength > 0 {
			errorReply(w, fail(400, "Update checks do not accept a request body"))
			return
		}
		if r.Body != nil {
			if raw, err := readBounded(r.Body, 0); err != nil || len(raw) != 0 {
				errorReply(w, fail(400, "Update checks do not accept a request body"))
				return
			}
		}
		if err := d.updates.request(); err != nil {
			errorReply(w, err)
			return
		}
		reply(w, 202, d.updates.status())
		return
	}
	errorReply(w, fail(404, "Not found"))
}
