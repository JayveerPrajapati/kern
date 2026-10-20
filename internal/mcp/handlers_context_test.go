package mcp

import (
	"strings"
	"testing"

	mcpcontext "github.com/JayveerPrajapati/kern/internal/mcp/context"
	"github.com/JayveerPrajapati/kern/internal/memory"
)

// TestHandleBuddyNoMemory pins kern_buddy's no_memory param end-to-end
// through the MCP handler: with no_memory the digest omits both memory
// sections; without it, the seeded lesson renders in "Project memory".
func TestHandleBuddyNoMemory(t *testing.T) {
	root := fixtureRoot(t)
	t.Cleanup(func() { mcpcontext.BuddyWarmWait() })
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if _, err := memory.AddExplicit(root, "always run tests before committing"); err != nil {
		t.Fatal(err)
	}

	withMem := mcpAssertOK(t, "kern_buddy", map[string]any{"root": root})
	if !strings.Contains(withMem, "## Project memory") || !strings.Contains(withMem, "always run tests before committing") {
		t.Fatalf("buddy digest must render Project memory:\n%s", withMem)
	}

	noMem := mcpAssertOK(t, "kern_buddy", map[string]any{"root": root, "no_memory": true})
	if strings.Contains(noMem, "## Project memory") || strings.Contains(noMem, "## Recent session activity") {
		t.Fatalf("no_memory buddy digest must omit both memory sections:\n%s", noMem)
	}
}
