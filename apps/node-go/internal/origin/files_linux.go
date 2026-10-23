package origin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func openRegular(path string, flags int) (*os.File, error) {
	// O_NONBLOCK prevents a preexisting FIFO from hanging before fstat.
	f, err := os.OpenFile(path, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0o600)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, fmt.Errorf("%w: symlink at %s", ErrInvalidState, path)
		}
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		var s unix.Stat_t
		err = unix.Fstat(int(f.Fd()), &s)
		if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || s.Nlink != 1) {
			err = fmt.Errorf("%w: expected private regular file with one link: %s", ErrInvalidState, path)
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func acquireLock(path string) (*os.File, error) {
	f, err := openRegular(path, os.O_RDWR|os.O_CREATE)
	if err != nil {
		return nil, fmt.Errorf("origin: open lock: %w", err)
	}
	if err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("origin: lock identity: %w", err)
	}
	info, err := f.Stat()
	if err != nil || info.Size() != 0 {
		f.Close()
		return nil, fmt.Errorf("%w: lock must be empty: %v", ErrInvalidState, err)
	}
	return f, nil
}

func releaseLock(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}

func setPrivateFile(f *os.File) error { return f.Chmod(0o600) }

func canonicalDirectory(path string) (string, error) { return filepath.EvalSymlinks(path) }

func checkDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("origin: identity directory must be a directory with mode 0700: %s", path)
	}
	return nil
}

func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func replaceFile(from, to string, existing bool) error {
	if !existing {
		// Initial publication must never replace a file that appeared meanwhile.
		return unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
	}
	return os.Rename(from, to)
}
