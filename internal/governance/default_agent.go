package governance

import (
	"os"
	"strings"
)

// DefaultAgentID is the built-in agent identity used to govern calls that
// omit agent_id — the P0.1 default retrieval path, shared by the MCP server
// (internal/mcp/govern.go) and the CLI guard (cmd/kern/cmd_context.go). It is
// registered at init with the minimal context.read permission; path
// confinement comes from the cwd-scoped default task scope.
const DefaultAgentID = "default"

// PermissiveMode reports whether the KERN_MCP_PERMISSIVE escape hatch is set,
// opting out of default governance and restoring raw (ungoverned) mode for
// calls without an agent_id. Explicit opt-in only: anything except "1" or
// "true" keeps default governance on.
func PermissiveMode() bool {
	v := os.Getenv("KERN_MCP_PERMISSIVE")
	return v == "1" || strings.EqualFold(v, "true")
}

// EnsureDefaultAgent registers the built-in default agent identity used for
// governed calls without an explicit agent_id. It is idempotent: re-entry
// after a prior registration (e.g. across tests sharing the in-memory
// registry) is a no-op.
func EnsureDefaultAgent() {
	if _, err := GetAgent(DefaultAgentID); err == nil {
		return
	}
	_ = RegisterAgent(NewAgent(DefaultAgentID, "Default Agent", "default", []Permission{
		{Resource: "context", Action: "read"},
	}))
}
