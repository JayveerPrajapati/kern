package intelligence

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestAdjacencyCachedPerGraph guards the fix that caches the "calls"
// adjacency per graph: a second query must reuse the same maps (identical
// pointers) rather than re-walking every edge. Before the cache, each of
// the 8 query sites rebuilt the full adjacency on every kern_impact /
// kern_why / blast-radius call.
func TestAdjacencyCachedPerGraph(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc A() { B() }\n\nfunc B() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix, err := index.Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	g := FromIndex(ix)
	out1, in1 := g.adjacency(false)
	if out1 == nil || in1 == nil {
		t.Fatal("adjacency returned nil maps")
	}
	if len(out1) == 0 && len(in1) == 0 {
		t.Fatal("fixture produced no call edges")
	}
	out2, in2 := g.adjacency(false)
	if reflect.ValueOf(out1).Pointer() != reflect.ValueOf(out2).Pointer() ||
		reflect.ValueOf(in1).Pointer() != reflect.ValueOf(in2).Pointer() {
		t.Fatal("loose adjacency rebuilt on second call - cache not effective")
	}
	s1, si1 := g.adjacency(true)
	s2, si2 := g.adjacency(true)
	if reflect.ValueOf(s1).Pointer() != reflect.ValueOf(s2).Pointer() ||
		reflect.ValueOf(si1).Pointer() != reflect.ValueOf(si2).Pointer() {
		t.Fatal("strict adjacency rebuilt on second call - cache not effective")
	}
}
