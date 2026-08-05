package lock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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
	defer lk2.Release()
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
	defer lk.Release()
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
	lk.Release()
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
	lk.Release()
	if err := Remove(root, "gate"); err != nil {
		t.Fatalf("remove stale lock should succeed, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".kern", "locks", "gate.lock")); !os.IsNotExist(err) {
		t.Errorf("lock file should be gone, got %v", err)
	}
}
