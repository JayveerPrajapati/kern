#!/bin/sh
# kern-guard: PreToolUse hook that redirects built-in read/grep/glob/bash to
# kern's MCP equivalents. Installed by `kern setup` for agents that support
# pre-tool blocking hooks (Claude Code, Cursor, Gemini, Copilot, Qwen, Qoder,
# Codex, Antigravity). opencode cannot consume shell hooks — it gets the same
# routing via the kern plugin's shadow tools (kern.ts), with the same simple
# read/command exemptions. KERN_ENFORCE=0 / KERN_BYPASS=1 are honored ONLY when
# a human confirms at an interactive terminal (/dev/tty); on a piped stdin
# (agent/CI) they are ignored and the guards enforce.
#
# Simple exemptions (honest scoping — kern adds no value there): reads of
# non-code files (markdown/config/data) are allowed through — code files
# (any extension in the code list, ANY size) stay governed; content-free
# git commands (git status, and git log WITHOUT patch-emitting flags
# -p/--patch/-u/--raw; each also valid through `git -C <dir>`),
# read-only kern diagnostics (kern version, kern --version, kern doctor,
# kern health, kern guard [--help|-h], kern mcp tools [list|--json] —
# exact invocation only) and
# exact-token trivial shell (pwd, date, which, echo, ls,
# whoami, true) are allowed through, including '&&'/';' compounds whose
# every segment is one of those (git status && git log).
# Also allowed: wc, git rev-parse, git diff/show restricted to stat/name
# flags, git branch/remote listing, git add (not -f) and git commit (not
# --no-verify). grep, sed, cat, head, tail, git blame, git push and
# patch-emitting git diff/show stay governed. Anything with
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
# ONLY when a human confirms at an interactive terminal.
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
  # lives in the global pre-commit hook's bypass prompt (the semantics of the
  # former humanBypassDecision) — this PreToolUse hook's job is fail-closed
  # gating, not a second interactive prompt.
(exec 3</dev/tty) 2>/dev/null
}
# Operator one-time allow. The CLI (`kern guard allow
# <hash>`, tty-gated so only a human can mint an entry) records an
# allowance for an exact subject string in ~/.kern/guard/allow-once.jsonl;
# the next identical subject on a guarded surface consumes it and passes
# ONCE. Registry misses and any error return 1 (deny as usual) — a failed
# mark must never ALLOW (fail-closed). Consume races are accepted; both
# wins are visible in the audit trail.
hash_subject() {
h=$(printf '%s' "$1" | shasum -a 256 2>/dev/null | cut -d' ' -f1)
[ -n "$h" ] || h=$(printf '%s' "$1" | sha256sum 2>/dev/null | cut -d' ' -f1)
[ -n "$h" ] && printf '%s' "$h"
}
allow_once_consume() {
subject="$1"
[ -n "$subject" ] || return 1
hash=$(hash_subject "$subject")
[ -n "$hash" ] || return 1
reg="${KERN_GUARD_ALLOW_ONCE:-$HOME/.kern/guard/allow-once.jsonl}"
[ -f "$reg" ] || return 1
ln=$(grep -n "$hash" "$reg" 2>/dev/null | grep '"used":""' | head -1 | cut -d: -f1)
[ -n "$ln" ] || return 1
now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
sed -i.bak "${ln}s/\"used\":\"\"/\"used\":\"$now\"/" "$reg" && rm -f "$reg.bak" || return 1
mkdir -p "$HOME/.kern/audit" 2>/dev/null && printf '{"ts":"%s","event":"kern-guard-allow-once-consumed","hash":"%s","pwd":"%s"}\n' "$now" "$hash" "$PWD" >> "$HOME/.kern/audit/bypass.jsonl" 2>/dev/null
return 0
}
if [ "$KERN_ENFORCE" = "0" ] || [ "$KERN_BYPASS" = "1" ]; then
  mode="confirmed"
  confirm_on_tty || mode="refused:no-tty"
  bt=$(printf '%s' "$payload" | sed -n 's/.*"tool_name"[[:space:]]*:[[:space:]]*"\([A-Za-z_]*\)".*/\1/p' | head -1)
  [ -z "$bt" ] && bt=$(printf '%s' "$payload" | sed -n 's/.*"toolCall"[[:space:]]*:[[:space:]]*"\([A-Za-z_]*\)".*/\1/p' | head -1)
  [ -z "$bt" ] && bt=$(printf '%s' "$payload" | sed -n 's/.*"name"[[:space:]]*:[[:space:]]*"\([A-Za-z_]*\)".*/\1/p' | head -1)
  bt=$(printf '%s' "$bt" | tr '[:upper:]' '[:lower:]')
  case "$bt" in
    read|read_file|view_file|grep|grep_file|grep_search|glob|find_by_name|list_files|bash|run_shell_command|shell|execute_bash|run_command)
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

