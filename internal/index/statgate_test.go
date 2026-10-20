package index

import (
	"os"
	"testing"
	"time"
)

// TestStaleStatGateSkipsRehash pins the dirty-tree stat-gate: repeated
// staleness checks on an unchanged tree must re-validate the retained
// observation with a STAT-ONLY walk — zero file reads, zero re-hashes —
// while any real change (mtime bump, content edit, new file) still falls
// through to the full content walk. The fixture is non-git, so every
// check takes the content-walk branch (treeOID cannot vouch) — exactly the
// dirty-tree hot path the gate exists for.
func TestStaleStatGateSkipsRehash(t *testing.T) {
	root := fixtureTree(t)
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}

	// First check: no retained snapshot yet, so the full content walk runs.
	n1, restore1 := countReads()
	if ix.Stale() {
		t.Fatal("expected fresh right after build")
	}
	restore1()
	if n1.Load() == 0 {
		t.Fatal("first check must run the content walk (nothing retained yet)")
	}

	// Second check, nothing changed: the stat-gate must decide with ZERO
	// file reads.
	n2, restore2 := countReads()
	if ix.Stale() {
		t.Fatal("expected fresh via the stat-gate")
	}
	restore2()
	if got := n2.Load(); got != 0 {
		t.Fatalf("stat-gate must not read file contents, got %d reads", got)
	}

	// Touch (mtime bump, identical content): the gate must fail and the
	// full walk must run — and correctly report FRESH (content unchanged).
	mt := time.Now().Add(1 * time.Second)
	n3, restore3 := countReads()
	touch(t, root, "a/one.go", mt)
	if ix.Stale() {
		t.Fatal("mtime bump with identical content must stay fresh (full walk decides)")
	}
	restore3()
	if n3.Load() == 0 {
		t.Fatal("mtime bump must force a real content walk")
	}

	// Content edit: gate fails, full walk, stale.
	n4, restore4 := countReads()
	writeFileAt(t, root, "a/one.go", "package a\n\nfunc One() int { return 42 }\n\nfunc helper() {}\n")
	if !ix.Stale() {
		t.Fatal("content edit must be stale")
	}
	restore4()
	if n4.Load() == 0 {
		t.Fatal("content edit must force a real content walk")
	}

	// New file: nothing recorded changed, but the walk-count leg of the
	// gate must catch it and force the full walk → stale.
	ix2, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if ix2.Stale() {
		t.Fatal("warmup: fresh build must be fresh")
	}
	n5, restore5 := countReads()
	writeFileAt(t, root, "a/added.go", "package a\n\nfunc Added() {}\n")
	if !ix2.Stale() {
		t.Fatal("new file must be stale (walk-count leg)")
	}
	restore5()
	if n5.Load() == 0 {
		t.Fatal("new file must force a real content walk")
	}
}

func touch(t *testing.T, root, rel string, mt time.Time) {
	t.Helper()
	p := root + "/" + rel
	if err := os.Chtimes(p, mt, mt); err != nil {
		t.Fatal(err)
	}
}
