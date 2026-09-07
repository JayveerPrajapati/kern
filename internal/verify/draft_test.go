package verify

import (
	"strings"
	"testing"
)

func TestCheckDraftCleanGo(t *testing.T) {
	ix, root := build(t, map[string]string{})
	code := `package main

func helper(n int) int { return n + 1 }

func main() {
	x := []int{1, 2, 3}
	y := append(x, 4)
	_ = len(y)
	_ = helper(len(y))
}
`
	findings := CheckDraft(ix, root, []byte(code), "")
	if len(findings) != 0 {
		t.Fatalf("expected clean draft, got %+v", findings)
	}
}

func TestCheckDraftUnknownSymbol(t *testing.T) {
	ix, root := build(t, map[string]string{})
	code := "package main\n\nfunc main() {\n\ttotallyMissingFunc()\n}\n"
	findings := CheckDraft(ix, root, []byte(code), "go")
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %+v", findings)
	}
	f := findings[0]
	if f.Kind != "unknown_symbol" {
		t.Errorf("expected unknown_symbol, got %q", f.Kind)
	}
	if f.Line != 4 {
		t.Errorf("expected line 4, got %d", f.Line)
	}
	if !strings.Contains(f.Message, "totallyMissingFunc") {
		t.Errorf("message should name the symbol: %q", f.Message)
	}
}

func TestCheckDraftUnknownRelativeImport(t *testing.T) {
	ix, root := build(t, map[string]string{})
	code := "package main\n\nimport (\n\t\"fmt\"\n\t\"./nonexistentpkg\"\n)\n\nfunc main() { fmt.Println(\"hi\") }\n"
	findings := CheckDraft(ix, root, []byte(code), "")
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %+v", findings)
	}
	f := findings[0]
	if f.Kind != "unknown_import" {
		t.Errorf("expected unknown_import, got %q", f.Kind)
	}
	if f.Line != 5 {
		t.Errorf("expected import line 5, got %d", f.Line)
	}
	if !strings.Contains(f.Message, "./nonexistentpkg") {
		t.Errorf("message should name the import: %q", f.Message)
	}
}

func TestCheckDraftParseError(t *testing.T) {
	ix, root := build(t, map[string]string{})
	code := "package main\n\nfunc main( {\n\tprintln(\"x\")\n}\n"
	findings := CheckDraft(ix, root, []byte(code), "")
	if len(findings) != 1 {
		t.Fatalf("expected exactly 1 parse_error finding, got %+v", findings)
	}
	f := findings[0]
	if f.Kind != "parse_error" {
		t.Errorf("expected parse_error, got %q", f.Kind)
	}
	if f.Line == 0 {
		t.Errorf("expected a nonzero position line, got %d", f.Line)
	}
}

func TestCheckDraftUnknownMethod(t *testing.T) {
	ix, root := build(t, map[string]string{
		"db/db.go": "package db\n\nfunc Do() {}\n",
	})
	bad := "package main\n\nimport \"./db\"\n\nfunc main() {\n\tdb.Nope()\n}\n"
	findings := CheckDraft(ix, root, []byte(bad), "")
	if len(findings) != 1 {
		t.Fatalf("expected 1 unknown_method finding, got %+v", findings)
	}
	f := findings[0]
	if f.Kind != "unknown_method" {
		t.Errorf("expected unknown_method, got %q", f.Kind)
	}
	if !strings.Contains(f.Message, "db.Nope") {
		t.Errorf("message should name db.Nope: %q", f.Message)
	}

	// The same import with an existing method validates cleanly.
	clean := "package main\n\nimport \"./db\"\n\nfunc main() {\n\tdb.Do()\n}\n"
	if f2 := CheckDraft(ix, root, []byte(clean), ""); len(f2) != 0 {
		t.Fatalf("expected clean draft for db.Do, got %+v", f2)
	}
}

func TestCheckDraftBuiltinsAndLocalsAllowed(t *testing.T) {
	ix, root := build(t, map[string]string{})
	code := `package main

func main() {
	m := make(map[string]int)
	m["a"] = 1
	x := []int{1, 2}
	_ = append(x, len(x))
	_ = cap(x)
	_ = m
}
`
	if findings := CheckDraft(ix, root, []byte(code), "go"); len(findings) != 0 {
		t.Fatalf("expected clean draft (builtins + locals), got %+v", findings)
	}
}

func TestCheckDraftNonGo(t *testing.T) {
	ix, root := build(t, map[string]string{})
	code := "import nonexistent\n\ndef f():\n    call_unknown_thing()\n"
	findings := CheckDraft(ix, root, []byte(code), "python")
	if len(findings) != 0 {
		t.Fatalf("expected no findings for non-Go language, got %+v", findings)
	}
}

