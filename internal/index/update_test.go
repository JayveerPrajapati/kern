package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFileAt writes a file under root, creating parent dirs.
func writeFileAt(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// unchangedCount returns how many of prev's indexed files still exist on disk
// with the same content hash — the set Update must reuse, never re-parse.
func unchangedCount(t *testing.T, root string, prev *Index) int {
	t.Helper()
	cur, err := FileHashes(root)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for f, h := range cur {
		if ph, ok := prev.FileHashes[f]; ok && ph == h {
			n++
		}
	}
	return n
}

// TestUpdateEquivalentAfterModify asserts Update(root, prior) equals a full
// rebuild after one file is edited: same symbol set, same edge multiset, same
// per-file attribution, same packages and hashes.
func TestUpdateEquivalentAfterModify(t *testing.T) {
	root := fixtureTree(t)
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, root, "a/one.go", "package a\n\nfunc One() int { return 1 }\n\nfunc OneExtra() {}\n")

	inc, err := Update(root, prior)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalent(t, "modify one file", inc, full)
	if got := inc.ReusedResults(); got != unchangedCount(t, root, prior) {
		t.Errorf("modify: reused %d files, want %d (all unchanged files must skip re-parsing)", got, unchangedCount(t, root, prior))
	}
}

// TestUpdateEquivalentAfterAdd asserts Update equals a full rebuild after a
// new file is added.
func TestUpdateEquivalentAfterAdd(t *testing.T) {
	root := fixtureTree(t)
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, root, "b/four.go", "package b\n\nfunc Four() int { return 4 }\n")

	inc, err := Update(root, prior)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalent(t, "add one file", inc, full)
	if got := inc.ReusedResults(); got != unchangedCount(t, root, prior) {
		t.Errorf("add: reused %d files, want %d", got, unchangedCount(t, root, prior))
	}
	// The new symbol must be present, via the merged index only.
	if _, ok := inc.FindSymbol("Four"); !ok {
		t.Error("added symbol Four missing from incremental index")
	}
}

// TestUpdateEquivalentAfterDelete asserts Update equals a full rebuild after
// a file no other file references is deleted.
func TestUpdateEquivalentAfterDelete(t *testing.T) {
	root := fixtureTree(t)
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "b", "generated_gen.go")); err != nil {
		t.Fatal(err)
	}

	inc, err := Update(root, prior)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalent(t, "delete one file", inc, full)
	if got := inc.ReusedResults(); got != unchangedCount(t, root, prior) {
		t.Errorf("delete: reused %d files, want %d", got, unchangedCount(t, root, prior))
	}
	// Deleted file's symbols must be gone.
	for _, s := range inc.Symbols {
		if s.File == "b/generated_gen.go" {
			t.Errorf("deleted file still present in incremental index: %+v", s)
		}
	}
}

// TestUpdateLoadedPriorEquivalent exercises the reconstruction path: a prior
// loaded from disk has no per-file parse results, so Update must rebuild each
// unchanged file's contribution from prev's merged symbols/edges. The result
// must still equal a full rebuild (fixture has no inheritance edges, so
// prev's call lists carry no dispatch edges and copy order is preserved).
func TestUpdateLoadedPriorEquivalent(t *testing.T) {
	root := fixtureTree(t)
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := prior.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.fileResults) != 0 {
		t.Fatal("loaded prior must carry no per-file parse results")
	}
	// Modify one file, add one, delete one — all at once.
	writeFileAt(t, root, "a/one.go", "package a\n\nfunc One() int { return 1 }\n\nfunc OneExtra() {}\n")
	writeFileAt(t, root, "b/four.go", "package b\n\nfunc Four() int { return 4 }\n")
	if err := os.Remove(filepath.Join(root, "b", "generated_gen.go")); err != nil {
		t.Fatal(err)
	}

	inc, err := Update(root, loaded)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalent(t, "loaded prior, mixed change", inc, full)
	if got := inc.ReusedResults(); got != unchangedCount(t, root, loaded) {
		t.Errorf("loaded prior: reused %d files, want %d", got, unchangedCount(t, root, loaded))
	}
}

// TestUpdateDropsDanglingEdges asserts that removing a callee symbol drops the
// copied edges into it — for bare callees and for qualified cross-package
// callees ("a.One"), where the callee endpoint is qualified but the graph node
// ID is bare ("One"). A full rebuild keeps these edges (per-file extraction is
// syntactic), so this documents the intended incremental semantic: edges whose
// target no longer exists are pruned instead of dangling.
func TestUpdateDropsDanglingEdges(t *testing.T) {
	root := t.TempDir()
	writeFileAt(t, root, "a/one.go", "package a\n\nfunc One() int { return 1 }\n")
	writeFileAt(t, root, "a/two.go", "package a\n\nfunc Two() int { return One() }\n")
	writeFileAt(t, root, "b/three.go", "package b\n\nimport \"a\"\n\nfunc Three() int { return a.One() }\n")

	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "a", "one.go")); err != nil {
		t.Fatal(err)
	}

	inc, err := Update(root, prior)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}

	// Incremental: both the bare and the qualified edge into the removed
	// callee must be gone.
	for _, ce := range inc.Calls["Two"] {
		if ce.Target == "One" {
			t.Errorf("incremental index kept dangling bare edge Two->One")
		}
	}
	for _, ce := range inc.Calls["Three"] {
		if ce.Target == "a.One" {
			t.Errorf("incremental index kept dangling qualified edge Three->a.One")
		}
	}
	if got := inc.CallersOfName("One"); len(got) != 0 {
		t.Errorf("incremental index still reports callers of removed symbol One: %v", got)
	}

	// Full rebuild (documentation of the divergence): extraction is
	// syntactic, so the edges survive there.
	fullBare, fullQualified := false, false
	for _, ce := range full.Calls["Two"] {
		if ce.Target == "One" {
			fullBare = true
		}
	}
	for _, ce := range full.Calls["Three"] {
		if ce.Target == "a.One" {
			fullQualified = true
		}
	}
	if !fullBare || !fullQualified {
		t.Error("full rebuild should keep syntactic edges into removed callee (documents divergence)")
	}
}

