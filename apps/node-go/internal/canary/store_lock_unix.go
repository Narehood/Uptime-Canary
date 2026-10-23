//go:build !windows

package canary

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

func lockDatabase(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, errors.New("Cannot open database ownership lock")
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, errors.New("Database is already in use by another node")
		}
		return nil, errors.New("Cannot acquire database ownership lock")
	}
	return f, nil
}
