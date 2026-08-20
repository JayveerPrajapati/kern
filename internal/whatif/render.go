package whatif

import (
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intelligence"
)

// SimulateRender builds the index for root, applies the hypothetical change to
// the knowledge graph, and returns the rendered what-if report. Routing both
// the CLI and MCP callers through this helper keeps their output identical. It
// is read-only: it never mutates the graph or index.
func SimulateRender(root string, kind ChangeKind, change, newTarget string) (string, error) {
	ix, err := index.Build(root)
	if err != nil {
		return "", fmt.Errorf("what-if: index: %w", err)
	}
	g := intelligence.FromIndex(ix)
	target := change
	if strings.ContainsAny(change, " \t") {
		cands := ExtractSymbols(change)
		if len(cands) == 0 {
			return "", fmt.Errorf("what-if: could not identify a symbol in the change description. Pass a bare symbol name (e.g. 'GetMySQLDB') or include a qualified name (e.g. 'pkg.Symbol') in the description.")
		}
		target = cands[0]
	}
	imp := Simulate(&g, Change{Kind: kind, Target: target, NewTarget: newTarget})
	var b strings.Builder
	fmt.Fprintf(&b, "change: %s %s\n", kind, target)
	if target != change {
		desc := change
		if len(desc) > 60 {
			desc = desc[:60]
		}
		fmt.Fprintf(&b, "  (extracted from: %s)\n", desc)
	}
	fmt.Fprintf(&b, "affected: %d\n", len(imp.Affected))
	fmt.Fprintf(&b, "files: %d\n", len(imp.Files))
	fmt.Fprintf(&b, "services: %d\n", len(imp.Services))
	fmt.Fprintf(&b, "tests: %d\n", len(imp.Tests))
	fmt.Fprintf(&b, "risk: %s\n", imp.Risk)
	fmt.Fprintf(&b, "recommendation: %s\n", imp.Recommendation)
	for _, c := range imp.Claims {
		fmt.Fprintf(&b, "claim[%s] %s (%.1f): %s\n", c.Type, c.Provenance, c.Confidence, c.Statement)
	}
	return b.String(), nil
}
