package retrieval

import (
	"sync"
	"time"
)

// cacheEntry is one cached render plus the handle whose content hash gates
// freshness.
type cacheEntry struct {
	handle   *Handle
	content  string
	tokens   int
	storedAt time.Time
}

// Cache is a bounded, staleness-aware render cache keyed by handle ID. A Get
// only hits when the stored handle's ContentHash still matches the requested
// one, so edited content is never served stale.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	maxSize int
}

// NewCache returns an empty cache holding at most maxSize entries
// (maxSize <= 0 means 1024).
func NewCache(maxSize int) *Cache {
	if maxSize <= 0 {
		maxSize = 1024
	}
	return &Cache{entries: map[string]cacheEntry{}, maxSize: maxSize}
}

// Get returns the cached content and token count for id when an entry exists
// and its content hash matches contentHash (stale content = miss).
func (c *Cache) Get(id, contentHash string) (string, int, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[id]
	if !ok || e.handle == nil || e.handle.ContentHash != contentHash {
		return "", 0, false
	}
	return e.content, e.tokens, true
}

// Set inserts or replaces the entry for h.ID, evicting the oldest entry when
// the cache is over capacity.
func (c *Cache) Set(h *Handle, content string, tokens int) {
	if h == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[h.ID] = cacheEntry{handle: h, content: content, tokens: tokens, storedAt: time.Now()}
	for len(c.entries) > c.maxSize {
		c.evictOldest()
	}
}

// evictOldest removes the entry with the smallest storedAt (ties broken by
// key) so eviction is deterministic.
func (c *Cache) evictOldest() {
	var oldestKey string
	var oldestAt time.Time
	first := true
	for k, e := range c.entries {
		if first || e.storedAt.Before(oldestAt) || (e.storedAt.Equal(oldestAt) && k < oldestKey) {
			oldestKey, oldestAt = k, e.storedAt
			first = false
		}
	}
	if !first {
		delete(c.entries, oldestKey)
	}
}

// Len returns the number of cached entries.
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// Clear empties the cache.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = map[string]cacheEntry{}
}
