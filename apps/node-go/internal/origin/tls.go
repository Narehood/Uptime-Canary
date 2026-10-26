package origin

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// TLSState is outside database backups and shares the reservation lifetime
// lock. Only the fixed TLS filename is accessible through this API.
func (r *Reservation) TLSState() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tlsState()
}

func (r *Reservation) tlsState() ([]byte, error) {
	if r.closed || r.lock == nil {
		return nil, ErrClosed
	}
	return readTLSState(r.dir, r.nodeID)
}

// ReadTLSState permits a local health/inspection command to read a private
// identity without acquiring or interfering with the server's lifetime lock.
// It never creates directories, modifies state or returns an unbounded file.
func ReadTLSState(directory, nodeID string) ([]byte, error) {
	if nodeID == "" || len(nodeID) > 128 {
		return nil, ErrInvalidState
	}
	dir, err := prepareDirectory(directory, false)
	if err != nil {
		return nil, err
	}
	state, err := readTLSState(dir, nodeID)
	if err == nil && state == nil {
		err = ErrMissingState
	}
	return state, err
}

func readTLSState(directory, nodeID string) ([]byte, error) {
	f, err := openRegular(filepath.Join(directory, fileStem(nodeID)+".tls.json"), os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("Cannot read retained TLS identity")
	}
	defer f.Close()
	if err = checkPrivateConnectionFile(f); err != nil {
		return nil, errors.New("TLS identity file permissions must be private")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxStateBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > maxStateBytes {
		return nil, errors.New("Invalid retained TLS identity")
	}
	return raw, nil
}

// CreateTLSState publishes once, never replacing an existing identity. A
// failure may follow publication; callers reopen state before another attempt.
func (r *Reservation) CreateTLSState(next []byte) error {
	return r.saveTLSState(nil, next)
}

// ReplaceTLSState is for explicit offline renewal only. Complete-byte compare
// prevents a stale renewal request from replacing a more recent identity.
func (r *Reservation) ReplaceTLSState(previous, next []byte) error {
	if len(previous) == 0 {
		return errors.New("A retained TLS identity is required")
	}
	return r.saveTLSState(previous, next)
}

func (r *Reservation) saveTLSState(previous, next []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(next) == 0 || len(next) > maxStateBytes {
		return errors.New("Invalid TLS identity size")
	}
	current, err := r.tlsState()
	if err != nil {
		return err
	}
	if !bytes.Equal(current, previous) {
		return errors.New("Retained TLS identity already exists or changed")
	}
	f, err := os.CreateTemp(r.dir, ".tls-*")
	if err != nil {
		return errors.New("Cannot create retained TLS identity")
	}
	name := f.Name()
	defer os.Remove(name) // Only this call's unpublished disposable temporary file.
	defer f.Close()
	if err = protectConnectionFile(f); err != nil {
		return err
	}
	n, err := r.ops.write(f, next)
	if err != nil || n != len(next) {
		return errors.New("Cannot write retained TLS identity")
	}
	if err = r.ops.sync(f); err != nil {
		return errors.New("Cannot sync retained TLS identity")
	}
	if err = r.ops.close(f); err != nil {
		return errors.New("Cannot close retained TLS identity")
	}
	if err = r.ops.replace(name, filepath.Join(r.dir, fileStem(r.nodeID)+".tls.json"), current != nil); err != nil {
		return errors.New("Cannot publish retained TLS identity")
	}
	if err = r.ops.syncDir(r.dir); err != nil {
		return errors.New("Cannot sync retained TLS directory")
	}
	return nil
}
