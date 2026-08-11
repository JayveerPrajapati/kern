package pack

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestBuildPacksEverythingInPathOrder(t *testing.T) {
	root := writeTree(t, map[string]string{
		"AGENTS.md":       "# rules\nfollow the style\n",
		"README.md":       "# demo\n",
		"cmd/main.go":     "package main\nfunc main() {}\n",
		"internal/a/a.go": "package a\nvar A = 1\n",
		"internal/b/b.go": "package b\nvar B = 2\n",
	})
	b, err := Build(root, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Instructions) != 2 {
		t.Fatalf("expected 2 instruction files, got %d (%v)", len(b.Instructions), b.Instructions)
	}
	if len(b.Files) != 3 {
		t.Fatalf("expected 3 source files, got %d", len(b.Files))
	}
	// Path order.
	got := []string{b.Files[0].Path, b.Files[1].Path, b.Files[2].Path}
	want := []string{"cmd/main.go", "internal/a/a.go", "internal/b/b.go"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("file order = %v, want %v", got, want)
		}
	}
	// Content actually packed.
	if !strings.Contains(b.Files[0].Content, "package main") {
		t.Fatalf("content not packed: %q", b.Files[0].Content)
	}
	if b.Files[0].Tokens == 0 {
		t.Fatalf("expected token count > 0")
	}
	out := b.Render()
	for _, marker := range []string{"INSTRUCTIONS", "REPOSITORY STRUCTURE", "REPOSITORY FILES", "STATS", "## File: cmd/main.go"} {
		if !strings.Contains(out, marker) {
			t.Fatalf("render missing %q", marker)
		}
	}
}

func TestBuildSkipsIgnoredAndBinary(t *testing.T) {
	root := writeTree(t, map[string]string{
		"main.go":        "package main\n",
		"node_modules/x": "junk\n",
		".git/config":    "x\n",
		"go.sum":         "hash\n",
		"blob.bin":       "A\x00B\x00C",
	})
	b, err := Build(root, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files) != 1 || b.Files[0].Path != "main.go" {
		t.Fatalf("expected only main.go, got %+v", b.Files)
	}
	if b.Ignored != 2 {
		t.Fatalf("expected 2 ignored (go.sum, blob.bin); skipped dirs don't count, got %d", b.Ignored)
	}
}

