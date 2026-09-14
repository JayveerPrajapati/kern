package flock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLockReleaseRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "test.lock")
	f, err := Lock(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := Release(f); err != nil {
		t.Fatal(err)
	}
}

func TestTryLockContention(t *testing.T) {
	p := filepath.Join(t.TempDir(), "contend.lock")
	f, err := TryLock(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = Release(f) }()
	// A second handle to the same file must contend.
	if _, err := TryLock(p); err != ErrLocked {
		t.Fatalf("second TryLock = %v, want ErrLocked", err)
	}
	// After release, the lock is acquirable again.
	if err := Release(f); err != nil {
		t.Fatal(err)
	}
	f2, err := TryLock(p)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	if err := Release(f2); err != nil {
		t.Fatal(err)
	}
}

func TestLockFilePersistsAfterRelease(t *testing.T) {
	p := filepath.Join(t.TempDir(), "persist.lock")
	f, err := Lock(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := Release(f); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("lock sidecar should persist after release: %v", err)
	}
}
