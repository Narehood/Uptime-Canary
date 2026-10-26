package origin

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestTLSIdentityIsPrivateCreateOnceAndSurvivesSequenceReservation(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "identity")
	options := Options{Directory: directory, NodeID: "private-node"}
	r, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	value := []byte(`{"version":1,"nodeId":"private-node","certificate":"fixture","key":"private-fixture"}`)
	if err = r.CreateTLSState(value); err != nil {
		t.Fatal(err)
	}
	if err = r.CreateTLSState([]byte("replacement")); err == nil {
		t.Fatal("existing certificate was replaced")
	}
	local, err := ReadTLSState(directory, options.NodeID)
	if err != nil || !bytes.Equal(local, value) {
		t.Fatal("read-only health inspection failed", err)
	}
	if _, err = Open(options); err == nil {
		t.Fatal("health inspection released the lifetime lock")
	}
	if _, err = r.Next(); err != nil {
		t.Fatal(err)
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	retained, err := r.TLSState()
	if err != nil || !bytes.Equal(retained, value) {
		t.Fatal("certificate changed during restart", err)
	}
	if state, err := ReadTLSState(directory, "other-node"); err == nil || state != nil {
		t.Fatal("another node's identity was disclosed")
	}
}
