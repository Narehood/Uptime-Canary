package origin

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// ConnectionState uses the reservation's existing lifetime lock and retained
// private directory. It is deliberately separate from the sequence manifest
// and from database snapshots. No caller can choose another filename.
func (r *Reservation) ConnectionState() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.connectionState()
}

func (r *Reservation) connectionState() ([]byte, error) {
	if r.closed || r.lock == nil {
		return nil, ErrClosed
	}
	f, err := openRegular(filepath.Join(r.dir, fileStem(r.nodeID)+".connection.json"), os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("Cannot read retained node connection identity")
	}
	defer f.Close()
	if err := checkPrivateConnectionFile(f); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, maxStateBytes+1))
	if err != nil || len(b) == 0 || len(b) > maxStateBytes {
		return nil, errors.New("Invalid retained node connection identity")
	}
	return b, nil
}

// SaveConnectionState compares the complete previous bytes before replacement.
// A failure may follow publication, so callers must stop using their old copy
// and reopen the retained identity before trying again.
func (r *Reservation) SaveConnectionState(previous, next []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(next) == 0 || len(next) > maxStateBytes {
		return errors.New("Invalid node connection identity size")
	}
	current, err := r.connectionState()
	if err != nil {
		return err
	}
	if !bytes.Equal(current, previous) {
		return errors.New("Retained node connection identity changed")
	}
	f, err := os.CreateTemp(r.dir, ".connection-*")
	if err != nil {
		return errors.New("Cannot create retained node connection identity")
	}
	name := f.Name()
	defer os.Remove(name) // This call's own disposable temporary file only.
	defer f.Close()
	if err = protectConnectionFile(f); err != nil {
		return err
	}
	n, err := r.ops.write(f, next)
	if err != nil || n != len(next) {
		return errors.New("Cannot write retained node connection identity")
	}
	if err = r.ops.sync(f); err != nil {
		return errors.New("Cannot sync retained node connection identity")
	}
	if err = r.ops.close(f); err != nil {
		return errors.New("Cannot close retained node connection identity")
	}
	if err = r.ops.replace(name, filepath.Join(r.dir, fileStem(r.nodeID)+".connection.json"), current != nil); err != nil {
		return errors.New("Cannot publish retained node connection identity")
	}
	if err = r.ops.syncDir(r.dir); err != nil {
		return errors.New("Cannot sync retained node connection directory")
	}
	return nil
}
