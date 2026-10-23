package canary

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func recoveryFixture(t *testing.T) (*Node, string, account) {
	t.Helper()
	var clock atomic.Int64
	clock.Store(maintenanceClock(12, 1))
	path := filepath.Join(t.TempDir(), "recover.db")
	n := maintenanceNode(t, &clock, path)
	a, err := setPassword(n.accounts["bootstrap-admin"], "old recovery password")
	if err != nil {
		t.Fatal(err)
	}
	a.MFAEnabled, a.TOTPSecret, a.TOTPLastStep = true, "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP", 77
	a.PendingTOTP, a.PendingTOTPExpires = "pending-fixture", clock.Load()+10000
	n.accounts[a.ID] = a
	if !n.dbPut("accounts", a.ID, a) {
		t.Fatal(n.fault)
	}
	for id, session := range map[string]loginSession{"target-session": {AccountID: a.ID, AuthVersion: a.AuthVersion, ExpiresAt: clock.Load() + 10000}, "other-session": {AccountID: "another-account", AuthVersion: 0, ExpiresAt: clock.Load() + 10000}} {
		if !n.dbPut("sessions", id, session) {
			t.Fatal(n.fault)
		}
	}
	return n, path, a
}

func TestOfflineRecoveryRequiresExclusiveOwnershipAndRevokesOnlyTargetSessions(t *testing.T) {
	n, path, old := recoveryFixture(t)
	in := RecoveryOptions{Database: path, AccountName: "ADMIN", Password: "new recovery password", ResetMFA: true}
	if _, err := RecoverAccount(context.Background(), in); err == nil {
		t.Fatal("recovery opened a database still owned by its node")
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	view, err := RecoverAccount(context.Background(), in)
	if err != nil || view.MFAEnabled || !view.PasswordEnabled || view.Role != old.Role || view.ID != old.ID {
		t.Fatal("offline recovery failed", err, view)
	}
	s, err := openStore(path)
	if err != nil {
		t.Fatal("recovery did not release database ownership", err)
	}
	defer s.close()
	a, ok, err := loadOne[account](s, "accounts", old.ID)
	if err != nil || !ok || !verifyPassword(a, in.Password) || verifyPassword(a, "old recovery password") || a.AuthVersion != old.AuthVersion+1 || a.TOTPSecret != "" || a.TOTPLastStep != 0 || a.PendingTOTP != "" || a.PendingTOTPExpires != 0 || a.Hash != old.Hash {
		t.Fatal("recovery credentials were not durable or token retention changed", err)
	}
	sessions, err := loadAll[loginSession](s, "sessions")
	if err != nil || len(sessions) != 1 || sessions["other-session"].AccountID != "another-account" {
		t.Fatal("recovery did not isolate session revocation", err)
	}
}

func TestOfflineRecoveryRollbackPreservesCredentialsAndSessions(t *testing.T) {
	n, path, old := recoveryFixture(t)
	if _, err := n.store.db.Exec(`CREATE TRIGGER recovery_session_failure BEFORE DELETE ON records WHEN OLD.category='sessions' BEGIN SELECT RAISE(FAIL,'injected recovery failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverAccount(context.Background(), RecoveryOptions{Database: path, AccountName: old.Name, Password: "not committed password", ResetMFA: true, RevokeToken: true}); err == nil {
		t.Fatal("recovery acknowledged a partial transaction")
	}
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	a, _, err := loadOne[account](s, "accounts", old.ID)
	if err != nil || !equalJSON(a, old) {
		t.Fatal("failed recovery changed credentials", err)
	}
	sessions, err := loadAll[loginSession](s, "sessions")
	if err != nil || len(sessions) != 2 {
		t.Fatal("failed recovery revoked only part of the sessions", err)
	}
}

func TestRecoveryCLIUsesPasswordFileWithoutManagementToken(t *testing.T) {
	n, path, old := recoveryFixture(t)
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "private-password.txt")
	password := "  a private recovery passphrase  "
	if err := os.WriteFile(file, []byte(password+"\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	v, err := RunCLI(context.Background(), []string{"recover-account", path, old.Name, file, "--revoke-token"}, func(string) string { return "" }, nil)
	if err != nil || !v.(AccountView).MFAEnabled {
		t.Fatal("CLI recovery failed or unexpectedly disabled MFA", err)
	}
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	a, _, err := loadOne[account](s, "accounts", old.ID)
	if err != nil || !verifyPassword(a, password) || a.Hash != "" || a.TOTPSecret != old.TOTPSecret {
		t.Fatal("CLI changed password spaces or did not revoke token", err)
	}
}

func TestRecoveryRejectsMissingAndForeignDatabaseWithoutChangingIt(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.db")
	if _, err := RecoverAccount(context.Background(), RecoveryOptions{Database: missing, AccountName: "admin", Password: "valid recovery password"}); err == nil {
		t.Fatal("recovery created a missing database")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("missing database appeared")
	}
	foreign := filepath.Join(t.TempDir(), "foreign.db")
	db, err := sql.Open("sqlite", foreign)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE another_application(value TEXT); INSERT INTO another_application VALUES('keep');`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(foreign)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = RecoverAccount(context.Background(), RecoveryOptions{Database: foreign, AccountName: "admin", Password: "valid recovery password"}); err == nil {
		t.Fatal("recovery accepted an unrelated SQLite database")
	}
	after, err := os.ReadFile(foreign)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("recovery changed an unrelated SQLite database", err)
	}
}
