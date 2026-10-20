package graph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/gov"
	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
)

func TestExploreQualifiedSourceIdentity(t *testing.T) {
	root := t.TempDir()
	for _, prefix := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(root, prefix), 0o755); err != nil {
			t.Fatal(err)
		}
		source := "package " + prefix + "\nfunc Shared() { " + prefix + "Helper() }\nfunc " + prefix + "Helper() {}\nfunc Only" + strings.ToUpper(prefix) + "() { Shared() }\n"
		if err := os.WriteFile(filepath.Join(root, prefix, "source.go"), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, governed := range []bool{false, true} {
		t.Run(fmt.Sprintf("governed=%t", governed), func(t *testing.T) {
			gvc := gov.GovContext{
				NewGov: func() (*gov.Governor, error) {
					if !governed {
						return nil, nil
					}
					return &gov.Governor{Allowed: map[string]bool{
						"Shared": true, "OnlyA": true, "OnlyB": true, "aHelper": true, "bHelper": true,
					}}, nil
				},
				StampGov: func(*gov.Governor, []provenance.SymbolProvenance) {},
				StampRaw: func([]provenance.SymbolProvenance) {},
			}
			out, err := Explore(context.Background(), ix, gvc, map[string]any{"symbol": "b.Shared"})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, "evidence: b/source.go:") {
				t.Errorf("evidence must identify selected source: %s", out)
			}
			parts := strings.SplitN(out, "== affected files ==\n", 2)
			if len(parts) != 2 {
				t.Fatalf("missing affected files: %s", out)
			}
			files := strings.SplitN(parts[1], "== source ==", 2)[0]
			if strings.Contains(files, "a/source.go") || !strings.Contains(files, "b/source.go") {
				t.Errorf("affected files must retain selected identity: %s", files)
			}
		})
	}
}

func TestExploreGovernedDistinctDeclarationRadius(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"internal/a/core.go":   "package a\nfunc Shared() {}\n",
		"internal/b/caller.go": "package b\nimport \"example.com/mod/internal/a\"\nfunc Shared() { a.Shared() }\nfunc OnlyB() { Shared() }\n",
	}
	for file, source := range files {
		path := filepath.Join(root, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	gvc := gov.GovContext{
		NewGov: func() (*gov.Governor, error) {
			return &gov.Governor{Allowed: map[string]bool{"Shared": true, "OnlyB": true}}, nil
		},
		StampGov: func(*gov.Governor, []provenance.SymbolProvenance) {},
		StampRaw: func([]provenance.SymbolProvenance) {},
	}
	out, err := Explore(context.Background(), ix, gvc, map[string]any{"symbol": "a.Shared"})
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(out, "== affected files ==\n", 2)
	if len(parts) != 2 {
		t.Fatalf("missing affected files: %s", out)
	}
	filesBlock := strings.SplitN(parts[1], "== source ==", 2)[0]
	for file := range files {
		if !strings.Contains(filesBlock, file) {
			t.Errorf("affected files lost distinct authorized declaration %s: %s", file, filesBlock)
		}
	}
}

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

// TestAstSearchNoMatchQueryClipped pins QA F7 for kern_ast_search: a long
// pattern echoed in a "no symbols matched" line must be clipped to 120
// chars, not returned in full (F4 — same contract as Search).
func TestAstSearchNoMatchQueryClipped(t *testing.T) {
	root := graphEntitiesFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("q", 5000)
	out, err := AstSearch(context.Background(), ix, map[string]any{"root": root, "pattern": long})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no symbols matched: ") {
		t.Fatalf("expected no-match line, got: %q", out)
	}
	if len(out) > 300 {
		t.Fatalf("echoed pattern must be clipped (len=%d): %q...", len(out), out[:150])
	}
	if !strings.Contains(out, "qqq...") {
		t.Fatalf("expected ellipsis-suffixed clip, got: %q", out)
	}
	// The long pattern has no kind prefix, so the kind-glob hint (6.4) must
	// follow the clipped echo.
	if !strings.Contains(out, "kind prefix") {
		t.Fatalf("no-kind-prefix no-match must carry the kind-glob hint, got: %q", out)
	}
}

// TestAstSearchNoMatchKindGlobHint pins 6.4: a no-match kern_ast_search whose
// pattern had no kind prefix hints at the kind-glob syntax (`func handle*`
// misses methods — their kind is "method"); a pattern that already uses a
// kind prefix gets no hint.
func TestAstSearchNoMatchKindGlobHint(t *testing.T) {
	root := graphEntitiesFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	// Bare pattern, no kind prefix: the hint fires.
	out, err := AstSearch(context.Background(), ix, map[string]any{"root": root, "pattern": "zzzznope*"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no symbols matched: ") {
		t.Fatalf("expected no-match line, got: %q", out)
	}
	if !strings.Contains(out, "try a kind prefix") || !strings.Contains(out, "method handle*") {
		t.Fatalf("bare no-match must hint the kind-glob syntax, got: %q", out)
	}
	// Kind-prefixed pattern: no hint.
	out, err = AstSearch(context.Background(), ix, map[string]any{"root": root, "pattern": "func zzzznope*"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "no symbols matched: ") {
		t.Fatalf("expected no-match line, got: %q", out)
	}
	if strings.Contains(out, "kind prefix") {
		t.Fatalf("kind-prefixed no-match must not hint again, got: %q", out)
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
