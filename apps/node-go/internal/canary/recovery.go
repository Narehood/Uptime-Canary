package canary

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type RecoveryOptions struct {
	Database    string
	AccountName string
	Password    string
	ResetMFA    bool
	RevokeToken bool
}

// Check the existing file without creating a DB or changing another SQLite
// application's schema. The subsequent canonical ownership lock is still the
// authority for the write; accounts are reread after that lock is acquired.
func recoveryDatabase(path string) error {
	if path == "" || path == ":memory:" {
		return errors.New("Recovery requires an existing node database file")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("Recovery requires an existing node database file")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return errors.New("Cannot open recovery database")
	}
	db, err := sql.Open("sqlite", sqliteFileURI(abs)+"?mode=ro")
	if err != nil {
		return errors.New("Cannot open recovery database")
	}
	defer db.Close()
	s := &store{db: db}
	identity, exists, err := loadOne[Identity](s, "settings", "identity")
	if err != nil || !exists || identifier(identity.ID, "node ID") != nil || validText(identity.Name, "node name", 128) != nil {
		return errors.New("File is not an initialized Uptime-Canary database")
	}
	return nil
}

// RecoverAccount is an offline filesystem-administrator operation. It never
// starts monitoring or accepts a management bearer in place of the OS lock.
// The account update and removal of its password sessions commit atomically.
func RecoverAccount(ctx context.Context, in RecoveryOptions) (AccountView, error) {
	if err := passwordValue(in.Password); err != nil {
		return AccountView{}, err
	}
	if validText(in.AccountName, "account name", 128) != nil {
		return AccountView{}, errors.New("A valid account name is required")
	}
	if err := recoveryDatabase(in.Database); err != nil {
		return AccountView{}, err
	}
	s, err := openStore(in.Database)
	if err != nil {
		return AccountView{}, errors.New("Cannot exclusively open this node database; stop its running node before recovery")
	}
	defer s.close()
	accounts, err := loadAll[account](s, "accounts")
	if err != nil {
		return AccountView{}, errors.New("Cannot read node accounts")
	}
	var a account
	found := false
	for _, candidate := range accounts {
		if strings.EqualFold(candidate.Name, in.AccountName) {
			if found {
				return AccountView{}, errors.New("Account name is ambiguous")
			}
			a, found = candidate, true
		}
	}
	if !found {
		return AccountView{}, errors.New("Account not found in this node database")
	}
	if ctx.Err() != nil {
		return AccountView{}, errors.New("Account recovery cancelled")
	}
	a, err = setPassword(a, in.Password)
	if err != nil {
		return AccountView{}, err
	}
	a.PendingTOTP, a.PendingTOTPExpires = "", 0
	if in.ResetMFA {
		a.TOTPSecret, a.MFAEnabled, a.TOTPLastStep = "", false, 0
	}
	if in.RevokeToken {
		a.Hash = ""
	}
	a.AuthVersion++
	sessions, err := loadAll[loginSession](s, "sessions")
	if err != nil {
		return AccountView{}, errors.New("Cannot read node sessions")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountView{}, errors.New("Account recovery did not commit")
	}
	defer tx.Rollback()
	err = put(tx, "accounts", a.ID, a)
	for id, session := range sessions {
		if err == nil && session.AccountID == a.ID {
			err = remove(tx, "sessions", id)
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return AccountView{}, errors.New("Account recovery did not commit")
	}
	return a.AccountView, nil
}

func recoverCLI(ctx context.Context, args []string) (any, error) {
	if len(args) < 4 || len(args) > 6 {
		return nil, errors.New("Usage: recover-account <database-file> <account-name> <password-file> [--reset-mfa] [--revoke-token]")
	}
	in := RecoveryOptions{Database: args[1], AccountName: args[2]}
	seen := map[string]bool{}
	for _, flag := range args[4:] {
		if seen[flag] {
			return nil, errors.New("Recovery flags must not repeat")
		}
		seen[flag] = true
		switch flag {
		case "--reset-mfa":
			in.ResetMFA = true
		case "--revoke-token":
			in.RevokeToken = true
		default:
			return nil, errors.New("Unknown recovery flag")
		}
	}
	f, err := os.Open(args[3])
	if err != nil {
		return nil, errors.New("Cannot read the private password file")
	}
	defer f.Close()
	b, err := readBounded(f, 514)
	if err != nil {
		return nil, errors.New("Cannot read a valid password from the private password file")
	}
	// Permit one text-editor line ending; do not trim password spaces.
	in.Password = string(b)
	if strings.HasSuffix(in.Password, "\r\n") {
		in.Password = strings.TrimSuffix(in.Password, "\r\n")
	} else {
		in.Password = strings.TrimSuffix(in.Password, "\n")
	}
	return RecoverAccount(ctx, in)
}
