package origin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConnectionIdentityLinuxRejectsPublicFileAndAliases(t *testing.T) {
	for _, kind := range []string{"permissions", "symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			r := openTest(t, Options{Directory: privateTempDir(t), NodeID: "private-connector"})
			data := []byte(`{"private":"fixture"}`)
			if err := r.SaveConnectionState(nil, data); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(r.dir, fileStem(r.nodeID)+".connection.json")
			switch kind {
			case "permissions":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := path + ".original"
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, path+".alias"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := r.ConnectionState(); err == nil {
				t.Fatal("unsafe credential file was read")
			}
			if err := r.SaveConnectionState(data, []byte(`{}`)); err == nil {
				t.Fatal("unsafe credential file was silently replaced")
			}
		})
	}
}