func TestCheckDraftDeterministic(t *testing.T) {
	ix, root := build(t, map[string]string{})
	code := `package main

import (
	"bytes"
	"./nope"
)

func helper() {}

func main() {
	helper()
	missingOne()
	missingTwo()
}
`
	first := CheckDraft(ix, root, []byte(code), "go")
	second := CheckDraft(ix, root, []byte(code), "go")
	if len(first) != len(second) {
		t.Fatalf("finding count differs across runs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("finding %d differs across runs: %+v vs %+v", i, first[i], second[i])
		}
	}
}

func TestCheckDraftJavaUnresolvedCall(t *testing.T) {
	ix, root := build(t, map[string]string{
		"com/inn/rcp/RealClass.java": "package com.inn.rcp;\npublic class RealClass {\n    public static void real() {}\n}\n",
	})
	code := `package com.inn.rcp;

public class MyDraft {
    public void execute() {
        com.inn.rcp.does.not.Exist.doSomething();
    }
}
`
	findings := CheckDraft(ix, root, []byte(code), "java")
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for unresolvable Java call, got %+v", findings)
	}
	f := findings[0]
	if f.Kind != "unknown_symbol" {
		t.Errorf("expected unknown_symbol, got %q", f.Kind)
	}
	if f.Line != 5 {
		t.Errorf("expected line 5, got %d", f.Line)
	}
	if !strings.Contains(f.Message, "com.inn.rcp.does.not.Exist.doSomething") {
		t.Errorf("message should mention target: %q", f.Message)
	}

	// Also verify auto-detection when lang is empty string
	findingsAuto := CheckDraft(ix, root, []byte(code), "")
	if len(findingsAuto) != 1 {
		t.Fatalf("expected 1 finding with empty lang auto-detection, got %+v", findingsAuto)
	}
}

func TestCheckDraftJavaCleanCall(t *testing.T) {
	ix, root := build(t, map[string]string{
		"com/inn/rcp/RealService.java": "package com.inn.rcp;\npublic class RealService {\n    public static void serve() {}\n}\n",
	})
	code := `package com.inn.rcp;

import java.util.List;

public class MyDraft {
    private void localHelper() {}

    public void execute() {
        RealService.serve();
        localHelper();
        System.out.println("done");
    }
}
`
	findings := CheckDraft(ix, root, []byte(code), "java")
	if len(findings) != 0 {
		t.Fatalf("expected clean draft, got %+v", findings)
	}
}

func TestCheckDraftPythonSyntax(t *testing.T) {
	ix, root := build(t, map[string]string{})

	// Unclosed bracket
	badBracket := "def foo():\n    x = [1, 2, 3\n    return x\n"
	f1 := CheckDraft(ix, root, []byte(badBracket), "python")
	if len(f1) == 0 || f1[0].Kind != "parse_error" {
		t.Fatalf("expected parse_error for unclosed bracket, got %+v", f1)
	}

	// Missing colon
	badColon := "def foo()\n    return 42\n"
	f2 := CheckDraft(ix, root, []byte(badColon), "python")
	if len(f2) == 0 || f2[0].Kind != "parse_error" {
		t.Fatalf("expected parse_error for missing colon, got %+v", f2)
	}
}

func TestCheckDraftJSSyntax(t *testing.T) {
	ix, root := build(t, map[string]string{})

	// Unclosed brace
	badBrace := "function test() {\n  const a = 1;\n"
	f1 := CheckDraft(ix, root, []byte(badBrace), "typescript")
	if len(f1) == 0 || f1[0].Kind != "parse_error" {
		t.Fatalf("expected parse_error for unclosed brace, got %+v", f1)
	}

	// Missing relative import
	badImport := "import { foo } from './nonexistent/module';\n"
	f2 := CheckDraft(ix, root, []byte(badImport), "typescript")
	if len(f2) == 0 || f2[0].Kind != "unknown_import" {
		t.Fatalf("expected unknown_import for missing relative module, got %+v", f2)
	}
}

func TestCheckDraftJSONSyntax(t *testing.T) {
	badJSON := `{"name": "kern", "invalid": }`
	f := CheckDraft(nil, "", []byte(badJSON), "json")
	if len(f) == 0 || f[0].Kind != "parse_error" {
		t.Fatalf("expected parse_error for bad json, got %+v", f)
	}

	cleanJSON := `{"name": "kern", "valid": true}`
	f2 := CheckDraft(nil, "", []byte(cleanJSON), "json")
	if len(f2) != 0 {
		t.Fatalf("expected 0 findings for clean json, got %+v", f2)
	}
}

