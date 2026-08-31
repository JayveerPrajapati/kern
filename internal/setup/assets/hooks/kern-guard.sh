#!/bin/sh
# kern-guard: PreToolUse hook that redirects built-in read/grep/glob/bash to
# kern's MCP equivalents. Installed by `kern setup` for agents that support
# pre-tool blocking hooks (Claude Code, Cursor, Gemini, Copilot, Qwen, Qoder,
# Codex). Disable with KERN_ENFORCE=0.
#
# Contract: receives JSON on stdin with {"tool_name":"...","tool_input":{...}}.
# Exit 0 = allow; exit 2 = block (stderr is returned to the agent as the reason).

# Respect the bypass env var.
if [ "$KERN_ENFORCE" = "0" ]; then
  exit 0
fi

# Read stdin (the hook payload). Some agents send it on stdin; keep it bounded.
payload=$(cat 2>/dev/null | head -c 4096)

# Extract the tool name. Prefer jq if available; fall back to a grep.
if command -v jq >/dev/null 2>&1; then
  tool=$(printf '%s' "$payload" | jq -r '.tool_name // .tool // empty' 2>/dev/null)
else
  # Crude extraction: find "tool_name":"Read" or "tool":"Read" etc.
  tool=$(printf '%s' "$payload" | sed -n 's/.*"tool[_a-z]*"[[:space:]]*:[[:space:]]*"\([A-Za-z_]*\)".*/\1/p' | head -1)
fi

# Map tool -> kern suggestion. Tool names vary across agents (Read vs read_file
# vs read), so normalize to lowercase and match prefixes.
case "$(printf '%s' "$tool" | tr '[:upper:]' '[:lower:]')" in
  read|read_file)
    reason="Use kern_compact_file (symbolic summary, faster) or kern_context (source slice) instead of the built-in read. Call kern_compact_file with {\"path\":\"<filepath>\"}. Set KERN_ENFORCE=0 to disable this guard."
    ;;
  grep|grep_file)
    reason="Use kern_ast_search (code symbols) or kern_doc_search (docs) instead of the built-in grep. Call kern_ast_search with {\"pattern\":\"<regex>\"}. Set KERN_ENFORCE=0 to disable this guard."
    ;;
  glob|list|list_files)
    reason="Use kern_project_map (compressed symbol map) instead of the built-in glob. Call kern_project_map with {\"root\":\".\"}. Set KERN_ENFORCE=0 to disable this guard."
    ;;
  bash|run_shell_command|shell|execute_bash)
    reason="Use kern_run_build (build/test/lint) or kern_exec (governed command execution) instead of the built-in bash. Set KERN_ENFORCE=0 to disable this guard."
    ;;
  *)
    # Not a tool we guard — allow.
    exit 0
    ;;
esac

# Block (exit 2) with the reason on stderr. The agent receives this and can
# retry with the suggested kern tool.
echo "$reason" >&2
exit 2