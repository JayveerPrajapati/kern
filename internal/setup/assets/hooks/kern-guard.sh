#!/bin/sh
# kern-guard: PreToolUse hook that redirects built-in read/grep/glob/bash to
# kern's MCP equivalents. Installed by `kern setup` for agents that support
# pre-tool blocking hooks (Claude Code, Cursor, Gemini, Copilot, Qwen, Qoder,
# Codex, Antigravity). Disable with KERN_ENFORCE=0 or KERN_BYPASS=1.
#
# Contract: receives JSON on stdin with {"tool_name":"...","tool_input":{...}}
# or Antigravity's {"toolCall":{"name":"..."}}.
# Exit 0 = allow; exit 2 = block (stderr is returned to the agent as the reason).

# Respect the bypass env var.
if [ "$KERN_ENFORCE" = "0" ] || [ "$KERN_BYPASS" = "1" ]; then
  printf '{"decision":"allow"}\n' 2>/dev/null
  exit 0
fi

# Read stdin (the hook payload). Keep it bounded.
payload=$(cat 2>/dev/null | head -c 8192)

# Extract the tool name. Prefer jq if available; fall back to sed.
if command -v jq >/dev/null 2>&1; then
  tool=$(printf '%s' "$payload" | jq -r '.tool_name // .tool // .toolCall.name // empty' 2>/dev/null)
  has_tool_call=$(printf '%s' "$payload" | jq -r '.toolCall.name // empty' 2>/dev/null)
else
  # Crude extraction: find "tool_name":"Read" or "tool":"Read" or "name":"run_command"
  tool=$(printf '%s' "$payload" | sed -n 's/.*"tool[_a-z]*"[[:space:]]*:[[:space:]]*"\([A-Za-z_]*\)".*/\1/p' | head -1)
  if [ -z "$tool" ]; then
    tool=$(printf '%s' "$payload" | sed -n 's/.*"name"[[:space:]]*:[[:space:]]*"\([A-Za-z_]*\)".*/\1/p' | head -1)
  fi
  has_tool_call=$(printf '%s' "$payload" | grep '"toolCall"')
fi

if [ -z "$tool" ]; then
  if [ -n "$has_tool_call" ]; then
    printf '{"decision":"allow"}\n'
  fi
  exit 0
fi

# Map tool -> kern suggestion. Tool names vary across agents (Read vs read_file
# vs read vs view_file), so normalize to lowercase and match prefixes.
case "$(printf '%s' "$tool" | tr '[:upper:]' '[:lower:]')" in
  read|read_file|view_file)
    reason="Use kern_compact_file (symbolic summary, faster) or kern_context (source slice) instead of the built-in read. Call kern_compact_file with {\"path\":\"<filepath>\"}. Set KERN_ENFORCE=0 to disable this guard."
    ;;
  grep|grep_file)
    reason="Use kern_ast_search (code symbols) or kern_doc_search (docs) instead of the built-in grep. kern ast/grep patterns are SYMBOL queries, not regex: call kern_ast_search with a symbol-name pattern like {\"pattern\":\"funcName\"} or {\"pattern\":\"type *Name*\"}. For true regex search use bash: grep -rn <pattern> (or raw=true). Set KERN_ENFORCE=0 to disable this guard."
    ;;
  glob|list|list_files)
    reason="Use kern_project_map (compressed symbol map) instead of the built-in glob. Call kern_project_map with {\"root\":\".\"}. Set KERN_ENFORCE=0 to disable this guard."
    ;;
  bash|run_shell_command|shell|execute_bash|run_command)
    reason="Use kern_validate (build/test/lint) or kern_exec (governed command execution) instead of the built-in bash. Set KERN_ENFORCE=0 to disable this guard."
    ;;
  *)
    # Not a tool we guard — allow.
    if [ -n "$has_tool_call" ]; then
      printf '{"decision":"allow"}\n'
    fi
    exit 0
    ;;
esac

# Block (exit 2) with the reason on stderr. Also output JSON for Antigravity.
if [ -n "$has_tool_call" ]; then
  printf '{"decision":"deny","reason":"%s"}\n' "$reason"
fi
echo "$reason" >&2
exit 2