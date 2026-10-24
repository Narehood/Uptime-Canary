package canary

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"
)

func (d *Dashboard) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if strings.HasPrefix(r.URL.Path, "/public/") {
		d.publicRequest(w, r)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !slices.Contains(d.origins, origin) {
		errorReply(w, fail(403, "Origin is not allowed"))
		return
	}
	if r.URL.Path == "/healthz" && r.Method == "GET" {
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.URL.Path == "/readyz" && r.Method == "GET" {
		n := d.accounts.node
		n.mu.Lock()
		ready := !n.stopped && n.fault == nil
		n.mu.Unlock()
		status := 200
		if !ready {
			status = 503
		}
		reply(w, status, map[string]bool{"ready": ready})
		return
	}
	if r.URL.Path == "/api/dashboard/setup" || strings.HasPrefix(r.URL.Path, "/api/auth/") || r.URL.Path == "/api/accounts" || strings.HasPrefix(r.URL.Path, "/api/accounts/") {
		d.accounts.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/api/push" || strings.HasPrefix(r.URL.Path, "/api/push/") {
		d.pushRequest(w, r)
		return
	}
	actor, err := d.accounts.Authenticate(r)
	if err != nil {
		errorReply(w, err)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/dashboard/") {
		d.registryRequest(w, r, actor)
		return
	}
	if !dashboardMonitoringPath(r.URL.Path) {
		errorReply(w, fail(404, "Not found"))
		return
	}
	if r.Method != "GET" && actor.Role == "viewer" {
		errorReply(w, fail(403, "Insufficient permissions"))
		return
	}
	selected := r.Header.Get("X-Canary-Registered-Node")
	if selected == "" {
		if r.Method == "GET" && r.URL.Path == "/api/overview" {
			out := Overview{Node: Identity{"dashboard", "Dashboard"}, Now: time.Now().UnixMilli(), Monitors: []MonitorView{}, Nodes: []NodeView{}, Groups: []GroupView{}, Maintenance: []MaintenanceView{}, Incidents: []IncidentView{}, Notifications: []NotificationView{}}
			out.User.Name, out.User.Role = actor.Name, actor.Role
			reply(w, 200, out)
			return
		}
		errorReply(w, fail(409, "Select a registered monitoring node first"))
		return
	}
	d.proxyNode(w, r, actor, selected)
}

func dashboardMonitoringPath(p string) bool {
	if path.Clean(p) != p {
		return false
	}
	for _, prefix := range []string{"/api/monitors", "/api/groups", "/api/notification-channels", "/api/maintenance", "/api/status-pages", "/api/tags"} {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return p == "/api/overview" || p == "/api/notification-deliveries" || p == "/api/metrics"
}

func (d *Dashboard) currentActor(r *http.Request, actor AccountView, admin bool) error {
	current, err := d.accounts.Authenticate(r)
	if err != nil {
		return err
	}
	if current.ID != actor.ID || current.Role != actor.Role {
		return fail(409, "Dashboard permissions changed; sign in again")
	}
	if admin && current.Role != "admin" {
		return fail(403, "Insufficient permissions")
	}
	if r.Context().Err() != nil {
		return fail(408, "Request cancelled")
	}
	return nil
}

func (d *Dashboard) registryRequest(w http.ResponseWriter, r *http.Request, actor AccountView) {
	p := r.URL.Path
	if p == "/api/dashboard/nodes" && r.Method == "GET" {
		d.mu.Lock()
		out := []RegisteredNode{}
		for _, row := range d.registry {
			out = append(out, d.publicNode(row, actor.Role))
		}
		d.mu.Unlock()
		slices.SortFunc(out, func(a, b RegisteredNode) int { return strings.Compare(a.Label, b.Label) })
		reply(w, 200, out)
		return
	}
	if strings.HasPrefix(p, "/api/dashboard/nodes/") {
		parts := strings.Split(strings.TrimPrefix(p, "/api/dashboard/nodes/"), "/")
		if len(parts) == 2 && parts[1] == "check" && r.Method == "POST" {
			row, err := d.checkNode(r.Context(), parts[0])
			if err == nil {
				err = d.currentActor(r, actor, false)
			}
			if err != nil {
				errorReply(w, err)
				return
			}
			reply(w, 200, d.publicNode(row, actor.Role))
			return
		}
		if actor.Role != "admin" {
			errorReply(w, fail(403, "Insufficient permissions"))
			return
		}
		if len(parts) == 2 && parts[1] == "retry" && r.Method == "POST" {
			if err := d.currentActor(r, actor, true); err != nil {
				errorReply(w, err)
				return
			}
			row, err := d.finishEnrollment(r.Context(), parts[0])
			if err != nil {
				errorReply(w, err)
				return
			}
			if row.ID == "" {
				reply(w, 204, nil)
				return
			}
			reply(w, 200, d.publicNode(row, actor.Role))
			return
		}
		if len(parts) == 1 && r.Method == "DELETE" {
			d.removeNode(w, r, actor, parts[0])
			return
		}
	}
	if actor.Role != "admin" {
		errorReply(w, fail(403, "Insufficient permissions"))
		return
	}
	if p == "/api/dashboard/verify" && r.Method == "POST" {
		d.verifyNode(w, r, actor)
		return
	}
	if strings.HasPrefix(p, "/api/dashboard/verify/") && r.Method == "DELETE" {
		id := strings.TrimPrefix(p, "/api/dashboard/verify/")
		d.mu.Lock()
		v, ok := d.tickets[id]
		if ok && v.actorID == actor.ID && matches(v.bearerHash, bearer(r)) {
			delete(d.tickets, id)
		}
		d.mu.Unlock()
		reply(w, 204, nil)
		return
	}
	if p == "/api/dashboard/nodes" && r.Method == "POST" {
		d.addNode(w, r, actor)
		return
	}
	errorReply(w, fail(404, "Not found"))
}

func (d *Dashboard) verifyNode(w http.ResponseWriter, r *http.Request, actor AccountView) {
	b, err := readBounded(r.Body, 4096)
	var in struct {
		Label string `json:"label"`
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err == nil {
		err = dashboardJSON(b, &in, "label", "url", "token")
	}
	if err == nil {
		err = validText(in.Label, "node label", 128)
	}
	if err == nil {
		in.URL, err = dashboardNodeURL(in.URL)
	}
	if err == nil {
		err = tokenValue(in.Token)
	}
	if err != nil {
		errorReply(w, err)
		return
	}
	if strings.EqualFold(strings.SplitN(in.URL, "://", 2)[1], r.Host) || slices.Contains(d.origins, in.URL) {
		errorReply(w, fail(400, "Use the monitoring node address, not this dashboard address"))
		return
	}
	if err = d.currentActor(r, actor, true); err != nil {
		errorReply(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	row := registeredNodeRecord{RegisteredNode: RegisteredNode{URL: in.URL}}
	if err = d.nodeActor(ctx, row, in.Token, "admin"); err != nil {
		errorReply(w, err)
		return
	}
	view, err := d.readNode(ctx, row, in.Token)
	if err != nil {
		errorReply(w, err)
		return
	}
	row.NodeID = view.Node.ID
	// New node binaries advertise the atomic role-enrollment API. Reject older
	// nodes before creating a journal or changing any remote account.
	resp, status, err := d.nodeRequest(ctx, row, "", "GET", "/api/auth/status", nil)
	var capability struct {
		DashboardEnrollment bool `json:"dashboardEnrollment"`
	}
	if err != nil {
		errorReply(w, err)
		return
	}
	if resp.StatusCode != 200 || json.Unmarshal(status, &capability) != nil || !capability.DashboardEnrollment {
		errorReply(w, fail(409, "Upgrade this monitoring node before adding it to the dashboard"))
		return
	}
	if err = d.currentActor(r, actor, true); err != nil {
		errorReply(w, err)
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.registry) >= 64 {
		errorReply(w, fail(409, "Dashboard node capacity exceeded"))
		return
	}
	for _, saved := range d.registry {
		if saved.NodeID == view.Node.ID || saved.URL == in.URL {
			errorReply(w, fail(409, "This node identity or address is already registered"))
			return
		}
	}
	now := time.Now().UnixMilli()
	for key, v := range d.tickets {
		if v.ExpiresAt <= now {
			delete(d.tickets, key)
		}
	}
	if len(d.tickets) >= 16 {
		errorReply(w, fail(429, "Too many pending node verifications; cancel one or wait two minutes"))
		return
	}
	ticket, err := randomCredential(32)
	if err != nil {
		errorReply(w, err)
		return
	}
	v := nodeVerification{Ticket: ticket, ExpiresAt: now + 120000, Node: view.Node, Label: in.Label, URL: in.URL, PlainHTTP: strings.HasPrefix(in.URL, "http:"), actorID: actor.ID, bearerHash: digest(bearer(r)), token: in.Token}
	d.tickets[ticket] = v
	reply(w, 200, v)
}

func (d *Dashboard) addNode(w http.ResponseWriter, r *http.Request, actor AccountView) {
	b, err := readBounded(r.Body, 4096)
	var in struct {
		Ticket string `json:"ticket"`
	}
	if err == nil {
		err = dashboardJSON(b, &in, "ticket")
	}
	if err != nil {
		errorReply(w, err)
		return
	}
	if err = d.currentActor(r, actor, true); err != nil {
		errorReply(w, err)
		return
	}
	d.mu.Lock()
	v, ok := d.tickets[in.Ticket]
	if !ok || v.ExpiresAt <= time.Now().UnixMilli() || v.actorID != actor.ID || !matches(v.bearerHash, bearer(r)) {
		d.mu.Unlock()
		errorReply(w, fail(409, "Node verification expired; verify again"))
		return
	}
	row := registeredNodeRecord{RegisteredNode: RegisteredNode{ID: strings.ReplaceAll(newID(), "-", ""), Label: v.Label, NodeID: v.Node.ID, NodeName: v.Node.Name, URL: v.URL, State: "enrolling"}}
	secret := dashboardNodeSecrets{dashboardRoleTokens: dashboardRoleTokens{NodeID: row.NodeID}, EnrollmentToken: v.token}
	for _, role := range dashboardRoles {
		token, e := randomCredential(32)
		if e != nil {
			d.mu.Unlock()
			errorReply(w, e)
			return
		}
		switch role {
		case "admin":
			secret.Admin = token
		case "editor":
			secret.Editor = token
		case "viewer":
			secret.Viewer = token
		}
	}
	if len(d.registry) >= 64 {
		d.mu.Unlock()
		errorReply(w, fail(409, "Dashboard node capacity exceeded"))
		return
	}
	for _, saved := range d.registry {
		if saved.NodeID == row.NodeID || saved.URL == row.URL {
			d.mu.Unlock()
			errorReply(w, fail(409, "This node is already registered"))
			return
		}
	}
	row.Credentials, err = d.sealSecrets(row, secret)
	if err == nil {
		err = d.currentActor(r, actor, true)
	}
	if err == nil {
		err = d.saveAuthorizedRegistry(row, r, actor)
	}
	if err == nil {
		delete(d.tickets, in.Ticket)
	}
	d.mu.Unlock()
	if err != nil {
		errorReply(w, err)
		return
	}
	row, err = d.finishEnrollment(r.Context(), row.ID)
	if err != nil {
		// The durable journal remains visible and retryable; never imply an
		// ambiguous network result rolled back remote account creation.
		d.mu.Lock()
		row = d.registry[row.ID]
		d.mu.Unlock()
		reply(w, 202, d.publicNode(row, actor.Role))
		return
	}
	reply(w, 201, d.publicNode(row, actor.Role))
}

func (d *Dashboard) finishEnrollment(ctx context.Context, id string) (registeredNodeRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	d.mu.Lock()
	row, ok := d.registry[id]
	if !ok {
		d.mu.Unlock()
		return row, fail(404, "Registered node not found")
	}
	if d.busy[id] {
		d.mu.Unlock()
		return row, fail(409, "This node operation is already running")
	}
	if row.State != "enrolling" && row.State != "removing" {
		d.mu.Unlock()
		return row, nil
	}
	d.busy[id] = true
	secret, err := d.openSecrets(row)
	d.mu.Unlock()
	defer func() { d.mu.Lock(); delete(d.busy, id); d.mu.Unlock() }()
	if err != nil {
		return row, fail(503, "Saved node credentials are unavailable")
	}
	if row.Local && row.NodeID == "pending" {
		row, secret, err = d.identifyLocal(ctx, row, secret)
		if err != nil {
			d.mu.Lock()
			row.Error = dashboardErrorMessage(err)
			_ = d.saveRegistry(row)
			d.mu.Unlock()
			return row, err
		}
	}
	method, token := "POST", secret.EnrollmentToken
	if row.State == "removing" {
		method = "DELETE"
		token = secret.Admin
	}
	if token == "" {
		return row, fail(503, "Saved enrollment credential is unavailable")
	}
	b, _ := json.Marshal(secret.dashboardRoleTokens)
	resp, _, err := d.nodeRequest(ctx, row, token, method, "/api/dashboard-enrollments/"+row.ID, b)
	if method == "POST" && err == nil && resp.StatusCode == 401 {
		// The node may have committed its role bundle before the original
		// enrollment credential was rotated or its password session expired.
		resp, _, err = d.nodeRequest(ctx, row, secret.Admin, method, "/api/dashboard-enrollments/"+row.ID, b)
	}
	if row.State == "removing" && err == nil && resp.StatusCode == 401 && secret.EnrollmentToken != "" {
		// Covers cancellation before provisioning and a lost DELETE response.
		resp, _, err = d.nodeRequest(ctx, row, secret.EnrollmentToken, method, "/api/dashboard-enrollments/"+row.ID, b)
	}
	if err == nil && resp.StatusCode != 201 && !(method == "DELETE" && resp.StatusCode == 204) {
		err = fail(502, "Node delegation was not accepted; check enrollment credentials and node version")
	}
	if err == nil && method == "POST" {
		for _, role := range dashboardRoles {
			if e := d.nodeActor(ctx, row, secret.token(role), role); e != nil {
				err = e
				break
			}
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil {
		row.Error = dashboardErrorMessage(err)
		if e := d.saveRegistry(row); e != nil {
			return row, e
		}
		return row, err
	}
	if method == "DELETE" {
		n := d.accounts.node
		n.mu.Lock()
		err = n.identityTransactionLocked(func(e dbExec) error { return remove(e, "dashboardRegistry", id) })
		n.mu.Unlock()
		if err == nil {
			delete(d.registry, id)
		}
		return registeredNodeRecord{}, err
	}
	// Retain bootstrap only while a journal needs it. Active removal uses the
	// dedicated admin token and its idempotent tombstone on the node.
	secret.EnrollmentToken = ""
	row.Credentials, err = d.sealSecrets(row, secret)
	row.State, row.Error = "starting", ""
	if err == nil {
		err = d.saveRegistry(row)
	}
	return row, err
}

func (d *Dashboard) removeNode(w http.ResponseWriter, r *http.Request, actor AccountView, id string) {
	if err := d.currentActor(r, actor, true); err != nil {
		errorReply(w, err)
		return
	}
	d.mu.Lock()
	row, ok := d.registry[id]
	if !ok {
		d.mu.Unlock()
		errorReply(w, fail(404, "Registered node not found"))
		return
	}
	if row.Local {
		d.mu.Unlock()
		errorReply(w, fail(409, "The bundled local node is managed in deployment settings"))
		return
	}
	if d.busy[id] {
		d.mu.Unlock()
		errorReply(w, fail(409, "This node operation is already running"))
		return
	}
	row.State, row.Error = "removing", ""
	err := d.saveAuthorizedRegistry(row, r, actor)
	d.mu.Unlock()
	if err != nil {
		errorReply(w, err)
		return
	}
	_, err = d.finishEnrollment(r.Context(), id)
	if err == nil {
		reply(w, 204, nil)
		return
	}
	d.mu.Lock()
	row = d.registry[id]
	d.mu.Unlock()
	reply(w, 202, d.publicNode(row, actor.Role))
}

func (d *Dashboard) checkNode(ctx context.Context, id string) (registeredNodeRecord, error) {
	d.mu.Lock()
	row, ok := d.registry[id]
	d.mu.Unlock()
	if !ok {
		return row, fail(404, "Registered node not found")
	}
	if row.State == "enrolling" || row.State == "removing" {
		return row, nil
	}
	secret, err := d.openSecrets(row)
	if err == nil {
		err = d.nodeActor(ctx, row, secret.Viewer, "viewer")
	}
	var view Overview
	if err == nil {
		view, err = d.readNode(ctx, row, secret.Viewer)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	current, exists := d.registry[id]
	if !exists {
		return registeredNodeRecord{}, fail(404, "Registered node not found")
	}
	if current.State == "enrolling" || current.State == "removing" {
		return current, nil
	}
	if err != nil {
		current.State, current.Error = "not-answering", dashboardErrorMessage(err)
		if strings.Contains(current.Error, "credential") {
			current.State = "credential-refused"
		}
		if strings.Contains(current.Error, "Different node") {
			current.State = "different-node"
		}
	} else {
		current.State, current.Error, current.NodeName, current.LastReadAt = "answering", "", view.Node.Name, time.Now().UnixMilli()
		count := len(view.Monitors)
		current.MonitorCount = &count
	}
	// Read-only reachability is ephemeral; no SQLite write on each poll.
	d.registry[id] = current
	return current, nil
}

func (d *Dashboard) proxyNode(w http.ResponseWriter, r *http.Request, actor AccountView, id string) {
	d.mu.Lock()
	row, ok := d.registry[id]
	d.mu.Unlock()
	if !ok {
		errorReply(w, fail(404, "Registered node not found"))
		return
	}
	if row.State == "enrolling" || row.State == "removing" {
		errorReply(w, fail(409, "This node enrollment or removal is not complete"))
		return
	}
	b, err := readBounded(r.Body, maxAPIBody)
	if err == nil {
		err = d.currentActor(r, actor, false)
	}
	secret, e := d.openSecrets(row)
	if err == nil {
		err = e
	}
	if err == nil {
		err = d.nodeActor(r.Context(), row, secret.token(actor.Role), actor.Role)
	}
	if err == nil {
		err = d.currentActor(r, actor, false)
	}
	if err != nil {
		errorReply(w, err)
		return
	}
	u := r.URL.Path
	if r.URL.RawQuery != "" {
		u += "?" + r.URL.RawQuery
	}
	resp, body, err := d.nodeRequest(r.Context(), row, secret.token(actor.Role), r.Method, u, b)
	if err != nil {
		errorReply(w, err)
		return
	}
	if err = d.currentActor(r, actor, false); err != nil {
		errorReply(w, err)
		return
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		errorReply(w, fail(502, "Node credential was refused or its permissions changed"))
		return
	}
	if r.URL.Path == "/api/overview" && resp.StatusCode == 200 {
		var view Overview
		if json.Unmarshal(body, &view) != nil || view.Node.ID != row.NodeID || view.User.Role != actor.Role {
			errorReply(w, fail(502, "The registered node returned an invalid overview"))
			return
		}
		view.User.Name, view.User.Role = actor.Name, actor.Role
		reply(w, 200, view)
		return
	}
	if content := resp.Header.Get("Content-Type"); content != "" {
		w.Header().Set("Content-Type", content)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(body)
}

func (d *Dashboard) publicRequest(w http.ResponseWriter, r *http.Request) {
	if (r.Method != "GET" && r.Method != "HEAD") || path.Clean(r.URL.Path) != r.URL.Path || r.URL.RawPath != "" || len(r.URL.RawQuery) > 4096 {
		errorReply(w, fail(404, "Not found"))
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query["registration"]) != 1 {
		errorReply(w, fail(404, "Public node registration not found"))
		return
	}
	id := query.Get("registration")
	d.mu.Lock()
	row, ok := d.registry[id]
	d.mu.Unlock()
	if !ok || row.State == "enrolling" || row.State == "removing" {
		errorReply(w, fail(404, "Public node registration not found"))
		return
	}
	query.Del("registration")
	u := r.URL.Path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	resp, b, err := d.nodeRequest(r.Context(), row, "", r.Method, u, nil)
	if err != nil {
		errorReply(w, err)
		return
	}
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(b)
}
