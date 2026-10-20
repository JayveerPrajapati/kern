package intel

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Two packages define `New`. Changing a.New must be scored on a.New's own
// callers only — not on the callers of the unrelated b.New — and must not be
// flagged cross-package because of them.
func TestAnalyzeChangesDoesNotPoolSameNamedSymbols(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"go.mod": "module example.com/mod\n\ngo 1.22\n",
		"a/a.go": "package a\n\nfunc New() int { return 1 }\n\nfunc UseA() int { return New() }\n",
		"b/b.go": "package b\n\nfunc New() int { return 2 }\n",
		"c/c.go": "package c\n\nimport \"example.com/mod/b\"\n\nfunc UseB() int { return b.New() }\n",
		"d/d.go": "package d\n\nimport \"example.com/mod/b\"\n\nfunc UseB2() int { return b.New() }\n",
		"e/e.go": "package e\n\nimport \"example.com/mod/b\"\n\nfunc UseB3() int { return b.New() }\n",
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}

	rep := AnalyzeChangesRanged(ix, []FileChange{{File: "a/a.go", Ranges: []LineRange{{3, 3}}}})
	if len(rep.Changes) != 1 {
		t.Fatalf("want 1 change, got %+v", rep.Changes)
	}
	ch := rep.Changes[0]
	if ch.Callers != 1 {
		t.Errorf("a.New has exactly one caller (UseA); got %d", ch.Callers)
	}
	if ch.CrossPkg {
		t.Error("a.New is only called from its own package; must not be flagged cross-package")
	}
}
