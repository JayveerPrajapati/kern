package swap

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

func writeFixture(t *testing.T) (root, doc string) {
	t.Helper()
	root = t.TempDir()
	var src strings.Builder
	src.WriteString("package a\n\n")
	for i := 0; i < 40; i++ {
		src.WriteString("func Fn" + itoa(i) + "(x int) string {\n\treturn \"body with padding padding padding padding padding\" + x\n}\n\n")
	}
	_ = os.WriteFile(filepath.Join(root, "a.go"), []byte(src.String()), 0o644)
	doc = "```go:a.go\n" + src.String() + "```\n"
	return root, doc
}

func itoa(n int) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.Repeat(" ", 0), " ", "")) + fmt.Sprintf("%d", n)
}

func TestSummaryMode(t *testing.T) {
	root, doc := writeFixture(t)
	out := SummaryMode(doc, root)
	if !strings.Contains(out, ":summary") {
		t.Fatalf("expected summary marker, got:\n%s", out)
	}
	if strings.Contains(out, `return "hi"`) {
		t.Fatalf("summary should not contain body:\n%s", out)
	}
}

func TestExpandRoundTrip(t *testing.T) {
	root, doc := writeFixture(t)
	summed := SummaryMode(doc, root)
	expanded := ExpandMode(summed, root)
	if !strings.Contains(expanded, `body with padding`) {
		t.Fatalf("expected body restored:\n%.100s", expanded)
	}
}

func TestSummaryModeLeavesMissingFiles(t *testing.T) {
	doc := "```go:nope.go\nwhatever\n```\n"
	out := SummaryMode(doc, t.TempDir())
	if out != doc {
		t.Fatalf("missing file should be untouched, got:\n%s", out)
	}
}

func TestFitUnderBudgetUnchanged(t *testing.T) {
	root, doc := writeFixture(t)
	out, fits := Fit(doc, root, 10000)
	if !fits || out != doc {
		t.Fatalf("expected unchanged under budget, fits=%v", fits)
	}
}

func TestFitOverBudgetSwaps(t *testing.T) {
	root, doc := writeFixture(t)
	toks := tokenize.CountKind(doc, tokenize.KindGeneric)
	out, fits := Fit(doc, root, toks-1)
	if !fits {
		t.Fatal("summary swap should bring the document under budget")
	}
	if !strings.Contains(out, ":summary") {
		t.Fatalf("expected summarization under pressure:\n%.100s", out)
	}
}

func TestFitBlocksOverBudgetSwapsAndReports(t *testing.T) {
	root, doc := writeFixture(t)
	toks := tokenize.CountKind(doc, tokenize.KindGeneric)
	out, fits, swapped := FitBlocks(doc, root, toks-1)
	if !fits {
		t.Fatal("summary swap should bring the document under budget")
	}
	if len(swapped) != 1 || swapped[0] != "a.go" {
		t.Fatalf("expected swapped=[a.go], got %v", swapped)
	}
	if !strings.Contains(out, ":summary") {
		t.Fatalf("expected summarization under pressure:\n%.100s", out)
	}
}

func TestFitBlocksMissingFileUntouched(t *testing.T) {
	// The 5-token budget below is tuned to the estimator's ~4 chars/token
	// density; under the exact BPE default the block can no longer survive a
	// budget that small. Force the estimator via the env seam so this test
	// keeps asserting its real intent (missing file -> block survives, never
	// swapped) regardless of the active default tokenizer.
	t.Setenv("KERN_TOKENIZER", "estimate")
	tokenize.InitFromEnv()
	defer tokenize.ResetDefault()
	doc := "intro\n\n```go:nope.go\nwhatever\n```\n\noutro\n"
	out, _, swapped := FitBlocks(doc, t.TempDir(), 5)
	if len(swapped) != 0 {
		t.Fatalf("missing file must not be swapped, got %v", swapped)
	}
	if !strings.Contains(out, "nope.go") {
		t.Fatalf("block must survive: %q", out)
	}
}

func TestFitBlocksUnderBudgetUnchanged(t *testing.T) {
	root, doc := writeFixture(t)
	out, fits, swapped := FitBlocks(doc, root, 100000)
	if !fits || out != doc {
		t.Fatalf("expected unchanged under budget, fits=%v", fits)
	}
	if len(swapped) != 0 {
		t.Fatalf("expected no swaps under budget, got %v", swapped)
	}
}

func TestExpandModeBudgetInflatesWhenFits(t *testing.T) {
	root, doc := writeFixture(t)
	summed := SummaryMode(doc, root)
	out, fits := ExpandModeBudget(summed, root, 100000)
	if !fits {
		t.Fatal("generous budget must expand fully with fits=true")
	}
	if !strings.Contains(out, "body with padding") {
		t.Fatalf("expected full body restored:\n%.100s", out)
	}
	if strings.Contains(out, ":summary") {
		t.Fatal("no summary markers should remain after full expansion")
	}
}

