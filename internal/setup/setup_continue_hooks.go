package setup

import (
	"os"
	"path/filepath"
)

// continueSettingsPath resolves the Continue settings.json path. Continue
// reads global hooks from settings.json (NOT config.json, which only carries
// mcpServers). Resolution order matches the Continue CLI:
//  1. $CONTINUE_GLOBAL_DIR/settings.json when set;
//  2. the directory containing an existing Continue config.json
//     (~/.config/continue/config.json) → ~/.config/continue/settings.json;
//  3. ~/.continue/settings.json.
func continueSettingsPath() string {
	if dir := os.Getenv("CONTINUE_GLOBAL_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json")
	}
	home := globalHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".config", "continue", "config.json")); err == nil {
		return filepath.Join(home, ".config", "continue", "settings.json")
	}
	return filepath.Join(home, ".continue", "settings.json")
}

// wireContinueHooks registers a PreToolUse hook in the Continue settings.json
// that blocks built-in Read/Grep/Glob/Bash calls via the shared kern-guard
// script (hard block + suggest; Continue treats hook exit code 2 as a tool
// block). The hooks shape matches Continue's settings.hooks.<event>[] — the
// same matcher+hooks group shape Claude/Qwen/Qoder use — so mergeHookGroups
// preserves unrelated keys and never duplicates the kern group on re-run.
func wireContinueHooks() Status {
	guardPath, err := writeGuardScriptGlobal()
	if err != nil {
		return Status{Agent: "continue-hooks", Installed: false, Path: continueSettingsPath(), Note: err.Error()}
	}
	path := continueSettingsPath()
	groups := map[string]any{
		"PreToolUse": []any{
			map[string]any{
				"matcher": "Bash|Read|Grep|Glob",
				"hooks": []any{
					map[string]any{"type": "command", "command": guardPath},
				},
			},
		},
	}
	if err := mergeHookGroups(path, groups); err != nil {
		return Status{Agent: "continue-hooks", Path: path, Note: err.Error()}
	}
	return Status{Agent: "continue-hooks", Installed: true, Path: path, Note: "continue PreToolUse hook registered"}
}
