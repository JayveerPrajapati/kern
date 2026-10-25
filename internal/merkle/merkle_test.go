package merkle

import (
	"fmt"
	"testing"
)

// TestIncrementalTreeEquivalence verifies the incremental tree root matches
// ComputeRoot for every N up to 256 — the incremental path must agree with
// the recompute-from-scratch root exactly.
func TestIncrementalTreeEquivalence(t *testing.T) {
	tree := NewTree()
	var leaves []string
	for i := 1; i <= 256; i++ {
		leaf := fmt.Sprintf("leaf-%d-hash", i)
		leaves = append(leaves, leaf)
		tree.Append(leaf)

		want := ComputeRoot(leaves)
		got := tree.Root()
		if got != want {
			t.Fatalf("N=%d: tree.Root() = %s, want ComputeRoot = %s", i, got, want)
		}
	}
}

func BenchmarkIncrementalAppend(b *testing.B) {
	tree := NewTree()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		leaf := fmt.Sprintf("leaf-%d", i)
		tree.Append(leaf)
	}
}
