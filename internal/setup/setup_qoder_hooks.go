package setup

import (
	"path/filepath"
)

// wireQoderHooks registers a PreToolUse hook in ~/.qoder/settings.json that
// blocks built-in Read/Grep/Glob/Bash calls and suggests kern's MCP equivalents
// via the shared kern-guard script (hard block + suggest; Qoder treats hook
// exit code 2 as a block). Existing hooks and the mcpServers key are preserved.
func wireQoderHooks(root string) Status {
	guardPath, err := writeGuardScriptGlobal()
	if err != nil {
		return Status{Agent: "qoder-hooks", Installed: false, Path: filepath.Join(homeConfig(".qoder", "settings.json")("")), Note: err.Error()}
	}
	path := homeConfig(".qoder", "settings.json")("")
	groups := map[string]any{
		"PreToolUse": []any{
			map[string]any{
				"matcher": "Read|Grep|Glob|Bash",
				"hooks": []any{
					map[string]any{"type": "command", "command": guardPath},
				},
			},
		},
	}
	if err := mergeHookGroups(path, groups); err != nil {
		return Status{Agent: "qoder-hooks", Path: path, Note: err.Error()}
	}
	return Status{Agent: "qoder-hooks", Installed: true, Path: path, Note: "qoder PreToolUse hook registered"}
}
