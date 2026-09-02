package sec

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// writeTaintFile writes a fixture file under root, creating parent dirs.
func writeTaintFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTaintLiteEntryReach(t *testing.T) {
	root := t.TempDir()
	// The sink file is clean of source patterns; taint must come from the
	// entry-point path H -> util.F -> S.
	writeTaintFile(t, root, "svc/sink.go", "package svc\n\nfunc S(q string) string {\n\treturn q\n}\n")
	writeTaintFile(t, root, "util.go", "package util\n\nfunc F() {}\n")
	writeTaintFile(t, root, "main.go", "package main\n\nfunc H() {}\n")

	ix := &index.Index{
		Root: root,
		Symbols: []index.Symbol{
			{Kind: "func", Name: "H", File: "main.go", Line: 3, End: 3, Entry: true, Framework: "net-http", Route: "/x"},
			{Kind: "func", Name: "F", File: "util.go", Line: 3, End: 3},
			{Kind: "func", Name: "S", File: "svc/sink.go", Line: 3, End: 5},
		},
		Callers: map[string][]string{
			"S":      {"util.F"}, // cross-package qualified key
			"util.F": {"H"},
			"F":      {"H"}, // bare variant of the qualified key
		},
	}
	findings := []Finding{{
		File: "svc/sink.go", Line: 4, Rule: "sql-injection",
		Severity: "error", Message: "dynamic SQL built from variables",
	}}
	got := TaintLite(ix, findings)
	if len(got) != 1 {
		t.Fatalf("expected 1 taint finding, got %d", len(got))
	}
	tf := got[0]
	if !tf.Tainted {
		t.Fatal("expected tainted")
	}
	if tf.Func != "S" {
		t.Errorf("Func = %q, want S", tf.Func)
	}
	if tf.EntryPoint != "H" {
		t.Errorf("EntryPoint = %q, want H", tf.EntryPoint)
	}
	if len(tf.Path) == 0 {
		t.Fatal("expected non-empty path")
	}
	if tf.Path[0] != "H" || tf.Path[len(tf.Path)-1] != "S" {
		t.Errorf("path = %v, want source-side first ending at S", tf.Path)
	}
}

func TestTaintLiteSourceFile(t *testing.T) {
	root := t.TempDir()
	// Sink function is not reachable from any entry, but its file contains a
	// source expression (r.FormValue()).
	writeTaintFile(t, root, "app.go", "package main\n\nfunc sink() {\n\tname := r.FormValue(\"q\")\n\t_ = name\n}\n")
	ix := &index.Index{
		Root:    root,
		Symbols: []index.Symbol{{Kind: "func", Name: "sink", File: "app.go", Line: 3, End: 6}},
		Callers: map[string][]string{},
	}
	findings := []Finding{{
		File: "app.go", Line: 4, Rule: "sql-injection",
		Severity: "error", Message: "dynamic SQL built from variables",
	}}
	got := TaintLite(ix, findings)
	if len(got) != 1 {
		t.Fatalf("expected 1 taint finding, got %d", len(got))
	}
	if !got[0].Tainted {
		t.Fatal("expected tainted via source file")
	}
	if got[0].EntryPoint != "" {
		t.Errorf("EntryPoint = %q, want empty", got[0].EntryPoint)
	}
}

func TestTaintLiteUnreachedSink(t *testing.T) {
	root := t.TempDir()
	writeTaintFile(t, root, "svc/sink.go", "package svc\n\nfunc S() {\n\t_ = 1\n}\n")
	ix := &index.Index{
		Root: root,
		Symbols: []index.Symbol{
			{Kind: "func", Name: "S", File: "svc/sink.go", Line: 3, End: 5},
			{Kind: "func", Name: "H", File: "main.go", Line: 3, End: 3, Entry: true},
		},
		Callers: map[string][]string{}, // nothing calls S; H is never reached
	}
	findings := []Finding{{
		File: "svc/sink.go", Line: 4, Rule: "command-injection",
		Severity: "error", Message: "shell command built from variables",
	}}
	got := TaintLite(ix, findings)
	if len(got) != 1 {
		t.Fatalf("expected 1 taint finding, got %d", len(got))
	}
	if got[0].Tainted {
		t.Fatalf("expected untainted, got %+v", got[0])
	}
	if got[0].Func != "S" {
		t.Errorf("Func = %q, want S", got[0].Func)
	}
}

