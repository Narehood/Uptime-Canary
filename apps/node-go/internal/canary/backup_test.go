package canary

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestDatabaseBackupCapturesLiveWALAndRestoreRevokesSessions(t *testing.T) {
	n, source, old := recoveryFixture(t)
	if _, err := n.store.db.Exec(`INSERT INTO history(monitor_id,checked_at,payload) VALUES('fixture',100,'{"monitorId":"fixture","nodeId":"a","state":"down","checkedAt":100,"latencyMs":1,"message":"private history"}')`); err != nil {
		t.Fatal(err)
	}
	if err := put(n.store.db, "fixture", "committed-wal", map[string]string{"private": "value from live WAL"}); err != nil {
		t.Fatal(err)
	}
	// The live connection retains ownership. A main-file byte copy would miss
	// some uncheckpointed records; the backup must include their logical data.
	before := recoveryReviewSnapshot(t, n.store.db, "")
	dir := t.TempDir()
	backup := filepath.Join(dir, "backup #percent% + café.db")
	value, err := RunCLI(context.Background(), []string{"backup-database", source, backup}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := value.(DatabaseCopyResult)
	b, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(b)
	if result.Bytes != int64(len(b)) || result.SHA256 != hex.EncodeToString(digest[:]) || result.SessionsRevoked || result.NodeID != n.ID {
		t.Fatal("incorrect snapshot identity or digest", result)
	}
	copy, err := sql.Open("sqlite", sqliteFileURI(backup)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, recoveryReviewSnapshot(t, copy, "")) {
		t.Fatal("backup omitted or changed committed logical content")
	}
	if err := copy.Close(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, recoveryReviewSnapshot(t, n.store.db, "")) {
		t.Fatal("backup changed the live source")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(backup + suffix); !os.IsNotExist(err) {
			t.Fatal("snapshot needs a sidecar", suffix)
		}
	}
	restored := filepath.Join(dir, "restored.db")
	value, err = RunCLI(context.Background(), []string{"prepare-restore", backup, restored}, nil, nil)
	if err != nil || !value.(DatabaseCopyResult).SessionsRevoked || !value.(DatabaseCopyResult).RequiresReenrollment {
		t.Fatal("restore failed", err)
	}
	s, err := openStore(restored)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	a, ok, err := loadOne[account](s, "accounts", old.ID)
	if err != nil || !ok || !equalJSON(a, old) {
		t.Fatal("restore changed account credentials or roles", err)
	}
	sessions, err := loadAll[loginSession](s, "sessions")
	if err != nil || len(sessions) != 0 {
		t.Fatal("restore retained password sessions", err)
	}
	// Exclude only sessions in both logical projections. All other schema,
	// configuration, causal evidence, receipts and history remain intact.
	withoutSessions := func(db *sql.DB) map[string][]string {
		view := recoveryReviewSnapshot(t, db, "")
		rows := []string{}
		for _, row := range view["records"] {
			var values []any
			if json.Unmarshal([]byte(row), &values) != nil || len(values) != 3 {
				t.Fatal("invalid fixture record")
			}
			if values[0] != "sessions" && !(values[0] == "settings" && values[1] == "restoreQuarantined") {
				rows = append(rows, row)
			}
		}
		view["records"] = rows
		return view
	}
	if !reflect.DeepEqual(withoutSessions(n.store.db), withoutSessions(s.db)) {
		t.Fatal("restore changed data outside password sessions")
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	unsafeNode, err := New(Options{NodeID: n.ID, DBPath: restored, Port: Ptr(0), ClusterToken: clusterToken, AdminToken: adminToken})
	if unsafeNode != nil {
		_ = unsafeNode.Stop()
	}
	if err == nil || !strings.Contains(err.Error(), "re-enrollment") {
		t.Fatal("restored sequence rollback could resume the old identity", err)
	}
}

func TestDatabaseBackupIsConsistentDuringTransactions(t *testing.T) {
	n, source, _ := recoveryFixture(t)
	if err := put(n.store.db, "fixture", "left", 0); err != nil {
		t.Fatal(err)
	}
	if err := put(n.store.db, "fixture", "right", 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; ctx.Err() == nil; i++ {
			tx, err := n.store.db.Begin()
			if err == nil {
				err = put(tx, "fixture", "left", i)
				if err == nil {
					err = put(tx, "fixture", "right", i)
				}
				if err == nil {
					err = tx.Commit()
				} else {
					_ = tx.Rollback()
				}
			}
			if err != nil {
				errCh <- err
				return
			}
		}
	}()
	defer func() { cancel(); wg.Wait() }()
	for i := 0; i < 3; i++ {
		path := filepath.Join(t.TempDir(), "snapshot.db")
		if _, err := BackupDatabase(context.Background(), source, path); err != nil {
			t.Fatal(err)
		}
		db, err := sql.Open("sqlite", sqliteFileURI(path)+"?mode=ro")
		if err != nil {
			t.Fatal(err)
		}
		s := &store{db: db}
		left, _, errL := loadOne[int](s, "fixture", "left")
		right, _, errR := loadOne[int](s, "fixture", "right")
		_ = db.Close()
		if errL != nil || errR != nil || left != right {
			t.Fatal("snapshot split an atomic source transaction", left, right, errL, errR)
		}
	}
	cancel()
	wg.Wait()
	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}
}

func TestDatabaseCopiesRejectUnsafePublicationAndForeignSources(t *testing.T) {
	n, source, _ := recoveryFixture(t)
	dir := t.TempDir()
	occupied := filepath.Join(dir, "occupied.db")
	if err := os.WriteFile(occupied, []byte("existing private file"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, restore := range []bool{false, true} {
		copy := BackupDatabase
		if restore {
			copy = PrepareDatabaseRestore
		}
		if _, err := copy(context.Background(), source, occupied); err == nil {
			t.Fatal("overwrote an existing file")
		}
		b, _ := os.ReadFile(occupied)
		if string(b) != "existing private file" {
			t.Fatal("rejection changed the existing file")
		}
		if _, err := copy(context.Background(), source, source); err == nil {
			t.Fatal("overwrote the live source")
		}
		path := filepath.Join(dir, "owned.db")
		lock, err := lockDatabase(path + ".lock")
		if err != nil {
			t.Fatal(err)
		}
		_, err = copy(context.Background(), source, path)
		_ = lock.Close()
		if err == nil {
			t.Fatal("ignored destination ownership")
		}
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := copy(cancelled, source, path); err == nil {
			t.Fatal("ignored cancellation")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("rejection published a destination")
		}
		foreign := filepath.Join(dir, "foreign.db")
		if err := os.WriteFile(foreign, []byte("foreign source"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := copy(context.Background(), foreign, path); err == nil {
			t.Fatal("accepted a foreign source")
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".canary-database-") {
			t.Fatal("left an incomplete snapshot")
		}
	}
	if runtime.GOOS != "windows" {
		path := filepath.Join(dir, "literal?mode=ro.db")
		if _, err := BackupDatabase(context.Background(), source, path); err != nil {
			t.Fatal("literal question-mark backup path failed", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "literal")); !os.IsNotExist(err) {
			t.Fatal("backup selected a different destination")
		}
	}
	if _, err := n.store.db.Exec("SELECT 1"); err != nil {
		t.Fatal("backup failures closed the live source", err)
	}
}
