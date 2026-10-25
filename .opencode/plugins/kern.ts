import { type Plugin, tool } from "@opencode-ai/plugin"
import { resolve, relative } from "node:path"
import { appendFileSync, existsSync, mkdirSync } from "node:fs"
import { writeFile, rm, readFile, readdir, stat } from "node:fs/promises"
import { homedir, tmpdir } from "node:os"
import { createHash, randomBytes } from "node:crypto"
import { AsyncLocalStorage } from "node:async_hooks"

// Resolve the kern binary. Prefer an explicit env var, then the project's
// bin/ directory, then PATH.
function kernBin(directory: string): string {
  if (process.env.KERN_BIN) return process.env.KERN_BIN
  const local = resolve(directory, "bin/kern")
  if (existsSync(local)) return local
  return "kern"
}

// Pass large text to the CLI through a unique temp file (avoids arg-length
// limits and races between concurrent tool calls) and always clean it up,
// even when the CLI fails.
async function withTempFile<T>(name: string, content: string, fn: (file: string) => Promise<T>): Promise<T> {
  const file = resolve(tmpdir(), `kern-${name}-${process.pid}-${randomBytes(4).toString("hex")}`)
  try {
    await writeFile(file, content)
    return await fn(file)
  } finally {
    await rm(file, { force: true })
  }
}

const DEFAULT_COMPACT_THRESHOLD = 4000

// F11: the "[kern] compressed" banner is a savings claim — emit it only when
// compression genuinely shrank the text; equal or inflated output ships bare
// so a zero/negative "saving" is never reported as if compression happened.
function compressedFooter(textLen: number, compressedLen: number): string {
  return compressedLen >= textLen
    ? ""
    : `[kern] compressed ${textLen} -> ${compressedLen} chars\n`
}

// --- Session event capture (P0-2, borrowed from mksglu/context-mode) ---
// Record what the session did (file edits, failing commands) into project
// memory so that after a context compaction — or in a fresh session — the
// agent can recall its own recent state via kern buddy / kern_memory (action=list).

const EDIT_TOOLS = new Set(["edit", "write", "patch", "apply_patch", "update_file"])
// The FAIL_RE marks command output that indicates a real failure. It only
// matches error markers at line start (or specific failure phrases) so a
// successful command that merely mentions "error" isn't mislogged.
const FAIL_RE = /^\s*(?:error|fatal|panic|failed|cannot)[:;]|panic:|command not found|: No such file|no such file or directory/i

// Rate-limit and dedupe chat.message memory capture so project memory isn't
// flooded by ordinary conversation (evicting real lessons).
let lastChatAt = 0
let lastChatText = ""

// --- Exploration capture (context-reuse WRITE end) ---
// Read-only kern tools (kern_explore/kern_search/kern_context/kern_buddy)
// are what a lane actually learned from. Recording their successful calls
// as low-volume "Explored <target> → <top result>" rows lets the read end
// (brief.go's "Recent session activity" section) show what was already
// explored so a later session or subagent lane does not re-derive it.
// Records are deduped per target within the session, capped, byte-bounded,
// and skipped whenever the call failed or matched nothing.
const EXPLORE_TOOLS = new Set(["kern_explore", "kern_search", "kern_context", "kern_buddy"])
const EXPLORE_SESSION_MAX = 20 // exploration records per session
const EXPLORE_RECORD_MAX = 200 // hard byte bound per record
const exploredTargets = new Set() // session-level dedupe by symbol/query
let exploredCount = 0 // session cap counter

// exploreTarget returns the symbol/query a read-only kern tool call was
// about: kern_search's query, kern_buddy's root (or "session digest"), and
// the symbol argument for the rest. Empty when the call had no usable
// target.
function exploreTarget(tool, args) {
  if (typeof args !== "object" || args === null) return ""
  if (tool === "kern_search") return typeof args.query === "string" ? args.query.trim() : ""
  if (tool === "kern_buddy") {
    return typeof args.root === "string" && args.root.trim() ? args.root.trim() : "session digest"
  }
  return typeof args.symbol === "string" ? args.symbol.trim() : ""
}

// topResultRef extracts the top result reference from a successful
// read-only kern tool output: the evidence footer (explore/context) first,
// then the first path:line mention (search results, callers/callees), then
// the buddy digest's "Project: <root>" line. Empty when the output holds no
// location at all — a no-match or failed call, which the caller skips.
function topResultRef(text) {
  if (!text) return ""
  const ev = /(?:^|\n)evidence: (\S+?):(\d+)/.exec(text)
  if (ev) return `${ev[1]}:${ev[2]}`
  const m = /([A-Za-z0-9_@./-]+\.[A-Za-z0-9]+):(\d+)/.exec(text)
  if (m) return `${m[1]}:${m[2]}`
  const pr = /^Project: (\S+)/m.exec(text)
  return pr ? pr[1] : ""
}

// rememberExploration records one successful read-only kern tool call via
// the plugin's remember() (kern remember → memory store, Source "auto").
// Hosts that prefix the MCP server name register the tools twice
// (kern_kern_explore); normalize to the canonical name. Best-effort like
// every capture: failures are swallowed by the caller.
async function rememberExploration(tool, args, text, remember) {
  const t = tool.replace(/^kern_kern_/, "kern_")
  if (!EXPLORE_TOOLS.has(t)) return
  if (!text || /^error/i.test(text.trim())) return
  const target = exploreTarget(t, args)
  if (!target) return
  const ref = topResultRef(text)
  if (!ref) return
  if (exploredTargets.has(target)) return
  if (exploredCount >= EXPLORE_SESSION_MAX) return
  exploredTargets.add(target)
  exploredCount++
  let rec = `Explored ${target} → ${ref}`
  if (Buffer.byteLength(rec, "utf8") >= EXPLORE_RECORD_MAX) {
    rec = rec.slice(0, EXPLORE_RECORD_MAX - 4)
    while (rec.length > 1 && Buffer.byteLength(rec + "…", "utf8") >= EXPLORE_RECORD_MAX) rec = rec.slice(0, -1)
    rec = rec.trimEnd() + "…"
  }
  await remember(rec, EXPLORE_RECORD_MAX)
}
// --- End exploration capture ---

function fileArg(args: any): string {
  if (typeof args !== "object" || args === null) return ""
  for (const k of ["file_path", "filePath", "path", "file"]) {
    if (typeof args[k] === "string" && args[k].trim() !== "") return args[k]
  }
  return ""
}

// --- Shadow built-in fallbacks (raw behavior) ---
// The opencode ToolContext exposes no way to re-invoke the tool being
// shadowed (there is no context.tool), so each shadow tool reimplements the
// built-in's behavior with node:fs / the host `$` shell. These run ONLY when
// kern is unavailable, so a missing binary can never block the agent.

// Directories never worth walking in a fallback glob/grep.
const SKIP_DIRS = new Set([".git", "node_modules", ".kern", "dist", "build", "vendor", "target", ".cache", ".next", ".turbo"])

// Translate one glob segment (*, ?, {a,b}) into an anchored regex.
function segmentRe(seg: string): RegExp {
  let src = ""
  let brace = 0
  for (let i = 0; i < seg.length; i++) {
    const c = seg[i]
    if (c === "{") { brace++; src += "(?:" }
    else if (c === "}") { brace--; src += ")" }
    else if (c === "," && brace > 0) { src += "|" }
    else if (c === "*") { src += "[^/]*" }
    else if (c === "?") { src += "[^/]" }
    else { src += c.replace(/[.+^$()|[\]\\]/g, "\\$&") }
  }
  return new RegExp("^" + src + "$")
}

// Minimal recursive glob: supports *, **, ? and {a,b}. Used only as the
// fallback when the kern binary is unavailable, so it never needs to be
// exhaustive — but it must not hang, so heavy/generated directories are
// skipped.
async function globFallback(pattern: string, base: string): Promise<string> {
  const segments = pattern.replace(/\\/g, "/").split("/").filter((s) => s !== "" && s !== ".")
  if (segments.length === 0) return "error: empty glob pattern"
  const firstWild = segments.findIndex((s) => /[*?{]/.test(s))
  if (firstWild === -1) {
    const full = resolve(base, ...segments)
    try { await stat(full); return full } catch { return `error: no files matched pattern ${pattern}` }
  }
  const root = resolve(base, ...segments.slice(0, firstWild))
  const rest = segments.slice(firstWild)
  const out: string[] = []
  const walk = async (dir: string, segs: string[]): Promise<void> => {
    const [seg, ...tail] = segs
    if (seg === undefined) { out.push(dir); return }
    let entries: Awaited<ReturnType<typeof readdir>>
    try { entries = await readdir(dir, { withFileTypes: true }) } catch { return }
    if (seg === "**") {
      // Match zero or more directory levels, then descend keeping `**`.
      await walk(dir, tail)
      for (const e of entries) {
        if (e.isDirectory() && !SKIP_DIRS.has(e.name)) await walk(resolve(dir, e.name), segs)
      }
      return
    }
    const re = segmentRe(seg)
    for (const e of entries) {
      if (!re.test(e.name)) continue
      const child = resolve(dir, e.name)
      if (tail.length === 0) {
        if (e.isFile() || e.isDirectory()) out.push(child)
      } else if (e.isDirectory()) {
        await walk(child, tail)
      }
    }
  }
  await walk(root, rest)
  if (out.length === 0) return `error: no files matched pattern ${pattern}`
  return out.sort().join("\n")
}

// Raw regex search over files under a directory, mirroring the built-in grep
// (path:line: content output). Only used when kern is unavailable.
async function grepFallback(pattern: string, path: string | undefined, include: string | undefined): Promise<string> {
  let re: RegExp
  try { re = new RegExp(pattern) } catch { return `error: invalid regex: ${pattern}` }
  const incRe = include ? segmentRe(include) : null
  const root = resolve(path ?? ".")
  const out: string[] = []
  const scan = async (file: string): Promise<void> => {
    if (incRe) {
      const name = file.split("/").pop() ?? file
      const rel = relative(root, file)
      if (!(incRe.test(name) || incRe.test(rel))) return
    }
    let text: string
    try { text = await readFile(file, "utf8") } catch { return } // binary/unreadable: skip
    const lines = text.split("\n")
    for (let i = 0; i < lines.length; i++) {
      if (re.test(lines[i])) {
        out.push(`${file}:${i + 1}: ${lines[i].trim()}`)
        if (out.length >= 200) return
      }
    }
  }
  const walk = async (dir: string): Promise<void> => {
    let entries: Awaited<ReturnType<typeof readdir>>
    try { entries = await readdir(dir, { withFileTypes: true }) } catch { return }
    for (const e of entries) {
      if (out.length >= 200) return
      const full = resolve(dir, e.name)
      if (e.isDirectory()) {
        if (!SKIP_DIRS.has(e.name)) await walk(full)
      } else if (e.isFile()) {
        await scan(full)
      }
    }
  }
  await walk(root)
  if (out.length === 0) return "No files found"
  return out.join("\n")
}

// Operator one-time allow, mirroring the shell guard's
// allow_once_consume. The CLI (`kern guard allow <hash>`, tty-gated so only
// a human can mint an entry) records the allowance in
// ~/.kern/guard/allow-once.jsonl; the next identical subject on a shadow
// deny surface consumes it and passes once. Misses and any error return
// false (deny as usual) — a failed mark must never ALLOW (fail-closed).
const guardAllowOncePath = (): string =>
  process.env.KERN_GUARD_ALLOW_ONCE ?? resolve(homedir(), ".kern/guard/allow-once.jsonl")
const hashSubject = (s: string): string => createHash("sha256").update(s).digest("hex")
const consumeAllowOnce = async (subject: string): Promise<boolean> => {
  if (!subject) return false
  const h = hashSubject(subject)
  const p = guardAllowOncePath()
  let text: string
  try { text = await readFile(p, "utf8") } catch { return false }
  const lines = text.split("\n")
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    if (!line.includes(h) || !line.includes('"used":""')) continue
    lines[i] = line.replace('"used":""', `"used":"${new Date().toISOString()}"`)
    try { await writeFile(p, lines.join("\n"), "utf8") } catch { return false }
    return true
  }
  return false
}

// Code extensions eligible for kern routing (symbolic compaction). Everything
// else — markdown, config, data, dotfiles, extensionless files — reads raw:
// `kern compact` returns empty for non-code files anyway.
const CODE_EXTENSIONS = new Set([
  "go", "ts", "tsx", "js", "jsx", "mjs", "cjs", "py", "rs", "java", "c", "h",
  "cpp", "hpp", "cc", "hh", "rb", "sh", "bash", "kt", "kts", "swift", "php",
  "cs", "scala", "lua", "m", "mm", "sql", "zig", "ex", "exs", "erl", "hrl",
  "hs", "clj", "cljs", "dart", "vue", "svelte",
])

// isSimpleRead reports whether a read needs no kern routing: only non-code
// files (kern compact returns empty for them anyway) serve raw. Code files
// are ALWAYS routed to kern regardless of size — even a one-line source file
// is part of the symbol context and can be served/grepped via kern's AST
// tools. The stat below is an existence probe only: a missing or unreadable
// code file fails closed (governed) instead of serving raw.
async function isSimpleRead(filePath: string): Promise<boolean> {
const dot = filePath.lastIndexOf(".")
const sep = Math.max(filePath.lastIndexOf("/"), filePath.lastIndexOf("\\"))
const ext = dot > sep ? filePath.slice(dot + 1).toLowerCase() : ""
if (!CODE_EXTENSIONS.has(ext)) return true
try {
await stat(filePath) // existence probe — unreadable/missing stays governed
} catch {
// missing/unreadable: still governed — never raw.
}
return false
}

