package intel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

func writeRefFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestExploreNonCallableSymbolReportsReferences pins the explore half
// of audit P0-1: a field-only type has no call edges, so "== callers (0)" was
// presented as the whole truth. Explore must add the labelled textual
// reference section instead of implying nothing uses the symbol.
func TestExploreNonCallableSymbolReportsReferences(t *testing.T) {
	root := t.TempDir()
	writeRefFixture(t, root, "lib/defs.go", `package lib

type FieldOnly struct{ N int }
`)
	writeRefFixture(t, root, "consumer/use.go", `package consumer

import "example/lib"

type Holder struct{ F lib.FieldOnly }

func Use() lib.FieldOnly { return lib.FieldOnly{N: 1} }
`)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("index.Build: %v", err)
	}
	rep, err := Explore(ix, "lib.FieldOnly", 2, 30)
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if len(rep.Callers) != 0 {
		t.Fatalf("fixture precondition: field-only type unexpectedly has callers %v", rep.Callers)
	}
	if rep.ReferenceConfidence == "" {
		t.Fatal("Explore reported no reference coverage for a field-only type")
	}
	if rep.ReferenceCount == 0 || len(rep.ReferenceFiles) == 0 {
		t.Fatalf("Explore found no references (count=%d files=%v)", rep.ReferenceCount, rep.ReferenceFiles)
	}
	out := RenderExplore(rep)
	if !strings.Contains(out, "== references (") || (!strings.Contains(out, "ast") && !strings.Contains(out, "textual")) {
		t.Errorf("RenderExplore omits the reference section:\n%s", out)
	}
}
