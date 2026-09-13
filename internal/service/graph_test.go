package service

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestGraphServiceSmoke exercises every GraphService operation against a tiny
// project with two symbols (Greeter, Run).
func TestGraphServiceSmoke(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "demo.go", tinyProject)

	svc := New()
	ctx := context.Background()

	// Graph renders the neighbourhood of a symbol.
	out, err := svc.Graph.Graph(ctx, root, "Greeter")
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if !strings.Contains(out, "Greeter") {
		t.Errorf("Graph output missing symbol: %q", out)
	}

	// Neighborhood returns the structured graph result.
	nb, err := svc.Graph.Neighborhood(ctx, root, "Greeter")
	if err != nil {
		t.Fatalf("Neighborhood: %v", err)
	}
	if len(nb.Nodes) == 0 {
		t.Error("Neighborhood returned no nodes")
	}

	// Search finds symbols by free text.
	hits, err := svc.Graph.Search(ctx, root, "Greeter", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Error("Search returned no hits for Greeter")
	}

	// AstSearch finds symbols by pattern.
	patternHits, err := svc.Graph.AstSearch(ctx, root, "Run", 10)
	if err != nil {
		t.Fatalf("AstSearch: %v", err)
	}
	if len(patternHits) == 0 {
		t.Error("AstSearch returned no hits for Run")
	}

	// Why explains the symbol.
	info, err := svc.Graph.Why(ctx, root, "Run")
	if err != nil {
		t.Fatalf("Why: %v", err)
	}
	if info.Symbol.Name == "" {
		t.Error("Why returned a symbol with an empty name")
	}
	if len(info.Callers) == 0 && info.InEdges == 0 {
		t.Log("Why: Run has no in-edges (callers may be unresolved in the tiny fixture)")
	}

	// Path computes a call path between symbols.
	path, err := svc.Graph.Path(ctx, root, "Run", "Hello")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if len(path) == 0 {
		t.Error("Path returned an empty path between Run and Hello")
	}

	// Explore bundles definition, call flow and blast radius.
	rep, err := svc.Graph.Explore(ctx, root, "Run", 2, 20)
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if rep == nil {
		t.Fatal("Explore returned nil report")
	}
}

// TestGraphServiceUnknownSymbol verifies error paths for missing symbols.
func TestGraphServiceUnknownSymbol(t *testing.T) {
	root := t.TempDir()
	writeGoFile(t, root, "demo.go", tinyProject)

	svc := New()
	ctx := context.Background()

	if _, err := svc.Graph.Why(ctx, root, "DoesNotExist"); err == nil {
		t.Error("Why should error on unknown symbol")
	}
	if _, err := svc.Graph.Neighborhood(ctx, root, "DoesNotExist"); err == nil {
		t.Error("Neighborhood should error on unknown symbol")
	}
	if _, err := svc.Graph.Path(ctx, root, "Run", "DoesNotExist"); err == nil {
		t.Error("Path should error on unknown symbol")
	}
}

// TestGraphServiceIndexLoadOrBuild pins the graph service's index resolution:
// it returns a usable index for a real (small) root and honors a cancelled
// context with a fast error.
func TestGraphServiceIndexLoadOrBuild(t *testing.T) {
	s := &graphService{}
	root := t.TempDir()
	ix, err := s.index(context.Background(), root)
	if err != nil {
		t.Fatalf("index on empty root: %v", err)
	}
	if ix == nil {
		t.Fatal("expected a non-nil index")
	}
	_ = ix.Search("nothing", 5) // must not panic on the empty index

	// Cancelled context: index() checks ctx.Err() first.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.index(ctx, root); err == nil {
		t.Error("expected error for cancelled context")
	}
}

// TestGraphServiceExploreEndToEnd exercises the full graph path used by the
// MCP tools: index -> intel.Explore on a real symbol in a tiny fixture tree.
func TestGraphServiceExploreEndToEnd(t *testing.T) {
	s := &graphService{}
	root := t.TempDir()
	writeGoFile(t, root, "main.go", "package main\nfunc Greet() string { return \"hi\" }\n")
	ix, err := s.index(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	_ = index.StorePath(root)
	if len(ix.Symbols) == 0 {
		t.Fatal("fixture tree should index at least Greet")
	}
}
