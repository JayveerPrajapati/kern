package main

import (
	"reflect"
	"testing"
)

// TestParseDiffLineNumbers_RealHunkNumbers: the hunk parser must return REAL
// line numbers from `@@ -a,b +c,d @@` headers (not the diff line content the
// Phase 1 implementation stored). Covers multi-hunk accumulation, a
// pure-deletion hunk (+0,0), a pure-addition hunk (-0,0), and a rename.
func TestParseDiffLineNumbers_RealHunkNumbers(t *testing.T) {
	// Multi-hunk modified file: first hunk replaces old 3-4 with new 3-4,
	// second hunk removes old 9 only (pure deletion) and adds new 12-13.
	diff := `diff --git a/f.go b/f.go
index 111..222 100644
--- a/f.go
+++ b/f.go
@@ -3,2 +3,2 @@
-x
+y
@@ -9 +9,0 @@
-z
@@ -11,0 +12,2 @@
+a
+b
`
	added, removed := parseDiffLineNumbers(diff)
	wantAdded := []string{"3", "4", "12", "13"}
	wantRemoved := []string{"3", "4", "9"}
	if !reflect.DeepEqual(added, wantAdded) {
		t.Errorf("added = %v, want %v", added, wantAdded)
	}
	if !reflect.DeepEqual(removed, wantRemoved) {
		t.Errorf("removed = %v, want %v", removed, wantRemoved)
	}
}

// TestParseDiffLineNumbers_PureDeletionHunk: a hunk that only removes lines
// (`+0,0`) yields added=[] and the real removed line numbers.
func TestParseDiffLineNumbers_PureDeletionHunk(t *testing.T) {
	diff := `diff --git a/old.go b/old.go
--- a/old.go
+++ /dev/null
@@ -1,2 +0,0 @@
-// old
-// gone
`
	added, removed := parseDiffLineNumbers(diff)
	if len(added) != 0 {
		t.Errorf("added = %v, want empty for a pure-deletion hunk", added)
	}
	if want := []string{"1", "2"}; !reflect.DeepEqual(removed, want) {
		t.Errorf("removed = %v, want %v", removed, want)
	}
}

// TestParseDiffLineNumbers_PureAdditionHunk: a hunk that only adds lines
// (`-0,0`) yields removed=[] and the real added line numbers.
func TestParseDiffLineNumbers_PureAdditionHunk(t *testing.T) {
	diff := `diff --git a/new.go b/new.go
new file mode 100644
--- /dev/null
+++ b/new.go
@@ -0,0 +1,3 @@
+package main
+
+func main() {}
`
	added, removed := parseDiffLineNumbers(diff)
	if want := []string{"1", "2", "3"}; !reflect.DeepEqual(added, want) {
		t.Errorf("added = %v, want %v", added, want)
	}
	if len(removed) != 0 {
		t.Errorf("removed = %v, want empty for a pure-addition hunk", removed)
	}
}

// TestParseDiffLineNumbers_Rename: a content-identical rename emits no hunks,
// so no line numbers are attached (and the parser must not misparse the
// rename block as content).
func TestParseDiffLineNumbers_Rename(t *testing.T) {
	diff := `diff --git a/old.go b/new.go
similarity index 100%
rename from old.go
rename to new.go
`
	added, removed := parseDiffLineNumbers(diff)
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("rename with no content change must carry no line numbers; got added=%v removed=%v", added, removed)
	}
}

// TestSplitDiffBlocks_MatchesByNewPath: the combined-diff splitter keys blocks
// by the new path (b/ side) so deletions, additions, and renames all attach
// to the right FileChange.
func TestSplitDiffBlocks_MatchesByNewPath(t *testing.T) {
	diff := `diff --git a/del.go b/del.go
deleted file mode 100644
--- a/del.go
+++ /dev/null
@@ -1 +0,0 @@
-old
diff --git a/new.go b/new.go
new file mode 100644
--- /dev/null
+++ b/new.go
@@ -0,0 +1 @@
+new
diff --git a/old.go b/renamed.go
similarity index 100%
rename from old.go
rename to renamed.go
`
	blocks := splitDiffBlocks(diff)
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d, want 3 (del, new, rename)", len(blocks))
	}
	for _, path := range []string{"del.go", "new.go", "renamed.go"} {
		if _, ok := blocks[path]; !ok {
			t.Errorf("missing block for %q; keys: %v", path, keys(blocks))
		}
	}
	// Binary blocks keep their text but carry no hunks.
	if !isBinaryDiffBlock("Binary files a/x and b/y differ") {
		t.Error("isBinaryDiffBlock should detect binary blocks")
	}
	if isBinaryDiffBlock(blocks["new.go"]) {
		t.Error("isBinaryDiffBlock false-positive on a text block")
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
