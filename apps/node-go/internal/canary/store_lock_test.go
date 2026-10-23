package canary

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDatabaseOwnershipIsExclusiveAndReleasedAfterFailedStartup(t *testing.T) {
	o := Options{NodeID: "owner", DBPath: filepath.Join(t.TempDir(), "node.db"), Port: Ptr(0), AdminToken: adminToken, ClusterToken: clusterToken}
	first, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Stop() })
	if second, err := New(o); err == nil {
		_ = second.Stop()
		t.Fatal("two nodes acquired the same database")
	} else if !strings.Contains(err.Error(), "already in use") {
		t.Fatal("ownership error did not identify the conflict", err)
	}
	if err = first.Stop(); err != nil {
		t.Fatal(err)
	}
	wrong := o
	wrong.NodeID = "different-owner"
	if n, err := New(wrong); err == nil {
		_ = n.Stop()
		t.Fatal("database identity changed unexpectedly")
	}
	restarted, err := New(o)
	if err != nil {
		t.Fatal("failed startup retained the database ownership lock", err)
	}
	if err = restarted.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseLockProcessHelper(t *testing.T) {
	path := os.Getenv("CANARY_DB_LOCK_TEST_PATH")
	if path == "" {
		return
	}
	s, err := openStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	fmt.Println("DATABASE_LOCK_READY")
	select {}
}

func TestDatabaseOwnershipSurvivesProcessDeathWithoutStaleLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crash.db")
	cmd := exec.Command(os.Args[0], "-test.run=^TestDatabaseLockProcessHelper$", "-test.timeout=20s")
	cmd.Env = append(os.Environ(), "CANARY_DB_LOCK_TEST_PATH="+path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	ready := make(chan bool, 1)
	go func() {
		s := bufio.NewScanner(stdout)
		for s.Scan() {
			if s.Text() == "DATABASE_LOCK_READY" {
				ready <- true
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("lock owner exited before acquiring database")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("database lock owner did not start")
	}
	if other, err := openStore(path); err == nil {
		_ = other.close()
		t.Fatal("another process opened the owned database")
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	waited = true
	reopened, err := openStore(path)
	if err != nil {
		t.Fatal("kernel did not release ownership after process death", err)
	}
	if err = reopened.close(); err != nil {
		t.Fatal(err)
	}
}