// TestUpdateDoesNotReParseUnchanged pins the core B2 contract with an exact
// parse-count check: every file whose content hash matches the previous index
// must be reused (not re-parsed), regardless of whether the previous index
// came from an in-memory build or was loaded from disk.
func TestUpdateDoesNotReParseUnchanged(t *testing.T) {
	t.Run("in-memory prior", func(t *testing.T) {
		root := fixtureTree(t)
		prior, err := Build(root)
		if err != nil {
			t.Fatal(err)
		}
		writeFileAt(t, root, "b/four.go", "package b\n\nfunc Four() int { return 4 }\n")
		inc, err := Update(root, prior)
		if err != nil {
			t.Fatal(err)
		}
		want := unchangedCount(t, root, prior)
		if inc.ReusedResults() != want {
			t.Errorf("reused %d files, want %d (all unchanged files, and only those)", inc.ReusedResults(), want)
		}
		if want != 5 {
			t.Fatalf("fixture should have 5 unchanged files, got %d", want)
		}
	})
	t.Run("loaded prior", func(t *testing.T) {
		root := fixtureTree(t)
		prior, err := Build(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := prior.Save(); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		writeFileAt(t, root, "b/four.go", "package b\n\nfunc Four() int { return 4 }\n")
		inc, err := Update(root, loaded)
		if err != nil {
			t.Fatal(err)
		}
		want := unchangedCount(t, root, loaded)
		if inc.ReusedResults() != want {
			t.Errorf("reused %d files, want %d", inc.ReusedResults(), want)
		}
		if want != 5 {
			t.Fatalf("fixture should have 5 unchanged files, got %d", want)
		}
	})
}

// TestUpdateNilPriorReturnsError covers the fallback contract: Update with no
// previous index must error so callers fall back to a full Build.
func TestUpdateNilPriorReturnsError(t *testing.T) {
	root := fixtureTree(t)
	if _, err := Update(root, nil); err == nil {
		t.Fatal("Update(root, nil) must return an error")
	}
}

// TestUpdateWrongRootPriorReturnsError covers the fallback contract for a
// previous index that cannot be reused (different root): error, not a
// silently wrong incremental result.
func TestUpdateWrongRootPriorReturnsError(t *testing.T) {
	root := fixtureTree(t)
	other := fixtureTree(t)
	prior, err := Build(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Update(root, prior); err == nil {
		t.Fatal("Update with a prior from a different root must return an error")
	}
}

// TestUpdateChained checks Update(Update(...)) — the in-memory prior of the
// second call has fileResults populated by the first Update, so reuse flows
// through the per-file-results path again.
func TestUpdateChained(t *testing.T) {
	root := fixtureTree(t)
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, root, "a/one.go", "package a\n\nfunc One() int { return 1 }\n\nfunc OneExtra() {}\n")
	first, err := Update(root, prior)
	if err != nil {
		t.Fatal(err)
	}
	if first.ReusedResults() == 0 {
		t.Fatal("first update reused nothing")
	}
	writeFileAt(t, root, "b/four.go", "package b\n\nfunc Four() int { return 4 }\n")
	second, err := Update(root, first)
	if err != nil {
		t.Fatal(err)
	}
	full, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	assertEquivalent(t, "chained update", second, full)
	if second.ReusedResults() != unchangedCount(t, root, first) {
		t.Errorf("chained: reused %d files, want %d", second.ReusedResults(), unchangedCount(t, root, first))
	}
}

// TestUpdateLoadedPriorSkipsIgnoredFiles ensures the incremental walk applies
// the same .gitignore/.kernignore rules as Build: a gitignored file added
// after the prior build is not indexed by either path.
func TestUpdateLoadedPriorSkipsIgnoredFiles(t *testing.T) {
	root := fixtureTree(t)
	writeFileAt(t, root, ".gitignore", "ignored/\n")
	prior, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	writeFileAt(t, root, "ignored/secret.py", "def leaked():\n    return 1\n")
	inc, err := Update(root, prior)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := inc.FindSymbol("leaked"); ok {
		t.Error("gitignored file was indexed by Update")
	}
	if _, ok := inc.FileHashes["ignored/secret.py"]; ok {
		t.Error("gitignored file hash recorded by Update")
	}
	// The unignored addition is still picked up.
	writeFileAt(t, root, "b/four.go", "package b\n\nfunc Four() int { return 4 }\n")
	inc2, err := Update(root, inc)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := inc2.FindSymbol("Four"); !ok {
		t.Error("non-ignored added file missing from second Update")
	}
	if strings.Contains(inc2.Identity.ContentRoot, "leaked") {
		t.Error("ignored file leaked into content identity")
	}
}
