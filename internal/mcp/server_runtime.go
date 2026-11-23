package mcp

import (
	"context"
	"fmt"
	"github.com/JayveerPrajapati/kern/internal/index"
	mcproot "github.com/JayveerPrajapati/kern/internal/mcp/root"
	"github.com/JayveerPrajapati/kern/internal/mcp/watcher"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

func (s *Server) startPreload() {
	s.indexOnce.Do(func() { go s.preloadIndexes() })
}

// preloadIndexes builds and caches each workspace root's index in the
// background so a later tool call reuses it instead of blocking on a cold
// build. It is triggered at serve start (see startPreload), falling back to
// a lazy trigger on the first MCP request for non-serve paths, and runs in
// a goroutine so setup never blocks on it. Skipped for filesystem
// roots, for roots that are not plausible projects (NEW-2: a scratch cwd
// must not get a silently built junk index — the .kern/index.sqlite that
// used to appear there), and when KERN_PRELOAD=0 (test/CI guard).
func (s *Server) preloadIndexes() {
	if os.Getenv("KERN_PRELOAD") == "0" {
		return
	}
	for _, r := range s.workspaceRoots() {
		if isFilesystemRoot(r) || !mcproot.PlausibleProject(resolveRoot(r)) {
			continue
		}
		// Register under mu so Add cannot race Close's Wait, and so no
		// preload starts after shutdown began.
		s.mu.Lock()
		if s.closing {
			s.mu.Unlock()
			return
		}
		s.preloadWG.Add(1)
		s.mu.Unlock()
		go func(root string) {
			defer s.preloadWG.Done()
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(os.Stderr, "kern-mcp: preload index %s: panic recovered: %v\n", root, r)
				}
			}()
			_, err := s.sessionFor(root).Index()
			if err != nil {
				fmt.Fprintf(os.Stderr, "kern-mcp: preload index %s: %v\n", root, err)
				return
			}
			// Record the build so a later graph tool call that reuses this
			// warm index does not print the "first build in progress" notice.
			s.indexedRoots.Store(root, true)
			// Warm the root's shared Platform in the background: the index
			// build just settled, so the first high-level tool call
			// (kern_impact, kern_plan, kern_analyze, ...) finds the cached
			// Platform instead of blocking on the ~5 tree walks
			// app.NewWithIndex runs. Best-effort and preloadWG-tracked like
			// the index preload itself, so Close drains it and a warm
			// failure just means the first call builds as before.
			s.warmPlatform(root)
		}(r)
	}
}

// warmPlatform builds the root's shared application Platform in the
// background so the first high-level tool call after the index build finds a
// warm cache instead of blocking on the ~5 tree walks app.NewWithIndex runs.
// It calls the same platformFor code path the handlers use, so the existing
// per-root cache keying (index instance) and the per-root build lock apply
// unchanged: a warm that races a rebuild keys to whatever index instance the
// next call would resolve (it can never pin a stale platform), concurrent
// tool calls and the warm serialize on the same per-root lock, and a warm
// failure is silent — the first call builds the Platform exactly as before
// the warm existed. The goroutine is tracked by preloadWG (registered under
// mu with the same closing guard as preloadIndexes) so Close drains it like
// a preload; once shutdown has begun no warm starts.
func (s *Server) warmPlatform(root string) {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	s.preloadWG.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.preloadWG.Done()
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "kern-mcp: warm platform %s: panic recovered: %v\n", root, r)
			}
		}()
		// The session LRU may have evicted this root between the index build
		// and this warm (a server touching many roots at capacity): nothing
		// to warm, and re-walking the tree here would only rebuild the index
		// the eviction just released. The next tool call builds both as
		// today.
		if _, ok := s.sessCache.Peek(root); !ok {
			return
		}
		if _, err := s.platformFor(context.Background(), root); err != nil {
			fmt.Fprintf(os.Stderr, "kern-mcp: warm platform %s: %v\n", root, err)
			return
		}
		// The session may have been evicted WHILE the Platform was building
		// (the build holds only the per-root lock, never the session cache).
		// Drop the warm entry so an evicted root's Platform cannot linger;
		// the next call rebuilds it, exactly as if the warm had never run.
		// Peek is called WITHOUT s.mu held: the session cache invokes its
		// onEvict callback (evictSessionState, which takes s.mu) under its
		// own lock, so holding s.mu here would invert that lock order.
		if _, ok := s.sessCache.Peek(root); !ok {
			s.evictSessionState(root)
		}
	}()
}

