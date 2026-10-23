package canary

import (
	"encoding/json"
	"testing"
)

func TestMeasurementMetadataUpgradeRejectsOlderPeersAndFutureDatabase(t *testing.T) {
	o := cr2Options(t, true)
	n := cr2New(t, o)
	old, err := n.snapshotPage("", "")
	if err != nil {
		t.Fatal(err)
	}
	old.Node = Identity{ID: "b", Name: "older peer"}
	old.Protocol = peerProtocol - 1
	b, _ := json.Marshal(old)
	if _, err := n.validateSnapshot(b, "b"); err == nil {
		t.Fatal("older peer could discard required coordination metadata")
	}
	identity := n.ID
	if err := put(n.store.db, "settings", "causalSchema", peerProtocol+1); err != nil {
		t.Fatal(err)
	}
	if err := n.Stop(); err != nil {
		t.Fatal(err)
	}
	if unexpected, err := New(o); err == nil {
		_ = unexpected.Stop()
		t.Fatal("future database was downgraded")
	}
	s, err := openStore(o.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	version, _, err := loadOne[int](s, "settings", "causalSchema")
	got, _, identityErr := loadOne[Identity](s, "settings", "identity")
	if err != nil || version != peerProtocol+1 || identityErr != nil || got.ID != identity {
		t.Fatal("rejected open rewrote coordination or identity", version, got, err, identityErr)
	}
}
