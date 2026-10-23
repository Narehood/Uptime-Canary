package origin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrivatePermissions(t *testing.T) {
	dir := filepath.Join(privateTempDir(t), "identity")
	r := openTest(t, Options{Directory: dir, NodeID: "n"})
	for path, mode := range map[string]os.FileMode{
		dir: 0o700, r.path: 0o600, filepath.Join(dir, fileStem("n")+".lock"): 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("permissions on %s: %v, %v; want %o", path, info, err, mode)
		}
	}
	r.Close()
	if err := os.Chmod(r.path, 0o644); err != nil {
		t.Fatal(err)
	}
	if owner, err := Open(Options{Directory: dir, NodeID: "n"}); owner != nil || !errors.Is(err, ErrInvalidState) {
		t.Fatalf("public manifest accepted: %v, %v", owner, err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if owner, err := Open(Options{Directory: dir, NodeID: "other"}); owner != nil || err == nil {
		t.Fatalf("public identity directory accepted: %v, %v", owner, err)
	}
}

func TestSpecialFilesFailWithoutBlocking(t *testing.T) {
	for _, suffix := range []string{".json", ".lock"} {
		t.Run(suffix, func(t *testing.T) {
			dir := privateTempDir(t)
			path := filepath.Join(dir, fileStem("n")+suffix)
			if err := unix.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
			if r, err := Open(Options{Directory: dir, NodeID: "n"}); r != nil || !errors.Is(err, ErrInvalidState) {
				t.Fatalf("FIFO accepted: %v, %v", r, err)
			}
		})
	}
}
