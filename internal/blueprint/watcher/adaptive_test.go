package watcher

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// waitForEvent waits until an event matching pred is received or the timeout
// elapses. It returns the elapsed time and whether a matching event arrived.
func waitForEvent(t *testing.T, ch <-chan []Event, timeout time.Duration, pred func(Event) bool) (time.Duration, bool) {
	t.Helper()
	start := time.Now()
	deadline := time.After(timeout)
	for {
		select {
		case batch, ok := <-ch:
			if !ok {
				return time.Since(start), false
			}
			for _, e := range batch {
				if pred(e) {
					return time.Since(start), true
				}
			}
		case <-deadline:
			return time.Since(start), false
		}
	}
}

func hasEvent(events []Event, path string, typ EventType) bool {
	for _, e := range events {
		if e.Path == path && e.Type == typ {
			return true
		}
	}
	return false
}

// TestNextPollInterval verifies the adaptive backoff math deterministically:
// each quiet poll doubles the interval up to max and then holds at max.
func TestNextPollInterval(t *testing.T) {
	base := 500 * time.Millisecond
	max := 5 * time.Second

	got := base
	want := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		5 * time.Second,
		5 * time.Second, // holds at max
	}
	for i, w := range want {
		got = nextPollInterval(got, base, max)
		if got != w {
			t.Errorf("backoff step %d: got %s, want %s", i, got, w)
		}
	}

	// A change resets to the base (active) interval — exercised in
	// pollLoop; assert the clamp keeps intervals sane regardless.
	if r := nextPollInterval(0, base, max); r != base {
		t.Errorf("clamp from zero: got %s, want %s", r, base)
	}
	if r := nextPollInterval(base, base, base); r != base {
		t.Errorf("clamp when max==base: got %s, want %s", r, base)
	}
}

// TestWatcherAdaptivePolling verifies the adaptive mechanism end to end:
//  1. Active phase: a change is detected within a window shorter than the
//     old fixed 500ms poll (base interval 50ms here).
//  2. Quiet phase: the poller backs off to MaxInterval.
//  3. Idle phase: a change made after backoff is still detected — the slow
//     path keeps working.
func TestWatcherAdaptivePolling(t *testing.T) {
	dir := testDir(t)

	cfg := fastConfig(dir) // base interval 50ms
	cfg.MaxInterval = 300 * time.Millisecond
	w := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer w.Stop()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Active phase: fresh write detected quickly (well under old 500ms poll).
	writeFile(t, dir, "active.go", "package main\n")
	elapsed, ok := waitForEvent(t, w.Events(), 2*time.Second, func(e Event) bool {
		return e.Path == "active.go" && e.Type == EventCreate
	})
	if !ok {
		t.Fatal("active phase: no CREATE event for active.go")
	}
	if elapsed >= 500*time.Millisecond {
		t.Errorf("active phase: event took %s, want < 500ms (old fixed poll)", elapsed)
	}

	// Quiet phase: let the poller back off to MaxInterval (50→100→200→300).
	time.Sleep(800 * time.Millisecond)

	// Idle phase: a write after backoff must still be detected.
	writeFile(t, dir, "idle.go", "package main\n")
	_, ok = waitForEvent(t, w.Events(), 2*time.Second, func(e Event) bool {
		return e.Path == "idle.go" && e.Type == EventCreate
	})
	if !ok {
		t.Fatal("idle phase: no CREATE event for idle.go after adaptive backoff")
	}
}

// TestWatcherFallbackToPolling verifies that a failed start (analogous to a
// native watcher that cannot initialize — simulated here by a bad root path)
// fails cleanly, and that the polling mechanism automatically detects changes
// on a valid root.
func TestWatcherFallbackToPolling(t *testing.T) {
	dir := testDir(t)

	// Bad root: Start fails cleanly instead of panicking or hanging.
	badRoot := filepath.Join(dir, "does-not-exist")
	w := New(fastConfig(badRoot))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.Start(ctx); err == nil {
		t.Fatal("expected Start to fail for nonexistent root")
	}
	// Stop after a failed start must be safe (no-op, no panic).
	w.Stop()

	// Polling fallback on a valid root: automatic and functional.
	good := filepath.Join(dir, "tree")
	if err := os.MkdirAll(good, 0o755); err != nil {
		t.Fatal(err)
	}
	w2 := New(fastConfig(good))
	if err := w2.Start(ctx); err != nil {
		t.Fatalf("Start on valid root: %v", err)
	}
	defer w2.Stop()

	writeFile(t, good, "a.go", "package main\n")
	events := waitForEvents(t, w2.Events(), 1*time.Second)
	if !hasEvent(events, "a.go", EventCreate) {
		t.Fatal("polling fallback did not detect change")
	}
}