// Content-free git subcommands, read-only kern diagnostics, and trivial
// commands exempt from governed bash routing: no build/test/PII surface, and
// the governed wrapper adds latency without value. Only SINGLE commands
// qualify — any shell operator, substitution, redirection, variable
// expansion ($), or newline (multi-line compound) means the full governed
// path. git diff/show/blame are NOT exempt: they emit file content, so they
// stay governed like any other code read.
const TRIVIAL_COMMANDS = new Set(["pwd", "date", "whoami", "true", "echo", "ls", "which", "wc"])
const READONLY_GIT = new Set(["status", "log", "rev-parse", "diff", "show", "branch", "remote", "add", "commit"])
const GIT_STAT_FLAGS = new Set(["--stat", "--shortstat", "--numstat", "--name-only", "--name-status", "--summary", "--dirstat", "--no-patch", "-s"])
const GIT_BRANCH_LIST_FLAGS = new Set(["--show-current", "-a", "--all", "-r", "--remotes", "-v", "-vv", "--verbose", "--list"])
// git diff/show qualify only when they print statistics or file names: a
// stat-style flag is present, nothing emits a patch or writes a file, and
// (for show) no rev:path argument prints file content.
function isStatOnlyGit(sub: string, args: string[]): boolean {
let stat = false
for (const arg of args) {
if (GIT_STAT_FLAGS.has(arg) || arg.startsWith("--stat=") || arg.startsWith("--dirstat=")) { stat = true; continue }
if (arg === "-p" || arg === "--patch" || arg === "-u" || arg === "--patch-with-stat" || arg === "--patch-with-raw" ||
arg.startsWith("-U") || arg.startsWith("--unified") || arg.startsWith("--word-diff") || arg.startsWith("--color-words") ||
arg.startsWith("--output") || arg === "--ext-diff" || arg === "--textconv" ||
(/^-[a-zA-Z]+$/.test(arg) && (arg.includes("p") || arg.includes("u")))) return false
if (sub === "show" && arg.includes(":")) return false
}
return stat
}
// Flags that make `git log` emit file content (patch bodies / raw diffs).
const GIT_LOG_PATCH_FLAGS = ["-p", "--patch", "-u", "--raw"]
function isSimpleCommand(cmd: string): boolean {
if (/[;&|<>`$\n\r]/.test(cmd)) return false
const trimmed = cmd.trim()
const parts = trimmed.split(/\s+/)
if (parts.length === 0) return false
// ANY invocation of the kern CLI runs raw. The CLI carries its own
// governance (approval workflow for installer-class commands, tty-gated
// allow-once minting, sandboxed exec, update provenance guard), and the
// guard's deny messages steer agents TO kern commands — blocking them
// would be self-defeating.
if (parts[0] === "kern") return true
if (parts[0] === "git") {
// `git -C <dir> <sub>` operates on another working tree: skip the -C
// <dir> pair (dir must be a real non-flag token) and classify the
// subcommand exactly like the in-tree form.
let i = 1
if (parts[i] === "-C") {
if (parts.length < i + 2 || !parts[i + 1] || /^-/.test(parts[i + 1])) return false
i += 2
}
if (parts.length < i + 1 || !READONLY_GIT.has(parts[i])) return false
const rest = parts.slice(i + 1)
if (parts[i] === "diff" || parts[i] === "show") return isStatOnlyGit(parts[i], rest)
if (parts[i] === "branch") return rest.every((a) => GIT_BRANCH_LIST_FLAGS.has(a))
if (parts[i] === "remote") {
if (rest.length === 0) return true
if (rest.length === 1 && (rest[0] === "-v" || rest[0] === "--verbose")) return true
return rest[0] === "get-url"
}
if (parts[i] === "add") return !rest.some((a) => a === "-f" || a === "--force" || /^-[a-zA-Z]*f[a-zA-Z]*$/.test(a))
if (parts[i] === "commit") return !rest.some((a) => a === "--no-verify" || /^-[a-zA-Z]*n[a-zA-Z]*$/.test(a))
if (parts[i] === "log") {
for (const arg of rest) {
// exact patch flags, --patch* long forms, and single-dash letter
// bundles containing p/u (e.g. -pu) all emit file content
if (arg === "-p" || arg === "--patch" || arg === "-u" || arg === "--raw" ||
arg.startsWith("--patch") || (/^-[a-z]+$/.test(arg) && (arg.includes("p") || arg.includes("u")))) {
return false
}
}
}
return true
}
 return TRIVIAL_COMMANDS.has(parts[0])
}

// shellTokenize splits a command segment into tokens, quote-aware: single-
// and double-quoted spans stay one token (env values like A='b c' must not
// split mid-quote), backslash escapes join their next character (ca\ + t
// tokenizes as "cat"), and $'…' ANSI-C quoting is unquoted. Parity with the
// Go-side shellTokens classifier.
function shellTokenize(seg: string): string[] {
  const toks: string[] = []
  let cur = ""
  let has = false
  let quote: string | null = null
  for (let i = 0; i < seg.length; i++) {
    const c = seg[i]
    if (quote) {
      if (c === quote) quote = null
      else cur += c
      continue
    }
    if (c === "'" || c === '"') {
      quote = c
      has = true
      continue
    }
    if (c === "$" && (seg[i + 1] === "'" || seg[i + 1] === '"')) {
      continue // $'…' ANSI-C span: the quote arm takes over next iteration
    }
    if (c === "\\" && i + 1 < seg.length) {
      cur += seg[i + 1]
      i++
      has = true
      continue
    }
    if (/\s/.test(c)) {
      if (cur !== "" || has) {
        toks.push(cur)
        cur = ""
        has = false
      }
      continue
    }
    cur += c
  }
  if (cur !== "" || has) toks.push(cur)
  return toks
}

// Prefix commands whose ARGUMENTS must be skipped before the real command
// name appears (sudo -u x cat, env -i cat, nice -n 5 cat, xargs cat,
// watch -n 2 cat, time/nohup/strace/stdbuf cat ...).
const PREFIX_CMDS = new Set([
  "sudo", "doas", "env", "nice", "nohup", "time", "xargs", "watch", "strace", "ltrace", "stdbuf",
])
// Prefix-command flags that consume the NEXT token as their value
// (sudo -u x, nice -n 5, env --unset NAME, watch -n 2, strace -o file...).
// Only flags that reliably take a value across the prefix commands are
// listed: over-listing a boolean flag (env -i) would swallow the real
// command name and MISS the content-intent hit.
const PREFIX_VALUE_FLAGS = new Set([
  "-u", "-n", "-o", "-g", "-p",
  "--user", "--prompt", "--group", "--nice-level", "--env", "--unset", "--input", "--output",
])
// stripPrefixTokens removes leading env-assignment tokens (VAR=value), prefix
// commands (sudo/doas/env/nice/nohup/time/xargs/watch/strace/...) and their
// flag arguments, so `ZZZ=1 grep`, `sudo -u x cat` and `xargs cat` all
// classify by their real command — parity with kern-guard.sh's
// position-independent matching (the whole-command first-token-only
// check was spoofable via env prefixes; F-011: prefix flags, quoted env
// values and wrapper commands extended the same hole).
function stripPrefixTokens(tokens: string[]): string[] {
  let i = 0
  let inPrefixArgs = false
  while (i < tokens.length) {
    const t = tokens[i]
    if (inPrefixArgs) {
      if (PREFIX_VALUE_FLAGS.has(t) && i + 1 < tokens.length) {
        i += 2
        continue
      }
      if (t.startsWith("-")) {
        i++
        continue
      }
      if (/^[A-Za-z_][A-Za-z0-9_]*=/.test(t)) {
        i++ // env-style assignment inside a prefix chain (sudo VAR=v cmd)
        continue
      }
      inPrefixArgs = false // the real command
      continue
    }
    if (PREFIX_CMDS.has(t)) {
      inPrefixArgs = true
      i++
      continue
    }
    if (/^[A-Za-z_][A-Za-z0-9_]*=/.test(t)) {
      i++
      continue
    }
    break
  }
  return tokens.slice(i)
}

// splitCommandSegments splits a command on &&, ||, ;, | and NEWLINE
// separators, respecting single- and double-quoted spans so `grep "a|b" f`
// stays one segment while a two-line script cannot hide content tools on
// line 2 (F-011: newlines were not segmented).
function splitCommandSegments(cmd: string): string[] {
  const segs: string[] = []
  let cur = ""
  let quote: string | null = null
  for (let i = 0; i < cmd.length; i++) {
    const c = cmd[i]
    if (quote) {
      if (c === quote) quote = null
      cur += c
      continue
    }
    if (c === "'" || c === '"') {
      quote = c
      cur += c
      continue
    }
    if ((c === "&" || c === "|") && cmd[i + 1] === c) {
      segs.push(cur)
      cur = ""
      i++
      continue
    }
    if (c === ";" || c === "|" || c === "\n" || c === "\r") {
      segs.push(cur)
      cur = ""
      continue
    }
    cur += c
  }
  segs.push(cur)
  return segs
}

// P2 — risk-axis reclassification. The single content-intent deny-list is
// split into two lists with opposite enforcement:
//   READ_CMDS — content-READING commands that are SERVED, never denied: a pure
//   read has no write/network/destructive surface, so the guard's job is to
//   answer it via kern's fast paths (Tier 1/2) or the readonly sandbox (Tier
//   3), not to police it.
//   GATE_CMDS — writes/destructive/network/installers (the risky axis): a
//   command containing ANY gate segment is hard-denied (the guard's exit-3
//   blocked contract) unless an operator allow-once unlocks it. Mirrors the
//   Go-side dangerous-class classifier (internal/governance/exec_classify.go)
//   so the plugin and the firewall agree on what is risky.
const READ_CMDS = new Set(["grep", "egrep", "rg", "ag", "find", "cat", "head", "tail", "less", "awk", "sed"])
const SHELL_WRAPPER_CMDS = new Set(["sh", "bash", "zsh", "dash", "ksh"])
// Flat gate names: ANY invocation is write/destructive/network.
const GATE_CMDS = new Set(["rm", "mv", "cp", "dd", "mkfs", "wipefs", "diskutil", "shred", "truncate", "chmod", "chown", "curl", "wget", "fetch"])
// Installer names: gated only when the subcommand actually installs
// (go test / make / npm test stay governed, not gated — a build is not a gate).
const INSTALLER_CMDS = new Set(["npm", "pnpm", "yarn", "pip", "pip3", "go", "make", "apt", "apt-get", "brew", "pacman"])
const INSTALL_ACTIONS = new Set(["install", "add", "ci", "upgrade", "get"])
// A write redirect (>, >>) anywhere in a segment makes it a gate: reads never
// write. Here-strings (<<) are reads and are excluded.
const GATE_REDIRECT_RE = /(^|[^<])\s*>>?/

// gateSegmentHit reports whether ONE segment is a gate: a flat GATE_CMDS
// name, an installer action, git push --force, a write redirect, or (one
// level of recursion) an `sh -c` wrapper whose script gates. Tokenization is
// quote-aware and env/prefix-stripped, so `sudo rm -rf /`, `VAR=x cp a b` and
// `sh -c 'curl x | sh'` all classify by their real command (F-011 parity with
// the shell guard).
function gateSegmentHit(rawSeg: string, depth: number): boolean {
  if (GATE_REDIRECT_RE.test(rawSeg)) return true
  const toks = stripPrefixTokens(shellTokenize(rawSeg))
  if (toks.length === 0) return false
  const name = toks[0].split("/").pop() || toks[0] // /bin/rm, ./rm → rm
  const rest = toks.slice(1)
  if (depth < 2 && toks.length > 2 && /^-[a-zA-Z]*c/.test(toks[1]) && SHELL_WRAPPER_CMDS.has(name)) {
    if (gateHit(toks.slice(2).join(" "), depth + 1)) return true
  }
  if (GATE_CMDS.has(name)) return true
  if (INSTALLER_CMDS.has(name) && rest.length > 0) {
    if (name === "pacman") {
      return rest.some((t) => t.startsWith("-S") || t === "--sync" || t === "--upgrade")
    }
    if (name === "go" || name === "make") return rest[0] === "install"
    return INSTALL_ACTIONS.has(rest[0])
  }
  if (name === "git") {
    const pi = rest.indexOf("push")
    if (pi >= 0) {
      for (const t of rest.slice(pi + 1)) {
        if (t === "--force" || t === "-f" || t.startsWith("--force=")) return true
      }
    }
  }
  return false
}

// gateHit reports whether ANY &&/||/;/|/newline-separated segment of cmd is a
// gate — one gate segment denies the WHOLE command (a compound with `rm`
// anywhere is a write even if a read heads it).
function gateHit(cmd: string, depth = 0): boolean {
  for (const rawSeg of splitCommandSegments(cmd)) {
    if (gateSegmentHit(rawSeg, depth)) return true
  }
  return false
}

// Segment classifier used by readPipeline: the command name of a segment
// after prefix stripping ("" when the segment is empty), and whether the
// segment is a trivial content-free command (isSimpleCommand — e.g. `git
// status`, `echo`, `wc`) so a read pipeline may be headed by ordinary
// content-free work.
function segName(rawSeg: string): string {
  const toks = stripPrefixTokens(shellTokenize(rawSeg))
  if (toks.length === 0) return ""
  return (toks[0].split("/").pop() || toks[0])
}

// A read head is symbol-shaped when its pattern is a plausible symbol (no
// regex metacharacters): those translate to kern's AST index. Regex-only,
// comments/TODO and punctuation patterns are NOT symbol-shaped and fall to
// Tier 3 (kern exec --readonly).
const SYMBOL_SHAPED_RE = /^[A-Za-z_][A-Za-z0-9_.]*$/

// isReadOnlyCompanion reports whether a segment may legally co-head a
// pure-read pipeline: a trivial content-free command (echo/ls/wc/...) or a
// READ-ONLY git form (status/log w/o patch/rev-parse/diff|show --stat/
// branch -a/remote) — NEVER a write. isSimpleCommand alone is NOT enough
// here: it deliberately treats `git add`/`git commit` as content-free (they
// run raw through the simple exemption), but a git add/commit must never
// ride along into a Tier-3 `kern exec --readonly` script (it would mutate
// the real worktree from inside the "read").
function isReadOnlyCompanion(seg: string): boolean {
  if (!isSimpleCommand(seg)) return false
  const parts = seg.trim().split(/\s+/)
  // A kern CLI command runs raw as a simple command but must never ride
  // into a Tier-3 readonly-sandbox serve — pipes containing kern stay
  // governed (split them instead).
  if (parts[0] === "kern") return false
  if (parts[0] !== "git") return true
  let i = 1
  if (parts[i] === "-C") i += 2
  const sub = parts[i] ?? ""
  return sub !== "add" && sub !== "commit"
}

// grepPatternAndTarget splits grep-family args into the pattern (first
// non-flag argument) and whether an explicit TARGET (file or directory)
// follows it — parity with the Go classifier's grepPatternAndTarget.
function grepPatternAndTarget(args: string[]): { pattern: string; hasTarget: boolean } {
  let pattern = ""
  let hasTarget = false
  for (const a of args) {
    if (a.startsWith("-") && a !== "-") continue
    if (pattern === "") {
      pattern = a
      continue
    }
    hasTarget = true
    break
  }
  return { pattern, hasTarget }
}

// readPipeline classifies a command that contains at least one READ segment
// and no GATE segment. Returns the serve plan (tier + the pieces needed to
// serve) or null when the command is NOT a pure-read pipeline (a non-read,
// non-trivial segment such as `go test` or `git log -p` is present — that
// command is governed below, not served).
//   tier 1: single translatable read — symbol grep → kern ast; file read →
//     kern compact; find → kern buddy. Served from the index in grep shape.
//     A grep naming an explicit target (file/dir) is NOT tier 1: the symbol
//     search is repo-wide and ignores the scope.
//   tier 2: read head + bounded simple tail (| wc -l, | head N, | tail N,
//     | grep -v P, | grep P) — serve the head from the index, apply the tail
//     in-process, return the COMPUTED answer.
//   tier 3: everything else pure-read (regex greps, scoped greps, awk/sed
//     transforms, compounds of reads) → kern exec --readonly (sandboxed,
//     masked).
function readPipeline(cmd: string): { tier: number; head?: string; pattern?: string; file?: string; tails?: string[] } | null {
  // One level of sh -c "..." / bash -c "..." wrapper unwrapping (mirrors
  // gateSegmentHit's recursion): a wrapper segment classifies as its inner
  // script — `sh -c "cat .gitignore"` is a read, not an unknown command.
  // Declared as a plain function (not an annotated arrow) so the node parity
  // harness can eval the extracted block.
  const unwrap = (raw) => {
    const toks = stripPrefixTokens(shellTokenize(raw))
    if (toks.length >= 3 && /^-[a-zA-Z]*c/.test(toks[1]) && SHELL_WRAPPER_CMDS.has(toks[0])) {
      return toks.slice(2).join(" ")
    }
    return raw
  }
  const segs = splitCommandSegments(cmd).map((s) => s.trim()).filter((s) => s !== "").map(unwrap)
  if (segs.length === 0) return null
  const names = segs.map(segName)
  if (!names.some((n) => READ_CMDS.has(n))) return null
  // Pure-read check: every segment is a READ command or a read-only
  // companion (trivial shell / read-only git). Anything else (a write like
  // git add/commit, a build/test/install, git -p) means the command is
  // governed, not served — a write must never ride into a Tier-3 read.
  const companion = segs.map((s) => isReadOnlyCompanion(s))
  for (let i = 0; i < segs.length; i++) {
    if (!(READ_CMDS.has(names[i]) || companion[i])) return null
  }
  // tier 1: a single read segment (optionally with &&/;-joined content-free
  // companions). A PIPE is never a tier-1 shape: `grep foo | wc -l` is a
  // read-head + tail (tier 2), not a single read with a companion — the tail
  // consumes and transforms the read's output, so the COMPUTED answer (the
  // count, not the lines) is the serve.
  const readIdx = names.findIndex((n) => READ_CMDS.has(n))
  const nonReadTrivial = segs.every((s, i) => i === readIdx || isReadOnlyCompanion(s))
  if (nonReadTrivial && !cmd.includes("|")) {
    const name = names[readIdx]
    const toks = stripPrefixTokens(shellTokenize(segs[readIdx]))
    const args = toks.slice(1)
    if (name === "find") return { tier: 1, head: "find" }
    if (name === "grep" || name === "egrep" || name === "rg" || name === "ag") {
      const { pattern, hasTarget } = grepPatternAndTarget(args)
      // A scoped grep (explicit file/dir target) is tier 3: the tier-1
      // symbol search is repo-wide and ignores the caller's scope — a
      // named file must never be answered with symbols from other files.
      if (SYMBOL_SHAPED_RE.test(pattern) && !hasTarget) return { tier: 1, head: name, pattern }
      return { tier: 3 } // regex/punctuation/scoped pattern → sandboxed read
    }
    if (name === "cat" || name === "head" || name === "tail" || name === "less" || name === "more") {
      const file = args.filter((a) => !a.startsWith("-")).pop() || ""
      if (file !== "") return { tier: 1, head: name, file }
      return { tier: 3 }
    }
    // sed/awk transform a stream — never a plain file read → Tier 3.
    return { tier: 3 }
  }
  // tier 2: read head + bounded simple tails (| wc -l, | head N, | tail N,
  // | grep -v P, | grep P). The head must be a symbol-shaped grep-family read.
  const headToks = stripPrefixTokens(shellTokenize(segs[0]))
  const headName = segName(segs[0])
  if ((headName === "grep" || headName === "egrep" || headName === "rg" || headName === "ag") && READ_CMDS.has(headName)) {
    const headArgs = headToks.slice(1)
    const { pattern, hasTarget } = grepPatternAndTarget(headArgs)
    if (SYMBOL_SHAPED_RE.test(pattern) && !hasTarget) {
      const tails: string[] = []
      let ok = true
      for (let i = 1; i < segs.length; i++) {
        const t = segs[i].trim()
        if (/^wc(\s+.*)?$/i.test(t) && /\s-l(\s|$)/.test(t)) { tails.push(t); continue }
        if (/^head(\s+.*)?$/i.test(t) || /^tail(\s+.*)?$/i.test(t)) { tails.push(t); continue }
        if (/^grep(\s+.*)?$/i.test(t) && /-v\b/.test(t)) { tails.push(t); continue }
        if (/^grep(\s+.*)?$/i.test(t)) { tails.push(t); continue }
        ok = false
        break
      }
      if (ok && tails.length > 0) return { tier: 2, head: headName, pattern, tails }
    }
  }
  return { tier: 3 }
}

// Raw read: verbatim file contents (or a directory listing), mirroring the
// built-in read tool. Only used when kern is unavailable.
async function readFallback(filePath: string): Promise<string> {
  try {
    const st = await stat(filePath)
    if (st.isDirectory()) {
      const entries = await readdir(filePath, { withFileTypes: true })
      return entries.map((e) => e.name + (e.isDirectory() ? "/" : "")).join("\n")
    }
    return await readFile(filePath, "utf8")
  } catch (err) {
    return `error: ${(err as Error).message}`
  }
}

// Phase metadata for phase-aware tool filtering (mirrors the MCP catalog's
// Tool.Phase in internal/mcp/tools.go). "meta" and "cross" tools are always
// advertised; the four agent phases (explore|plan|edit|verify) advertise only
// their own tools when KERN_MCP_PHASE is set. KERN_TOOLS narrows to an
// explicit allowlist; KERN_MCP_SINGLE_TOOL=1 reduces the surface to kern_meta.
const TOOL_PHASES: Record<string, string> = {
kern_arch: "explore",
  kern_ast_search: "explore",
  kern_buddy: "explore",
  kern_compact_file: "explore",
  kern_context: "explore",
  kern_doc: "cross",
  kern_evidence: "verify",
  kern_exec: "edit",
  kern_explore: "explore",
  kern_fetch_raw_anchor: "cross",
  kern_fit_context: "plan",
  kern_graph: "explore",
  kern_apply: "edit",
  kern_heal: "edit",
  kern_impact: "plan",
  kern_mask_pii: "cross",
  kern_memory: "cross",
  kern_meta: "meta",
  kern_optimize: "cross",
  kern_path: "explore",
  kern_plan: "plan",
  kern_plan_context: "plan",
  kern_probe: "explore",
  kern_project_map: "explore",
  kern_review: "verify",
  kern_run: "cross",
  kern_search: "explore",
  kern_semcache: "cross",
  kern_stats: "cross",
  kern_swap: "plan",
  kern_verify: "verify",
  kern_why: "explore",
}
// defaultTools mirrors internal/mcp/toolpolicy.go's 32-tool default surface (the
// full catalog since 2026-10-23) so the
// plugin advertises the same tools the MCP server does when no env vars are
// set. KERN_MCP_FULL=1, KERN_MCP_PHASE, and KERN_TOOLS all lift the default
// (matching the server's behavior).
const DEFAULT_TOOLS = new Set([
  "kern_meta", // NL router → all sub-tools
  "kern_explore", // symbol source + callers/callees + blast radius
  "kern_impact", // blast radius of a change
  "kern_apply", // safe edit path: transactional apply + verify + rollback
  "kern_search", // ranked symbol search
  "kern_verify", // unified verification
  "kern_buddy", // session onboarding digest
  "kern_exec", // governed execution — fails closed without KERN_ALLOW_EXEC/KERN_TOOLS (D5)
  "kern_compact_file", // compact symbolic file summary (tier=full verbatim)
  "kern_run", // run an intent through the full task pipeline
  "kern_optimize", // compress context: action=prompt|log|output
  "kern_context", // minimal relevant source slice for a symbol
  "kern_memory", // project brain memory (add/list/recall/remove)
  "kern_evidence", // signed-evidence toolkit (verify/explain/export/anchor)
  "kern_probe", // micro-context router: task → symbol bundle
  "kern_plan", // deterministic implementation plan for a change
  "kern_doc", // project docs: action=search|fetch|index
  "kern_project_map", // compressed whole-project symbol map
  "kern_ast_search", // AST-level symbol search
  "kern_stats", // before/after token savings and cost estimates
  "kern_why", // rationale and doc-reference report for a symbol
  "kern_heal", // self-correction loop on failing files (snapshot)
  "kern_fit_context", // fit files/symbols into a token budget
  "kern_plan_context", // deterministically plan which context to include
  "kern_path", // shortest call path between two symbols
  "kern_semcache", // inspect/manage the semantic cache
  "kern_swap", // swap fenced code blocks to per-file signatures
  "kern_mask_pii", // locally scan text for secrets/PII
  "kern_fetch_raw_anchor", // re-read truncated anchor output
  "kern_review", // token-optimised code-review context
  "kern_graph", // one-call graph context (callers/callees/blast radius)
  "kern_arch", // architecture overview from call-graph communities
])

// filterToolSurface applies the same advertisement rules as the MCP server's
// filteredTools(): default surface, phase filter (meta/cross always shown),
// then the KERN_TOOLS allowlist. Like the server, this only shapes what the
// agent SEES — it is not a security boundary (kern_meta can still route to
// unadvertised tools).
function filterToolSurface<T extends Record<string, unknown>>(tools: T): Partial<T> {
  const phase = (process.env.KERN_MCP_PHASE || "").trim().toLowerCase()
  const allowlist = (process.env.KERN_TOOLS || "")
    .split(",").map((s) => s.trim()).filter(Boolean)
  const single = process.env.KERN_MCP_SINGLE_TOOL === "1"
  const full = process.env.KERN_MCP_FULL === "1"
  const valid = new Set(["explore", "plan", "edit", "verify"])
  const active = valid.has(phase) ? phase : ""
  const out: Record<string, unknown> = {}
  for (const [name, def] of Object.entries(tools)) {
    if (single && name !== "kern_meta") continue
    const p = TOOL_PHASES[name] ?? ""
    if (active && p !== "meta" && p !== "cross" && p !== active) continue
    if (allowlist.length > 0 && !allowlist.includes(name)) continue
// No env override → default to the full 32-tool catalog (same as the MCP
// server, standing mandate 2026-10-23). Shadow built-ins (read/glob/grep/bash)
// are not kern_* tools
// and must always stay advertised to keep precedence over the
// built-ins they replace.
	if (name.startsWith("kern_") && !full && !active && allowlist.length === 0 && !DEFAULT_TOOLS.has(name)) continue
    // wrapCaller (C1/P2): record the ToolContext caller for every tool that
    // passes the filter — kern_* tools and the shadows alike — so etagKey
    // can scope the conditional-fetch cache per reader.
    out[name] = wrapCaller(def)
  }
  return out
}

// --- Caller-scoped etag cache (C1/P2, W3 client leg) ---
// opencode's ToolContext (the second execute argument) identifies the
// caller: sessionID + agent. A subagent runs with its own session/agent and
// a fresh context holds no earlier copy to reuse, so the shared etag cache
// must be keyed per caller — otherwise a subagent's identical read gets the
// tiny "unchanged" reply with no bytes behind it. The caller is captured
// once at the single registration point (wrapCaller inside
// filterToolSurface, which every tool — kern_* and the read/glob/grep/bash
// shadows — passes through) and carried down to etagKey via
// AsyncLocalStorage instead of threading a parameter through ~120 execute
// signatures. Hosts or hook paths that run without a ToolContext store
// (undefined) fall back to the legacy shared bucket: exactly the behavior
// before this change.
const callerALS = new AsyncLocalStorage<{ session: string; agent: string }>()

// wrapCaller wraps one tool definition's execute so the caller identity is
// recorded for the duration of the call (and everything it awaits). The
// wrapped execute returns the original promise; ALS propagation follows
// every await inside it.
function wrapCaller(def: unknown): unknown {
  if (!def || typeof def !== "object" || typeof (def as any).execute !== "function") return def
  const inner = (def as any).execute as (args: any, ctx?: any) => Promise<unknown>
  ;(def as any).execute = async (args: any, ctx?: any) => {
    const caller =
      ctx && typeof ctx.agent === "string" && typeof ctx.sessionID === "string"
        ? { session: ctx.sessionID, agent: ctx.agent }
        : undefined
    return callerALS.run(caller, () => inner(args, ctx))
  }
  return def
}

export default (async ({ directory, $ }) => {
  const bin = kernBin(directory)

  // Timeout ceiling for governed tool calls: an explicit agent-provided
  // timeout (milliseconds — the opencode bash convention) is honored up to a
  // 30-minute absolute cap, so long builds/tests can run when the agent asks
  // for them; without one, a 2-minute default keeps a hung subprocess from
  // wedging the agent's tool call. The runtime `$` shell exposes `.timeout()`
  // in some opencode versions but not others, so fall back to a manual timer
  // when absent.
  // must stay <= MCP per-call ceiling (server.go) which is the effective cap
  const MAX_CEILING_MS = 1_800_000
  const ceilingMs = (requested?: number) => Math.min(requested ?? 120_000, MAX_CEILING_MS)
  const withTimeout = <T>(promise: Promise<T>, ms: number): Promise<T> =>
    new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error(`kern timed out after ${ms}ms`)), ms)
      promise.then(
        (v) => { clearTimeout(timer); resolve(v) },
        (e) => { clearTimeout(timer); reject(e) },
      )
    })

  const truthy = (v?: string): boolean => v === "true" || v === "1"

  // --- ETag conditional-fetch cache (B2, ADR-0012) ---
  // Client mirror of the B1 CLI contract: `kern context|compact|retrieve|
  // explore --etag <64-hex>` short-circuits to `unchanged (etag <E>)` on
  // stdout when the content hash matches, and prints an `etag: <64-hex>`
  // footer on STDERR for a full response. Caching the last-seen etag per
  // canonical call key collapses repeat reads to a handful of tokens (the
  // model reuses its earlier copy) instead of the full payload. Bounded
  // like envelopeCache: clear-on-overflow keeps memory flat.
  const etagCache = new Map<string, string>() // caller-scoped call key -> last-seen etag
  // Bound (C1/P2, W4): KERN_ETAG_CACHE_MAX tunes the cache size (default
  // 256, capped 4096). Eviction is true LRU — delete the oldest entry on
  // overflow — replacing the former clear-on-overflow full wipe that
  // silently re-opened full reads after 64 distinct views.
  const ETAG_CACHE_MAX = (() => {
    const n = parseInt(process.env.KERN_ETAG_CACHE_MAX || "", 10)
    return Number.isFinite(n) && n > 0 ? Math.min(n, 4096) : 256
  })()
  // CLI subcommands mirroring etag-eligible MCP tools (kern_explore,
  // kern_context, kern_compact_file, kern_retrieve — internal/mcp/etag
  // EligibleTools): only these print an `etag:` footer and consume --etag.
  const ETAG_ELIGIBLE_TOOLS = new Set(["explore", "context", "compact", "retrieve", "search", "why", "inherits", "graph"])
  // Canonical key for an etag-eligible call: the CALLER (C1/P2 —
  // ToolContext sessionID/agent via callerALS, so a subagent's identical
  // read never borrows the parent's cached "unchanged" verdict) plus the
  // CLI subcommand and its meaningful arguments. JSON.stringify of the
  // filtered array (not a "|" join): args may legally contain "|" (paths),
  // which would collide across distinct calls. The --etag exclusion keeps
  // the key identical whether or not the cached etag was appended.
  function etagKey(args: string[]): string {
    const c = callerALS.getStore()
    const caller = c ? `${c.session}/${c.agent}` : ""
    return JSON.stringify([caller, ...args.filter((a, i) => a !== "--etag" && args[i - 1] !== "--etag")])
  }
  // Record an etag learned from a CLI stderr footer (a line `etag: <64-hex>`)
  // under the call's canonical key. A full response refreshes the entry; a
  // matched conditional fetch prints no footer and leaves it untouched.
  // True LRU (C1/P2, W4): a JS Map preserves insertion order, so re-setting
  // a key moves it to the back (most-recent), and overflow deletes the
  // OLDEST entry — never the former clear() full wipe.
  function etagTouch(key: string, etag: string): void {
    etagCache.delete(key)
    etagCache.set(key, etag)
  }
  // etagGet: touch-on-hit lookup so a hot entry never ages out.
  function etagGet(key: string): string | undefined {
    const hit = etagCache.get(key)
    if (hit !== undefined) etagTouch(key, hit)
    return hit
  }
  function rememberEtag(key: string, stderr: string): void {
    const m = /^etag: ([0-9a-f]{64})\r?$/m.exec(stderr)
    if (!m) return
    etagTouch(key, m[1])
    if (etagCache.size > ETAG_CACHE_MAX) etagCache.delete(etagCache.keys().next().value as string)
  }
  const run = async (args: string[], timeoutMs?: number): Promise<string> => {
    // ETag conditional-fetch loop (B2, ADR-0012): read the cached etag for
    // this call's canonical key and auto-append --etag so a repeat read of
    // the same view short-circuits to the tiny `unchanged (etag <E>)` reply
    // instead of the full payload. Callers that already append --etag (the
    // native tool handlers, the read shadow) are left untouched by the
    // includes() guard; only eligible mirror subcommands can ever have a
    // cached etag, and the eligibility guard stays explicit.
    const key = etagKey(args)
    const cached = etagGet(key)
    if (cached && ETAG_ELIGIBLE_TOOLS.has(args[0]) && !args.includes("--etag")) {
      args = [...args, "--etag", cached]
    }
    // Bun's shell escapes each interpolated array element as one argument.
    // The ceiling is the agent's requested budget (or the 2-minute default);
    // the shell kills the child on expiry (exit 124).
    const p = $`${bin} ${args}`
    const ms = ceilingMs(timeoutMs)
    // Prefer the runtime .timeout(); fall back to a manual ceiling when the
    // injected `$` shell does not implement it (observed on opencode 1.18.15).
    const out = typeof (p as any).timeout === "function"
      ? await p.timeout(ms).quiet()
      : await withTimeout(p.quiet(), ms)
    // ETag learning (B2, ADR-0012): a full response carries an `etag:
    // <64-hex>` footer on STDERR — record it so the next identical call can
    // pass --etag and get the tiny `unchanged (etag <E>)` reply instead of
    // the full payload. A matched conditional fetch prints no footer, so the
    // cached etag persists untouched. stdout stays byte-identical for
    // existing consumers (the footer never lands in the tool result).
    rememberEtag(key, out.stderr.toString())
    return out.stdout.toString()
  }

  // Raw shell fallback for the shadow bash tool: run the command through the
  // host `$` shell (sh -c) exactly as opencode's built-in bash would, with the
  // same timeout ceiling as `run` (timeout is milliseconds, the opencode bash
  // convention). Only used when kern is unavailable.
  // Host model delegation for hosts that do not announce MCP sampling
  // (e.g. opencode) is handled server-side: set KERN_HOST_SAMPLER_CMD (and
  // optionally KERN_HOST_SAMPLER_TIMEOUT / KERN_HOST_SAMPLER_KEY) and the
  // kern MCP server self-registers the command sampler at startup — the auto
  // LLM chain's host leg then shells it per generation (stdin = user prompt,
  // $KERN_SYSTEM_PROMPT = system prompt, stdout = reply). Example:
  // KERN_HOST_SAMPLER_CMD="opencode run" kern-mcp
  const runRaw = async (command: string, workdir?: string, timeout?: number): Promise<string> => {
    // Full capture (UX fix): run through an outFile wrapper so a non-zero
    // exit never degrades into the host's bare "Failed with exit code N"
    // error rendering — which drops stdout AND stderr entirely (observed
    // live). The wrapper prints the real exit code on its own stdout; the
    // command's streams land in temp files. Success keeps the old contract
    // (stdout only); failure returns stdout+stderr with the exit code
    // annotated, so the agent sees WHY the command failed.
    const sq = (s: string) => `'${s.replace(/'/g, `'\\''`)}'`
    const stamp = `${process.pid}-${randomBytes(4).toString("hex")}`
    const outF = resolve(tmpdir(), `kern-shadow-${stamp}.out`)
    const errF = resolve(tmpdir(), `kern-shadow-${stamp}.err`)
    try {
      // Subshell so the redirect covers every command of a compound — a
      // bare `A && B > f` would capture only B.
      const wrapped = `( ${command} ) > ${sq(outF)} 2> ${sq(errF)}; printf '%s' "$?"`
      const p = $`sh -c ${wrapped}`
      if (workdir) p.cwd(workdir)
      const ms = ceilingMs(timeout)
      const out = typeof (p as any).timeout === "function"
        ? await p.timeout(ms).quiet()
        : await withTimeout(p.quiet(), ms)
      const code = parseInt(out.stdout.toString().trim(), 10)
      if (Number.isNaN(code)) {
        // The wrapper never printed an exit code — the command line failed
        // to parse (sh syntax error) or the shell died. Surface the shell's
        // own stderr instead of returning an empty success. Return (not
        // throw): the host renders thrown tool errors as a bare failure
        // with no payload, which would drop this diagnostic.
        return out.stderr.toString().trim() || "shell could not execute the command"
      }
      const stdout = await readFile(outF, "utf8").catch(() => "")
      if (Number.isNaN(code) || code === 0) return stdout
      const stderr = await readFile(errF, "utf8").catch(() => "")
      const parts = [stdout, stderr.trim() !== "" ? `[stderr]\n${stderr}` : ""].filter(Boolean)
      const body = parts.join("\n")
      return body ? `${body}\n[exit code: ${code}]` : `[exit code: ${code}]`
    } finally {
      await rm(outF, { force: true })
      await rm(errF, { force: true })
    }
  }

  // Kern-first adoption logging (opt-in, KERN_ADOPTION_LOG=1): each shadow
  // tool call appends one JSON line {ts, tool, routed} to .kern/adoption.log
  // in the project root. The Go side aggregates it
  // (internal/metrics.AggregateAdoptionLog) into the kern-first adoption
  // metric surfaced by `kern doctor` (adoption check). routed=true means the
  // result the agent saw came from kern (compact output, governed build,
  // or a governed denial/error); routed=false means a raw fallback served
  // the call (operator bypass, glob/grep metachar fallback, or kern
  // unavailable). Best-effort: failures are swallowed so metrics can never
  // break a tool call. Default off — no behavior change without the gate.
  const logAdoption = (tool: string, routed: boolean): void => {
    if (process.env.KERN_ADOPTION_LOG !== "1") return
    try {
      const dir = resolve(directory, ".kern")
      mkdirSync(dir, { recursive: true })
      appendFileSync(resolve(dir, "adoption.log"), JSON.stringify({ ts: Date.now(), tool, routed }) + "\n")
    } catch {
      /* swallow — adoption metrics must never break the tool call */
    }
  }

  // Some commands (kern sec, kern delete, kern guard check) print their
  // payload to stdout and then exit non-zero as a secondary signal. Surface
  // the payload instead of treating the exit code as a hard failure.
async function runPayload(args: string[], timeoutMs?: number, preserveExit = false): Promise<string> {
    // Several kern CLI commands print their full report to stdout and then
    // exit non-zero BY DESIGN as a CI signal (kern review exits 1 when
    // risk > 0, kern sec on error findings, kern verify on FAIL,
    // kern guard check exits 3 on violations, kern build/heal/sandbox/exec on
    // command failure). The report must survive as the tool result. Capture it
    // via sh -c with output redirection and a forced exit 0: some host `$`
    // shells reject redirect tokens in the template literal itself AND do not
    // attach stdout to the rejection error, either of which would lose the
    // report. With preserveExit=true the forced exit 0 is dropped and a
    // non-zero exit returns the captured output as the tool result, so
    // callers that need the real exit status (kern_exec: "stderr is only
    // surfaced on failure") still surface it.
    const sq = (s: string) => `'${s.replace(/'/g, `'\\''`)}'`
    const outFile = resolve(tmpdir(), `kern-shadow-${process.pid}-${randomBytes(4).toString("hex")}.out`)
    try {
      const cmdStr = `${sq(bin)} ${args.map(sq).join(" ")} > ${sq(outFile)} 2>&1${preserveExit ? "" : "; exit 0"}`
      const p = $`sh -c ${cmdStr}`
      try {
        if (typeof (p as any).timeout === 'function') await p.timeout(ceilingMs(timeoutMs)).quiet()
        else await withTimeout(p.quiet(), ceilingMs(timeoutMs))
      } catch (err) {
        if (preserveExit) {
          // Non-zero exit (or timeout) of the wrapped command: return the
          // captured output as the tool result so the agent sees why it
          // failed — a thrown error surfaces as a bare failure with no
          // payload (observed live). Fall back to the error message when
          // nothing was captured.
          const captured = await readFile(outFile, "utf8").catch(() => "")
          return captured || (err instanceof Error ? err.message : String(err))
        }
        // Timeout/spawn failure on the non-preserveExit path: return the
        // diagnostic instead of throwing — the host drops thrown-error
        // payloads, leaving the agent with a bare failure and no reason.
        return err instanceof Error ? err.message : String(err)
      }
      const text = await readFile(outFile, 'utf8')
      // NOTE: no etag learning here — runPayload merges stderr into the
      // capture file (2>&1), so a B2 `etag: <64-hex>` footer would land
      // INSIDE the returned tool text (breaking byte-identical stdout), and a
      // source file containing a literal `etag: <64-hex>` line would poison
      // the cache. run()'s stderr-based learning is the correct, live path.
      return text
    } finally {
      await rm(outFile, { force: true })
    }
  }

  // Governed execution with full output + exit-code capture (UX fix): like
  // runPayload, but instead of forcing exit 0 (or throwing the captured
  // text away with only a bare "Failed with exit code N"), it reports the
  // real exit code alongside the captured output so the caller can return a
  // non-zero result AS the tool result — the host renders thrown tool
  // errors without their payload, which silently discarded both the
  // command's output and the denial reason (observed live). A rejection
  // from this helper still means timeout/spawn failure (the wrapper itself
  // always exits 0), so callers keep their existing throw semantics for
  // those.
  const runWithExit = async (args: string[], timeoutMs?: number): Promise<{ text: string; exitCode: number }> => {
    // sq produces STANDARD POSIX single-quote quoting ('\'' for an embedded
    // quote). The host `$` transport hands cmdStr to sh -c as ONE argument
    // (lossless), so the inner sh must recover the args exactly as the
    // plugin received them — no extra escaping layer may be applied here.
    // Live regressions (F2): a compound with quotes/parens/redirects hit a sh
    // "syntax error near unexpected token '('" (quotes mangled, metachars
    // left unquoted), and `grep -c '^kern_'` reached grep as `\^kern_\`
    // ("trailing backslash", exit 2).
    const sq = (s: string) => `'${s.replace(/'/g, `'\\''`)}'`
    const outFile = resolve(tmpdir(), `kern-shadow-${process.pid}-${randomBytes(4).toString("hex")}.out`)
    try {
      const cmdStr = `${sq(bin)} ${args.map(sq).join(" ")} > ${sq(outFile)} 2>&1; printf '%s' "$?"`
      const p = $`sh -c ${cmdStr}`
      const out = typeof (p as any).timeout === "function"
        ? await p.timeout(ceilingMs(timeoutMs)).quiet()
        : await withTimeout(p.quiet(), ceilingMs(timeoutMs))
      const code = parseInt(out.stdout.toString().trim(), 10)
      const text = await readFile(outFile, "utf8").catch(() => "")
      return { text, exitCode: Number.isNaN(code) ? 0 : code }
    } finally {
      await rm(outFile, { force: true })
    }
  }

  // P3: classify a command into the risk-axis verdict via the Go binary
  // (`kern guard classify --json`) — the single source of truth for
  // tokenization/prefix-strip/segment-split and the gate/read decisions,
  // replacing the former TS duplicate. The result's verdict is one of
  // allow|gate|serve|governed; a serve verdict carries tier + the
  // head/pattern/file/tails pieces the bash arm needs to serve.
  const classifyCmd = async (cmd: string): Promise<Record<string, any>> => {
    const empty = { verdict: "governed", tier: 0, hint: "", hash: "" }
    try {
      const { text, exitCode } = await runWithExit(["guard", "classify", "--json", cmd], 10_000)
      if (exitCode !== 0) return empty
      const parsed = JSON.parse(text)
      if (!parsed || typeof parsed.verdict !== "string") return empty
      return parsed
    } catch {
      // kern unavailable or classify unsupported — fall back to governed
      // (the safe default: runs through kern build, which then fails
      // closed if kern is genuinely absent).
      return empty
    }
  }

  // --- governed-bash banner start (extracted verbatim by TestPluginShadowBannerEmptyReason) ---
  // Renders the bash shadow's failure banner. Never renders an empty why: a
  // nonzero exit with no captured output means the command was rejected
  // before execution or the runner failed to start (observed live: opencode
  // showed only "Failed with exit code 2" with zero output). Say so instead
  // of returning an empty payload.
  function governedBashBanner(exitCode: number, text: string): string {
    // Classify by the documented exit-code contract (kern exitcode): 3 =
    // denied/blocked by governance, 124 = timeout, anything else = failure.
    // A text-regex heuristic mislabeled benign findings (the word "timeout"
    // in usage hints like "raise with --timeout N" tripped the deny banner).
    // The harness strips the TS signature and runs the body under node, so
    // the body must stay plain-JS compatible (no TS-only syntax).
    let kind = "failed"
    if (exitCode === 3) kind = "denied"
    else if (exitCode === 124) kind = "timeout"
    else if (/execution denied|denied by governance|not permitted by policy|approval required/i.test(text)) kind = "denied"
    const label = kind === "denied" ? "[kern] governed command denied/blocked" : kind === "timeout" ? "[kern] command timed out" : "[kern] command failed"
    const why = text.trim() === "" ? "\ncommand produced no output — rejected before execution or the runner failed to start; classify the segments and retry the governed part directly" : ""
    return `${label} (exit code ${exitCode}):\n${text}${why}`
  }
  // --- governed-bash banner end ---

  // Best-effort: a memory-log failure must never break the host tool call.
  async function remember(lesson: string, maxLen = 300): Promise<void> {
    try {
      const text = lesson.replace(/\s+/g, " ").trim()
      if (!text) return
      const clipped = text.length > maxLen ? text.slice(0, maxLen).trimEnd() + "…" : text
      await run(["remember", "--auto", clipped])
    } catch {
      /* swallow */
    }
  }

  // --- Silent context injection (DeepSeek harness spec, Phase C) ---
  // Before the model's FIRST response of a session, append a planner-selected
  // context envelope to the system prompt: classify the user's intent,
  // assemble the context packet, select evidence by the task policy, fit it
  // to a token budget, and deliver the compact result. Injected once per
  // session so ordinary turns never pay the orchestration latency. FAIL-
  // CLOSED: any error, timeout, or missing binary skips injection entirely —
  // the host call is never blocked and the user never sees orchestration.
  // Disable with KERN_SILENT_INJECT=0.
  const INJECT_ENABLED = process.env.KERN_SILENT_INJECT !== "0"
  // Fully silent mode: KERN_SILENT=1 strips every visible [kern] marker from
  // tool output and injected context (north-star NS-5). Default is the
  // transparent mode (markers shown).
  const SILENT_MODE = process.env.KERN_SILENT === "1"
  const INJECT_MAX_WAIT_MS = 3000 // hard ceiling: a miss is fine, a stall is not
  const INJECT_MIN_TEXT = 12 // skip trivial chatter
  const envelopeCache = new Map<string, string>() // normalized text -> rendered block
  const injectedSessions = new Set<string>() // sessions that already got their envelope
  let currentMessageText = "" // text of the latest chat.message (for system.transform)
  let envelopeFlight: { text: string; promise: Promise<string | null> } | null = null

  async function orchestrateEnvelope(text: string): Promise<string | null> {
    try {
      const out = await runPayload(["orchestrate", text])
      if (!out) return null
      const parsed = JSON.parse(out)
      const plan = parsed.plan ?? {}
      const sels: any[] = (plan.selections ?? []).slice(0, 6)
      const head = SILENT_MODE
        ? `[context · ${parsed.task_type ?? "unknown"}] v${parsed.envelope_version}/${parsed.schema_version} · budget ${parsed.budget} · ${parsed.token_count} tokens${parsed.truncated ? " (truncated)" : ""}`
        : `[kern context · ${parsed.task_type ?? "unknown"}] envelope v${parsed.envelope_version}/${parsed.schema_version} · budget ${parsed.budget} · ${parsed.token_count} tokens${parsed.truncated ? " (truncated)" : ""} · handle ${String(parsed.handle?.id ?? "").slice(0, 16)}`
      const body = sels.map((s: any) => `- [${s.type ?? "?"} x${s.weight ?? "?"}] ${s.content ?? ""}`).join("\n")
      return body ? `${head}\n${body}` : head
    } catch {
      return null
    }
  }

  // envelopeFor returns the rendered envelope for a message, from cache or a
  // single in-flight computation under the hard ceiling. Always resolves
  // (null on any failure); concurrent callers for the same text share one
  // computation.
  function envelopeFor(text: string): Promise<string | null> {
    if (!INJECT_ENABLED || !text || text.length < INJECT_MIN_TEXT) return Promise.resolve(null)
    const cached = envelopeCache.get(text)
    if (cached !== undefined) return Promise.resolve(cached)
    if (envelopeFlight && envelopeFlight.text === text) return envelopeFlight.promise
    const promise = withTimeout(orchestrateEnvelope(text), INJECT_MAX_WAIT_MS).then(
      (block) => {
        envelopeFlight = null
        if (block) {
          envelopeCache.set(text, block)
          if (envelopeCache.size > 8) envelopeCache.clear() // bounded cache
        }
        return block
      },
      () => {
        // Timeout/error: clear the flight so the next message can retry, and
        // report a miss — injection is always fail-closed.
        envelopeFlight = null
        return null
      },
    )
    envelopeFlight = { text, promise }
    return promise
  }

  // Pre-warm the index at plugin load. This plugin calls the kern CLI, which
  // never triggers the MCP server's own preload, so the first tool call would
  // otherwise wait for the whole index build (and the 3s context injection
  // would kill a build it started). `kern buddy` goes through the guarded
  // auto-index path (it refuses $HOME, filesystem roots and documents folders)
  // and costs a few hundred ms when the index is already fresh. Fire-and-forget:
  // a failure never reaches the host. Opt-outs mirror the shell hook guard.
  if (
    process.env.KERN_PRELOAD !== "0" &&
    process.env.KERN_DISABLED !== "1" &&
    !existsSync(resolve(directory, ".kern", "off"))
  ) {
    void run(["buddy"], 600_000).catch(() => { /* best effort */ })
  }

  return {
    tool: filterToolSurface({
      kern_optimize: tool({
        description:
          "Deterministic local context optimizer. action=prompt compresses and cleans a raw prompt (returns it plus token savings); action=log strips noise from log output (keeps errors, warnings, stack traces, build failures); action=output compresses an LLM reply by stripping filler while preserving code blocks, lists and errors. Use before pasting logs or prompts into context.",
        args: {
          action: tool.schema.string().describe("prompt|log|output"),
          prompt: tool.schema.string().optional(),
          attached_log: tool.schema.string().optional(),
          session: tool.schema.string().optional(),
          model: tool.schema.string().optional(),
          log: tool.schema.string().optional(),
          text: tool.schema.string().optional(),
        },
        async execute(args) {
          switch (args.action) {
            case "log":
              if (!args.log) return "error: kern_optimize action=log requires log"
              return withTempFile("log.input.log", args.log, (file) => run(["log", file]))
            case "output":
              if (!args.text) return "error: kern_optimize action=output requires text"
              return withTempFile("output.text", args.text, (file) => run(["terse", file]))
            case "prompt":
            default:
              if (!args.prompt) return "error: kern_optimize action=prompt requires prompt"
              const flags: string[] = ["optimize"]
              if (args.attached_log) {
                return withTempFile("prompt.attached.log", args.attached_log, (file) =>
                  run([...flags, "--attach", file, args.prompt])
                )
              }
              if (args.session) flags.push("--session", args.session)
              if (args.model) flags.push("--model", args.model)
              return run([...flags, args.prompt])
          }
        },
      }),
      kern_fetch_raw_anchor: tool({
        description:
          "Two-tier context hydration: fetch raw uncompressed text segments that were truncated by kern (e.g. from an anchor marker like [kern: Truncated ... Anchor: anchor-xxx]). Allows AI agents to pull full original logs or code slices on-demand without hallucination or context bloat.",
        args: {
          anchor_id: tool.schema.string().describe("The anchor identifier emitted in the compressed output (e.g. anchor-a1b2c3d4e5f6)"),
        },
        async execute(args) {
          const flags: string[] = ["anchor", args.anchor_id]
          return run(flags)
        },
      }),
      kern_compact_file: tool({
        description:
          "Return a compact symbolic summary of a source file (functions, types, line numbers) instead of reading the whole file. Use before reading files in large codebases. Pass tier=full for the verbatim file, tier=folded for signatures+docstrings.",
        args: { path: tool.schema.string(), tier: tool.schema.string().optional() },
async execute(args) {
const flags: string[] = ["compact", args.path]
if (args.tier) flags.push("--tier", args.tier)
const cached = etagGet(etagKey(flags))
if (cached) flags.push("--etag", cached)
return run(flags)
},
      }),
      kern_fit_context: tool({
        description:
          "Adaptive token compressor: fits targeted source files, symbols, or queries into any specified token budget using tiered AST folding (Full Source -> Signatures + Docstrings -> Symbolic Summary). Prevents context truncation panics while maximizing code fidelity.",
        args: {
          root: tool.schema.string().optional(),
          max_tokens: tool.schema.string().optional(),
          files: tool.schema.string().optional(),
          symbols: tool.schema.string().optional(),
          query: tool.schema.string().optional(),
          format: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["fit-context"]
          if (args.max_tokens) flags.push("--max-tokens", String(args.max_tokens))
          if (args.files) flags.push("--file", args.files)
          if (args.symbols) flags.push("--symbol", args.symbols)
          if (args.query) flags.push("--query", args.query)
          if (args.format) flags.push("--format", args.format)
          if (args.root) flags.push("--root", args.root)
          return run(flags)
        },
      }),
      kern_project_map: tool({
        description:
          "Return a compressed map of a whole project: every source file with its symbols and line counts. Use instead of listing/reading every file in a repo.",
        args: {
          root: tool.schema.string().optional(),
          max_files: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["project"]
          if (args.max_files) {
            flags.push("--max-files", String(args.max_files))
          }
          flags.push(args.root ?? ".")
          return run(flags)
        },
      }),
      kern_buddy: tool({
        description:
          "Session onboarding digest for any agent: the project's conventions, layout, entry points and gotchas distilled from the index, docs and recent history. Call once at the start of a session on an unfamiliar repo.",
        args: {
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["buddy"]
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_stats: tool({
        description:
          "Return before/after token savings and cost estimates from kern optimizations, optionally filtered to today or a session.",
        args: {
          days: tool.schema.string().optional(),
          session: tool.schema.string().optional(),
          json: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["stats"]
          if (args.days) flags.push("--days", String(args.days))
          if (args.session) flags.push("--session", args.session)
          if (truthy(args.json)) flags.push("--json")
          return run(flags)
        },
      }),
      kern_semcache: tool({
        description:
          "Inspect and manage the semantic cache that serves similar (not just identical) prior queries instantly. Actions: stats (default) lists entries per namespace (prompt/log), list shows the stored inputs of a namespace, clear wipes it (or all), similarity reports the Jaccard overlap of two inputs so you can predict whether a near-duplicate will hit.",
        args: {
          action: tool.schema.string().optional(),
          namespace: tool.schema.string().optional(),
          a: tool.schema.string().optional(),
          b: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["semcache"]
          const action = args.action === "similarity" ? "sim" : args.action ?? "stats"
          flags.push(action)
          if (args.namespace) flags.push(args.namespace)
          if (action === "sim" && args.a && args.b) {
            flags.push(args.a, args.b)
          }
          return run(flags)
        },
      }),
      kern_review: tool({
        description:
          "Token-optimised code-review context for changed files: line-scoped changed symbols (with file:line spans), their callers, blast radius, risk and test gaps, sized to fit a token budget. The smallest answer a reviewer needs.",
        args: {
          root: tool.schema.string().optional(),
          range: tool.schema.string().optional(),
          file: tool.schema.string().optional(),
          max_tokens: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["review"]
          if (args.root) flags.push(args.root)
          if (args.range) flags.push("--range", args.range)
          if (args.file) flags.push("--file", args.file)
          if (args.max_tokens) flags.push("--max", String(args.max_tokens))
          return runPayload(flags)
        },
      }),
      kern_path: tool({
        description:
          "Shortest call path between two symbols, following in-project call edges in either direction. Traces how two things connect without reading files.",
        args: {
          root: tool.schema.string().optional(),
          from: tool.schema.string(),
          to: tool.schema.string(),
        },
        async execute(args) {
          const flags: string[] = ["path", args.from, args.to]
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_arch: tool({
        description:
          "Architecture overview from call-graph communities: subsystems with their hubs/packages, plus coupling warnings ranking the cross-community call bundles that make changes ripple.",
        args: {
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["arch"]
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_explore: tool({
        description:
          "Single-call explore: a symbol's verbatim source, direct call flow (callers + callees) and transitive blast radius (with affected files) in one shot. Replaces three separate calls for 'what touches this and how'.",
        args: {
          symbol: tool.schema.string(),
          depth: tool.schema.string().optional(),
          max: tool.schema.string().optional(),
          root: tool.schema.string().optional(),
          tier: tool.schema.string().optional(),
          start_line: tool.schema.string().optional(),
          end_line: tool.schema.string().optional(),
        },
        async execute(args) {
          const wantsFile = args.tier || args.start_line || args.end_line
          const dot = args.symbol.lastIndexOf(".")
          const sep = Math.max(args.symbol.lastIndexOf("/"), args.symbol.lastIndexOf("\\"))
          const isCodePath = dot > sep && CODE_EXTENSIONS.has(args.symbol.slice(dot + 1).toLowerCase()) &&
            existsSync(resolve(args.root ?? ".", args.symbol))
          if (wantsFile || isCodePath) {
            const start = Number(args.start_line) || 0
            const end = Number(args.end_line) || 0
            const cflags: string[] = ["compact", args.symbol]
            if (args.root) cflags.push("--root", args.root)
            if (start || end) cflags.push("--tier", "full")
            else if (args.tier) cflags.push("--tier", args.tier)
            const body = await run(cflags)
            if (!start && !end) return body
            const lines = body.replace(/\n$/, "").split("\n")
            const from = Math.max(start, 1)
            const to = Math.min(end >= from ? end : from + 199, lines.length)
            if (from > lines.length) return `error: start_line ${from} is past the end of ${args.symbol} (${lines.length} lines)`
            const out = [`${args.symbol} lines ${from}-${to} of ${lines.length}:`]
            for (let i = from; i <= to; i++) out.push(`${i}: ${lines[i - 1]}`)
            return out.join("\n")
          }
          const flags: string[] = ["explore", args.symbol]
          if (args.depth !== undefined) flags.push("--depth", String(args.depth))
          if (args.max !== undefined) flags.push("--max", String(args.max))
          if (args.root) flags.push(args.root)
          const cached = etagGet(etagKey(flags))
          if (cached) flags.push("--etag", cached)
          return run(flags)
        },
      }),
      kern_graph: tool({
        description:
          "One-call graph context: token-budgeted names-only adjacency for a symbol — callers first, then callees, every edge tagged EXTRACTED/INFERRED/AMBIGUOUS, plus community membership. Interface-method calls carry dispatch hints listing reachable concrete implementations. Set format=one-line for the single-line call-graph neighbourhood (the former kern_code_graph output).",
        args: {
          symbol: tool.schema.string(),
          max_tokens: tool.schema.string().optional(),
          format: tool.schema.string().optional(),
          entities: tool.schema.string().optional(),
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["graph", args.symbol]
          if (args.max_tokens !== undefined) flags.push("--max-tokens", String(args.max_tokens))
          if (args.format === "one-line") flags.push("--one-line")
          if (args.entities === "true" || args.entities === "1") flags.push("--entities")
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),kern_ast_search: tool({
        description:
          "AST-level symbol search across a Go project. Supports patterns like 'func greet', 'type *User*', 'method *', '*Handler*'. Returns definitions with file:line.",
        args: {
          pattern: tool.schema.string(),
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["ast", args.pattern]
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_search: tool({
        description:
          "Ranked free-text symbol search: returns symbols matching a query by name or file, best matches first. Forgiving lookup for humans — e.g. 'load index' or 'login handler'.",
        args: {
          query: tool.schema.string(),
          root: tool.schema.string().optional(),
          limit: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["search", args.query]
          if (args.root) flags.push(args.root)
          if (args.limit) flags.push("--limit", String(args.limit))
          return run(flags)
        },
      }),
      kern_why: tool({
        description:
          "Rationale and doc-reference report for a symbol: its doc comment, who depends on it and why (each caller's own doc line), and its in/out edge counts. Use to answer 'why does this exist and who needs it'.",
        args: {
          symbol: tool.schema.string(),
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["why", args.symbol]
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_context: tool({
        description:
          "Return the minimal relevant source slice for a symbol: its definition source, its callers, and what it calls. Use instead of reading an entire file.",
        args: {
          symbol: tool.schema.string(),
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["context", args.symbol]
          if (args.root) flags.push(args.root)
          const cached = etagGet(etagKey(flags))
          if (cached) flags.push("--etag", cached)
          return run(flags)
        },
      }),
kern_probe: tool({
        description:
          "Query-driven micro-context router: given a task (bug report, prompt, error text), extract the symbol names it mentions, resolve them against the index, and return a budget-capped bundle of definitions, callers, callees and tests. The graph is the retrieval index, never the payload.",
        args: {
          task: tool.schema.string(),
          root: tool.schema.string().optional(),
          max_tokens: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["probe", args.task]
          if (args.max_tokens) flags.push("--max", String(args.max_tokens))
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_plan_context: tool({
        description:
          "Deterministically plan which context to include for a change: classify the task type, score evidence classes by policy, and fit the selection to a token budget. Explainable — use json=true for the structured plan.",
        args: {
          change: tool.schema.string(),
          root: tool.schema.string().optional(),
          budget: tool.schema.string().optional(),
          json: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["explain-context", args.change]
          if (args.budget !== undefined) flags.push("--budget", String(args.budget))
          if (truthy(args.json)) flags.push("--json")
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_memory: tool({
        description:
          "Project memory brain — add a lesson, list stored lessons, recall by prompt, ranked recall with exponential time-decay (half-life), remove by index/prefix, or clear. Use to persist and retrieve cross-session project knowledge.",
        args: {
          action: tool.schema.string().describe("add|list|recall|ranked|remove|clear"),
          lesson: tool.schema.string().optional(),
          prompt: tool.schema.string().optional(),
          limit: tool.schema.string().optional(),
          k: tool.schema.string().optional(),
          half_life_days: tool.schema.string().optional(),
          id: tool.schema.string().optional(),
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          switch (args.action) {
            case "add":
              if (!args.lesson) return "error: kern_memory action=add requires lesson"
              const aflags: string[] = ["memory", "add", args.lesson]
              if (args.root) aflags.push("--root", args.root)
              return run(aflags)
            case "recall":
              if (!args.prompt) return "error: kern_memory action=recall requires prompt"
              const flags: string[] = ["memory", "recall", args.prompt]
              if (args.limit) flags.push("--limit", String(args.limit))
              if (args.root) flags.push("--root", args.root)
              return run(flags)
            case "ranked":
              if (!args.prompt) return "error: kern_memory action=ranked requires prompt"
              const rflags: string[] = ["memory-ranked", args.prompt]
              if (args.k) rflags.push("-k", String(args.k))
              if (args.half_life_days) rflags.push("--half-life", String(args.half_life_days))
              if (args.root) rflags.push("--root", args.root)
              return run(rflags)
            case "remove":
              if (!args.id) return "error: kern_memory action=remove requires id (1-based index or text prefix)"
              const dflags: string[] = ["memory", "remove", args.id]
              if (args.root) dflags.push("--root", args.root)
              return run(dflags)
            case "clear":
              const cflags: string[] = ["memory", "--clear"]
              if (args.root) cflags.push("--root", args.root)
              return run(cflags)
            case "list":
            default:
              const lflags: string[] = ["memory", "list"]
              if (args.root) lflags.push("--root", args.root)
              return run(lflags)
          }
        },
      }),
      kern_mask_pii: tool({
        description:
          "Locally scan text for secrets and PII (API keys, passwords, tokens, URLs with credentials, IPs, emails) and replace them with safe [MASKED_*] placeholders. Use before sending any text to a remote LLM. Pure local, deterministic, reversible.",
        args: {
          text: tool.schema.string(),
          mask_names: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["mask"]
          if (args.mask_names) flags.push("--names", args.mask_names)
          return withTempFile("pii.input.txt", args.text, (file) => run([...flags, file]))
        },
      }),
      kern_exec: tool({
        description:
          "Run code in an isolated local runtime and return ONLY stdout. Language from lang= or a shebang; runtimes from PATH. Fresh temp dir, hard timeout (default 15s; in MILLISECONDS), stdout cap (16KiB); HOME/XDG point into the sandbox, secrets stripped. Private network namespace (egress blocked) if user namespaces allow; otherwise env isolation. For exact answers without polluting context.",
        args: {
          code: tool.schema.string(),
          lang: tool.schema.string().optional(),
          timeout: tool.schema.string().optional(),
          max: tool.schema.string().optional(),
          stdin: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["exec", "--lang", args.lang ?? "python3"]
          if (args.timeout) flags.push("--timeout", String(Math.max(1, Math.ceil(args.timeout / 1000))))
          if (args.max) flags.push("--max", String(args.max))
          return withTempFile("exec.code", args.code, (file) => {
            if (args.stdin) {
              return withTempFile("exec.stdin", args.stdin, (stdinFile) =>
                runPayload([...flags, file, "--stdin", stdinFile], args.timeout, true)
              )
            }
            return runPayload([...flags, file], args.timeout, true)
          })
        },
      }),
      kern_doc: tool({
        description:
          "Local document toolkit over the project's doc index. action=search returns only the most relevant doc fragments (no whole-document pasting); action=fetch pulls a public docs page into the index — kern's ONLY network call (HTML-stripped, capped, cached); action=index pre-indexes after doc changes (searches auto-index on first use). semantic=true attaches dense embeddings via local Ollama.",
        args: {
          action: tool.schema.string().describe("search|fetch|index"),
          query: tool.schema.string().optional(),
          root: tool.schema.string().optional(),
          k: tool.schema.string().optional(),
          url: tool.schema.string().optional(),
          name: tool.schema.string().optional(),
          semantic: tool.schema.string().optional(),
        },
        async execute(args) {
          switch (args.action) {
            case "fetch":
              if (!args.url) return "error: kern_doc action=fetch requires url"
              const flags: string[] = ["docs", "fetch", args.url]
              if (args.name) flags.push(args.name)
              if (args.root) flags.push(args.root)
              if (truthy(args.semantic)) flags.push("--semantic")
              return run(flags)
            case "index":
              const iflags: string[] = ["docs", "index"]
              if (args.root) iflags.push(args.root)
              if (truthy(args.semantic)) iflags.push("--semantic")
              return run(iflags)
            case "search":
            default:
              if (!args.query) return "error: kern_doc action=search requires query"
              const sflags: string[] = ["docs", args.query]
              if (args.k) sflags.push("--limit", String(args.k))
              if (args.root) sflags.push(args.root)
              return run(sflags)
          }
        },
      }),
      kern_swap: tool({
        description:
          "Budget swapping: in a context document, replace fenced code blocks tagged `lang:path` with per-file symbolic signatures to fit a token budget, or expand `lang:path:summary` blocks back to full file contents. Returns the budget-fitted document.",
        args: {
          text: tool.schema.string(),
          root: tool.schema.string().optional(),
          mode: tool.schema.string().optional(),
          max_tokens: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["swap"]
          if (args.mode) flags.push("--mode", args.mode)
          if (args.max_tokens) flags.push("--max", String(args.max_tokens))
          if (args.root) flags.push(args.root)
          return withTempFile("swap-input.txt", args.text, (file) => run([...flags, file]))
        },
      }),
      kern_heal: tool({
        description:
          "Self-correction loop: run validation; on failure ask a local Ollama model to rewrite the failing files, apply the fix inside a throwaway snapshot, re-validate, and report a diff to review. Never edits the user's working tree. timeout is in MILLISECONDS.",
        args: {
          root: tool.schema.string().optional(),
          task: tool.schema.string().optional(),
          model: tool.schema.string().optional(),
          max_rounds: tool.schema.string().optional(),
          timeout: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["heal"]
          if (args.task) flags.push("--task", args.task)
          if (args.model) flags.push("--llm", args.model)
          if (args.max_rounds) flags.push("--max", String(args.max_rounds))
          if (args.timeout) flags.push("--timeout", String(Math.max(1, Math.ceil(args.timeout / 1000))))
          if (args.root) flags.push(args.root)
          return runPayload(flags, args.timeout)
        },
      }),
      kern_plan: tool({
        description:
          "Produce an implementation plan for a proposed change — affected files, dependencies, risks and required validation. Deterministic plan over the analysis; no LLM required.",
        args: {
          root: tool.schema.string().optional(),
          change: tool.schema.string(),
        },
        async execute(args) {
          const flags: string[] = ["plan"]
          if (args.root) flags.push("--root", args.root)
          return run([...flags, args.change])
        },
      }),
      kern_verify: tool({
        description:
          "Verify a change (build, unit tests, security, architecture, dependency) — returns the typed verdict (PASS/FAIL/WARN) and per-check summary. Compliance opt-in: cve/license/secrets (advisory; missing binary reports SKIPPED). Command mode: command=YOUR build/test/lint cmd with output=summary|failures|tail:N|lines:A-B|full; re-slice the kept full run with anchor.",
        args: {
          root: tool.schema.string().optional(),
          types: tool.schema.string().optional(),
          cve: tool.schema.boolean().optional(),
          license: tool.schema.boolean().optional(),
          secrets: tool.schema.boolean().optional(),
          command: tool.schema.string().optional(),
          output: tool.schema.string().optional(),
          anchor: tool.schema.string().optional(),
          timeout: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["verify"]
          if (args.root) flags.push("--root", args.root)
          if (args.types) flags.push(args.types)
          if (args.cve) flags.push("--cve")
          if (args.license) flags.push("--license")
          if (args.secrets) flags.push("--secrets")
          if (args.command) flags.push("--command", args.command)
          if (args.output) flags.push("--output", args.output)
          if (args.anchor) flags.push("--anchor", args.anchor)
          if (args.timeout) flags.push("--timeout", String(Math.round(args.timeout)))
          // Forward the requested deadline (seconds → ms) to the subprocess
          // ceiling too: without it runPayload clamps to the 2-minute default
          // and kills `kern verify` mid-run before the CLI's own --timeout
          // machinery can return a PARTIAL result — observed live as
          // "kern timed out after 120000ms" on a 400s request.
          return runPayload(flags, args.timeout ? args.timeout * 1000 : undefined)
        },
      }),
      kern_impact: tool({
        description:
          "HIGH-LEVEL: estimate the impact/blast-radius of a change to a symbol — transitively affected symbols/files/services/tests, deterministic risk, and typed claims. Read-only. Set risk=true to render the governance risk assessment for the change instead (the former kern_risk output).",
        args: {
          root: tool.schema.string().optional(),
          change: tool.schema.string(),
          kind: tool.schema.string().optional(),
          new_target: tool.schema.string().optional(),
risk: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["impact"]
          if (args.root) flags.push("--root", args.root)
          flags.push(args.change)
          if (args.risk === "true") flags.push("--risk")
          if (args.kind) flags.push(args.kind)
          if (args.new_target) flags.push(args.new_target)
          return run(flags)
        },
      }),
      kern_apply: tool({
        description:
          "Apply edits transactionally, verify, roll back on failure: the safe edit path. Multi-file {path, content} edits go through a sandboxed compile gate, then scoped `go test` on the modified packages; a verify failure restores the live tree. Returns a deterministic receipt hash.",
        args: {
          root: tool.schema.string().optional(),
          edits: tool.schema.string(),
          test: tool.schema.boolean().optional(),
          dry_run: tool.schema.boolean().optional(),
          format: tool.schema.string().optional(),
        },
        async execute(args) {
          // The edits JSON travels through a temp file (arg-length limits),
          // and apply's exit-1-with-report contract (failure findings)
          // needs preserveExit so the rolled-back report survives as the
          // tool result.
          return withTempFile("apply.edits", args.edits, (file) => {
            const flags: string[] = ["apply", file]
            if (args.root) flags.push("--root", args.root)
            if (args.test) flags.push("--test")
            if (args.dry_run) flags.push("--dry-run")
            if (args.format === "json") flags.push("--json")
            return runPayload(flags, 600_000, true)
          })
        },
      }),
                    kern_meta: tool({
        description:
          "Single entry point: describe what you need in natural language and kern classifies the request and runs the right tool(s) internally. Examples: 'how does dispatch work?' → kern_explore, 'what breaks if I change dispatch?' → kern_impact, 'find the dispatch function' → kern_search, 'show me the architecture' → kern_arch. Prefer it over calling individual kern_* tools.",
        args: {
          root: tool.schema.string().optional(),
          request: tool.schema.string(),
        },
        async execute(args) {
          const flags: string[] = ["meta", args.request]
          if (args.root) flags.push("--root", args.root)
          return run(flags)
        },
}),
kern_run: tool({
        description:
          "HIGH-LEVEL (Workflow E): run an intent through the full task pipeline — compile the intent, select workflow + capabilities + agents, create the Task, run policy preflight, and return the result (task, workflow, risk/approval, caps, tools, agents, next). Single entry point that orchestrates the whole workflow.",
        args: {
          root: tool.schema.string().optional(),
          intent: tool.schema.string(),
        },
        async execute(args) {
          const flags: string[] = ["run", args.intent]
          if (args.root) flags.push("--root", args.root)
          return run(flags)
        },
      }),
      kern_evidence: tool({
        description:
          "Signed-evidence read path + anchoring. action=verify validates a bundle (file or url) — tamper-seal, signature, audit-chain replay, optional trust-anchor match; action=explain renders it in plain language; action=export builds a bundle for a task; action=anchor validates a code claim (symbol, file:line), corrects drift, issues a SHA-256 certificate.",
        args: {
          action: tool.schema.string().optional(),
          file: tool.schema.string().optional(),
          url: tool.schema.string().optional(),
          task_id: tool.schema.string().optional(),
          expect_fingerprint: tool.schema.string().optional(),
          claim: tool.schema.string().optional(),
          line: tool.schema.string().optional(),
          symbol: tool.schema.string().optional(),
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          if (args.action === "anchor") {
            const aflags: string[] = ["evidence-anchor"]
            if (args.claim) aflags.push("--claim", args.claim)
            if (args.file) aflags.push("--file", args.file)
            if (args.line) aflags.push("--line", String(args.line))
            if (args.symbol) aflags.push("--symbol", args.symbol)
            if (args.root) aflags.push("--root", args.root)
            return run(aflags)
          }
          const flags: string[] = ["evidence", args.action === "explain" ? "explain" : args.action === "export" ? "export" : "verify"]
          if (args.file) flags.push("--file", args.file)
          if (args.url) flags.push("--url", args.url)
          if (args.task_id) flags.push("--task", args.task_id)
          if (args.expect_fingerprint) flags.push("--expect-fingerprint", args.expect_fingerprint)
          if (args.root) flags.push("--root", args.root)
          return run(flags)
        },
      }),
      // --- Shadow built-ins: route read/grep/glob/bash to kern transparently ---
      // A plugin tool with the same name as a built-in takes precedence, so the
      // agent's "read the file" call hits kern_compact_file under the hood.
      // Fail-closed parity with the shell guard: code-content
      // intents BLOCK with the guard's redirect message — a grep with an
      // include filter or regex metacharacters, a code-file read when kern is
      // unavailable, and a bash command whose first token is a content-reading
      // tool (grep/find/cat/...) never run raw and never sneak through kern
      // build. Execution-class commands stay governed: non-code reads serve
      // raw, simple content-free commands run raw, everything else routes
      // through kern build. The per-call raw/full/docs flags are operator-gated
      // (L4): they only take effect when the OPERATOR's host env sets
      // KERN_BYPASS=1 (or KERN_ENFORCE=0) — an agent passing raw=true in
      // the tool args alone no longer routes around kern.
      read: tool({
        description:
          "Read a file. Code files route to kern_compact_file (symbolic summary) regardless of size; if kern is unavailable the read is blocked with the kern redirect message. Non-code files (markdown/config/data) read raw.",
        args: {
          filePath: tool.schema.string(),
          full: tool.schema.string().optional(),
        },
        async execute(args) {
          if (process.env.KERN_BYPASS === "1" || process.env.KERN_ENFORCE === "0") {
            // Emergency bypass — read directly, no kern.
            logAdoption("read", false)
            return readFallback(args.filePath)
          }
// Simple reads are exempt from kern routing: only non-code files, which
// kern compact returns empty for anyway.
if (await isSimpleRead(args.filePath)) {
            logAdoption("read", false)
            return readFallback(args.filePath)
          }
          const flags: string[] = ["compact"]
          if (truthy(args.full) && process.env.KERN_BYPASS === "1") flags.push("--tier", "full")
          flags.push(args.filePath)
          // Same canonical key as kern_compact_file (compact|<path>), so a
          // repeat read of the same file collapses to the unchanged reply.
          const cached = etagGet(etagKey(flags))
          if (cached) flags.push("--etag", cached)
          try {
            const compacted = await run(flags)
            // kern compact returns EMPTY for non-code files (markdown,
            // config, data) — never silently show the agent nothing. Fall
            // back to the raw read so the content is always visible.
            if (!compacted || compacted.trim() === "") {
              logAdoption("read", false)
              return readFallback(args.filePath)
            }
            logAdoption("read", true)
            const pointerLine = "[kern] code file — symbolic summary served. verbatim: kern_compact_file tier=full · symbol slice: kern_context <symbol>"
            return `${compacted}\n${pointerLine}`
} catch {
// kern unavailable on a CODE file — block with the guard's
// redirect message (fail-closed; never a raw read).
// Non-code reads never reach here: isSimpleRead already served
// them raw, and the empty-compact fallback above is the non-code
// path. This catch is the code path only.
logAdoption("read", false)
// An operator-minted allowance (kern guard allow <hash>,
// tty-gated on the CLI side) unlocks this exact path once.
if (await consumeAllowOnce(args.filePath)) {
return readFallback(args.filePath)
}
// Auto-redirect: run the kern equivalent instead of
// denying — the subject (file path) is cleanly extractable here. Default
// mode: unchanged, the deny below stays.
if (process.env.KERN_GUARD_MODE === "auto-redirect") {
try {
const out = await run(["compact", args.filePath])
logAdoption("read", true)
return `${out}\n[kern] read redirected to kern_compact_file`
} catch {
// kern unavailable — fall through to the deny below
}
}
const subjHash = hashSubject(args.filePath)
throw new Error(`Use kern_explore with the file path as symbol (symbolic summary; add tier=full for the verbatim file, or start_line/end_line for a line window) instead of the built-in read; kern_compact_file does the same when KERN_MCP_FULL=1. Call kern_explore with {\"symbol\":\"<filepath>\"}. Set KERN_ENFORCE=0 to disable this guard. Operator allow-once: kern guard allow ${subjHash} (tty-gated), then retry.`)
}
        },
      }),
      glob: tool({
        description:
          "Find files by glob pattern. Routes to kern_project_map (symbol map) for repo exploration. Falls back to raw glob if kern is unavailable.",
        args: {
          pattern: tool.schema.string(),
          path: tool.schema.string().optional(),
          raw: tool.schema.string().optional(),
        },
        async execute(args) {
          if ((truthy(args.raw) && process.env.KERN_BYPASS === "1") || process.env.KERN_BYPASS === "1" || process.env.KERN_ENFORCE === "0") {
            logAdoption("glob", false)
            return globFallback(args.pattern, args.path ?? ".")
          }
          // A glob pattern with metacharacters can't be expressed by the kern
          // project symbol map — route it to the raw glob so the pattern is
          // honored instead of silently dropped.
          if (/[*?[\]{}]/.test(args.pattern)) {
            logAdoption("glob", false)
            return globFallback(args.pattern, args.path ?? ".")
          }
          try {
            const out = await run(["project", args.path ?? "."])
            logAdoption("glob", true)
            return out + "\n[kern] glob routed to kern_project_map"
          } catch {
            logAdoption("glob", false)
            return globFallback(args.pattern, args.path ?? ".")
          }
        },
      }),
