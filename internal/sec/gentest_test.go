package sec

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// chdirTemp changes the process working directory to a fresh temp dir and
// returns a cleanup that restores the original. Used so GenTestScaffold's
// package-clause read (root/<t.File> resolved against the working directory)
// sees fixture files.
func chdirTemp(t *testing.T) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
}

func TestGenTestScaffoldSQLInjection(t *testing.T) {
	chdirTemp(t)
	writeTaintFile(t, ".", "svc/handlers.go", "package svc\n\nfunc lookup() {}\n")

	tf := TaintFinding{
		Finding: Finding{
			File: "svc/handlers.go", Line: 12, Rule: "sql-injection",
			Severity: "error", Message: "dynamic SQL built from variables",
		},
		Func: "lookup", Tainted: true, EntryPoint: "H", Path: []string{"H", "lookup"},
	}
	sc := GenTestScaffold(tf)
	if sc.Pkg != "svc" {
		t.Errorf("Pkg = %q, want svc", sc.Pkg)
	}
	if sc.File != "svc/handlers_taint_test.go" {
		t.Errorf("File = %q, want svc/handlers_taint_test.go", sc.File)
	}
	for _, want := range []string{
		"package svc",
		`import "testing"`,
		"func TestTaintSQLInjection12(t *testing.T)",
		"craft a malicious SQL input and assert the query path parameterizes it",
		"TODO",
		"svc/handlers.go:12",
		"tainted via H",
	} {
		if !strings.Contains(sc.Code, want) {
			t.Errorf("scaffold missing %q:\n%s", want, sc.Code)
		}
	}
}

func TestGenTestScaffoldFallbackPkg(t *testing.T) {
	chdirTemp(t)
	// The sink file exists but has no package clause -> fallback "main".
	writeTaintFile(t, ".", "sink.txt", "no package clause here\n")

	tf := TaintFinding{
		Finding: Finding{
			File: "sink.txt", Line: 1, Rule: "command-injection",
			Severity: "error", Message: "shell command built from variables",
		},
		Func: "run", Tainted: true,
	}
	sc := GenTestScaffold(tf)
	if sc.Pkg != "main" {
		t.Errorf("Pkg = %q, want main", sc.Pkg)
	}
	if !strings.Contains(sc.Code, "package main") {
		t.Errorf("scaffold missing fallback package clause:\n%s", sc.Code)
	}
	if !strings.Contains(sc.Code, "func TestTaintCommandInjection1(t *testing.T)") {
		t.Errorf("scaffold missing CommandInjection test:\n%s", sc.Code)
	}
	if !strings.Contains(sc.Code, "assert shell input is validated before exec") {
		t.Errorf("scaffold missing command-injection probe comment:\n%s", sc.Code)
	}
	// No entry point -> "via source" in the header comment.
	if !strings.Contains(sc.Code, "tainted via source") {
		t.Errorf("scaffold missing 'via source' header:\n%s", sc.Code)
	}
}

func TestGenTestScaffoldDeterministic(t *testing.T) {
	chdirTemp(t)
	writeTaintFile(t, ".", "app.go", "package main\n\nfunc sink() {}\n")
	tf := TaintFinding{
		Finding: Finding{
			File: "app.go", Line: 7, Rule: "unsafe-deserialization",
			Severity: "warning", Message: "untrusted input deserialized into untyped/weak types",
		},
		Func: "sink", Tainted: true, EntryPoint: "H", Path: []string{"H", "sink"},
	}
	a := GenTestScaffold(tf)
	b := GenTestScaffold(tf)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("GenTestScaffold not deterministic:\na=%+v\nb=%+v", a, b)
	}
	if !strings.Contains(a.Code, "func TestTaintUnsafeDeserialization7(t *testing.T)") {
		t.Errorf("scaffold missing UnsafeDeserialization test:\n%s", a.Code)
	}
	if !strings.Contains(a.Code, "assert untrusted payloads are rejected") {
		t.Errorf("scaffold missing deserialization probe comment:\n%s", a.Code)
	}
}
