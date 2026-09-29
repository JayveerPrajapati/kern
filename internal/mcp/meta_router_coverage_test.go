package mcp

import (
	"sort"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp/catalog"
	"github.com/JayveerPrajapati/kern/internal/mcp/meta"
)

// kern_meta is advertised (by both the server and the opencode plugin, whose
// DEFAULT_TOOLS names it "NL router → all sub-tools") as the single entry
// point that can reach the other catalog tools via deterministic keyword
// classification. This test is the drift gate for that claim: every catalog
// tool — minus the router itself — must be dispatchable through the meta
// router's route table (meta.RoutableTools, i.e. metaRoutedTools).
//
// How routing works today (internal/mcp/meta/meta.go): classifyMetaRequest
// maps the request to a tool name, then Handle rewrites any name that is NOT
// in metaRoutedTools to the kern_search fallback before dispatch. So the
// set of tools kern_meta can actually invoke is exactly metaRoutedTools —
// classifier output outside that set never reaches the tool. If the catalog
// grows tools without adding route arms, those tools are unreachable via
// kern_meta even though the plugin advertises it as the router for
// everything; this gate fails with the exact list so the gap is visible
// instead of silent.
func TestMetaRouterCatalogCoverage(t *testing.T) {
	t.Parallel()

	catalogNames := make(map[string]bool, len(catalog.All))
	for _, tool := range catalog.All {
		catalogNames[tool.Name] = true
	}

	routed := meta.RoutableTools()
	routable := make(map[string]bool, len(routed))
	for _, name := range routed {
		routable[name] = true
		if !catalogNames[name] {
			t.Errorf("kern_meta routes to %q, which is not in the catalog (stale route arm)", name)
		}
	}

	// kern_meta itself is the router — routing to it would recurse, so it is
	// the one catalog tool intentionally not in its own route table.
	const selfTool = "kern_meta"

	var missing []string
	for _, tool := range catalog.All {
		if tool.Name == selfTool {
			continue
		}
		if !routable[tool.Name] {
			missing = append(missing, tool.Name)
		}
	}
	sort.Strings(missing)

	t.Logf("kern_meta router coverage: %d of %d catalog tools dispatchable (router itself excluded from the denominator's missing set)",
		len(routable), len(catalogNames))
	if len(missing) > 0 {
		t.Errorf("kern_meta cannot route to %d of %d catalog tools (requests that classify to them fall back to kern_search): %s",
			len(missing), len(catalogNames)-1, strings.Join(missing, ", "))
	}
}