func TestExpandModeBudgetKeepsSummaryWhenOverBudget(t *testing.T) {
	root, doc := writeFixture(t)
	summed := SummaryMode(doc, root)
	sumToks := tokenize.CountKind(summed, tokenize.KindGeneric)
	// Budget enough for the summary but far below the full file content.
	out, fits := ExpandModeBudget(summed, root, sumToks+10)
	if !fits {
		t.Fatal("kept summaries must fit the budget")
	}
	if n := tokenize.CountKind(out, tokenize.KindGeneric); n > sumToks+10 {
		t.Fatalf("exceeded budget: %d > %d", n, sumToks+10)
	}
	if !strings.Contains(out, ":summary") {
		t.Fatal("over-budget block must stay summarized instead of being inflated")
	}
	if strings.Contains(out, "body with padding") {
		t.Fatal("full file content must not be inflated when it exceeds the budget")
	}
}

func TestExpandModeBudgetMixedExpansion(t *testing.T) {
	root := t.TempDir()
	big := "package big\n\n" + strings.Repeat("func Fn(x int) string {\n\treturn \"body with padding padding padding padding padding\" + x\n}\n\n", 40)
	small := "package small\n\nfunc Tiny() int { return 1 }\n"
	_ = os.WriteFile(filepath.Join(root, "big.go"), []byte(big), 0o644)
	_ = os.WriteFile(filepath.Join(root, "small.go"), []byte(small), 0o644)
	doc := "```go:big.go\n" + big + "```\n\nsome prose in between\n\n```go:small.go\n" + small + "```\n"
	summed := SummaryMode(doc, root)
	budget := tokenize.CountKind(summed, tokenize.KindGeneric) + tokenize.CountKind(small, tokenize.KindGeneric) + 5
	out, fits := ExpandModeBudget(summed, root, budget)
	if !fits {
		t.Fatal("expected fits=true")
	}
	if n := tokenize.CountKind(out, tokenize.KindGeneric); n > budget {
		t.Fatalf("exceeded budget: %d > %d", n, budget)
	}
	if !strings.Contains(out, "func Tiny() int { return 1 }") {
		t.Fatal("small file should be expanded within the budget")
	}
	if strings.Contains(out, "body with padding") {
		t.Fatal("big file must stay summarized when its full content exceeds the budget")
	}
	if !strings.Contains(out, ":summary") {
		t.Fatal("big file summary marker must remain")
	}
}

func TestExpandModeBudgetProseOverBudgetFitsFalse(t *testing.T) {
	root, doc := writeFixture(t)
	summed := SummaryMode(doc, root)
	// Prose alone exceeds the budget; expansion cannot fix that — the output
	// must still respect the ceiling and report fits=false.
	huge := strings.Repeat("noise line that keeps going and going and going and going\n", 500)
	text := huge + summed
	out, fits := ExpandModeBudget(text, root, 100)
	if fits {
		t.Fatal("prose alone over budget must report fits=false")
	}
	if n := tokenize.CountKind(out, tokenize.KindGeneric); n > 100 {
		t.Fatalf("last-resort fit exceeded budget: %d > 100", n)
	}
}

func TestFitBlocksCapsProseWithMarker(t *testing.T) {
	root, doc := writeFixture(t)
	// The code block comes first (claims its summary allowance), then a huge
	// prose block that must be capped with an explicit marker so the total
	// respects the budget.
	huge := strings.Repeat("verbose prose line that is not code and just keeps going with padding and more padding\n", 300)
	text := doc + huge
	budget := tokenize.CountKind(doc, tokenize.KindGeneric) + 30
	out, fits, swapped := FitBlocks(text, root, budget)
	if !fits {
		t.Fatal("prose capping should bring the document under budget")
	}
	if n := tokenize.CountKind(out, tokenize.KindGeneric); n > budget {
		t.Fatalf("exceeded budget: %d > %d", n, budget)
	}
	if !strings.Contains(out, proseTrimMarker) {
		t.Fatalf("expected explicit prose trim marker in output:\n%.200s", out)
	}
	if len(swapped) != 1 || swapped[0] != "a.go" {
		t.Fatalf("expected swapped=[a.go], got %v", swapped)
	}
}

func TestFitBlocksProseOnlyCapped(t *testing.T) {
	// A document with no fenced blocks: a single huge prose block must be
	// capped with the marker instead of blowing the budget.
	huge := strings.Repeat("plain prose line with words and more words that fill the budget quickly\n", 400)
	out, fits, swapped := FitBlocks(huge, "", 100)
	if !fits {
		t.Fatal("prose capping should fit a prose-only document")
	}
	if len(swapped) != 0 {
		t.Fatalf("expected no swaps, got %v", swapped)
	}
	if n := tokenize.CountKind(out, tokenize.KindGeneric); n > 100 {
		t.Fatalf("exceeded budget: %d > 100", n)
	}
	if !strings.Contains(out, proseTrimMarker) {
		t.Fatalf("expected prose trim marker:\n%.200s", out)
	}
}
