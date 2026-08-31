package setup

import (
	"path/filepath"
)

// wireCopilotHooks registers a preToolUse hook in
// ~/.copilot/hooks/kern-pretooluse.json (global user scope) that blocks
// built-in Read/Grep/Glob/Bash calls and suggests kern's MCP equivalents via
// the shared kern-guard script (hard block + suggest). Global scope means hooks
// fire in EVERY project without per-repo setup. Stale kern hooks are replaced
// on re-run (overwrite-always).
func wireCopilotHooks() Status {
	guardPath, err := writeGuardScriptGlobal()
	if err != nil {
		return Status{Agent: "copilot-hooks", Installed: false, Path: filepath.Join(globalHomeDir(), ".copilot", "hooks", "kern-pretooluse.json"), Note: err.Error()}
	}
	path := filepath.Join(globalHomeDir(), ".copilot", "hooks", "kern-pretooluse.json")
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
		return Status{Agent: "copilot-hooks", Path: path, Note: err.Error()}
	}
	return Status{Agent: "copilot-hooks", Installed: true, Path: path, Note: "copilot preToolUse hook registered"}
}
