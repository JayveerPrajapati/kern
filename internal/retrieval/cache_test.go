package retrieval

import (
	"testing"
	"time"
)

func TestCacheSetGetHit(t *testing.T) {
	c := NewCache(10)
	h := NewHandle(TypeSymbol, "greet", "a/b.go", 10, 5, 1.0, "hash1")
	c.Set(h, "func greet() {}", 7)
	content, tokens, ok := c.Get(h.ID, "hash1")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if content != "func greet() {}" || tokens != 7 {
		t.Fatalf("Get = (%q, %d), want (%q, %d)", content, tokens, "func greet() {}", 7)
	}
	if c.Len() != 1 {
		t.Fatalf("Len = %d, want 1", c.Len())
	}
}

func TestCacheStaleHashMiss(t *testing.T) {
	c := NewCache(10)
	h := NewHandle(TypeSymbol, "greet", "a/b.go", 10, 5, 1.0, "hash1")
	c.Set(h, "old content", 3)
	if _, _, ok := c.Get(h.ID, "hash2"); ok {
		t.Fatal("stale content hash should be a miss")
	}
	if _, _, ok := c.Get("missing", "hash1"); ok {
		t.Fatal("unknown id should be a miss")
	}
}

func TestCacheEvictionOldestDropped(t *testing.T) {
	c := NewCache(2)
	h1 := NewHandle(TypeSymbol, "a", "a/a.go", 1, 1, 1.0, "h1")
	h2 := NewHandle(TypeSymbol, "b", "b/b.go", 1, 1, 1.0, "h2")
	c.Set(h1, "one", 1)
	time.Sleep(time.Millisecond)
	c.Set(h2, "two", 1)
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2", c.Len())
	}
	h3 := NewHandle(TypeSymbol, "c", "c/c.go", 1, 1, 1.0, "h3")
	c.Set(h3, "three", 1)
	if c.Len() != 2 {
		t.Fatalf("Len after eviction = %d, want 2", c.Len())
	}
	if _, _, ok := c.Get(h1.ID, "h1"); ok {
		t.Fatal("oldest entry should have been evicted")
	}
	if _, _, ok := c.Get(h2.ID, "h2"); !ok {
		t.Fatal("second entry should still be present")
	}
	if _, _, ok := c.Get(h3.ID, "h3"); !ok {
		t.Fatal("newest entry should still be present")
	}
}

func TestCacheClear(t *testing.T) {
	c := NewCache(5)
	c.Set(NewHandle(TypeSymbol, "a", "a/a.go", 1, 1, 1.0, "h"), "one", 1)
	c.Set(NewHandle(TypeSymbol, "b", "b/b.go", 1, 1, 1.0, "h"), "two", 1)
	c.Clear()
	if c.Len() != 0 {
		t.Fatalf("Len after Clear = %d, want 0", c.Len())
	}
}
