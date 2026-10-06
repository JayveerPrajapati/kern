package setup

import "path/filepath"

// wireCursorHooks registers a preToolUse hook in ~/.cursor/hooks.json (global
// user scope) that blocks built-in Read/Grep/Glob/Bash calls and suggests kern's
// MCP equivalents via the shared kern-guard script (hard block + suggest).
// Cursor's rule files cannot execute commands, so the hook is the only hard
// enforcement Cursor supports. Global scope means hooks fire in EVERY project
// without per-repo setup. Stale kern hooks are replaced on re-run
// (overwrite-always).
func wireCursorHooks() Status {
	return wireGuardHook("cursor-hooks", "preToolUse", "Read|Grep|Glob|Bash",
		filepath.Join(globalHomeDir(), ".cursor", "hooks.json"), "cursor preToolUse hook registered")
}
