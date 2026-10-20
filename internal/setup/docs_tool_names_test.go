package setup

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	mcp "github.com/JayveerPrajapati/kern/internal/mcpcatalog"
)

// TestCurrentDocsToolNamesMatchCatalog prevents removed MCP tools from
// lingering in current user-facing docs. Historical migration examples in
// docs/mcp/versioning.md, ADRs, and the changelog intentionally retain old
// names; this gate covers the live guides users consult today.
func TestCurrentDocsToolNamesMatchCatalog(t *testing.T) {
	t.Parallel()

	toolNames := map[string]bool{}
	for _, name := range mcp.ToolNames() {
		toolNames[name] = true
	}

	docs := []string{
		"README.md",
		"AGENTS.md",
		"docs/architecture.md",
		"docs/configuration.md",
		"docs/contributing/architecture-tour.md",
		"docs/mcp-client.md",
		"docs/mcp/README.md",
		"docs/mcp/protocol.md",
		"docs/mcp/tool-contracts.md",
		"docs/tool-catalog.md",
		"docs/tool-selection.md",
		"docs/security/threat-model.md",
	}

	// Only fully backticked kern_* tokens are treated as MCP tool references.
	// Paths such as `sdk/python/kern_sdk` and CLI commands such as `kern doctor`
	// intentionally do not match.
	tokenRe := regexp.MustCompile("`(kern_[a-z][a-z0-9_]*)`")
	var violations []string
	for _, rel := range docs {
		b, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		seen := map[string]bool{}
		for _, match := range tokenRe.FindAllStringSubmatch(string(b), -1) {
			name := match[1]
			if seen[name] || toolNames[name] {
				continue
			}
			seen[name] = true
			violations = append(violations, rel+": "+name)
		}
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("removed MCP tool names in current docs:\n%s", strings.Join(violations, "\n"))
	}
}
