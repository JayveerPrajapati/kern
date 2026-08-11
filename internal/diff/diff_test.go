package diff

import (
	"strings"
	"testing"
)

func split(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

func TestUnifiedIdentical(t *testing.T) {
	a := []string{"x", "y"}
	if got := Unified("a", "b", a, []string{"x", "y"}); got != "" {
		t.Fatalf("identical should be empty, got %q", got)
	}
}

func TestUnifiedDeleteInsert(t *testing.T) {
	a := split("hello\nworld\nfoo")
	b := split("hello\ncruel\nworld\nfoo")
	got := Unified("old.txt", "new.txt", a, b)
	wantContains := []string{
		"--- a/old.txt",
		"+++ b/new.txt",
		"@@",
		"+cruel",
	}
	for _, w := range wantContains {
		if !strings.Contains(got, w) {
			t.Fatalf("expected %q in diff:\n%s", w, got)
		}
	}
	if strings.Contains(got, "-world") {
		t.Fatalf("world should remain unchanged:\n%s", got)
	}
}

func TestUnifiedLabelPathNormalized(t *testing.T) {
	a := split("foo")
	b := split("bar")
	got := Unified("/tmp/fa", "./fb", a, b)
	if !strings.Contains(got, "--- a/tmp/fa") || strings.Contains(got, "a//tmp") {
		t.Fatalf("absolute path label not normalized:\n%s", got)
	}
	if !strings.Contains(got, "+++ b/fb") {
		t.Fatalf("relative path label not normalized:\n%s", got)
	}
	if strings.Contains(got, "- foo\n") || !strings.Contains(got, "-foo") {
		t.Fatalf("removed line should render without a space after '-':\n%s", got)
	}
	if !strings.Contains(got, "+bar") || strings.Contains(got, "+ bar") {
		t.Fatalf("added line should render as '+bar':\n%s", got)
	}
}

func TestDiffLinesEditScript(t *testing.T) {
	a := []string{"1", "2", "3"}
	b := []string{"1", "x", "3"}
	ops := DiffLines(a, b)
	var kinds []byte
	for _, op := range ops {
		kinds = append(kinds, op.Kind)
	}
	// The exact ordering: equal "1", delete "2", insert "x", equal "3"
	// (backtrack tie-break may swap del/ins order; just require both present).
	s := string(kinds)
	if !strings.Contains(s, " ") || !strings.Contains(s, "-") || !strings.Contains(s, "+") {
		t.Fatalf("expected mix of kinds, got %q", s)
	}
}

func TestCoarseFallback(t *testing.T) {
	a := make([]string, 3000)
	b := make([]string, 3000)
	for i := range a {
		a[i] = "line"
		b[i] = "line"
	}
	b[1500] = "changed"
	// 3000*3000 = 9e6 > 5e6 → coarse path
	ops := DiffLines(a, b)
	if len(ops) < 5999 {
		t.Fatalf("coarse path should emit ~6000 ops, got %d", len(ops))
	}
}

// TestLeadingDeletionHunkHeader verifies a hunk beginning with a deletion from
// line 1 emits a valid header (never "+0,N") (W2-48).
func TestLeadingDeletionHunkHeader(t *testing.T) {
	a := []string{"x", "y", "z"}
	b := []string{"z"} // delete line 1 "x"
	got := Unified("a", "b", a, b)
	if strings.Contains(got, "+0,") {
		t.Fatalf("invalid +0 start header:\n%s", got)
	}
	if !strings.Contains(got, "@@ -1,3 +1,1 @@") {
		t.Fatalf("expected @@ -1,3 +1,1 @@ (delete x,y keep z), got:\n%s", got)
	}
}

// TestHunksMergeWithinDoubleContext verifies two change clusters within 2*ctx
// unchanged lines are merged into one hunk (W2-47).
func TestHunksMergeWithinDoubleContext(t *testing.T) {
	// change at line 1, unchanged gap of 6 (=2*ctx), change at line 8
	ctx := 3
	a := []string{"L1a", "L2", "L3", "L4", "L5", "L6", "L7", "L8a"}
	b := []string{"L1b", "L2", "L3", "L4", "L5", "L6", "L7", "L8b"}
	ops := DiffLines(a, b)
	hunks := groupHunks(ops, ctx)
	if len(hunks) != 1 {
		t.Fatalf("expected 1 merged hunk, got %d:\n%v", len(hunks), hunks)
	}
}
