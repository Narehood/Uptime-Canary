package canary

import (
	"errors"
	"path/filepath"
	"uptime-canary/apps/node-go/internal/origin"
)

func (n *Node) openOrigin(directory string, requireExisting bool) error {
	if n.sequence < 0 || n.sequence > maxSafe {
		return errors.New("Invalid persisted local sequence")
	}
	if directory == "" {
		if n.store.lock == nil {
			return nil // Explicit in-memory databases are ephemeral test/runtime state.
		}
		directory = filepath.Join(filepath.Dir(n.store.lock.Name()), ".canary-identities")
	}
	reservation, err := origin.Open(origin.Options{Directory: directory, NodeID: n.ID, MinimumSequence: n.sequence, RequireExisting: requireExisting})
	if err != nil {
		return errors.New("Cannot reserve local sequences in the retained identity directory")
	}
	n.store.origin = reservation
	return nil
}

func (n *Node) nextProbeSequence() (int64, error) {
	if n.store.origin != nil {
		seq, err := n.store.origin.Next()
		if err != nil || seq <= n.sequence {
			return 0, errors.New("Local sequence reservation failed")
		}
		return seq, nil
	}
	if n.sequence < 0 || n.sequence >= maxSafe {
		return 0, errors.New("Local sequence is exhausted")
	}
	return n.sequence + 1, nil
}
