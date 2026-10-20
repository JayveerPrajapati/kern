// Package filecache is an in-memory, freshness-gated file-content cache.
//
// Repeated reads of the same file by different tools (kern_explore,
// kern_context, kern_graph, kern_retrieve, kern_compact_file,
// kern_project_map and CLI paths) hit this cache: the caller stats the path
// first (~1µs) and passes the stat in, and Get serves the stored bytes only
// when the file's current size AND mtime both match the stat the bytes were
// read under — the fileFingerprint size:mtime freshness contract. A Get is
// never a content read; a miss returns ok=false and the caller reads the
// file itself, then Put. Keys are canonicalized (filepath.Clean, made
// absolute for relative input), so a relative CLI read and an absolute index
// read of the same file share one entry. Write paths that replace a file
// while preserving its size:mtime identity (kern rename apply) call
// Invalidate so the next Get re-reads the new bytes.
//
// The package is a LEAF: stdlib only, no internal imports. It is shared
// across all agents and connections via the package-level Default instance;
// tests construct isolated caches with New. Bounds mirror the D1 tool-cache
// discipline (internal/mcp/tool_cache.go): entries larger than 1 MiB are
// skipped, the LRU holds 128 entries, and all state is mutex-guarded (the
// MCP server runs 16 concurrent calls).
package filecache

import (
	"os"
	"path/filepath"
	"sync"
)

// Bounds, mirroring the D1 tool-cache discipline (skip big entries, bound
// the LRU).
const (
	// MaxEntryBytes is the largest single file the cache stores; Put skips
	// larger reads (caching them costs more memory than the reads save).
	MaxEntryBytes = 1 << 20 // 1 MiB
	// LRUCap is the maximum number of entries kept; the least-recently-used
	// entry is evicted when a Put grows the cache past it.
	LRUCap = 128
)

// entry is one cached file: its bytes plus the stat identity they were read
// under. A Get is a hit only when the file's current size AND mtime both
// equal these values.
type entry struct {
	bytes []byte
	size  int64
	mtime int64 // UnixNano, matching the fileFingerprint "size:mtime" contract
}

// Cache is a path-keyed, in-memory file-content cache, safe for concurrent
// use. Freshness is re-checked on every Get via os.Stat, so a cached hit is
// never stale: a file modified between reads (new size or mtime) is a miss
// and the caller re-reads it.
type Cache struct {
	mu    sync.Mutex
	order []string // most-recently-used first (LRU)
	items map[string]entry
	cap   int
	max   int64
}

// Default is the process-wide cache shared by every tool and connection.
var Default = New()

// New returns an empty cache with the package bounds (LRUCap entries, at
// most MaxEntryBytes per entry). Production code uses Default; tests use
// New for isolation.
func New() *Cache {
	return &Cache{
		items: make(map[string]entry),
		cap:   LRUCap,
		max:   MaxEntryBytes,
	}
}

// Get returns the cached bytes for absPath when the file on disk still
// matches the stat the entry was stored under. fi is the caller's fresh stat
// of absPath (every call site stats for its own bounds anyway); a hit
// requires BOTH the current size and mtime to equal the stored values, so a
// modified file is always a miss. A nil fi is never a hit. A miss returns
// ok=false WITHOUT reading the file — the caller reads the bytes and then
// Puts them.
//
// The returned slice is SHARED cache state: it aliases the stored entry and
// must be treated as read-only by the caller. Do not mutate it in place.
func (c *Cache) Get(absPath string, fi os.FileInfo) ([]byte, bool) {
	if fi == nil {
		return nil, false
	}
	absPath = canon(absPath)
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[absPath]
	if !ok {
		return nil, false
	}
	if e.size != fi.Size() || e.mtime != fi.ModTime().UnixNano() {
		// Stale: drop the entry so the next Get is a clean miss.
		delete(c.items, absPath)
		c.delOrderLocked(absPath)
		return nil, false
	}
	c.promoteLocked(absPath)
	return e.bytes, true
}

// Put stores b under absPath, tagged with the caller's stat so a later Get
// can verify freshness. fi is the stat the caller read the bytes under and
// is required — it pins the freshness tag to the observed version, so a
// concurrent edit is detected on the next Get. Entries larger than the
// per-entry bound are skipped (not cached), as are calls with a nil fi (no
// freshness identity). The cache never reads the file itself — the caller
// has already read the bytes. Callers must not Put credential-shaped files:
// the fsutil.LooksLikeCredentialFile guard is applied at the call sites,
// ahead of the cache.
func (c *Cache) Put(absPath string, b []byte, fi os.FileInfo) {
	if fi == nil || len(b) > int(c.max) {
		return // skip big entries: costs more memory than it saves
	}
	absPath = canon(absPath)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.promoteLocked(absPath)
	c.items[absPath] = entry{
		bytes: b,
		size:  fi.Size(),
		mtime: fi.ModTime().UnixNano(),
	}
	for len(c.order) > c.cap {
		tail := c.order[len(c.order)-1]
		c.order = c.order[:len(c.order)-1]
		delete(c.items, tail)
	}
}

// Invalidate drops the entry for absPath, if any. Write paths (renames,
// mutations) call this so the next Get re-reads even when size and mtime
// happen to coincide.
func (c *Cache) Invalidate(absPath string) {
	absPath = canon(absPath)
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, absPath)
	c.delOrderLocked(absPath)
}

// canon canonicalizes a cache key: filepath.Abs resolves relative input
// against the working directory and Clean collapses ".."/"." segments, so a
// relative CLI path and an absolute index path for the same file share one
// entry (instead of silently halving the hit rate). Absolute input is
// returned Clean'd with no syscall.
func canon(absPath string) string {
	abs, err := filepath.Abs(absPath)
	if err != nil {
		return filepath.Clean(absPath)
	}
	return abs
}

// promoteLocked moves key to the MRU front. Caller holds c.mu. On a hit the
// move is done in place with a single memmove shift — no allocation on the
// hot path; only a brand-new key allocates (bounded by the cap).
func (c *Cache) promoteLocked(key string) {
	for i, k := range c.order {
		if k == key {
			copy(c.order[1:i+1], c.order[:i]) // memmove shift right
			c.order[0] = key
			return
		}
	}
	c.order = append([]string{key}, c.order...)
}

// delOrderLocked removes key from the LRU order. Caller holds c.mu.
func (c *Cache) delOrderLocked(key string) {
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}
