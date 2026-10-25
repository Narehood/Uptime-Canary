package canary

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"uptime-canary/apps/node-go/internal/origin"
)

type store struct {
	db     *sql.DB
	lock   *os.File
	origin *origin.Reservation
}
type dbExec interface {
	Exec(string, ...any) (sql.Result, error)
}

// SQLite treats a bare filename's '?' suffix as driver options. Use an escaped
// file URI so the database and its ownership lock always refer to the same path.
func sqliteFileURI(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func openStore(path string) (*store, error) {
	var lock *os.File
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, fmt.Errorf("Cannot create database directory")
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("Cannot resolve database path")
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
		if err != nil {
			return nil, fmt.Errorf("Cannot resolve database directory")
		}
		path = filepath.Join(parent, filepath.Base(absolute))
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		lock, err = lockDatabase(path + ".lock")
		if err != nil {
			return nil, err
		}
	}
	success := false
	defer func() {
		if !success && lock != nil {
			_ = lock.Close()
		}
	}()
	dsn := path
	if path != ":memory:" {
		dsn = sqliteFileURI(path)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("Cannot open database")
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;
 CREATE TABLE IF NOT EXISTS records(category TEXT NOT NULL,id TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(category,id)) STRICT;
 CREATE TABLE IF NOT EXISTS history(id INTEGER PRIMARY KEY,monitor_id TEXT NOT NULL,checked_at INTEGER NOT NULL,payload TEXT NOT NULL) STRICT;
 CREATE INDEX IF NOT EXISTS history_monitor ON history(monitor_id,id DESC);
 CREATE INDEX IF NOT EXISTS history_age ON history(checked_at);
 CREATE TABLE IF NOT EXISTS revision_index(entity TEXT NOT NULL,revision TEXT NOT NULL,edge_id TEXT NOT NULL,payload_hash TEXT NOT NULL,PRIMARY KEY(entity,revision)) STRICT;
 CREATE TABLE IF NOT EXISTS revision_parents(entity TEXT NOT NULL,revision TEXT NOT NULL,parent TEXT NOT NULL,PRIMARY KEY(entity,revision,parent)) STRICT;
 CREATE INDEX IF NOT EXISTS revision_children ON revision_parents(entity,parent,revision);
 CREATE TABLE IF NOT EXISTS incident_fact_index(fact_id TEXT PRIMARY KEY,monitor_id TEXT NOT NULL,incident_id TEXT NOT NULL,revision TEXT NOT NULL,kind TEXT NOT NULL,node_id TEXT NOT NULL,run INTEGER NOT NULL,related_id TEXT NOT NULL) STRICT;
 CREATE INDEX IF NOT EXISTS incident_run_evidence ON incident_fact_index(monitor_id,incident_id,revision,kind,node_id,run);`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("Cannot initialize database")
	}
	if err = initializeBrowserStore(db); err != nil {
		db.Close()
		return nil, err
	}
	if err = initializeAvailabilityStore(db); err != nil {
		db.Close()
		return nil, err
	}
	if path != ":memory:" {
		_ = os.Chmod(path, 0600)
	}
	success = true
	return &store{db: db, lock: lock}, nil
}

func (s *store) close() error {
	err := s.db.Close()
	if s.origin != nil {
		err = errors.Join(err, s.origin.Close())
	}
	if s.lock != nil {
		err = errors.Join(err, s.lock.Close())
	}
	return err
}
func put(e dbExec, category, id string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = e.Exec(`INSERT INTO records(category,id,payload) VALUES(?,?,?) ON CONFLICT(category,id) DO UPDATE SET payload=excluded.payload`, category, id, string(b))
	return err
}
func remove(e dbExec, category, id string) error {
	_, err := e.Exec(`DELETE FROM records WHERE category=? AND id=?`, category, id)
	return err
}
func loadAll[T any](s *store, category string) (map[string]T, error) {
	rows, err := s.db.Query(`SELECT id,payload FROM records WHERE category=?`, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]T{}
	for rows.Next() {
		var id, b string
		if err := rows.Scan(&id, &b); err != nil {
			return nil, err
		}
		var v T
		if err := json.Unmarshal([]byte(b), &v); err != nil {
			return nil, fmt.Errorf("Invalid persisted record")
		}
		out[id] = v
	}
	return out, rows.Err()
}
func loadOne[T any](s *store, category, id string) (T, bool, error) {
	var v T
	var b string
	err := s.db.QueryRow(`SELECT payload FROM records WHERE category=? AND id=?`, category, id).Scan(&b)
	if err == sql.ErrNoRows {
		return v, false, nil
	}
	if err != nil {
		return v, false, err
	}
	err = json.Unmarshal([]byte(b), &v)
	return v, true, err
}
func (s *store) history(id string, limit int) ([]Observation, error) {
	rows, err := s.db.Query(`SELECT payload FROM history WHERE monitor_id=? ORDER BY id DESC LIMIT ?`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Observation{}
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var o Observation
		if err := json.Unmarshal([]byte(b), &o); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}
func appendHistory(e dbExec, o Observation, limit int, cutoff int64) error {
	b, err := json.Marshal(o)
	if err != nil {
		return err
	}
	if _, err = e.Exec(`INSERT INTO history(monitor_id,checked_at,payload) VALUES(?,?,?)`, o.MonitorID, o.CheckedAt, string(b)); err != nil {
		return err
	}
	if _, err = e.Exec(`DELETE FROM history WHERE monitor_id=? AND id NOT IN(SELECT id FROM history WHERE monitor_id=? ORDER BY id DESC LIMIT ?)`, o.MonitorID, o.MonitorID, limit); err != nil {
		return err
	}
	_, err = e.Exec(`DELETE FROM history WHERE checked_at<?`, cutoff)
	return err
}
