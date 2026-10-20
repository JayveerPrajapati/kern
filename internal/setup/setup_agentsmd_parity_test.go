package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/agentwire"
	"github.com/JayveerPrajapati/kern/internal/mcp/toolsurface"
	mcp "github.com/JayveerPrajapati/kern/internal/mcpcatalog"
)

// setup_agentsmd_parity_test.go — the AGENTS.md rules text (embedded asset
// and the tracked repo-root copy) must advertise exactly the live default
// MCP surface and the live catalog count. Without this gate, a tool removed
// from the catalog can linger in the advertised list (e.g. kern_commitmsg)
// and every installed agent is told to call a tool that does not exist —
// the same drift class TestPluginMatchesMCPCatalog pins for the plugin.

// defaultSurfaceRe extracts the tool list from the "Default surface: N tools
// (`a`, `b`, …)" line of the AGENTS.md rules text.
var defaultSurfaceRe = regexp.MustCompile("Default surface: \\d+ tools \\(`([^)]+)`\\)")

// surfaceCountRe extracts the count literal in the same line.
var surfaceCountRe = regexp.MustCompile("Default surface: (\\d+) tools")

func TestAgentRulesToolListMatchesCatalog(t *testing.T) {
	// The embedded asset is what `kern setup` installs into repos and global
	// slots; the repo-root AGENTS.md is the tracked canonical copy. Both must
	// agree with the live surface, or agents read a stale advertisement.
	sources := map[string][]byte{}
	emb, err := agentwire.RulesFS().ReadFile("assets/AGENTS.md")
	if err != nil {
		t.Fatalf("read embedded AGENTS.md: %v", err)
	}
	sources["embedded"] = emb
	repo, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read repo AGENTS.md: %v", err)
	}
	sources["repo"] = repo

	full := len(mcp.ToolNames())
	for label, content := range sources {
		text := string(content)
		m := defaultSurfaceRe.FindStringSubmatch(text)
		if m == nil {
			t.Fatalf("%s: no \"Default surface: N tools (`...`)\" line", label)
		}
		listed := map[string]bool{}
		// The list wraps across lines ("…`kern_impact`,\n`kern_search`…"), so
		// normalize newlines before splitting on the ", " separator.
		for _, name := range strings.Split(strings.ReplaceAll(m[1], "\n", " "), ", ") {
			listed[strings.Trim(name, "`")] = true
		}
		for _, name := range toolsurface.Default {
			if !listed[name] {
				t.Errorf("%s: AGENTS.md default surface missing %s", label, name)
			}
		}
		for name := range listed {
			found := false
			for _, d := range toolsurface.Default {
				if d == name {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s: AGENTS.md lists %s which is not in toolsurface.Default (stale tool)", label, name)
			}
		}
		// The count literals in the same paragraph must match the live sets:
		// the full catalog size and the default-surface size.
		if !strings.Contains(text, fmt.Sprintf("among %d individual", full)) {
			t.Errorf("%s: AGENTS.md missing \"among %d individual\" count (catalog is %d tools)", label, full, full)
		}
		if cm := surfaceCountRe.FindStringSubmatch(text); cm == nil || cm[1] != fmt.Sprintf("%d", len(toolsurface.Default)) {
			t.Errorf("%s: \"Default surface: N tools\" count = %v, want %d", label, cm, len(toolsurface.Default))
		}
	}
}
