package setup

import (
	"path/filepath"
)

// wireQwenHooks registers a PreToolUse hook in ~/.qwen/settings.json that blocks
// built-in Read/Grep/Glob/Bash calls and suggests kern's MCP equivalents via the
// shared kern-guard script (hard block + suggest). Qwen reads the same
// settings.hooks.<event> shape as Claude Code; existing hooks and the
// mcpServers key are preserved.
func wireQwenHooks(root string) Status {
	guardPath, err := writeGuardScriptGlobal()
	if err != nil {
		return Status{Agent: "qwen-hooks", Installed: false, Path: filepath.Join(homeConfig(".qwen", "settings.json")("")), Note: err.Error()}
	}
	path := homeConfig(".qwen", "settings.json")("")
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
		return Status{Agent: "qwen-hooks", Path: path, Note: err.Error()}
	}
	return Status{Agent: "qwen-hooks", Installed: true, Path: path, Note: "qwen PreToolUse hook registered"}
}
