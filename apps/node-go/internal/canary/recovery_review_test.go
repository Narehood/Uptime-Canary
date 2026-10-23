package canary

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

const recoveryReviewPassword = "review replacement password"
const recoveryReviewToken = "review-target-api-credential-1234567890"
const recoveryReviewNow int64 = 1792670400000

// These fixtures never start a listener, monitor, peer client, or external service.
func recoveryReviewNode(t *testing.T, path string) *Node {
	t.Helper()
	// Each fixture models an independent node, including when two database files
	// share a directory. Keep their external origin locks independent as well.
	n, err := New(Options{NodeID: "recovery-review", DBPath: path, IdentityDirectory: path + ".origin", Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken, Now: func() int64 { return recoveryReviewNow }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Stop() })
	return n
}

func recoveryReviewFixture(t *testing.T, path, role string) (*Node, account) {
	t.Helper()
	n := recoveryReviewNode(t, path)
	a, err := setPassword(account{AccountView: AccountView{ID: "review-target", Name: "Reviewer", Role: role}, Hash: digest(recoveryReviewToken), AuthVersion: 17}, "review original password")
	if err != nil {
		t.Fatal(err)
	}
	a.MFAEnabled, a.TOTPSecret, a.TOTPLastStep = true, "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP", 77
	a.PendingTOTP, a.PendingTOTPExpires = "review-pending-secret", recoveryReviewNow+600000
	n.accounts[a.ID] = a
	if err := put(n.store.db, "accounts", a.ID, a); err != nil {
		t.Fatal(err)
	}
	for token, session := range map[string]loginSession{
		"review-target-session":  {AccountID: a.ID, AuthVersion: a.AuthVersion, ExpiresAt: recoveryReviewNow + 600000},
		"review-expired-session": {AccountID: a.ID, AuthVersion: a.AuthVersion, ExpiresAt: recoveryReviewNow - 1},
		"review-stale-session":   {AccountID: a.ID, AuthVersion: a.AuthVersion - 1, ExpiresAt: recoveryReviewNow + 600000},
		"review-other-session":   {AccountID: "bootstrap-admin", ExpiresAt: recoveryReviewNow + 600000},
	} {
		if err := put(n.store.db, "sessions", digest(token), session); err != nil {
			t.Fatal(err)
		}
	}
	if err := put(n.store.db, "review-data", "preserve", map[string]string{"value": "configuration sentinel"}); err != nil {
		t.Fatal(err)
	}
	if _, err := n.store.db.Exec(`INSERT INTO history(monitor_id,checked_at,payload) VALUES('review-monitor',123,'{"message":"raw history sentinel"}')`); err != nil {
		t.Fatal(err)
	}
	return n, a
}