# Fail open when kern is not usable. This hook is installed GLOBALLY, so it
# also fires in projects where kern was never set up and after kern has been
# uninstalled; redirecting built-in read/grep/bash to tools that do not exist
# would leave the agent unable to read code at all. Allowed when: the user or
# repo switched kern off (KERN_DISABLED=1, or a .kern/off file in the project),
# or no kern binary can be found. KERN_GUARD_ASSUME_KERN=1 skips the probe
# (tests). The bypass/allow-once machinery above still applies when kern IS
# usable.
kern_unavailable() {
  [ "${KERN_GUARD_ASSUME_KERN:-0}" = "1" ] && return 1
  [ "${KERN_DISABLED:-0}" = "1" ] && return 0
  [ -f "${CLAUDE_PROJECT_DIR:-$PWD}/.kern/off" ] && return 0
  # kern is mid-way through the first index build of this project: it can only
  # answer "still building" right now. The marker expires after 15 minutes so
  # a crashed build can never leave the guard open for good.
  [ -n "$(find "${CLAUDE_PROJECT_DIR:-$PWD}/.kern/indexing" -mmin -15 2>/dev/null)" ] && return 0
  command -v kern >/dev/null 2>&1 && return 1
  [ -x "${KERN_BIN:-$HOME/.local/bin/kern}" ] && return 1
  return 0
}
if kern_unavailable; then
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
fpath=$(printf '%s' "$payload" | jq -r '.tool_input.file_path // .tool_input.path // .toolCall.args.filePath // .toolCall.args.path // .toolCall.args.AbsolutePath // empty' 2>/dev/null)
else
fpath=$(printf '%s' "$payload" | sed -n 's/.*"file_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
if [ -z "$fpath" ]; then
fpath=$(printf '%s' "$payload" | sed -n 's/.*"AbsolutePath"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
if [ -z "$fpath" ]; then
  fpath=$(printf '%s' "$payload" | sed -n 's/.*"path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
fi
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
# A consumed operator allowance for this exact path unlocks the read
# once; the deny below then names the hash so the operator can mint it.
if [ -n "$fpath" ] && allow_once_consume "$fpath"; then
if [ -n "$has_tool_call" ]; then
printf '{"decision":"allow"}\n'
fi
exit 0
fi
subj_hash=$(hash_subject "$fpath")
reason="Use kern_explore with the file path as symbol (symbolic summary; add tier=full for the verbatim file, or start_line/end_line for a line window) instead of the built-in read; kern_compact_file does the same when KERN_MCP_FULL=1. Call kern_explore with {\"symbol\":\"<filepath>\"}. Set KERN_ENFORCE=0 to disable this guard. Operator allow-once: kern guard allow $subj_hash (tty-gated), then retry."
    ;;
grep|grep_file|grep_search)
# Subject = the pattern string. A consumed operator allowance for
# this exact pattern unlocks the grep once; the deny names the hash.
if command -v jq >/dev/null 2>&1; then
gpat=$(printf '%s' "$payload" | jq -r '.tool_input.pattern // .toolCall.args.pattern // .toolCall.args.Pattern // .input.pattern // empty' 2>/dev/null)
else
gpat=$(printf '%s' "$payload" | sed -n 's/.*"Pattern"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
if [ -z "$gpat" ]; then
  gpat=$(printf '%s' "$payload" | sed -n 's/.*"pattern"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)
fi
fi
if [ -n "$gpat" ] && allow_once_consume "$gpat"; then
if [ -n "$has_tool_call" ]; then
printf '{"decision":"allow"}\n'
fi
exit 0
fi
subj_hash=$(hash_subject "$gpat")
reason="Use kern_search (code symbols — patterns are SYMBOL queries, not regex) instead of the built-in grep: call it with a symbol-name pattern like {\"pattern\":\"funcName\"}. For true regex search run bash: kern ast <pattern> (CLI; wildcards + kind prefixes) — a plain bash grep is denied by the same guard, not an escape (operator break-glass only: host env KERN_BYPASS=1, then grep -rn <pattern> with raw=true). For docs use kern_doc (action=search; KERN_MCP_FULL=1). Set KERN_ENFORCE=0 to disable this guard. Operator allow-once: kern guard allow $subj_hash (tty-gated), then retry."
    ;;
  glob|find_by_name|list_files)
    reason="Use kern_buddy (repo layout + entry points digest; on the default MCP surface) instead of the built-in glob, or kern_project_map (compressed symbol map) when KERN_MCP_FULL=1. To locate a file by name ask kern_meta (request=find file <name>). Escapes: KERN_BYPASS=1 / KERN_ENFORCE=0."
    ;;
bash|run_shell_command|shell|execute_bash|run_command)
cmd=$(printf '%s' "$payload" | jq -r '.tool_input.command // .toolCall.args.command // .toolCall.args.CommandLine // .input.command // empty' 2>/dev/null)
if [ -n "$cmd" ]; then
# Literal newline: $(printf '\n') would strip it via command substitution.
# git_stat_only <diff|show> <args...>: 0 iff the invocation prints only
# statistics or file names — a stat-style flag is present and nothing that
# emits a patch, a word diff, or writes an output file. For `show`, a
# rev:path argument prints a file's content and is refused.
git_stat_only() {
  gso_cmd="$1"
  shift
  gso_stat=1
  for a in "$@"; do
    case "$a" in
    --stat|--stat=*|--shortstat|--numstat|--name-only|--name-status|--summary|--dirstat|--dirstat=*|--no-patch|-s)
      gso_stat=0
      ;;
    -p|--patch|-u|--patch-with-stat|--patch-with-raw|-U*|--unified*|--word-diff*|--color-words*|--output*|--ext-diff|--textconv)
      return 1
      ;;
    -[a-zA-Z]*[pu][a-zA-Z]*)
      return 1
      ;;
    *:*)
      [ "$gso_cmd" = "show" ] && return 1
      ;;
    esac
  done
  return $gso_stat
}
# git_branch_list_only <args...>: 0 iff every argument is a listing flag
# (no positional name, so nothing is created, renamed or deleted).
git_branch_list_only() {
  for a in "$@"; do
    case "$a" in
    --show-current|-a|--all|-r|--remotes|-v|-vv|--verbose|--list) ;;
    *) return 1 ;;
    esac
  done
  return 0
}
# git_remote_read_only <args...>: bare `git remote`, `-v`, or `get-url <name>`.
git_remote_read_only() {
  case "$1" in
  "") return 0 ;;
  -v|--verbose) [ $# -eq 1 ] ;;
  get-url) return 0 ;;
  *) return 1 ;;
  esac
}
# git_add_ok / git_commit_ok: staging and committing are ordinary work, but
# `add --force` defeats .gitignore (secrets) and `commit --no-verify` skips the
# governance pre-commit gate, so those two stay governed.
git_add_ok() {
  for a in "$@"; do
    case "$a" in
    --force) return 1 ;;
    --*) ;;
    -*f*) return 1 ;;
    esac
  done
  return 0
}
git_commit_ok() {
  for a in "$@"; do
    case "$a" in
    --no-verify) return 1 ;;
    --*) ;;
    -*n*) return 1 ;;
    esac
  done
  return 0
}
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
    # `git -C <dir> <subcommand>` runs against another working tree: drop
    # the optional -C <dir> pair (the dir must be a real non-flag token)
    # and classify the subcommand exactly like the in-tree form.
    shift 1 # drop "git"; $1 is now the subcommand, or "-C"
    if [ "$1" = "-C" ]; then
      case "$2" in
      ""|-*)
        # missing or flag-like dir — governed
        return 1
        ;;
      esac
      shift 2 # drop "-C <dir>"; $1 is now the subcommand
    fi
    case "$1" in
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
    rev-parse)
      # prints refs/paths only — allow
      return 0
      ;;
    diff|show)
      sub="$1"
      shift
      git_stat_only "$sub" "$@"
      return $?
      ;;
    branch)
      shift
      git_branch_list_only "$@"
      return $?
      ;;
    remote)
      shift
      git_remote_read_only "$@"
      return $?
      ;;
    add)
      shift
      git_add_ok "$@"
      return $?
      ;;
    commit)
      shift
      git_commit_ok "$@"
      return $?
      ;;
      # blame/push/plain diff-or-show/anything else — governed
    esac
    ;;
  kern)
    case "$2" in
    version|--version|doctor|health)
      # read-only diagnostics — allow, EXACT invocation only: a bare
      # `kern version --json` or any other subcommand stays governed
      [ $# -eq 2 ] && return 0
      ;;
    guard)
      # read-only guard surface: the bare usage/help printers expose no
      # state. `kern guard allow <hash>` MUTATES the allow-once registry
      # and stays governed.
      case "$3" in
      ""|--help|-h)
        [ $# -le 3 ] && return 0
        ;;
      esac
      ;;
    mcp)
      # `kern mcp tools [list|--json]` prints the catalog (read-only);
      # anything else under mcp starts a server or mutates — governed.
      case "$3" in
      tools)
        case "$4" in
        ""|list|--json)
          [ $# -le 4 ] && return 0
          ;;
        esac
        ;;
      esac
      ;;
      # setup/mutate/update/anything else — governed
    esac
    ;;
  pwd|date|which|echo|ls|whoami|true|wc)
    # exact-token trivial shell (wc prints counts, never file content) — allow
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
# The deny message must NAME the blocked command
# (clipped), say WHY it was blocked (not in the passthrough set), and give
# the EXACT replacement tool for this command's first token — all
# replacements are default-surface tools. The parenthetical keeps the
# build/test and unsure anchors so the message stays self-contained.
# (Uniform fail-closed posture: the opencode plugin's bash shadow denies
# content-intent commands with this same text, so the message is
# byte-parity across hosts. On hook hosts the deny additionally covers
# every non-exempt command, since allowing there would mean raw, ungoverned
# execution.)
blocked_cmd=$(printf '%s' "$cmd" | cut -c1-80)
# Braces are REQUIRED: on macOS /bin/sh (bash 3.2) a multibyte char directly
# after an unbraced "$var" in a double-quoted string corrupts the expansion.
[ -n "$(printf '%s' "$cmd" | cut -c81-)" ] && blocked_cmd="${blocked_cmd}…"
# A consumed operator allowance for this exact command unlocks it
# once; the deny below names the hash so the operator can mint it.
if [ -n "$cmd" ] && allow_once_consume "$cmd"; then
if [ -n "$has_tool_call" ]; then
printf '{"decision":"allow"}\n'
fi
exit 0
fi
subj_hash=$(hash_subject "$cmd")
# Per-command replacement map: first token -> exact default-surface
# tool. Env-prefixed / sudo-wrapped / compound commands resolve to their
# first token; the kern_meta default routes anything else.
first_token=$(printf '%s' "$cmd" | awk '{print $1}')
case "$first_token" in
grep|rg|ag) repl="kern_search (query=<symbol>)" ;;
cat|head|tail|sed|awk|less|more) repl="kern_explore (symbol=<file path>, tier=full, start_line/end_line)" ;;
find) repl="kern_buddy" ;;
go|make) repl="kern_verify (command=<cmd>, output=summary)" ;;
*) repl="kern_meta (request=<what you need>)" ;;
esac
reason="kern guard: blocked '$blocked_cmd' — not in the passthrough set; use $repl instead (build/test: kern_verify, unsure: kern_meta). Compounds/pipes: split into single commands — each segment is classified separately. Escapes: KERN_BYPASS=1 / KERN_ENFORCE=0. Operator allow-once: kern guard allow $subj_hash (tty-gated), then retry."
    ;;
  *)
    # Not a tool we guard — allow.
    if [ -n "$has_tool_call" ]; then
      printf '{"decision":"allow"}\n'
    fi
    exit 0
    ;;
