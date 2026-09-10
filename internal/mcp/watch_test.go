package mcp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/lock"
)

// TestBackgroundWatchRebuildsStaleIndexOnce exercises the implicit background
// index watch end to end: with a tiny poll interval, a stale index is rebuilt
// exactly once on the watcher's own tick (not by any tool call), stays fresh
// afterwards, and the watch goroutine stops cleanly on Close.
func TestBackgroundWatchRebuildsStaleIndexOnce(t *testing.T) {
	root := mcpProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	s.roots = []string{root}
	defer s.Close()

	// Build the initial index and record its instance.
	if _, err := s.loadIndex(context.Background(), root); err != nil {
		t.Fatalf("initial index build: %v", err)
	}
	before, ok := s.sessionFor(root).CachedIndex()
	if !ok || before == nil {
		t.Fatal("expected a cached index after the initial build")
	}

	// Make the index stale: add a source file the cached index does not know.
	if err := os.WriteFile(filepath.Join(root, "extra.go"), []byte("package main\nfunc Extra() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const interval = 30 * time.Millisecond
	s.StartBackgroundWatch(ctx, interval)

	// The watcher must rebuild the stale index on its own: wait until the
	// session serves a NEW index instance that knows about the added file.
	deadline := time.Now().Add(15 * time.Second)
	var rebuilt *index.Index
	for time.Now().Before(deadline) {
		if cur, ok := s.sessionFor(root).CachedIndex(); ok && cur != before {
			rebuilt = cur
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rebuilt == nil {
		t.Fatal("background watch did not rebuild the stale index")
	}
	if len(rebuilt.Search("Extra", 5)) == 0 {
		t.Fatal("rebuilt index does not contain the added symbol Extra")
	}

	// The index is now fresh, so several more intervals must NOT trigger a
	// second rebuild (a rebuild would allocate a new index instance).
	time.Sleep(5 * interval)
	if cur, ok := s.sessionFor(root).CachedIndex(); !ok || cur != rebuilt {
		t.Fatalf("expected exactly one rebuild (index fresh afterwards), got a different index instance")
	}

	// Clean stop on Close: the watch goroutine must exit promptly.
	s.Close()
	select {
	case <-s.watchDone:
	case <-time.After(5 * time.Second):
		t.Fatal("background watch goroutine did not stop on Close")
	}
}

// TestBackgroundWatchSingleFlight verifies the watcher's single-flight
// contract: a tick that fires while a rebuild is running is skipped rather
// than stacking a concurrent rebuild. It drives maybeRebuildIndexes directly
// (the loop's tick handler) with the busy flag held to simulate an in-flight
// rebuild.
func TestBackgroundWatchSingleFlight(t *testing.T) {
	root := mcpProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	s.roots = []string{root}
	defer s.Close()

	if _, err := s.loadIndex(context.Background(), root); err != nil {
		t.Fatalf("initial index build: %v", err)
	}
	before, ok := s.sessionFor(root).CachedIndex()
	if !ok || before == nil {
		t.Fatal("expected a cached index after the initial build")
	}
	// Make the index stale so a rebuild is actually needed.
	if err := os.WriteFile(filepath.Join(root, "extra.go"), []byte("package main\nfunc Extra() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate a rebuild already in flight: every tick while busy must be
	// skipped — no rebuild goroutine may be spawned (a spawned rebuild would
	// complete quickly on this tiny fixture, clearing watchBusy and swapping
	// the index instance).
	s.watchBusy = true
	s.maybeRebuildIndexes()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.watchMu.Lock()
		busy := s.watchBusy
		s.watchMu.Unlock()
		cur, _ := s.sessionFor(root).CachedIndex()
		if !busy || cur != before {
			t.Fatal("tick while a rebuild was in flight spawned a rebuild (single-flight violated)")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Let the in-flight rebuild "finish", then the next tick may rebuild.
	s.watchBusy = false
	s.maybeRebuildIndexes()
	deadline = time.Now().Add(15 * time.Second)
	rebuilt := false
	for time.Now().Before(deadline) {
		if cur, _ := s.sessionFor(root).CachedIndex(); cur != before {
			rebuilt = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !rebuilt {
		t.Fatal("tick after the busy flag cleared did not rebuild the stale index")
	}
	// The spawned rebuild must have cleared the busy flag on completion.
	s.watchMu.Lock()
	busy := s.watchBusy
	s.watchMu.Unlock()
	if busy {
		t.Fatal("watchBusy was not cleared after the rebuild completed")
	}
}

// TestWatchIntervalFromEnv covers the KERN_MCP_WATCH / KERN_MCP_WATCH_INTERVAL
// configuration: disable flag, valid interval, and invalid/unset fallback.
func TestWatchIntervalFromEnv(t *testing.T) {
	t.Setenv("KERN_MCP_WATCH", "")
	t.Setenv("KERN_MCP_WATCH_INTERVAL", "")
	if got := watchIntervalFromEnv(); got != 5*time.Second {
		t.Fatalf("unset env: expected 5s, got %v", got)
	}
	t.Setenv("KERN_MCP_WATCH_INTERVAL", "2")
	if got := watchIntervalFromEnv(); got != 2*time.Second {
		t.Fatalf("KERN_MCP_WATCH_INTERVAL=2: expected 2s, got %v", got)
	}
	t.Setenv("KERN_MCP_WATCH_INTERVAL", "not-a-number")
	if got := watchIntervalFromEnv(); got != 5*time.Second {
		t.Fatalf("invalid interval: expected 5s fallback, got %v", got)
	}
	t.Setenv("KERN_MCP_WATCH_INTERVAL", "0")
	if got := watchIntervalFromEnv(); got != 5*time.Second {
		t.Fatalf("KERN_MCP_WATCH_INTERVAL=0: expected 5s fallback, got %v", got)
	}
	t.Setenv("KERN_MCP_WATCH", "0")
	t.Setenv("KERN_MCP_WATCH_INTERVAL", "2")
	if got := watchIntervalFromEnv(); got != 0 {
		t.Fatalf("KERN_MCP_WATCH=0: expected disabled (0), got %v", got)
	}
}

// TestWatchElectionDefersToLockHolder verifies the C5 leadership election:
// when another kern process (modeled by a second flock on the same scope,
// which conflicts per open-file-description) owns the index-watch lock,
// maybeRebuildIndexes must skip the rebuild instead of stacking a concurrent
// one.
func TestWatchElectionDefersToLockHolder(t *testing.T) {
	s := newTestServer()
	defer s.Close()
	root := t.TempDir()
	s.roots = []string{root}
	holder, err := lock.Acquire(root, "index-watch")
	if err != nil {
		t.Fatalf("acquire election lock: %v", err)
	}
	defer holder.Release()

	// The rebuild must be skipped entirely: no index may be built for the
	// root while another process owns the rebuild.
	s.maybeRebuildIndexes()
	s.watchWG.Wait()
	if _, built := s.indexedRoots.Load(root); built {
		t.Fatalf("rebuild ran despite the election lock being held by another process")
	}
}

// TestWatchElectionRunsWhenFree verifies the counter-case: with no competing
// holder the watch rebuilds the root and records it as warmed.
func TestWatchElectionRunsWhenFree(t *testing.T) {
	s := newTestServer()
	defer s.Close()
	root := t.TempDir()
	s.roots = []string{root}
	s.maybeRebuildIndexes()
	s.watchWG.Wait()
	if _, built := s.indexedRoots.Load(root); !built {
		t.Fatalf("rebuild did not run for root %s", root)
	}
}