// Compare every application table and schema definition. In the success case,
// exclude only the target account and its sessions, which are checked separately.
func recoveryReviewSnapshot(t *testing.T, db *sql.DB, excludeAccount string) map[string][]string {
	t.Helper()
	rows, err := db.Query(`SELECT type,name,tbl_name,sql FROM sqlite_schema ORDER BY type,name`)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	var tables []string
	for rows.Next() {
		var typ, name, table string
		var definition sql.NullString
		if err := rows.Scan(&typ, &name, &table, &definition); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal([]any{typ, name, table, definition})
		out["schema"] = append(out["schema"], string(encoded))
		if typ == "table" {
			tables = append(tables, name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	for _, table := range tables {
		rows, err := db.Query(`SELECT * FROM "` + strings.ReplaceAll(table, `"`, `""`) + `"`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			if table == "records" && excludeAccount != "" {
				category, id, payload := fmt.Sprint(values[0]), fmt.Sprint(values[1]), fmt.Sprint(values[2])
				var session loginSession
				if category == "accounts" && id == excludeAccount || category == "sessions" && json.Unmarshal([]byte(payload), &session) == nil && session.AccountID == excludeAccount {
					continue
				}
			}
			encoded, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			out[table] = append(out[table], string(encoded))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		_ = rows.Close()
		sort.Strings(out[table])
	}
	return out
}

func recoveryReviewOpen(t *testing.T, path string) *store {
	t.Helper()
	s, err := openStore(path)
	if err != nil {
		t.Fatal("recovery retained database ownership", err)
	}
	t.Cleanup(func() { _ = s.close() })
	return s
}

func TestRecoveryReviewAuthenticationAfterNew(t *testing.T) {
	for _, tc := range []struct {
		name, role    string
		reset, revoke bool
	}{
		{"retain", "admin", false, false},
		{"reset-mfa", "editor", true, false},
		{"revoke-token", "viewer", false, true},
		{"reset-and-revoke", "admin", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recovery.db")
			n, old := recoveryReviewFixture(t, path, tc.role)
			before := recoveryReviewSnapshot(t, n.store.db, old.ID)
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			view, err := RecoverAccount(context.Background(), RecoveryOptions{Database: path, AccountName: "rEvIeWeR", Password: recoveryReviewPassword, ResetMFA: tc.reset, RevokeToken: tc.revoke})
			if err != nil {
				t.Fatal(err)
			}
			if view.ID != old.ID || view.Role != old.Role || !view.PasswordEnabled || view.MFAEnabled == tc.reset {
				t.Fatal("recovery changed account identity, role, or flags")
			}
			s := recoveryReviewOpen(t, path)
			if !reflect.DeepEqual(before, recoveryReviewSnapshot(t, s.db, old.ID)) {
				t.Fatal("recovery changed unrelated accounts, configuration, schema, or raw history")
			}
			a, ok, err := loadOne[account](s, "accounts", old.ID)
			if err != nil || !ok || a.AuthVersion != old.AuthVersion+1 || a.PendingTOTP != "" || a.PendingTOTPExpires != 0 || a.PasswordSalt == old.PasswordSalt || a.PasswordVersion != 1 {
				t.Fatal("recovery did not persist fresh credentials and clear pending enrollment", err)
			}
			if tc.revoke && a.Hash != "" || !tc.revoke && a.Hash != old.Hash {
				t.Fatal("API hash revocation did not follow the explicit option")
			}
			if tc.reset {
				if a.TOTPSecret != "" || a.TOTPLastStep != 0 {
					t.Fatal("MFA reset retained replay state")
				}
			} else if a.TOTPSecret != old.TOTPSecret || a.TOTPLastStep != old.TOTPLastStep {
				t.Fatal("default recovery changed MFA secret or replay counter")
			}
			sessions, err := loadAll[loginSession](s, "sessions")
			if err != nil || len(sessions) != 1 || sessions[digest("review-other-session")].AccountID != "bootstrap-admin" {
				t.Fatal("target sessions were not completely and exclusively removed", err)
			}
			encoded, _ := json.Marshal(view)
			for _, secret := range []string{recoveryReviewPassword, recoveryReviewToken, old.Hash, a.PasswordHash, a.PasswordSalt, old.TOTPSecret, old.PendingTOTP} {
				if strings.Contains(string(encoded), secret) {
					t.Fatal("CLI result exposes credential material")
				}
			}
			if err := s.close(); err != nil {
				t.Fatal(err)
			}
			n = recoveryReviewNode(t, path)
			for _, token := range []string{"review-target-session", "review-expired-session", "review-stale-session"} {
				if identityCall(n, "GET", "/api/auth/me", "", token).Code != 401 {
					t.Fatal("old password session authorized after restart")
				}
			}
			for _, token := range []string{adminToken, "review-other-session"} {
				if identityCall(n, "GET", "/api/auth/me", "", token).Code != 200 {
					t.Fatal("another account lost access after recovery")
				}
			}
			wantToken := 200
			if tc.revoke {
				wantToken = 401
			}
			if got := identityCall(n, "GET", "/api/auth/me", "", recoveryReviewToken).Code; got != wantToken {
				t.Fatalf("API token after New: %d, want %d", got, wantToken)
			}
			oldBody, _ := json.Marshal(map[string]string{"name": old.Name, "password": "review original password"})
			if identityCall(n, "POST", "/api/auth/login", string(oldBody), "").Code != 401 {
				t.Fatal("old password still works after restart")
			}
			otp := ""
			if !tc.reset {
				body, _ := json.Marshal(map[string]string{"name": old.Name, "password": recoveryReviewPassword})
				w := identityCall(n, "POST", "/api/auth/login", string(body), "")
				var challenge map[string]any
				if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &challenge) != nil || challenge["mfaRequired"] != true || challenge["token"] != nil {
					t.Fatal("retained MFA did not challenge the replacement password")
				}
				otp = totpCode(old.TOTPSecret, recoveryReviewNow/30000)
			}
			body, _ := json.Marshal(map[string]string{"name": old.Name, "password": recoveryReviewPassword, "code": otp})
			w := identityCall(n, "POST", "/api/auth/login", string(body), "")
			var login struct{ Token string }
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &login) != nil || login.Token == "" {
				t.Fatal("replacement password login failed after restart")
			}
			if identityCall(n, "GET", "/api/auth/me", "", login.Token).Code != 200 {
				t.Fatal("replacement password session did not authorize")
			}
		})
	}
}

func TestRecoveryReviewFailuresLeaveAllDataIntact(t *testing.T) {
	for _, name := range []string{"cancelled", "unknown", "ambiguous", "malformed-account", "malformed-session", "account-write-failure"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recovery.db")
			n, old := recoveryReviewFixture(t, path, "admin")
			ctx := context.Background()
			in := RecoveryOptions{Database: path, AccountName: old.Name, Password: recoveryReviewPassword, ResetMFA: true, RevokeToken: true}
			var err error
			switch name {
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "unknown":
				in.AccountName = "absent-account"
			case "ambiguous":
				duplicate := old
				duplicate.ID, duplicate.Name = "duplicate-target", strings.ToUpper(old.Name)
				err = put(n.store.db, "accounts", duplicate.ID, duplicate)
			case "malformed-account", "malformed-session":
				category := "accounts"
				if name == "malformed-session" {
					category = "sessions"
				}
				_, err = n.store.db.Exec(`INSERT INTO records(category,id,payload) VALUES(?, 'malformed-review-row', '{invalid')`, category)
			case "account-write-failure":
				_, err = n.store.db.Exec(`CREATE TRIGGER recovery_review_write_failure BEFORE UPDATE ON records WHEN OLD.category='accounts' BEGIN SELECT RAISE(ABORT,'review-pending-secret'); END`)
			}
			if err != nil {
				t.Fatal(err)
			}
			before := recoveryReviewSnapshot(t, n.store.db, "")
			if err := n.Stop(); err != nil {
				t.Fatal(err)
			}
			_, err = RecoverAccount(ctx, in)
			if err == nil {
				t.Fatal("recovery acknowledged a rejected operation")
			}
			if strings.Contains(err.Error(), recoveryReviewPassword) || strings.Contains(err.Error(), old.PendingTOTP) {
				t.Fatal("recovery error exposed a secret")
			}
			s := recoveryReviewOpen(t, path)
			if !reflect.DeepEqual(before, recoveryReviewSnapshot(t, s.db, "")) {
				t.Fatal("failed recovery changed durable data or schema")
			}
		})
	}
}

