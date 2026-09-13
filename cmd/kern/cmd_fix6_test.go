package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestGoNativeEntry pins F-009: Go's language-native entry points — main in
// package main, and init funcs — must be recognized as entry points so
// `kern entry-points` finds them on a plain Go module with no framework.
func TestGoNativeEntry(t *testing.T) {
	pkgOf := map[string]string{
		"cmd/app/main.go":   "main",
		"lib/lib.go":        "lib",
		"internal/svc/s.go": "svc",
	}
	cases := []struct {
		name string
		sym  index.Symbol
		want bool
	}{
		{"main in package main", index.Symbol{Kind: "func", Name: "main", File: "cmd/app/main.go"}, true},
		{"init anywhere", index.Symbol{Kind: "func", Name: "init", File: "lib/lib.go"}, true},
		{"main in a library package is not an entry point", index.Symbol{Kind: "func", Name: "main", File: "lib/lib.go"}, false},
		{"plain exported func is not native", index.Symbol{Kind: "func", Name: "FindUser", File: "internal/svc/s.go"}, false},
		{"non-func named main is not native", index.Symbol{Kind: "struct", Name: "main", File: "cmd/app/main.go"}, false},
		{"non-Go file is not native", index.Symbol{Kind: "func", Name: "main", File: "cmd/app/main.py"}, false},
	}
	for _, c := range cases {
		got, ok := goNativeEntry(c.sym, pkgOf)
		if ok != c.want {
			t.Errorf("%s: goNativeEntry(%+v) ok = %v, want %v", c.name, c.sym, ok, c.want)
			continue
		}
		if ok && got != "go" {
			t.Errorf("%s: framework label = %q, want \"go\"", c.name, got)
		}
	}
}

// TestRunEntryPointsFindsGoMain pins F-009 end to end: after indexing a plain
// Go module (no framework), `kern entry-points` must list the native main
// entry instead of reporting "no framework entry points".
func TestRunEntryPointsFindsGoMain(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := jsonCliFixture(t)
	out := captureStdout(t, func() { runEntryPoints([]string{dir}) })
	if !strings.Contains(out, "go main") || !strings.Contains(out, "main.go") {
		t.Fatalf("entry-points did not list the Go native main entry, got:\n%s", out)
	}
	if strings.Contains(out, "no framework entry points") {
		t.Fatalf("entry-points still reports no entries, got:\n%s", out)
	}
}

// TestRunFwReportsGoStdlib pins F-010: `kern frameworks` on a plain Go module
// must report Go itself ("Go (stdlib)") instead of "No known frameworks
// detected", while a non-Go project keeps the old message.
func TestRunFwReportsGoStdlib(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/plain\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { runFw([]string{dir}) })
	if !strings.Contains(out, "Go (stdlib)") || strings.Contains(out, "No known frameworks detected") {
		t.Fatalf("kern frameworks on a Go module must report Go (stdlib), got:\n%s", out)
	}

	// Non-Go project: unchanged behavior.
	py := t.TempDir()
	if err := os.WriteFile(filepath.Join(py, "app.py"), []byte("import flask\napp = flask.Flask(__name__)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = captureStdout(t, func() { runFw([]string{py}) })
	if !strings.Contains(out, "Flask") {
		t.Fatalf("kern frameworks on a Flask project must still detect Flask, got:\n%s", out)
	}
	if strings.Contains(out, "Go (stdlib)") {
		t.Fatalf("kern frameworks must not claim Go for a Python project, got:\n%s", out)
	}
}

// TestWithGoStdlib pins the synthesized "Go (stdlib)" entry rules: appended
// for go.mod/.go projects, absent for foreign-language projects.
func TestWithGoStdlib(t *testing.T) {
	gomod := t.TempDir()
	if err := os.WriteFile(filepath.Join(gomod, "go.mod"), []byte("module m\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	det := withGoStdlib(gomod, nil)
	if len(det) != 1 || det[0].ID != "go-stdlib" || det[0].Lang != "go" {
		t.Fatalf("withGoStdlib(go.mod dir) = %+v, want one go-stdlib entry", det)
	}
	// A gin-style module keeps both the framework and the baseline.
	gin := t.TempDir()
	if err := os.WriteFile(filepath.Join(gin, "go.mod"), []byte("module m\ngo 1.23\nrequire github.com/gin-gonic/gin v1.9.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	det = withGoStdlib(gin, nil)
	if len(det) != 1 {
		t.Fatalf("withGoStdlib without detected frameworks = %+v", det)
	}
	// .go files alone (no go.mod at root) still count as Go.
	gosrc := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gosrc, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gosrc, "lib", "x.go"), []byte("package lib\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if det := withGoStdlib(gosrc, nil); len(det) != 1 {
		t.Fatalf("withGoStdlib(.go-only dir) = %+v, want one go-stdlib entry", det)
	}
	// Empty dir: no Go entry, no framework.
	if det := withGoStdlib(t.TempDir(), nil); len(det) != 0 {
		t.Fatalf("withGoStdlib(empty dir) = %+v, want none", det)
	}
	// Existing go-stdlib entry is not duplicated.
	dup := withGoStdlib(gomod, det)
	if len(dup) != 1 {
		t.Fatalf("withGoStdlib must not duplicate go-stdlib, got %d entries", len(dup))
	}
}

// TestAnnotateImpactCallees pins F-014: the "What it calls" section of a
// rendered impact report must label each entry (direct) or (transitive) using
// the index's direct call edges, and leave unrelated text untouched.
func TestAnnotateImpactCallees(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module demo\n\ngo 1.23\n",
		"svc/svc.go": `package svc
import "demo/repo"
func FindUser(id int) string { return repo.Query(id) }
func legacyPrint() string { return "x" }
`,
		"repo/repo.go": `package repo
func Query(id int) string { return fmtInt(id) }
func fmtInt(i int) string { return itoa(i) }
func itoa(i int) string { return "" }
`,
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = ix.Save()
	text := `IMPACT for: FindUser
Risk: medium
What it calls: 3
  - Query
  - fmtInt
  - itoa
Tests that cover it: 0
`
	got := annotateImpactCallees(text, "FindUser", dir)
	for _, want := range []string{"- Query (direct)", "- fmtInt (transitive)", "- itoa (transitive)"} {
		if !strings.Contains(got, want) {
			t.Errorf("annotated text missing %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "IMPACT for: FindUser\nIMPACT for:") {
		t.Errorf("annotateImpactCallees must not duplicate the header, got:\n%s", got)
	}
	// Unrelated sections and unknown targets stay untouched.
	if !strings.Contains(got, "Tests that cover it: 0") {
		t.Errorf("unrelated section was altered:\n%s", got)
	}
	if got := annotateImpactCallees(text, "NoSuchSymbol", dir); got != text {
		t.Errorf("unknown target must return text unchanged")
	}
}

// TestSimpleSymName covers the qualified/bare name normalization used by the
// impact callee annotation.
func TestSimpleSymName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"repo.Query", "Query"},
		{"FindUser", "FindUser"},
		{"TaskService.Deploy", "Deploy"},
		{"", ""},
	}
	for _, c := range cases {
		if got := simpleSymName(c.in); got != c.want {
			t.Errorf("simpleSymName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
