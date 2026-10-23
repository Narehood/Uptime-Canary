package origin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Per-reservation hooks keep failure injection private and free of global races.
type persistenceOps struct {
	write   func(*os.File, []byte) (int, error)
	sync    func(*os.File) error
	close   func(*os.File) error
	replace func(string, string, bool) error
	syncDir func(string) error
}

func defaultPersistenceOps() persistenceOps {
	return persistenceOps{
		write: (*os.File).Write, sync: (*os.File).Sync, close: (*os.File).Close,
		replace: replaceFile, syncDir: syncDirectory,
	}
}

func (r *Reservation) persist(high int64) error {
	// Never overwrite a newly encountered or externally changed file. The
	// private directory and lifetime lock exclude cooperating writer races.
	m, err := readManifest(r.path, r.nodeID)
	if r.exists {
		if err != nil {
			return fmt.Errorf("origin: recheck state before reserve: %w", err)
		}
		if m.HighWater != r.high {
			return fmt.Errorf("%w: reservation changed while locked", ErrInvalidState)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: unexpected state at initial publication: %v", ErrInvalidState, err)
	}
	data, err := json.Marshal(manifest{Version: formatVersion, NodeID: r.nodeID, HighWater: high})
	if err != nil {
		return fmt.Errorf("origin: encode state: %w", err)
	}
	f, err := os.CreateTemp(r.dir, ".origin-reserve-*")
	if err != nil {
		return fmt.Errorf("origin: create reservation: %w", err)
	}
	temp := f.Name()
	defer os.Remove(temp) // Only this call's own uniquely created temporary file.
	defer f.Close()
	if err := setPrivateFile(f); err != nil {
		return err
	}
	n, err := r.ops.write(f, data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return fmt.Errorf("origin: write reservation: %w", err)
	}
	if err := r.ops.sync(f); err != nil {
		return fmt.Errorf("origin: sync reservation: %w", err)
	}
	if err := r.ops.close(f); err != nil {
		return fmt.Errorf("origin: close reservation: %w", err)
	}
	if err := r.ops.replace(temp, r.path, r.exists); err != nil {
		return fmt.Errorf("origin: replace reservation: %w", err)
	}
	if err := r.ops.syncDir(r.dir); err != nil {
		// Replacement may already be visible. This instance must never issue
		// numbers from that block or retry against its stale in-memory state.
		return fmt.Errorf("origin: sync reservation directory: %w", err)
	}
	r.high, r.exists = high, true
	return nil
}

// Resolve aliases after creation, and sync each newly created directory's
// parent: syncing only the final directory does not persist its own name.
func prepareDirectory(path string, create bool) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("origin: absolute directory: %w", err)
	}
	if _, err := os.Stat(abs); errors.Is(err, os.ErrNotExist) {
		if !create {
			return "", fmt.Errorf("%w: %s", ErrMissingState, abs)
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", errors.New("origin: cannot create filesystem root")
		}
		// Existing ancestors need not be private; the identity directory does.
		if err := makeParentsDurable(parent); err != nil {
			return "", err
		}
		if err := makeDirectoryDurable(abs); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", fmt.Errorf("origin: inspect directory: %w", err)
	}
	canonical, err := canonicalDirectory(abs)
	if err != nil {
		return "", fmt.Errorf("origin: resolve directory %q: %w", abs, err)
	}
	if err := checkDirectory(canonical); err != nil {
		return "", err
	}
	return canonical, nil
}

func makeParentsDurable(path string) error {
	if info, err := os.Stat(path); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("origin: parent is not a directory: %s", path)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return errors.New("origin: missing filesystem root")
	}
	if err := makeParentsDurable(parent); err != nil {
		return err
	}
	return makeDirectoryDurable(path)
}

func makeDirectoryDurable(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("origin: create directory: %w", err)
	}
	if err := syncDirectory(path); err != nil {
		return fmt.Errorf("origin: sync new directory: %w", err)
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("origin: sync directory parent: %w", err)
	}
	return nil
}