func TestRecoveryReviewProcessHelper(t *testing.T) {
	path := os.Getenv("CANARY_RECOVERY_REVIEW_LOCK_PATH")
	if path == "" {
		return
	}
	_, err := RecoverAccount(context.Background(), RecoveryOptions{Database: path, AccountName: "Reviewer", Password: recoveryReviewPassword})
	if err == nil || !strings.Contains(err.Error(), "exclusively") {
		t.Fatal("child process did not reject the node's OS ownership lock", err)
	}
	fmt.Println("RECOVERY_REVIEW_LOCK_REJECTED")
}

func TestRecoveryReviewOwnershipAcrossProcessAliases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "node #percent% + café.db")
	n, old := recoveryReviewFixture(t, path, "admin")
	before := recoveryReviewSnapshot(t, n.store.db, "")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, path)
	if err != nil {
		t.Fatal(err)
	}
	aliases := map[string]string{"absolute-special-characters": path, "relative": relative, "dot-segment": filepath.Dir(path) + string(os.PathSeparator) + "." + string(os.PathSeparator) + filepath.Base(path)}
	fileAlias := filepath.Join(dir, "file-alias.db")
	if err := os.Symlink(path, fileAlias); err == nil {
		aliases["file-symlink"] = fileAlias
	} else {
		t.Log("file symlink unavailable:", err)
	}
	dirAlias := filepath.Join(t.TempDir(), "directory-alias")
	if err := os.Symlink(dir, dirAlias); err == nil {
		aliases["directory-symlink"] = filepath.Join(dirAlias, filepath.Base(path))
	} else {
		t.Log("directory symlink unavailable:", err)
	}
	for name, alias := range aliases {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRecoveryReviewProcessHelper$", "-test.timeout=10s")
			cmd.Env = append(os.Environ(), "CANARY_RECOVERY_REVIEW_LOCK_PATH="+alias)
			output, err := cmd.CombinedOutput()
			if err != nil || !bytes.Contains(output, []byte("RECOVERY_REVIEW_LOCK_REJECTED")) {
				t.Fatalf("cross-process recovery lock: %v\n%s", err, output)
			}
		})
	}
	if !reflect.DeepEqual(before, recoveryReviewSnapshot(t, n.store.db, "")) {
		t.Fatal("rejected cross-process recovery changed the database")
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverAccount(context.Background(), RecoveryOptions{Database: relative, AccountName: old.Name, Password: recoveryReviewPassword}); err != nil {
		t.Fatal("relative path or escaped read-only URI failed after ownership was released", err)
	}
}

