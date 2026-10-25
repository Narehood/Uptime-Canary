package canary

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
)

type identityInput struct {
	Name            string `json:"name"`
	Role            string `json:"role"`
	Token           string `json:"token"`
	Password        string `json:"password"`
	CurrentPassword string `json:"currentPassword"`
	Code            string `json:"code"`
	ResetMFA        bool   `json:"resetMfa"`
}

// Each route accepts only its own exact, non-null fields. In particular, a
// password reset cannot also change privileges, and PUT cannot reset a password.
func identityFields(path, method string) ([]string, bool) {
	if path == "/api/accounts" || strings.HasPrefix(path, "/api/accounts/") {
		id, password, ok := accountRoute(path, method)
		if !ok {
			return nil, false
		}
		if password {
			return []string{"password", "resetMfa"}, true
		}
		if method == "POST" && id == "" {
			return []string{"name", "role", "token", "password"}, true
		}
		if method == "PUT" {
			return []string{"name", "role", "token"}, true
		}
		return nil, true
	}
	if method == "GET" && (path == "/api/auth/status" || path == "/api/auth/me") {
		return nil, true
	}
	if method != "POST" {
		return nil, false
	}
	switch path {
	case "/api/auth/login":
		return []string{"name", "password", "code"}, true
	case "/api/auth/password":
		return []string{"password", "currentPassword", "code"}, true
	case "/api/auth/totp/setup":
		return []string{"currentPassword"}, true
	case "/api/auth/totp/confirm", "/api/auth/totp/disable":
		return []string{"currentPassword", "code"}, true
	case "/api/auth/logout", "/api/auth/logout-all":
		return nil, true
	}
	return nil, false
}

func parseIdentity(b []byte, fields []string) (identityInput, error) {
	var in identityInput
	bad := fail(400, "Invalid identity request")
	if len(bytes.TrimSpace(b)) == 0 {
		return in, nil
	}
	d := json.NewDecoder(bytes.NewReader(b))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return in, bad
	}
	targets := map[string]any{"name": &in.Name, "role": &in.Role, "token": &in.Token, "password": &in.Password, "currentPassword": &in.CurrentPassword, "code": &in.Code, "resetMfa": &in.ResetMFA}
	seen := map[string]bool{}
	for d.More() {
		key, err := d.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] || !slices.Contains(fields, name) {
			return in, bad
		}
		seen[name] = true
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, targets[name]) != nil {
			return in, bad
		}
	}
	if _, err := d.Token(); err != nil {
		return in, bad
	}
	if d.Decode(new(any)) != io.EOF {
		return in, bad
	}
	return in, nil
}

func (n *Node) passwordWorker() (func(), error) {
	select {
	case n.passwordWorkers <- struct{}{}:
		return func() { <-n.passwordWorkers }, nil
	default:
		return nil, fail(429, "Authentication is busy; retry shortly")
	}
}

// Recheck both the account generation and the actual bearer after body I/O or
// expensive hashing. Single-session logout does not change the generation.
func (n *Node) identityActorLocked(r *http.Request, actor account) (account, error) {
	if n.stopped || n.fault != nil {
		return account{}, fail(503, "Runtime unavailable")
	}
	current, err := n.authenticate(r)
	if err != nil {
		return account{}, err
	}
	if current.ID != actor.ID || current.AuthVersion != actor.AuthVersion {
		return account{}, fail(409, "Account credentials changed; sign in again")
	}
	return current, nil
}

