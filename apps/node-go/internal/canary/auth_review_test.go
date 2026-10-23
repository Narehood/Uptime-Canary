package canary

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func identityNode(t *testing.T, configure ...func(*Options)) *Node {
	t.Helper()
	o := Options{NodeID: "auth-review", DBPath: ":memory:", Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken}
	for _, apply := range configure {
		apply(&o)
	}
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	if err := n.Start(); err != nil {
		t.Fatal(err)
	}
	return n
}

func createIdentityAccount(t *testing.T, n *Node, name, role, token, password string) AccountView {
	t.Helper()
	code, b := api(t, n, "/api/accounts", "POST", map[string]string{"name": name, "role": role, "token": token, "password": password}, adminToken)
	var a AccountView
	if code != 201 || json.Unmarshal(b, &a) != nil {
		t.Fatalf("create account: %d %s", code, b)
	}
	return a
}

func identityCall(n *Node, method, path, body, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	n.ServeHTTP(w, r)
	return w
}

func TestIdentityRejectsAmbiguousRoutesAndBodies(t *testing.T) {
	n := identityNode(t)
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"POST", "/api/auth/login", " null \n", 400},
		{"POST", "/api/auth/login", `{"name":"admin","password":null}`, 400},
		{"POST", "/api/auth/login", `{"name":"admin","password":"wrong","unexpected":true}`, 400},
		{"POST", "/api/auth/login", `{"name":"admin","name":"other","password":"wrong"}`, 400},
		{"POST", "/api/auth/login", `{"Name":"admin","password":"wrong"}`, 400},
		{"POST", "/api/accounts/", `{"name":"alias","role":"viewer","token":"` + viewerToken + `"}`, 404},
		{"POST", "/api/accounts//password", `{"name":"alias","role":"viewer","password":"` + accountPassword + `"}`, 404},
		{"POST", "/api/accounts/bootstrap-admin/password", `{"password":"` + accountPassword + `","role":"viewer"}`, 400},
		{"PUT", "/api/accounts/bootstrap-admin", `{"password":"` + accountPassword + `"}`, 400},
	} {
		t.Run(tc.path+tc.body, func(t *testing.T) {
			w := identityCall(n, tc.method, tc.path, tc.body, adminToken)
			if w.Code != tc.want {
				t.Fatalf("got %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

func TestLoginLimiterCapacityKeepsKnownSocketUsable(t *testing.T) {
	n := identityNode(t)
	now := n.now()
	for i := range 4095 {
		n.loginLimits[fmt.Sprintf("192.0.%d.%d", i/256, i%256)] = loginLimit{Since: now, Count: 1}
	}
	n.loginLimits["198.51.100.1"] = loginLimit{Since: now, Count: 1}
	for _, tc := range []struct {
		ip   string
		want int
	}{{"198.51.100.1", 401}, {"203.0.113.1", 429}} {
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"name":"missing","password":"wrong"}`))
		r.RemoteAddr = tc.ip + ":12345"
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		w := httptest.NewRecorder()
		n.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("socket %s: got %d, want %d", tc.ip, w.Code, tc.want)
		}
	}
	if len(n.loginLimits) != 4096 {
		t.Fatal("unbounded limiter", len(n.loginLimits))
	}
}

type pausedIdentityBody struct {
	io.Reader
	started, release chan struct{}
	once             sync.Once
}

func (b *pausedIdentityBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.started); <-b.release })
	return b.Reader.Read(p)
}

func (b *pausedIdentityBody) Close() error { return nil }

func TestIdentityRechecksLoggedOutBearerAfterBodyRead(t *testing.T) {
	n := identityNode(t)
	setupPassword(t, n, adminToken, accountPassword)
	session := passwordLogin(t, n, "admin", accountPassword, "")
	body := &pausedIdentityBody{Reader: strings.NewReader(`{"currentPassword":"` + accountPassword + `","password":"a different long password"}`), started: make(chan struct{}), release: make(chan struct{})}
	r := httptest.NewRequest("POST", "/api/auth/password", nil)
	r.Header.Set("Authorization", "Bearer "+session)
	r.Body = body
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); n.ServeHTTP(w, r) }()
	<-body.started
	logout := identityCall(n, "POST", "/api/auth/logout", "{}", session)
	close(body.release)
	<-done
	if logout.Code != 204 || w.Code != 401 {
		t.Fatalf("logout=%d, stale password request=%d", logout.Code, w.Code)
	}
}

func failSessionDeletes(t *testing.T, n *Node) {
	t.Helper()
	_, err := n.store.db.Exec(`CREATE TRIGGER auth_review_delete_failure BEFORE DELETE ON records WHEN OLD.category='sessions' BEGIN SELECT RAISE(FAIL,'injected session delete failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSessionDeletionFailuresDoNotReportSuccess(t *testing.T) {
	for _, path := range []string{"/api/auth/logout-all", "/api/accounts/bootstrap-admin"} {
		t.Run(path, func(t *testing.T) {
			n := identityNode(t)
			setupPassword(t, n, adminToken, accountPassword)
			_ = passwordLogin(t, n, "admin", accountPassword, "")
			before := n.accounts["bootstrap-admin"]
			failSessionDeletes(t, n)
			method, body := "POST", "{}"
			if strings.HasPrefix(path, "/api/accounts/") {
				method, body = "PUT", `{"name":"Changed"}`
			}
			w := identityCall(n, method, path, body, adminToken)
			if w.Code != 503 {
				t.Fatalf("got %d; persistence failure was hidden", w.Code)
			}
			after, _, err := loadOne[account](n.store, "accounts", before.ID)
			if err != nil || after != before || n.accounts[before.ID] != before {
				t.Fatal("failed mutation was partially applied", err)
			}
			if n.fault == nil || n.ctx.Err() == nil || identityCall(n, "GET", "/api/auth/me", "", adminToken).Code != 503 {
				t.Fatal("persistence failure did not close authentication")
			}
		})
	}
}

func TestSessionsAreBoundedHashedDurableAndRejectInvalidBearers(t *testing.T) {
	var now atomic.Int64
	now.Store(1700000000000)
	db := filepath.Join(t.TempDir(), "sessions.db")
	configure := func(o *Options) { o.DBPath, o.Now = db, now.Load }
	n := identityNode(t, configure)
	setupPassword(t, n, adminToken, accountPassword)
	var tokens []string
	for range 9 {
		tokens = append(tokens, passwordLogin(t, n, "admin", accountPassword, ""))
		now.Add(1)
	}
	persisted, err := loadAll[loginSession](n.store, "sessions")
	if err != nil || len(persisted) != 8 || len(n.sessions) != 8 {
		t.Fatal("session limit did not persist", len(persisted), err)
	}
	for i, token := range tokens {
		want := 200
		if i == 0 {
			want = 401
		}
		if w := identityCall(n, "GET", "/api/auth/me", "", token); w.Code != want {
			t.Fatalf("session %d: %d, want %d", i, w.Code, want)
		}
		if _, raw := persisted[token]; raw {
			t.Fatal("raw bearer stored as session key")
		}
	}
	// Malformed credentials and stale/orphan session records must never authorize.
	current := n.accounts["bootstrap-admin"]
	for _, tc := range []struct {
		token string
		s     loginSession
	}{
		{"expired-session", loginSession{AccountID: current.ID, ExpiresAt: now.Load(), AuthVersion: current.AuthVersion}},
		{"wrong-generation", loginSession{AccountID: current.ID, ExpiresAt: now.Load() + sessionLifetimeMs, AuthVersion: current.AuthVersion + 1}},
		{"deleted-account", loginSession{AccountID: "missing", ExpiresAt: now.Load() + sessionLifetimeMs}},
	} {
		n.sessions[digest(tc.token)] = tc.s
		if err := put(n.store.db, "sessions", digest(tc.token), tc.s); err != nil {
			t.Fatal(err)
		}
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = identityNode(t, configure)
	for _, token := range []string{"", "unknown-bearer", tokens[0], "expired-session", "wrong-generation", "deleted-account"} {
		if w := identityCall(n, "GET", "/api/auth/me", "", token); w.Code != 401 {
			t.Fatal("invalid durable bearer authorized", w.Code)
		}
	}
	if w := identityCall(n, "GET", "/api/auth/me", "", tokens[8]); w.Code != 200 {
		t.Fatal("valid session lost on restart", w.Code)
	}
	expires := persisted[digest(tokens[8])].ExpiresAt
	now.Store(expires)
	if w := identityCall(n, "GET", "/api/auth/me", "", tokens[8]); w.Code != 401 {
		t.Fatal("session valid at its expiry", w.Code)
	}
	session := passwordLogin(t, n, "admin", accountPassword, "")
	persisted, err = loadAll[loginSession](n.store, "sessions")
	if err != nil || len(persisted) != 1 {
		t.Fatal("expired or invalid sessions not pruned durably", len(persisted), err)
	}
	version := n.accounts[current.ID].AuthVersion
	if w := identityCall(n, "POST", "/api/auth/logout-all", "{}", adminToken); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if n.accounts[current.ID].AuthVersion <= version || identityCall(n, "GET", "/api/auth/me", "", session).Code != 401 {
		t.Fatal("logout-all did not advance revocation generation")
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = identityNode(t, configure)
	if identityCall(n, "GET", "/api/auth/me", "", session).Code != 401 || identityCall(n, "GET", "/api/auth/me", "", adminToken).Code != 200 {
		t.Fatal("revocation or independent API token failed across restart")
	}
}

func TestSessionEvictionAndExpiryFailuresRollBackLogin(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired=%v", expired), func(t *testing.T) {
			var now atomic.Int64
			now.Store(1700000000000)
			n := identityNode(t, func(o *Options) { o.Now = now.Load })
			setupPassword(t, n, adminToken, accountPassword)
			count := 8
			if expired {
				count = 1
			}
			for range count {
				_ = passwordLogin(t, n, "admin", accountPassword, "")
			}
			if expired {
				now.Add(sessionLifetimeMs)
			}
			failSessionDeletes(t, n)
			w := identityCall(n, "POST", "/api/auth/login", `{"name":"admin","password":"`+accountPassword+`"}`, "")
			persisted, err := loadAll[loginSession](n.store, "sessions")
			if w.Code != 503 || err != nil || len(persisted) != count || len(n.sessions) != count {
				t.Fatalf("failed pruning issued session or changed state: status=%d disk=%d memory=%d error=%v", w.Code, len(persisted), len(n.sessions), err)
			}
		})
	}
}

func TestAuthenticatorEnrollmentExpiryRenewalAndPasswordReset(t *testing.T) {
	var now atomic.Int64
	now.Store(1700000000000)
	n := identityNode(t, func(o *Options) { o.Now = now.Load })
	a := createIdentityAccount(t, n, "Human", "viewer", viewerToken, accountPassword)
	session := passwordLogin(t, n, "Human", accountPassword, "")
	enroll := func() string {
		t.Helper()
		w := identityCall(n, "POST", "/api/auth/totp/setup", `{"currentPassword":"`+accountPassword+`"}`, session)
		var out struct{ Secret string }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Secret == "" {
			t.Fatal("enrollment failed", w.Code)
		}
		return out.Secret
	}
	first := enroll()
	now.Add(600000)
	w := identityCall(n, "POST", "/api/auth/totp/confirm", `{"currentPassword":"`+accountPassword+`","code":"`+totpCode(first, now.Load()/30000)+`"}`, session)
	if w.Code != 409 {
		t.Fatal("enrollment accepted at expiry", w.Code)
	}
	second := enroll()
	if second == first || n.accounts[a.ID].PendingTOTP != second || n.accounts[a.ID].MFAEnabled {
		t.Fatal("renewal did not replace pending secret without enabling MFA")
	}
	w = identityCall(n, "POST", "/api/auth/totp/confirm", `{"currentPassword":"`+accountPassword+`","code":"`+totpCode(second, now.Load()/30000)+`"}`, session)
	if w.Code != 200 {
		t.Fatal("renewed enrollment failed", w.Code)
	}
	now.Add(30000)
	session = passwordLogin(t, n, "Human", accountPassword, totpCode(second, now.Load()/30000))
	w = identityCall(n, "POST", "/api/accounts/"+a.ID+"/password", `{"password":"reset human password","resetMfa":true}`, adminToken)
	if w.Code != 200 || strings.Contains(w.Body.String(), second) || strings.Contains(w.Body.String(), "reset human password") {
		t.Fatal("password/MFA reset failed or leaked credentials", w.Code)
	}
	updated := n.accounts[a.ID]
	if updated.MFAEnabled || updated.TOTPSecret != "" || updated.PendingTOTP != "" || updated.TOTPLastStep != 0 || updated.PendingTOTPExpires != 0 || identityCall(n, "GET", "/api/auth/me", "", session).Code != 401 {
		t.Fatal("reset retained MFA or old session")
	}
	session = passwordLogin(t, n, "Human", "reset human password", "")
	if w := identityCall(n, "DELETE", "/api/accounts/"+a.ID, "", adminToken); w.Code != 204 {
		t.Fatal("account delete failed", w.Code)
	}
	for _, token := range []string{viewerToken, session} {
		if identityCall(n, "GET", "/api/auth/me", "", token).Code != 401 {
			t.Fatal("deleted account credential remained valid")
		}
	}
	if saved, err := loadAll[loginSession](n.store, "sessions"); err != nil || len(saved) != 0 {
		t.Fatal("delete retained durable sessions", err)
	}
}

func TestAdminSelfDemotionPreservesLastAdministrator(t *testing.T) {
	n := identityNode(t)
	otherToken := "other-admin-token-A1b2C3d4E5f6G7"
	a := createIdentityAccount(t, n, "Independent admin", "admin", otherToken, "")
	if w := identityCall(n, "PUT", "/api/accounts/bootstrap-admin", `{"role":"viewer"}`, adminToken); w.Code != 200 {
		t.Fatal("self-demotion with independent admin rejected", w.Code)
	}
	if w := identityCall(n, "DELETE", "/api/accounts/"+a.ID, "", adminToken); w.Code != 403 {
		t.Fatal("demoted admin retained authority", w.Code)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		body := `{"role":"viewer"}`
		if method == "DELETE" {
			body = "{}"
		}
		w := identityCall(n, method, "/api/accounts/"+a.ID, body, otherToken)
		if w.Code != 409 {
			t.Fatal("last independent admin could be removed", method, w.Code)
		}
	}
}

func TestAuthenticatorReplaySurvivesRestartAndFailedLoginRollsBackCounter(t *testing.T) {
	var now atomic.Int64
	now.Store(1700000000000)
	db := filepath.Join(t.TempDir(), "mfa.db")
	configure := func(o *Options) { o.DBPath, o.Now = db, now.Load }
	n := identityNode(t, configure)
	setupPassword(t, n, adminToken, accountPassword)
	w := identityCall(n, "POST", "/api/auth/totp/setup", `{"currentPassword":"`+accountPassword+`"}`, adminToken)
	var enrollment struct{ Secret string }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &enrollment) != nil {
		t.Fatal("setup failed", w.Code)
	}
	w = identityCall(n, "POST", "/api/auth/totp/confirm", `{"currentPassword":"`+accountPassword+`","code":"`+totpCode(enrollment.Secret, now.Load()/30000)+`"}`, adminToken)
	if w.Code != 200 {
		t.Fatal("confirm failed", w.Code)
	}
	now.Add(30000)
	otp := totpCode(enrollment.Secret, now.Load()/30000)
	_ = passwordLogin(t, n, "admin", accountPassword, otp)
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = identityNode(t, configure)
	w = identityCall(n, "POST", "/api/auth/login", `{"name":"admin","password":"`+accountPassword+`","code":"`+otp+`"}`, "")
	if w.Code != 401 {
		t.Fatal("restart allowed authenticator replay", w.Code)
	}
	now.Add(30000)
	otp = totpCode(enrollment.Secret, now.Load()/30000)
	before := n.accounts["bootstrap-admin"]
	_, err := n.store.db.Exec(`CREATE TRIGGER auth_review_insert_failure BEFORE INSERT ON records WHEN NEW.category='sessions' BEGIN SELECT RAISE(FAIL,'injected session insert failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	w = identityCall(n, "POST", "/api/auth/login", `{"name":"admin","password":"`+accountPassword+`","code":"`+otp+`"}`, "")
	after, _, err := loadOne[account](n.store, "accounts", before.ID)
	if w.Code != 503 || err != nil || after != before || n.accounts[before.ID] != before {
		t.Fatal("failed session creation consumed authenticator code", w.Code, err)
	}
	if _, err := n.store.db.Exec(`DROP TRIGGER auth_review_insert_failure`); err != nil {
		t.Fatal(err)
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	n = identityNode(t, configure)
	_ = passwordLogin(t, n, "admin", accountPassword, otp)
}

func TestPasswordChangesClearPendingAuthenticatorEnrollment(t *testing.T) {
	for _, adminReset := range []bool{false, true} {
		t.Run(fmt.Sprintf("adminReset=%v", adminReset), func(t *testing.T) {
			n := identityNode(t)
			a := createIdentityAccount(t, n, "Pending human", "viewer", viewerToken, accountPassword)
			w := identityCall(n, "POST", "/api/auth/totp/setup", `{"currentPassword":"`+accountPassword+`"}`, viewerToken)
			if w.Code != 200 || n.accounts[a.ID].PendingTOTP == "" {
				t.Fatal("setup failed", w.Code)
			}
			path, token := "/api/auth/password", viewerToken
			body := `{"currentPassword":"` + accountPassword + `","password":"changed human password"}`
			if adminReset {
				path, token = "/api/accounts/"+a.ID+"/password", adminToken
				body = `{"password":"changed human password"}`
			}
			w = identityCall(n, "POST", path, body, token)
			updated := n.accounts[a.ID]
			if w.Code != 200 || updated.PendingTOTP != "" || updated.PendingTOTPExpires != 0 {
				t.Fatal("password mutation retained pending enrollment", w.Code)
			}
		})
	}
}

func TestAccountDeleteSessionFailureRollsBackAccount(t *testing.T) {
	n := identityNode(t)
	a := createIdentityAccount(t, n, "Delete human", "viewer", viewerToken, accountPassword)
	_ = passwordLogin(t, n, a.Name, accountPassword, "")
	failSessionDeletes(t, n)
	w := identityCall(n, "DELETE", "/api/accounts/"+a.ID, "{}", adminToken)
	saved, exists, err := loadOne[account](n.store, "accounts", a.ID)
	if w.Code != 503 || !exists || err != nil || saved != n.accounts[a.ID] {
		t.Fatal("failed deletion partially removed account", w.Code, err)
	}
}

func TestAuthenticationStateStaysOutOfPeerSnapshots(t *testing.T) {
	n := identityNode(t)
	setupPassword(t, n, adminToken, accountPassword)
	session := passwordLogin(t, n, "admin", accountPassword, "")
	w := identityCall(n, "POST", "/api/auth/totp/setup", `{"currentPassword":"`+accountPassword+`"}`, adminToken)
	if w.Code != 200 {
		t.Fatal("setup failed", w.Code)
	}
	n.mu.Lock()
	page, err := n.snapshotPage("", "")
	a := n.accounts["bootstrap-admin"]
	n.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"accounts"`, `"sessions"`, adminToken, accountPassword, a.Hash, a.PasswordHash, a.PasswordSalt, a.PendingTOTP, session, digest(session)} {
		if value == "" || strings.Contains(string(b), value) {
			t.Fatal("authentication field missing from fixture or leaked to peer snapshot")
		}
	}
}

func TestPasswordWorkerBoundDoesNotBlockLogout(t *testing.T) {
	n := identityNode(t)
	setupPassword(t, n, adminToken, accountPassword)
	session := passwordLogin(t, n, "admin", accountPassword, "")
	if cap(n.passwordWorkers) != 2 {
		t.Fatal("unexpected password work bound")
	}
	for range 2 {
		n.passwordWorkers <- struct{}{}
	}
	defer func() { <-n.passwordWorkers; <-n.passwordWorkers }()
	for _, tc := range []struct{ path, body string }{
		{"/api/auth/login", `{"name":"admin","password":"` + accountPassword + `"}`},
		{"/api/auth/password", `{"currentPassword":"` + accountPassword + `","password":"new valid password"}`},
		{"/api/accounts", `{"name":"busy","role":"viewer","password":"` + accountPassword + `"}`},
	} {
		if w := identityCall(n, "POST", tc.path, tc.body, adminToken); w.Code != 429 {
			t.Fatal("password work exceeded concurrency limit", tc.path, w.Code)
		}
	}
	if w := identityCall(n, "GET", "/api/overview", "", session); w.Code != 200 {
		t.Fatal("monitor API unavailable while password workers busy", w.Code)
	}
	if w := identityCall(n, "POST", "/api/auth/logout", "{}", session); w.Code != 204 {
		t.Fatal("busy KDF blocked session revocation", w.Code)
	}
}

func TestLoginSocketRateWindowIgnoresForwardedHeaders(t *testing.T) {
	var now atomic.Int64
	now.Store(1700000000000)
	n := identityNode(t, func(o *Options) { o.Now = now.Load })
	for i := range 11 {
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"name":"missing","password":"wrong"}`))
		r.RemoteAddr = fmt.Sprintf("198.51.100.4:%d", 10000+i)
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
		w := httptest.NewRecorder()
		n.ServeHTTP(w, r)
		want := 401
		if i == 10 {
			want = 429
			if w.Header().Get("Retry-After") == "" {
				t.Fatal("rate response omitted retry guidance")
			}
		}
		if w.Code != want {
			t.Fatal("socket IP throttle changed with forwarded header or port", i, w.Code)
		}
	}
	now.Add(time.Minute.Milliseconds())
	r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"name":"missing","password":"wrong"}`))
	r.RemoteAddr = "198.51.100.4:9000"
	w := httptest.NewRecorder()
	n.ServeHTTP(w, r)
	if w.Code != 401 || len(n.loginLimits) != 1 || n.loginLimits["198.51.100.4"].Count != 1 {
		t.Fatal("socket IP window failed to reset at one minute", w.Code)
	}
}
