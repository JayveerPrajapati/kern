package astrefs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) *index.Index {
	t.Helper()
	root := t.TempDir()
	write(t, root, "lib/def.go", `package lib

type Widget struct{ N int }

func NewWidget() Widget { return Widget{} }
`)
	write(t, root, "consumer/use.go", `package consumer

import "example/lib"

var W lib.Widget
var S = "lib.Widget in a string"

// lib.Widget in a comment must not count.

func Make() lib.Widget { return lib.Widget{} }
`)
	write(t, root, "other/other.go", `package other

type Holder struct{ Widget int }

func Get(h Holder) int { return h.Widget }
`)
	return &index.Index{
		Root:       root,
		FileHashes: map[string]string{"lib/def.go": "", "consumer/use.go": "", "other/other.go": ""},
	}
}

// TestReferencesASTOnly pins the AST pass: comments and strings never count,
// an unqualified same-named symbol in another package is excluded, and a
// package-qualified selector plus same-package uses are reported with
// positions.
func TestReferencesASTOnly(t *testing.T) {
	ix := fixture(t)
	refs, complete := References(ix, index.Symbol{Name: "Widget", Kind: "struct", File: "lib/def.go"})
	if !complete {
		t.Fatal("References reported an incomplete scan on a tiny fixture")
	}
	files := DistinctFiles(refs)
	want := []string{"consumer/use.go", "lib/def.go"}
	if len(files) != len(want) {
		t.Fatalf("reference files = %v, want %v", files, want)
	}
	for i := range want {
		if files[i] != want[i] {
			t.Fatalf("reference files = %v, want %v", files, want)
		}
	}
	for _, r := range refs {
		if r.File == "other/other.go" {
			t.Fatalf("unqualified Widget in another package counted as a reference: %+v", r)
		}
		if r.Line <= 0 {
			t.Fatalf("reference without a line: %+v", r)
		}
	}
}

func TestReferencesFieldSelectors(t *testing.T) {
	ix := fixture(t)
	refs, _ := References(ix, index.Symbol{Name: "N", Kind: "field", File: "lib/def.go"})
	if len(refs) != 0 {
		t.Fatalf("field N refs = %+v, want none (no x.N use in the fixture)", refs)
	}
	write(t, ix.Root, "consumer/field.go", `package consumer

import "example/lib"

func Read() int {
	var w lib.Widget
	return w.N
}
`)
	ix.FileHashes["consumer/field.go"] = ""
	refs, _ = References(ix, index.Symbol{Name: "N", Kind: "field", File: "lib/def.go"})
	if len(refs) != 1 || refs[0].Kind != "selector" {
		t.Fatalf("field N refs = %+v, want one selector use", refs)
	}
}
