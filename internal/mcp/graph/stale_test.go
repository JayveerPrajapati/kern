package graph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestSearchNoMatchStaleNote pins N2: after a file edit, a no-match result
// must carry an inline staleness note — the result text alone reads as a
// confident "nothing found" even though recent changes may not be reflected
// in the index. The verdict reuses index.FreshnessProof (content-root
// rehash), the same source the provenance footer renders.
func TestSearchNoMatchStaleNote(t *testing.T) {
	root := graphEntitiesFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	// Make the index stale: edit an indexed file after the build.
	mainPath := filepath.Join(root, "main.go")
	orig, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, append(orig, []byte("\n// post-build edit\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if !ix.FreshnessProof(root).Stale() {
		t.Fatal("precondition failed: edited tree must yield a stale verdict")
	}
	out, err := AstSearch(context.Background(), ix, map[string]any{"pattern": "zzzznope"})
	if err != nil {
		t.Fatalf("AstSearch: %v", err)
	}
	if !strings.Contains(out, "no symbols matched: zzzznope") {
		t.Errorf("expected the no-match line, got: %q", out)
	}
	if !strings.Contains(out, "[kern] note: index is STALE (built ") {
		t.Errorf("stale index must carry the inline staleness note, got: %q", out)
	}
}

// TestSearchNoMatchFreshNoNote pins the N2 negative: a fresh index keeps the
// exact current output — no staleness note is appended.
func TestSearchNoMatchFreshNoNote(t *testing.T) {
	root := graphEntitiesFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if ix.FreshnessProof(root).Stale() {
		t.Fatal("precondition failed: unedited tree must be fresh")
	}
	out, err := AstSearch(context.Background(), ix, map[string]any{"pattern": "zzzznope"})
	if err != nil {
		t.Fatalf("AstSearch: %v", err)
	}
	if !strings.Contains(out, "no symbols matched: zzzznope") {
		t.Errorf("expected the no-match line, got: %q", out)
	}
	if strings.Contains(out, "[kern] note: index is STALE") {
		t.Errorf("fresh index must not carry the staleness note, got: %q", out)
	}
}

// TestRepoSearchNoMatchStaleNote extends N2 to the cross-repo path: the
// repo walk has no index in scope, so the miss path checks the primary
// root's persisted index best-effort — a stale root index must carry the
// same inline hint as the single-repo searches.
func TestRepoSearchNoMatchStaleNote(t *testing.T) {
	root := graphEntitiesFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(root, "main.go")
	orig, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, append(orig, []byte("\n// post-build edit\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := RepoSearch(context.Background(), root, map[string]any{"query": "zzzznope"})
	if err != nil {
		t.Fatalf("RepoSearch: %v", err)
	}
	if !strings.Contains(out, "no symbols matched across repos: zzzznope") {
		t.Errorf("expected the no-match line, got: %q", out)
	}
	if !strings.Contains(out, "[kern] note: index is STALE (built ") {
		t.Errorf("stale root index must carry the inline staleness note, got: %q", out)
	}
}

// TestRepoSearchNoMatchFreshNoNote pins the negative: a fresh root index
// keeps the exact current output — no staleness note is appended.
func TestRepoSearchNoMatchFreshNoNote(t *testing.T) {
	root := graphEntitiesFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	out, err := RepoSearch(context.Background(), root, map[string]any{"query": "zzzznope"})
	if err != nil {
		t.Fatalf("RepoSearch: %v", err)
	}
	if !strings.Contains(out, "no symbols matched across repos: zzzznope") {
		t.Errorf("expected the no-match line, got: %q", out)
	}
	if strings.Contains(out, "[kern] note: index is STALE") {
		t.Errorf("fresh index must not carry the staleness note, got: %q", out)
	}
}

// TestRepoSearchNoMatchNoIndex pins the honest-skip case: a root with no
// persisted index gets no note (the verdict cannot be derived), never an
// error.
func TestRepoSearchNoMatchNoIndex(t *testing.T) {
	root := t.TempDir()
	out, err := RepoSearch(context.Background(), root, map[string]any{"query": "zzzznope"})
	if err != nil {
		t.Fatalf("RepoSearch: %v", err)
	}
	if !strings.Contains(out, "no symbols matched across repos: zzzznope") {
		t.Errorf("expected the no-match line, got: %q", out)
	}
	if strings.Contains(out, "[kern] note: index is STALE") {
		t.Errorf("missing index must not carry the staleness note, got: %q", out)
	}
}
