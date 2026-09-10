package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockFileAcquireReleaseReacquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")

	l, err := LockFile(path)
	if err != nil {
		t.Fatalf("LockFile: %v", err)
	}
	if l == nil {
		t.Fatal("LockFile returned a nil lock")
	}
	l.Unlock()

	// After release the lock is acquirable again.
	l2, err := LockFile(path)
	if err != nil {
		t.Fatalf("LockFile after release: %v", err)
	}
	l2.Unlock()

	// The sidecar is persistent: the API never deletes it, so lock identity
	// stays stable across processes.
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Fatalf("sidecar %q missing after lock/unlock: %v", path+".lock", err)
	}
}

func TestLockFileSecondAcquireBlocksUntilRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")

	first, err := LockFile(path)
	if err != nil {
		t.Fatalf("LockFile: %v", err)
	}

	acquired := make(chan *FileLock, 1)
	go func() {
		l, err := LockFile(path)
		if err != nil {
			t.Errorf("second LockFile: %v", err)
			acquired <- nil
			return
		}
		acquired <- l
	}()

	select {
	case l := <-acquired:
		if l != nil {
			l.Unlock()
		}
		t.Fatal("second LockFile acquired while the first lock was held")
	case <-time.After(300 * time.Millisecond):
		// Blocked as expected.
	}

	first.Unlock()

	select {
	case l := <-acquired:
		if l == nil {
			t.Fatal("second LockFile errored after the first released")
		}
		l.Unlock()
	case <-time.After(time.Second):
		t.Fatal("second LockFile did not acquire after the first released")
	}
}
