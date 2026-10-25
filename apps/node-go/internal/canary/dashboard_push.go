package canary

import (
	"net/http"
	"net/url"
	"path"
	"strings"
)

// Heartbeats authenticate with their monitor's existing Push capability, never
// with a dashboard account or role delegation. Destinations are saved registry
// entries; machine senders need no dashboard session. A sign-in proxy can still
// impose its own access policy before this handler is reached.
func (d *Dashboard) pushRequest(w http.ResponseWriter, r *http.Request) {
	if path.Clean(r.URL.Path) != r.URL.Path || r.URL.RawPath != "" || len(r.URL.RawQuery) > pushBodyBytes {
		errorReply(w, fail(404, "Push endpoint unavailable"))
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query["registration"]) != 1 {
		errorReply(w, fail(404, "Push node registration not found"))
		return
	}
	id := query.Get("registration")
	query.Del("registration")
	d.mu.Lock()
	row, ok := d.registry[id]
	d.mu.Unlock()
	if !ok || row.State == "enrolling" || row.State == "removing" {
		errorReply(w, fail(404, "Push node registration not found"))
		return
	}
	authorization := ""
	if r.Method == "POST" && r.URL.Path == "/api/push" {
		if len(r.Header.Values("Authorization")) != 1 || !strings.HasPrefix(r.Header.Get("Authorization"), "Push ") {
			errorReply(w, fail(400, "Use the monitor's Push credential, not a dashboard account credential"))
			return
		}
		authorization = r.Header.Get("Authorization")
		if !validPushSecret(strings.TrimPrefix(authorization, "Push ")) {
			errorReply(w, fail(404, "Push endpoint unavailable"))
			return
		}
	} else if r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/push/") {
		if len(r.Header.Values("Authorization")) != 0 || !validPushSecret(strings.TrimPrefix(r.URL.Path, "/api/push/")) {
			errorReply(w, fail(404, "Push endpoint unavailable"))
			return
		}
	} else {
		errorReply(w, fail(405, "Push method unavailable"))
		return
	}
	body, err := readBounded(r.Body, pushBodyBytes)
	if err != nil {
		errorReply(w, err)
		return
	}
	// Reading a caller's body may block across an address edit or removal.
	// Use the current route immediately before sending its Push capability.
	// Delegation rotation alone does not change the node's Push credential.
	d.mu.Lock()
	row, ok = d.registry[id]
	d.mu.Unlock()
	if !ok || row.State == "enrolling" || row.State == "removing" {
		errorReply(w, fail(404, "Push node registration not found"))
		return
	}
	u := r.URL.Path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	response, b, err := d.nodeCall(r.Context(), row, authorization, r.Method, u, body)
	if err != nil {
		errorReply(w, err)
		return
	}
	w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(b)
}
