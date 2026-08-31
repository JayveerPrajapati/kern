package setup

import (
	"path/filepath"
)

// wireCursorHooks registers a preToolUse hook in ~/.cursor/hooks.json (global
// user scope) that blocks built-in Read/Grep/Glob/Bash calls and suggests kern's
// MCP equivalents via the shared kern-guard script (hard block + suggest).
// Cursor's rule files cannot execute commands, so the hook is the only hard
// enforcement Cursor supports. Global scope means hooks fire in EVERY project
// without per-repo setup. Stale kern hooks are replaced on re-run
// (overwrite-always).
func wireCursorHooks() Status {
	guardPath, err := writeGuardScriptGlobal()
	if err != nil {
		return Status{Agent: "cursor-hooks", Installed: false, Path: filepath.Join(globalHomeDir(), ".cursor", "hooks.json"), Note: err.Error()}
	}
	path := filepath.Join(globalHomeDir(), ".cursor", "hooks.json")
	groups := map[string]any{
		"preToolUse": []any{
			map[string]any{
				"matcher": "Read|Grep|Glob|Bash",
				"hooks": []any{
					map[string]any{"type": "command", "command": guardPath},
				},
			},
		},
	}
	if err := mergeHookGroups(path, groups); err != nil {
		return Status{Agent: "cursor-hooks", Path: path, Note: err.Error()}
	}
	return Status{Agent: "cursor-hooks", Installed: true, Path: path, Note: "cursor preToolUse hook registered"}
}
