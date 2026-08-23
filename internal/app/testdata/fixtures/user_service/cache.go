package main

import "sync"

// CacheService is a tenant-scoped in-memory cache. The architecture rule
// declares the cache layer as explicitly allowed so services may depend on it.
type CacheService struct {
	mu    sync.Mutex
	store map[string]any
}

// Get returns a cached value, if present.
func (c *CacheService) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.store[key]
	return v, ok
}

// Set stores a value under a key.
func (c *CacheService) Set(key string, v any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil {
		c.store = map[string]any{}
	}
	c.store[key] = v
}