// Identity endpoints keep expensive password operations outside n.mu. Tokens
// returned by login are opaque, expire absolutely, and are persisted as hashes.
// Bearer sessions work with a separately hosted dashboard without third-party
// cookies or CSRF-prone ambient credentials.
func (n *Node) serveIdentity(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	fields, recognized := identityFields(path, r.Method)
	if path == "/api/auth/status" && r.Method == "GET" {
		n.mu.Lock()
		configured := false
		for _, a := range n.accounts {
			configured = configured || a.PasswordHash != ""
		}
		available := !n.stopped && n.fault == nil
		n.mu.Unlock()
		if !available {
			errorReply(w, fail(503, "Runtime unavailable"))
			return
		}
		reply(w, 200, map[string]any{"passwordLogin": true, "passwordConfigured": configured, "setupRequired": !configured, "tokenLogin": true, "mfaSupported": true, "dashboardEnrollment": true})
		return
	}
	publicLogin := path == "/api/auth/login" && r.Method == "POST"
	n.mu.Lock()
	var actor account
	var err error
	if !publicLogin {
		actor, err = n.authenticate(r)
	}
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
	if strings.HasPrefix(path, "/api/accounts") && actor.Role != "admin" {
		errorReply(w, fail(403, "Insufficient permissions"))
		return
	}
	if !recognized {
		errorReply(w, fail(404, "Not found"))
		return
	}
	var in identityInput
	if r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE" {
		b, readErr := readBounded(r.Body, 8192)
		if readErr != nil {
			errorReply(w, readErr)
			return
		}
		in, err = parseIdentity(b, fields)
		if err != nil {
			errorReply(w, err)
			return
		}
	}
	if publicLogin {
		n.login(w, r, in)
		return
	}
	if strings.HasPrefix(path, "/api/accounts") {
		n.accountRequest(w, r, actor, in)
		return
	}
	if path == "/api/auth/me" && r.Method == "GET" {
		n.mu.Lock()
		current, err := n.identityActorLocked(r, actor)
		n.mu.Unlock()
		if err != nil {
			errorReply(w, err)
			return
		}
		reply(w, 200, current.AccountView)
		return
	}
	if (path == "/api/auth/logout" || path == "/api/auth/logout-all") && r.Method == "POST" {
		n.mu.Lock()
		defer n.mu.Unlock()
		current, err := n.identityActorLocked(r, actor)
		if err != nil {
			errorReply(w, err)
			return
		}
		if path == "/api/auth/logout-all" {
			current.AuthVersion++
			err = n.writeAccountLocked(current, true)
		} else {
			key := digest(bearer(r))
			if _, exists := n.sessions[key]; exists {
				err = n.identityTransactionLocked(func(e dbExec) error { return remove(e, "sessions", key) })
				if err == nil {
					delete(n.sessions, key)
				}
			}
		}
		if err != nil {
			errorReply(w, err)
			return
		}
		reply(w, 204, nil)
		return
	}
	release, err := n.passwordWorker()
	if err != nil {
		errorReply(w, err)
		return
	}
	defer release()
	if actor.PasswordHash != "" && !verifyPassword(actor, in.CurrentPassword) {
		errorReply(w, fail(401, "Current password is incorrect"))
		return
	}
	if path != "/api/auth/password" && actor.PasswordHash == "" {
		errorReply(w, fail(409, "Set an account password first"))
		return
	}
	updated := actor
	var secret, uri string
	if path == "/api/auth/password" {
		updated, err = setPassword(actor, in.Password)
	}
	if path == "/api/auth/totp/setup" {
		secret, uri, err = newTOTP(actor.Name)
	}
	if err != nil {
		errorReply(w, err)
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	current, err := n.identityActorLocked(r, actor)
	if err != nil {
		errorReply(w, err)
		return
	}
	if path != "/api/auth/totp/setup" && current.TOTPSecret != "" {
		step, valid := verifyTOTP(current.TOTPSecret, in.Code, n.now(), current.TOTPLastStep)
		if !valid {
			errorReply(w, fail(401, "A fresh authenticator code is required"))
			return
		}
		current.TOTPLastStep = step
	}
	switch path {
	case "/api/auth/password":
		current.PasswordHash, current.PasswordSalt, current.PasswordVersion = updated.PasswordHash, updated.PasswordSalt, updated.PasswordVersion
		current.PasswordEnabled = true
		current.PendingTOTP, current.PendingTOTPExpires = "", 0
		current.AuthVersion++
	case "/api/auth/totp/setup":
		if current.TOTPSecret != "" {
			errorReply(w, fail(409, "Two-factor authentication is already enabled"))
			return
		}
		current.PendingTOTP, current.PendingTOTPExpires = secret, n.now()+600000
	case "/api/auth/totp/confirm":
		if current.PendingTOTP == "" || current.PendingTOTPExpires <= n.now() {
			errorReply(w, fail(409, "Authenticator enrollment expired; start again"))
			return
		}
		step, valid := verifyTOTP(current.PendingTOTP, in.Code, n.now(), -1)
		if !valid {
			errorReply(w, fail(401, "Authenticator code is incorrect"))
			return
		}
		current.TOTPSecret, current.TOTPLastStep, current.MFAEnabled = current.PendingTOTP, step, true
		current.PendingTOTP, current.PendingTOTPExpires = "", 0
		current.AuthVersion++
	case "/api/auth/totp/disable":
		if current.TOTPSecret == "" {
			errorReply(w, fail(409, "Two-factor authentication is not enabled"))
			return
		}
		current.TOTPSecret, current.PendingTOTP = "", ""
		current.TOTPLastStep, current.PendingTOTPExpires, current.MFAEnabled = 0, 0, false
		current.AuthVersion++
	}
	if err := n.writeAccountLocked(current, current.AuthVersion != actor.AuthVersion); err != nil {
		errorReply(w, err)
		return
	}
	if path == "/api/auth/totp/setup" {
		reply(w, 200, map[string]any{"secret": secret, "uri": uri, "expiresAt": current.PendingTOTPExpires})
		return
	}
	reply(w, 200, current.AccountView)
}

