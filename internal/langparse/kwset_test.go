package langparse

import (
	"testing"
)

func TestKWSet(t *testing.T) {
	kw := kwSet("a", "b")
	if len(kw) != 2 || !kw["a"] || !kw["b"] {
		t.Errorf("kwSet = %v", kw)
	}
	if kwSet()["x"] {
		t.Error("kwSet() should not contain x")
	}
}
