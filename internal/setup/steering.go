package setup

// SteeringBrief is the single source of truth for the session-start steering
// text. Every host gets the same content through the channel it supports:
//   - hosts with a SessionStart hook (Claude Code, Qwen Code, Continue) run
//     `kern hook session-brief` and inject this text into the session context;
//   - opencode injects its envelope from the plugin;
//   - hosts with no session-hook API (Cursor, Copilot, Windsurf, Zed,
//     Antigravity, Codex) receive the same policy through the managed rules
//     block written by `kern setup --global-rules` plus the MCP
//     initialize.instructions field, which every MCP client receives on connect.
//
// Keep it short: it is paid for on every session, and tool descriptions carry
// the per-tool detail.
func SteeringBrief(root string) string {
	if root == "" {
		root = "."
	}
	return "kern is wired for this session (" + root + "). Use kern_* tools BEFORE built-in read/grep/glob/bash:\n" +
		"- how/where is X -> kern_explore, then kern_search\n" +
		"- before editing -> kern_impact (a type aggregates its methods)\n" +
		"- build / test / lint -> kern_verify\n" +
		"- unfamiliar repo -> kern_buddy\n" +
		"- natural language -> kern_meta\n" +
		"Fall back to a built-in only if the kern tool errors."
}
