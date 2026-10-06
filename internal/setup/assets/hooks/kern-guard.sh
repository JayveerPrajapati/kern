#!/bin/sh
# kern-guard: PreToolUse hook that redirects built-in read/grep/glob/bash to
# kern's MCP equivalents. Installed by `kern setup` for agents that support
# pre-tool blocking hooks (Claude Code, Cursor, Gemini, Copilot, Qwen, Qoder,
# Codex, Antigravity). opencode cannot consume shell hooks — it gets the same
# routing via the kern plugin's shadow tools (kern.ts), with the same simple
# read/command exemptions. KERN_ENFORCE=0 / KERN_BYPASS=1 are honored ONLY when
# a human confirms at an interactive terminal (/dev/tty); on a piped stdin
# (agent/CI) they are ignored and the guards enforce (audit-table-2 B).
#
# Simple exemptions (honest scoping — kern adds no value there): reads of
# non-code files (markdown/config/data) are allowed through — code files
# (any extension in the code list, ANY size) stay governed; content-free
# git commands (git status, and git log WITHOUT patch-emitting flags
# -p/--patch/-u/--raw), read-only kern diagnostics (kern version, kern
# --version, kern doctor, kern health — exact invocation only) and
# exact-token trivial shell (pwd, date, which, echo, ls,
# whoami, true) are allowed through, including '&&'/';' compounds whose
# every segment is one of those (git status && git log).
# git diff/show/blame emit file content and stay governed. Anything with
# pipes, substitutions, redirects, variable expansions, a lone '&', or a
# newline (multi-line compound) still goes through the governed path.
#
# Contract: receives JSON on stdin with {"tool_name":"...","tool_input":{...}}
# or Antigravity's {"toolCall":{"name":"..."}}.
# Exit 0 = allow; exit 2 = block (stderr is returned to the agent as the reason).

# Read stdin (the hook payload) once, before the bypass check — both the
# bypass branch and the governed path need it, and reading it twice would
# starve the second reader.
payload=$(cat 2>/dev/null | head -c 8192)

# Respect the bypass env vars — audited, never silent: a bypass is honored
# ONLY when a human confirms at an interactive terminal (audit-table-2 B).
# Without a terminal (piped stdin from an agent/CI), the env vars are ignored
# and the governed path below still gates — the refusal is audited so kern
# doctor can report bypass attempts. The confirmation is read from /dev/tty,
# NOT stdin: stdin carries the hook payload. The audit record (mirroring the
# global pre-commit hook's convention) names the bypassed tool and the
# confirmation mode; only guarded tool families are recorded, a failed append
# must never break the bypass, stdout stays the allow decision, and stderr
# stays clean.
confirm_on_tty() {
  # Presence probe only. The hook's stdin carries the JSON payload, so the
  # human-presence signal is the controlling terminal: if /dev/tty cannot be
  # opened there is no human to confirm (agent daemon / CI / container without
  # a tty) and the bypass is REFUSED — fail-closed. A typed confirmation is
  # deliberately NOT attempted here: reading /dev/tty can hang indefinitely in
  # sandboxed/containerized hook hosts where /dev/tty is a placeholder char
  # device with no reader (verified empirically: read -t and signal-based
  # timeouts do not fire on such devices). The authoritative typed human gate
  # lives in `kern check` (humanBypassDecision), which the global pre-commit
  # hook invokes — this PreToolUse hook's job is fail-closed gating, not a
  # second interactive prompt.
  (exec 3</dev/tty) 2>/dev/null
}

if [ "$KERN_ENFORCE" = "0" ] || [ "$KERN_BYPASS" = "1" ]; then
  mode="confirmed"
  confirm_on_tty || mode="refused:no-tty"
  bt=$(printf '%s' "$payload" | sed -n 's/.*"tool[_a-z]*"[[:space:]]*:[[:space:]]*"\([A-Za-z_]*\)".*/\1/p' | head -1)
  [ -z "$bt" ] && bt=$(printf '%s' "$payload" | sed -n 's/.*"name"[[:space:]]*:[[:space:]]*"\([A-Za-z_]*\)".*/\1/p' | head -1)
  bt=$(printf '%s' "$bt" | tr '[:upper:]' '[:lower:]')
  case "$bt" in
    read|read_file|view_file|grep|grep_file|grep_search|glob|find_by_name|bash|run_shell_command|shell|execute_bash|run_command)
      mkdir -p "$HOME/.kern/audit" 2>/dev/null && printf '{"ts":"%s","event":"kern-guard-bypass","tool":"%s","reason":"%s","mode":"%s","pwd":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$bt" "${KERN_BYPASS_REASON:-unset}" "$mode" "$PWD" >> "$HOME/.kern/audit/bypass.jsonl" 2>/dev/null
      ;;
  esac
  if [ "$mode" = "confirmed" ]; then
    printf '{"decision":"allow"}\n' 2>/dev/null
    exit 0
  fi
  # Refused (no human at a terminal): fall through to the governed path — the
  # env vars are ignored and the guarded tools still block.
