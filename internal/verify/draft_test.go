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
