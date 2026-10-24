//go:build windows

package pm2monitor

import (
	"context"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPM2WindowsPrivatePipeAdmission(t *testing.T) {
	c, endpoint, accept := workerFixtureListener(t)
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		conn, err := accept()
		if conn != nil {
			<-release
			conn.Close()
		}
		done <- err
	}()
	// Keep the server instance alive throughout metadata admission.
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	conn, err := dialWindowsPipe(context.Background(), c, endpoint)
	if err != nil {
		t.Fatal("private production dial:", err)
	}
	conn.Close()
}

func TestPM2WindowsPipeDescriptorAdmission(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid.String()
	for _, item := range []struct {
		name, sddl string
		allowed    bool
	}{
		{"private", "O:" + sid + "D:(A;;GA;;;" + sid + ")(A;;GA;;;SY)(A;;GA;;;BA)", true},
		{"read-only-other-clients", "O:" + sid + "D:(A;;GA;;;" + sid + ")(A;;GR;;;WD)", true},
		{"foreign-instance", "O:" + sid + "D:(A;;GA;;;" + sid + ")(A;;0x4;;;WD)", false},
		{"foreign-generic-write", "O:" + sid + "D:(A;;GA;;;" + sid + ")(A;;GW;;;WD)", false},
		{"foreign-write-dacl", "O:" + sid + "D:(A;;GA;;;" + sid + ")(A;;WD;;;WD)", false},
		{"foreign-write-owner", "O:" + sid + "D:(A;;GA;;;" + sid + ")(A;;WO;;;WD)", false},
		{"deny-does-not-hide-foreign-allow", "O:" + sid + "D:(D;;GA;;;WD)(A;;GW;;;WD)(A;;GA;;;" + sid + ")", false},
		{"foreign-owner", "O:WDD:(A;;GA;;;" + sid + ")", false},
		{"null-dacl", "O:" + sid + "D:NO_ACCESS_CONTROL", false},
		{"empty-dacl", "O:" + sid + "D:", true},
	} {
		t.Run(item.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(item.sddl)
			if err != nil {
				t.Fatal("invalid private ACL fixture", err)
			}
			if got := privatePipeDescriptor(sd, user.User.Sid) == nil; got != item.allowed {
				t.Fatalf("descriptor admission = %v, want %v", got, item.allowed)
			}
		})
	}
}