func TestTaintLiteUnknownFunc(t *testing.T) {
	root := t.TempDir()
	// app.go holds no symbol covering the finding line.
	writeTaintFile(t, root, "app.go", "package main\n\nvar x = 1\n")
	ix := &index.Index{
		Root:    root,
		Symbols: []index.Symbol{{Kind: "func", Name: "Other", File: "other.go", Line: 3, End: 5}},
		Callers: map[string][]string{},
	}
	findings := []Finding{{
		File: "app.go", Line: 3, Rule: "sql-injection",
		Severity: "error", Message: "dynamic SQL built from variables",
	}}
	got := TaintLite(ix, findings)
	if len(got) != 1 {
		t.Fatalf("expected 1 taint finding, got %d", len(got))
	}
	if got[0].Func != "<unknown>" {
		t.Errorf("Func = %q, want <unknown>", got[0].Func)
	}
	if got[0].Tainted {
		t.Error("expected Tainted false for an unresolvable line")
	}
}

func TestTaintLiteDeterministic(t *testing.T) {
	root := t.TempDir()
	writeTaintFile(t, root, "svc/sink.go", "package svc\n\nfunc S() {\n\t_ = 1\n}\n")
	writeTaintFile(t, root, "app.go", "package main\n\nfunc sink() {\n\t_ = r.FormValue(\"q\")\n}\n")
	writeTaintFile(t, root, "main.go", "package main\n\nfunc H() {}\n")
	ix := &index.Index{
		Root: root,
		Symbols: []index.Symbol{
			{Kind: "func", Name: "S", File: "svc/sink.go", Line: 3, End: 5},
			{Kind: "func", Name: "sink", File: "app.go", Line: 3, End: 6},
			{Kind: "func", Name: "H", File: "main.go", Line: 3, End: 3, Entry: true},
		},
		// Unsorted caller lists must not affect the result.
		Callers: map[string][]string{
			"S": {"M", "A"},
			"M": {"H", "Z"},
			"A": {"H"},
			"H": {"init"},
		},
	}
	findings := []Finding{
		{File: "svc/sink.go", Line: 4, Rule: "command-injection", Severity: "error", Message: "shell command built from variables"},
		{File: "app.go", Line: 4, Rule: "sql-injection", Severity: "error", Message: "dynamic SQL built from variables"},
		{File: "main.go", Line: 3, Rule: "unsafe-deserialization", Severity: "warning", Message: "untrusted input deserialized into untyped/weak types"},
	}
	a := TaintLite(ix, findings)
	b := TaintLite(ix, findings)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("TaintLite not deterministic:\na=%+v\nb=%+v", a, b)
	}
	// Findings order is preserved.
	if len(a) != 3 || a[0].File != "svc/sink.go" || a[2].File != "main.go" {
		t.Errorf("findings order not preserved: %+v", a)
	}
}

func TestTaintLiteDepthCap(t *testing.T) {
	root := t.TempDir()
	writeTaintFile(t, root, "svc/sink.go", "package svc\n\nfunc S() {\n\t_ = 1\n}\n")
	// Chain S -> n0 -> ... -> n19 -> H is 21 edges deep, beyond the BFS cap.
	callers := map[string][]string{}
	prev := "S"
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("n%d", i)
		callers[prev] = []string{name}
		prev = name
	}
	callers[prev] = []string{"H"}
	ix := &index.Index{
		Root: root,
		Symbols: []index.Symbol{
			{Kind: "func", Name: "S", File: "svc/sink.go", Line: 3, End: 5},
			{Kind: "func", Name: "H", File: "main.go", Line: 3, End: 3, Entry: true},
		},
		Callers: callers,
	}
	findings := []Finding{{
		File: "svc/sink.go", Line: 4, Rule: "sql-injection",
		Severity: "error", Message: "dynamic SQL built from variables",
	}}
	// Must terminate without a panic and still return a result.
	got := TaintLite(ix, findings)
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	// The entry sits beyond the depth cap: the sink stays untainted.
	if got[0].Tainted {
		t.Error("expected sink beyond depth cap to stay untainted")
	}
}

func TestTaintLiteEmptyFindings(t *testing.T) {
	ix := &index.Index{Root: t.TempDir(), Symbols: nil, Callers: map[string][]string{}}
	if got := TaintLite(ix, nil); len(got) != 0 {
		t.Fatalf("expected empty result for empty findings, got %d", len(got))
	}
}
