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
		"+ cruel",
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
