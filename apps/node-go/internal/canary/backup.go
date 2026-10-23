package canary

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

type DatabaseCopyResult struct {
	NodeID               string `json:"nodeId"`
	Bytes                int64  `json:"bytes"`
	SHA256               string `json:"sha256"`
	SessionsRevoked      bool   `json:"sessionsRevoked"`
	RequiresReenrollment bool   `json:"requiresReenrollment"`
}

// BackupDatabase uses a separate read-only SQLite connection. VACUUM INTO
// includes committed WAL contents and produces a consistent snapshot while
// the node continues running; copying only the main .db file would not.
func BackupDatabase(ctx context.Context, database, destination string) (DatabaseCopyResult, error) {
	return copyDatabase(ctx, database, destination, false)
}

// PrepareDatabaseRestore stages a new database without replacing an existing file.
// Password sessions are removed; account credentials, roles, node identity and
// coordination/history data are retained. The result is quarantined: rolling
// back a node's durable run sequence cannot safely resume the same HA identity.
// Activation requires the latest separately retained origin state, or a fresh
// identity and explicit re-enrollment when that state has been lost.
func PrepareDatabaseRestore(ctx context.Context, backup, destination string) (DatabaseCopyResult, error) {
	return copyDatabase(ctx, backup, destination, true)
}

func databaseCopyTarget(destination string) (string, error) {
	if destination == "" || destination == ":memory:" {
		return "", errors.New("A new destination database file is required")
	}
	abs, err := filepath.Abs(destination)
	if err != nil {
		return "", errors.New("Cannot resolve destination database")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", errors.New("Destination directory must already exist")
	}
	path := filepath.Join(parent, filepath.Base(abs))
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return "", errors.New("Destination file must not already exist")
	}
	return path, nil
}

func checkDatabaseCopy(ctx context.Context, db *sql.DB) (Identity, error) {
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return Identity{}, errors.New("Database integrity check failed")
	}
	identity, exists, err := loadOne[Identity](&store{db: db}, "settings", "identity")
	if err != nil || !exists || identifier(identity.ID, "node ID") != nil || validText(identity.Name, "node name", 128) != nil {
		return Identity{}, errors.New("File is not an initialized Uptime-Canary database")
	}
	return identity, nil
}

func copyDatabase(ctx context.Context, source, destination string, restore bool) (DatabaseCopyResult, error) {
	if ctx.Err() != nil {
		return DatabaseCopyResult{}, errors.New("Database copy cancelled")
	}
	if err := recoveryDatabase(source); err != nil {
		return DatabaseCopyResult{}, err
	}
	target, err := databaseCopyTarget(destination)
	if err != nil {
		return DatabaseCopyResult{}, err
	}
	// A node may try to create this previously absent path concurrently. Use
	// exactly its ownership lock until the complete file has been published.
	lock, err := lockDatabase(target + ".lock")
	if err != nil {
		return DatabaseCopyResult{}, errors.New("Cannot acquire destination database ownership")
	}
	defer lock.Close()
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		return DatabaseCopyResult{}, errors.New("Destination file must not already exist")
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".canary-database-*.tmp")
	if err != nil {
		return DatabaseCopyResult{}, errors.New("Cannot create a private database snapshot")
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Close(); err != nil {
		return DatabaseCopyResult{}, errors.New("Cannot prepare database snapshot")
	}
	abs, err := filepath.Abs(source)
	if err != nil {
		return DatabaseCopyResult{}, errors.New("Cannot open source database")
	}
	db, err := sql.Open("sqlite", sqliteFileURI(abs)+"?mode=ro")
	if err != nil {
		return DatabaseCopyResult{}, errors.New("Cannot open source database")
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err = db.ExecContext(ctx, "PRAGMA busy_timeout=5000; PRAGMA synchronous=FULL"); err != nil {
		return DatabaseCopyResult{}, errors.New("Cannot prepare source database")
	}
	// Parameters are bound as values. The output's escaped URI handles literal
	// question marks and prevents filename bytes being interpreted as options.
	if _, err = db.ExecContext(ctx, "VACUUM INTO ?", sqliteFileURI(name)); err != nil {
		return DatabaseCopyResult{}, errors.New("Database snapshot did not complete")
	}
	copyDB, err := sql.Open("sqlite", sqliteFileURI(name)+"?mode=rw")
	if err != nil {
		return DatabaseCopyResult{}, errors.New("Cannot verify database snapshot")
	}
	copyDB.SetMaxOpenConns(1)
	defer copyDB.Close()
	// Publish one self-contained database, independent of any WAL sidecars.
	if _, err = copyDB.ExecContext(ctx, "PRAGMA journal_mode=DELETE; PRAGMA synchronous=FULL"); err != nil {
		return DatabaseCopyResult{}, errors.New("Cannot finalize database snapshot")
	}
	if restore {
		tx, beginErr := copyDB.BeginTx(ctx, nil)
		if beginErr != nil {
			return DatabaseCopyResult{}, errors.New("Cannot prepare restored identity quarantine")
		}
		err = put(tx, "settings", "restoreQuarantined", true)
		if err == nil {
			_, err = tx.ExecContext(ctx, "DELETE FROM records WHERE category='sessions'")
		}
		if err == nil {
			err = tx.Commit()
		}
		_ = tx.Rollback()
		if err != nil {
			return DatabaseCopyResult{}, errors.New("Cannot revoke restored password sessions")
		}
	}
	identity, err := checkDatabaseCopy(ctx, copyDB)
	if err != nil {
		return DatabaseCopyResult{}, err
	}
	if err := copyDB.Close(); err != nil {
		return DatabaseCopyResult{}, errors.New("Cannot close database snapshot")
	}
	f, err := os.OpenFile(name, os.O_RDWR, 0600)
	if err != nil {
		return DatabaseCopyResult{}, errors.New("Cannot finish database snapshot")
	}
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return DatabaseCopyResult{}, errors.New("Cannot protect database snapshot")
	}
	h := sha256.New()
	bytes, err := io.Copy(h, f)
	if err != nil || f.Sync() != nil {
		return DatabaseCopyResult{}, errors.New("Cannot synchronize database snapshot")
	}
	if err := f.Close(); err != nil || ctx.Err() != nil {
		return DatabaseCopyResult{}, errors.New("Database copy did not complete")
	}
	// Hard-link publication is atomic and fails when a competing file exists.
	// Both names share a directory/filesystem. Unsupported filesystems fail
	// safely, retaining no partially published destination.
	if err := os.Link(name, target); err != nil {
		return DatabaseCopyResult{}, errors.New("Cannot publish database snapshot without replacing an existing file")
	}
	return DatabaseCopyResult{NodeID: identity.ID, Bytes: bytes, SHA256: hex.EncodeToString(h.Sum(nil)), SessionsRevoked: restore, RequiresReenrollment: restore}, nil
}

const databaseCopyUsage = "backup-database <database-file> <new-backup-file>, prepare-restore <backup-file> <new-quarantined-database-file>"

func databaseCopyCLI(ctx context.Context, args []string) (any, error) {
	if len(args) != 3 {
		return nil, errors.New("Usage: " + databaseCopyUsage)
	}
	if args[0] == "prepare-restore" {
		return PrepareDatabaseRestore(ctx, args[1], args[2])
	}
	return BackupDatabase(ctx, args[1], args[2])
}
