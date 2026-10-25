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
# any `kern ...` invocation (the CLI carries its own governance —
# approval workflow, tty-gated allow-once minting, sandboxed exec, update
# provenance guard; the guard's deny messages steer agents TO kern
# commands, so blocking them would be self-defeating) and
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

# R9: audited, duration-bounded repo pause. The registry existence probe keeps
# the no-pause path cheap. This affects ONLY read/grep/glob steering; the
# bash arm below is deliberately never paused and exec governance stays intact.
guard_pause_active() {
  [ -f "$HOME/.kern/guard/pauses.jsonl" ] || return 1
  [ "$(kern guard paused "${CLAUDE_PROJECT_DIR:-$PWD}" 2>/dev/null)" = "active" ]
}

# Map tool -> kern suggestion. Tool names vary across agents (Read vs read_file
# vs read vs view_file), so normalize to lowercase and match prefixes.
case "$(printf '%s' "$tool" | tr '[:upper:]' '[:lower:]')" in
read|read_file|view_file)
if guard_pause_active; then
  if [ -n "$has_tool_call" ]; then
    printf '{"decision":"allow"}\n'
  fi
  exit 0
fi
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
if guard_pause_active; then
  if [ -n "$has_tool_call" ]; then
    printf '{"decision":"allow"}\n'
  fi
  exit 0
fi
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
reason="Use kern_search (code symbols — patterns are SYMBOL queries, not regex) instead of the built-in grep: call it with a symbol-name pattern like {\"pattern\":\"funcName\"}. For true regex search run bash: kern ast <pattern> (CLI; wildcards + kind prefixes) — a plain bash grep is denied by the same guard, not an escape (operator break-glass only: host env KERN_BYPASS=1). For docs use kern_doc (action=search; KERN_MCP_FULL=1). Set KERN_ENFORCE=0 to disable this guard. Operator allow-once: kern guard allow $subj_hash (tty-gated), then retry."
    ;;
  glob|find_by_name|list_files)
    if guard_pause_active; then
      if [ -n "$has_tool_call" ]; then
        printf '{"decision":"allow"}\n'
      fi
      exit 0
    fi
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
    # ANY invocation of the kern CLI runs raw. The CLI carries its own
    # governance (approval workflow for installer-class commands, tty-gated
    # allow-once minting, sandboxed exec, update provenance guard), and
    # this guard's deny messages steer agents TO kern commands — blocking
    # them here would be self-defeating.
    return 0
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