grep: tool({
description:
"Search code by symbol query. Patterns are symbol queries, NOT regex — kern_ast_search routes them to AST symbol search by default. Patterns with an include filter or regex metacharacters are blocked — use kern_ast_search (symbols) or bash: kern ast <pattern> (CLI regex; plain bash grep is denied by the same guard).",
args: {
pattern: tool.schema.string(),
path: tool.schema.string().optional(),
include: tool.schema.string().optional(),
docs: tool.schema.string().optional(),
raw: tool.schema.string().optional(),
},
async execute(args) {
if ((truthy(args.raw) && process.env.KERN_BYPASS === "1") || process.env.KERN_BYPASS === "1" || process.env.KERN_ENFORCE === "0") {
logAdoption("grep", false)
return grepFallback(args.pattern, args.path, args.include)
}
if (truthy(args.docs) && process.env.KERN_BYPASS === "1") {
// Docs search is semantic/FTS over an index — the pattern is a
// query, not a regex, so metacharacters are left to kern.
try {
const flags: string[] = ["docs", args.pattern]
if (args.path) flags.push("--root", args.path)
const out = await run(flags)
logAdoption("grep", true)
return out
} catch {
// kern unavailable — block: never a node-fs scan. An
// operator-minted allowance for this exact pattern unlocks it once.
logAdoption("grep", false)
if (await consumeAllowOnce(args.pattern)) {
return grepFallback(args.pattern, args.path, args.include)
}
// Auto-redirect: run the kern equivalent instead of
// denying — the subject (pattern) is cleanly extractable here. Default
// mode: unchanged, the deny below stays.
if (process.env.KERN_GUARD_MODE === "auto-redirect") {
try {
const out = await run(["docs", args.pattern])
logAdoption("grep", true)
return `${out}\n[kern] grep redirected to kern_doc`
} catch {
// kern unavailable — fall through to the deny below
}
}
const subjHash = hashSubject(args.pattern)
throw new Error(`Use kern_search (code symbols — patterns are SYMBOL queries, not regex) instead of the built-in grep: call it with a symbol-name pattern like {\"pattern\":\"funcName\"}. For true regex search run bash: kern ast <pattern> (CLI; wildcards + kind prefixes) — a plain bash grep is denied by the same guard, not an escape (operator break-glass only: host env KERN_BYPASS=1). For docs use kern_doc (action=search; KERN_MCP_FULL=1). Set KERN_ENFORCE=0 to disable this guard. Operator allow-once: kern guard allow ${subjHash} (tty-gated), then retry.`)
}
}
if (args.include || /[[\]\\^$.|?*+()]/.test(args.pattern)) {
// Block with the guard's message: kern ast/docs searches
// have no include filter and take symbol queries, not regex — and no
// real grep runs here (the guard never raw-scans; the message documents
// the operator-gated bash raw=true escape). The same regex-metachar
// condition that used to fall through to a second grepFallback branch is
// covered here, so that dead branch is gone. An operator-minted
// allowance for this exact pattern unlocks it once.
logAdoption("grep", false)
if (await consumeAllowOnce(args.pattern)) {
return grepFallback(args.pattern, args.path, args.include)
}
// Auto-redirect: run the kern equivalent instead of
// denying — the subject (pattern) is cleanly extractable here. The redirect
// is deliberately lossy for regex/include patterns (kern takes symbol
// queries); the operator opted into that with the explicit env gate. Default
// mode: unchanged, the deny below stays.
if (process.env.KERN_GUARD_MODE === "auto-redirect") {
try {
const out = await run(["ast", args.pattern])
logAdoption("grep", true)
return `${out}\n[kern] grep redirected to kern_search`
} catch {
// kern unavailable — fall through to the deny below
}
}
const subjHash = hashSubject(args.pattern)
throw new Error(`Use kern_search (code symbols — patterns are SYMBOL queries, not regex) instead of the built-in grep: call it with a symbol-name pattern like {\"pattern\":\"funcName\"}. For true regex search run bash: kern ast <pattern> (CLI; wildcards + kind prefixes) — a plain bash grep is denied by the same guard, not an escape (operator break-glass only: host env KERN_BYPASS=1). For docs use kern_doc (action=search; KERN_MCP_FULL=1). Set KERN_ENFORCE=0 to disable this guard. Operator allow-once: kern guard allow ${subjHash} (tty-gated), then retry.`)
}
// Symbol query path: no include filter, no regex metacharacters — this is
// what kern_ast_search is for.
try {
const flags: string[] = ["ast", args.pattern]
if (args.path) flags.push("--root", args.path)
const out = await run(flags)
logAdoption("grep", true)
return out + "\n[kern] grep routed to kern_search (symbol query, not regex)"
} catch {
// kern unavailable — block: a node-fs scan would bypass
// the guard entirely. An operator-minted allowance for this exact
// pattern unlocks it once.
logAdoption("grep", false)
if (await consumeAllowOnce(args.pattern)) {
return grepFallback(args.pattern, args.path, args.include)
}
const subjHash = hashSubject(args.pattern)
throw new Error(`Use kern_search (code symbols — patterns are SYMBOL queries, not regex) instead of the built-in grep: call it with a symbol-name pattern like {\"pattern\":\"funcName\"}. For true regex search run bash: kern ast <pattern> (CLI; wildcards + kind prefixes) — a plain bash grep is denied by the same guard, not an escape (operator break-glass only: host env KERN_BYPASS=1). For docs use kern_doc (action=search; KERN_MCP_FULL=1). Set KERN_ENFORCE=0 to disable this guard. Operator allow-once: kern guard allow ${subjHash} (tty-gated), then retry.`)
}
        },
      }),
