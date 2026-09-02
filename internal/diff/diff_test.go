package diff

import (
	"fmt"
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
// line 1 emits a valid header (never "+0,N") .
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
// unchanged lines are merged into one hunk .
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

// TestCompactCollapsesContextRuns verifies long unchanged runs collapse into
// annotation lines while every changed line is preserved verbatim.
func TestCompactCollapsesContextRuns(t *testing.T) {
	a := make([]string, 120)
	for i := range a {
		a[i] = fmt.Sprintf("line %d", i+1)
	}
	b := make([]string, 0, 125)
	b = append(b, a[:49]...) // lines 1-49 unchanged
	b = append(b, "replaced 50", "replaced 51", "replaced 52", "replaced 53")
	b = append(b, "added after 53")
	b = append(b, a[53:]...) // lines 54-120 unchanged
	got := Compact("old.go", "new.go", a, b, nil)
	if !strings.HasPrefix(got, "--- a/old.go\n+++ b/new.go\n") {
		t.Fatalf("expected header first, got:\n%s", got)
	}
	if !strings.Contains(got, " ... 49 lines unchanged") {
		t.Fatalf("expected 49-line annotation, got:\n%s", got)
	}
	if !strings.Contains(got, " ... 67 lines unchanged") {
		t.Fatalf("expected 67-line annotation, got:\n%s", got)
	}
	for _, w := range []string{
		"-line 50", "-line 51", "-line 52", "-line 53",
		"+replaced 50", "+replaced 51", "+replaced 52", "+replaced 53",
		"+added after 53",
	} {
		if !strings.Contains(got, w) {
			t.Fatalf("expected %q verbatim in compact diff:\n%s", w, got)
		}
	}
}

// TestCompactShortRunsVerbatim verifies context runs of <= 2 lines stay
// verbatim and no annotation lines appear.
func TestCompactShortRunsVerbatim(t *testing.T) {
	a := []string{"l1", "l2", "l3", "l4", "l5", "l6", "l7"}
	b := []string{"l1", "l2", "l3x", "l4", "l5", "l6x", "l7"}
	got := Compact("a.txt", "b.txt", a, b, nil)
	if strings.Contains(got, "lines unchanged") {
		t.Fatalf("no annotation expected for short runs:\n%s", got)
	}
	for _, w := range []string{" l1", " l2", " l4", " l5", " l7", "-l3", "+l3x", "-l6", "+l6x"} {
		if !strings.Contains(got, w) {
			t.Fatalf("expected %q verbatim:\n%s", w, got)
		}
	}
}

// TestCompactSpanAnnotation verifies the span resolver annotates the run whose
// first line falls inside the resolved span.
func TestCompactSpanAnnotation(t *testing.T) {
	a := make([]string, 100)
	for i := range a {
		a[i] = fmt.Sprintf("line %d", i+1)
	}
	b := make([]string, len(a))
	copy(b, a)
	b[49] = "changed 50"
	resolve := func(file string, line int) string {
		if line >= 40 && line <= 55 {
			return "main"
		}
		return ""
	}
	got := Compact("app.go", "app.go", a, b, resolve)
	if !strings.Contains(got, " ... 50 lines unchanged in main ...") {
		t.Fatalf("expected span-annotated trailing run, got:\n%s", got)
	}
	if strings.Contains(got, "49 lines unchanged in main") {
		t.Fatalf("leading run (first line 1) must not be span-annotated:\n%s", got)
	}
}

// TestCompactNilResolver verifies a nil resolver yields annotations without
// span text.
func TestCompactNilResolver(t *testing.T) {
	a := make([]string, 80)
	for i := range a {
		a[i] = fmt.Sprintf("line %d", i+1)
	}
	b := make([]string, len(a))
	copy(b, a)
	b[39] = "changed 40"
	got := Compact("a.txt", "b.txt", a, b, nil)
	if !strings.Contains(got, "lines unchanged") {
		t.Fatalf("expected annotation, got:\n%s", got)
	}
	if strings.Contains(got, " in ") {
		t.Fatalf("nil resolver must not annotate spans:\n%s", got)
	}
}

// TestCompactDeterministic verifies identical inputs produce identical output.
func TestCompactDeterministic(t *testing.T) {
	a := make([]string, 60)
	for i := range a {
		a[i] = fmt.Sprintf("line %d", i+1)
	}
	b := make([]string, len(a))
	copy(b, a)
	b[20], b[40] = "x20", "x40"
	first := Compact("a.txt", "b.txt", a, b, nil)
	second := Compact("a.txt", "b.txt", a, b, nil)
	if first != second {
		t.Fatalf("compact output must be deterministic")
	}
}

// TestCompactTrailingAndLeadingRuns verifies collapsed runs on both the very
// start (trailing run) and the very end (leading run) of a file.
func TestCompactTrailingAndLeadingRuns(t *testing.T) {
	mk := func(n int) []string {
		s := make([]string, n)
		for i := range s {
			s[i] = fmt.Sprintf("line %d", i+1)
		}
		return s
	}
	// Change at the very start: trailing run collapsed.
	a := mk(100)
	b := append([]string{"replaced first"}, a[1:]...)
	got := Compact("a.txt", "b.txt", a, b, nil)
	if !strings.Contains(got, " ... 99 lines unchanged") {
		t.Fatalf("expected collapsed trailing run, got:\n%s", got)
	}
	// Change at the very end: leading run collapsed.
	a2 := mk(100)
	b2 := make([]string, 100)
	copy(b2, a2)
	b2[99] = "replaced last"
	got2 := Compact("a.txt", "b.txt", a2, b2, nil)
	if !strings.Contains(got2, " ... 99 lines unchanged") {
		t.Fatalf("expected collapsed leading run, got:\n%s", got2)
	}
}
