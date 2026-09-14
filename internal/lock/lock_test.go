package lock

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireReleaseRoundTrip(t *testing.T) {
	root := t.TempDir()
	lk, err := Acquire(root, "db-models")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(root, "db-models"); !errors.Is(err, ErrLocked) {
		t.Fatalf("second acquire must be ErrLocked, got %v", err)
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
	lk2, err := Acquire(root, "db-models")
	if err != nil {
		t.Fatalf("re-acquire after release should succeed, got %v", err)
	}
	defer func() { _ = lk2.Release() }()
}

func TestHeldReflectsLock(t *testing.T) {
	root := t.TempDir()
	held, _, err := Held(root, "checkout")
	if err != nil {
		t.Fatal(err)
	}
	if held {
		t.Fatal("fresh scope must not be held")
	}
	lk, err := Acquire(root, "checkout")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lk.Release() }()
	held, pid, err := Held(root, "checkout")
	if err != nil {
		t.Fatal(err)
	}
	if !held || pid == 0 {
		t.Errorf("held scope should report the holder pid, got held=%v pid=%d", held, pid)
	}
}

func TestListShowsScopes(t *testing.T) {
	root := t.TempDir()
	lk, err := Acquire(root, "checkout")
	if err != nil {
		t.Fatal(err)
	}
	sts, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sts) != 1 || sts[0].Scope != "checkout" || !sts[0].Held {
		t.Fatalf("expected checkout HELD, got %+v", sts)
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
	sts, err = List(root)
	if err != nil {
		t.Fatal(err)
	}
	if sts[0].Held {
		t.Errorf("lock should be free after release, got %+v", sts[0])
	}
}

func TestRemoveRefusesHeldAndCleansStale(t *testing.T) {
	root := t.TempDir()
	lk, err := Acquire(root, "gate")
	if err != nil {
		t.Fatal(err)
	}
	if err := Remove(root, "gate"); err == nil {
		t.Fatal("remove must refuse a held lock")
	}
	if err := lk.Release(); err != nil {
		t.Fatal(err)
	}
	if err := Remove(root, "gate"); err != nil {
		t.Fatalf("remove stale lock should succeed, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".kern", "locks", "gate.lock")); !os.IsNotExist(err) {
		t.Errorf("lock file should be gone, got %v", err)
	}
}

// TestCrossProcessFlock verifies the cross-process guarantee (E2): a lock held
// by another process is visible as ErrLocked here, and becomes acquirable the
// moment the holder dies — the kernel drops the flock on process exit, which
// is the PID-liveness property the MCP watch election and workspace locks
// rely on.
func TestCrossProcessFlock(t *testing.T) {
	root := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=TestLockHolderChildProcess", "--")
	child.Env = append(os.Environ(), "GO_WANT_LOCK_HOLDER=1", "KERN_LOCK_ROOT="+root)
	if err := child.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})

	// Wait until the child actually holds the lock.
	waitFor(t, func() bool {
		held, _, err := Held(root, "election-test")
		return err == nil && held
	}, "child never acquired the lock")

	// The parent must see the cross-process lock as held.
	if _, err := Acquire(root, "election-test"); !errors.Is(err, ErrLocked) {
		t.Fatalf("Acquire while child holds = %v, want ErrLocked", err)
	}
	// Held must report the holder's PID (from the child's stamp).
	held, pid, err := Held(root, "election-test")
	if err != nil || !held {
		t.Fatalf("Held = (%v, %d, %v), want (true, pid>0, nil)", held, pid, err)
	}
	if pid <= 0 {
		t.Errorf("Held pid = %d, want the child's pid", pid)
	}

	// Kill the holder; the flock is released by the kernel on process death.
	if err := child.Process.Kill(); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	_ = child.Wait()

	// The lock must become acquirable without any manual cleanup.
	waitFor(t, func() bool {
		l, err := Acquire(root, "election-test")
		if err != nil {
			return false
		}
		_ = l.Release()
		return true
	}, "lock never released after holder death")
}

// TestLockHolderChildProcess is not a real test: spawned by
// TestCrossProcessFlock, it acquires the lock and holds it until killed.
func TestLockHolderChildProcess(t *testing.T) {
	if os.Getenv("GO_WANT_LOCK_HOLDER") != "1" {
		return
	}
	root := os.Getenv("KERN_LOCK_ROOT")
	l, err := Acquire(root, "election-test")
	if err != nil {
		os.Exit(3)
	}
	defer func() { _ = l.Release() }()
	// Hold until killed. A long timer sleep (not select {}) so the runtime's
	// deadlock detector does not panic the child before the parent kills it.
	time.Sleep(24 * time.Hour)
}

func waitFor(t *testing.T, cond func() bool, failMsg string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(failMsg)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
