package astrefs

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/indexmem"
)

// TestMemoizedLookupUsesCacheWithinGeneration pins the daemon-side memo: an
// answer cached for (index, symbol) is served without rescanning, and the
// cache is dropped when the index generation changes — so a rebuild can never
// be answered from a scan of the previous tree.
func TestMemoizedLookupUsesCacheWithinGeneration(t *testing.T) {
	MemoizeScans = true
	t.Cleanup(func() {
		MemoizeScans = false
		indexmem.ResetGenForTest()
	})

	ix := &index.Index{}
	sym := index.Symbol{Name: "Target", File: "pkg/target.go"}

	// Seed the cache directly: a hit must return the seeded scan verbatim
	// (proving Lookup consulted the memo rather than rescanning).
	indexmem.GenPut(ix, "astrefs.refs", sym.Name+"\x00"+sym.File, refsAnswer{
		refs:     []Reference{{File: "seeded.go", Line: 7, Kind: "call"}},
		complete: true,
	})

	got := Lookup(ix, sym)
	if got.Count != 1 || len(got.Files) != 1 || got.Files[0] != "seeded.go" {
		t.Fatalf("expected the memoized answer, got %+v", got)
	}

	// A new index generation must not see the old entry.
	rebuilt := &index.Index{}
	got = Lookup(rebuilt, sym)
	if got.Count == 1 && len(got.Files) == 1 && got.Files[0] == "seeded.go" {
		t.Fatalf("a new index generation must not serve the old scan: %+v", got)
	}
}
