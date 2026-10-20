package cache

import "testing"

func TestSetGet(t *testing.T) {
	c := New()
	c.Set("a", "1")
	if v, ok := c.Get("a"); !ok || v != "1" {
		t.Fatalf("Get(a) = %q, %v", v, ok)
	}
}
