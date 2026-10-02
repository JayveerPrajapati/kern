#!/bin/sh
# kern-guard: PreToolUse hook that redirects built-in read/grep/glob/bash to
# kern's MCP equivalents. Installed by `kern setup` for agents that support
# pre-tool blocking hooks (Claude Code, Cursor, Gemini, Copilot, Qwen, Qoder,
# Codex, Antigravity). opencode cannot consume shell hooks — it gets the same
# routing via the kern plugin's shadow tools (kern.ts), with the same simple
# read/command exemptions. Disable with KERN_ENFORCE=0 or KERN_BYPASS=1.
#
# Simple exemptions (honest scoping — kern adds no value there): reads of
# non-code files (markdown/config/data) are allowed through — code files
# (any extension in the code list, ANY size) stay governed; single
# content-free git commands (git status, and git log WITHOUT patch-emitting flags -p/--patch/-u/--raw) and exact-token trivial shell
# (pwd, date, which, echo, ls, whoami, true) are allowed through.
# git diff/show/blame emit file content and stay governed. Anything with
# shell operators, substitutions, redirects, variable expansions, or a
# newline (multi-line compound) still goes through the governed path.
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
# Extract the file path. Prefer jq if available; fall back to sed so the
# non-code read exemption also works on jq-less hosts.
if command -v jq >/dev/null 2>&1; then
fpath=$(printf '%s' "$payload" | jq -r '.tool_input.file_path // .tool_input.path // .args.filePath // .args.path // empty' 2>/dev/null)
else
fpath=$(printf '%s' "$payload" | sed -n 's/.*"file_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
if [ -z "$fpath" ]; then
fpath=$(printf '%s' "$payload" | sed -n 's/.*"path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
fi
fi
if [ -n "$fpath" ]; then
      case "$fpath" in
*.go|*.ts|*.tsx|*.js|*.jsx|*.mjs|*.cjs|*.py|*.rs|*.java|*.c|*.h|*.cpp|*.hpp|*.cc|*.hh|*.rb|*.sh|*.bash|*.kt|*.kts|*.swift|*.php|*.cs|*.scala|*.lua|*.m|*.mm|*.sql|*.zig|*.ex|*.exs|*.erl|*.hrl|*.hs|*.clj|*.cljs|*.dart|*.vue|*.svelte)
# code file — always governed, regardless of size (part of the symbol
# context; a missing file blocks too — extension-only fail-closed)
;;
*)
# non-code (markdown/config/data/dotfile/extensionless) — allow
exit 0
;;
esac
fi
    reason="Use kern_compact_file (symbolic summary, faster) or kern_context (source slice) instead of the built-in read. Call kern_compact_file with {\"path\":\"<filepath>\"}. Set KERN_ENFORCE=0 to disable this guard."
    ;;
  grep|grep_file|grep_search)
    reason="Use kern_ast_search (code symbols) or kern_doc (action=search) (docs) instead of the built-in grep. kern ast/grep patterns are SYMBOL queries, not regex: call kern_ast_search with a symbol-name pattern like {\"pattern\":\"funcName\"} or {\"pattern\":\"type *Name*\"}. For true regex search use bash: grep -rn <pattern> (or raw=true). Set KERN_ENFORCE=0 to disable this guard."
    ;;
  glob|find_by_name)
    reason="Use kern_project_map (compressed symbol map) instead of the built-in glob. Call kern_project_map with {\"root\":\".\"}. Set KERN_ENFORCE=0 to disable this guard."
    ;;
bash|run_shell_command|shell|execute_bash|run_command)
cmd=$(printf '%s' "$payload" | jq -r '.tool_input.command // .args.command // .input.command // empty' 2>/dev/null)
if [ -n "$cmd" ]; then
# Literal newline: $(printf '\n') would strip it via command substitution.
nl='
'
cr=$(printf '\r')
case "$cmd" in
*\&*|*\|*|*\;*|*\<*|*\>*|*\`*|*\$*|*"$nl"*|*"$cr"*)
# compound / redirected / substituted / expanded / multi-line — governed below
;;
*)
# Single command: exact first-token allowlist match (no prefix
# matching — "pwd123" and "git statusX" are NOT exempt).
set -f
set -- $cmd
set +f
case "$1" in
git)
case "$2" in
status)
# content-free — allow
exit 0
;;
log)
# content-free UNLESS a patch-emitting flag is present
case " $cmd" in
*" -p"*|*" --patch"*|*" -u"*|*" --raw"*)
# patch-emitting — governed below
;;
*)
exit 0
;;
esac
;;
*)
# diff/show/blame/anything else — governed below
;;
esac
;;
pwd|date|which|echo|ls|whoami|true)
# exact-token trivial shell — allow
exit 0
;;
*)
# everything else (builds, tests, installs, ...) — governed below
;;
esac
;;
esac
fi
    reason="Use kern_validate (build/test/lint) or kern_exec (governed command execution) instead of the built-in bash. Set KERN_ENFORCE=0 to disable this guard.\nkern alternative: kern_context/kern_explore/kern_search for symbol context · kern_exec (KERN_MCP_FULL=1) for governed commands"
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