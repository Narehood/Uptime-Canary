//go:build windows

package canary

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
)

// The kernel releases this lock on process death. Keep the file in place after
// close so a new owner cannot accidentally lock a different file object.
func lockDatabase(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("Cannot open database ownership lock")
	}
	err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{})
	if err != nil {
		_ = f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errors.New("Database is already in use by another node")
		}
		return nil, errors.New("Cannot acquire database ownership lock")
	}
	return f, nil
}
