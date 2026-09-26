package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenameApplyEditsDisk is the QA 2026-09-23 P0 regression: runRename used
// to set rep.Applied = true before calling rename.Apply (to suppress the
// preview hint in Render), but Apply treats r.Applied as already-committed and
// returned (0, nil) — every `kern rename --apply` was a silent no-op with a
// success message and no backup. The fix orders gate → Apply → render.
// Before the fix this test fails at "--apply did not edit disk".
func TestRenameApplyEditsDisk(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod": "module renameqa\n\ngo 1.23\n",
		"a/a.go": "package a\n\n// Foo comment mentions Foo.\nfunc Foo() string { return \"Foo literal stays\" }\n\nfunc bar() string { return Foo() }\n",
		"b/b.go": "package b\n\nimport \"renameqa/a\"\n\nfunc UseFoo() string { return a.Foo() }\n",
	}
	for rel, src := range files {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	readA := func() string {
		b, err := os.ReadFile(filepath.Join(root, "a", "a.go"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// Preview (no --apply): report only — disk untouched.
	runRename([]string{"Foo", "FooNew", root})
	a := readA()
	if !strings.Contains(a, "func Foo()") || strings.Contains(a, "FooNew") {
		t.Fatalf("preview mutated disk:\n%s", a)
	}
	backups, _ := filepath.Glob(filepath.Join(root, ".kern", "rename-backup", "*"))
	if len(backups) != 0 {
		t.Fatalf("preview created a backup: %v", backups)
	}

	// Apply (--apply): definition + references renamed on disk, strings and
	// comments untouched, backup holds the pre-rename content.
	runRename([]string{"Foo", "FooNew", root, "--apply"})
	a = readA()
	if !strings.Contains(a, "func FooNew()") {
		t.Fatalf("--apply did not edit disk:\n%s", a)
	}
	if !strings.Contains(a, "return FooNew()") {
		t.Fatalf("--apply missed the in-package reference:\n%s", a)
	}
	if !strings.Contains(a, `"Foo literal stays"`) {
		t.Fatalf("--apply touched a string literal:\n%s", a)
	}
	if !strings.Contains(a, "// Foo comment mentions Foo.") {
		t.Fatalf("--apply touched a comment:\n%s", a)
	}
	bB, err := os.ReadFile(filepath.Join(root, "b", "b.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bB), "a.FooNew()") {
		t.Fatalf("--apply missed the cross-package reference:\n%s", string(bB))
	}
	backups, _ = filepath.Glob(filepath.Join(root, ".kern", "rename-backup", "*", "a", "a.go"))
	if len(backups) == 0 {
		t.Fatalf("--apply created no backup")
	}
	orig, err := os.ReadFile(backups[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(orig), "func Foo()") {
		t.Fatalf("backup does not hold pre-rename content:\n%s", string(orig))
	}
}

// TestRenameApplyJSONStillApplies pins the second half of the P0: the old
// handler returned early on --json BEFORE the apply branch, so
// `kern rename a b --apply --json` printed JSON and never applied.
func TestRenameApplyJSONStillApplies(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module renameqa\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "a.go"), []byte("package a\n\nfunc Solo() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runRename([]string{"Solo", "SoloRenamed", root, "--apply", "--json"})
	b, err := os.ReadFile(filepath.Join(root, "a", "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "func SoloRenamed()") {
		t.Fatalf("--apply --json did not edit disk:\n%s", string(b))
	}
}
