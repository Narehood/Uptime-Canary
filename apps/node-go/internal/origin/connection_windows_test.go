package origin

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func TestConnectionIdentityWindowsRejectsPublicFileACL(t *testing.T) {
	r := openTest(t, Options{Directory: privateTempDir(t), NodeID: "private-connector"})
	data := []byte(`{"private":"fixture"}`)
	if err := r.SaveConnectionState(nil, data); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(r.dir, fileStem(r.nodeID)+".connection.json")
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkPrivateConnectionFile(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ConnectionState(); err == nil {
		t.Fatal("public credential file was read")
	}
	if err := r.SaveConnectionState(data, []byte(`{}`)); err == nil {
		t.Fatal("unsafe credential file was silently replaced")
	}
}
