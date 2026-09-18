package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestInheritsResolution guards the inherits resolution chain (cmd_graph.go
// runInherits): a bare ambiguous name must prefer the type symbol over a
// same-named method, and a package-qualified name ("index.Index") must
// resolve through the package-directory fallback instead of failing.
func TestInheritsResolution(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"index", "app"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module demo\n\ngo 1.22\n")
	write("index/engine.go", `package index

// Index is a struct.
type Index struct {
	Name string
}
`)
	write("app/platform.go", `package app

// Index is a method on Platform.
type Platform struct{}

func (p *Platform) Index() int { return 0 }
`)
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}

	// The qualified form must resolve to the struct in package index
	// (package-directory fallback), not fail.
	r, ok := ix.ResolveName("index.Index")
	if !ok {
		t.Fatal("ResolveName(index.Index) failed; package-directory fallback missing")
	}
	if r.File != "index/engine.go" || r.Kind != "struct" {
		t.Fatalf("ResolveName(index.Index) = %s (%s) in %s, want struct in index/engine.go",
			r.FullName(), r.Kind, r.File)
	}

	// The bare ambiguous name must prefer the type over the method.
	types := ix.Search("type Index", 1)
	if len(types) == 0 || types[0].Kind != "struct" {
		t.Fatalf("Search(type Index) = %+v, want the struct", types)
	}
}

// TestTwinRejectsFileRoot guards runTwin's root validation: a file path
// must fail fast with a usage error (exitError code 2) instead of building
// a graph on a bogus root and emitting governance-store "not a directory"
// noise.
func TestTwinRejectsFileRoot(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("runTwin(file) did not fail; expected a usage exitError")
		}
		ee, ok := r.(exitError)
		if !ok || ee.code != 2 {
			t.Fatalf("runTwin(file) panic = %v, want exitError code 2", r)
		}
	}()
	runTwin([]string{file})
}
