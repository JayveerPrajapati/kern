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

// wireContinueHooks registers Continue's PreToolUse guard plus its SessionStart
// steering hook. Continue treats hook exit code 2 as a tool block; the hooks
// shape matches settings.hooks.<event>[] (the same shape Claude/Qwen/Qoder
// use), so mergeHookGroups preserves unrelated keys and never duplicates the
// kern groups on re-run. Passing bin lets the session hook use the same
// absolute command as every other host.
func wireContinueHooks(bin string) Status {
	path := continueSettingsPath()
	st := wireGuardHook("continue-hooks", "PreToolUse", "Bash|Read|Grep|Glob",
		path, "continue PreToolUse hook registered")
	if !st.Installed {
		return st
	}
	if err := mergeHookGroups(path, map[string]any{
		"SessionStart": []any{
			map[string]any{
				"hooks": []any{
					map[string]any{"type": "command", "command": bin + ` hook session-brief "$CONTINUE_PROJECT_DIR"`},
				},
			},
		},
	}); err != nil {
		return Status{Agent: "continue-hooks", Path: path, Note: err.Error()}
	}
	return Status{Agent: "continue-hooks", Installed: true, Path: path, Note: "continue PreToolUse + SessionStart hooks registered"}
}
