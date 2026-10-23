// Package origin reserves monotonic node sequences outside a database snapshot.
// Its state directory must survive restores and must not be rolled back.
package origin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"unicode/utf8"
)

const (
	MaxSequence   int64 = 9_007_199_254_740_991
	BlockSize     int64 = 65_536
	formatVersion       = 1
	maxStateBytes       = 4_096
)

var (
	ErrLocked       = errors.New("origin: identity already locked")
	ErrInvalidState = errors.New("origin: invalid reservation state")
	ErrMissingState = errors.New("origin: reservation state is missing")
	ErrExhausted    = errors.New("origin: sequence exhausted")
	ErrClosed       = errors.New("origin: reservation is closed")
)

// Options identifies the independent, retained identity state. Directory must
// be dedicated to allocator state (0700 on Linux), outside database snapshots.
// MinimumSequence is the latest sequence already known to the caller's DB.
// RequireExisting must be true when restoring an existing identity: missing
// state cannot safely be bootstrapped from an older database.
type Options struct {
	Directory       string
	NodeID          string
	MinimumSequence int64
	RequireExisting bool
}

// Reservation owns an OS lock until Close. Next and Close are goroutine safe.
// Do not copy a Reservation. An I/O failure permanently stops this instance;
// close it and reopen the retained state before attempting further issuance.
type Reservation struct {
	mu       sync.Mutex
	dir      string
	path     string
	nodeID   string
	lock     *os.File
	high     int64
	next     int64
	exists   bool
	closed   bool
	failure  error
	closeErr error
	ops      persistenceOps
}

type manifest struct {
	Version   int    `json:"version"`
	NodeID    string `json:"nodeId"`
	HighWater int64  `json:"highWater"`
}

// Open locks the identity and durably reserves a block before returning.
// The first Next exceeds both the previous reservation and MinimumSequence.
// Reopening always skips the previous block, including its unissued numbers.
// Lock contention returns immediately with ErrLocked.
func Open(options Options) (*Reservation, error) {
	return openWithOps(options, defaultPersistenceOps())
}

func openWithOps(options Options, ops persistenceOps) (_ *Reservation, err error) {
	if options.Directory == "" {
		return nil, errors.New("origin: Directory is required")
	}
	if !utf8.ValidString(options.NodeID) || len(options.NodeID) == 0 || len(options.NodeID) > 128 {
		return nil, errors.New("origin: NodeID must contain 1..128 valid UTF-8 bytes")
	}
	if options.MinimumSequence < 0 || options.MinimumSequence > MaxSequence {
		return nil, errors.New("origin: MinimumSequence is out of range")
	}
	dir, err := prepareDirectory(options.Directory, !options.RequireExisting)
	if err != nil {
		return nil, err
	}
	stem := fileStem(options.NodeID)
	r := &Reservation{dir: dir, path: filepath.Join(dir, stem+".json"), nodeID: options.NodeID, ops: ops}
	r.lock, err = acquireLock(filepath.Join(dir, stem+".lock"))
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, r.Close())
		}
	}()
	// Make the permanent lock inode durable before publishing a manifest.
	if err = r.lock.Sync(); err != nil {
		return nil, fmt.Errorf("origin: sync lock: %w", err)
	}
	if err = syncDirectory(dir); err != nil {
		return nil, fmt.Errorf("origin: sync lock directory: %w", err)
	}
	m, err := readManifest(r.path, r.nodeID)
	if errors.Is(err, os.ErrNotExist) {
		if options.RequireExisting {
			return nil, fmt.Errorf("%w: %s", ErrMissingState, r.path)
		}
		err = nil
	} else if err != nil {
		return nil, err
	} else {
		r.high, r.exists = m.HighWater, true
	}
	base := max(r.high, options.MinimumSequence)
	if base == MaxSequence {
		// Retain a caller's exhausted minimum as well, so a later stale DB
		// cannot reopen this identity at a lower sequence.
		if !r.exists || r.high != MaxSequence {
			if err = r.persist(MaxSequence); err != nil {
				return nil, err
			}
		}
		return nil, ErrExhausted
	}
	if err = r.reserve(base); err != nil {
		return nil, err
	}
	return r, nil
}

