package architecture_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/mcp"
	"github.com/JayveerPrajapati/kern/internal/mcp/toolsurface"
)

// surfaceRe matches the thin AGENTS.md's default-surface claim
// ("Default surface: 7 tools (kern_meta, ...)"); fullRe matches its
// full-catalog claim ("KERN_MCP_FULL=1 for all 117"). The thin format is
// the only AGENTS.md this repo ships; these claims are the docs-integrity
// surface this guard pins.
var (
	surfaceRe = regexp.MustCompile(`Default surface:\s*(\d+)\s*tools?\s*\(([^)]*)\)`)
	fullRe    = regexp.MustCompile(`KERN_MCP_FULL=1 for all\s+(\d+)`)
)

// TestAgentsDocToolCountMatchesCatalog guards the thin AGENTS.md's two
// tool-count claims against the live sources of truth: the default-surface
// count and names against internal/mcp/toolsurface.Default (the
// advertisement gate also pinned by TestDefaultToolsMatchToolsurface), and
// the full-catalog count against mcp.ToolNames (guarded by TestCatalogCount
// and TestPluginMatchesMCPCatalog). The AGENTS.md usage-rules text is
// maintained by hand, so a claim that silently drifts is a doc bug; if the
// claims disappear entirely, fatal — they are the thing being guarded.
func TestAgentsDocToolCountMatchesCatalog(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}

	// Default-surface claim: "Default surface: 7 tools (names...)".
	sm := surfaceRe.FindStringSubmatch(string(doc))
	if sm == nil {
		t.Fatalf(`AGENTS.md contains no "Default surface: N tools (...)" claim to guard`)
	}
	advertised, err := strconv.Atoi(sm[1])
	if err != nil {
		t.Fatalf("unparseable default-surface count %q: %v", sm[1], err)
	}
	var names []string
	for _, name := range strings.Split(sm[2], ",") {
		if name = strings.TrimSpace(name); name != "" {
			// The list is markdown (backticked names); strip the ticks so
			// the comparison is on bare tool names.
			name = strings.Trim(name, "`")
			names = append(names, name)
		}
	}
	if advertised != len(names) {
		t.Errorf("AGENTS.md default-surface count %d disagrees with its own name list (%d names)",
			advertised, len(names))
	}
	if len(names) != len(toolsurface.Default) {
		t.Errorf("AGENTS.md default surface lists %d names but toolsurface.Default has %d — update AGENTS.md to match the advertised surface",
			len(names), len(toolsurface.Default))
	}
	want := make(map[string]bool, len(toolsurface.Default))
	for _, name := range toolsurface.Default {
		want[name] = true
	}
	for _, name := range names {
		if !want[name] {
			t.Errorf("AGENTS.md default surface lists %q which is not in toolsurface.Default %v — update AGENTS.md to match the advertised surface",
				name, toolsurface.Default)
		}
	}

	// Full-catalog claim: "KERN_MCP_FULL=1 for all 117".
	fm := fullRe.FindStringSubmatch(string(doc))
	if fm == nil {
		t.Fatalf(`AGENTS.md contains no "KERN_MCP_FULL=1 for all N" claim to guard`)
	}
	full, err := strconv.Atoi(fm[1])
	if err != nil {
		t.Fatalf("unparseable full-catalog count %q: %v", fm[1], err)
	}
	if actual := len(mcp.ToolNames()); full != actual {
		t.Errorf("AGENTS.md advertises %d tools under KERN_MCP_FULL=1 but the live MCP catalog has %d — update AGENTS.md to match the catalog",
			full, actual)
	}
}
