package mcp

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// waitWarmPlatform polls until the background warm (fired from preloadIndexes
// right after the index build) has populated the per-root platform cache,
// failing the test if it never does. Reading the cache under s.mu keeps the
// race detector quiet — platformFor writes it under the same lock. The cache
// is keyed by the resolved root, so reads go through resolveRoot like the
// production path.
func waitWarmPlatform(t *testing.T, s *Server, root string) *platformEntry {
	t.Helper()
	key := resolveRoot(root)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		e := s.platforms[key]
		s.mu.Unlock()
		if e != nil && e.p != nil {
			return e
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("background platform warm never populated the cache for %s", root)
	return nil
}

// The background warm must populate platformFor's per-root cache WITHOUT any
// tool call: after an index build settles, the first high-level call finds a
// warm Platform instead of blocking on ~5 tree walks.
func TestWarmPlatformAfterIndexBuild(t *testing.T) {
	t.Setenv("KERN_PRELOAD", "1")
	root := preloadTestRoot(t)
	s := NewServerForRoot(strings.NewReader(""), io.Discard, root)

	s.preloadIndexes()
	e := waitWarmPlatform(t, s, root)

	// Keyed to the SAME index instance a tool call would resolve: the warm
	// went through the platformFor code path, so the cache entry's instance
	// is the one the session serves.
	ix, err := s.sessionFor(root).Index()
	if err != nil {
		t.Fatalf("session index: %v", err)
	}
	if e.ix != ix {
		t.Fatalf("warm platform keyed to a different index instance: warm %p, session %p", e.ix, ix)
	}
}

// A tool call after the warm must NOT rebuild the Platform: the cache entry
// (keyed to the index instance) is the same object before and after the call.
func TestWarmPlatformToolCallDoesNotRebuild(t *testing.T) {
	t.Setenv("KERN_PRELOAD", "1")
	root := preloadTestRoot(t)
	s := NewServerForRoot(strings.NewReader(""), io.Discard, root)

	s.preloadIndexes()
	e := waitWarmPlatform(t, s, root)
	ix0, p0 := e.ix, e.p

	p, err := s.platformFor(context.Background(), root)
	if err != nil {
		t.Fatalf("platformFor after warm: %v", err)
	}
	if p != p0 {
		t.Fatalf("platformFor rebuilt the Platform after a warm: got %p, want the warmed %p", p, p0)
	}
	key := resolveRoot(root)
	s.mu.Lock()
	after := s.platforms[key]
	s.mu.Unlock()
	if after == nil || after.ix != ix0 || after.p != p0 {
		t.Fatalf("platform cache entry changed after the warm: got ix %p (want %p), p %p (want %p)", after.ix, ix0, after.p, p0)
	}
}

// Eviction must still drop the warmed platform (existing behavior preserved):
// after evictSessionState the next call rebuilds — the warm must not make an
// evicted root's Platform linger.
func TestWarmPlatformEvictionDropsEntry(t *testing.T) {
	t.Setenv("KERN_PRELOAD", "1")
	root := preloadTestRoot(t)
	s := NewServerForRoot(strings.NewReader(""), io.Discard, root)

	s.preloadIndexes()
	e := waitWarmPlatform(t, s, root)

	s.evictSessionState(root)
	key := resolveRoot(root)
	s.mu.Lock()
	dropped := s.platforms[key] == nil && s.platformLocks[key] == nil
	s.mu.Unlock()
	if !dropped {
		t.Fatal("evictSessionState did not drop the warmed platform entry")
	}

	p, err := s.platformFor(context.Background(), root)
	if err != nil {
		t.Fatalf("platformFor after eviction: %v", err)
	}
	if p == e.p {
		t.Fatal("platformFor served the evicted Platform; expected a rebuild")
	}
}

// Close must drain the background warm like a preload: the warm is registered
// under preloadWG (synchronously, before its goroutine starts), so Close's
// preload drain covers an in-flight warm. After Close the WaitGroup is
// settled — no warm goroutine survives against the closed server.
func TestCloseDrainsRunningPlatformWarm(t *testing.T) {
	t.Setenv("KERN_PRELOAD", "1")
	root := preloadTestRoot(t)
	s := NewServerForRoot(strings.NewReader(""), io.Discard, root)

	// Production sequence: build the index, then warm the Platform, then
	// Close immediately — the warm is registered and possibly still running
	// when Close's drain starts.
	if _, err := s.sessionFor(root).Index(); err != nil {
		t.Fatalf("index: %v", err)
	}
	s.warmPlatform(root)
	s.Close()

	done := make(chan struct{})
	go func() {
		s.preloadWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("preloadWG still has in-flight work after Close")
	}
}

// After shutdown has begun no warm may start (mirrors TestNoPreloadAfterClose):
// a warm would build a Platform for a server nothing will ever use again.
func TestNoPlatformWarmAfterClose(t *testing.T) {
	t.Setenv("KERN_PRELOAD", "1")
	root := preloadTestRoot(t)
	s := NewServerForRoot(strings.NewReader(""), io.Discard, root)

	s.Close()
	s.preloadIndexes()
	s.preloadWG.Wait()

	key := resolveRoot(root)
	s.mu.Lock()
	e := s.platforms[key]
	s.mu.Unlock()
	if e != nil {
		t.Fatalf("platform warm ran after Close for %v", root)
	}
}