esac

# Auto-redirect is the default: instead of denying a
# simple content-intent command, run its kern equivalent and exit 0 with the
# output (audited, event kern-guard-redirect). Only first tokens with a
# faithful kern mapping redirect; commands with shell metacharacters
# (compounds/pipes/expansion/quoting) or an empty subject are ambiguous and
# fall through to the normal deny below. KERN_GUARD_MODE=deny restores the
# uniform deny; an explicit KERN_GUARD_MODE=auto-redirect is honored for
# compatibility.
if [ "$KERN_GUARD_MODE" != "deny" ] && [ -n "$cmd" ]; then
  case "$cmd" in
  *"$nl"*|*\|*|*'&'*|*';'*|*\<*|*\>*|*'$'*|*\`*|*'"'*|*"'"'*|*'\'*)
    # ambiguous compound/expansion/quoting — fall through to the deny below
    ;;
  *)
    kbin="${KERN_BIN:-kern}"
    subj=""
    case "$first_token" in
    grep|rg|ag)
      # subject = pattern = first non-flag arg
      set -f
      for a in $cmd; do
        case "$a" in
        -*) ;;
        *) subj="$a"; break ;;
        esac
      done
      set +f
      if [ -n "$subj" ] && out=$("$kbin" ast "$subj" 2>&1); then
        mkdir -p "$HOME/.kern/audit" 2>/dev/null && printf '{"ts":"%s","event":"kern-guard-redirect","tool":"%s","kern_cmd":"%s","subject":"%s","pwd":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$tool" "ast $subj" "$subj" "$PWD" >> "$HOME/.kern/audit/bypass.jsonl" 2>/dev/null
        printf '%s\n' "$out"
        exit 0
      fi
      ;;
    cat|head|tail|sed|awk|less|more)
      # subject = file path = last non-flag arg
      set -f
      for a in $cmd; do
        case "$a" in
        -*) ;;
        *) subj="$a" ;;
        esac
      done
      set +f
      if [ -n "$subj" ] && out=$("$kbin" compact "$subj" 2>&1); then
        mkdir -p "$HOME/.kern/audit" 2>/dev/null && printf '{"ts":"%s","event":"kern-guard-redirect","tool":"%s","kern_cmd":"%s","subject":"%s","pwd":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$tool" "compact $subj" "$subj" "$PWD" >> "$HOME/.kern/audit/bypass.jsonl" 2>/dev/null
        printf '%s\n' "$out"
        exit 0
      fi
      ;;
    find)
      if out=$("$kbin" buddy 2>&1); then
        mkdir -p "$HOME/.kern/audit" 2>/dev/null && printf '{"ts":"%s","event":"kern-guard-redirect","tool":"%s","kern_cmd":"%s","subject":"","pwd":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$tool" "buddy" "$PWD" >> "$HOME/.kern/audit/bypass.jsonl" 2>/dev/null
        printf '%s\n' "$out"
        exit 0
      fi
      ;;
    go|make)
      if out=$("$kbin" verify --command "$cmd" --output summary 2>&1); then
        mkdir -p "$HOME/.kern/audit" 2>/dev/null && printf '{"ts":"%s","event":"kern-guard-redirect","tool":"%s","kern_cmd":"%s","subject":"%s","pwd":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$tool" "verify --command $cmd --output summary" "$cmd" "$PWD" >> "$HOME/.kern/audit/bypass.jsonl" 2>/dev/null
        printf '%s\n' "$out"
        exit 0
      fi
      ;;
    esac
    ;;
  esac
fi

# Block (exit 2) with the reason on stderr. Also output JSON for Antigravity.
# Every plain denial is audited too (event kern-guard-deny) —
# best-effort, like the bypass audit: a failed append must never change the
# deny. The subject is the denied command for bash, the path for read, the
# pattern for grep (whichever the guard arm set). A bypass-refusal is NOT
# double-audited: it already wrote kern-guard-bypass above, so the deny
# audit only fires when no bypass env was in play.
if [ "$KERN_ENFORCE" != "0" ] && [ "$KERN_BYPASS" != "1" ]; then
deny_subj="${cmd:-${fpath:-${gpat:-}}}"
mkdir -p "$HOME/.kern/audit" 2>/dev/null && printf '{"ts":"%s","event":"kern-guard-deny","tool":"%s","subject":"%s","pwd":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$tool" "$deny_subj" "$PWD" >> "$HOME/.kern/audit/bypass.jsonl" 2>/dev/null
fi
if [ -n "$has_tool_call" ]; then
  printf '{"decision":"deny","reason":"%s"}\n' "$reason"
fi
echo "$reason" >&2
exit 2