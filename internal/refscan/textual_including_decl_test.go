package refscan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func writeRefScanFile(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func refScanIndex(root string, files ...string) *index.Index {
	hashes := map[string]string{}
	for _, f := range files {
		hashes[f] = "h"
	}
	return &index.Index{Root: root, FileHashes: hashes, GeneratedFiles: map[string]bool{}}
}

// TestTextualReferencesIncludingDeclCountsSameFileUse pins the P2-2 gap: a
// reference in the file that declares the symbol was dropped by excluding the
// whole defining file, so a single-file C#/Java project reported zero refs.
func TestTextualReferencesIncludingDeclCountsSameFileUse(t *testing.T) {
	root := t.TempDir()
	writeRefScanFile(t, root, "a.cs", "class Widget {}\n// use\nvar w = new Widget();\n")
	writeRefScanFile(t, root, "b.cs", "var x = new Widget();\n")
	ix := refScanIndex(root, "a.cs", "b.cs")
	files, n, complete := TextualReferencesIncludingDecl(ix, "Widget", "a.cs", 1)
	if !complete {
		t.Fatal("scan unexpectedly truncated")
	}
	if n != 2 {
		t.Fatalf("occurrences = %d, want 2 (one same-file use + one cross-file use); files=%v", n, files)
	}
	if len(files) != 2 {
		t.Fatalf("files = %v, want both a.cs and b.cs", files)
	}
}

// TestTextualReferencesStillExcludesDefFile keeps the legacy API's contract.
func TestTextualReferencesStillExcludesDefFile(t *testing.T) {
	root := t.TempDir()
	writeRefScanFile(t, root, "a.cs", "class Widget {}\nvar w = new Widget();\n")
	ix := refScanIndex(root, "a.cs")
	files, n, _ := TextualReferences(ix, "Widget", "a.cs")
	if n != 0 || len(files) != 0 {
		t.Fatalf("legacy API must exclude the def file, got n=%d files=%v", n, files)
	}
}

// TestCountWordBoundaryOnLine covers the declaration-line subtract directly.
func TestCountWordBoundaryOnLine(t *testing.T) {
	src := []byte("class Widget {}\nnew Widget();\nWidget x;\n")
	if got := countWordBoundaryOnLine(src, []byte("Widget"), 1); got != 1 {
		t.Fatalf("line 1 = %d, want 1", got)
	}
	if got := countWordBoundaryOnLine(src, []byte("Widget"), 2); got != 1 {
		t.Fatalf("line 2 = %d, want 1", got)
	}
	if got := countWordBoundaryOnLine(src, []byte("Widget"), 99); got != 0 {
		t.Fatalf("past EOF = %d, want 0", got)
	}
}

// TestTextualReferencesSkipsAllDeclarationLines: the class and its constructor
// both carry the type name; neither declaration is a use.
func TestTextualReferencesSkipsAllDeclarationLines(t *testing.T) {
	root := t.TempDir()
	writeRefScanFile(t, root, "a.cs", "class Widget {}\n// ctor\npublic Widget(int x) {}\nvar w = new Widget(1);\n")
	ix := refScanIndex(root, "a.cs")
	ix.SymbolsByFile = map[string][]index.Symbol{
		"a.cs": {{Name: "Widget", Line: 1}, {Name: "Widget", Line: 3}},
	}
	files, n, _ := TextualReferencesIncludingDecl(ix, "Widget", "a.cs", 1)
	if n != 1 || len(files) != 1 {
		t.Fatalf("occurrences = %d files=%v, want 1 (only the use on line 4)", n, files)
	}
}
