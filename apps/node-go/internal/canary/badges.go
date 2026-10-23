package canary

import (
	"net/http"
	"net/url"
	"strings"
)

const publicBadgePrefix = "/public/badge/"

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
		queryOK := len(query) == 0 || len(query) == 2 && len(query["group"]) == 1 && len(query["monitor"]) == 1 && query.Get("group") != "" && query.Get("monitor") != ""
		if err == nil && queryOK && statusSlug(slug) {
			n.mu.Lock()
			state, maintenance, found := n.publicBadge(slug, query)
			n.mu.Unlock()
			if found {
				code, body = http.StatusOK, renderStatusBadge(state, maintenance)
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
