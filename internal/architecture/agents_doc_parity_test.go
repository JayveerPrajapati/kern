package architecture_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp"
)

// advertRe matches the tool-count claims in the AGENTS.md usage-rules text
// ("among 140 individual `kern_*` tools", "all 140 tools", "ships 140
// `kern_*` MCP tools"). Keeping the advertised count in lockstep with the
// live MCP catalog is a docs-integrity requirement (F16): the
// machine-generated CLAUDE.md wiring used to claim 146 while the catalog
// had 140, and nothing but this guard caught the drift — it was caught by
// humans.
var advertRe = regexp.MustCompile(`(?:among|all|ships)\s+(\d+)\s+(?:individual\s+)?(?:kern_\*?\s+)?(?:MCP\s+)?tools`)

// TestAgentsDocToolCountMatchesCatalog guards the AGENTS.md advertised
// kern_* tool count against the live MCP catalog (catalog.All / ToolNames,
// the single point of truth guarded by TestCatalogCount and
// TestPluginMatchesMCPCatalog). The AGENTS.md usage-rules text is
// maintained by hand, so a count that silently drifts from the catalog is a
// doc bug; fail loudly instead of letting it ship.
func TestAgentsDocToolCountMatchesCatalog(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	actual := len(mcp.ToolNames())

	matches := advertRe.FindAllStringSubmatch(string(doc), -1)
	if len(matches) == 0 {
		t.Fatalf("AGENTS.md contains no advertised kern_* tool-count claims to guard")
	}
	for _, m := range matches {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("unparseable advertised count %q: %v", m[1], err)
		}
		if n != actual {
			t.Errorf("AGENTS.md advertises %d kern_* tools but the live MCP catalog has %d (%q) — update AGENTS.md to match the catalog",
				n, actual, strings.TrimSpace(m[0]))
		}
	}
}
