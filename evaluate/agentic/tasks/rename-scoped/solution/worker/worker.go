package worker

import "example.com/shop/cache"

// Warm returns a cache preloaded with a warm marker.
func Warm() *cache.Cache {
	c := cache.Open()
	c.Set("warm", "1")
	return c
}
