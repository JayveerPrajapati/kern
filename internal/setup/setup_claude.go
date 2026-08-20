package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func wireClaude(bin string) Status {
	path, err := exec.LookPath("claude")
	if err != nil {
		return Status{Agent: "claude", Note: "claude not on PATH — project .mcp.json still covers Claude Code"}
	}
	cmd := exec.Command(path, "mcp", "add", "kern", "--", bin)
	if out, err := cmd.CombinedOutput(); err != nil {
		return Status{Agent: "claude", Path: path, Note: fmt.Sprintf("claude mcp add failed: %s", strings.TrimSpace(string(out)))}
	}
	return Status{Agent: "claude", Installed: true, Path: path, Note: "claude mcp add ok"}
}

func claudeStatus() Status {
	path, err := exec.LookPath("claude")
	if err != nil {
		return Status{Agent: "claude", Note: "not installed"}
	}
	if b, err := os.ReadFile(claudeConfigPath()); err == nil && strings.Contains(string(b), `"kern"`) {
		return Status{Agent: "claude", Installed: true, Path: path, Note: "kern MCP registered (project or user scope)"}
	}
	return Status{Agent: "claude", Path: path, Note: "claude available — run kern setup to add kern MCP"}
}

// hookCommand builds the shell command for a kern hook event. Agent hook
// shells expand the per-agent project-dir env var ($CLAUDE_PROJECT_DIR /
// $GEMINI_PROJECT_DIR), keeping the command free of machine-specific paths.
func hookCommand(bin, sub string) string {
	return bin + ` hook ` + sub + ` "$CLAUDE_PROJECT_DIR"`
}

// wireClaudeHooks registers kern's PostToolUse and UserPromptSubmit hooks in
// .claude/settings.json. The PostToolUse hook compresses oversized Bash/Read/
// Grep results and records edits + failures into project memory; the
// UserPromptSubmit hook captures substantive user prompts. Existing hooks are
// preserved; the kern group is merged in only when absent.
func wireClaudeHooks(root, bin string) Status {
	path := filepath.Join(root, ".claude", "settings.json")
	groups := map[string]any{
		"PostToolUse": []any{
			map[string]any{
				"matcher": "Edit|Write|Bash|Read|Grep",
				"hooks": []any{
					map[string]any{"type": "command", "command": hookCommand(bin, "claude-post")},
				},
			},
		},
		"UserPromptSubmit": []any{
			map[string]any{
				"hooks": []any{
					map[string]any{"type": "command", "command": hookCommand(bin, "claude-prompt")},
				},
			},
		},
	}
	if err := mergeHookGroups(path, groups); err != nil {
		return Status{Agent: "claude-hooks", Path: path, Note: err.Error()}
	}
	return Status{Agent: "claude-hooks", Installed: true, Path: path, Note: "claude PostToolUse/UserPromptSubmit hooks registered"}
}

// mergeHookGroups merges hook event groups into a settings JSON file (Claude
// Code and Gemini CLI share the same shape: settings.hooks.<event>[]), creating
// the file when absent and always preserving unrelated keys (mcpServers, other
// hooks). A group already containing a "kern hook" command is left untouched,
// so re-running setup never duplicates hooks.
func mergeHookGroups(path string, groups map[string]any) error {
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
	hooks, _ := m["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	changed := false
	for event, group := range groups {
		existing, _ := hooks[event].([]any)
		if hasKernHook(existing) {
			continue
		}
		// groups values are the matcher-group arrays themselves; append each
		// group so the JSON shape stays hooks.<event>[] — not doubly nested.
		for _, g := range group.([]any) {
			existing = append(existing, g)
		}
		hooks[event] = existing
		changed = true
	}
	if !changed {
		return nil
	}
	m["hooks"] = hooks
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err = json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// hasKernHook reports whether any group in an event's hook groups already
// contains a kern hook command.
func hasKernHook(groups []any) bool {
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		hs, _ := gm["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); strings.Contains(cmd, " hook ") {
				return true
			}
		}
	}
	return false
}
