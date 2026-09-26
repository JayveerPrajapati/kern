package graph

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/gov"
	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
)

// TestExploreUnknownSymbolSuggestsCandidates pins the did-you-mean hint on a
// total-miss kern_explore: the bare "unknown symbol" error must be followed
// by the strongest ranked candidates (matching the CLI), so an agent can
// recover instead of guessing. The query must NOT be auto-resolved by
// intel's fuzzy fallback (which handles clean case/typo variants), so it is a
// partial match: "serve health mux" strongly matches ServeMux's segments but
// not every word, leaving it unresolvable while still scoring >= 150.
func TestExploreUnknownSymbolSuggestsCandidates(t *testing.T) {
	root := graphEntitiesFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Explore(context.Background(), ix, gov.GovContext{}, map[string]any{"root": root, "symbol": "serve health mux"})
	if err == nil {
		t.Fatal("explore of a missing symbol must error")
	}
	if !strings.Contains(err.Error(), "unknown symbol") {
		t.Fatalf("expected unknown-symbol error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "did you mean one of: ServeMux") {
		t.Fatalf("expected did-you-mean suggestion, got: %v", err)
	}
}

// TestExploreUnknownSymbolNoWeakSuggestions pins the 150-score floor: a miss
// with no strong candidates must NOT fabricate a suggestion list. The
// "zzzznope" query matches nothing, so the error stays bare.
func TestExploreUnknownSymbolNoWeakSuggestions(t *testing.T) {
	root := graphEntitiesFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Explore(context.Background(), ix, gov.GovContext{}, map[string]any{"root": root, "symbol": "zzzznope"})
	if err == nil {
		t.Fatal("explore of a missing symbol must error")
	}
	if strings.Contains(err.Error(), "did you mean") {
		t.Fatalf("no strong candidates: expected bare error, got: %v", err)
	}
}

// TestSearchNoMatchQueryClipped pins QA F7: a 5000-char query echoed in a
// "no symbols matched" line must be clipped to 120 chars, not returned in
// full.
func TestSearchNoMatchQueryClipped(t *testing.T) {
	root := graphEntitiesFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	// Minimal raw-mode bundle: nil governor (no filtering), no-op stamps —
	// Search calls NewGov/StampGov on the no-match path.
	gvc := gov.GovContext{
		NewGov:   func() (*gov.Governor, error) { return nil, nil },
		StampGov: func(*gov.Governor, []provenance.SymbolProvenance) {},
		StampRaw: func([]provenance.SymbolProvenance) {},
	}
	long := strings.Repeat("q", 5000)
	out, err := Search(context.Background(), ix, gvc, map[string]any{"root": root, "query": long})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no symbols matched: ") {
		t.Fatalf("expected no-match line, got: %q", out)
	}
	if len(out) > 200 {
		t.Fatalf("echoed query must be clipped (len=%d): %q...", len(out), out[:150])
	}
	if !strings.HasSuffix(out, "qqq...") {
		t.Fatalf("expected ellipsis-suffixed clip, got: %q", out)
	}
}