func TestBuildHonorsGitignoreAndKernignore(t *testing.T) {
	root := writeTree(t, map[string]string{
		"main.go":          "package main\n",
		"keep.go":          "package keep\n",
		"out/tmp.log":      "noise\n",
		"generated/out.go": "package gen\n",
	})
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\nout/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kernignore"), []byte("generated/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Build(root, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range b.Files {
		paths = append(paths, f.Path)
	}
	if len(paths) != 2 || paths[0] != "keep.go" || paths[1] != "main.go" {
		t.Fatalf("expected only keep.go and main.go, got %v", paths)
	}
}

func TestKernignoreNegationOverridesGitignore(t *testing.T) {
	root := writeTree(t, map[string]string{
		"main.go":  "package main\n",
		"app.log":  "noise\n",
		"keep.log": "kept\n",
	})
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kernignore"), []byte("!keep.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Build(root, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range b.Files {
		paths = append(paths, f.Path)
	}
	if len(paths) != 2 {
		t.Fatalf("expected main.go and keep.log (negation), got %v", paths)
	}
}

func TestNestedGitignoreHonoredByPack(t *testing.T) {
	root := writeTree(t, map[string]string{
		"main.go":          "package main\n",
		"sub/keep.go":      "package keep\n",
		"sub/build/out.go": "package gen\n",
		"sub/trace.log":    "noise\n",
	})
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", ".gitignore"), []byte("build/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Build(root, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range b.Files {
		paths = append(paths, f.Path)
	}
	want := []string{"main.go", "sub/keep.go"}
	if len(paths) != len(want) {
		t.Fatalf("expected %v, got %v", want, paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, paths)
		}
	}
}

func TestSecurityFindingsSurfaceInBundle(t *testing.T) {
	root := writeTree(t, map[string]string{
		"main.go": "package main\n\nconst awsKey = \"AKIAIOSFODNN7EXAMPLE\"\n",
		"ok.go":   "package ok\n",
	})
	b, err := Build(root, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Security) == 0 {
		t.Fatalf("expected a hardcoded-secret finding, got none (bundle: %+v)", b.Security)
	}
	report := b.Render()
	if !strings.Contains(report, "SECURITY") || !strings.Contains(report, "hardcoded secret") {
		t.Fatalf("render missing security section:\n%s", report)
	}
	if !strings.Contains(report, "main.go:3") {
		t.Fatalf("finding must be line-scoped:\n%s", report)
	}
}

func TestBudgetSkipsOversizedFiles(t *testing.T) {
	big := strings.Repeat("x", 200) // ~200 tokens
	root := writeTree(t, map[string]string{
		"a.go": "package a\n" + big + "\n",
		"b.go": "package b\nsmall\n",
		"c.go": "package c\n",
	})
	b, err := Build(root, Options{Root: root, MaxTokens: 30})
	if err != nil {
		t.Fatal(err)
	}
	if !b.Truncated {
		t.Fatalf("expected Truncated with a 30-token budget")
	}
	if b.Dropped == 0 {
		t.Fatalf("expected dropped files, got %d", b.Dropped)
	}
	for _, f := range b.Files {
		if strings.HasPrefix(f.Path, "a") {
			t.Fatalf("oversized a.go should have been dropped, got %+v", b.Files)
		}
	}
	// Budget respected in the render too.
	if tokenCount := countTokens(b); tokenCount > 200 {
		t.Fatalf("pack too big: %d tokens", tokenCount)
	}
	if !strings.Contains(b.Render(), "Dropped to fit budget") {
		t.Fatalf("render missing dropped note")
	}
}

func countTokens(b *Bundle) int {
	n := 0
	for _, f := range b.Files {
		n += f.Tokens
	}
	return n
}

func TestSkipInstructions(t *testing.T) {
	root := writeTree(t, map[string]string{
		"AGENTS.md":      "# rules\n",
		"main.go":        "package main\n",
		"docs/AGENTS.md": "# docs rules\n",
	})
	b, err := Build(root, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Instructions) != 2 {
		t.Fatalf("expected AGENTS.md + docs/AGENTS.md, got %d (%v)", len(b.Instructions), b.Instructions)
	}
	b2, err := Build(root, Options{Root: root, SkipInstructions: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(b2.Instructions) != 0 {
		t.Fatalf("SkipInstructions should drop docs, got %d", len(b2.Instructions))
	}
}

func TestTreeAndJSON(t *testing.T) {
	root := writeTree(t, map[string]string{
		"cmd/main.go":     "package main\n",
		"internal/a/a.go": "package a\n",
	})
	b, err := Build(root, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	tree := b.tree()
	for _, frag := range []string{"cmd/", "internal/", "a/", "main.go", "a.go", "tokens"} {
		if !strings.Contains(tree, frag) {
			t.Fatalf("tree missing %q:\n%s", frag, tree)
		}
	}
	// A real tree nests basenames by depth; the full path must not be echoed.
	if strings.Contains(tree, "internal/a/a.go") {
		t.Fatalf("tree must render basenames, not full paths:\n%s", tree)
	}
	if !strings.Contains(tree, "├── cmd/\n  ├── main.go") && !strings.Contains(tree, "├── cmd/\n    ├── main.go") {
		t.Fatalf("tree must nest main.go under cmd/:\n%s", tree)
	}
	js, err := b.JSON()
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{`"root"`, `"files"`, `"content"`, `"tokens"`} {
		if !strings.Contains(js, frag) {
			t.Fatalf("JSON missing %q", frag)
		}
	}
}
