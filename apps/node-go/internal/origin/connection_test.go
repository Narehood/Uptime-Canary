package origin

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestConnectionStateRetainedCompareAndReplace(t *testing.T) {
	dir := privateTempDir(t)
	r, err := Open(Options{Directory: dir, NodeID: "connector"})
	if err != nil {
		t.Fatal(err)
	}
	first := []byte(`{"private":"first"}`)
	second := []byte(`{"private":"second"}`)
	if raw, err := r.ConnectionState(); err != nil || raw != nil {
		t.Fatal("new connection state was not absent")
	}
	if err := r.SaveConnectionState(nil, first); err != nil {
		t.Fatal(err)
	}
	if err := r.SaveConnectionState(nil, second); err == nil {
		t.Fatal("stale writer replaced retained state")
	}
	if raw, _ := r.ConnectionState(); !bytes.Equal(raw, first) {
		t.Fatal("failed replace changed identity")
	}
	if err := r.SaveConnectionState(first, second); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	r, err = Open(Options{Directory: dir, NodeID: "connector"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if raw, err := r.ConnectionState(); err != nil || !bytes.Equal(raw, second) {
		t.Fatal("connection state was not retained independently of allocator restart")
	}
	if err := r.SaveConnectionState(second, make([]byte, maxStateBytes+1)); err == nil {
		t.Fatal("oversized state accepted")
	}
	if _, err := r.Next(); err != nil {
		t.Fatal("connection state disrupted sequence allocator")
	}
}

func TestConnectionStatePersistenceFailuresPreservePreviousIdentity(t *testing.T) {
	for _, operation := range []string{"write", "sync", "close", "replace", "directory"} {
		t.Run(operation, func(t *testing.T) {
			r := openTest(t, Options{Directory: privateTempDir(t), NodeID: "connector"})
			first, next := []byte(`{"first":true}`), []byte(`{"next":true}`)
			if err := r.SaveConnectionState(nil, first); err != nil {
				t.Fatal(err)
			}
			original := r.ops
			injected := errors.New("injected storage failure")
			switch operation {
			case "write":
				r.ops.write = func(*os.File, []byte) (int, error) { return 0, injected }
			case "sync":
				r.ops.sync = func(*os.File) error { return injected }
			case "close":
				r.ops.close = func(f *os.File) error { _ = f.Close(); return injected }
			case "replace":
				r.ops.replace = func(string, string, bool) error { return injected }
			case "directory":
				r.ops.syncDir = func(string) error { return injected }
			}
			if err := r.SaveConnectionState(first, next); err == nil {
				t.Fatal("injected persistence failure reported success")
			}
			r.ops = original
			want := first
			if operation == "directory" {
				want = next
			} // Publication is already visible; caller must reopen.
			if raw, err := r.ConnectionState(); err != nil || !bytes.Equal(raw, want) {
				t.Fatal("unexpected identity after interrupted publication")
			}
			files, _ := filepath.Glob(filepath.Join(r.dir, ".connection-*"))
			if len(files) != 0 {
				t.Fatal("temporary publication file leaked")
			}
		})
	}
}

func TestConnectionStateRejectsNonRegularAndCorruptFiles(t *testing.T) {
	r := openTest(t, Options{Directory: privateTempDir(t), NodeID: "connector"})
	path := filepath.Join(r.dir, fileStem(r.nodeID)+".connection.json")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ConnectionState(); err == nil {
		t.Fatal("directory accepted as connection state")
	}
	if err := r.SaveConnectionState(nil, []byte(`{}`)); err == nil {
		t.Fatal("invalid existing identity overwritten")
	}
}
