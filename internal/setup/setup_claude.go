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
		return Status{Agent: "claude", Skipped: true, Note: "claude not on PATH — project .mcp.json still covers Claude Code"}
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

// wireClaudeHooks registers kern's PreToolUse, PostToolUse and UserPromptSubmit
// hooks in ~/.claude/settings.json (global user scope). The PreToolUse hook
// blocks built-in Read/Grep/Glob/Bash calls via the kern-guard script (hard
// block + suggest); the PostToolUse hook compresses oversized Bash/Read/Grep
// results and records edits + failures into project memory; the
// UserPromptSubmit hook captures substantive user prompts. Global scope means
// hooks fire in EVERY project without per-repo setup. Existing hooks are
// preserved; stale kern hooks are replaced on re-run (overwrite-always).
func wireClaudeHooks(bin string) Status {
	guardPath, err := writeGuardScriptGlobal()
	if err != nil {
		return Status{Agent: "claude-hooks", Installed: false, Path: filepath.Join(globalHomeDir(), ".claude", "settings.json"), Note: err.Error()}
	}
	path := filepath.Join(globalHomeDir(), ".claude", "settings.json")
	groups := map[string]any{
		"PreToolUse": []any{
			map[string]any{
				"matcher": "Read|Grep|Glob|Bash",
				"hooks": []any{
					map[string]any{"type": "command", "command": guardPath},
				},
			},
		},
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
	return Status{Agent: "claude-hooks", Installed: true, Path: path, Note: "claude PreToolUse/PostToolUse/UserPromptSubmit hooks registered"}
}

// mergeHookGroups merges hook event groups into a settings JSON file (Claude
// Code and Gemini CLI share the same shape: settings.hooks.<event>[]), creating
// the file when absent and always preserving unrelated keys (mcpServers, other
// hooks). Overwrite-always: on every run, any existing kern-owned hook groups
// (identified by hasKernHook) are removed from each event and the fresh kern
// groups re-inserted, so an upgrade that changes the hook command always
// replaces the stale hook rather than skipping when a kern hook is detected.
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
		// Overwrite-always: strip any kern-owned groups first, then append the
		// fresh ones. This replaces stale hooks from a prior setup run instead
		// of skipping when a kern hook is already present.
		filtered := filterKernHooks(existing)
		for _, g := range group.([]any) {
			filtered = append(filtered, g)
		}
		hooks[event] = filtered
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

// filterKernHooks returns groups with any kern-owned hook groups removed. A
// group is kern-owned when any of its hook commands contains " hook " (kern
// hook subcommands) or "kern-guard.sh" (the PreToolUse guard script). Used by
// mergeHookGroups to strip stale kern hooks before re-inserting fresh ones.
func filterKernHooks(groups []any) []any {
	out := make([]any, 0, len(groups))
	for _, g := range groups {
		if !hasKernHook([]any{g}) {
			out = append(out, g)
		}
	}
	return out
}

// hasKernHook reports whether any group in an event's hook groups already
// contains a kern hook command (either a "kern <sub> hook ..." command or the
// kern-guard.sh PreToolUse guard).
func hasKernHook(groups []any) bool {
	for _, g := range groups {
		gm, _ := g.(map[string]any)
		hs, _ := gm["hooks"].([]any)
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			if cmd, _ := hm["command"].(string); strings.Contains(cmd, " hook ") || strings.Contains(cmd, "kern-guard.sh") {
				return true
			}
		}
	}
	return false
}
