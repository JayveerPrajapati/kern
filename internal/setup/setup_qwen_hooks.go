package setup

// wireQwenHooks registers Qwen's PreToolUse guard plus its SessionStart
// steering hook in ~/.qwen/settings.json. Qwen Code reads the same
// settings.hooks.<event> shape as Claude Code, so every hook-capable host gets
// the same two things: built-in tools are blocked in favour of kern's MCP
// equivalents, and the session begins with the shared steering brief. Existing
// hooks and the mcpServers key are preserved.
func wireQwenHooks(root string) Status {
	path := homeConfig(".qwen", "settings.json")(root)
	st := wireGuardHook("qwen-hooks", "PreToolUse", "Read|Grep|Glob|Bash", path, "qwen PreToolUse hook registered")
	if !st.Installed {
		return st
	}
	if err := mergeHookGroups(path, map[string]any{
		"SessionStart": []any{
			map[string]any{
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": LaunchCLICommand() + ` hook session-brief "$QWEN_PROJECT_DIR"`,
					},
				},
			},
		},
	}); err != nil {
		return Status{Agent: "qwen-hooks", Path: path, Note: err.Error()}
	}
	return Status{Agent: "qwen-hooks", Installed: true, Path: path, Note: "qwen PreToolUse + SessionStart hooks registered"}
}
