package canary

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

const publicBadgePrefix = "/public/badge/"

func publicBadgeQuery(query url.Values) bool {
	selection := len(query["group"]) == 1 && len(query["monitor"]) == 1 && query.Get("group") != "" && query.Get("monitor") != ""
	return len(query) == 0 || selection && (len(query) == 2 || len(query) == 3 && len(query["metric"]) == 1 && query.Get("metric") == "uptime")
}

// The only historical badge selection is a unique explicitly opted-in public
// service. No whole-page uptime is invented from present-day membership.
func (n *Node) publicObservedBadge(ctx context.Context, slug string, query url.Values) (*PublicObservedAvailability, bool) {
	n.mu.Lock()
	page := n.publicStatusPage(slug)
	n.mu.Unlock()
	if page == nil {
		return nil, false
	}
	matches := 0
	var selected *PublicStatusMonitor
	for i := range page.Groups {
		g := &page.Groups[i]
		for j := range g.Monitors {
			m := &g.Monitors[j]
			if g.ID == query.Get("group") && m.Name == query.Get("monitor") {
				matches++
				selected = m
			} else {
				m.availabilityID = "" // A single badge reads only its service.
			}
		}
	}
	if matches != 1 || selected.Availability == nil || !n.fillPublicAvailability(ctx, page) {
		return nil, false
	}
	for _, g := range page.Groups {
		for _, m := range g.Monitors {
			if g.ID == query.Get("group") && m.Name == query.Get("monitor") {
				return m.Availability, m.Availability != nil
			}
		}
	}
	return nil, false
}

// Badge selections are resolved entirely from the existing bounded public
// projection. No private monitor identifier or target is accepted by this API.
func (n *Node) publicBadge(slug string, query url.Values) (string, bool, bool) {
	page := n.publicStatusPage(slug)
	if page == nil {
		return "", false, false
	}
	if len(query) == 0 {
		return page.State, page.Maintenance, true
	}
	if len(query) != 2 || len(query["group"]) != 1 || len(query["monitor"]) != 1 || query.Get("group") == "" || query.Get("monitor") == "" {
		return "", false, false
	}
	state, maintenance, matches := "", false, 0
	for _, group := range page.Groups {
		if group.ID != query.Get("group") {
			continue
		}
		for _, monitor := range group.Monitors {
			if monitor.Name == query.Get("monitor") {
				state, maintenance, matches = monitor.State, monitor.Maintenance, matches+1
			}
		}
	}
	return state, maintenance, matches == 1
}

func (n *Node) servePublicBadge(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if path != "/public/badge" && !strings.HasPrefix(path, publicBadgePrefix) {
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	methodOK := r.Method == http.MethodGet || r.Method == http.MethodHead
	if methodOK {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
	code, body := http.StatusNotFound, `{"error":"Not found"}`
	if methodOK && len(path) <= len(publicBadgePrefix)+64+4 && r.URL.RawPath == "" && len(r.URL.RawQuery) <= 4096 && !r.URL.ForceQuery && strings.HasSuffix(path, ".svg") {
		slug := strings.TrimSuffix(strings.TrimPrefix(path, publicBadgePrefix), ".svg")
		query, err := url.ParseQuery(r.URL.RawQuery)
		// Check query shape before spending the public projection's scan budget.
		queryOK := publicBadgeQuery(query)
		if err == nil && queryOK && statusSlug(slug) {
			found := false
			if query.Get("metric") == "uptime" {
				metric, ok := n.publicObservedBadge(r.Context(), slug, query)
				found = ok
				if ok {
					up, known, window := int64(0), int64(0), int64(0)
					if metric.Summary != nil {
						up, known, window = metric.Summary.UpMs, metric.Summary.KnownMs, metric.To-metric.From
					}
					body = renderObservedBadge(up, known, window, metric.Status == "available")
				}
			} else {
				n.mu.Lock()
				state, maintenance, ok := n.publicBadge(slug, query)
				n.mu.Unlock()
				found = ok
				if ok {
					body = renderStatusBadge(state, maintenance)
				}
			}
			if found {
				code = http.StatusOK
				w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
				w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
			}
		}
	}
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(body))
	}
	return true
}
