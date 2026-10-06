package setup

// wireQwenHooks registers a PreToolUse hook in ~/.qwen/settings.json that blocks
// built-in Read/Grep/Glob/Bash calls and suggests kern's MCP equivalents via the
// shared kern-guard script (hard block + suggest). Qwen reads the same
// settings.hooks.<event> shape as Claude Code; existing hooks and the
// mcpServers key are preserved.
func wireQwenHooks(root string) Status {
	return wireGuardHook("qwen-hooks", "PreToolUse", "Read|Grep|Glob|Bash",
		homeConfig(".qwen", "settings.json")(root), "qwen PreToolUse hook registered")
}
