package research

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

func TestResearchCombinesTaskSymbolsAndImpact(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"guardrule/guardrule.go": `package guardrule

func ClassifyCommand(command string) bool {
	return ClassifyCommandInRoot(command, ".")
}

func ClassifyCommandInRoot(command, root string) bool {
	return command != ""
}
`,
		"verifycmd/verify.go": `package verifycmd

import "guardrule"

func VerifyCommand(command string) bool {
	return guardrule.ClassifyCommand(command)
}

func ReusePath(command string) bool {
	return VerifyCommand(command)
}
`,
		"guardrule/guardrule_test.go": `package guardrule

import "testing"

func TestClassifyCommand(t *testing.T) {
	if !ClassifyCommand("go test ./...") {
		t.Fatal("expected command to pass")
	}
}
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}

	report := Research(ix, "Understand the guard command classifier and verify reuse path", 6000)
	out := RenderResearch(report)
	for _, needle := range []string{
		"ClassifyCommand",
		"VerifyCommand",
		"guardrule/guardrule.go",
		"verifycmd/verify.go",
		"TestClassifyCommand",
		"evidence:",
		"missing:",
	} {
		if !strings.Contains(out, needle) {
			t.Fatalf("research output missing %q:\n%s", needle, out)
		}
	}
}

func TestResearchBudgetIsBounded(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"guardrule/guardrule.go": `package guardrule

func ClassifyCommand(command string) bool { return command != "" }
`,
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}

	report := Research(ix, "Understand the guard command classifier", 25)
	out := FitResearch(RenderResearch(report), 25)
	if got := tokenize.Count(out); got > 25 {
		t.Fatalf("fitted research output = %d tokens, want <= 25:\n%s", got, out)
	}
	if !report.Truncated {
		t.Fatal("expected research report to be marked truncated")
	}
}

func TestResearchShowsSameNameAlternatives(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a/a.go": "package a\n\nfunc Handle() {}\n",
		"b/b.go": "package b\n\nfunc Handle() {}\n",
	})
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	report := Research(ix, "Understand Handle", 6000)
	out := RenderResearch(report)
	if !strings.Contains(out, "alternatives (1): Handle — b/b.go:3") && !strings.Contains(out, "alternatives (1): Handle — a/a.go:3") {
		t.Fatalf("ambiguous simple name must show the other definition:\n%s", out)
	}
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
