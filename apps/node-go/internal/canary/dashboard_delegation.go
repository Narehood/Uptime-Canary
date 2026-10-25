package canary

import (
	"net/http"
	"strings"
)

type dashboardRoleTokens struct {
	NodeID string `json:"nodeId"`
	Admin  string `json:"admin"`
	Editor string `json:"editor"`
	Viewer string `json:"viewer"`
}

var dashboardRoles = []string{"admin", "editor", "viewer"}

func (t dashboardRoleTokens) token(role string) string {
	switch role {
	case "admin":
		return t.Admin
	case "editor":
		return t.Editor
	case "viewer":
		return t.Viewer
	}
	return ""
}

func dashboardDelegateID(enrollment, role string) string {
	return "dashboard-" + enrollment + "-" + role
}

func dashboardManagedAccountID(id string) bool {
	for _, role := range dashboardRoles {
		if strings.HasPrefix(id, "dashboard-") && strings.HasSuffix(id, "-"+role) {
			middle := strings.TrimSuffix(strings.TrimPrefix(id, "dashboard-"), "-"+role)
			return len(middle) == 32 && strings.Trim(middle, "0123456789abcdef") == ""
		}
	}
	return false
}

// An entire role bundle is installed or removed atomically. The caller journals
// its encrypted tokens before sending, making a lost response safely retryable.
// These accounts are local authorization, never replicated configuration.
func (n *Node) serveDashboardDelegation(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/dashboard-enrollments/")
	if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" || (r.Method != "POST" && r.Method != "DELETE" && r.Method != "PATCH") {
		errorReply(w, fail(404, "Not found"))
		return
	}
	n.mu.Lock()
	if n.stopped || n.fault != nil {
		n.mu.Unlock()
		errorReply(w, fail(503, "Runtime unavailable"))
		return
	}
	actor, err := n.authenticate(r)
	// A DELETE may have committed before its response was lost. Retain only
	// its hashed dedicated admin capability, scoped to this enrollment ID.
	if err != nil && r.Method == "DELETE" {
		completed, _, loadErr := loadOne[string](n.store, "dashboardRevocations", id)
		if loadErr == nil && completed != "" && matches(completed, bearer(r)) {
			n.mu.Unlock()
			reply(w, 204, nil)
			return
		}
	}
	n.mu.Unlock()
	if err != nil {
		errorReply(w, err)
		return
	}
	if actor.Role != "admin" {
		errorReply(w, fail(403, "Insufficient permissions"))
		return
	}
	b, err := readBounded(r.Body, 4096)
	if err != nil {
		errorReply(w, err)
		return
	}
	var tokens dashboardRoleTokens
	if err = dashboardJSON(b, &tokens, "nodeId", "admin", "editor", "viewer"); err != nil {
		errorReply(w, err)
		return
	}
	if tokens.NodeID != n.ID {
		errorReply(w, fail(409, "Different node at this address"))
		return
	}
	if tokenValue(tokens.Admin) != nil {
		errorReply(w, fail(400, "Invalid dashboard role credentials"))
		return
	}
	if r.Method == "POST" || r.Method == "PATCH" {
		seen := map[string]bool{}
		for _, role := range dashboardRoles {
			token := tokens.token(role)
			if tokenValue(token) != nil || seen[token] || matches(n.clusterHash, token) {
				errorReply(w, fail(400, "Invalid dashboard role credentials"))
				return
			}
			seen[token] = true
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	current, err := n.identityActorLocked(r, actor)
	if err != nil {
		errorReply(w, err)
		return
	}
	if current.Role != "admin" {
		errorReply(w, fail(403, "Insufficient permissions"))
		return
	}
	if r.Context().Err() != nil {
		errorReply(w, fail(408, "Request cancelled"))
		return
	}
	updates := map[string]account{}
	deleted := map[string]bool{}
	for _, role := range dashboardRoles {
		key := dashboardDelegateID(id, role)
		existing, exists := n.accounts[key]
		if r.Method == "DELETE" {
			deleted[key] = true
			continue
		}
		if exists {
			if existing.Role != role || existing.PasswordEnabled || r.Method != "PATCH" && !matches(existing.Hash, tokens.token(role)) {
				errorReply(w, fail(409, "Dashboard delegation differs from its saved credentials"))
				return
			}
			if matches(existing.Hash, tokens.token(role)) {
				continue
			}
		} else if r.Method == "PATCH" {
			errorReply(w, fail(409, "Dashboard delegation no longer exists"))
			return
		}
		if n.pushCredentialInUse(tokens.token(role)) {
			errorReply(w, fail(400, "Management and push credentials must differ"))
			return
		}
		for accountID, a := range n.accounts {
			if r.Method == "PATCH" && accountID == key {
				continue
			}
			if strings.EqualFold(a.Name, key) || a.Hash != "" && matches(a.Hash, tokens.token(role)) {
				errorReply(w, fail(409, "Credential already exists"))
				return
			}
		}
		if r.Method == "PATCH" {
			if existing.AuthVersion >= uint64(maxSafe) {
				errorReply(w, fail(409, "Account authorization version exhausted"))
				return
			}
			existing.Hash, existing.AuthVersion = digest(tokens.token(role)), existing.AuthVersion+1
			updates[key] = existing
		} else {
			updates[key] = account{AccountView: AccountView{ID: key, Name: key, Role: role}, Hash: digest(tokens.token(role)), AuthVersion: 1}
		}
	}
	if r.Method == "POST" && len(n.accounts)+len(updates) > 64 {
		errorReply(w, fail(409, "Account capacity exceeded"))
		return
	}
	if r.Method == "POST" {
		revoked, loadErr := loadAll[string](n.store, "dashboardRevocations")
		if loadErr != nil {
			errorReply(w, fail(503, "Cannot read dashboard revocations"))
			return
		}
		if revoked[id] != "" {
			errorReply(w, fail(409, "This dashboard enrollment was revoked"))
			return
		}
		if len(revoked) >= 1024 {
			errorReply(w, fail(409, "Dashboard enrollment history capacity exceeded"))
			return
		}
	}
	if r.Method == "DELETE" {
		remainingAdmin := false
		for key, a := range n.accounts {
			remainingAdmin = remainingAdmin || (a.Role == "admin" && !deleted[key])
		}
		if !remainingAdmin {
			errorReply(w, fail(409, "Cannot remove the last administrator"))
			return
		}
	}
	if err := n.identityTransactionLocked(func(e dbExec) error {
		if r.Method == "DELETE" {
			if err := put(e, "dashboardRevocations", id, digest(tokens.Admin)); err != nil {
				return err
			}
		}
		for key, a := range updates {
			if err := put(e, "accounts", key, a); err != nil {
				return err
			}
			if r.Method == "PATCH" {
				for _, session := range n.accountSessionKeysLocked(key) {
					if err := remove(e, "sessions", session); err != nil {
						return err
					}
				}
			}
		}
		for key := range deleted {
			if err := remove(e, "accounts", key); err != nil {
				return err
			}
			for _, session := range n.accountSessionKeysLocked(key) {
				if err := remove(e, "sessions", session); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		errorReply(w, err)
		return
	}
	for key, a := range updates {
		n.accounts[key] = a
		if r.Method == "PATCH" {
			for session, saved := range n.sessions {
				if saved.AccountID == key {
					delete(n.sessions, session)
				}
			}
		}
	}
	for key := range deleted {
		delete(n.accounts, key)
		for session, s := range n.sessions {
			if s.AccountID == key {
				delete(n.sessions, session)
			}
		}
	}
	if r.Method == "DELETE" {
		reply(w, 204, nil)
		return
	}
	status := 201
	if r.Method == "PATCH" {
		status = 200
	}
	reply(w, status, map[string]string{"nodeId": n.ID})
}
