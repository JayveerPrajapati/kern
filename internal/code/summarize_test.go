package code

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSummarizeGo(t *testing.T) {
	src := `package main

// Foo does things.
func Foo(a int, b string) error {
	return nil
}

type Bar struct {
	X int
}

const (
	Max = 10
)
`
	sum := Summarize("main.go", []byte(src), 100)
	if sum.Language != "go" {
		t.Fatalf("expected go, got %s", sum.Language)
	}
	found := false
	for _, s := range sum.Symbols {
		if s.Kind == "func" && s.Name == "Foo" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected func Foo in symbols, got %+v", sum.Symbols)
	}
}

func TestSummarizeUnknownLang(t *testing.T) {
	sum := Summarize("notes.txt", []byte("hello"), 100)
	if sum.Language != "" || len(sum.Symbols) != 0 {
		t.Fatalf("expected empty summary for unknown lang")
	}
}

func TestProjectMapSkipsIgnored(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package a\nfunc A() {}\n")
	write("sub/b.go", "package b\nfunc B() {}\n")
	write("node_modules/x.js", "const x = 1\n")
	write("vendor/y.go", "package y\nfunc Y() {}\n")

	p, err := BuildProject(dir, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 2 {
		t.Fatalf("expected 2 files, got %d (%+v)", len(p.Files), p.Files)
	}
}

func TestReadFileSizeGuard(t *testing.T) {
	dir := t.TempDir()
	normal := filepath.Join(dir, "normal.go")
	if err := os.WriteFile(normal, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content, err := ReadFile(normal)
	if err != nil || len(content) == 0 {
		t.Fatalf("expected successful read, got %v", err)
	}

	huge := filepath.Join(dir, "huge.bin")
	f, err := os.Create(huge)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxReadFileBytes + 1024); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	_, err = ReadFile(huge)
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("expected size error for huge file, got %v", err)
	}
}
