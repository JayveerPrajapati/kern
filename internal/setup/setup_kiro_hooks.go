package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// kiroHooksPath resolves the Kiro global hooks file. Kiro loads global hooks
// from individual JSON files under <KIRO_HOME>/hooks (default ~/.kiro/hooks);
// the kern guard lives in its own kern-owned file so it can be rewritten
// wholesale without touching other hooks.
func kiroHooksPath() string {
	base := os.Getenv("KIRO_HOME")
	if base == "" {
		base = filepath.Join(globalHomeDir(), ".kiro")
	}
	return filepath.Join(base, "hooks", "kern-guard.json")
}

// wireKiroHooks registers two PreToolUse hooks in Kiro's global hooks file
// that block built-in Read/Grep/Glob ("read" matcher) and Bash ("shell"
// matcher) calls via the shared kern-guard script (hard block + suggest; Kiro
// blocks the tool on any non-zero hook exit). The file is kern-owned: it is
// overwritten wholesale when it differs and left untouched when byte-identical,
// so re-running setup is idempotent.
func wireKiroHooks() Status {
	guardPath, err := writeGuardScriptGlobal()
	if err != nil {
		return Status{Agent: "kiro-hooks", Installed: false, Path: kiroHooksPath(), Note: err.Error()}
	}
	path := kiroHooksPath()
	payload := map[string]any{
		"version": "v1",
		"hooks": []any{
			map[string]any{
				"name":    "kern-guard-read",
				"trigger": "PreToolUse",
				"matcher": "read",
				"action":  map[string]any{"type": "command", "command": guardPath},
			},
			map[string]any{
				"name":    "kern-guard-shell",
				"trigger": "PreToolUse",
				"matcher": "shell",
				"action":  map[string]any{"type": "command", "command": guardPath},
			},
		},
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return Status{Agent: "kiro-hooks", Path: path, Note: err.Error()}
	}
	data = append(data, '\n')
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, data) {
		return Status{Agent: "kiro-hooks", Installed: true, Path: path, Note: "kiro PreToolUse hooks already current"}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Status{Agent: "kiro-hooks", Path: path, Note: err.Error()}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return Status{Agent: "kiro-hooks", Path: path, Note: err.Error()}
	}
	return Status{Agent: "kiro-hooks", Installed: true, Path: path, Note: "kiro PreToolUse hooks registered"}
}
