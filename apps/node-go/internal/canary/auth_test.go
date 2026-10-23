package canary

import (
	"encoding/base32"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const accountPassword = "long sample password for tests"

func setupPassword(t *testing.T, n *Node, token, password string) {
	t.Helper()
	code, b := api(t, n, "/api/auth/password", "POST", map[string]string{"password": password}, token)
	if code != 200 {
		t.Fatal(code, string(b))
	}
}
func passwordLogin(t *testing.T, n *Node, name, password, otp string) string {
	t.Helper()
	code, b := api(t, n, "/api/auth/login", "POST", map[string]string{"name": name, "password": password, "code": otp}, "")
	var result struct {
		Token     string
		ExpiresAt int64
	}
	if code != 200 || json.Unmarshal(b, &result) != nil || result.Token == "" || result.ExpiresAt <= n.now() {
		t.Fatal(code, "Login failed or omitted session")
	}
	return result.Token
}

func TestPasswordLoginSessionsExpiryLogoutAndAccountPermissions(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	n := local(t, func(o *Options) { o.Now = clock.Load })
	code, b := api(t, n, "/api/auth/status", "GET", nil, "")
	if code != 200 || !strings.Contains(string(b), `"setupRequired":true`) {
		t.Fatal(code, string(b))
	}
	setupPassword(t, n, adminToken, accountPassword)
	for _, body := range []map[string]string{{"name": "missing", "password": accountPassword}, {"name": "admin", "password": "incorrect"}} {
		code, _ = api(t, n, "/api/auth/login", "POST", body, "")
		if code != 401 {
			t.Fatal("Invalid login accepted", code)
		}
	}
	session := passwordLogin(t, n, "ADMIN", accountPassword, "")
	code, _ = api(t, n, "/api/overview", "GET", nil, session)
	if code != 200 {
		t.Fatal("Session did not authorize", code)
	}
	code, b = api(t, n, "/api/accounts", "POST", map[string]string{"name": "Password viewer", "role": "viewer", "password": accountPassword}, session)
	var viewer AccountView
	if code != 201 || json.Unmarshal(b, &viewer) != nil || !viewer.PasswordEnabled || strings.Contains(string(b), accountPassword) || strings.Contains(string(b), "hash") {
		t.Fatal(code, string(b))
	}
	viewerSession := passwordLogin(t, n, "password viewer", accountPassword, "")
	for _, path := range []string{"/api/accounts", "/api/accounts/" + viewer.ID} {
		code, _ = api(t, n, path, "DELETE", map[string]string{}, viewerSession)
		if code != 403 {
			t.Fatal("Viewer managed accounts", code)
		}
	}
	code, _ = api(t, n, "/api/accounts/"+viewer.ID, "PUT", map[string]string{"role": "editor"}, session)
	if code != 200 {
		t.Fatal(code)
	}
	code, _ = api(t, n, "/api/overview", "GET", nil, viewerSession)
	if code != 401 {
		t.Fatal("Role change failed to revoke sessions", code)
	}
	code, _ = api(t, n, "/api/accounts/bootstrap-admin", "PUT", map[string]string{"role": "viewer"}, session)
	if code != 409 {
		t.Fatal("Last admin could be demoted", code)
	}
	code, _ = api(t, n, "/api/accounts/bootstrap-admin", "DELETE", map[string]string{}, session)
	if code != 409 {
		t.Fatal("Last admin could be deleted", code)
	}
	code, _ = api(t, n, "/api/auth/logout", "POST", map[string]string{}, session)
	if code != 204 {
		t.Fatal(code)
	}
	code, _ = api(t, n, "/api/overview", "GET", nil, session)
	if code != 401 {
		t.Fatal("Logged-out session remained usable", code)
	}
	session = passwordLogin(t, n, "admin", accountPassword, "")
	clock.Add(sessionLifetimeMs + 1)
	code, _ = api(t, n, "/api/overview", "GET", nil, session)
	if code != 401 {
		t.Fatal("Expired session remained usable", code)
	}
}

func TestAuthenticatorEnrollmentReplayAndDisable(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixMilli())
	n := local(t, func(o *Options) { o.Now = clock.Load })
	setupPassword(t, n, adminToken, accountPassword)
	oldSession := passwordLogin(t, n, "admin", accountPassword, "")
	code, b := api(t, n, "/api/auth/totp/setup", "POST", map[string]string{"currentPassword": accountPassword}, oldSession)
	var enrollment struct{ Secret, URI string }
	if code != 200 || json.Unmarshal(b, &enrollment) != nil || enrollment.Secret == "" || !strings.HasPrefix(enrollment.URI, "otpauth://totp/") {
		t.Fatal(code, "Enrollment failed")
	}
	code, _ = api(t, n, "/api/auth/totp/confirm", "POST", map[string]string{"currentPassword": accountPassword, "code": totpCode(enrollment.Secret, clock.Load()/30000)}, oldSession)
	if code != 200 {
		t.Fatal("Confirmation failed", code)
	}
	code, _ = api(t, n, "/api/overview", "GET", nil, oldSession)
	if code != 401 {
		t.Fatal("Enrollment did not revoke old session", code)
	}
	code, b = api(t, n, "/api/auth/login", "POST", map[string]string{"name": "admin", "password": accountPassword}, "")
	if code != 200 || !strings.Contains(string(b), `"mfaRequired":true`) || strings.Contains(string(b), `"token"`) {
		t.Fatal(code, string(b))
	}
	clock.Add(30000)
	otp := totpCode(enrollment.Secret, clock.Load()/30000)
	session := passwordLogin(t, n, "admin", accountPassword, otp)
	code, _ = api(t, n, "/api/auth/login", "POST", map[string]string{"name": "admin", "password": accountPassword, "code": otp}, "")
	if code != 401 {
		t.Fatal("Authenticator replay accepted", code)
	}
	code, b = api(t, n, "/api/accounts", "GET", nil, adminToken)
	if code != 200 || strings.Contains(string(b), enrollment.Secret) || strings.Contains(string(b), "passwordHash") {
		t.Fatal("Credential leaked")
	}
	clock.Add(30000)
	code, _ = api(t, n, "/api/auth/totp/disable", "POST", map[string]string{"currentPassword": accountPassword, "code": totpCode(enrollment.Secret, clock.Load()/30000)}, session)
	if code != 200 {
		t.Fatal("Disable failed", code)
	}
	code, _ = api(t, n, "/api/overview", "GET", nil, session)
	if code != 401 {
		t.Fatal("Disable did not revoke sessions", code)
	}
	_ = passwordLogin(t, n, "admin", accountPassword, "")
}

