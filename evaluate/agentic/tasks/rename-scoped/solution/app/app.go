package app

import (
	"example.com/shop/cache"
	"example.com/shop/queue"
)

// Run stores a job in the cache and hands it to the queue.
func Run() string {
	c := cache.Open()
	q := queue.New()
	c.Set("job", "build")
	v, _ := c.Get("job")
	q.Push(v)
	return q.Pop()
}
