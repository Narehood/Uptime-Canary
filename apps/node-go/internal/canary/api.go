package canary

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

func bearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") || len(v) > 300 {
		return ""
	}
	return strings.TrimPrefix(v, "Bearer ")
}
func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if value == nil {
		w.WriteHeader(status)
		return
	}
	b, err := json.Marshal(value)
	if err != nil {
		status = 500
		b = []byte(`{"error":"Internal request failed"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
func errorReply(w http.ResponseWriter, err error) {
	var api *apiError
	if errors.As(err, &api) {
		reply(w, api.Status, map[string]string{"error": api.Message})
		return
	}
	reply(w, 500, map[string]string{"error": "Internal request failed"})
}
func (n *Node) authenticate(r *http.Request) (account, error) {
	token := bearer(r)
	if token != "" {
		if session, ok := n.sessions[digest(token)]; ok {
			if session.ExpiresAt > n.now() {
				if a, exists := n.accounts[session.AccountID]; exists && a.AuthVersion == session.AuthVersion {
					return a, nil
				}
			}
		}
		for _, a := range n.accounts {
			if a.Hash != "" && matches(a.Hash, token) {
				return a, nil
			}
		}
	}
	return account{}, fail(401, "Authentication required")
}
func (n *Node) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	method := r.Method
	if n.servePush(w, r) {
		return
	}
	if path == "/healthz" && method == "GET" {
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	if path == "/readyz" && method == "GET" {
		n.mu.Lock()
		ready := !n.stopped && n.fault == nil
		n.mu.Unlock()
		status := http.StatusOK
		if !ready {
			status = http.StatusServiceUnavailable
		}
		reply(w, status, map[string]bool{"ready": ready})
		return
	}
	if path == "/internal/snapshot" && method == "POST" {
		if !matches(n.clusterHash, bearer(r)) {
			errorReply(w, fail(401, "Peer authentication required"))
			return
		}
		sender := r.Header.Get("X-Canary-Node-Id")
		if sender == n.ID || !n.known[sender] {
			errorReply(w, fail(403, "Unknown peer"))
			return
		}
		if r.ContentLength > maxBody {
			errorReply(w, fail(413, "Request body too large"))
			return
		}
		b, err := readBounded(r.Body, maxBody)
		if err != nil {
			errorReply(w, err)
			return
		}
		incoming, err := n.validateSnapshot(b, sender)
		if err != nil {
			errorReply(w, err)
			return
		}
		n.mu.Lock()
		if n.stopped || n.fault != nil {
			n.mu.Unlock()
			errorReply(w, fail(503, "Runtime unavailable"))
			return
		}
		err = n.mergeSnapshot(incoming)
		if err == nil {
			n.peerSeen[sender] = n.now()
		}
		var outgoing []byte
		if err == nil {
			var page Snapshot
			page, err = n.snapshotPage(incoming.After, "")
			if err == nil {
				outgoing, err = json.Marshal(page)
				if len(outgoing) > maxBody {
					err = fail(503, "Peer page capacity exceeded")
				}
			}
		}
		n.mu.Unlock()
		if err != nil {
			errorReply(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(200)
		_, _ = w.Write(outgoing)
		return
	}
	if n.servePublicStatus(w, r) {
		return
	}
	if n.servePublicBadge(w, r) {
		return
	}
	if !strings.HasPrefix(path, "/api/") {
		errorReply(w, fail(404, "Not found"))
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		if !slices.Contains(n.settings.CORSOrigins, origin) {
			errorReply(w, fail(403, "Origin is not allowed"))
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	if method == "OPTIONS" {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		reply(w, 204, nil)
		return
	}
	if strings.HasPrefix(path, "/api/auth/") || path == "/api/accounts" || strings.HasPrefix(path, "/api/accounts/") {
		n.serveIdentity(w, r)
		return
	}
	n.mu.Lock()
	a, err := n.authenticate(r)
	unavailable := n.stopped || n.fault != nil
	n.mu.Unlock()
	if err != nil {
		errorReply(w, err)
		return
	}
	if unavailable {
		errorReply(w, fail(503, "Runtime unavailable"))
		return
	}
	if path == "/api/accounts" && a.Role != "admin" {
		errorReply(w, fail(403, "Insufficient permissions"))
		return
	}
	if method != "GET" && a.Role == "viewer" {
		errorReply(w, fail(403, "Insufficient permissions"))
		return
	}
	var b []byte
	if method == "POST" || method == "PUT" || method == "DELETE" {
		if r.ContentLength > maxAPIBody {
			errorReply(w, fail(413, "Request body too large"))
			return
		}
		b, err = readBounded(r.Body, maxAPIBody)
		if err != nil {
			errorReply(w, err)
			return
		}
		if !json.Valid(b) {
			errorReply(w, fail(400, "Invalid JSON body"))
			return
		}
	}
	if maintenanceNeedsPreflight(path, method) {
		prepared, prepareErr := n.prepareMaintenanceRequest(r.Context(), b)
		if prepareErr != nil {
			errorReply(w, prepareErr)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), maintenanceRequestPlansKey{}, prepared))
	}
	n.mu.Lock()
	// Reading the request body can block while the account is changed or the
	// bearer is revoked. Authorize the operation against the current identity
	// while holding the same lock used to apply it.
	a, err = n.authenticate(r)
	if err == nil && method != "GET" && a.Role == "viewer" {
		err = fail(403, "Insufficient permissions")
	}
	if err != nil {
		n.mu.Unlock()
		errorReply(w, err)
		return
	}
	status, value, err := n.apiLocked(r, a, b)
	var encoded []byte
	metrics, isMetrics := value.(metricsResponse)
	if err == nil && value != nil && !isMetrics {
		encoded, err = json.Marshal(value)
	}
	n.mu.Unlock()
	if err != nil {
		errorReply(w, err)
		return
	}
	if isMetrics {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(metrics))
		return
	}
	if value == nil {
		reply(w, status, nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(encoded)
}
func (n *Node) apiLocked(r *http.Request, a account, b []byte) (int, any, error) {
	if n.stopped || n.fault != nil {
		return 0, nil, fail(503, "Runtime unavailable")
	}
	path, method := r.URL.Path, r.Method
	if path == "/api/tags" || strings.HasPrefix(path, "/api/tags/") {
		return n.tagAPI(r, a, b)
	}
	if path == "/api/status-pages" || strings.HasPrefix(path, "/api/status-pages/") {
		return n.statusPageAPI(r, a, b)
	}
	if path == "/api/metrics" && method == "GET" {
		return 200, n.metrics(), nil
	}
	if path == "/api/notification-channels" || strings.HasPrefix(path, "/api/notification-channels/") {
		return n.channelAPI(r, a, b)
	}
	if path == "/api/notification-deliveries" && method == "GET" {
		return n.deliveryAPI(r)
	}
	if path == "/api/maintenance" || strings.HasPrefix(path, "/api/maintenance/") {
		return n.maintenanceAPI(r, a, b)
	}
	if path == "/api/overview" && method == "GET" {
		return 200, n.overview(a), nil
	}
	if path == "/api/monitors" {
		if method == "GET" {
			return 200, n.monitorViews(a.Role), nil
		}
		if method == "POST" {
			if len(n.heads) >= maxMonitors {
				return 0, nil, fail(409, "Monitor capacity exceeded")
			}
			input, err := parseMonitor(b, n.known)
			if err != nil {
				return 0, nil, err
			}
			id := newID()
			if err = n.validateReferences(&Monitor{MonitorInput: input, ID: id}, nil); err != nil {
				return 0, nil, err
			}
			m, err := n.writeMonitor(input, nil, id, false)
			return 201, m, err
		}
	}
	if path == "/api/groups" {
		if method == "GET" {
			return 200, n.groupViews(), nil
		}
		if method == "POST" {
			if len(n.groups) >= maxGroups {
				return 0, nil, fail(409, "Group capacity exceeded")
			}
			input, err := parseGroup(b, n.known)
			if err != nil {
				return 0, nil, err
			}
			id := newID()
			if err = n.validateReferences(nil, &Group{GroupInput: input, ID: id}); err != nil {
				return 0, nil, err
			}
			g, err := n.writeGroup(input, nil, id, false)
			return 201, g, err
		}
	}
	if path == "/api/accounts" {
		if method == "GET" {
			out := []AccountView{}
			for _, a := range n.accounts {
				out = append(out, a.AccountView)
			}
			slices.SortFunc(out, func(a, b AccountView) int { return stringsCompare(a.Name, b.Name) })
			return 200, out, nil
		}
		if method == "POST" {
			if len(n.accounts) >= 64 {
				return 0, nil, fail(409, "Account capacity exceeded")
			}
			var raw struct{ Name, Role, Token string }
			if json.Unmarshal(b, &raw) != nil {
				return 0, nil, fail(400, "Invalid account input")
			}
			if err := validText(raw.Name, "name", 128); err != nil {
				return 0, nil, err
			}
			if !validRole(raw.Role) {
				return 0, nil, fail(400, "Invalid role")
			}
			if err := tokenValue(raw.Token); err != nil {
				return 0, nil, err
			}
			hash := digest(raw.Token)
			for _, existing := range n.accounts {
				if existing.Name == raw.Name || existing.Hash == hash {
					return 0, nil, fail(409, "Account name or credential already exists")
				}
			}
			if matches(n.clusterHash, raw.Token) {
				return 0, nil, fail(400, "Management and cluster tokens must differ")
			}
			if n.pushCredentialInUse(raw.Token) {
				return 0, nil, fail(400, "Management and push credentials must differ")
			}
			a := account{AccountView: AccountView{ID: newID(), Name: raw.Name, Role: raw.Role}, Hash: hash}
			if !n.dbPut("accounts", a.ID, a) {
				return 0, nil, fail(503, "Persistence unavailable")
			}
			n.accounts[a.ID] = a
			return 201, a.AccountView, nil
		}
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/"), "/")
	if len(parts) < 2 || len(parts) > 3 || (parts[0] != "monitors" && parts[0] != "groups") {
		return 0, nil, fail(404, "Not found")
	}
	id := parts[1]
	if err := identifier(id, "id"); err != nil {
		return 0, nil, err
	}
	action := ""
	if len(parts) == 3 {
		action = parts[2]
	}
	isGroup := parts[0] == "groups"
	var revisions []string
	var deleted bool
	if isGroup {
		heads, ok := n.groups[id]
		if !ok {
			return 0, nil, fail(404, "Group not found")
		}
		for _, h := range heads {
			revisions = append(revisions, h.Group.Revision)
		}
		if len(heads) == 1 {
			deleted = heads[0].Group.Deleted
		}
		if action == "conflicts" && method == "GET" {
			out := []Group{}
			for _, h := range heads {
				out = append(out, h.Group)
			}
			return 200, out, nil
		}
	} else {
		heads, ok := n.heads[id]
		if !ok {
			return 0, nil, fail(404, "Monitor not found")
		}
		for _, h := range heads {
			revisions = append(revisions, h.Monitor.Revision)
		}
		if len(heads) == 1 {
			deleted = heads[0].Monitor.Deleted
		}
		if action == "history" && method == "GET" {
			history, err := n.localHistory(id, r.URL.Query())
			return 200, history, err
		}
		if action == "statistics" && method == "GET" {
			statistics, err := n.historyStatistics(id, r.URL.Query())
			return 200, statistics, err
		}
		if action == "availability" && method == "GET" {
			query, err := url.ParseQuery(r.URL.RawQuery)
			if err != nil {
				return 0, nil, fail(400, "Invalid availability query")
			}
			history, err := n.availabilityHistory(id, query)
			return 200, history, err
		}
		if action == "conflicts" && method == "GET" {
			out := []Monitor{}
			for _, h := range heads {
				out = append(out, publicMonitor(h.Monitor, a.Role))
			}
			return 200, out, nil
		}
	}
	if a.Role == "viewer" {
		return 0, nil, fail(403, "Insufficient permissions")
	}
	if !(action == "resolve" && method == "POST" || action == "" && (method == "PUT" || method == "DELETE")) {
		return 0, nil, fail(404, "Not found")
	}
	object, err := jsonObject(b)
	if err != nil {
		return 0, nil, err
	}
	var raw struct {
		Revision  string   `json:"revision"`
		Revisions []string `json:"revisions"`
	}
	_ = json.Unmarshal(object["revision"], &raw.Revision)
	_ = json.Unmarshal(object["revisions"], &raw.Revisions)
	if action == "resolve" && method == "POST" {
		supplied := unique(raw.Revisions)
		if len(supplied) != len(raw.Revisions) || len(supplied) != len(revisions) {
			return 0, nil, fail(409, "Resolution must name every current head exactly once")
		}
		for _, v := range revisions {
			if !slices.Contains(supplied, v) {
				return 0, nil, fail(409, "Resolution must name every current head exactly once")
			}
		}
		if len(revisions) < 2 {
			return 0, nil, fail(409, "Configuration has no unresolved conflict")
		}
	} else if action == "" && (method == "PUT" || method == "DELETE") {
		if len(revisions) != 1 || raw.Revision != revisions[0] {
			return 0, nil, fail(409, "Revision is stale or configuration has conflicting heads")
		}
		if deleted {
			return 0, nil, fail(404, "Configuration was deleted")
		}
		if method == "DELETE" {
			var err error
			if isGroup {
				_, err = n.writeGroup(n.groups[id][0].Group.GroupInput, n.groups[id], id, true)
			} else {
				_, err = n.writeMonitor(n.heads[id][0].Monitor.MonitorInput, n.heads[id], id, true)
			}
			return 204, nil, err
		}
	} else {
		return 0, nil, fail(404, "Not found")
	}
	if isGroup {
		input, err := parseGroup(b, n.known)
		if err != nil {
			return 0, nil, err
		}
		if err = n.validateReferences(nil, &Group{GroupInput: input, ID: id}); err != nil {
			return 0, nil, err
		}
		g, err := n.writeGroup(input, n.groups[id], id, false)
		return 200, g, err
	}
	input, err := parseMonitor(b, n.known)
	if err != nil {
		return 0, nil, err
	}
	if err = n.validateReferences(&Monitor{MonitorInput: input, ID: id}, nil); err != nil {
		return 0, nil, err
	}
	m, err := n.writeMonitor(input, n.heads[id], id, false)
	return 200, m, err
}