// Next returns a positive sequence only after its entire block is durable.
// Failed calls return zero. Gaps, including whole blocks, are expected.
func (r *Reservation) Next() (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.lock == nil {
		return 0, ErrClosed
	}
	if r.failure != nil {
		return 0, r.failure
	}
	if r.next > r.high {
		if err := r.reserve(r.high); err != nil {
			r.failure = err
			return 0, err
		}
	}
	sequence := r.next
	r.next++ // MaxSequence+1 still fits int64; it is never returned.
	return sequence, nil
}

// Close releases the OS lock without deleting the permanent lock file.
// Repeated calls return the same close result; Next then returns ErrClosed.
func (r *Reservation) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		if r.lock != nil {
			r.closeErr = errors.Join(releaseLock(r.lock), r.lock.Close())
			r.lock = nil
		}
	}
	return r.closeErr
}

func fileStem(nodeID string) string {
	digest := sha256.Sum256([]byte(nodeID))
	return "origin-" + hex.EncodeToString(digest[:])
}

func (r *Reservation) reserve(base int64) error {
	if base >= MaxSequence {
		return ErrExhausted
	}
	// Subtraction before addition prevents overflow; the last block is short.
	high := base + min(BlockSize, MaxSequence-base)
	if err := r.persist(high); err != nil {
		return err
	}
	r.next = base + 1
	return nil
}

func readManifest(path, nodeID string) (manifest, error) {
	f, err := openRegular(path, os.O_RDONLY)
	if err != nil {
		return manifest{}, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, maxStateBytes+1))
	closeErr := f.Close()
	if err = errors.Join(readErr, closeErr); err != nil {
		return manifest{}, fmt.Errorf("origin: read state: %w", err)
	}
	m, err := decodeManifest(data, nodeID)
	if err != nil {
		return manifest{}, fmt.Errorf("%w: %s: %v", ErrInvalidState, path, err)
	}
	return m, nil
}

// Decode fields individually: encoding/json's struct decoder accepts duplicate
// keys, case aliases, missing values, and null for non-pointer scalar fields.
func decodeManifest(data []byte, nodeID string) (manifest, error) {
	var m manifest
	if len(data) > maxStateBytes || !utf8.Valid(data) {
		return m, errors.New("oversize or invalid UTF-8 JSON")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return m, errors.New("expected a JSON object")
	}
	seen := make(map[string]bool, 3)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return m, err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return m, errors.New("invalid or duplicate key")
		}
		seen[key] = true
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil {
			return m, err
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return m, errors.New("null field")
		}
		switch key {
		case "version":
			err = json.Unmarshal(raw, &m.Version)
		case "nodeId":
			err = json.Unmarshal(raw, &m.NodeID)
			if err == nil && !validUnicodeEscapes(raw) {
				err = errors.New("unpaired Unicode surrogate")
			}
		case "highWater":
			err = json.Unmarshal(raw, &m.HighWater)
		default:
			return m, errors.New("unknown field")
		}
		if err != nil {
			return m, err
		}
	}
	if end, err := d.Token(); err != nil || end != json.Delim('}') {
		return m, errors.New("unterminated object")
	}
	if _, err := d.Token(); err != io.EOF {
		return m, errors.New("trailing data")
	}
	if len(seen) != 3 || m.Version != formatVersion || m.NodeID != nodeID || m.HighWater < 0 || m.HighWater > MaxSequence {
		return m, errors.New("missing fields, unsupported version, identity mismatch, or invalid high water")
	}
	return m, nil
}

// encoding/json replaces unpaired UTF-16 escapes with U+FFFD. Reject them
// rather than silently changing a corrupt on-disk identity into another ID.
func validUnicodeEscapes(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++ // An escaped backslash must not start another Unicode escape.
		if i >= len(raw) || raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		value, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil || value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+10 >= len(raw) || raw[i+5] != '\\' || raw[i+6] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+7:i+11]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 10
		} else {
			i += 4
		}
	}
	return true
}
