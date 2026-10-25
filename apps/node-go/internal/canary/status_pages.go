package canary

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const resourceStatusPage = "status-page"
const maxStatusPages = 64
const publicStatusPrefix = "/public/status/"

// Revision history may produce arbitrarily many concurrent heads. Public reads
// have their own work budget; exhausting it never drops replicated resources
// or chooses a page before checking for a duplicate later in the scan.
const publicStatusHeadBudget = maxResources * maxClusterMembers
const publicStatusByteBudget = 16 * maxBody

// These names and messages are an operator's explicit publication selection.
// They never inherit names, messages or group membership from private monitors.
type StatusPageMonitor struct {
	MonitorID        string `json:"monitorId"`
	Name             string `json:"name"`
	ShowCertificate  bool   `json:"showCertificate"`
	ShowTags         bool   `json:"showTags,omitempty"`
	ShowAvailability bool   `json:"showAvailability,omitempty"`
}

type StatusPageGroup struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	Monitors []StatusPageMonitor `json:"monitors"`
}

type StatusPageAnnouncement struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	State     string `json:"state"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	Pinned    bool   `json:"pinned"`
}

type StatusPageInput struct {
	Slug          string                   `json:"slug"`
	Title         string                   `json:"title"`
	Description   string                   `json:"description"`
	Enabled       bool                     `json:"enabled"`
	Groups        []StatusPageGroup        `json:"groups"`
	Announcements []StatusPageAnnouncement `json:"announcements"`
	Appearance    *StatusPageAppearance    `json:"appearance,omitempty"`
	PublicURL     string                   `json:"publicUrl,omitempty"`
	RSSEnabled    bool                     `json:"rssEnabled,omitempty"`
}

// These are explicitly public, operator-authored values. Styling is typed;
// arbitrary HTML, CSS and scripts are not executed in an authenticated page.
type StatusPageAppearance struct {
	Theme             string `json:"theme,omitempty"`
	AccentColor       string `json:"accentColor,omitempty"`
	LogoURL           string `json:"logoUrl,omitempty"`
	FooterText        string `json:"footerText,omitempty"`
	RefreshIntervalMs int64  `json:"refreshIntervalMs,omitempty"`
	Markdown          bool   `json:"markdown,omitempty"`
}

type StatusPage struct {
	StatusPageInput
	ID        string `json:"id"`
	Revision  string `json:"revision"`
	UpdatedAt int64  `json:"updatedAt"`
	Deleted   bool   `json:"deleted,omitempty"`
}

type StatusPageView struct {
	StatusPage     StatusPage `json:"statusPage"`
	ConfigConflict bool       `json:"configConflict"`
}

// Public types deliberately contain no resource/monitor revision, target,
// node identity, private observation, incident or maintenance configuration.
type PublicStatusCertificate struct {
	NotBefore time.Time `json:"notBefore"`
	NotAfter  time.Time `json:"notAfter"`
}

type PublicStatusMonitor struct {
	Name                 string                      `json:"name"`
	State                string                      `json:"state"`
	Maintenance          bool                        `json:"maintenance"`
	Certificate          *PublicStatusCertificate    `json:"certificate,omitempty"`
	Tags                 []PublicStatusTag           `json:"tags,omitempty"`
	Availability         *PublicObservedAvailability `json:"availability,omitempty"`
	availabilityID       string
	availabilityRevision string
}

type PublicStatusGroup struct {
	ID          string                `json:"id"`
	Name        string                `json:"name"`
	State       string                `json:"state"`
	Maintenance bool                  `json:"maintenance"`
	Monitors    []PublicStatusMonitor `json:"monitors"`
}

type PublicStatusPage struct {
	Slug           string                   `json:"slug"`
	Title          string                   `json:"title"`
	Description    string                   `json:"description"`
	State          string                   `json:"state"`
	Maintenance    bool                     `json:"maintenance"`
	GeneratedAt    int64                    `json:"generatedAt"`
	Groups         []PublicStatusGroup      `json:"groups"`
	Announcements  []StatusPageAnnouncement `json:"announcements"`
	Appearance     *StatusPageAppearance    `json:"appearance,omitempty"`
	PublicURL      string                   `json:"publicUrl,omitempty"`
	RSSEnabled     bool                     `json:"rssEnabled,omitempty"`
	publicationKey string
}

func statusSlug(s string) bool {
	if len(s) < 1 || len(s) > 64 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return false
		}
	}
	return true
}

func statusText(s string, limit int, empty, multiline bool) bool {
	return utf8.ValidString(s) && len(s) <= limit && (empty || strings.TrimSpace(s) != "") &&
		!strings.ContainsFunc(s, func(c rune) bool { return c == 127 || c < 32 && !(multiline && c == '\n') })
}

// encoding/json alone accepts duplicate and case-insensitive keys. Reject both
// before decoding anything that controls publication or revision authorization.
func statusObject(b []byte, fields ...string) (map[string]json.RawMessage, error) {
	bad := fail(400, "Invalid status page input")
	d := json.NewDecoder(bytes.NewReader(b))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return nil, bad
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		t, err := d.Token()
		key, ok := t.(string)
		if err != nil || !ok || !slices.Contains(fields, key) || out[key] != nil {
			return nil, bad
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, bad
		}
		out[key] = raw
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') {
		return nil, bad
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, bad
	}
	return out, nil
}

func statusArray(b []byte, limit int) ([]json.RawMessage, error) {
	var out []json.RawMessage
	if len(b) == 0 || json.Unmarshal(b, &out) != nil || out == nil || len(out) > limit {
		return nil, fail(400, "Invalid status page selection")
	}
	return out, nil
}

func parseStatusPage(b []byte) (StatusPageInput, error) {
	var in StatusPageInput
	bad := fail(400, "Invalid status page input")
	if len(b) > maxAPIBody || !utf8.Valid(b) {
		return in, bad
	}
	o, err := statusObject(b, "slug", "title", "description", "enabled", "groups", "announcements", "revision", "revisions", "id", "updatedAt", "deleted", "appearance", "publicUrl", "rssEnabled")
	if err != nil || required(o, "slug", "title", "description", "enabled", "groups", "announcements") != nil || json.Unmarshal(b, &in) != nil ||
		!statusSlug(in.Slug) || !statusText(in.Title, 128, false, false) || !statusText(in.Description, 2048, true, true) {
		return in, bad
	}
	if err := validateStatusPresentation(o, &in); err != nil {
		return in, bad
	}
	// Metadata permits a management response to be edited and sent back. Only
	// the route and revision envelope authorize a write; these never enter Data.
	for _, key := range []string{"id", "revision"} {
		if o[key] != nil {
			var v string
			if json.Unmarshal(o[key], &v) != nil || identifier(v, key) != nil {
				return in, bad
			}
		}
	}
	if o["revisions"] != nil {
		var v []string
		// The body ceiling bounds this list. Cluster membership does not bound
		// the number of current heads after repeated partitioned restores.
		if json.Unmarshal(o["revisions"], &v) != nil || !validIDs(v, maxAPIBody) || len(unique(v)) != len(v) {
			return in, bad
		}
	}
	if o["updatedAt"] != nil {
		var v int64
		if json.Unmarshal(o["updatedAt"], &v) != nil || !validTime(v) {
			return in, bad
		}
	}
	if o["deleted"] != nil {
		var v bool
		if json.Unmarshal(o["deleted"], &v) != nil {
			return in, bad
		}
	}
	groups, err := statusArray(o["groups"], 16)
	if err != nil {
		return in, bad
	}
	groupIDs, monitorIDs := map[string]bool{}, map[string]bool{}
	for i, raw := range groups {
		g, err := statusObject(raw, "id", "name", "monitors")
		if err != nil || required(g, "id", "name", "monitors") != nil {
			return in, bad
		}
		group := in.Groups[i]
		if identifier(group.ID, "group id") != nil || groupIDs[group.ID] || !statusText(group.Name, 128, false, false) {
			return in, bad
		}
		groupIDs[group.ID] = true
		monitors, err := statusArray(g["monitors"], 64)
		if err != nil {
			return in, bad
		}
		for j, raw := range monitors {
			m, err := statusObject(raw, "monitorId", "name", "showCertificate", "showTags", "showAvailability")
			if err != nil || required(m, "monitorId", "name", "showCertificate") != nil {
				return in, bad
			}
			monitor := group.Monitors[j]
			if identifier(monitor.MonitorID, "monitorId") != nil || monitorIDs[monitor.MonitorID] || !statusText(monitor.Name, 128, false, false) {
				return in, bad
			}
			monitorIDs[monitor.MonitorID] = true
			if len(monitorIDs) > 64 {
				return in, bad
			}
		}
	}
	announcements, err := statusArray(o["announcements"], 50)
	if err != nil {
		return in, bad
	}
	announcementIDs := map[string]bool{}
	for i, raw := range announcements {
		a, err := statusObject(raw, "id", "title", "message", "state", "createdAt", "updatedAt", "pinned")
		if err != nil || required(a, "id", "title", "message", "state", "createdAt", "updatedAt", "pinned") != nil {
			return in, bad
		}
		v := in.Announcements[i]
		if identifier(v.ID, "announcement id") != nil || announcementIDs[v.ID] || !statusText(v.Title, 128, false, false) ||
			!statusText(v.Message, 2048, false, true) || !slices.Contains([]string{"investigating", "identified", "monitoring", "resolved"}, v.State) ||
			!validTime(v.CreatedAt) || !validTime(v.UpdatedAt) || v.UpdatedAt < v.CreatedAt {
			return in, bad
		}
		announcementIDs[v.ID] = true
	}
	return in, nil
}

func statusPageOf(r Resource) StatusPage {
	var in StatusPageInput
	_ = json.Unmarshal(r.Data, &in)
	return StatusPage{StatusPageInput: in, ID: r.ID, Revision: r.Revision, UpdatedAt: r.UpdatedAt, Deleted: r.Deleted}
}

func (n *Node) statusPageViews() []StatusPageView {
	out := []StatusPageView{}
	for _, heads := range n.resources {
		for _, h := range heads {
			if h.Resource.Kind == resourceStatusPage && !h.Resource.Deleted {
				out = append(out, StatusPageView{StatusPage: statusPageOf(h.Resource), ConfigConflict: len(heads) != 1 || !n.resourceVersionReady(h)})
				break
			}
		}
	}
	slices.SortFunc(out, func(a, b StatusPageView) int {
		if c := stringsCompare(a.StatusPage.Slug, b.StatusPage.Slug); c != 0 {
			return c
		}
		return stringsCompare(a.StatusPage.ID, b.StatusPage.ID)
	})
	return out
}

func statusPageSlugOf(r Resource) string {
	var header struct {
		Slug string `json:"slug"`
	}
	_ = json.Unmarshal(r.Data, &header)
	return header.Slug
}

// Drafts reserve their slug too. Conflicted and not-yet-ready candidates must
// participate, otherwise a later edge page could silently publish a duplicate.
func (n *Node) statusPageUnique(slug, exceptID string) error {
	for _, heads := range n.resources {
		for _, h := range heads {
			r := h.Resource
			if r.Kind == resourceStatusPage && r.ID != exceptID && !r.Deleted && statusPageSlugOf(r) == slug {
				return fail(409, "Status page slug is already reserved")
			}
		}
	}
	return nil
}

// Called under n.mu, through the same authentication/re-authorization boundary
// as other management APIs. Keep a role check here for direct callers as well.
func (n *Node) statusPageAPI(r *http.Request, a account, b []byte) (int, any, error) {
	path, method := r.URL.Path, r.Method
	if a.Role != "admin" && a.Role != "editor" && a.Role != "viewer" || method != "GET" && a.Role == "viewer" {
		return 0, nil, fail(403, "Insufficient permissions")
	}
	if len(path) > 180 || r.URL.RawQuery != "" {
		return 0, nil, fail(404, "Not found")
	}
	if len(b) > maxAPIBody {
		return 0, nil, fail(413, "Request body too large")
	}
	if path == "/api/status-pages" {
		if method == "GET" {
			return 200, n.statusPageViews(), nil
		}
		if method != "POST" {
			return 0, nil, fail(404, "Not found")
		}
		count := 0
		for _, heads := range n.resources {
			if len(heads) > 0 && heads[0].Resource.Kind == resourceStatusPage {
				count++
			}
		}
		if len(n.resources) >= maxResources || count >= maxStatusPages {
			return 0, nil, fail(409, "Status page capacity exceeded")
		}
		in, err := parseStatusPage(b)
		if err != nil {
			return 0, nil, err
		}
		if err = n.statusPageUnique(in.Slug, ""); err != nil {
			return 0, nil, err
		}
		saved, err := n.writeResource(resourceStatusPage, "", in, false)
		return 201, statusPageOf(saved), err
	}
	if !strings.HasPrefix(path, "/api/status-pages/") {
		return 0, nil, fail(404, "Not found")
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/status-pages/"), "/")
	if len(parts) > 2 || identifier(parts[0], "id") != nil {
		return 0, nil, fail(404, "Not found")
	}
	heads := n.resources[resourceKey(resourceStatusPage, parts[0])]
	if len(heads) == 0 {
		return 0, nil, fail(404, "Status page not found")
	}
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	if method == "GET" && action == "conflicts" {
		out := []StatusPage{}
		for _, h := range heads {
			out = append(out, statusPageOf(h.Resource))
		}
		return 200, out, nil
	}
	if method == "GET" && action == "" {
		if len(heads) != 1 || !n.resourceVersionReady(heads[0]) {
			return 0, nil, fail(409, "Status page is conflicting or awaiting revision evidence")
		}
		if heads[0].Resource.Deleted {
			return 0, nil, fail(404, "Status page was deleted")
		}
		return 200, statusPageOf(heads[0].Resource), nil
	}
	if !(action == "resolve" && method == "POST" || action == "" && (method == "PUT" || method == "DELETE")) {
		return 0, nil, fail(404, "Not found")
	}
	var rev struct {
		Revision  string   `json:"revision"`
		Revisions []string `json:"revisions"`
	}
	// DELETE has its own small envelope. All other writes validate the complete
	// typed input before checking the exact current heads.
	var in StatusPageInput
	var err error
	if method == "DELETE" {
		var o map[string]json.RawMessage
		o, err = statusObject(b, "revision")
		if err == nil {
			err = required(o, "revision")
		}
	} else {
		in, err = parseStatusPage(b)
	}
	if err != nil || json.Unmarshal(b, &rev) != nil {
		return 0, nil, fail(400, "Invalid status page input")
	}
	if method == "DELETE" && identifier(rev.Revision, "revision") != nil {
		return 0, nil, fail(400, "Invalid status page revision")
	}
	if action == "resolve" {
		if len(heads) < 2 || len(rev.Revisions) != len(heads) || len(unique(rev.Revisions)) != len(heads) {
			return 0, nil, fail(409, "Resolution must name every current head exactly once")
		}
		for _, h := range heads {
			if !slices.Contains(rev.Revisions, h.Resource.Revision) || !n.resourceVersionReady(h) {
				return 0, nil, fail(409, "Resolution requires every current head and its revision evidence")
			}
		}
	} else if len(heads) != 1 || heads[0].Resource.Revision != rev.Revision || !n.resourceVersionReady(heads[0]) {
		return 0, nil, fail(409, "Status page revision is stale or conflicting")
	} else if heads[0].Resource.Deleted {
		return 0, nil, fail(404, "Status page was deleted")
	}
	if method == "DELETE" {
		_, err := n.writeResource(resourceStatusPage, parts[0], statusPageOf(heads[0].Resource).StatusPageInput, true)
		return 204, nil, err
	}
	if err = n.statusPageUnique(in.Slug, parts[0]); err != nil {
		return 0, nil, err
	}
	saved, err := n.writeResource(resourceStatusPage, parts[0], in, false)
	return 200, statusPageOf(saved), err
}

// An empty selection is unknown; down evidence wins over unknown or pending.
func publicStatusRollup(states []string) string {
	state := "up"
	if len(states) == 0 {
		return "unknown"
	}
	for _, s := range states {
		if s == "down" {
			return "down"
		}
		if s != "up" && s != "pending" {
			state = "unknown"
		} else if s == "pending" && state == "up" {
			state = "pending"
		}
	}
	return state
}

func (n *Node) publicStatusMonitor(selected StatusPageMonitor) PublicStatusMonitor {
	out := PublicStatusMonitor{Name: selected.Name, State: "unknown"}
	if selected.ShowAvailability {
		out.Availability = unavailablePublicAvailability()
	}
	m := n.current(selected.MonitorID)
	if m == nil {
		return out
	}
	if selected.ShowTags {
		out.Tags = n.publicMonitorTags(*m)
	}
	out.Maintenance = len(n.activeMaintenance(*m)) > 0
	if !m.Enabled || len(m.NodeIDs) == 0 {
		return out
	}
	if selected.ShowAvailability && slices.Contains(m.NodeIDs, n.ID) {
		out.availabilityID, out.availabilityRevision = m.ID, m.Revision
	}
	fresh := n.fresh(*m)
	states := make([]string, 0, len(fresh)+1)
	if len(fresh) < len(m.NodeIDs) {
		states = append(states, "unknown")
	}
	var cert *PublicStatusCertificate
	certsAgree := selected.ShowCertificate && len(fresh) == len(m.NodeIDs)
	for _, s := range fresh {
		state := s.State
		if !slices.Contains([]string{"up", "down", "pending"}, state) || s.CheckedAt > n.now() {
			state = "unknown"
		}
		states = append(states, state)
		c := s.Certificate
		if c == nil || c.NotBefore.IsZero() || c.NotAfter.IsZero() || c.NotAfter.Before(c.NotBefore) || state == "unknown" {
			certsAgree = false
			continue
		}
		if cert != nil && (!cert.NotBefore.Equal(c.NotBefore) || !cert.NotAfter.Equal(c.NotAfter)) {
			certsAgree = false
		}
		cert = &PublicStatusCertificate{NotBefore: c.NotBefore, NotAfter: c.NotAfter}
	}
	out.State = publicStatusRollup(states)
	if certsAgree {
		out.Certificate = cert
	}
	return out
}

func (n *Node) publicStatusPage(slug string) *PublicStatusPage {
	var chosen *ResourceVersion
	headsLeft, bytesLeft := publicStatusHeadBudget, publicStatusByteBudget
	for _, heads := range n.resources {
		headsLeft -= len(heads)
		if headsLeft < 0 {
			return nil
		}
		claims := false
		for _, h := range heads {
			if h.Resource.Kind != resourceStatusPage || h.Resource.Deleted {
				continue
			}
			bytesLeft -= len(h.Resource.Data)
			if bytesLeft < 0 {
				return nil
			}
			if statusPageSlugOf(h.Resource) == slug {
				claims = true
				break
			}
		}
		if !claims {
			continue
		}
		if chosen != nil || len(heads) != 1 || heads[0].Resource.Deleted || !n.resourceVersionReady(heads[0]) {
			return nil
		}
		copy := heads[0]
		chosen = &copy
	}
	if chosen == nil || n.stopped || n.fault != nil {
		return nil
	}
	in, err := parseStatusPage(chosen.Resource.Data)
	if err != nil || !in.Enabled {
		return nil
	}
	out := &PublicStatusPage{Slug: in.Slug, Title: in.Title, Description: in.Description, GeneratedAt: n.now(), Groups: []PublicStatusGroup{}, Announcements: in.Announcements, Appearance: in.Appearance, PublicURL: in.PublicURL, RSSEnabled: in.RSSEnabled}
	out.publicationKey = chosen.Resource.ID + "/" + chosen.Resource.Revision + "/" + chosen.EdgeID
	states := []string{}
	for _, group := range in.Groups {
		g := PublicStatusGroup{ID: group.ID, Name: group.Name, Monitors: []PublicStatusMonitor{}}
		groupStates := []string{}
		for _, selected := range group.Monitors {
			m := n.publicStatusMonitor(selected)
			g.Monitors = append(g.Monitors, m)
			g.Maintenance = g.Maintenance || m.Maintenance
			groupStates = append(groupStates, m.State)
		}
		g.State = publicStatusRollup(groupStates)
		states = append(states, g.State)
		out.Maintenance = out.Maintenance || g.Maintenance
		out.Groups = append(out.Groups, g)
	}
	out.State = publicStatusRollup(states)
	return out
}

// This is a separate unauthenticated JSON boundary, called before management
// CORS and the /api filter. Optional archive reads run outside n.mu under their
// own bounded, nonwaiting work policy; private history is never implied.
func (n *Node) servePublicStatus(w http.ResponseWriter, r *http.Request) bool {
	if n.serveStatusRSS(w, r) {
		return true
	}
	path := r.URL.Path
	if path != "/public/status" && !strings.HasPrefix(path, publicStatusPrefix) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	methodOK := r.Method == "GET" || r.Method == "HEAD"
	if methodOK {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
	code := http.StatusNotFound
	var value any = map[string]string{"error": "Not found"}
	if methodOK && len(path) <= len(publicStatusPrefix)+64 && strings.HasPrefix(path, publicStatusPrefix) &&
		r.URL.RawQuery == "" && !r.URL.ForceQuery && r.URL.RawPath == "" && statusSlug(strings.TrimPrefix(path, publicStatusPrefix)) {
		n.mu.Lock()
		page := n.publicStatusPage(strings.TrimPrefix(path, publicStatusPrefix))
		n.mu.Unlock()
		if page != nil && n.fillPublicAvailability(r.Context(), page) {
			code, value = http.StatusOK, page
		}
	}
	b, err := json.Marshal(value)
	if err != nil {
		code, b = http.StatusNotFound, []byte(`{"error":"Not found"}`)
	}
	w.WriteHeader(code)
	if r.Method != "HEAD" {
		_, _ = w.Write(b)
	}
	return true
}
