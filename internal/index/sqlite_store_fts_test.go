//go:build !nosqlite

package index

import (
	"fmt"
	"strings"
	"testing"
)

// F-C4 regression tests (2026-11-22 dogfood campaign): delete+reinsert churn
// grows fts5 tombstone segments that MaybeVacuum's freelist trigger cannot
// see — TradingApp's live index was 39 MB for 3,539 symbols (36 MB of it
// symbols_fts_data). OptimizeFTS on every full Save (and MaybeCompactFTS on
// the delta path) keeps the store bounded.

// ftsChurnTestIndex returns an index whose fts content is fat AND unique per
// row (a shared padding token would collapse into one dictionary entry — the
// inverted index is proportional to UNIQUE tokens, so each row needs its own
// long token for the churn growth to be measurable).
func ftsChurnTestIndex(root string, n int) *Index {
	ix := New(root)
	for i := 0; i < n; i++ {
		ix.Symbols = append(ix.Symbols, Symbol{
			Kind: "func",
			Name: fmt.Sprintf("FtsChurnSymbol%dWithAPaddingSuffixToWidenTheRow", i),
			File: fmt.Sprintf("pkg/file_%d_fts_churn_test.go", i%25),
			Line: i + 1,
			End:  i + 5,
			Lang: "go",
			Params: []string{
				fmt.Sprintf("arg%d context.Context /* %s%d */", i, strings.Repeat("u", 500), i),
			},
		})
	}
	return ix
}

func ftsPageCount(t *testing.T, s *SQLiteStore) int64 {
	t.Helper()
	var pages int64
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatalf("page_count: %v", err)
	}
	return pages
}

// TestSQLiteSaveFTSBoundedAcrossChurnedSaves pins the full-save contract
// (F-C4): repeated delete-all+reinsert-all saves must not grow the store
// linearly, and the FTS index must stay queryable. The full-save path runs
// OptimizeFTS + maybeVacuum together; the live TradingApp verification of the
// 2026-11-22 campaign proved the compaction on a real bloated store (39 MB →
// expected few MB after rebuild with the fix).
func TestSQLiteSaveFTSBoundedAcrossChurnedSaves(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	s, err := OpenSQLite(root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = s.Close() }()

	ix := ftsChurnTestIndex(root, 2500)
	if err := s.Save(ix); err != nil {
		t.Fatalf("first save: %v", err)
	}
	first := ftsPageCount(t, s)
	if first < 1000 {
		// The store must be big enough for MaybeVacuum's small-store guard
		// not to mask the compaction; fat params guarantee that.
		t.Fatalf("test store too small to exercise the vacuum valve: %d pages", first)
	}

	for cycle := 0; cycle < 6; cycle++ {
		// Mutate every symbol so each save is a real churn cycle with fresh
		// unique tokens (a new fts segment generation, not a byte-identical
		// rewrite the dictionary could deduplicate).
		for i := range ix.Symbols {
			ix.Symbols[i].Params = []string{
				fmt.Sprintf("arg%d context.Context /* %s%d_c%d */", i, strings.Repeat("v", 500), i, cycle),
			}
		}
		if err := s.Save(ix); err != nil {
			t.Fatalf("churn save %d: %v", cycle, err)
		}
	}
	final := ftsPageCount(t, s)

	// Post-fix the store stays near the first-save size (optimize collapses
	// the segment generations, vacuum reclaims them). Pre-fix it grows by
	// roughly a full fts content generation per cycle (~6x).
	if final > first*2 {
		t.Fatalf("fts churn not compacted: first save %d pages, after 6 churned saves %d pages (F-C4 regression: delete+reinsert segments must be optimized away)", first, final)
	}

	// Correctness: the compacted index still answers FTS searches (the full
	// symbol name is a single unicode61 token, so query it whole).
	hits, err := s.SearchFTS("FtsChurnSymbol7WithAPaddingSuffixToWidenTheRow", 5)
	if err != nil {
		t.Fatalf("search after churn: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("FtsChurnSymbol7WithAPaddingSuffixToWidenTheRow must still be findable via FTS after churned saves")
	}
}
