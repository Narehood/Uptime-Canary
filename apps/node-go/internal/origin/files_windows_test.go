package origin

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsCaseAndExtendedPathAliases(t *testing.T) {
	dir := privateTempDir(t)
	r := openTest(t, Options{Directory: dir, NodeID: "n"})
	for _, alias := range []string{strings.ToUpper(dir), `\\?\` + dir} {
		if second, err := Open(Options{Directory: alias, NodeID: "n"}); second != nil || !errors.Is(err, ErrLocked) {
			t.Fatalf("Windows alias owner = %v, %v", second, err)
		}
		probeProcess(t, alias, "n")
	}
	nextTest(t, r, 1)
}

func TestWindowsLongDirectoryPath(t *testing.T) {
	dir := privateTempDir(t)
	for len(dir) < 300 {
		dir = filepath.Join(dir, "retained-origin-directory")
	}
	r := openTest(t, Options{Directory: dir, NodeID: "n"})
	nextTest(t, r, 1)
	r.Close()
	r = openTest(t, Options{Directory: dir, NodeID: "n", RequireExisting: true})
	nextTest(t, r, BlockSize+1)
}