type recoveryReviewNoNetwork struct{ t *testing.T }

func (d recoveryReviewNoNetwork) Do(*http.Request) (*http.Response, error) {
	d.t.Fatal("offline recovery called an HTTP client")
	return nil, fmt.Errorf("unexpected HTTP call")
}

func TestRecoveryReviewCLIRejectsMalformedFilesAndFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.db")
	n, old := recoveryReviewFixture(t, path, "admin")
	before := recoveryReviewSnapshot(t, n.store.db, "")
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "password.txt")
	for _, tc := range []struct {
		name  string
		value []byte
	}{
		{"empty", nil}, {"eleven-runes", []byte("12345678901")}, {"129-runes", []byte(strings.Repeat("x", 129))},
		{"515-bytes", []byte(strings.Repeat("x", 515))}, {"invalid-utf8", append([]byte("a valid password"), 0xff)},
		{"nul", []byte("a valid\x00 password")}, {"embedded-lf", []byte("a valid\n password")},
		{"embedded-cr", []byte("a valid\r password")}, {"double-lf", []byte("a valid password\n\n")},
		{"double-crlf", []byte("a valid password\r\n\r\n")}, {"bare-cr", []byte("a valid password\r")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(file, tc.value, 0600); err != nil {
				t.Fatal(err)
			}
			_, err := RunCLI(context.Background(), []string{"recover-account", path, old.Name, file}, func(string) string { t.Fatal("offline recovery read remote configuration"); return "" }, recoveryReviewNoNetwork{t})
			if err == nil {
				t.Fatal("malformed password file was accepted")
			}
			if len(tc.value) >= 12 && bytes.Contains([]byte(err.Error()), tc.value) {
				t.Fatal("error echoed the password bytes")
			}
		})
	}
	if err := os.WriteFile(file, []byte(recoveryReviewPassword), 0600); err != nil {
		t.Fatal(err)
	}
	base := []string{"recover-account", path, old.Name, file}
	for _, args := range [][]string{
		base[:1], base[:3], append(append([]string{}, base...), "--unknown-secret-value"),
		append(append([]string{}, base...), "--reset-mfa", "--reset-mfa"),
		append(append([]string{}, base...), "--revoke-token", "--revoke-token"),
		append(append([]string{}, base...), "--reset-mfa", "--revoke-token", "--extra"),
		{"recover-account", path, old.Name, filepath.Join(dir, "missing-secret-file")},
		{"recover-account", path, old.Name, dir},
	} {
		_, err := RunCLI(context.Background(), args, nil, recoveryReviewNoNetwork{t})
		if err == nil || strings.Contains(err.Error(), "unknown-secret-value") || strings.Contains(err.Error(), "missing-secret-file") || strings.Contains(err.Error(), recoveryReviewPassword) {
			t.Fatal("malformed CLI invocation succeeded or exposed supplied values", err)
		}
	}
	s := recoveryReviewOpen(t, path)
	if !reflect.DeepEqual(before, recoveryReviewSnapshot(t, s.db, "")) {
		t.Fatal("rejected CLI input changed the database")
	}
}

