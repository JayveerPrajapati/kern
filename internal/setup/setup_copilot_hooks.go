package setup

import "path/filepath"

// wireCopilotHooks registers a preToolUse hook in
// ~/.copilot/hooks/kern-pretooluse.json (global user scope) that blocks
// built-in Read/Grep/Glob/Bash calls and suggests kern's MCP equivalents via
// the shared kern-guard script (hard block + suggest). Global scope means hooks
// fire in EVERY project without per-repo setup. The copilot MCP server config
// (~/.copilot/mcp-config.json, mcpServers key) is wired by the global
// "copilot" adapter in the adapters registry — same gate, so `kern setup
// --agents copilot` writes both hooks and MCP config. Stale kern hooks are
// replaced on re-run (overwrite-always).
func wireCopilotHooks() Status {
	return wireGuardHook("copilot-hooks", "preToolUse", "Read|Grep|Glob|Bash",
		filepath.Join(globalHomeDir(), ".copilot", "hooks", "kern-pretooluse.json"),
		"copilot preToolUse hook registered")
}