# P2 — risk-axis reclassification (mirror of the plugin's TS classifier in
# kern.ts: READ_CMDS serve-never-deny, GATE_CMDS deny the whole command).
# A command containing ANY gate segment (write/destructive/network/installer)
# is denied; a pure-read pipeline (every segment a read command or a trivial
# content-free command) is SERVED via kern (Tier 1/2/3) — never denied.
# Everything else (builds/tests/installs) stays governed/denied as before.
nl='
'
cr=$(printf '\r')
case "$cmd" in
*"$nl"*|*"$cr"*|*\|*|*\<*|*\>*|*\`*|*\$*)
# multi-line / pipe / redirect / substitution / expansion — classified below
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

# P2 gate/read classification (position-independent, prefix-stripped,
# sh -c-aware — mirrors the plugin's gateSegmentHit). Tokenization is
# word-split (the plugin uses quote-aware shellTokenize; the guard accepts
# the same fidelity exempt_single already uses for classification).
seg_first_name() {
  seg="$1"
  set -f
  set -- $seg
  set +f
  while [ $# -gt 0 ]; do
    case "$1" in
    sudo|doas|env|nice|nohup|time|xargs|watch|strace|ltrace|stdbuf)
      shift
      case "$1" in
      -u|-n|-o|-g|-p|--user|--prompt|--group|--nice-level|--env|--unset|--input|--output) shift 2 ;;
      -*) shift ;;
      *) ;;
      esac
      ;;
    *=*)
      shift  # env assignment (VAR=x cmd)
      ;;
    *) break ;;
    esac
  done
  [ $# -eq 0 ] && return 1
  printf '%s' "${1##*/}"  # basename — /bin/rm, ./rm → rm
}
gate_cmd_name() {  # $1 = command name: flat gate (write/destructive/network)
  case "$1" in
  rm|mv|cp|dd|mkfs|wipefs|diskutil|shred|truncate|chmod|chown|curl|wget|fetch) return 0 ;;
  esac
  return 1
}
installer_cmd_name() {  # $1 = command name
  case "$1" in
  npm|pnpm|yarn|pip|pip3|go|make|apt|apt-get|brew|pacman) return 0 ;;
  esac
  return 1
}
read_cmd_name() {  # $1 = command name
  case "$1" in
  grep|egrep|rg|ag|find|cat|head|tail|less|awk|sed) return 0 ;;
  esac
  return 1
}
# gate_segment <seg>: 0 iff the segment is a gate. A write redirect (>, >>)
# anywhere makes it a gate (a read that writes is a write); flat gate names
# gate unconditionally; installers gate on the install action; git push with
# --force/-f gates.
gate_segment() {
  seg="$1"
  case "$seg" in
  *[\>]*)
    # a write/append redirect — gate (reads never write)
    return 0
    ;;
  esac
  name=$(seg_first_name "$seg") || return 1
  case "$name" in
  sh|bash|zsh|dash|ksh)
    # one level of sh -c "..." recursion
    case "$seg" in
    *\ -c\ *|*\ -c\"*)
      inner=${seg#*-c }
      inner=${inner#\"}
      [ -n "$inner" ] && gate_hit "$inner" && return 0
      ;;
    esac
    ;;
  esac
  gate_cmd_name "$name" && return 0
  if installer_cmd_name "$name"; then
    # npm/pip/go/make/apt install|add|ci|upgrade (pacman: -S/--sync/--upgrade)
    case "$name" in
    pacman)
      case "$seg" in
      *\ -S*|*\ -Syu*|*\ --sync*|*\ --upgrade*) return 0 ;;
      esac
      ;;
    *)
      case "$seg" in
      *\ install*|*\ add*|*\ ci*|*\ upgrade*|*\ get*)
        # go/make gate ONLY on install; the others gate on any install action
        case "$name" in
        go|make) case "$seg" in *\ install*) return 0 ;; esac ;;
        *) return 0 ;;
        esac
        ;;
      esac
      ;;
    esac
  fi
  if [ "$name" = "git" ]; then
    case "$seg" in
    *\ push\ *|*push*)
      case "$seg" in
      *\ --force*|*\ -f*|*\ --force=*) return 0 ;;
      esac
      ;;
    esac
  fi
  return 1
}
# gate_hit <cmd>: 0 iff ANY &&/||/;/|/newline-separated segment is a gate.
# The while runs in a subshell (pipeline); its status — 0 when any
# segment's gate_segment passed, 1 when every segment passed — IS the
# function's verdict. Do not add a trailing return here: it would mask
# the subshell's verdict (same trap compound_read_only documents).
gate_hit() {
  printf '%s\n' "$1" | sed 's/&&/\n/g; s/||/\n/g; s/|/\n/g; s/;/\n/g' | while IFS= read -r seg; do
    seg=$(printf '%s' "$seg" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
    [ -z "$seg" ] && continue
    gate_segment "$seg" && exit 0
  done
  # No trailing return — the pipeline's status is the verdict.
}
# read_companion <seg>: 0 iff the segment may legally co-head a pure-read
# pipeline: a trivial content-free command (echo/ls/wc/...) or a READ-ONLY
# git form (status/log w/o patch/rev-parse/diff|show --stat/branch -a/
# remote) — NEVER a write and NEVER a shell-metachar segment (a `$`, `&`,
# backtick, `<` or `>` in the segment means the plugin would not classify it
# simple, so the pipeline is not pure-read). exempt_single alone is NOT
# enough: it deliberately treats `git add`/`git commit` as content-free (they
# run raw through the simple exemption), but a git add/commit must never ride
# along into a Tier-3 `kern exec --readonly` script (it would mutate the real
# worktree from inside the "read").
read_companion() {
  seg="$1"
  case "$seg" in
  *\$*|*'&'*|*\`*|*'<'*|*'>'*)
    return 1
    ;;
  esac
  exempt_single "$seg" || return 1
  first=$(printf '%s' "$seg" | awk '{print $1}')
  # A kern CLI command runs raw as an exempt single command, but must never
  # ride into a Tier-3 readonly-sandbox serve — pipes containing kern stay
  # governed (split them instead).
  [ "$first" = "kern" ] && return 1
  [ "$first" = "git" ] || return 0
  set -f
  set -- $seg
  set +f
  shift 1 # drop "git"
  if [ "$1" = "-C" ]; then
    shift 2 2>/dev/null || return 1
  fi
  case "$1" in
  add|commit) return 1 ;;
  *) return 0 ;;
  esac
}
# read_only_pipeline <cmd>: 0 iff every segment is a read command or a
# read-only companion (trivial shell / read-only git — a write like git
# add/commit disqualifies the whole pipeline) AND at least one segment is an
# actual READ command (a `pwd || whoami` of trivial-only segments is NOT a
# read pipeline — it is governed like the plugin's readPipeline). Same
# subshell-verdict convention as compound_read_only / gate_hit: no trailing
# return.
read_only_pipeline() {
  # The subshell (pipeline) exits 0 (pure-read with >=1 read segment), 1 (a
  # segment is neither read nor companion — not a read pipeline) or 2 (every
  # segment is a companion but none is an actual read — pwd || whoami is
  # governed, not served). The pipeline's status IS the verdict.
  printf '%s\n' "$1" | sed 's/&&/\n/g; s/||/\n/g; s/|/\n/g; s/;/\n/g' | {
    saw_read=0
    while IFS= read -r seg; do
      seg=$(printf '%s' "$seg" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
      [ -z "$seg" ] && continue
      if read_cmd_name "$(seg_first_name "$seg" 2>/dev/null)"; then
        saw_read=1
        continue
      fi
      read_companion "$seg" || exit 1
    done
    if [ "$saw_read" = "1" ]; then exit 0; else exit 2; fi
  }
  case $? in
  0) return 0 ;;
  *) return 1 ;;
  esac
}
# read_tier <cmd>: echoes 1|2|3 for a pure-read pipeline (0 if not pure-read).
# Tier 1: single translatable read (symbol grep / file read / find).
# Tier 2: read-head + bounded simple tail (grep X | wc -l / head / tail /
#   grep -v). Tier 3: everything else pure-read → kern exec --readonly.
read_tier() {
  cmd="$1"
  if ! read_only_pipeline "$cmd"; then
    printf '0'
    return
  fi
  # Tier 1: exactly one segment, a read command, symbol-shaped grep pattern
  # or a plain file read.
  segs=$(printf '%s\n' "$cmd" | sed 's/&&/\n/g; s/||/\n/g; s/|/\n/g; s/;/\n/g' | sed '/^[[:space:]]*$/d')
  nseg=$(printf '%s\n' "$segs" | wc -l | tr -d ' ')
  if [ "$nseg" -eq 1 ]; then
    seg=$(printf '%s\n' "$segs" | head -1)
    name=$(seg_first_name "$seg")
    case "$name" in
    grep|egrep|rg|ag)
      pat=$(printf '%s' "$seg" | awk '{for(i=2;i<=NF;i++) if($i !~ /^-/) {print $i; exit}}')
      # A scoped grep (explicit file/dir target after the pattern) is
      # Tier 3: the Tier-1 symbol search is repo-wide and ignores the
      # caller's scope — a named file must never be answered with
      # symbols from other files.
      tgt=$(printf '%s' "$seg" | awk '{n=0; f=0; for(i=2;i<=NF;i++){ if($i ~ /^-/) continue; n++; if(n==2){f=1; exit}}} END{print (f?1:0)}')
      case "$pat" in
      ''|*[^A-Za-z0-9_.]*|[-:]) printf '3' ;;  # regex/punctuation → Tier 3
      *) [ "$tgt" = "1" ] && printf '3' || printf '1' ;;
      esac
      return
      ;;
    cat|head|tail|less|more)
      f=$(printf '%s' "$seg" | awk '{for(i=2;i<=NF;i++) if($i !~ /^-/) f=$i} END{print f}')
      [ -n "$f" ] && printf '1' || printf '3'
      return
      ;;
    find) printf '1' ;;
    *) printf '3' ;;
    esac
    return
  fi
  # Tier 2: head is a symbol-shaped grep-family read; each tail is wc -l /
  # head N / tail N / grep -v P / grep P.
  headseg=$(printf '%s\n' "$segs" | head -1)
  hname=$(seg_first_name "$headseg")
  case "$hname" in
  grep|egrep|rg|ag)
    hpat=$(printf '%s' "$headseg" | awk '{for(i=2;i<=NF;i++) if($i !~ /^-/) {print $i; exit}}')
    # Same fidelity rule as Tier 1: a scoped grep head never gets the
    # repo-wide symbol answer, tails or not.
    htgt=$(printf '%s' "$headseg" | awk '{n=0; f=0; for(i=2;i<=NF;i++){ if($i ~ /^-/) continue; n++; if(n==2){f=1; exit}}} END{print (f?1:0)}')
    case "$hpat" in
    ''|*[^A-Za-z0-9_.]*|[-:]) printf '3' ;;
    *)
      # Validate every tail in the parent (not a subshell): collect the tails
      # into a single string and check each — the pipeline would lose the
      # result (the while runs in a subshell), so the tail verdict must be
      # computed here, in the caller's scope.
      tailok=1
      tails=$(printf '%s\n' "$segs" | tail -n +2)
      for t in $(printf '%s\n' "$tails" | tr '\n' ';' | sed 's/;;*/;/g'); do
        case "$t" in
        wc\ *\ -l*|wc\ -l*) ;;
        head\ *|tail\ *|grep\ -v\ *|grep\ *) ;;
        *) tailok=0 ;;
        esac
      done
      [ "$tailok" = "1" ] && [ "$htgt" != "1" ] && printf '2' || printf '3'
      ;;
    esac
    ;;
  *) printf '3' ;;
  esac
}
fi
# The deny message must NAME the blocked command
# (clipped), say WHY it was blocked (not in the passthrough set), and give
# the EXACT replacement tool for this command's first token — all
# replacements are default-surface tools. The parenthetical keeps the
# build/test and unsure anchors so the message stays self-contained.
# (Uniform fail-closed posture: the opencode plugin's bash shadow denies
# GATE commands with this same text, so the message is
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
# R1: the bash risk-axis verdict comes from the Go source of truth,
# `kern guard classify --json`. The command is passed as ONE argument so flags
# inside it are never consumed by the guard CLI. A missing/stale classifier,
# invalid JSON, or an unknown verdict falls back to GOVERNED (never allow).
kbin="${KERN_BIN:-kern}"
class_json=""
class_verdict="governed"
class_tier=0
class_head=""
class_pattern=""
class_file=""
class_hash=""
if [ -n "$cmd" ]; then
  class_json=$("$kbin" guard classify --json "$cmd" 2>/dev/null) || class_json=""
fi
if [ -n "$class_json" ] && command -v jq >/dev/null 2>&1; then
  class_verdict=$(printf '%s' "$class_json" | jq -r '.verdict // "governed"' 2>/dev/null)
  class_tier=$(printf '%s' "$class_json" | jq -r '.tier // 0' 2>/dev/null)
  class_head=$(printf '%s' "$class_json" | jq -r '.head // ""' 2>/dev/null)
  class_pattern=$(printf '%s' "$class_json" | jq -r '.pattern // ""' 2>/dev/null)
  class_file=$(printf '%s' "$class_json" | jq -r '.file // ""' 2>/dev/null)
  class_hash=$(printf '%s' "$class_json" | jq -r '.hash // ""' 2>/dev/null)
fi
case "$class_verdict" in
allow|gate|serve|governed) ;;
*) class_verdict="governed" ;;
esac
[ -n "$class_hash" ] && subj_hash="$class_hash"
gate_denied=0
[ "$class_verdict" = "gate" ] && gate_denied=1
# GATE commands are denied; simple content-free commands are allowed; pure
# reads are served. KERN_GUARD_MODE=deny restores the uniform read deny.
# Ask Protocol (P1): an active grant (single-use approved record or live
# prefix grant) covering this command lets it through — same one-question
# store the exec gate uses, consulted via the Go source of truth so the
# script never re-implements matching. The grant is CONSUMED by the check
# exactly like an execution would.
if [ "$gate_denied" = "1" ] && [ -n "$cmd" ]; then
  if "$kbin" guard grant --check --root "${CLAUDE_PROJECT_DIR:-$PWD}" "$cmd" >/dev/null 2>&1; then
    mkdir -p "$HOME/.kern/audit" 2>/dev/null && printf '{"ts":"%s","event":"kern-guard-gate-grant-use","tool":"%s","hash":"%s","pwd":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$tool" "$(hash_subject "$cmd")" "$PWD" >> "$HOME/.kern/audit/bypass.jsonl" 2>/dev/null
    exit 0
  fi
fi
if [ "$gate_denied" = "1" ]; then
  reason="kern guard: blocked '$blocked_cmd' — GATE command (write/destructive/network/installer). Ask the operator, then retry byte-identical: run \`kern guard grant '<the exact blocked command>'\` (mints approval appr-<id>), the operator answers \`kern approval ask appr-<id> --root ${CLAUDE_PROJECT_DIR:-$PWD}\` with [1] accept once / [2] accept prefix this session / [3] reject, await the decision with \`kern approval wait appr-<id> --timeout 30m\`, then retry the exact command. Escapes: KERN_BYPASS=1 / KERN_ENFORCE=0. Operator allow-once: kern guard allow $subj_hash (tty-gated), then retry."
elif [ "$class_verdict" = "allow" ]; then
  if [ -n "$has_tool_call" ]; then
    printf '{"decision":"allow"}\n'
  fi
  exit 0
elif [ "$KERN_GUARD_MODE" != "deny" ] && [ "$class_verdict" = "serve" ] && [ -n "$cmd" ]; then
  # The bash-steering pause is the operator's relief valve for a misbehaving
  # serve (stale index, tier bug): serve-class commands run RAW. GATE
  # verdicts, exec governance and the ask-protocol are never paused.
  if [ "$(kern guard paused "${CLAUDE_PROJECT_DIR:-$PWD}" bash-steering 2>/dev/null)" = "active" ]; then
    if [ -n "$has_tool_call" ]; then
      printf '{"decision":"allow"}\n'
    fi
    exit 0
  fi
  tier="$class_tier"
case "$tier" in
1)
  name="$class_head"
  case "$name" in
  find)
    if out=$("$kbin" buddy 2>&1); then
      mkdir -p "$HOME/.kern/audit" 2>/dev/null && printf '{"ts":"%s","event":"kern-guard-redirect","tool":"%s","kern_cmd":"%s","subject":"","pwd":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$tool" "buddy" "$PWD" >> "$HOME/.kern/audit/bypass.jsonl" 2>/dev/null
      printf '%s\n' "$out"
      exit 0
    fi
    ;;
  grep|egrep|rg|ag)
    pat="$class_pattern"
    if [ -n "$pat" ] && out=$("$kbin" ast "$pat" 2>&1); then
      mkdir -p "$HOME/.kern/audit" 2>/dev/null && printf '{"ts":"%s","event":"kern-guard-redirect","tool":"%s","kern_cmd":"%s","subject":"%s","pwd":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$tool" "ast $pat" "$pat" "$PWD" >> "$HOME/.kern/audit/bypass.jsonl" 2>/dev/null
      printf '%s\n' "$out"
      exit 0
    fi
    ;;
  cat|head|tail|less|more)
    f="$class_file"
    if [ -n "$f" ] && out=$("$kbin" compact "$f" 2>&1); then
      mkdir -p "$HOME/.kern/audit" 2>/dev/null && printf '{"ts":"%s","event":"kern-guard-redirect","tool":"%s","kern_cmd":"%s","subject":"%s","pwd":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$tool" "compact $f" "$f" "$PWD" >> "$HOME/.kern/audit/bypass.jsonl" 2>/dev/null
      printf '%s\n' "$out"
      exit 0
    fi
    ;;
  esac
  ;;
2)
  hpat="$class_pattern"
  if [ -n "$hpat" ] && headout=$("$kbin" ast "$hpat" 2>&1); then
    # Apply the simple tail in-process (bounded: wc -l / head N / tail N /
    # grep -v P / grep P) — the COMPUTED answer is the result.
    tailpart=$(printf '%s' "$cmd" | sed 's/.*|//')
    case "$tailpart" in
    wc\ *\ -l*|wc\ -l*) result=$(printf '%s\n' "$headout" | grep -c . ) ;;
    head\ *)
      hnum=$(printf '%s' "$tailpart" | awk '{for(i=1;i<=NF;i++) if($i ~ /^[0-9]+$/) {print $i; exit}}')
      [ -z "$hnum" ] && hnum=10
      result=$(printf '%s\n' "$headout" | head -n "$hnum")
      ;;
    tail\ *)
      tnum=$(printf '%s' "$tailpart" | awk '{for(i=1;i<=NF;i++) if($i ~ /^[0-9]+$/) {print $i; exit}}')
      [ -z "$tnum" ] && tnum=10
      result=$(printf '%s\n' "$headout" | tail -n "$tnum")
      ;;
    grep\ -v\ *)
      gp=$(printf '%s' "$tailpart" | awk '{print $NF}')
      result=$(printf '%s\n' "$headout" | grep -v "$gp")
      ;;
    grep\ *)
      gp=$(printf '%s' "$tailpart" | awk '{print $NF}')
      result=$(printf '%s\n' "$headout" | grep "$gp")
      ;;
    *) result=$(printf '%s\n' "$headout") ;;
    esac
    mkdir -p "$HOME/.kern/audit" 2>/dev/null && printf '{"ts":"%s","event":"kern-guard-redirect","tool":"%s","kern_cmd":"%s","subject":"%s","pwd":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$tool" "ast $hpat | tail" "$hpat" "$PWD" >> "$HOME/.kern/audit/bypass.jsonl" 2>/dev/null
    printf '%s\n' "$result"
    exit 0
  fi
  ;;
3)
  # Tier 3: untranslatable pure read → kern exec --readonly (sandboxed,
  # PII-masked). The readonly path skips the KERN_TOOLS/KERN_ALLOW_EXEC gate
  # but keeps the risk firewall + rejection stickiness (P3b). Serve on exit
  # 0 (success) or 1 (a no-match read — grep found nothing, still an answer).
  # A GOVERNANCE denial (the readonly firewall refused the read) falls
  # through to the governed deny below; any other failure (stale kern
  # binary, missing index) is a READ — fail-open serve raw with a note
  # (P4: reads serve raw when kern cannot; only writes/exec stay
  # fail-closed).
  tier3_out=$(cd "${CLAUDE_PROJECT_DIR:-$PWD}" 2>/dev/null && "$kbin" exec --readonly --lang sh "cd '${CLAUDE_PROJECT_DIR:-$PWD}' && $cmd" 2>&1; printf '\nKERN_T3_EXIT=%s' "$?")
  # The marker is the LAST line (a leading \n guards against a missing
  # trailing newline in the kern output); the code is the marker's suffix,
  # the body is everything before the marker line.
  tier3_code=$(printf '%s\n' "$tier3_out" | tail -1 | sed 's/.*KERN_T3_EXIT=//')
  tier3_body=$(printf '%s\n' "$tier3_out" | sed '$d')
  if [ "$tier3_code" = "0" ] || [ "$tier3_code" = "1" ]; then
    mkdir -p "$HOME/.kern/audit" 2>/dev/null && printf '{"ts":"%s","event":"kern-guard-redirect","tool":"%s","kern_cmd":"%s","subject":"","pwd":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$tool" "exec --readonly" "$PWD" >> "$HOME/.kern/audit/bypass.jsonl" 2>/dev/null
    printf '%s\n' "$tier3_body"
    exit 0
  fi
  # Governance denial (exit 3 / a denied-by-governance message) — the read
  # firewall refused: fall through to the deny below (fail-closed).
  case "$tier3_body" in
  *"denied by governance"*|*"blocked by governance"*|*"approval required"*)
    ;;
  *)
    # Any other failure is a read that kern cannot serve — serve raw with a
    # note (P4 fail-open reads). The raw run is the command itself (a pure
    # read, no write/network/destructive surface).
    mkdir -p "$HOME/.kern/audit" 2>/dev/null && printf '{"ts":"%s","event":"kern-guard-redirect","tool":"%s","kern_cmd":"%s","subject":"","pwd":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$tool" "exec --readonly (fallback raw)" "$PWD" >> "$HOME/.kern/audit/bypass.jsonl" 2>/dev/null
    printf '%s\n' "$cmd"
    printf '%s\n' "[kern] index unavailable/stale — raw read served (pure-read pipeline)"
    exit 0
    ;;
  esac
  ;;
esac
fi
# Per-command replacement map: first token -> exact default-surface
# tool. Env-prefixed / sudo-wrapped / compound commands resolve to their
# first token; the kern_meta default routes anything else. Only reached
# when the command is NOT a gate (the GATE reason above already won) and
# was NOT served (the serve branches above already exited 0).
if [ "$gate_denied" != "1" ]; then
first_token=$(printf '%s' "$cmd" | awk '{print $1}')
case "$first_token" in
grep|rg|ag) repl="kern_search (query=<symbol>)" ;;
cat|head|tail|sed|awk|less|more) repl="kern_explore (symbol=<file path>, tier=full, start_line/end_line)" ;;
find) repl="kern_buddy" ;;
go|make) repl="kern_verify (command=<cmd>, output=summary)" ;;
*) repl="kern_meta (request=<what you need>)" ;;
esac
reason="kern guard: blocked '$blocked_cmd' — not in the passthrough set; use $repl instead (build/test: kern_verify, unsure: kern_meta). To RUN it governed (verification + audit), re-run as: kern build '<the exact command>' (build/test loops: kern verify --command '<the exact command>' --output summary). Compounds/pipes: split into single commands — each segment is classified separately. Escapes: KERN_BYPASS=1 / KERN_ENFORCE=0. Operator allow-once: kern guard allow $subj_hash (tty-gated), then retry."
fi
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
if [ "$KERN_GUARD_MODE" != "deny" ] && [ -n "$cmd" ] && [ "$gate_denied" != "1" ]; then
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