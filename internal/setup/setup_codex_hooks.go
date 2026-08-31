package setup

import (
	"path/filepath"
)

// wireCodexHooks registers a PreToolUse hook in ~/.codex/hooks.json that blocks
// built-in Bash calls and suggests kern's MCP equivalents via the shared
// kern-guard script (hard block + suggest). Codex's hook support is best-effort
// and in practice Bash-only, so the matcher targets Bash alone. The hook only
// fires when the user enables hooks in ~/.codex/config.toml with
// `[features] codex_hooks = true` (this is reported in the Status note, not
// modified — the flag lives in the user's TOML config that wireCodex writes).
func wireCodexHooks(root string) Status {
	guardPath, err := writeGuardScriptGlobal()
	if err != nil {
		return Status{Agent: "codex-hooks", Installed: false, Path: filepath.Join(homeConfig(".codex", "hooks.json")("")), Note: err.Error()}
	}
	path := homeConfig(".codex", "hooks.json")("")
	groups := map[string]any{
		"PreToolUse": []any{
			map[string]any{
				"matcher": "Bash",
				"hooks": []any{
					map[string]any{"type": "command", "command": guardPath},
				},
			},
		},
	}
	if err := mergeHookGroups(path, groups); err != nil {
		return Status{Agent: "codex-hooks", Path: path, Note: err.Error()}
	}
	return Status{Agent: "codex-hooks", Installed: true, Path: path, Note: "codex PreToolUse hook registered — enable with [features] codex_hooks = true in ~/.codex/config.toml"}
}
