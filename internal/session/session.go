// Package session provides a bounded LRU cache of project sessions keyed by
// workspace root. Every distinct root accumulates a project.Session (a full
// in-memory index plus an fswatch subprocess each), so an unbounded map would
// leak both memory and watcher processes across a long-lived server. The
// cache bounds the map, evicts the least-recently-used idle session beyond
// the cap, and hands root-owned companion state cleanup to an onEvict
// callback so the package stays free of the owning server's caches.
//
// The package is deliberately dependency-free beyond internal/project and the
// standard library: it must not import internal/mcp (the server imports this
// package, so an import back would cycle).
package session

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/project"
)

// MaxSessions caps the number of project sessions the cache holds at once.
// Every distinct root accumulates a project.Session (a full in-memory index
// plus an fswatch subprocess each), so an unbounded map would leak both
// memory and watcher processes across a long-lived server. Beyond the cap the
// least-recently-used idle session is evicted and closed (see Cache.For).
const MaxSessions = 16

// SessionIdleEvict is the minimum idle time before a session is eligible for
// eviction. Tool calls are short (seconds), so a 10-minute idle threshold
// never evicts a session a handler is mid-use of, while still bounding the
// map on servers that touch many distinct roots.
const SessionIdleEvict = 10 * time.Minute

// entry is one cached project session plus the LRU bookkeeping used for
// bounded eviction.
type entry struct {
	sess     *project.Session
	lastUsed time.Time
}

// Cache is a bounded LRU of project sessions keyed by resolved workspace
// root. The zero value is not usable; construct with NewCache.
type Cache struct {
	mu      sync.Mutex
	max     int
	idle    time.Duration
	onEvict func(root string)
	entries map[string]*entry
}

// NewCache returns a bounded LRU session cache holding at most max sessions.
// A session idle for at least idle is eligible for eviction when the cache is
// at capacity. onEvict, when non-nil, is invoked with the evicted root after
// the entry is removed and before its session is closed, so the owner can
// drop root-owned companion state (e.g. a per-root platform cache).
func NewCache(max int, idle time.Duration, onEvict func(root string)) *Cache {
	return &Cache{max: max, idle: idle, onEvict: onEvict, entries: map[string]*entry{}}
}

// resolveRoot returns the cleaned absolute workspace root: cwd when root is
// empty, filepath.Abs+Clean otherwise, falling back to the raw input if Abs
// fails. It mirrors the root resolution the mcp adapter applies before
// caching (internal/mcp/root.ResolveRoot), replicated here so the cache
// stays stdlib-only.
func resolveRoot(root string) string {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			return filepath.Clean(cwd)
		}
		return "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}
	return root
}

// For returns the project session for root, creating and caching one per
// root so index state and stats identity are shared across tool calls. The
// cache is bounded (max): inserting beyond the cap evicts the
// least-recently-used entry that has been idle for at least idle, closing
// its watcher and releasing its index (and invoking the onEvict callback for
// root-owned companion state), so a server that touches many distinct roots
// cannot accumulate a project.Session per root forever. A hit promotes the
// entry to most-recently-used. The returned error is always nil today; it is
// part of the signature so callers can distinguish a cache miss from a
// construction failure without changing the call shape.
func (c *Cache) For(root string) (*project.Session, error) {
	root = resolveRoot(root)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]*entry{}
	}
	now := time.Now()
	if e, ok := c.entries[root]; ok {
		e.lastUsed = now
		return e.sess, nil
	}
	if len(c.entries) >= c.max {
		c.evictIdleLocked(now)
	}
	e := &entry{sess: project.New(root, ""), lastUsed: now}
	c.entries[root] = e
	return e.sess, nil
}

// Peek returns the cached session for root without promoting it in the LRU
// order and without creating one. ok is false when root is not cached. The
// key must already be resolved (callers that resolve their own roots before
// peeking keep the same key space as For).
func (c *Cache) Peek(root string) (*project.Session, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[root]
	if !ok {
		return nil, false
	}
	return e.sess, true
}

// EvictIdle closes and removes the least-recently-used session entry idle for
// at least the configured idle duration, invoking the onEvict callback for
// the evicted root. A no-op when every cached session is still in recent use
// (the cap may be exceeded rather than evict a session a handler is actively
// using).
func (c *Cache) EvictIdle(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.evictIdleLocked(now)
}

// evictIdleLocked closes and removes the least-recently-used session entry
// idle for at least c.idle. Called with c.mu held; a no-op when every cached
// session is still in recent use (the cap may be exceeded rather than evict a
// session a handler is actively using).
func (c *Cache) evictIdleLocked(now time.Time) {
	var oldestKey string
	var oldest time.Time
	for k, e := range c.entries {
		if now.Sub(e.lastUsed) < c.idle {
			continue // still recent: never evict an in-use session
		}
		if oldestKey == "" || e.lastUsed.Before(oldest) {
			oldestKey = k
			oldest = e.lastUsed
		}
	}
	if oldestKey == "" {
		return
	}
	e := c.entries[oldestKey]
	delete(c.entries, oldestKey)
	// Hand root-owned companion state to the owner BEFORE closing the
	// session so the eviction order matches the original: entry removed,
	// companion state dropped, then the session closed.
	if c.onEvict != nil {
		c.onEvict(oldestKey)
	}
	e.sess.Close() // project.Session.Close is documented safe to call multiple times
}

// CloseAll closes every cached session and drains the cache. Safe to call
// multiple times; a nil or already-drained cache is a no-op (project.Session.
// Close is itself documented safe to call multiple times).
func (c *Cache) CloseAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		e.sess.Close()
	}
	c.entries = map[string]*entry{}
}

// Len returns the number of cached sessions.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