func (n *Node) login(w http.ResponseWriter, r *http.Request, in identityInput) {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	n.mu.Lock()
	now := n.now()
	for key, limit := range n.loginLimits {
		if now-limit.Since >= 60000 {
			delete(n.loginLimits, key)
		}
	}
	limit, known := n.loginLimits[ip]
	if limit.Count >= 10 || !known && len(n.loginLimits) >= 4096 {
		n.mu.Unlock()
		w.Header().Set("Retry-After", "60")
		errorReply(w, fail(429, "Too many sign-in attempts; retry in one minute"))
		return
	}
	if limit.Count == 0 {
		limit.Since = now
	}
	limit.Count++
	n.loginLimits[ip] = limit
	if n.stopped || n.fault != nil {
		n.mu.Unlock()
		errorReply(w, fail(503, "Runtime unavailable"))
		return
	}
	var found account
	for _, a := range n.accounts {
		if strings.EqualFold(a.Name, strings.TrimSpace(in.Name)) {
			found = a
			break
		}
	}
	n.mu.Unlock()
	if len(in.Name) > 128 || len(in.Password) > 512 || len(in.Code) > 6 {
		errorReply(w, fail(401, "Invalid username, password or authenticator code"))
		return
	}
	release, err := n.passwordWorker()
	if err != nil {
		errorReply(w, err)
		return
	}
	defer release()
	valid := verifyPassword(found, in.Password)
	if !valid {
		errorReply(w, fail(401, "Invalid username, password or authenticator code"))
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.stopped || n.fault != nil {
		errorReply(w, fail(503, "Runtime unavailable"))
		return
	}
	current, exists := n.accounts[found.ID]
	if !exists || current.PasswordHash != found.PasswordHash || current.AuthVersion != found.AuthVersion {
		errorReply(w, fail(401, "Invalid username, password or authenticator code"))
		return
	}
	if current.TOTPSecret != "" {
		if in.Code == "" {
			reply(w, 200, map[string]bool{"mfaRequired": true})
			return
		}
		step, ok := verifyTOTP(current.TOTPSecret, in.Code, n.now(), current.TOTPLastStep)
		if !ok {
			errorReply(w, fail(401, "Invalid username, password or authenticator code"))
			return
		}
		current.TOTPLastStep = step
	}
	token, err := randomCredential(32)
	if err != nil {
		errorReply(w, err)
		return
	}
	removed := []string{}
	owned := []string{}
	for key, s := range n.sessions {
		a, exists := n.accounts[s.AccountID]
		if s.ExpiresAt <= n.now() || !exists || a.AuthVersion != s.AuthVersion {
			removed = append(removed, key)
			continue
		}
		if s.AccountID == current.ID {
			owned = append(owned, key)
		}
	}
	slices.SortFunc(owned, func(a, b string) int {
		if n.sessions[a].CreatedAt < n.sessions[b].CreatedAt {
			return -1
		}
		if n.sessions[a].CreatedAt > n.sessions[b].CreatedAt {
			return 1
		}
		return stringsCompare(a, b)
	})
	for len(owned) >= 8 {
		removed = append(removed, owned[0])
		owned = owned[1:]
	}
	session := loginSession{AccountID: current.ID, CreatedAt: n.now(), ExpiresAt: n.now() + sessionLifetimeMs, AuthVersion: current.AuthVersion}
	key := digest(token)
	if err := n.identityTransactionLocked(func(e dbExec) error {
		if current.TOTPSecret != "" {
			if err := put(e, "accounts", current.ID, current); err != nil {
				return err
			}
		}
		for _, old := range removed {
			if err := remove(e, "sessions", old); err != nil {
				return err
			}
		}
		return put(e, "sessions", key, session)
	}); err != nil {
		errorReply(w, err)
		return
	}
	for _, old := range removed {
		delete(n.sessions, old)
	}
	n.accounts[current.ID] = current
	n.sessions[key] = session
	reply(w, 200, map[string]any{"token": token, "expiresAt": session.ExpiresAt, "user": current.AccountView})
}

// Account generations, replay counters, and session deletions commit together.
// Memory changes only after commit; every persistence error faults the runtime.
func (n *Node) identityTransactionLocked(write func(dbExec) error) error {
	if n.stopped || n.fault != nil {
		return fail(503, "Runtime unavailable")
	}
	tx, err := n.store.db.Begin()
	if err == nil {
		defer tx.Rollback()
		err = write(tx)
		if err == nil {
			err = tx.Commit()
		}
	}
	if err != nil {
		n.fault = fail(503, "Persistence unavailable")
		n.cancel()
		return n.fault
	}
	return nil
}

func (n *Node) accountSessionKeysLocked(accountID string) []string {
	keys := []string{}
	for key, s := range n.sessions {
		if s.AccountID == accountID {
			keys = append(keys, key)
		}
	}
	return keys
}

func (n *Node) writeAccountLocked(a account, revoke bool) error {
	var keys []string
	if revoke {
		keys = n.accountSessionKeysLocked(a.ID)
	}
	if err := n.identityTransactionLocked(func(e dbExec) error {
		if err := put(e, "accounts", a.ID, a); err != nil {
			return err
		}
		for _, key := range keys {
			if err := remove(e, "sessions", key); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	n.accounts[a.ID] = a
	for _, key := range keys {
		delete(n.sessions, key)
	}
	return nil
}
