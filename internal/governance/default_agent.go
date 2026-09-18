package governance

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// DefaultAgentID is the built-in agent identity used to govern calls that
// omit agent_id — the default retrieval path, shared by the MCP server
// (internal/mcp/govern.go) and the CLI guard (cmd/kern/cmd_context.go). It is
// registered at init with the minimal context.read permission; path
// confinement comes from the cwd-scoped default task scope.
const DefaultAgentID = "default"

// permissiveWarning is the one-time stderr warning emitted the first time
// permissive mode is observed active, so an operator who left
// KERN_MCP_PERMISSIVE set (or a deployment that sets it) is reminded that
// governance enforcement is disabled. It is a package-level var so tests can
// assert the exact text.
const permissiveWarning = "WARNING: KERN_MCP_PERMISSIVE=1 — governance enforcement disabled"

// permissiveWarnOnce guarantees the permissive-mode warning fires at most
// once per process, even though every governed call re-reads the mode.
var permissiveWarnOnce sync.Once

// emitPermissiveWarning writes the one-time permissive-mode warning to stderr.
// The warning is best-effort: writing to stderr cannot fail the caller.
func emitPermissiveWarning() {
	permissiveWarnOnce.Do(func() {
		fmt.Fprintln(os.Stderr, permissiveWarning)
	})
}

// PermissiveMode reports whether the KERN_MCP_PERMISSIVE escape hatch is set,
// opting out of default governance and restoring raw (ungoverned) mode for
// calls without an agent_id. Explicit opt-in only: anything except "1" or
// "true" keeps default governance on.
// The first time permissive mode is observed active, a one-time warning is
// emitted to stderr (audit A1: permissive must be loud, not silent).
func PermissiveMode() bool {
	v := os.Getenv("KERN_MCP_PERMISSIVE")
	active := v == "1" || strings.EqualFold(v, "true")
	if active {
		emitPermissiveWarning()
	}
	return active
}

// PermissiveActive reports whether permissive mode is ACTIVE right now — the
// same state PermissiveMode reports, under a status-query name. Governance
// status surfaces (health/status output, diagnostics, tests) should call this
// so the active state is explicit, and every activation path also fires the
// one-time warning. An audit-chain entry on activation is intentionally not
// written here: the mode getter has no project root to write under, and a
// write side effect from an env-var read would be untraceable; the warning
// plus this status flag is the documented surface.
func PermissiveActive() bool {
	return PermissiveMode()
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
