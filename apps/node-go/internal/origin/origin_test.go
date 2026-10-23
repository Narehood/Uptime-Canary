package origin

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// privateTempDir models the allocator's explicit private-directory contract
// independently of the test runner's umask. Permission-rejection tests still
// change modes explicitly after creating their fixtures.
func privateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func openTest(t *testing.T, options Options) *Reservation {
	t.Helper()
	r, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r
}

func nextTest(t *testing.T, r *Reservation, want int64) {
	t.Helper()
	sequence, err := r.Next()
	if err != nil || sequence != want {
		t.Fatalf("Next = %d, %v; want %d", sequence, err, want)
	}
	m, err := readManifest(r.path, r.nodeID)
	if err != nil || m.HighWater < sequence {
		t.Fatalf("sequence %d was issued without a retained reservation: %+v, %v", sequence, m, err)
	}
}

func writeState(t *testing.T, dir, nodeID string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, fileStem(nodeID)+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func encodedState(t *testing.T, nodeID string, high int64) []byte {
	t.Helper()
	data, err := json.Marshal(manifest{Version: formatVersion, NodeID: nodeID, HighWater: high})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestBootstrapReopenMinimumAndPermanentLock(t *testing.T) {
	dir := filepath.Join(privateTempDir(t), "nested", "identity")
	options := Options{Directory: dir, NodeID: "node:with/Windows<>bad|?*names", MinimumSequence: 42}
	r := openTest(t, options)
	if r.high != 42+BlockSize {
		t.Fatalf("high = %d", r.high)
	}
	nextTest(t, r, 43)
	nextTest(t, r, 44)
	lockPath := filepath.Join(dir, fileStem(options.NodeID)+".lock")
	before, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := r.Next(); n != 0 || !errors.Is(err, ErrClosed) {
		t.Fatalf("Next after Close = %d, %v", n, err)
	}
	options.RequireExisting = true
	options.MinimumSequence = 1 // A restored database is behind retained state.
	r = openTest(t, options)
	nextTest(t, r, 42+BlockSize+1)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	options.MinimumSequence = 4 * BlockSize
	r = openTest(t, options)
	nextTest(t, r, options.MinimumSequence+1)
	after, err := os.Stat(lockPath)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("permanent lock inode changed: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("unexpected identity files: %v, %v", entries, err)
	}
}

func TestMissingRequiredState(t *testing.T) {
	for _, existingDir := range []bool{false, true} {
		t.Run(strconv.FormatBool(existingDir), func(t *testing.T) {
			dir := filepath.Join(privateTempDir(t), "identity")
			if existingDir {
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if r, err := Open(Options{Directory: dir, NodeID: "n", RequireExisting: true}); r != nil || !errors.Is(err, ErrMissingState) {
				t.Fatalf("Open required absent state = %v, %v", r, err)
			}
			if _, err := os.Stat(filepath.Join(dir, fileStem("n")+".json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing state initialized: %v", err)
			}
			// Failure must release any acquired lock.
			r := openTest(t, Options{Directory: dir, NodeID: "n"})
			nextTest(t, r, 1)
		})
	}
}

func TestInvalidOptions(t *testing.T) {
	base := Options{Directory: privateTempDir(t), NodeID: "n"}
	for name, change := range map[string]func(*Options){
		"directory":  func(o *Options) { o.Directory = "" },
		"empty ID":   func(o *Options) { o.NodeID = "" },
		"long ID":    func(o *Options) { o.NodeID = strings.Repeat("a", 129) },
		"invalid ID": func(o *Options) { o.NodeID = string([]byte{0xff}) },
		"negative":   func(o *Options) { o.MinimumSequence = -1 },
		"too large":  func(o *Options) { o.MinimumSequence = MaxSequence + 1 },
		"overflow":   func(o *Options) { o.MinimumSequence = 1<<63 - 1 },
	} {
		t.Run(name, func(t *testing.T) {
			o := base
			change(&o)
			if r, err := Open(o); r != nil || err == nil {
				t.Fatalf("invalid options accepted: %v, %v", r, err)
			}
		})
	}
}

func TestStrictManifestDoesNotInitializeOrOverwrite(t *testing.T) {
	for name, data := range map[string]string{
		"empty":           "",
		"short":           `{"version":1`,
		"array":           `[]`,
		"null":            `null`,
		"string":          `"text"`,
		"missing":         `{"version":1,"nodeId":"n"}`,
		"identity":        `{"version":1,"nodeId":"other","highWater":0}`,
		"version":         `{"version":2,"nodeId":"n","highWater":0}`,
		"old version":     `{"version":0,"nodeId":"n","highWater":0}`,
		"version null":    `{"version":null,"nodeId":"n","highWater":0}`,
		"ID null":         `{"version":1,"nodeId":null,"highWater":0}`,
		"water null":      `{"version":1,"nodeId":"n","highWater":null}`,
		"duplicate":       `{"version":1,"nodeId":"n","highWater":2,"highWater":0}`,
		"escaped dup":     `{"version":1,"nodeId":"n","highWater":2,"high\u0057ater":0}`,
		"duplicate ID":    `{"version":1,"nodeId":"other","nodeId":"n","highWater":0}`,
		"case alias":      `{"Version":1,"nodeId":"n","highWater":0}`,
		"unknown":         `{"version":1,"nodeId":"n","highWater":0,"extra":true}`,
		"negative":        `{"version":1,"nodeId":"n","highWater":-1}`,
		"beyond limit":    `{"version":1,"nodeId":"n","highWater":9007199254740992}`,
		"int64 overflow":  `{"version":1,"nodeId":"n","highWater":9223372036854775808}`,
		"float":           `{"version":1,"nodeId":"n","highWater":1.0}`,
		"exponent":        `{"version":1,"nodeId":"n","highWater":1e2}`,
		"quoted number":   `{"version":1,"nodeId":"n","highWater":"1"}`,
		"bool":            `{"version":1,"nodeId":"n","highWater":true}`,
		"nested":          `{"version":1,"nodeId":"n","highWater":{}}`,
		"ID numeric":      `{"version":1,"nodeId":12,"highWater":0}`,
		"version string":  `{"version":"1","nodeId":"n","highWater":0}`,
		"trailing object": `{"version":1,"nodeId":"n","highWater":0}{}`,
		"trailing token":  `{"version":1,"nodeId":"n","highWater":0}x`,
		"invalid utf8":    "{\"version\":1,\"nodeId\":\"\xff\",\"highWater\":0}",
		"oversize":        strings.Repeat(" ", maxStateBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			dir := privateTempDir(t)
			path := writeState(t, dir, "n", []byte(data))
			for _, required := range []bool{false, true} {
				r, err := Open(Options{Directory: dir, NodeID: "n", RequireExisting: required})
				if r != nil || !errors.Is(err, ErrInvalidState) {
					t.Fatalf("Open corrupt state = %v, %v", r, err)
				}
				got, err := os.ReadFile(path)
				if err != nil || string(got) != data {
					t.Fatalf("corrupt state was modified: %q, %v", got, err)
				}
			}
		})
	}
}

func TestManifestMaximumAndExactIdentity(t *testing.T) {
	dir := privateTempDir(t)
	nodeID := strings.Repeat("é", 64) // 128 bytes, preserved without normalization.
	data := encodedState(t, nodeID, 5)
	data = append(data, bytes.Repeat([]byte(" "), maxStateBytes-len(data))...)
	writeState(t, dir, nodeID, data)
	r := openTest(t, Options{Directory: dir, NodeID: nodeID, RequireExisting: true})
	nextTest(t, r, 6)
}

func TestManifestUnicodeEscapes(t *testing.T) {
	for _, test := range []struct {
		id   string
		raw  string
		good bool
	}{
		{"�", `"\ud800"`, false},
		{"�", `"\udfff"`, false},
		{"��", `"\ud800\ud800"`, false},
		{"�x", `"\ud800x"`, false},
		{"😀", `"\ud83d\ude00"`, true},
		{"�", `"\ufffd"`, true},
		{`\ud800`, `"\\ud800"`, true},
	} {
		data := []byte(`{"version":1,"nodeId":` + test.raw + `,"highWater":0}`)
		if _, err := decodeManifest(data, test.id); (err == nil) != test.good {
			t.Errorf("decode ID %s: %v; want good=%v", test.raw, err, test.good)
		}
	}
}

func TestIndependentIDsAndUnrelatedFiles(t *testing.T) {
	dir := privateTempDir(t)
	unrelated := filepath.Join(dir, "do-not-touch.txt")
	if err := os.WriteFile(unrelated, []byte("retained"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"n", "N", "CON:<bad>|?*/\\.", "node:one"} {
		r := openTest(t, Options{Directory: dir, NodeID: id})
		nextTest(t, r, 1)
	}
	got, err := os.ReadFile(unrelated)
	if err != nil || string(got) != "retained" {
		t.Fatalf("unrelated file changed: %q, %v", got, err)
	}
	// A nonempty file in the lock namespace must also be left untouched.
	lockPath := filepath.Join(dir, fileStem("other")+".lock")
	if err := os.WriteFile(lockPath, []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r, err := Open(Options{Directory: dir, NodeID: "other"}); r != nil || !errors.Is(err, ErrInvalidState) {
		t.Fatalf("nonempty lock accepted: %v, %v", r, err)
	}
	got, err = os.ReadFile(lockPath)
	if err != nil || string(got) != "unrelated" {
		t.Fatalf("lock namespace file overwritten: %q, %v", got, err)
	}
}

func TestBlockBoundaryAndConcurrentNext(t *testing.T) {
	r := openTest(t, Options{Directory: privateTempDir(t), NodeID: "n"})
	// Cross a block boundary without allocating an unbounded number of results.
	for want := int64(1); want <= BlockSize-1; want++ {
		n, err := r.Next()
		if n != want || err != nil {
			t.Fatalf("Next = %d, %v; want %d", n, err, want)
		}
	}
	const workers, each = 16, 64
	sequences := make(chan int64, workers*each)
	errCh := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for range each {
				n, err := r.Next()
				if err != nil {
					errCh <- err
					return
				}
				sequences <- n
			}
		})
	}
	wg.Wait()
	close(sequences)
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	all := make([]int64, 0, workers*each)
	for n := range sequences {
		all = append(all, n)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	if len(all) != workers*each {
		t.Fatalf("got %d sequences", len(all))
	}
	for i, n := range all {
		if want := BlockSize + int64(i); n != want {
			t.Fatalf("sequence[%d] = %d; want %d", i, n, want)
		}
	}
	m, err := readManifest(r.path, r.nodeID)
	if err != nil || m.HighWater != 2*BlockSize {
		t.Fatalf("next block not durable: %+v, %v", m, err)
	}
}

func TestConcurrentCloseAndNext(t *testing.T) {
	r := openTest(t, Options{Directory: privateTempDir(t), NodeID: "n"})
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 100 {
				n, err := r.Next()
				if err != nil && (!errors.Is(err, ErrClosed) || n != 0) {
					t.Errorf("Next during Close = %d, %v", n, err)
				}
			}
		})
		wg.Go(func() {
			if err := r.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	r2 := openTest(t, Options{Directory: r.dir, NodeID: "n", RequireExisting: true})
	nextTest(t, r2, BlockSize+1)
}

func TestExhaustionIsRetainedAndBounded(t *testing.T) {
	for _, minimum := range []int64{MaxSequence - 3, MaxSequence - BlockSize, MaxSequence} {
		t.Run(strconv.FormatInt(minimum, 10), func(t *testing.T) {
			dir := privateTempDir(t)
			r, err := Open(Options{Directory: dir, NodeID: "n", MinimumSequence: minimum})
			if minimum == MaxSequence {
				if r != nil || !errors.Is(err, ErrExhausted) {
					t.Fatalf("exhausted bootstrap = %v, %v", r, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { r.Close() })
				m, err := readManifest(r.path, "n")
				if err != nil || m.HighWater != MaxSequence {
					t.Fatalf("last reservation = %+v, %v", m, err)
				}
				if minimum == MaxSequence-3 {
					for want := minimum + 1; want <= MaxSequence; want++ {
						nextTest(t, r, want)
					}
					for range 2 {
						if n, err := r.Next(); n != 0 || !errors.Is(err, ErrExhausted) {
							t.Fatalf("exhausted Next = %d, %v", n, err)
						}
					}
				}
				r.Close()
			}
			// An older restored minimum cannot reopen the exhausted identity.
			if r, err := Open(Options{Directory: dir, NodeID: "n", RequireExisting: true}); r != nil || !errors.Is(err, ErrExhausted) {
				t.Fatalf("exhaustion did not survive reopen: %v, %v", r, err)
			}
		})
	}
	// Advancing an existing state to an exhausted minimum is also permanent.
	dir := privateTempDir(t)
	r := openTest(t, Options{Directory: dir, NodeID: "n"})
	r.Close()
	if _, err := Open(Options{Directory: dir, NodeID: "n", MinimumSequence: MaxSequence}); !errors.Is(err, ErrExhausted) {
		t.Fatal(err)
	}
	m, err := readManifest(filepath.Join(dir, fileStem("n")+".json"), "n")
	if err != nil || m.HighWater != MaxSequence {
		t.Fatalf("exhausted minimum not persisted: %+v, %v", m, err)
	}
}

func TestSameIdentityTwoOwners(t *testing.T) {
	dir := privateTempDir(t)
	r := openTest(t, Options{Directory: dir, NodeID: "n"})
	for _, path := range []string{dir, filepath.Join(dir, "..", filepath.Base(dir))} {
		if second, err := Open(Options{Directory: path, NodeID: "n", MinimumSequence: 500}); second != nil || !errors.Is(err, ErrLocked) {
			t.Fatalf("second owner = %v, %v", second, err)
		}
	}
	nextTest(t, r, 1)
}

func TestAliasOwners(t *testing.T) {
	parent := privateTempDir(t)
	dir := filepath.Join(parent, "real")
	r := openTest(t, Options{Directory: dir, NodeID: "n"})
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(dir, alias); err != nil {
		if runtime.GOOS != "windows" {
			t.Skipf("symlinks unavailable: %v", err)
		}
		// A junction is a directory alias available without symlink privilege.
		cmd := exec.Command("cmd", "/c", "mklink", "/J", alias, dir)
		if output, junctionErr := cmd.CombinedOutput(); junctionErr != nil {
			t.Skipf("directory aliases unavailable: %v; %v; %s", err, junctionErr, output)
		}
	}
	if second, err := Open(Options{Directory: alias, NodeID: "n"}); second != nil || !errors.Is(err, ErrLocked) {
		t.Fatalf("alias owner = %v, %v", second, err)
	}
	probeProcess(t, alias, "n")
	r.Close()
	r = openTest(t, Options{Directory: alias, NodeID: "n", RequireExisting: true})
	nextTest(t, r, BlockSize+1)
	// A nonexistent child beneath a symlink parent resolves to the same lock.
	sub := openTest(t, Options{Directory: filepath.Join(alias, "nested"), NodeID: "n"})
	if second, err := Open(Options{Directory: filepath.Join(dir, "nested"), NodeID: "n"}); second != nil || !errors.Is(err, ErrLocked) {
		t.Fatalf("aliased parent owner = %v, %v", second, err)
	}
	nextTest(t, sub, 1)
}

func probeProcess(t *testing.T, dir, nodeID string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockProcessHelper$", "--", "origin-helper", "probe", dir, nodeID)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("process lock probe: %v\n%s", err, output)
	}
}

func TestProcessLockAndUncleanExit(t *testing.T) {
	dir := privateTempDir(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockProcessHelper$", "--", "origin-helper", "hold", dir, "n")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		stdin.Close()
		if !waited {
			cmd.Wait()
		}
	})
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "READY 1" {
		t.Fatalf("child did not reserve: %q, %v; %s", scanner.Text(), scanner.Err(), stderr.String())
	}
	if r, err := Open(Options{Directory: dir, NodeID: "n"}); r != nil || !errors.Is(err, ErrLocked) {
		t.Fatalf("child owner not exclusive: %v, %v", r, err)
	}
	probeProcess(t, dir, "n")
	// The child exits via os.Exit, bypassing Close and deferred cleanup.
	stdin.Close()
	err = cmd.Wait()
	waited = true
	if err != nil {
		t.Fatalf("child exit: %v; %s", err, stderr.String())
	}
	r := openTest(t, Options{Directory: dir, NodeID: "n", RequireExisting: true})
	nextTest(t, r, BlockSize+1)
}

func TestLockProcessHelper(t *testing.T) {
	for i, arg := range os.Args {
		if arg != "origin-helper" {
			continue
		}
		if len(os.Args) != i+4 {
			t.Fatal("invalid helper arguments")
		}
		mode, dir, nodeID := os.Args[i+1], os.Args[i+2], os.Args[i+3]
		r, err := Open(Options{Directory: dir, NodeID: nodeID})
		if mode == "probe" {
			if r != nil || !errors.Is(err, ErrLocked) {
				if r != nil {
					r.Close()
				}
				t.Fatalf("process accepted second owner: %v, %v", r, err)
			}
			return
		}
		if mode != "hold" || err != nil {
			t.Fatalf("hold Open: %v", err)
		}
		n, err := r.Next()
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("READY %d\n", n)
		io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
}
