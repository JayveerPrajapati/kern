package main

import (
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/lock"
)

// QA F4: `--wait N` bounds the WAIT to acquire — a held lock is retried
// instead of failing fast, and the budget is respected.

func TestAcquireLockWithWaitFailFast(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	first, err := lock.Acquire(root, "scope")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	// wait <= 0 keeps today's fail-fast behavior.
	start := time.Now()
	if _, err := acquireLockWithWait(root, "scope", 0); err == nil {
		t.Fatal("expected immediate contention failure")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("wait=0 must not retry")
	}
}

func TestAcquireLockWithWaitTimesOut(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	first, err := lock.Acquire(root, "scope")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	start := time.Now()
	if _, err := acquireLockWithWait(root, "scope", 1); err == nil {
		t.Fatal("expected contention failure after the wait budget")
	}
	elapsed := time.Since(start)
	if elapsed < 800*time.Millisecond {
		t.Fatalf("gave up too early: %v", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("waited past the budget: %v", elapsed)
	}
}

func TestAcquireLockWithWaitAcquiresAfterRelease(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	first, err := lock.Acquire(root, "scope")
	if err != nil {
		t.Fatal(err)
	}
	// Free the lock partway through the wait budget; the retry loop
	// must pick it up.
	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := first.Release(); err != nil {
			t.Errorf("release: %v", err)
		}
	}()
	lk, err := acquireLockWithWait(root, "scope", 5)
	if err != nil {
		t.Fatalf("expected acquisition after release: %v", err)
	}
	lk.Release()
}

func TestAcquireLockWithWaitFree(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lk, err := acquireLockWithWait(root, "free-scope", 1)
	if err != nil {
		t.Fatal(err)
	}
	lk.Release()
}
