package canary

import (
	"bytes"
	"database/sql"
	"image"
	"image/png"
	"path/filepath"
	"testing"
)

func TestBrowserScreenshotRetainsOriginalObserverAfterStateTransfer(t *testing.T) {
	node := local(t, func(options *Options) { options.Peers = []Peer{{ID: "b", URL: "http://127.0.0.1:1"}} })
	input := browserInput()
	input.NodeIDs = []string{node.ID, "b"}
	input.Browser.Screenshot = true
	// The running node has scheduler and peer goroutines. Use the API's locked
	// writer, as a deployed caller would, rather than mutating its maps directly.
	monitor := add(t, node, input)
	var pixels bytes.Buffer
	if err := png.Encode(&pixels, image.NewRGBA(image.Rect(0, 0, 1024, 576))); err != nil {
		t.Fatal(err)
	}
	// A private state backup can contain the previous observer's local image.
	// Keeping the monitor/generation does not make this node its photographer.
	previous := Sample{Observation: Observation{MonitorID: monitor.ID, NodeID: "b", CheckedAt: node.now(), State: "up"}, Revision: monitor.Revision, CheckGeneration: checkGeneration(monitor), Sequence: 1}
	tx, err := node.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = saveBrowserScreenshot(tx, monitor, previous, pixels.Bytes()); err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		t.Fatal(err)
	}
	status, _ := api(t, node, "/api/monitors/"+monitor.ID+"/screenshot", "GET", nil, adminToken)
	if status != 404 {
		t.Fatal("a previous observer's capture was relabelled as local", status)
	}
	current := previous
	current.NodeID = node.ID
	current.Sequence++
	tx, err = node.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = saveBrowserScreenshot(tx, monitor, current, pixels.Bytes()); err == nil {
		err = tx.Commit()
	} else {
		_ = tx.Rollback()
	}
	if err != nil {
		t.Fatal(err)
	}
	if status, _ = api(t, node, "/api/monitors/"+monitor.ID+"/screenshot", "GET", nil, adminToken); status != 200 {
		t.Fatal("new local capture was not available", status)
	}
}

func TestBrowserScreenshotLegacyMigrationDoesNotInventObserver(t *testing.T) {
	db, err := sql.Open("sqlite", sqliteFileURI(filepath.Join(t.TempDir(), "legacy.sqlite")))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE browser_screenshots(
 monitor_id TEXT PRIMARY KEY,generation TEXT NOT NULL,sequence INTEGER NOT NULL,
 checked_at INTEGER NOT NULL,data BLOB NOT NULL CHECK(length(data)<=196608)) STRICT;
 INSERT INTO browser_screenshots VALUES('old','generation',7,1234,x'010203');`)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = initializeBrowserStore(db); err != nil {
			t.Fatal(err)
		}
	}
	var observer, generation string
	var sequence, checkedAt int64
	var data []byte
	if err = db.QueryRow(`SELECT node_id,generation,sequence,checked_at,data FROM browser_screenshots WHERE monitor_id='old'`).Scan(&observer, &generation, &sequence, &checkedAt, &data); err != nil {
		t.Fatal(err)
	}
	if observer != "" || generation != "generation" || sequence != 7 || checkedAt != 1234 || !bytes.Equal(data, []byte{1, 2, 3}) {
		t.Fatal("legacy capture was lost, changed, or assigned invented provenance")
	}
}
