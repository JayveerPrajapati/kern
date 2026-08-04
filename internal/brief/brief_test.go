package brief

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBuildIncludesSections(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "main.go", `package main

func helper() int { return 1 }

func main() {
	_ = helper()
}
`)
	out, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# kern buddy briefing",
		"## Project map",
		"## Index",
		"Languages: go",
		"main.go",
		"## kern savings",
		"## How to use kern",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("briefing missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "{{") {
		t.Fatalf("unfilled placeholders:\n%s", out)
	}
}

func TestBuildEntryPoints(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "app.go", `package main

func init() {}

func Run() {}
`)
	out, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Entry points:") || !strings.Contains(out, "Run") {
		t.Fatalf("entry points missing:\n%s", out)
	}
}

func TestBuildNoGo(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write(t, dir, "notes.txt", "just notes\n")
	out, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "# kern buddy briefing") {
		t.Fatalf("briefing header missing:\n%s", out)
	}
}
