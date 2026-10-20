package setup

// wireCodexHooks registers a PreToolUse hook in ~/.codex/hooks.json that blocks
// built-in Read/Grep/Glob/Bash calls and suggests kern's MCP equivalents via the
// shared kern-guard script (hard block + suggest). The matcher covers the same
// four guarded families as every other host wiring (Claude, Cursor, Qwen,
// Qoder, Copilot, Continue); Codex accepts |-separated matchers exactly like
// the other hosts. The hook only fires when the user enables hooks in
// ~/.codex/config.toml with `[features] codex_hooks = true` (this is reported
// in the Status note, not modified — the flag lives in the user's TOML config
// that wireCodex writes).
func wireCodexHooks(root string) Status {
	return wireGuardHook("codex-hooks", "PreToolUse", "Read|Grep|Glob|Bash",
		homeConfig(".codex", "hooks.json")(root),
		"codex PreToolUse hook registered — enable with [features] codex_hooks = true in ~/.codex/config.toml")
}
