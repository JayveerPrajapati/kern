package cache

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// probeLock reports whether mu is free: it attempts to lock in a goroutine and
// reports whether the lock was acquired within timeout, unlocking when it was.
// If the lock is held, the probe goroutine stays blocked until the holder
// releases (at which point it acquires and unlocks itself).
func probeLock(t *testing.T, mu *sync.Mutex, timeout time.Duration) bool {
	t.Helper()
	done := make(chan struct{})
	go func() {
		mu.Lock()
		close(done)
	}()
	select {
	case <-done:
		mu.Unlock()
		return true
	case <-time.After(timeout):
		return false
	}
}

func TestPathLockSamePathSerializes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.json")
	a := PathLock(path)
	b := PathLock(path)
	if a != b {
		t.Fatal("PathLock called twice with the same path must return the same mutex")
	}

	a.Lock()
	if probeLock(t, b, 300*time.Millisecond) {
		t.Fatal("second lock on the same path acquired while the first was held")
	}
	a.Unlock()

	if !probeLock(t, b, time.Second) {
		t.Fatal("second lock on the same path did not acquire after the first released")
	}
}

func TestPathLockDifferentPathsDoNotBlock(t *testing.T) {
	dir := t.TempDir()
	a := PathLock(filepath.Join(dir, "a.json"))
	b := PathLock(filepath.Join(dir, "b.json"))
	if a == b {
		t.Fatal("PathLock called with different paths must return distinct mutexes")
	}

	a.Lock()
	defer a.Unlock()
	if !probeLock(t, b, time.Second) {
		t.Fatal("lock on an unrelated path blocked while another path was held")
	}
}

func TestPathLockRoundTrip(t *testing.T) {
	mu := PathLock(filepath.Join(t.TempDir(), "rt.json"))
	mu.Lock()
	pathHeld := mu != nil
	mu.Unlock()
	if !pathHeld || !probeLock(t, mu, time.Second) {
		t.Fatal("mutex not reacquirable after Unlock")
	}
}
