package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// wireAntigravityHooks registers kern's PreToolUse hook in ~/.gemini/config/hooks.json
// (global user scope) and optionally in <root>/.agents/hooks.json (if .agents/ exists).
// Antigravity's PreToolUse hook intercepts built-in view_file/run_command calls and
// redirects them to kern's MCP equivalents.
func wireAntigravityHooks() Status {
	guardPath, err := writeGuardScriptGlobal()
	if err != nil {
		return Status{
			Agent:     "antigravity-hooks",
			Installed: false,
			Path:      filepath.Join(globalHomeDir(), ".gemini", "config", "hooks.json"),
			Note:      err.Error(),
		}
	}

	path := filepath.Join(globalHomeDir(), ".gemini", "config", "hooks.json")
	if err := mergeAntigravityHooks(path, guardPath); err != nil {
		return Status{Agent: "antigravity-hooks", Path: path, Note: err.Error()}
	}

	return Status{
		Agent:     "antigravity-hooks",
		Installed: true,
		Path:      path,
		Note:      "antigravity PreToolUse hook registered",
	}
}

// wireProjectAntigravityHooks writes hooks into <root>/.agents/hooks.json if the
// .agents directory exists in the workspace.
func wireProjectAntigravityHooks(root string) Status {
	agentsDir := filepath.Join(root, ".agents")
	if st, err := os.Stat(agentsDir); err != nil || !st.IsDir() {
		return Status{
			Agent:   "antigravity-project-hooks",
			Skipped: true,
			Path:    filepath.Join(agentsDir, "hooks.json"),
			Note:    ".agents directory not present",
		}
	}

	guardPath, err := writeGuardScriptGlobal()
	if err != nil {
		return Status{
			Agent:     "antigravity-project-hooks",
			Installed: false,
			Path:      filepath.Join(agentsDir, "hooks.json"),
			Note:      err.Error(),
		}
	}

	path := filepath.Join(agentsDir, "hooks.json")
	if err := mergeAntigravityHooks(path, guardPath); err != nil {
		return Status{Agent: "antigravity-project-hooks", Path: path, Note: err.Error()}
	}

	return Status{
		Agent:     "antigravity-project-hooks",
		Installed: true,
		Path:      path,
		Note:      "antigravity project PreToolUse hook registered",
	}
}

// mergeAntigravityHooks inserts or updates the "kern-guard" hook in hooks.json.
func mergeAntigravityHooks(path string, guardPath string) error {
	var m map[string]any
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
		m = map[string]any{}
	default:
		return err
	}
	if m == nil {
		m = map[string]any{}
	}

	kernHook := map[string]any{
		"enabled": true,
		"PreToolUse": []any{
			map[string]any{
				"matcher": "run_command|view_file",
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": guardPath,
						"timeout": 15,
					},
				},
			},
		},
	}

	m["kern-guard"] = kernHook

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}
