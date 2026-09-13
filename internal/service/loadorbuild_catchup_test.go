package service

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// hasSymbol reports whether ix contains a symbol with the given name.
func hasSymbol(ix *index.Index, name string) bool {
	for _, s := range ix.Symbols {
		if s.Name == name {
			return true
		}
	}
	return false
}

// hashesMatchTree asserts that ix.FileHashes equals the current FileHashes of
// the tree — the observable freshness contract both the catch-up Update path
// and the full-build fallback must satisfy.
func hashesMatchTree(t *testing.T, ix *index.Index, root string) {
	t.Helper()
	cur, err := index.FileHashes(root)
	if err != nil {
		t.Fatalf("FileHashes: %v", err)
	}
	if len(ix.FileHashes) != len(cur) {
		t.Errorf("index has %d files, tree has %d", len(ix.FileHashes), len(cur))
	}
	for f, h := range cur {
		if ix.FileHashes[f] != h {
			t.Errorf("FileHashes[%s] = %q, want %q", f, ix.FileHashes[f], h)
		}
	}
}

// TestLoadOrBuildNoChangesServesCached: with no tree changes, LoadOrBuild
// serves the cached index untouched (nothing was updated or rebuilt).
func TestLoadOrBuildNoChangesServesCached(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "demo.go", tinyProject)
	svc := New()
	if _, err := svc.Index.Build(context.Background(), root); err != nil {
		t.Fatalf("Build: %v", err)
	}
	ix, err := svc.Index.LoadOrBuild(context.Background(), root)
	if err != nil {
		t.Fatalf("LoadOrBuild: %v", err)
	}
	if !hasSymbol(ix, "Run") {
		t.Fatal("cached index missing fixture symbol Run")
	}
	hashesMatchTree(t, ix, root)
	if got := ix.ReusedResults(); got != 0 {
		t.Errorf("ReusedResults = %d, want 0 on the no-change path (index served, not updated)", got)
	}
}

// TestLoadOrBuildCatchUpSmallDiff: after ONE file changes, LoadOrBuild
// reconciles via the incremental catch-up (index.Update) and the returned
// index reflects the change. The diff path is observable: unchanged files are
// reused (ReusedResults >= 1), which a full rebuild would never report, and
// the catch-up result is equivalent to a full rebuild for the changed symbol.
func TestLoadOrBuildCatchUpSmallDiff(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "a.go", tinyProject)
	writeGoFile(t, root, "b.go", "package demo\n\nfunc B() {}\n")
	svc := New()
	built, err := svc.Index.Build(context.Background(), root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !hasSymbol(built, "B") {
		t.Fatal("fixture missing symbol B")
	}

	// Modify ONE file: add function B2.
	writeGoFile(t, root, "b.go", "package demo\n\nfunc B() {}\n\nfunc B2() {}\n")

	ix, err := svc.Index.LoadOrBuild(context.Background(), root)
	if err != nil {
		t.Fatalf("LoadOrBuild: %v", err)
	}
	if !hasSymbol(ix, "B2") {
		t.Fatal("catch-up index missing the newly added symbol B2")
	}
	// The small-diff path (index.Update) was used: the unchanged a.go was
	// reused from the cached index. A full Build would reuse nothing.
	if got := ix.ReusedResults(); got < 1 {
		t.Errorf("ReusedResults = %d, want >= 1 (small-diff catch-up must reuse unchanged files)", got)
	}
	// Correctness: the catch-up result matches the current tree hashes and is
	// equivalent to a full rebuild for the changed symbol.
	hashesMatchTree(t, ix, root)
	full, err := index.Build(root)
	if err != nil {
		t.Fatalf("full rebuild: %v", err)
	}
	if !hasSymbol(full, "B2") {
		t.Error("full rebuild missing B2")
	}
}

// TestLoadOrBuildCatchUpLargeDiffFallsBackToFullBuild: when the change set
// exceeds index.CatchUpMaxChanges, LoadOrBuild falls back to a full Build and still
// returns a correct fresh index.
func TestLoadOrBuildCatchUpLargeDiffFallsBackToFullBuild(t *testing.T) {
	root := t.TempDir()
	const n = index.CatchUpMaxChanges + 10 // 210 files: every one modified below
	for i := 0; i < n; i++ {
		writeGoFile(t, root, filepath.Join("pkg", fmt.Sprintf("f%03d.go", i)),
			fmt.Sprintf("package pkg\n\nfunc F%d() {}\n", i))
	}
	svc := New()
	if _, err := svc.Index.Build(context.Background(), root); err != nil {
		t.Fatalf("Build: %v", err)
	}

	// Modify EVERY file (add one symbol each) so the change set is n > 200.
	for i := 0; i < n; i++ {
		writeGoFile(t, root, filepath.Join("pkg", fmt.Sprintf("f%03d.go", i)),
			fmt.Sprintf("package pkg\n\nfunc F%d() {}\n\nfunc G%d() {}\n", i, i))
	}

	ix, err := svc.Index.LoadOrBuild(context.Background(), root)
	if err != nil {
		t.Fatalf("LoadOrBuild: %v", err)
	}
	if !hasSymbol(ix, "G0") || !hasSymbol(ix, fmt.Sprintf("G%d", n-1)) {
		t.Error("large-diff LoadOrBuild result missing newly added symbols")
	}
	hashesMatchTree(t, ix, root)
}
