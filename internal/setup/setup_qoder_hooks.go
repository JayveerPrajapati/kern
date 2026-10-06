package setup

// wireQoderHooks registers a PreToolUse hook in ~/.qoder/settings.json that
// blocks built-in Read/Grep/Glob/Bash calls and suggests kern's MCP equivalents
// via the shared kern-guard script (hard block + suggest; Qoder treats hook
// exit code 2 as a block). Existing hooks and the mcpServers key are preserved.
func wireQoderHooks(root string) Status {
	return wireGuardHook("qoder-hooks", "PreToolUse", "Read|Grep|Glob|Bash",
		homeConfig(".qoder", "settings.json")(root), "qoder PreToolUse hook registered")
}
