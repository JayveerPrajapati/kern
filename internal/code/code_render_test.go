package code

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectLanguageAll(t *testing.T) {
	cases := map[string]string{
		"foo.py":     "python",
		"bar.py":     "python",
		"x.js":       "js",
		"y.ts":       "js",
		"z.jsx":      "js",
		"w.tsx":      "js",
		"a.mjs":      "js",
		"b.cjs":      "js",
		"c.java":     "java",
		"d.c":        "c",
		"e.h":        "c",
		"f.cc":       "c",
		"g.hpp":      "c",
		"r.rs":       "rust",
		"r1.rb":      "ruby",
		"s.sh":       "shell",
		"s1.bash":    "shell",
		"main.go":    "go",
		"noext":      "",
		"notes.md":   "",
	}
	for path, want := range cases {
		if got := DetectLanguage(path); got != want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestFilepathExt(t *testing.T) {
	if filepathExt("foo.go") != ".go" {
		t.Fatal("expected .go")
	}
	if filepathExt("noext") != "" {
		t.Fatal("expected empty ext")
	}
}

func TestSummaryRender(t *testing.T) {
	src := "package main\n\n// Foo does a thing.\nfunc Foo(a int) error { return nil }\n"
	sum := Summarize("main.go", []byte(src), 100)
	out := sum.Render()
	if !strings.Contains(out, "main.go [go") {
		t.Fatalf("expected header, got %q", out)
	}
	if !strings.Contains(out, "func Foo") {
		t.Fatalf("expected func Foo in render, got %q", out)
	}
	if !strings.Contains(out, "(a int)") {
		t.Fatalf("expected params in render, got %q", out)
	}
}

func TestSummaryRenderUnknownLang(t *testing.T) {
	sum := Summary{Path: "notes.txt", Language: ""}
	if sum.Render() != "" {
		t.Fatalf("expected empty render for unknown lang, got %q", sum.Render())
	}
}

func TestItola(t *testing.T) {
	// Exercise the package-local itoa on several values via Render (lines/symbols).
	sum := Summary{Lines: 5, Symbols: []Symbol{{Line: 3}}, Language: "go", Path: "x.go"}
	r := sum.Render()
	if !strings.Contains(r, "5 lines") || !strings.Contains(r, "1 symbols") || !strings.Contains(r, " :3") {
		t.Fatalf("itoa/render output unexpected: %q", r)
	}
}

func TestReadAllLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(p, []byte("a\nb\nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, err := ReadAllLines(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 3 || lines[0] != "a" || lines[2] != "c" {
		t.Fatalf("unexpected lines: %v", lines)
	}
}

func TestReadAllLinesMissingFile(t *testing.T) {
	if _, err := ReadAllLines("/nonexistent/path/file.txt"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestSummarizeCachedHit(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	p := filepath.Join(root, "a.go")
	if err := os.WriteFile(p, []byte("package main\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// First call: cache miss.
	sum1, hit := summarizeCached(p, "a.go", 100)
	if hit {
		t.Fatal("first call must be a cache miss")
	}
	if len(sum1.Symbols) == 0 {
		t.Fatal("first call should still summarize")
	}
	// Second call: cache hit.
	sum2, hit := summarizeCached(p, "a.go", 100)
	if !hit {
		t.Fatal("second call should hit cache")
	}
	if sum2.Path != "a.go" {
		t.Fatalf("cached path remapped: %q", sum2.Path)
	}
	if len(sum2.Symbols) != len(sum1.Symbols) {
		t.Fatalf("cached symbol count mismatch: %d vs %d", len(sum2.Symbols), len(sum1.Symbols))
	}
}

func TestSummarizeCachedReadError(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// summarizeCached on a missing file returns a path-only summary, no hit.
	sum, hit := summarizeCached("/nonexistent/file.go", "missing.go", 100)
	if hit {
		t.Fatal("expected no hit on read error")
	}
	if sum.Path != "missing.go" {
		t.Fatalf("expected rel path preserved: %q", sum.Path)
	}
}

func TestShouldIgnore(t *testing.T) {
	cases := map[string]bool{
		"go.sum":              true,
		".DS_Store":           true,
		"package-lock.json":   true,
		".env":                true,
		"main.go":             false,
		"vendor/x.go":         true,
		"node_modules/x.js":   true,
		"src/app.py":          false,
		"build/output.o":      true,
	}
	for rel, want := range cases {
		if got := shouldIgnore(rel); got != want {
			t.Errorf("shouldIgnore(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestProjectRender(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module d\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\nfunc A() {}\nfunc B() {}\n"), 0o644)

	p, err := BuildProject(dir, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := p.Render()
	if !containsStr(out, "Project:") || !containsStr(out, "files") {
		t.Fatalf("expected project header, got %q", out)
	}
	if !containsStr(out, "a.go") {
		t.Fatalf("expected a.go in render, got %q", out)
	}
}

func TestProjectRenderWithCacheHit(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\nfunc A() {}\n"), 0o644)

	// Prime the cache by building once.
	if _, err := BuildProject(dir, 100, 100); err != nil {
		t.Fatal(err)
	}
	p, err := BuildProject(dir, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if p.CacheHit == 0 {
		t.Fatal("expected cache hit on second build")
	}
	if !containsStr(p.Render(), "from cache") {
		t.Fatalf("expected cache note in render: %q", p.Render())
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (strings.Contains(s, sub))
}
