package canary

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNodeIdentityMismatchPreservesOldStorageAndAllowsSeparateNewNode(t *testing.T) {
	dir := t.TempDir()
	options := Options{NodeID: "previous-node", DBPath: filepath.Join(dir, "old-data", "canary.db"),
		IdentityDirectory: filepath.Join(dir, "old-identity"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken}
	previous, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if err = previous.Stop(); err != nil {
		t.Fatal(err)
	}
	options.NodeID = "home-dns01"
	wrong, err := New(options)
	if wrong != nil || err == nil || !strings.Contains(err.Error(), "Configured node ID differs from the persisted node ID") ||
		!strings.Contains(err.Error(), "separate data and identity volumes") {
		t.Fatalf("reused volumes must refuse identity replacement with a recovery instruction: %v", err)
	}
	options.NodeID = "previous-node"
	retained, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Stop()
	if retained.ID != "previous-node" {
		t.Fatal("the original identity was changed")
	}
	options.NodeID = "home-dns01"
	options.DBPath = filepath.Join(dir, "new-data", "canary.db")
	options.IdentityDirectory = filepath.Join(dir, "new-identity")
	fresh, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Stop()
	if fresh.ID != "home-dns01" || retained.ID != "previous-node" {
		t.Fatal("separate new-node storage did not preserve both identities")
	}
}
