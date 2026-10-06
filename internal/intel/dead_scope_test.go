package intel

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func TestDeadCodeSameNamedCallersDowngradeToUncertain(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a/a.go": "package a\n\nfunc helper() int { return 1 }\n",
		"b/b.go": "package b\n\nfunc helper() int { return 2 }\n\nfunc Run() int { return helper() }\n",
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, d := range DeadCode(ix) {
		if d.Name == "helper" && strings.HasPrefix(d.File, "a/") {
			found = true
			if d.Confidence != ConfidenceUncertain || d.Note == "" {
				t.Fatalf("a.helper has unattributable same-named callers; want uncertain+note, got %+v", d)
			}
			if out := RenderDead([]DeadSymbol{d}); strings.Contains(out, "certainly dead") {
				t.Fatalf("must not claim certainty:\n%s", out)
			}
		}
	}
	if !found {
		t.Skip("a.helper not reported dead by this index build")
	}
}

func TestDeadCodeTestOnlyCallersMarked(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"lib/lib.go":      "package lib\n\nfunc onlyTested() int { return 1 }\n",
		"lib/lib_test.go": "package lib\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) { onlyTested() }\n",
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range DeadCode(ix) {
		if d.Name == "onlyTested" {
			if !d.TestOnly || !strings.Contains(deadCaveat(d), "test-only callers") {
				t.Fatalf("want test-only marker, got %+v / %q", d, deadCaveat(d))
			}
			return
		}
	}
	t.Fatal("onlyTested should be reported (test-only callers)")
}

func TestFilterDeadByPath(t *testing.T) {
	dead := []DeadSymbol{
		{Name: "x", File: "internal/a/x.go"},
		{Name: "y", File: "internal/ab/y.go"},
		{Name: "z", File: "cmd/z.go"},
	}
	if got := FilterDeadByPath(dead, "internal/a"); len(got) != 1 || got[0].Name != "x" {
		t.Fatalf("prefix must match whole path segments, got %v", got)
	}
	if got := FilterDeadByPath(dead, "", "."); len(got) != 3 {
		t.Fatalf("no usable prefix keeps all, got %v", got)
	}
	if got := FilterDeadByPath(dead, "cmd/", "internal/ab"); len(got) != 2 {
		t.Fatalf("multiple prefixes, got %v", got)
	}
}

func TestRenderDeadLimitedSummaryFirstAndNotTruncated(t *testing.T) {
	dead := []DeadSymbol{
		{Name: "a", Kind: "func", File: "f.go", Confidence: ConfidenceCertain},
		{Name: "b", Kind: "func", File: "f.go", Confidence: ConfidenceUncertain, TestOnly: true},
		{Name: "C", Kind: "func", File: "f.go", Public: true, Confidence: ConfidenceProbable},
	}
	out := RenderDeadLimited(dead, 1)
	if !strings.HasPrefix(out, "summary: 3 dead symbols (2 private, 1 public-API; 1 test-only, 1 uncertain)") {
		t.Fatalf("summary must come first with full totals:\n%s", out)
	}
	if !strings.Contains(out, "2 more not shown (limit 1)") {
		t.Fatalf("truncation must be announced:\n%s", out)
	}
	if strings.Contains(RenderDeadLimited(dead, 0), "not shown") {
		t.Fatal("no limit means nothing hidden")
	}
}
