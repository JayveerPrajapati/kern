// Package guardrulecache provides a bounded in-memory cache for non-GATE
// guard command classifications.
//
// The cache is deliberately separate from guardrule so the classifier package
// remains small and the cache can import its result type without an import
// cycle. It caches only allow, serve, and governed results. GATE and
// path-aware classifications always run fresh: GATE decisions are the security
// boundary, and path-aware decisions depend on mutable filesystem state.
package guardrulecache

import (
	"crypto/sha256"
	"encoding/binary"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/guardrule"
)

const (
	// ClassifierVersion changes the cache namespace whenever classification
	// semantics change. A stale process is already isolated by the in-memory
	// design, but an explicit version makes invalidation auditable and avoids
	// accidental reuse after binary upgrades within a long-lived host.
	ClassifierVersion = "guardrule-v1"

	// DefaultCapacity bounds memory and lookup work for the default cache.
	DefaultCapacity = 512

	// DefaultTTL limits how long a classification can outlive changes to
	// environment, working tree state, or host configuration.
	DefaultTTL = 5 * time.Minute
)

// classifier is the uncached classification function. Tests replace it with a
// deterministic stub; production always uses guardrule.ClassifyCommandInRoots.
type classifier func(cmd, root string, extraRoots []string) guardrule.ClassifyResult

type entry struct {
	result    guardrule.ClassifyResult
	expiresAt time.Time
}

// Cache is a bounded, process-local LRU cache for non-GATE classifications.
// It is safe for concurrent use and performs no disk I/O.
type Cache struct {
	mu       sync.Mutex
	items    map[string]entry
	order    []string // most-recently-used first
	cap      int
	ttl      time.Duration
	now      func() time.Time
	classify classifier
}

var defaultCache = newCache(DefaultCapacity, DefaultTTL, time.Now, guardrule.ClassifyCommandInRoots)

// newCache constructs an isolated cache. The now hook and classifier make TTL,
// LRU, and verdict policy testable without sleeping or relying on real command
// classification.
func newCache(capacity int, ttl time.Duration, now func() time.Time, classify classifier) *Cache {
	if capacity <= 0 || ttl <= 0 || now == nil || classify == nil {
		return &Cache{items: map[string]entry{}, ttl: 1, now: time.Now, classify: guardrule.ClassifyCommandInRoots}
	}
	return &Cache{
		items:    make(map[string]entry),
		cap:      capacity,
		ttl:      ttl,
		now:      now,
		classify: classify,
	}
}

// ClassifyCommandInRoots returns the cached classification when a cacheable
// result is fresh, and otherwise runs the real classifier.
func (c *Cache) ClassifyCommandInRoots(cmd, root string, extraRoots []string) guardrule.ClassifyResult {
	key := cacheKey(cmd, root, extraRoots)
	if result, ok := c.get(key); ok {
		return result
	}
	result := c.classify(cmd, root, extraRoots)
	if cacheable(result) {
		c.put(key, result)
	}
	return result
}

// Len reports the number of entries, including expired entries that have not
// been read since expiry. It exists for bounded-capacity tests.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

func (c *Cache) get(key string) (guardrule.ClassifyResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	if !ok {
		return guardrule.ClassifyResult{}, false
	}
	if !c.now().Before(item.expiresAt) {
		delete(c.items, key)
		c.removeOrderLocked(key)
		return guardrule.ClassifyResult{}, false
	}
	c.promoteLocked(key)
	return item.result, true
}

func (c *Cache) put(key string, result guardrule.ClassifyResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.promoteLocked(key)
	c.items[key] = entry{result: result, expiresAt: c.now().Add(c.ttl)}
	for len(c.order) > c.cap {
		victim := c.order[len(c.order)-1]
		c.order = c.order[:len(c.order)-1]
		delete(c.items, victim)
	}
}

func cacheable(result guardrule.ClassifyResult) bool {
	if result.PathAware {
		return false
	}
	switch result.Verdict {
	case guardrule.VerdictAllow, guardrule.VerdictServe, guardrule.VerdictGoverned:
		return true
	default:
		return false
	}
}

func cacheKey(cmd, root string, extraRoots []string) string {
	h := sha256.New()
	writeField(h, ClassifierVersion)
	writeField(h, cmd)
	writeField(h, root)
	_ = binary.Write(h, binary.LittleEndian, uint64(len(extraRoots)))
	for _, extra := range extraRoots {
		writeField(h, extra)
	}
	return string(h.Sum(nil))
}

func writeField(h interface{ Write([]byte) (int, error) }, value string) {
	_ = binary.Write(h, binary.LittleEndian, uint64(len(value)))
	_, _ = h.Write([]byte(value))
}

func (c *Cache) promoteLocked(key string) {
	for i, candidate := range c.order {
		if candidate == key {
			copy(c.order[1:i+1], c.order[:i])
			c.order[0] = key
			return
		}
	}
	c.order = append([]string{key}, c.order...)
}

func (c *Cache) removeOrderLocked(key string) {
	for i, candidate := range c.order {
		if candidate == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			return
		}
	}
}

// ClassifyCommandInRoots is the process-wide guarded-entry path. It preserves
// guardrule.ClassifyCommandInRoots semantics while avoiding repeated
// non-security classification work for identical commands and roots.
func ClassifyCommandInRoots(cmd, root string, extraRoots []string) guardrule.ClassifyResult {
	return defaultCache.ClassifyCommandInRoots(cmd, root, extraRoots)
}