func TestRecoveryReviewCLIPasswordRuneAndByteBoundaries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.db")
	n, old := recoveryReviewFixture(t, path, "admin")
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "private-password")
	for _, tc := range []struct{ name, password, ending string }{
		{"minimum-no-newline", "123456789012", ""},
		{"spaces-with-lf", "  spaced passphrase  ", "\n"},
		{"maximum-ascii", strings.Repeat("x", 128), "\r\n"},
		{"maximum-514-byte-file", strings.Repeat("🔐", 128), "\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(file, []byte(tc.password+tc.ending), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := RunCLI(context.Background(), []string{"recover-account", path, old.Name, file, "--revoke-token", "--reset-mfa"}, nil, recoveryReviewNoNetwork{t})
			if err != nil {
				t.Fatal("valid password-file boundary rejected", err)
			}
			s := recoveryReviewOpen(t, path)
			a, ok, err := loadOne[account](s, "accounts", old.ID)
			if err != nil || !ok || !verifyPassword(a, tc.password) {
				t.Fatal("password-file bytes or rune boundary changed", err)
			}
			if err := s.close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func recoveryReviewFileURI(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func TestRecoveryReviewForeignSchemaAndBytesRemainUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name, sql string
		raw       []byte
	}{
		{name: "empty", raw: []byte{}}, {name: "non-sqlite", raw: []byte("foreign private data")},
		{name: "foreign-schema", sql: `CREATE TABLE users(id INTEGER PRIMARY KEY,secret BLOB); INSERT INTO users(secret) VALUES(x'010203'); PRAGMA user_version=81; PRAGMA application_id=12345;`},
		{name: "no-identity", sql: `CREATE TABLE records(category TEXT,id TEXT,payload TEXT); INSERT INTO records VALUES('foreign','keep','private user data');`},
		{name: "invalid-identity-json", sql: `CREATE TABLE records(category TEXT,id TEXT,payload TEXT); INSERT INTO records VALUES('settings','identity','{invalid');`},
		{name: "invalid-identity-id", sql: `CREATE TABLE records(category TEXT,id TEXT,payload TEXT); INSERT INTO records VALUES('settings','identity','{"id":"bad id","name":"Foreign"}');`},
		{name: "invalid-identity-name", sql: `CREATE TABLE records(category TEXT,id TEXT,payload TEXT); INSERT INTO records VALUES('settings','identity','{"id":"foreign","name":""}');`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "foreign #percent% + café.db")
			if tc.sql != "" {
				db, err := sql.Open("sqlite", recoveryReviewFileURI(path))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(tc.sql); err != nil {
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(path, tc.raw, 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := RecoverAccount(context.Background(), RecoveryOptions{Database: path, AccountName: "admin", Password: recoveryReviewPassword}); err == nil {
				t.Fatal("foreign file accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("foreign database bytes changed", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatal("read-only rejection created a lock, journal, or other file", err)
			}
		})
	}
}

// '?' is a legal Unix filename character. The read-only URI and write open must
// refer to exactly the same file rather than interpreting the suffix as a DSN.
func TestRecoveryReviewQuestionMarkFilename(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames cannot contain '?'; run on Unix")
	}
	dir := t.TempDir()
	original := filepath.Join(dir, "original.db")
	n, old := recoveryReviewFixture(t, original, "admin")
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "literal?mode=rwc.db")
	if err := os.Rename(original, path); err != nil {
		t.Fatal(err)
	}
	_, err := RecoverAccount(context.Background(), RecoveryOptions{Database: path, AccountName: old.Name, Password: recoveryReviewPassword})
	if err != nil {
		t.Error("literal question-mark filename recovery failed:", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "literal")); !os.IsNotExist(err) {
		t.Error("recovery opened or created the DSN-truncated sibling database")
	}
	db, openErr := sql.Open("sqlite", recoveryReviewFileURI(path)+"?mode=ro")
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer db.Close()
	a, ok, loadErr := loadOne[account](&store{db: db}, "accounts", old.ID)
	if loadErr != nil || !ok || !verifyPassword(a, recoveryReviewPassword) {
		t.Error("recovery did not update the requested literal database", loadErr)
	}
}

func TestRecoveryReviewQuestionMarkCannotSelectAnOwnedSibling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames cannot contain '?'; run on Unix")
	}
	dir := t.TempDir()
	ownedPath := filepath.Join(dir, "literal")
	owner, _ := recoveryReviewFixture(t, ownedPath, "admin")
	before := recoveryReviewSnapshot(t, owner.store.db, "")
	source := filepath.Join(dir, "source.db")
	target, old := recoveryReviewFixture(t, source, "viewer")
	if err := target.Stop(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "literal?mode=rwc.db")
	if err := os.Rename(source, path); err != nil {
		t.Fatal(err)
	}
	view, err := RecoverAccount(context.Background(), RecoveryOptions{Database: path, AccountName: old.Name, Password: recoveryReviewPassword})
	if err != nil || view.Role != "viewer" {
		t.Error("recovery selected the wrong database or failed to recover the requested file", err)
	}
	if !reflect.DeepEqual(before, recoveryReviewSnapshot(t, owner.store.db, "")) {
		t.Error("recovery mutated a different database while its node held the ownership lock")
	}
}