// startReactiveWatcher starts the ADR-0011 reactive file watcher when the
// opt-in env enables it (KERN_MCP_WATCH_INTERVAL_MS, default OFF): one
// polling watcher per workspace root cheaply proves the index fresh every
// interval and, when stale, rebuilds into a NEW index instance that
// publishIndex atomically swaps into the serving path. All watchers share one
// context cancelled by Close, which also drains an in-flight rebuild. A
// disabled env (unset/zero/invalid) is a no-op. Idempotent via watcherOnce.
func (s *Server) startReactiveWatcher() {
	s.watcherOnce.Do(func() {
		interval := watcher.IntervalMsFromEnv()
		if interval <= 0 {
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		s.mu.Lock()
		s.watcherCancel = cancel
		s.watcherDone = done
		s.mu.Unlock()
		go func() {
			defer close(done)
			var wg sync.WaitGroup
			for _, r := range s.workspaceRoots() {
				if isFilesystemRoot(r) {
					continue
				}
				root := resolveRoot(r)
				// NEW-2 consistency: a scratch cwd gets no watcher either —
				// polling a non-project every interval is pure idle waste.
				if !mcproot.PlausibleProject(root) {
					continue
				}
				w := watcher.New(watcher.Options{
					Root:     root,
					Interval: interval,
					OnReload: func(fresh *index.Index) error { return s.publishIndex(root, fresh) },
				})
				wg.Add(1)
				go func() {
					defer wg.Done()
					w.Run(ctx)
				}()
			}
			wg.Wait()
		}()
	})
}

// watchedIndex returns the atomically-published fresh index for root, if the
// reactive watcher has swapped one in. The published instance is at least as
// fresh as anything the session can produce, and reading it is lock-free: a
// concurrent publish stores the pointer atomically, so per-call readers see
// either the old or the new instance.
func (s *Server) watchedIndex(root string) (*index.Index, bool) {
	root = resolveRoot(root)
	v, ok := s.liveIndexes.Load(root)
	if !ok {
		return nil, false
	}
	ix := v.(*atomic.Pointer[index.Index]).Load()
	return ix, ix != nil
}

// publishIndex atomically publishes a freshly built index for root so every
// handler accessor — loadIndex, cacheIndexIdentity, the health probe — sees
// the new instance from the next call on. The session's own cached copy and
// its derived caches (communities/arch/...) are dropped so nothing serves
// old-index-derived data after the swap, then the fresh index is reinstalled
// into the session so the tool cache's identity peek (cacheIndexIdentity →
// CachedIndex, tool_cache.go F2/F6) rotates to the new content root and old
// entries become unreachable. Logs exactly one line on success, server
// stderr style. Called from the background watcher's rebuild goroutine.
func (s *Server) publishIndex(root string, fresh *index.Index) error {
	if fresh == nil {
		return fmt.Errorf("reactive watcher: nil index for %s", root)
	}
	root = resolveRoot(root)
	holder, _ := s.liveIndexes.LoadOrStore(root, &atomic.Pointer[index.Index]{})
	holder.(*atomic.Pointer[index.Index]).Store(fresh)
	// Drop the session's stale copy + derived caches, then reinstall the
	// fresh index so session-side readers (CachedIndex, CommunitiesList)
	// converge immediately. Only touch an existing session — the watcher must
	// not create one. The reinstall is best-effort: the published instance is
	// already what handlers are served, so a failure only costs cache hits.
	if sess, ok := s.sessCache.Peek(root); ok {
		sess.Invalidate()
		_, _ = sess.Index() // reload the just-persisted fresh index
	}
	fmt.Fprintf(os.Stderr, "kern-mcp: index stale; reloaded (%d files)\n", len(fresh.FileHashes))
	return nil
}

// isFilesystemRoot reports whether p is a filesystem root ("/" on Unix, or a
// drive root on Windows) that must never be treated as a project to index.
func isFilesystemRoot(p string) bool {
	vol := filepath.VolumeName(p)
	return filepath.Clean(p) == filepath.Clean(vol+string(filepath.Separator))
}

// workspaceRoots returns the roots field, falling back to the default when
// unset (defensive; NewServer always initializes it).
func (s *Server) workspaceRoots() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.roots) == 0 {
		return defaultWorkspaceRoots()
	}
	out := append([]string(nil), s.roots...)
	return out
}
