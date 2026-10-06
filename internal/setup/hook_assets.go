package setup

import (
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed assets/hooks/kern-guard.sh
var kernGuardScript string

// writeGuardScriptTo writes the kern-guard hook script to <dir>/kern-guard.sh
// and returns its absolute path. Agents' PreToolUse hooks call this script.
func writeGuardScriptTo(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "kern-guard.sh")
	if err := os.WriteFile(p, []byte(kernGuardScript), 0o755); err != nil {
		return "", err
	}
	return p, nil
}

// writeGuardScriptGlobal writes the kern-guard hook script to
// <home>/.kern/hooks/kern-guard.sh (where <home> is globalHomeDir) and returns
// its absolute path. Home-based agents (Qwen, Qoder, Codex) reference this
// global install from their ~/.<agent> configs so the guard is not tied to any
// single project's path.
func writeGuardScriptGlobal() (string, error) {
	return writeGuardScriptTo(filepath.Join(globalHomeDir(), ".kern", "hooks"))
}

// wireGuardHook registers a single kern-guard PreToolUse hook group at path
// via mergeHookGroups. event is the hooks-object key (agents disagree on
// casing: Claude/Qwen/Qoder/Codex/Continue use "PreToolUse", Cursor/Copilot
// "preToolUse"); matcher selects the built-in tools to gate; note is the
// success Status text.
func wireGuardHook(agent, event, matcher, path, note string) Status {
	guardPath, err := writeGuardScriptGlobal()
	if err != nil {
		return Status{Agent: agent, Installed: false, Path: path, Note: err.Error()}
	}
	groups := map[string]any{
		event: []any{
			map[string]any{
				"matcher": matcher,
				"hooks": []any{
					map[string]any{"type": "command", "command": guardPath},
				},
			},
		},
	}
	if err := mergeHookGroups(path, groups); err != nil {
		return Status{Agent: agent, Path: path, Note: err.Error()}
	}
	return Status{Agent: agent, Installed: true, Path: path, Note: note}
}