fi

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
    reason="Use kern_explore with the file path as symbol (symbolic summary; add tier=full for the verbatim file, or start_line/end_line for a line window) instead of the built-in read; kern_compact_file does the same when KERN_MCP_FULL=1. Call kern_explore with {\"symbol\":\"<filepath>\"}. Set KERN_ENFORCE=0 to disable this guard."
    ;;
  grep|grep_file|grep_search)
    reason="Use kern_search (code symbols — patterns are SYMBOL queries, not regex) instead of the built-in grep: call it with a symbol-name pattern like {\"pattern\":\"funcName\"}. For true regex search use bash: grep -rn <pattern> (or raw=true). For docs use kern_doc (action=search; KERN_MCP_FULL=1). Set KERN_ENFORCE=0 to disable this guard."
    ;;
  glob|find_by_name)
    reason="Use kern_project_map (compressed symbol map) instead of the built-in glob. Call kern_project_map with {\"root\":\".\"}. Set KERN_ENFORCE=0 to disable this guard."
    ;;
bash|run_shell_command|shell|execute_bash|run_command)
cmd=$(printf '%s' "$payload" | jq -r '.tool_input.command // .args.command // .input.command // empty' 2>/dev/null)
if [ -n "$cmd" ]; then
# Literal newline: $(printf '\n') would strip it via command substitution.
# exempt_single <cmd>: 0 iff cmd is ONE allowlisted content-free command
# (exact first token — no prefix matching, "pwd123"/"git statusX" fail).
# git status passes with any args; git log passes unless a patch-emitting
# flag (-p/--patch/-u/--raw) is present; trivial shell tokens pass.
exempt_single() {
  full="$1"
  set -f
  set -- $full
  set +f
  case "$1" in
  git)
    case "$2" in
    status)
      # content-free — allow
      return 0
      ;;
    log)
      # content-free UNLESS a patch-emitting flag is present
      case " $full" in
      *" -p"*|*" --patch"*|*" -u"*|*" --raw"*)
        # patch-emitting — governed
        return 1
        ;;
      *)
        return 0
        ;;
      esac
      ;;
      # diff/show/blame/anything else — governed
    esac
    ;;
  kern)
    case "$2" in
    version|--version|doctor|health)
      # read-only diagnostics — allow, EXACT invocation only: a bare
      # `kern version --json` or any other subcommand stays governed
      [ $# -eq 2 ] && return 0
      ;;
      # setup/mutate/update/anything else — governed
    esac
    ;;
  pwd|date|which|echo|ls|whoami|true)
    # exact-token trivial shell — allow
    return 0
    ;;
  esac
  return 1
}

# compound_read_only <cmd>: 0 iff every '&&'-/';'-separated segment of cmd
# is an exempt_single command (segments trimmed; empty segments skipped).
# Callers have already governed pipes, redirects, substitutions, '$'
# expansions and newlines, so only '&&' and ';' can appear here. Any
# remaining '&' after '&&' splitting is a lone background '&' — governed.
compound_read_only() {
  printf '%s\n' "$1" | sed 's/&&/\n/g; s/;/\n/g' | while IFS= read -r seg; do
    seg=$(printf '%s' "$seg" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    [ -z "$seg" ] && continue
    case "$seg" in
    *'&'*)
      return 1
      ;;
    esac
    exempt_single "$seg" || return 1
  done
  # The while runs in a subshell (pipeline); its status — 0 when every
  # segment passed, 1 when any segment's exempt_single failed — IS the
  # function's verdict. Do not add a trailing return here: it would mask
  # the subshell's failure.
}

nl='
'
cr=$(printf '\r')
case "$cmd" in
*"$nl"*|*"$cr"*|*\|*|*\<*|*\>*|*\`*|*\$*)
# multi-line / pipe / redirect / substitution / expansion — governed below
;;
*)
# '&&'- or ';'-joined compounds whose every segment is an exempt
# content-free command are content-free too (git status && git log).
# A lone '&' (backgrounding, e.g. "git status & git diff") is NOT a
# compound separator we trust — it stays governed.
if compound_read_only "$cmd"; then
exit 0
fi
# otherwise governed below
;;
esac
fi
    reason="Use kern_verify instead of the built-in bash — it is the sanctioned path for go build / go vet / go test. Run your own build/test/lint command with kern_verify {\"command\":\"go test ./...\",\"output\":\"summary\"} (output: summary|failures|tail:N|lines:A-B|full; the full run is kept, so re-slice it with {\"anchor\":\"<id>\",\"output\":\"lines:A-B\"}), or call it with no arguments for build+test+security in one verdict. For governed one-off commands use kern_exec (KERN_MCP_FULL=1). Set KERN_ENFORCE=0 to disable this guard.\nkern alternative: kern_context/kern_explore/kern_search for symbol context · content-free compounds like 'git status && git log' are allowed through"
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