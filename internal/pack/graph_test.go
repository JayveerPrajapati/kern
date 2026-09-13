package pack

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// graphFixture is a small two-package tree mirroring the index snapshot
// fixture: Public calls inner and is called by UsePublic; main only reaches
// the lib package through UsePublic.
var graphFixture = map[string]string{
	"lib/lib.go": `package lib
// Public calls inner and is called by UsePublic.
func Public() string {
	return inner()
}

func inner() string {
	return "x"
}

// UsePublic is a caller of Public.
func UsePublic() string {
	return Public()
}
`,
	"app/main.go": `package main
import "example.com/repo/lib"
func main() {
	lib.UsePublic()
}
`,
}

// buildGraphIndex writes the fixture tree and returns its root.
func buildGraphIndex(t *testing.T) string {
	t.Helper()
	return writeTree(t, graphFixture)
}

func treeHash(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestBuildGraphSubgraph: the bundle carries the symbol's neighbourhood
// (definition + callers + callees), a one-line signature per node, and the
// per-file SHA-256 fingerprint matching the tree hashes.
func TestBuildGraphSubgraph(t *testing.T) {
	root := buildGraphIndex(t)
	gb, err := BuildGraph(root, Options{GraphSymbol: "Public"})
	if err != nil {
		t.Fatal(err)
	}
	if gb.Mode != "subgraph" {
		t.Fatalf("Mode = %q; want subgraph", gb.Mode)
	}
	if gb.Symbol != "Public" {
		t.Fatalf("Symbol = %q; want Public", gb.Symbol)
	}
	// Neighbourhood of Public: definition + caller UsePublic + callee inner.
	nodeSet := map[string]bool{}
	for _, n := range gb.Nodes {
		nodeSet[n] = true
	}
	for _, want := range []string{"Public", "UsePublic", "inner"} {
		if !nodeSet[want] {
			t.Errorf("subgraph missing node %q; nodes = %v", want, gb.Nodes)
		}
	}
	// One sorted signature per node.
	if len(gb.Signatures) != len(gb.Nodes) {
		t.Fatalf("Signatures (%d) != Nodes (%d)", len(gb.Signatures), len(gb.Nodes))
	}
	for i := 1; i < len(gb.Nodes); i++ {
		if gb.Nodes[i-1] >= gb.Nodes[i] {
			t.Fatalf("Nodes not sorted: %v", gb.Nodes)
		}
	}
	for _, id := range gb.Nodes {
		sig, ok := gb.Signatures[id]
		if !ok {
			t.Fatalf("no signature for node %q", id)
		}
		if !strings.Contains(sig, id) {
			t.Errorf("signature %q does not name node %q", sig, id)
		}
	}
	if sig := gb.Signatures["Public"]; !strings.Contains(sig, "lib/lib.go") {
		t.Errorf("Public signature %q lacks file:line", sig)
	}
	// Fingerprint matches the on-disk tree hashes.
	if len(gb.Files) != len(graphFixture) {
		t.Fatalf("fingerprint has %d files; want %d", len(gb.Files), len(graphFixture))
	}
	for rel := range graphFixture {
		if got := gb.Files[rel]; got != treeHash(t, root, rel) {
			t.Errorf("fingerprint[%q] = %q; want tree hash", rel, got)
		}
	}
	out := gb.Render()
	for _, marker := range []string{"== symbols (", "== edges (", "== fingerprint (", "Public ->", "inner"} {
		if !strings.Contains(out, marker) {
			t.Errorf("render missing %q", marker)
		}
	}
}

// TestBuildGraphWholeContainsSubgraph: whole mode packs at least as many
// nodes as any single-symbol subgraph (the snapshot's 400-symbol default cap
// is far above this fixture's symbol count).
func TestBuildGraphWholeContainsSubgraph(t *testing.T) {
	root := buildGraphIndex(t)
	whole, err := BuildGraph(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if whole.Mode != "whole" {
		t.Fatalf("Mode = %q; want whole", whole.Mode)
	}
	if whole.Symbol != "" {
		t.Fatalf("Symbol = %q; want empty for whole graph", whole.Symbol)
	}
	sub, err := BuildGraph(root, Options{GraphSymbol: "Public"})
	if err != nil {
		t.Fatal(err)
	}
	if len(whole.Nodes) <= len(sub.Nodes) {
		t.Fatalf("whole nodes (%d) must exceed subgraph nodes (%d)", len(whole.Nodes), len(sub.Nodes))
	}
	// Whole graph still carries the same tree fingerprint.
	for rel, h := range sub.Files {
		if whole.Files[rel] != h {
			t.Errorf("whole fingerprint differs from subgraph for %q", rel)
		}
	}
}

// TestBuildGraphUnknownSymbol: fail loud on an unknown symbol.
func TestBuildGraphUnknownSymbol(t *testing.T) {
	root := buildGraphIndex(t)
	_, err := BuildGraph(root, Options{GraphSymbol: "NoSuchSymbol"})
	if err == nil {
		t.Fatal("expected error for unknown symbol")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error %q should mention the missing symbol", err)
	}
}

// TestBuildGraphMaxTokensTruncation: the signature section is capped
// deterministically and the render carries the "...N more" marker.
func TestBuildGraphMaxTokensTruncation(t *testing.T) {
	root := buildGraphIndex(t)
	uncapped, err := BuildGraph(root, Options{GraphSymbol: "Public"})
	if err != nil {
		t.Fatal(err)
	}
	capped, err := BuildGraph(root, Options{GraphSymbol: "Public", MaxTokens: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(capped.Nodes) >= len(uncapped.Nodes) {
		t.Fatalf("capped nodes (%d) must be fewer than uncapped (%d)", len(capped.Nodes), len(uncapped.Nodes))
	}
	out := capped.Render()
	if !strings.Contains(out, "more symbols not packed (raise max_tokens)") {
		t.Fatalf("render missing truncation marker:\n%s", out)
	}
	// Deterministic across builds of the same state.
	capped2, err := BuildGraph(root, Options{GraphSymbol: "Public", MaxTokens: 8})
	if err != nil {
		t.Fatal(err)
	}
	if capped2.Render() != out {
		t.Fatal("capped render not deterministic across builds")
	}
}

// TestBuildGraphRenderDeterministic: the same tree yields byte-identical
// renders (no timestamps or map-iteration order in the output).
func TestBuildGraphRenderDeterministic(t *testing.T) {
	root := buildGraphIndex(t)
	a, err := BuildGraph(root, Options{GraphSymbol: "Public"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildGraph(root, Options{GraphSymbol: "Public"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Render() != b.Render() {
		t.Fatal("render not deterministic for identical state")
	}
}

// TestBuildGraphFingerprintDetectsChange: when a touched file's content
// changes, its fingerprint entry changes (the receiver-side freshness signal)
// while untouched files keep their hashes.
func TestBuildGraphFingerprintDetectsChange(t *testing.T) {
	root := buildGraphIndex(t)
	gb1, err := BuildGraph(root, Options{GraphSymbol: "Public"})
	if err != nil {
		t.Fatal(err)
	}
	h1 := gb1.Files["app/main.go"]
	if h1 == "" {
		t.Fatal("fingerprint missing app/main.go")
	}
	// Touch app/main.go: same symbols, different content.
	if err := os.WriteFile(filepath.Join(root, "app", "main.go"),
		[]byte("package main\nimport \"example.com/repo/lib\"\nfunc main() {\n\tlib.UsePublic() // touched\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gb2, err := BuildGraph(root, Options{GraphSymbol: "Public"})
	if err != nil {
		t.Fatal(err)
	}
	if gb2.Files["app/main.go"] == h1 {
		t.Fatal("fingerprint for touched file must change")
	}
	if gb2.Files["lib/lib.go"] != gb1.Files["lib/lib.go"] {
		t.Fatal("fingerprint for untouched file must stay the same")
	}
}