func TestPasswordAndTokenChangesSurviveBootstrapRestart(t *testing.T) {
	o := Options{NodeID: "identity-test", DBPath: filepath.Join(t.TempDir(), "identity.db"), AdminToken: adminToken, ClusterToken: clusterToken, Port: Ptr(0)}
	n, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	if err = n.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	setupPassword(t, n, adminToken, accountPassword)
	session := passwordLogin(t, n, "admin", accountPassword, "")
	newToken := "rotated-private-token-J9pL8nC6rE3qW2"
	code, _ := api(t, n, "/api/accounts/bootstrap-admin", "PUT", map[string]string{"name": "Owner", "token": newToken}, adminToken)
	if code != 200 {
		t.Fatal(code)
	}
	code, _ = api(t, n, "/api/overview", "GET", nil, adminToken)
	if code != 401 {
		t.Fatal("Old API token not revoked")
	}
	code, _ = api(t, n, "/api/overview", "GET", nil, session)
	if code != 401 {
		t.Fatal("Old session not revoked")
	}
	if err = n.Stop(); err != nil {
		t.Fatal(err)
	}
	n, err = New(o)
	if err != nil {
		t.Fatal(err)
	}
	if err = n.Start(); err != nil {
		t.Fatal(err)
	}
	code, _ = api(t, n, "/api/overview", "GET", nil, adminToken)
	if code != 401 {
		t.Fatal("Bootstrap env resurrected revoked token")
	}
	code, _ = api(t, n, "/api/overview", "GET", nil, newToken)
	if code != 200 {
		t.Fatal("Rotated API token lost", code)
	}
	session = passwordLogin(t, n, "Owner", accountPassword, "")
	code, _ = api(t, n, "/api/auth/password", "POST", map[string]string{"currentPassword": accountPassword, "password": "another long test password"}, session)
	if code != 200 {
		t.Fatal(code)
	}
	code, _ = api(t, n, "/api/overview", "GET", nil, session)
	if code != 401 {
		t.Fatal("Password change did not revoke session")
	}
	_ = passwordLogin(t, n, "Owner", "another long test password", "")
	for _, a := range n.accounts {
		if a.PasswordHash == accountPassword || a.PasswordSalt == "" || a.PasswordVersion != 1 {
			t.Fatal("Password not salted/derived")
		}
	}
}

func TestLoginRateLimitAndRFCAuthenticatorVector(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	if totpCode(secret, 1) != "287082" {
		t.Fatal("RFC6238 SHA1 vector mismatch")
	}
	if _, valid := verifyTOTP(secret, "287082", 59000, -1); !valid {
		t.Fatal("Valid TOTP rejected")
	}
	if _, valid := verifyTOTP(secret, "287082", 59000, 1); valid {
		t.Fatal("Replayed TOTP accepted")
	}
	n := local(t, nil)
	for i := 0; i < 11; i++ {
		code, _ := api(t, n, "/api/auth/login", "POST", map[string]string{"name": "missing", "password": "wrong"}, "")
		want := 401
		if i == 10 {
			want = 429
		}
		if code != want {
			t.Fatal("Login throttle", i, code)
		}
	}
}
