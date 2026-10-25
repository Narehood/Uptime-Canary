package canary

import (
	"net/http"
	"slices"
	"strings"
)

func accountRoute(path, method string) (id string, password bool, ok bool) {
	if path == "/api/accounts" {
		return "", false, method == "GET" || method == "POST"
	}
	if !strings.HasPrefix(path, "/api/accounts/") {
		return "", false, false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/accounts/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", false, false
	}
	if len(parts) == 2 && parts[1] == "password" && method == "POST" {
		return parts[0], true, true
	}
	return parts[0], false, len(parts) == 1 && (method == "PUT" || method == "DELETE")
}

func (n *Node) accountRequest(w http.ResponseWriter, r *http.Request, actor account, in identityInput) {
	if actor.Role != "admin" {
		errorReply(w, fail(403, "Insufficient permissions"))
		return
	}
	id, passwordAction, ok := accountRoute(r.URL.Path, r.Method)
	if !ok {
		errorReply(w, fail(404, "Not found"))
		return
	}
	if id != "" {
		if err := identifier(id, "account ID"); err != nil {
			errorReply(w, err)
			return
		}
	}
	if r.Method == "GET" && id == "" {
		n.mu.Lock()
		defer n.mu.Unlock()
		if _, err := n.identityActorLocked(r, actor); err != nil {
			errorReply(w, err)
			return
		}
		out := []AccountView{}
		for _, a := range n.accounts {
			out = append(out, a.AccountView)
		}
		slices.SortFunc(out, func(a, b AccountView) int { return stringsCompare(a.Name, b.Name) })
		reply(w, 200, out)
		return
	}
	if in.Name != "" {
		if err := validText(in.Name, "name", 128); err != nil {
			errorReply(w, err)
			return
		}
	}
	if in.Role != "" && !validRole(in.Role) {
		errorReply(w, fail(400, "Invalid role"))
		return
	}
	if in.Token != "" {
		if err := tokenValue(in.Token); err != nil {
			errorReply(w, err)
			return
		}
		if matches(n.clusterHash, in.Token) {
			errorReply(w, fail(400, "Management and cluster tokens must differ"))
			return
		}
	}
	var password account
	var err error
	if in.Password != "" {
		release, workerErr := n.passwordWorker()
		if workerErr != nil {
			errorReply(w, workerErr)
			return
		}
		defer release()
		password, err = setPassword(account{}, in.Password)
		if err != nil {
			errorReply(w, err)
			return
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	// Recheck authority after password hashing in case another admin changed it.
	currentActor, err := n.identityActorLocked(r, actor)
	if err != nil {
		errorReply(w, err)
		return
	}
	if currentActor.Role != "admin" {
		errorReply(w, fail(403, "Insufficient permissions"))
		return
	}
	if in.Token != "" && n.pushCredentialInUse(in.Token) {
		errorReply(w, fail(400, "Management and push credentials must differ"))
		return
	}
	if r.Method == "POST" && id == "" {
		if len(n.accounts) >= 64 {
			errorReply(w, fail(409, "Account capacity exceeded"))
			return
		}
		if in.Name == "" || in.Role == "" || in.Token == "" && in.Password == "" {
			errorReply(w, fail(400, "Name, role and a password or API token are required"))
			return
		}
		id = newID()
	}
	a, exists := n.accounts[id]
	if exists && dashboardManagedAccountID(id) && r.Method != "DELETE" {
		errorReply(w, fail(409, "Dashboard role credentials are managed by their enrollment; remove and enroll the dashboard again"))
		return
	}
	if !exists && !(r.Method == "POST" && !passwordAction) {
		errorReply(w, fail(404, "Account not found"))
		return
	}
	if !exists {
		a.AccountView.ID = id
	}
	if r.Method == "DELETE" {
		if n.lastAdminLocked(a) {
			errorReply(w, fail(409, "Cannot remove the last administrator"))
			return
		}
		keys := n.accountSessionKeysLocked(id)
		if err := n.identityTransactionLocked(func(e dbExec) error {
			if err := remove(e, "accounts", id); err != nil {
				return err
			}
			for _, key := range keys {
				if err := remove(e, "sessions", key); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			errorReply(w, err)
			return
		}
		delete(n.accounts, id)
		for _, key := range keys {
			delete(n.sessions, key)
		}
		reply(w, 204, nil)
		return
	}
	if passwordAction && in.Password == "" {
		errorReply(w, fail(400, "A new password is required"))
		return
	}
	if in.Role != "" && in.Role != "admin" && n.lastAdminLocked(a) {
		errorReply(w, fail(409, "Cannot demote the last administrator"))
		return
	}
	if in.Name != "" {
		a.Name = strings.TrimSpace(in.Name)
	}
	if in.Role != "" {
		a.Role = in.Role
	}
	if in.Token != "" {
		a.Hash = digest(in.Token)
	}
	if in.Password != "" {
		a.PasswordHash, a.PasswordSalt, a.PasswordVersion, a.PasswordEnabled = password.PasswordHash, password.PasswordSalt, password.PasswordVersion, true
		a.PendingTOTP, a.PendingTOTPExpires = "", 0
	}
	if passwordAction && in.ResetMFA {
		a.TOTPSecret, a.PendingTOTP = "", ""
		a.MFAEnabled = false
		a.TOTPLastStep, a.PendingTOTPExpires = 0, 0
	}
	for otherID, other := range n.accounts {
		if otherID != id && (strings.EqualFold(a.Name, other.Name) || a.Hash != "" && a.Hash == other.Hash) {
			errorReply(w, fail(409, "Account name or credential already exists"))
			return
		}
	}
	a.AuthVersion++
	if err := n.writeAccountLocked(a, true); err != nil {
		errorReply(w, err)
		return
	}
	status := 200
	if !exists {
		status = 201
	}
	reply(w, status, a.AccountView)
}

func (n *Node) lastAdminLocked(a account) bool {
	if a.Role != "admin" {
		return false
	}
	for _, other := range n.accounts {
		if other.ID != a.ID && other.Role == "admin" {
			return false
		}
	}
	return true
}
