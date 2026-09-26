package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/twin"
)

// graphEntitiesFixture writes a temp repo whose index carries a real
// framework entry point that the regex extractors cannot see (mux.HandleFunc
// is not one of the extractor's route patterns), plus a duplicate-route trap:
// /v1/users registered via two extractor-recognized frameworks so the
// inventory must render it once.
func graphEntitiesFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module demo\n\ngo 1.22\n")
	write("main.go", `package main

import (
	"fmt"
	"net/http"
)

// ServeMux registers the server's endpoints.
func ServeMux() {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", handleHealth)
}

// handleHealth serves the health endpoint.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "ok")
}
`)
	// Duplicate-route trap: /v1/users registered via both a gin-style verb
	// call and net/http HandleFunc, which the extractor records as two
	// distinct framework nodes with the same display name.
	write("dup.go", `package main

import "net/http"

func dupRoutes() {
	r := &ginRouter{}
	r.GET("/v1/users", handleUsers)
	http.HandleFunc("/v1/users", handleUsers)
}

func handleUsers() {}

type ginRouter struct{}

func (g *ginRouter) GET(path string, h func()) {}
`)
	return root
}

// TestGraphEntityBlockIndexEntry locks the entities leaf over the twin-merged
// graph: an index entry point (mux.HandleFunc) that the regex extractors do
// not match still surfaces as an api entity connected to its handler symbol.
func TestGraphEntityBlockIndexEntry(t *testing.T) {
	root := graphEntitiesFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := entityBlock(ix, map[string]any{"root": root}, "handleHealth")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "entities (") {
		t.Fatalf("entity block must render the entities header, got: %s", out)
	}
	if !strings.Contains(out, "/v1/health") {
		t.Errorf("expected the /v1/health api entity connected to handleHealth, got:\n%s", out)
	}
}

// TestGraphEntitiesInventoryDedup locks the inventory dedup: an endpoint
// recorded by several extractor frameworks (and the index derivation) with
// the same display name renders exactly once.
func TestGraphEntitiesInventoryDedup(t *testing.T) {
	root := graphEntitiesFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	ents, err := twin.Entities(twin.MergeIntoIndex(ix, root), "")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range ents {
		if e.Kind == "api" && e.Name == "GET /v1/users" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("route GET /v1/users rendered %d times in the inventory, want 1", count)
	}
}

// TestGraphEntityBlockUnknownSymbol locks the unresolvable-symbol contract of
// the entities leaf: an unknown symbol errors instead of rendering an empty
// block, matching the CLI/MCP no-symbol behavior.
func TestGraphEntityBlockUnknownSymbol(t *testing.T) {
	root := graphEntitiesFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entityBlock(ix, map[string]any{"root": root}, "DoesNotExist"); err == nil {
		t.Fatal("entity block for an unknown symbol: want error, got nil")
	}
}
