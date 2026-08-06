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
