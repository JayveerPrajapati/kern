package cache

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// probeLock reports whether mu is free: it attempts to lock in a goroutine and
// reports whether the lock was acquired within timeout. The probe goroutine
// ALWAYS releases the mutex (deferred unlock) — a probe whose timeout fires
// must not leave the mutex held by a leaked goroutine, or every later probe
// on the same mutex would block forever (the observed flake: the negative
// probe's goroutine won the lock after the holder released and never
// unlocked, so the positive probe timed out).
func probeLock(t *testing.T, mu *sync.Mutex, timeout time.Duration) bool {
	t.Helper()
	acquired := make(chan struct{})
	go func() {
		mu.Lock()
		defer mu.Unlock()
		close(acquired)
	}()
	select {
	case <-acquired:
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
	// Generous window: under a fully-loaded CI machine the woken probe
	// goroutine can take a while to be scheduled; the assertion itself is
	// what matters, not the latency.
	if !probeLock(t, b, 10*time.Second) {
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
