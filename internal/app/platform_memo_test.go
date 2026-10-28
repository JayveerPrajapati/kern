package app

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestMemoizedIndexOnlyFollowsIndexPointer pins the correctness invariant the
// daemon relies on: the cached Platform is tied to the INDEX POINTER, so a
// rebuilt index (a new pointer) can never be served a graph built from the old
// one.
func TestMemoizedIndexOnlyFollowsIndexPointer(t *testing.T) {
	MemoizePlatform = true
	t.Cleanup(func() { MemoizePlatform = false })

	ix := &index.Index{}
	p1 := memoizedIndexOnly("/repo", ix)
	p2 := memoizedIndexOnly("/repo", ix)
	if p1 != p2 {
		t.Fatal("same index pointer must return the same Platform")
	}
	if p1.ix != ix || p1.graph == nil {
		t.Fatal("memoized platform must carry the index and a graph")
	}

	// A rebuild produces a new *Index: the graph must be rebuilt with it.
	rebuilt := &index.Index{}
	p3 := memoizedIndexOnly("/repo", rebuilt)
	if p3 == p1 {
		t.Fatal("a new index generation must not reuse the previous Platform")
	}
	if p3.ix != rebuilt {
		t.Fatal("rebuilt platform must carry the new index")
	}
}
