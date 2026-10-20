package index

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestContextDefServesFreshContent: ContextDef must serve the NEW content of
// a modified file (size change => stat miss) on the next call — the shared
// file-content cache re-checks size:mtime on every hit and must never serve
// stale bytes. Repeated identical calls must return identical output.
func TestContextDefServesFreshContent(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": "package main\n\nfunc greet() {}\n",
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	got1 := ix.Context("greet", 2)
	if !strings.Contains(got1, "func greet") {
		t.Fatalf("expected definition in first context:\n%s", got1)
	}
	// Modify the file (different size => different stat): the next call must
	// re-read and serve the new line.
	p := filepath.Join(dir, "main.go")
	if err := os.WriteFile(p, []byte("package main\n\nfunc greet() {}\n\n// added\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got2 := ix.Context("greet", 2)
	if !strings.Contains(got2, "// added") {
		t.Fatalf("context served stale content after edit:\n%s", got2)
	}
	// Repeated identical calls (file unchanged) return identical output.
	got3 := ix.Context("greet", 2)
	if got3 != got2 {
		t.Fatalf("identical calls returned different output:\n--- first ---\n%s\n--- second ---\n%s", got2, got3)
	}
}

// TestContextDefMtimeOnlyEditIsFresh: a same-size edit (mtime bump) is also
// detected, so a touched file never serves stale bytes.
func TestContextDefMtimeOnlyEditIsFresh(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"main.go": "package main\n\nfunc greet() { println(\"a\") }\n",
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c := ix.Context("greet", 2); !strings.Contains(c, "println(\"a\")") {
		t.Fatalf("expected original body:\n%s", c)
	}
	// Same-size edit: "a" -> "b" (identical length), mtime advanced via
	// Chtimes for determinism on coarse-granularity filesystems.
	p := filepath.Join(dir, "main.go")
	if err := os.WriteFile(p, []byte("package main\n\nfunc greet() { println(\"b\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	if c := ix.Context("greet", 2); !strings.Contains(c, "println(\"b\")") {
		t.Fatalf("same-size edit not detected (stale body):\n%s", c)
	}
}
