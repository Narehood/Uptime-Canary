package origin

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func windowsPath(path string) (*uint16, error) {
	if !strings.HasPrefix(path, `\\?\`) {
		if strings.HasPrefix(path, `\\`) {
			path = `\\?\UNC\` + path[2:]
		} else {
			path = `\\?\` + path
		}
	}
	return windows.UTF16PtrFromString(path)
}

func openRegular(path string, flags int) (*os.File, error) {
	p, err := windowsPath(path)
	if err != nil {
		return nil, err
	}
	access, disposition := uint32(windows.GENERIC_READ), uint32(windows.OPEN_EXISTING)
	if flags&os.O_RDWR != 0 {
		access |= windows.GENERIC_WRITE
	}
	if flags&os.O_CREATE != 0 {
		disposition = windows.OPEN_ALWAYS
	}
	// No FILE_SHARE_DELETE: an open lock inode cannot be replaced or removed.
	h, err := windows.CreateFile(p, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, disposition,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), path)
	var info windows.ByHandleFileInformation
	err = windows.GetFileInformationByHandle(h, &info)
	if err == nil && (info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1) {
		err = fmt.Errorf("%w: expected regular file with one link: %s", ErrInvalidState, path)
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
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped); err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
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
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped)
}

func setPrivateFile(f *os.File) error { return nil } // Windows uses the directory ACL.

func canonicalDirectory(path string) (string, error) {
	p, err := windowsPath(path)
	if err != nil {
		return "", err
	}
	// Resolve junctions and volume/path aliases using the directory handle.
	// filepath.EvalSymlinks does not resolve every Windows reparse point.
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	buffer := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if n < uint32(len(buffer)) {
			return windows.UTF16ToString(buffer[:n]), nil
		}
		if n > 32_768 {
			return "", errors.New("origin: resolved directory path is too long")
		}
		buffer = make([]uint16, int(n)+1)
	}
}

func checkDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("origin: identity path is not a directory")
	}
	return nil
}

// MoveFileEx with WRITE_THROUGH supplies Windows publication durability.
// Directory FlushFileBuffers is not portable on Windows.
func syncDirectory(path string) error { return nil }

func replaceFile(from, to string, existing bool) error {
	source, err := windowsPath(from)
	if err != nil {
		return err
	}
	target, err := windowsPath(to)
	if err != nil {
		return err
	}
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if existing {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	return windows.MoveFileEx(source, target, flags)
}