bash: tool({
        description:
          "Run a shell command. Content-free commands (git status, pwd, echo, ls, date) run raw. Reads (grep/cat/head/sed/find) are SERVED, never denied: symbol reads from the kern index, pure-read pipelines via kern exec --readonly. GATE commands (rm/mv/cp/dd/curl|sh/installers/git push --force) block. Everything else runs governed (kern build). timeout in MS (default 120000, max 1800000).",
        args: {
          command: tool.schema.string(),
          workdir: tool.schema.string().optional(),
          timeout: tool.schema.string().optional(),
          raw: tool.schema.string().optional(),
        },
        async execute(args) {
          const cmd = args.command.trim()
          if (cmd === "") return "error: empty command"
          // Simple commands are exempt from governed routing: read-only git
          // and trivial shell have no build/test/PII surface to govern.
          if (isSimpleCommand(cmd)) {
            logAdoption("bash", false)
            return runRaw(args.command, args.workdir, args.timeout)
          }
          if ((truthy(args.raw) && process.env.KERN_BYPASS === "1") || process.env.KERN_BYPASS === "1" || process.env.KERN_ENFORCE === "0") {
            logAdoption("bash", false)
            return runRaw(args.command, args.workdir, args.timeout)
          }
          // GATE commands (P2) are hard-denied: a command where ANY segment is
          // write/destructive/network/installer never runs through the guard —
          // the enforcement budget lives on the risky axis. The deny throws the
          // guard's own reason (byte-parity pinned by
          // TestPluginShadowGuardMessageParity) after consuming an operator
          // allow-once for the exact command. KERN_GUARD_MODE=deny restores the
          // uniform deny (reads included) for operators who want strictness.
          //
          // P3: the classification is a single shell-out to
          // `kern guard classify --json` — the Go source of truth (one
          // implementation of tokenization/prefix-strip/segment-split instead
          // of the former TS duplicate). The result carries verdict
          // (allow|gate|serve|governed), tier, head/pattern/file/tails (for
          // the serve), hint and hash.
          const classify = await classifyCmd(cmd)
          if (classify.verdict === "gate") {
            logAdoption("bash", false)
            if (await consumeAllowOnce(cmd)) {
              return runRaw(args.command, args.workdir, args.timeout)
            }
            // Ask Protocol (P1): an active grant (single-use approved record
            // or live prefix grant) covering this command lets it through —
            // same one-question store the exec gate uses, consulted via the
            // Go source of truth (`kern guard grant --check`). The grant is
            // consumed by the check exactly like an execution would.
            const root = process.env.KERN_ROOT ?? process.cwd()
            try {
              const grantCheck = await runWithExit(["guard", "grant", "--check", "--root", root, cmd], 10_000)
              if (grantCheck.exitCode === 0) {
                return runRaw(args.command, args.workdir, args.timeout)
              }
            } catch {
              // kern unavailable — the deny below stands (fail closed).
            }
            const clippedCmd = cmd.length > 80 ? cmd.slice(0, 80) + "…" : cmd
            const subjHash = classify.hash || hashSubject(cmd)
            throw new Error(`kern guard: blocked '${clippedCmd}' — GATE command (write/destructive/network/installer). Ask the operator, then retry byte-identical: run \`kern guard grant '<the exact blocked command>'\` (mints approval appr-<id>), the operator answers \`kern approval ask appr-<id> --root ${root}\` with [1] accept once / [2] accept prefix this session / [3] reject, await the decision with \`kern approval wait appr-<id> --timeout 30m\`, then retry the exact command. Escapes: KERN_BYPASS=1 / KERN_ENFORCE=0. Operator allow-once: kern guard allow ${subjHash} (tty-gated), then retry.`)
          }
          // READ commands (P1) are SERVED, never denied: a pure-read pipeline
          // has no write/network/destructive surface, so the guard answers it
          // from kern's index (Tier 1/2) or the readonly sandbox (Tier 3).
          // KERN_GUARD_MODE=deny skips the serve and falls to the deny below.
          // An "allow" verdict (simple content-free) also falls through raw.
          const plan = process.env.KERN_GUARD_MODE === "deny" || classify.verdict !== "serve" ? null : classify
          if (plan) {
            // Tier 1: single translatable read — symbol grep → kern ast
            // (grep-shaped answer), file read → kern compact, find → kern buddy.
            if (plan.tier === 1) {
              try {
                let out = ""
                if (plan.head === "find") {
                  out = await run(["buddy"])
                } else if (plan.pattern !== undefined) {
                  out = await run(["ast", plan.pattern])
                } else if (plan.file !== undefined) {
                  out = await run(["compact", plan.file])
                }
                logAdoption("bash", true)
                return `${out}\n[kern] bash served from kern index (Tier 1: ${plan.head})`
              } catch {
                // kern unavailable or the mapping failed — fall through to Tier 3
              }
            }
            // Tier 2: read head + simple tail → serve the head from the index,
            // apply the tail in-process, return the COMPUTED answer (wc -l
            // yields the count, not a line list).
            if (plan.tier === 2 && plan.pattern !== undefined && plan.tails) {
              try {
                const head = await run(["ast", plan.pattern])
                let lines = head.split("\n").filter((l) => l.trim() !== "")
                for (const tail of plan.tails) {
                  const t = tail.trim()
                  if (/^wc\b/.test(t) && /\s-l(\s|$)/.test(t)) {
                    lines = [String(lines.length)]
                  } else if (/^head\b/.test(t)) {
                    const m = /^head\s+(-n\s*)?(\d+)/.exec(t)
                    lines = lines.slice(0, m ? parseInt(m[2], 10) : 10)
                  } else if (/^tail\b/.test(t)) {
                    const m = /^tail\s+(-n\s*)?(\d+)/.exec(t)
                    lines = lines.slice(-(m ? parseInt(m[2], 10) : 10))
                  } else if (/^grep\b/.test(t) && /-v\b/.test(t)) {
                    const m = /grep\s+.*-v\s+['"]?([^'"]+)['"]?/.exec(t)
                    if (m) lines = lines.filter((l) => !l.includes(m[1]))
                  } else if (/^grep\b/.test(t)) {
                    const m = /grep\s+['"]?([^'"]+)['"]?/.exec(t)
                    if (m) lines = lines.filter((l) => l.includes(m[1]))
                  }
                }
                logAdoption("bash", true)
                return `${lines.join("\n")}\n[kern] bash served from kern index (Tier 2: computed)`
              } catch {
                // kern unavailable or the mapping failed — fall through to Tier 3
              }
            }
            // Tier 3: untranslatable pure read → kern exec --readonly (P3b):
            // sandboxed (network isolated), PII-masked, output-shaped, kept and
            // re-sliceable. The readonly exec runs scripts in a temp cwd, so
            // cd into the project/workdir first.
            try {
              const q = (s: string) => `'${s.replace(/'/g, `'\\''`)}'`
              const wd = args.workdir || directory
              const { text, exitCode } = await runWithExit(["exec", "--readonly", "--lang", "sh", `cd ${q(wd)} && ${cmd}`], args.timeout)
              logAdoption("bash", true)
              const body = exitCode === 0 ? text : `[exit ${exitCode}]\n${text}`
              return `${body}\n[kern] bash read served sandboxed (kern exec --readonly, Tier 3)`
            } catch (err) {
              const e = err as { message?: string; stdout?: Buffer | Uint8Array; stderr?: Buffer | Uint8Array }
              const text = [e.message, e.stdout?.toString(), e.stderr?.toString()].filter(Boolean).join("\n")
              if (/blocked|denied|allowlist|approval|firewall|governance|not permitted|refused/i.test(text)) {
                // The readonly firewall refused the read (e.g. a read disguised
                // as a write) — surface the denial, never fall back raw.
                logAdoption("bash", true)
                return text
              }
              // kern is entirely unavailable: a pure-read has no risky surface,
              // so serving it raw with a one-line note is the fail-open read
              // posture (writes/exec stay fail-closed).
              logAdoption("bash", false)
              const raw = await runRaw(args.command, args.workdir, args.timeout)
              return `${raw}\n[kern] kern unavailable — raw read served (pure-read pipeline)`
            }
          }
          // kern build runs any command (sh -c) in the project dir, gated by
          // the governance firewall, and returns compact output. kern exec is
          // NOT used for this: it executes code in an isolated temp sandbox
          // (wrong cwd, no network, stripped env), which would break ordinary
          // shell usage — so every command goes through the same governed path.
          try {
            const flags: string[] = ["build", cmd]
            if (args.workdir) {
              // Fail fast with a clear reason: a missing workdir makes the
              // governed path's shell fail with an error that names neither
              // the directory nor the cause ("fork/exec /bin/sh: no such
              // file or directory" — observed live). Return (not throw) so
              // the host renders the message instead of dropping it.
              if (!existsSync(args.workdir)) {
                logAdoption("bash", false)
                return `error: workdir does not exist: ${args.workdir} — create it first`
              }
              flags.push("--dir", args.workdir)
            }
            if (args.timeout) flags.push("--timeout", String(Math.max(1, Math.ceil(args.timeout / 1000))))
            // Pass the agent's budget (ms) through so the governed path honors
            // it instead of the 2-minute default ceiling. runWithExit captures
            // the full output and the real exit code: a failed command
            // returns its output + an exit-code line instead of throwing into
            // the host's bare "Failed with exit code N" rendering (which drops
            // the payload AND the denial reason — observed live).
const { text, exitCode } = await runWithExit(flags, args.timeout)
logAdoption("bash", true)
if (exitCode === 0) return text
return governedBashBanner(exitCode, text)
          } catch (err) {
            // Never silently bypass the exec firewall: if the governed path
            // was DENIED (no KERN_TOOLS allowlist / no KERN_ALLOW_EXEC, or an
            // approval-required risk), surface the denial instead of falling
            // back to raw shell — a raw run would execute the command
            // ungoverned while the agent believes it was governed. Also never
            // re-run raw after a governed TIMEOUT: that would double-execute a
            // possibly stateful command with a shorter, differently-united
            // budget. The raw fallback is reserved for genuine unavailability:
            // the kern binary itself is missing.
            const e = err as { message?: string; stdout?: Buffer | Uint8Array; stderr?: Buffer | Uint8Array }
            const text = [e.message, e.stdout?.toString(), e.stderr?.toString()].filter(Boolean).join("\n")
if (/blocked|denied|allowlist|approval|firewall|governance|not permitted|refused|timed out|timeout/i.test(text)) {
// Served by kern's governed path (denial/timeout) — still
// kern-routed. Return the diagnostic instead of throwing: the
// host renders thrown tool errors as a bare failure with no
// payload, dropping the denial/timeout reason (observed live).
logAdoption("bash", true)
return text || (err instanceof Error ? err.message : String(err))
}
if (!existsSync(bin)) {
// kern itself is unavailable — no governed path. Content-intent
// commands never reach here (the deny above fires regardless of
// kern availability); everything else falls back raw as before.
logAdoption("bash", false)
return runRaw(args.command, args.workdir, args.timeout)
}
// Genuine governed-path failure with the kern binary present —
// same delivery: the diagnostic becomes the tool result.
logAdoption("bash", true)
return text || (err instanceof Error ? err.message : String(err))
          }
        },
      }),
    }),

    // Subagent kern access (verified live 2026-09-29): opencode 1.18.x
    // built-in agents (explore/explorer/fixer/designer/councillor) ship their
    // own "*": "deny" catch-all and/or explicit "kern_*": "deny" rules that
    // hide the kern_* MCP tools from subagents — a top-level global
    // permission block does NOT override those at runtime (the built-in deny
    // wins). Appending the allow to EVERY agent's own permission set here
    // makes the tools visible to all current and future agents: per-agent
    // permission is merged last at agent construction, and iterating whatever
    // agents exist in the merged config keeps this dynamic across opencode
    // updates (no per-agent enumeration, no maintenance when new agent types
    // appear). kern_* only — other MCP servers (e.g. codegraph) are out of
    // scope for this plugin.
    //
    // Subagent phase shaping (investigated 2026-09-29, best-effort only):
    // the goal was to also give every subagent a phase-filtered
    // KERN_MCP_PHASE env (explore/explorer/fixer/councillor/designer →
    // "explore", general/scout → default surface) so filterToolSurface()
    // would shape what each subagent sees. That is NOT injectable from
    // here: opencode's AgentConfig (@opencode-ai/plugin 1.18.32 SDK types)
    // has no per-agent env field — only MCP server configs carry
    // `environment`, and that is per-server (global), not per-agent. The
    // plugin process also shares one env across all agents in a session,
    // and filterToolSurface() runs once at plugin registration. Phase
    // shaping therefore stays operator-controlled via the host env that
    // launches opencode. The kern_* permission grant below is the
    // load-bearing part and must not be broken.
    config: (cfg) => {
      const agents = cfg.agent ?? {}
      for (const name of Object.keys(agents)) {
        const agent = agents[name]
        if (!agent) continue
        agent.permission = {
          ...(agent.permission ?? {}),
          "kern_*": "allow",
        }
      }
    },

    // Auto-compress large tool outputs before they enter context.
    "tool.execute.after": async (input, output) => {
      const text = typeof output?.output === "string" ? output.output : ""

      // Session capture: edits become project-memory lessons so a compacted
      // or fresh session can recover what was touched.
      try {
        if (EDIT_TOOLS.has(input.tool)) {
          const fp = fileArg(input.args)
          if (fp) await remember(`Edited ${fp}`)
        } else if (input.tool === "bash" && text.length < 4000 && text.length > 0) {
          const first = text.split("\n").find((l) => FAIL_RE.test(l))
          if (first && first.trim().length > 0) {
            await remember(`Command failed: ${first.trim()}`)
          }
        } else {
          // Read-only kern exploration: record what the session learned
          // (no-op for every other tool name).
          await rememberExploration(input.tool, input.args, text, remember)
        }
      } catch {
        /* swallow */
      }

      // Compression: only bash/read/grep outputs above the threshold.
      if (input.tool !== "bash" && input.tool !== "read" && input.tool !== "grep") {
        return
      }
      // JSON is data, not log noise — the log-stripper strips structural
      // braces out of it and silently corrupts it (live: `kern mcp tools
      // --json` and reads of large .json files came back unparseable). Guard
      // on the trimmed first byte BEFORE the command-class checks below so
      // every JSON-shaped output is exempted regardless of which tool
      // produced it.
      const trimmedText = text.trimStart()
      if (trimmedText.startsWith("{") || trimmedText.startsWith("[")) {
        return
      }
      // Governed-bash output wraps the child output in a "cmd: <command>"
      // header line; a JSON document behind that header is still data, not
      // log noise (live: a governed `kern audit --json` came back mangled by
      // the log-stripper). Exempt it too.
      if (trimmedText.startsWith("cmd: ")) {
        const nl = trimmedText.indexOf("\n")
        const rest = nl >= 0 ? trimmedText.slice(nl + 1).trimStart() : ""
        if (rest.startsWith("{") || rest.startsWith("[")) {
          return
        }
      }
      // A bash command that IS a kern read-only view (buddy digest, search,
      // explore, impact, graph, ast, stats, mcp tools, version, doctor,
      // health, plus the etag-eligible structured views context/compact/
      // retrieve/why/inherits — the full family now reachable raw since the
      // guard's all-kern passthrough) returns kern-shaped structured text,
      // not log noise — running the log-stripper over it interleaves its
      // sections into garbage (deep-verify L3 F3-8: a long `kern buddy`
      // digest came back mangled while the raw output was clean).
      // Verify/build outputs stay compressible: they wrap real command logs.
      if (input.tool === "bash") {
        const bashArgs = input.args as { command?: string } | undefined
        const bashCmd = typeof bashArgs?.command === "string" ? bashArgs.command : ""
        if (/^\s*kern\s+(--version|version|doctor|health|buddy|search|explore|impact|graph|ast|stats|context|compact|retrieve|why|inherits|mcp tools)\b/.test(bashCmd)) {
          return
        }
      }
      // Verbatim-request exemptions: a full=true read (or raw=true grep/bash)
      // only yields raw content under the operator's KERN_BYPASS=1 — only
      // then skip lossy compression (the flags are otherwise ignored).
      const hookArgs = input.args as { full?: string; raw?: string } | undefined
      if (process.env.KERN_BYPASS === "1" && input.tool === "read" && truthy(hookArgs?.full)) return
      if (process.env.KERN_BYPASS === "1" && (input.tool === "grep" || input.tool === "bash") && truthy(hookArgs?.raw)) return
      if (text.length < DEFAULT_COMPACT_THRESHOLD) return
      try {
        const compressed = await withTempFile("tool-output.txt", text, (file) => run(["log", file]))
        if (!compressed || compressed.trim() === "") return
        output.output = SILENT_MODE
          ? compressed
          : compressedFooter(text.length, compressed.length) + compressed
      } catch {
        // Never break tool execution on optimizer failure.
      }
    },

    // P0-2: capture user decisions at low volume so the session's direction
    // survives compaction. Only brief, substantive prompts are recorded;
    // questions, tiny messages, repeated prompts and rapid-fire chatter are
    // skipped so project memory isn't flooded (that would evict real lessons).
    "chat.message": async (input, output) => {
      try {
        const sessionID = (input as any)?.sessionID ?? ""
        const parts: any[] = (output.parts as any[]) ?? []
        let text = ""
        for (const p of parts) {
          if (typeof p.text === "string") text += p.text
          else if (typeof p.content === "string") text += p.content
        }
        text = text.trim()
        // Silent injection (Phase C): remember the current message text and
        // precompute its context envelope so the system.transform hook can
        // append it before the model's first response of the session.
        currentMessageText = text
        if (INJECT_ENABLED && sessionID && !injectedSessions.has(sessionID) && text.length >= INJECT_MIN_TEXT) {
          void envelopeFor(text)
        }
        if (!text || text.length < 16 || text.length > 600) return
        if (text.endsWith("?")) return
        // Mirror internal/hook/hook.go rememberPrompt (F2): command-ish
        // lines (leading "/") and system-reminder noise never enter memory.
        if (text.startsWith("/")) return
        if (/<system-reminder>|\[system-reminder\]/i.test(text)) return
        const lower = text.toLowerCase()
        if (/\b(remember|forget)\b/.test(lower)) return
        const now = Date.now()
        if (text === lastChatText || now - lastChatAt < 60_000) return
        lastChatAt = now
        lastChatText = text
        await remember(`User: ${text}`, 200)
      } catch {
        /* swallow */
      }
    },

    // Silent context injection (Phase C): append the planner-selected context
    // envelope to the system prompt before the model's FIRST response of the
    // session. The message text comes from currentMessageText (set by
    // chat.message before the model request is built); the envelope is served
    // from the precomputed cache or the single in-flight computation. Fail-
    // closed: a miss, timeout, or error injects nothing and never blocks the
    // host call. The session set + dedupe guard keep the block from being
    // appended more than once across the turn's model requests.
    "experimental.chat.system.transform": async (input, output) => {
      try {
        if (!INJECT_ENABLED) return
        const sessionID = (input as any)?.sessionID ?? ""
        if (!sessionID || injectedSessions.has(sessionID)) return
        const text = currentMessageText
        if (!text || text.length < INJECT_MIN_TEXT) return
        const block = await envelopeFor(text)
        if (!block) return
        if ((output.system ?? []).some((s) => s.startsWith("[kern context"))) return
        output.system = [...(output.system ?? []), block]
        injectedSessions.add(sessionID)
      } catch {
        /* swallow */
      }
    },
  }
}) satisfies Plugin
