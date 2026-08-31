package pack

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/code"
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

func TestBuildPacksEverythingDeterministic(t *testing.T) {
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
	// Files are ordered by sha256(relative path) — a KV-cache-friendly
	// deterministic order, not lexical.
	got := []string{b.Files[0].Path, b.Files[1].Path, b.Files[2].Path}
	want := []string{"cmd/main.go", "internal/a/a.go", "internal/b/b.go"}
	sort.Slice(want, func(i, j int) bool { return pathHash(want[i]) < pathHash(want[j]) })
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("file order = %v, want sha256-path order %v", got, want)
		}
	}
	// Content actually packed (locate by path — hash order is not lexical).
	var mainGo *File
	for i := range b.Files {
		if b.Files[i].Path == "cmd/main.go" {
			mainGo = &b.Files[i]
		}
	}
	if mainGo == nil || !strings.Contains(mainGo.Content, "package main") {
		t.Fatalf("cmd/main.go content not packed: %+v", b.Files)
	}
	if mainGo.Tokens == 0 {
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
	sort.Strings(paths)
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
	sort.Strings(paths)
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

// TestPackDeterministicOrder packs the same file set twice and asserts the
// rendered bundles are byte-identical: same order, same content — the property
// LLM prompt caches rely on.
func TestPackDeterministicOrder(t *testing.T) {
	root := writeTree(t, map[string]string{
		"zebra.go":      "package z\nfunc Z() {}\n",
		"alpha.go":      "package a\nfunc A() {}\n",
		"internal/m.go": "package m\nvar M = 1\n",
		"beta/b.go":     "package b\nvar B = 2\n",
	})
	b1, err := Build(root, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	b2, err := Build(root, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(b1.Render()), []byte(b2.Render())) {
		t.Fatalf("packs of the same file set must be byte-identical:\n%s\n---\n%s", b1.Render(), b2.Render())
	}
	for i := range b1.Files {
		if b1.Files[i].Path != b2.Files[i].Path || b1.Files[i].Content != b2.Files[i].Content {
			t.Fatalf("file %d differs between packs: %+v vs %+v", i, b1.Files[i], b2.Files[i])
		}
	}
}

// TestPackOrderIsPathHashStable asserts the packed order equals a stable sort
// of the file set by sha256(relative path), computed independently of the pack.
func TestPackOrderIsPathHashStable(t *testing.T) {
	files := map[string]string{
		"a.go":     "package a\n",
		"b.go":     "package b\n",
		"c/d.go":   "package d\n",
		"zebra.go": "package z\n",
		"main.go":  "package main\n",
		"x/y/z.go": "package zz\n",
	}
	root := writeTree(t, files)
	b, err := Build(root, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	// Expected order: the file set sorted by sha256 of the relative path.
	var want []string
	for rel := range files {
		want = append(want, rel)
	}
	sort.Slice(want, func(i, j int) bool { return pathHash(want[i]) < pathHash(want[j]) })
	var got []string
	for _, f := range b.Files {
		got = append(got, f.Path)
	}
	if len(got) != len(want) {
		t.Fatalf("packed %d files, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pack order = %v, want sha256-path order %v", got, want)
		}
	}
	// sha256(path) is stable: same path always hashes the same way.
	if pathHash("a.go") != pathHash("a.go") {
		t.Fatalf("pathHash must be deterministic")
	}
}

// TestPackWithFold packs a tree with Tier=folded (the --fold flag) and asserts
// Go and non-Go files carry signatures with bodies elided by line-counted
// placeholders.
func TestPackWithFold(t *testing.T) {
	root := writeTree(t, map[string]string{
		"main.go": "package main\n\nfunc Greet(name string) string {\n\treturn \"hi \" + name\n}\n",
		"util.py": "def helper():\n    return 1\n",
	})
	b, err := Build(root, Options{Root: root, Tier: code.TierFolded})
	if err != nil {
		t.Fatal(err)
	}
	var sawGo, sawPy bool
	for _, f := range b.Files {
		switch f.Path {
		case "main.go":
			sawGo = true
			if !strings.Contains(f.Content, "func Greet(name string) string {") {
				t.Fatalf("folded Go file must keep the signature:\n%s", f.Content)
			}
			if strings.Contains(f.Content, `return "hi " + name`) {
				t.Fatalf("folded Go file must elide the body:\n%s", f.Content)
			}
			if !strings.Contains(f.Content, "// ... body elided: 1 lines ...") {
				t.Fatalf("folded Go file must carry an elided-lines placeholder:\n%s", f.Content)
			}
		case "util.py":
			sawPy = true
			if strings.Contains(f.Content, "return 1") {
				t.Fatalf("folded python file must elide the body:\n%s", f.Content)
			}
			if !strings.Contains(f.Content, "# ... body elided: 1 lines ...") {
				t.Fatalf("folded python file must carry an elided-lines placeholder:\n%s", f.Content)
			}
		}
	}
	if !sawGo || !sawPy {
		t.Fatalf("expected main.go and util.py in the pack, got %v", b.Files)
	}
	// Tier=full is the default and packs the original source unchanged.
	bFull, err := Build(root, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range bFull.Files {
		if f.Path == "main.go" && !strings.Contains(f.Content, `return "hi " + name`) {
			t.Fatalf("tier=full must pack the original source:\n%s", f.Content)
		}
	}
}
