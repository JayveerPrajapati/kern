package integration

// Slice 5: the same graph query must be answered identically by the CLI read
// path (the functions cmd/kern's runGraph calls), the MCP tool path
// (internal/mcp/graph.Graph with format args), and the daemon's in-process
// fallback (KERN_DAEMON=0 runs the same CLI path). No socket is bound: the
// daemon dispatch MAPPING is pinned unit-style in cmd/kern
// (daemon_dispatch_parity_test.go), and here we compare the in-process data
// all three routes converge on.

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	mcpgov "github.com/JayveerPrajapati/kern/internal/mcp/gov"
	mcpgraph "github.com/JayveerPrajapati/kern/internal/mcp/graph"
	"github.com/JayveerPrajapati/kern/internal/mcp/provenance"
)

func surfaceFixture(t *testing.T) (*index.Index, string) {
	t.Helper()
	root := writeGraphFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("index.Build: %v", err)
	}
	return ix, root
}

func writeGraphFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":           "module fx\n\ngo 1.20\n",
		"shared/shared.go": "package shared\n\nfunc Shared() int { return 1 }\n",
		"lib/lib.go": "package lib\n\nimport \"fx/shared\"\n\n" +
			"func Run() int { return shared.Shared() }\n\n" +
			"func Helper() int { return 1 }\n\n" +
			"func UseHelper() int { return Helper() }\n",
	}
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// rawGraphGov is the permissive (nil governor) bundle a local caller gets.
func rawGraphGov() mcpgov.GovContext {
	return mcpgov.GovContext{
		NewGov:   func() (*mcpgov.Governor, error) { return nil, nil },
		StampGov: func(*mcpgov.Governor, []provenance.SymbolProvenance) {},
		StampRaw: func([]provenance.SymbolProvenance) {},
	}
}

// graphDataSignature canonicalizes a structured GraphResult's node/edge sets.
func graphDataSignature(g index.GraphResult) (nodes, edges string) {
	var n []string
	for _, node := range g.Nodes {
		var cands []string
		cands = append(cands, node.Candidates...)
		sort.Strings(cands)
		n = append(n, strings.Join(append([]string{node.ID, node.Kind, node.Name, node.File, node.Resolution}, cands...), "|"))
	}
	sort.Strings(n)
	var e []string
	for _, edge := range g.Edges {
		e = append(e, strings.Join([]string{
			edge.From, edge.To, edge.Reference, edge.SourceFile,
			edge.Resolution, edge.Confidence, edge.Synth, edge.Binding,
		}, "|"))
	}
	sort.Strings(e)
	return strings.Join(n, "\n"), strings.Join(e, "\n")
}

// TestGraphCLIEqualsMCPDefault pins the default text path: the CLI renders
// intel.GraphCtxMin with the adaptive budget; the MCP Graph tool (no format)
// must return exactly that text (no freshness footer unless requested).
func TestGraphCLIEqualsMCPDefault(t *testing.T) {
	ix, _ := surfaceFixture(t)
	symbol := "Run"

	cli := func() string {
		out, err := intel.GraphCtxMin(ix, symbol, intel.AdaptiveGraphCtxTokens(ix, symbol), "")
		if err != nil {
			t.Fatalf("CLI GraphCtxMin: %v", err)
		}
		return out
	}()

	mcp, err := mcpgraph.Graph(context.Background(), ix, rawGraphGov(), map[string]any{"symbol": symbol})
	if err != nil {
		t.Fatalf("MCP Graph: %v", err)
	}
	if mcp != cli {
		t.Fatalf("MCP default graph differs from CLI default:\n--- CLI ---\n%s\n--- MCP ---\n%s", cli, mcp)
	}
}

// TestGraphOneLineCLIEqualsMCP pins the one-line view across surfaces: both
// render index.Graph(symbol).
func TestGraphOneLineCLIEqualsMCP(t *testing.T) {
	ix, _ := surfaceFixture(t)
	symbol := "Run"

	cli := ix.Graph(symbol)
	mcp, err := mcpgraph.Graph(context.Background(), ix, rawGraphGov(), map[string]any{"symbol": symbol, "format": "one-line"})
	if err != nil {
		t.Fatalf("MCP Graph(one-line): %v", err)
	}
	if mcp != cli {
		t.Fatalf("MCP one-line graph differs from CLI one-line:\n--- CLI ---\n%s\n--- MCP ---\n%s", cli, mcp)
	}
}

// TestGraphStructuredDataStableAcrossSidecar pins that the structured graph
// data behind `kern graph --json` (index.Neighborhood) is identical when the
// export catalog is rebuilt vs loaded from the persisted sidecar — the
// daemon's in-process fallback loads the same sidecar.
func TestGraphStructuredDataStableAcrossSidecar(t *testing.T) {
	ix, root := surfaceFixture(t)
	symbol := "Run"
	fresh, ok := ix.Neighborhood(symbol)
	if !ok {
		t.Fatalf("Neighborhood(%q) not found", symbol)
	}

	// Second build of the same fixture: new *Index, empty cache → the
	// sidecar path is what a fresh process (or a daemon child) would take.
	ix2, err := index.Build(root)
	if err != nil {
		t.Fatalf("index.Build (2): %v", err)
	}
	loaded, ok := ix2.Neighborhood(symbol)
	if !ok {
		t.Fatalf("Neighborhood(%q) not found on second build", symbol)
	}

	fn, fe := graphDataSignature(fresh)
	ln, le := graphDataSignature(loaded)
	if fn != ln {
		t.Fatalf("graph NODES differ between fresh build and sidecar load:\n--- fresh ---\n%s\n--- loaded ---\n%s", fn, ln)
	}
	if fe != le {
		t.Fatalf("graph EDGES differ between fresh build and sidecar load:\n--- fresh ---\n%s\n--- loaded ---\n%s", fe, le)
	}
	if len(fresh.Nodes) == 0 || len(fresh.Edges) == 0 {
		t.Fatalf("fixture graph is empty (nodes=%d edges=%d)", len(fresh.Nodes), len(fresh.Edges))
	}
}
