package cache

// Cache is a small in-memory string cache.
type Cache struct{ m map[string]string }

// New returns an empty Cache.
func New() *Cache { return &Cache{m: map[string]string{}} }

// Set stores v under k.
func (c *Cache) Set(k, v string) { c.m[k] = v }

// Get returns the value stored under k.
func (c *Cache) Get(k string) (string, bool) {
	v, ok := c.m[k]
	return v, ok
}
