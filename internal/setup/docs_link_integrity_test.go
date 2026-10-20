package setup

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestCurrentDocsRelativeLinksExist keeps current user-facing documentation
// free of dead relative links. Historical ADRs, migration records, and retired
// gate catalogs intentionally reference deleted subsystems, so they are outside
// this gate.
func TestCurrentDocsRelativeLinksExist(t *testing.T) {
	t.Parallel()

	docs := []string{
		"README.md",
		"AGENTS.md",
		"SECURITY.md",
		"docs/index.md",
		"docs/architecture.md",
		"docs/configuration.md",
		"docs/governance-troubleshooting.md",
		"docs/contributing/architecture-tour.md",
		"docs/mcp-client.md",
		"docs/mcp/README.md",
		"docs/mcp/protocol.md",
		"docs/mcp/tool-contracts.md",
		"docs/tool-catalog.md",
		"docs/tool-selection.md",
		"docs/security/threat-model.md",
	}

	// Match [text](target), including an optional Markdown title. External URLs,
	// absolute paths, mailto links, and pure anchors are intentionally ignored.
	linkRE := regexp.MustCompile(`\[[^\]]+\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
	var violations []string
	for _, rel := range docs {
		b, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		for _, match := range linkRE.FindAllStringSubmatch(string(b), -1) {
			target := match[1]
			if target == "" || target == "#" || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			file := strings.SplitN(target, "#", 2)[0]
			if file == "" || filepath.IsAbs(file) {
				continue
			}
			resolved := filepath.Clean(filepath.Join("..", "..", filepath.Dir(rel), file))
			if _, err := os.Stat(resolved); err != nil {
				violations = append(violations, rel+": "+target)
			}
		}
	}
	if len(violations) > 0 {
		t.Fatalf("dead relative links in current docs:\n%s", strings.Join(violations, "\n"))
	}
}
