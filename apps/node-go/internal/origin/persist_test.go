package origin

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

var errInjected = errors.New("injected persistence failure")

func failAt(stage string) persistenceOps {
	ops := defaultPersistenceOps()
	switch stage {
	case "write":
		ops.write = func(f *os.File, data []byte) (int, error) {
			n, _ := f.Write(data[:len(data)/2])
			return n, errInjected
		}
	case "short write":
		ops.write = func(f *os.File, data []byte) (int, error) {
			return f.Write(data[:len(data)/2])
		}
	case "sync":
		ops.sync = func(*os.File) error { return errInjected }
	case "close":
		ops.close = func(f *os.File) error {
			f.Close()
			return errInjected
		}
	case "replace":
		ops.replace = func(string, string, bool) error { return errInjected }
	case "directory sync":
		ops.syncDir = func(string) error { return errInjected }
	}
	return ops
}

func TestInitialPersistenceFailures(t *testing.T) {
	for _, stage := range []string{"write", "short write", "sync", "close", "replace", "directory sync"} {
		t.Run(stage, func(t *testing.T) {
			dir := privateTempDir(t)
			options := Options{Directory: dir, NodeID: "n", MinimumSequence: 23}
			if r, err := openWithOps(options, failAt(stage)); r != nil || err == nil {
				t.Fatalf("failed initial reservation returned an owner: %v, %v", r, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() != fileStem("n")+".lock" && entry.Name() != fileStem("n")+".json" {
					t.Fatalf("failed reserve leaked temporary file %s", entry.Name())
				}
			}
			if stage != "directory sync" {
				if _, err := os.Stat(filepath.Join(dir, fileStem("n")+".json")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed reserve published a partial manifest: %v", err)
				}
			}
			r := openTest(t, options) // Failed Open must release its lock.
			want := int64(24)
			if stage == "directory sync" {
				want += BlockSize // Ambiguous replace is skipped on reopen.
			}
			nextTest(t, r, want)
		})
	}
}

func TestRefillPersistenceFailuresPoisonReservation(t *testing.T) {
	for _, stage := range []string{"write", "short write", "sync", "close", "replace", "directory sync"} {
		t.Run(stage, func(t *testing.T) {
			dir := privateTempDir(t)
			options := Options{Directory: dir, NodeID: "n"}
			r := openTest(t, options)
			nextTest(t, r, 1)
			before, err := os.ReadFile(r.path)
			if err != nil {
				t.Fatal(err)
			}
			r.ops = failAt(stage)
			r.next = r.high + 1 // Burn the unused block, to reach refill directly.
			var firstError error
			for range 2 {
				if n, err := r.Next(); n != 0 || err == nil {
					t.Fatalf("Next issued %d after %s failure: %v", n, stage, err)
				} else if firstError == nil {
					firstError = err
				} else if err != firstError {
					t.Fatal("reservation retried after failure")
				}
			}
			if second, err := Open(options); second != nil || !errors.Is(err, ErrLocked) {
				t.Fatalf("failed owner dropped its lock: %v, %v", second, err)
			}
			if stage != "directory sync" {
				after, err := os.ReadFile(r.path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("last complete reservation changed: %s, %v", after, err)
				}
			}
			r.Close()
			r = openTest(t, options)
			want := BlockSize + 1
			if stage == "directory sync" {
				want += BlockSize
			}
			nextTest(t, r, want)
		})
	}
}

func TestNoSequenceBeforeDurablePublication(t *testing.T) {
	options := Options{Directory: privateTempDir(t), NodeID: "n"}
	reached := make(chan struct{})
	complete := make(chan struct{})
	defer func() {
		// Let a blocked goroutine finish if an assertion fails.
		select {
		case <-complete:
		default:
			close(complete)
		}
	}()
	ops := defaultPersistenceOps()
	ops.syncDir = func(path string) error {
		close(reached)
		<-complete
		return syncDirectory(path)
	}
	type result struct {
		r   *Reservation
		err error
	}
	opened := make(chan result, 1)
	go func() {
		r, err := openWithOps(options, ops)
		opened <- result{r, err}
	}()
	select {
	case <-reached:
	case got := <-opened:
		if got.r != nil {
			got.r.Close()
		}
		t.Fatalf("Open returned without final directory sync: %+v", got)
	}
	select {
	case got := <-opened:
		t.Fatalf("Open returned before directory persistence: %+v", got)
	default:
	}
	close(complete)
	got := <-opened
	if got.err != nil {
		t.Fatal(got.err)
	}
	r := got.r
	t.Cleanup(func() { r.Close() })
	nextTest(t, r, 1)

	reached = make(chan struct{})
	complete = make(chan struct{})
	r.next = r.high + 1
	type issuance struct {
		n   int64
		err error
	}
	issued := make(chan issuance, 1)
	go func() {
		n, err := r.Next()
		issued <- issuance{n, err}
	}()
	select {
	case <-reached:
	case got := <-issued:
		t.Fatalf("Next returned without final directory sync: %+v", got)
	}
	select {
	case got := <-issued:
		t.Fatalf("Next returned before directory persistence: %+v", got)
	default:
	}
	close(complete)
	sequence := <-issued
	if sequence.err != nil || sequence.n != BlockSize+1 {
		t.Fatalf("refill = %+v", sequence)
	}
}

func TestUnexpectedFileChangeNotOverwritten(t *testing.T) {
	r := openTest(t, Options{Directory: privateTempDir(t), NodeID: "n"})
	unrelated := []byte("unrelated replacement file")
	if err := os.WriteFile(r.path, unrelated, 0o600); err != nil {
		t.Fatal(err)
	}
	r.next = r.high + 1
	if n, err := r.Next(); n != 0 || !errors.Is(err, ErrInvalidState) {
		t.Fatalf("changed file accepted: %d, %v", n, err)
	}
	got, err := os.ReadFile(r.path)
	if err != nil || !bytes.Equal(got, unrelated) {
		t.Fatalf("unexpected file overwritten: %q, %v", got, err)
	}
}

func TestInitialPublicationDoesNotReplaceUnexpectedFile(t *testing.T) {
	dir := privateTempDir(t)
	path := filepath.Join(dir, fileStem("n")+".json")
	ops := defaultPersistenceOps()
	ops.replace = func(from, to string, existing bool) error {
		if err := os.WriteFile(to, []byte("other owner"), 0o600); err != nil {
			return err
		}
		return replaceFile(from, to, existing)
	}
	if r, err := openWithOps(Options{Directory: dir, NodeID: "n"}, ops); r != nil || err == nil {
		t.Fatalf("initial reservation overwrote an unexpected file: %v, %v", r, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "other owner" {
		t.Fatalf("unexpected file changed: %q, %v", got, err)
	}
}

func TestStateSymlinkAndHardlinkRejected(t *testing.T) {
	for _, name := range []string{"symlink", "hardlink"} {
		t.Run(name, func(t *testing.T) {
			dir := privateTempDir(t)
			target := filepath.Join(privateTempDir(t), "unrelated.json")
			data := encodedState(t, "n", 20)
			if err := os.WriteFile(target, data, 0o600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, fileStem("n")+".json")
			link := os.Symlink
			if name == "hardlink" {
				link = os.Link
			}
			if err := link(target, path); err != nil {
				t.Skipf("%s unavailable: %v", name, err)
			}
			if r, err := Open(Options{Directory: dir, NodeID: "n"}); r != nil || err == nil {
				t.Fatalf("%s state accepted: %v, %v", name, r, err)
			}
			got, err := os.ReadFile(target)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("unrelated target overwritten: %q, %v", got, err)
			}
		})
	}
}
