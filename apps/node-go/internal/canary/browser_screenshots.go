package canary

import (
	"bytes"
	"database/sql"
	"errors"
	"image/png"

	"uptime-canary/apps/node-go/internal/browsermonitor"
)

func initializeBrowserStore(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS browser_screenshots(
 monitor_id TEXT PRIMARY KEY,node_id TEXT NOT NULL,generation TEXT NOT NULL,sequence INTEGER NOT NULL,
 checked_at INTEGER NOT NULL,data BLOB NOT NULL CHECK(length(data)<=196608)) STRICT;`)
	if err != nil {
		return err
	}
	rows, err := db.Query(`PRAGMA table_info(browser_screenshots)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var index, notNull, primaryKey int
		var name, kind string
		var defaultValue sql.NullString
		if err = rows.Scan(&index, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			break
		}
		found = found || name == "node_id"
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil || found {
		return err
	}
	// Older development captures have no trustworthy observer provenance. Keep
	// their bytes but leave them unavailable until a new local capture replaces
	// them. Never infer the photographer from the database's current owner.
	_, err = db.Exec(`ALTER TABLE browser_screenshots ADD COLUMN node_id TEXT NOT NULL DEFAULT ''`)
	return err
}

func saveBrowserScreenshot(tx dbExec, m Monitor, s Sample, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	if m.Type != "real-browser" || m.Browser == nil || !m.Browser.Screenshot || len(raw) > browsermonitor.MaxScreenshot || identifier(s.NodeID, "nodeId") != nil {
		return errors.New("invalid browser screenshot")
	}
	config, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil || config.Width != 1024 || config.Height != 576 {
		return errors.New("invalid browser screenshot")
	}
	_, err = tx.Exec(`INSERT INTO browser_screenshots(monitor_id,node_id,generation,sequence,checked_at,data) VALUES(?,?,?,?,?,?)
 ON CONFLICT(monitor_id) DO UPDATE SET node_id=excluded.node_id,generation=excluded.generation,sequence=excluded.sequence,checked_at=excluded.checked_at,data=excluded.data`, m.ID, s.NodeID, checkGeneration(m), s.Sequence, s.CheckedAt, raw)
	return err
}

type BrowserScreenshot struct {
	MonitorID  string `json:"monitorId"`
	NodeID     string `json:"nodeId"`
	Generation string `json:"checkGeneration"`
	CheckedAt  int64  `json:"checkedAt"`
	Current    bool   `json:"current"`
	MIME       string `json:"mimeType"`
	Data       []byte `json:"data"`
}

// Screenshots stay on the observer that captured them. They are not replicated
// proof, a public status asset or part of an overview. Keep one bounded image
// per monitor, and label older captures independently from current health.
func (n *Node) browserScreenshotAPI(id, role, query string) (int, any, error) {
	if role == "viewer" {
		return 0, nil, fail(403, "Insufficient permissions")
	}
	if query != "" {
		return 0, nil, fail(400, "Screenshot reads take no query parameters")
	}
	m := n.current(id)
	if m == nil || m.Type != "real-browser" || m.Browser == nil || !m.Browser.Screenshot {
		return 0, nil, fail(404, "Browser screenshot unavailable")
	}
	out := BrowserScreenshot{MonitorID: id, MIME: "image/png"}
	var sequence int64
	err := n.store.db.QueryRow(`SELECT node_id,generation,sequence,checked_at,data FROM browser_screenshots WHERE monitor_id=?`, id).Scan(&out.NodeID, &out.Generation, &sequence, &out.CheckedAt, &out.Data)
	if errors.Is(err, sql.ErrNoRows) || err == nil && (out.NodeID != n.ID || out.Generation != checkGeneration(*m)) {
		return 0, nil, fail(404, "Browser screenshot unavailable")
	}
	if err != nil || len(out.Data) > browsermonitor.MaxScreenshot {
		return 0, nil, fail(503, "Browser screenshot unavailable")
	}
	for _, sample := range n.fresh(*m) {
		if sample.NodeID == n.ID && sample.Sequence == sequence && sampleGeneration(sample) == out.Generation {
			out.Current = true
		}
	}
	return 200, out, nil
}

// Called in the same transaction as configuration publication. A rename keeps
// the image; a new measurement generation, deletion or conflict removes it.
func purgeBrowserScreenshots(tx dbExec, previous, next map[string][]Version) error {
	for id, heads := range previous {
		if len(heads) != 1 || heads[0].Monitor.Type != "real-browser" {
			continue
		}
		after := next[id]
		if len(after) == 1 && !after[0].Monitor.Deleted && after[0].Monitor.Type == "real-browser" && after[0].Monitor.Browser != nil && after[0].Monitor.Browser.Screenshot && checkGeneration(after[0].Monitor) == checkGeneration(heads[0].Monitor) {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM browser_screenshots WHERE monitor_id=?`, id); err != nil {
			return err
		}
	}
	return nil
}
