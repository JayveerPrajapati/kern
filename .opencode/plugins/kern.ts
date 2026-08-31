import { type Plugin, tool } from "@opencode-ai/plugin"
import { resolve, relative } from "node:path"
import { existsSync } from "node:fs"
import { writeFile, rm, readFile, readdir, stat } from "node:fs/promises"
import { tmpdir } from "node:os"
import { randomBytes } from "node:crypto"

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

// --- Session event capture (P0-2, borrowed from mksglu/context-mode) ---
// Record what the session did (file edits, failing commands) into project
// memory so that after a context compaction — or in a fresh session — the
// agent can recall its own recent state via kern buddy / kern_memory_list.

const EDIT_TOOLS = new Set(["edit", "write", "patch", "apply_patch", "update_file"])
// The FAIL_RE marks command output that indicates a real failure. It only
// matches error markers at line start (or specific failure phrases) so a
// successful command that merely mentions "error" isn't mislogged.
const FAIL_RE = /^\s*(?:error|fatal|panic|failed|cannot)[:;]|panic:|command not found|: No such file|no such file or directory/i

// Rate-limit and dedupe chat.message memory capture so project memory isn't
// flooded by ordinary conversation (evicting real lessons).
let lastChatAt = 0
let lastChatText = ""

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

  const run = async (args: string[], timeoutMs?: number): Promise<string> => {
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
    return out.stdout.toString()
  }

  // Raw shell fallback for the shadow bash tool: run the command through the
  // host `$` shell (sh -c) exactly as opencode's built-in bash would, with the
  // same timeout ceiling as `run` (timeout is milliseconds, the opencode bash
  // convention). Only used when kern is unavailable.
  const runRaw = async (command: string, workdir?: string, timeout?: number): Promise<string> => {
    const p = $`sh -c ${command}`
    if (workdir) p.cwd(workdir)
    const ms = ceilingMs(timeout)
    const out = typeof (p as any).timeout === "function"
      ? await p.timeout(ms).quiet()
      : await withTimeout(p.quiet(), ms)
    return out.stdout.toString()
  }

  // Some commands (kern sec, kern delete, kern guard check) print their
  // payload to stdout and then exit non-zero as a secondary signal. Surface
  // the payload instead of treating the exit code as a hard failure.
async function runPayload(args: string[], timeoutMs?: number, preserveExit = false): Promise<string> {
    // Several kern CLI commands print their full report to stdout and then
    // exit non-zero BY DESIGN as a CI signal (kern changes/review exit 1 when
    // risk > 0, kern security on error findings, kern validate on FAILED,
    // kern guard check exits 2 on violations, kern build/heal/sandbox/exec on
    // command failure). The report must survive as the tool result. Capture it
    // via sh -c with output redirection and a forced exit 0: some host `$`
    // shells reject redirect tokens in the template literal itself AND do not
    // attach stdout to the rejection error, either of which would lose the
    // report. With preserveExit=true the forced exit 0 is dropped and a
    // non-zero exit throws with the captured output, so callers that need the
    // real exit status (kern_exec: "stderr is only surfaced on failure") get it.
    const sq = (s: string) => `'${s.replace(/'/g, `'\\\\''`)}'`
    const outFile = resolve(tmpdir(), `kern-shadow-${process.pid}-${randomBytes(4).toString("hex")}.out`)
    try {
      const cmdStr = `${sq(bin)} ${args.map(sq).join(" ")} > ${sq(outFile)} 2>&1${preserveExit ? "" : "; exit 0"}`
      const p = $`sh -c ${cmdStr}`
      try {
        if (typeof (p as any).timeout === 'function') await p.timeout(ceilingMs(timeoutMs)).quiet()
        else await withTimeout(p.quiet(), ceilingMs(timeoutMs))
      } catch (err) {
        if (preserveExit) throw new Error(await readFile(outFile, 'utf8'))
        throw err
      }
      return await readFile(outFile, 'utf8')
    } finally {
      await rm(outFile, { force: true })
    }
  }

  // Best-effort: a memory-log failure must never break the host tool call.
  async function remember(lesson: string, maxLen = 300): Promise<void> {
    try {
      const text = lesson.replace(/\s+/g, " ").trim()
      if (!text) return
      const clipped = text.length > maxLen ? text.slice(0, maxLen).trimEnd() + "…" : text
      await run(["remember", clipped])
    } catch {
      /* swallow */
    }
  }

  return {
    tool: {
      kern_optimize_prompt: tool({
        description:
          "Compress and clean a raw prompt before sending it to an LLM. Returns the optimized prompt plus token savings. Use this to reduce context cost for large or noisy prompts.",
        args: {
          prompt: tool.schema.string(),
          attached_log: tool.schema.string().optional(),
          session: tool.schema.string().optional(),
          model: tool.schema.string().optional(),
        },
        async execute(args, context) {
          const flags: string[] = ["optimize"]
          if (args.attached_log) {
            return withTempFile("prompt.attached.log", args.attached_log, (file) =>
              run([...flags, "--attach", file, args.prompt])
            )
          }
          if (args.session) flags.push("--session", args.session)
          if (args.model) flags.push("--model", args.model)
          return run([...flags, args.prompt])
        },
      }),
      kern_compact_file: tool({
        description:
          "Return a compact symbolic summary of a source file (functions, types, line numbers) instead of reading the whole file. Use before reading files in large codebases.",
        args: { path: tool.schema.string() },
        async execute(args) {
          return run(["compact", args.path])
        },
      }),
      kern_project_map: tool({
        description:
          "Return a compressed map of a whole project: every source file with its symbols and line counts. Use instead of listing/reading every file in a repo.",
        args: {
          root: tool.schema.string().optional(),
          max_files: tool.schema.number().optional(),
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
      kern_pack: tool({
        description:
          "Pack a whole project into one paste-ready bundle: project instructions, a directory tree with per-file token counts, and file contents, sized to fit a token budget. Use when an agent needs the full source to edit against, not just a map.",
        args: {
          root: tool.schema.string().optional(),
          max_tokens: tool.schema.number().optional(),
          no_instructions: tool.schema.boolean().optional(),
          out: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["pack"]
          flags.push("--max-tokens", String(args.max_tokens ?? 8000))
          if (args.no_instructions) flags.push("--no-instructions")
          if (args.out) flags.push("--out", args.out)
          flags.push(args.root ?? ".")
          return run(flags)
        },
      }),
      kern_run_build: tool({
        description:
          "Run a build/test command locally and return only the compact result (exit status + errors), not full output. Use for builds, tests, linting to save context. timeout is in MILLISECONDS (default 120000, max 1800000).",
        args: {
          command: tool.schema.string(),
          dir: tool.schema.string().optional(),
          timeout: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["build"]
          if (args.dir) flags.push("--dir", args.dir)
          if (args.timeout) flags.push("--timeout", String(Math.max(1, Math.ceil(args.timeout / 1000))))
          return runPayload([...flags, args.command], args.timeout)
        },
      }),
      kern_optimize_log: tool({
        description:
          "Strip noise from log output: keeps errors, warnings, stack traces and build failures, removes timestamps and chatter. Use before pasting logs into context.",
        args: { log: tool.schema.string() },
        async execute(args) {
          return withTempFile("log.input.log", args.log, (file) => run(["log", file]))
        },
      }),
      kern_optimize_output: tool({
        description:
          "Compress an LLM's response (assistant output) by stripping filler, pleasantries and hedge language while preserving code blocks, lists, errors and technical content. Deterministic and local, no LLM involved. Use on verbose model replies before they are stored or echoed back into context.",
        args: { text: tool.schema.string() },
        async execute(args) {
          return withTempFile("output.text", args.text, (file) => run(["terse", file]))
        },
      }),
      kern_stats: tool({
        description:
          "Return before/after token savings and cost estimates from kern optimizations, optionally filtered to today or a session.",
        args: {
          days: tool.schema.number().optional(),
          session: tool.schema.string().optional(),
          json: tool.schema.boolean().optional(),
        },
        async execute(args) {
          const flags: string[] = ["stats"]
          if (args.days) flags.push("--days", String(args.days))
          if (args.session) flags.push("--session", args.session)
          if (args.json) flags.push("--json")
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
      kern_changes: tool({
        description:
          "Line-aware change-impact analysis for a diff: scopes each changed file to the symbols its added lines actually touch (from git diff hunks), then computes blast radius (transitive callers), risk scores, and test gaps. Use to review what a PR could break before reading files.",
        args: {
          root: tool.schema.string().optional(),
          range: tool.schema.string().optional(),
          file: tool.schema.string().optional(),
          json: tool.schema.boolean().optional(),
        },
        async execute(args) {
          const flags: string[] = ["changes"]
          if (args.root) flags.push(args.root)
          if (args.range) flags.push("--range", args.range)
          if (args.file) flags.push("--file", args.file)
          if (args.json) flags.push("--json")
          return runPayload(flags)
        },
      }),
      kern_review: tool({
        description:
          "Token-optimised code-review context for changed files: line-scoped changed symbols (with file:line spans), their callers, blast radius, risk and test gaps, sized to fit a token budget. The smallest answer a reviewer needs.",
        args: {
          root: tool.schema.string().optional(),
          range: tool.schema.string().optional(),
          file: tool.schema.string().optional(),
          max_tokens: tool.schema.number().optional(),
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
      kern_hubs: tool({
        description:
          "Architectural hotspots: the most depended-on symbols (hubs) and cross-package bridges where a change in one subsystem can break another.",
        args: {
          root: tool.schema.string().optional(),
          limit: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["hubs"]
          if (args.root) flags.push(args.root)
          if (args.limit) flags.push("--limit", String(args.limit))
          return run(flags)
        },
      }),
      kern_test_gaps: tool({
        description:
          "Test-coverage analysis from the call graph: what percent of callable symbols are exercised by tests, plus untested hotspots (called by many, covered by none).",
        args: {
          root: tool.schema.string().optional(),
          limit: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["testgaps"]
          if (args.root) flags.push(args.root)
          if (args.limit) flags.push("--limit", String(args.limit))
          return run(flags)
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
      kern_dead: tool({
        description:
          "Dead-code detection: symbols nothing in the project calls. Private names are dead for certain; public names may be external API. Sorted by size so the biggest cleanup wins show first. Callers reached through function values or interface dispatch are invisible to the index and are reported as dead — confirm before removing.",
        args: {
          root: tool.schema.string().optional(),
          limit: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["dead"]
          if (args.root) flags.push(args.root)
          if (args.limit) flags.push("--limit", String(args.limit))
          return run(flags)
        },
      }),
      kern_larges: tool({
        description:
          "Find the largest function/method declarations by source lines. Use to locate god functions that beg for refactoring.",
        args: {
          root: tool.schema.string().optional(),
          min_lines: tool.schema.number().optional(),
          limit: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["larges"]
          if (args.root) flags.push(args.root)
          if (args.min_lines) flags.push("--lines", String(args.min_lines))
          if (args.limit) flags.push("--limit", String(args.limit))
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
      kern_communities: tool({
        description:
          "Call-graph communities (label propagation): which symbols cluster together as subsystems, with each cluster's size and hub. Use to name the architecture's parts before refactoring.",
        args: {
          root: tool.schema.string().optional(),
          limit: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["communities"]
          if (args.root) flags.push(args.root)
          if (args.limit) flags.push("--limit", String(args.limit))
          return run(flags)
        },
      }),
      kern_churn: tool({
        description:
          "Change-frequency risk: which files were touched by the most commits in a range, whether they are being edited right now, and how risky they are in the call graph.",
        args: {
          root: tool.schema.string().optional(),
          range: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["churn"]
          if (args.root) flags.push(args.root)
          if (args.range) flags.push("--range", args.range)
          return run(flags)
        },
      }),
      kern_explore: tool({
        description:
          "Single-call explore: a symbol's verbatim source, direct call flow (callers + callees) and transitive blast radius (with affected files) in one shot. Replaces three separate calls for 'what touches this and how'.",
        args: {
          symbol: tool.schema.string(),
          depth: tool.schema.number().optional(),
          max: tool.schema.number().optional(),
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["explore", args.symbol]
          if (args.depth !== undefined) flags.push("--depth", String(args.depth))
          if (args.max !== undefined) flags.push("--max", String(args.max))
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_bridges: tool({
        description:
          "Bridge detection: symbols called from two or more distinct packages/directories — the coupling points where a change in one subsystem can break another.",
        args: {
          root: tool.schema.string().optional(),
          limit: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["bridges"]
          if (args.root) flags.push(args.root)
          if (args.limit !== undefined) flags.push("--limit", String(args.limit))
          return run(flags)
        },
      }),
      kern_cochange: tool({
        description:
          "Co-change coupling: which files are actually edited in the same commits (from git history), independent of the call graph. Use before a commit to see what else must change in lockstep.",
        args: {
          root: tool.schema.string().optional(),
          range: tool.schema.string().optional(),
          limit: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["cochange"]
          if (args.root) flags.push(args.root)
          if (args.range) flags.push("--range", args.range)
          if (args.limit !== undefined) flags.push("--limit", String(args.limit))
          return run(flags)
        },
      }),
      kern_fts_search: tool({
        description:
          "FTS5 full-text search over the persisted SQLite symbol index. Supports MATCH syntax ('greet', 'func AND greet'). Requires a build with -tags sqlite.",
        args: {
          query: tool.schema.string(),
          root: tool.schema.string().optional(),
          limit: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["fts", args.query]
          if (args.root) flags.push(args.root)
          if (args.limit !== undefined) flags.push("--limit", String(args.limit))
          return run(flags)
        },
      }),
      kern_near: tool({
        description:
          "Dependency-tree expansion: every symbol within N hops of a symbol, in both directions (callers + callees), budget-capped. The graph-guided traversal primitive that replaces blind grep — e.g. 'everything two degrees from this database model' in one call.",
        args: {
          symbol: tool.schema.string(),
          depth: tool.schema.number().optional(),
          max: tool.schema.number().optional(),
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["near", args.symbol]
          if (args.depth) flags.push("--depth", String(args.depth))
          if (args.max) flags.push("--max", String(args.max))
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_graph: tool({
        description:
          "One-call graph context: token-budgeted names-only adjacency for a symbol — callers first (the direction that matters for impact), then callees, every edge tagged EXTRACTED/INFERRED/AMBIGUOUS, plus community membership. Calls to interface methods carry dispatch hints listing the concrete implementations they can reach.",
        args: {
          symbol: tool.schema.string(),
          max_tokens: tool.schema.number().optional(),
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["graph", args.symbol]
          if (args.max_tokens !== undefined) flags.push("--max-tokens", String(args.max_tokens))
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_ast_search: tool({
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
          limit: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["search", args.query]
          if (args.root) flags.push(args.root)
          if (args.limit) flags.push("--limit", String(args.limit))
          return run(flags)
        },
      }),
      kern_repo_search: tool({
        description:
          "Ranked free-text symbol search across every repo in the kern multi-repo registry (kern repos add). Returns matches tagged with their repo name, best hits first.",
        args: {
          query: tool.schema.string(),
          limit: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["search", args.query, "--repos"]
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
      kern_code_graph: tool({
        description:
          "Return the call graph neighbourhood of a symbol: its definition, its callers, and what it calls. Use to understand dependencies without reading whole files.",
        args: {
          symbol: tool.schema.string(),
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["graph", args.symbol]
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_inherits: tool({
        description:
          "Return the inheritance edges of a symbol: its supertypes (extends/implements/embeds) and subtypes (what extends/implements/embeds it). Use to see class hierarchies without reading whole files.",
        args: {
          symbol: tool.schema.string(),
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["inherits", args.symbol]
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
          return run(flags)
        },
      }),
      kern_context_budget: tool({
        description:
          "Fit text into a token budget: deduplicate lines, keep the head plus important lines (errors, stack frames), then trim. Use to manage a crowded context window before adding more content.",
        args: {
          text: tool.schema.string(),
          max_tokens: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["budget", args.text]
          if (args.max_tokens) flags.push("--max", String(args.max_tokens))
          return run(flags)
        },
      }),
      kern_walk: tool({
        description:
          "Graph-guided walk: the /walk-graph primitive. Returns an indented parent-child dependency tree of every symbol up to N hops away from a function, across files, with file:line per node. Use instead of grepping or reading whole files to locate code.",
        args: {
          symbol: tool.schema.string(),
          depth: tool.schema.number().optional(),
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["walk", args.symbol]
          if (args.depth) flags.push("--depth", String(args.depth))
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_probe: tool({
        description:
          "Query-driven micro-context router: given a task (bug report, prompt, error text), extract the symbol names it mentions, resolve them against the index, and return a budget-capped bundle of definitions, callers, callees and tests. The graph is the retrieval index, never the payload.",
        args: {
          task: tool.schema.string(),
          root: tool.schema.string().optional(),
          max_tokens: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["probe", args.task]
          if (args.max_tokens) flags.push("--max", String(args.max_tokens))
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_trace: tool({
        description:
          "Runtime-impact overlay: parse a pprof -top dump, a crash stack trace, or a plain list of function names and map the hot symbols onto the call graph — file:line, blast radius, test coverage and risk. Use to see what a hot path touches at runtime.",
        args: {
          trace: tool.schema.string(),
          root: tool.schema.string().optional(),
          limit: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["trace"]
          if (args.limit) flags.push("--limit", String(args.limit))
          if (args.root) flags.push(args.root)
          return withTempFile("trace.input.txt", args.trace, (file) => run([...flags, file]))
        },
      }),
      kern_lock: tool({
        description:
          "Acquire an advisory workspace lock marker on a scope (flock-based) so concurrent agents coordinate before touching shared files. Errors when the scope is already held. Cleared with kern_unlock. Note: this CLI runs in its own process, so the OS releases the flock when the tool call ends; the lock marker persists until kern_unlock and `kern status` reflects reality.",
        args: {
          scope: tool.schema.string(),
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["lock", "--hold", args.scope]
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_unlock: tool({
        description: "Release a workspace lock previously acquired via kern_lock.",
        args: {
          scope: tool.schema.string(),
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["unlock", args.scope]
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_lock_status: tool({
        description:
          "List workspace locks with whether each is held and by which PID. Use to see what other agents are working on.",
        args: {
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["status"]
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_guard_check: tool({
        description:
          "Deterministic architectural guardrails: validate changed files against .kern/boundaries.json rules and return every forbidden dependency crossing (e.g. a frontend importing a backend DB model) with file evidence. Rejects a proposal before it touches the filesystem.",
        args: {
          root: tool.schema.string().optional(),
          file: tool.schema.string().optional(),
          range: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["guard", "check"]
          if (args.root) flags.push(args.root)
          if (args.file) flags.push("--file", args.file)
          if (args.range) flags.push("--range", args.range)
          return runPayload(flags)
        },
      }),
      kern_authorize_context: tool({
        description:
          "Authorized-context primitive (P0.1): compute the exact set of symbols and call edges an agent may legally read for a task, filtered by the agent's identity (firewall context.read permission) and an optional task scope, and return it with an auditable authorization proof (decision, fingerprint, index freshness). Use before retrieval when a task must not leak out-of-scope code.",
        args: {
          agent_id: tool.schema.string(),
          task: tool.schema.string(),
          root: tool.schema.string().optional(),
          symbol_filter: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["authorize-context", "--agent", args.agent_id, "--task", args.task]
          if (args.root) flags.push("--root", args.root)
          if (args.symbol_filter) flags.push("--symbol", args.symbol_filter)
          return runPayload(flags)
        },
      }),
      kern_usage_guide: tool({
        description:
          "Categorized usage guide for every kern MCP tool with performance tiers (fast/moderate/expensive), recommended workflows, and pitfalls. Consult this first when deciding which tool fits a task.",
        args: {},
        async execute() {
          return run(["guide"])
        },
      }),
      kern_memory_add: tool({
        description:
          "Persist a distilled, cross-session lesson for a project (the project 'brain'). Agents record what they learned so future sessions can recall it. Appends to the project memory store (most recent 50 entries kept).",
        args: {
          lesson: tool.schema.string(),
        },
        async execute(args) {
          return run(["remember", args.lesson])
        },
      }),
      kern_memory_list: tool({
        description:
          "List all stored lessons for a project, most recent first with timestamps.",
        args: {},
        async execute(args) {
          return run(["memory"])
        },
      }),
      kern_memory_recall: tool({
        description:
          "Recall the up-to-k most relevant past lessons for a prompt by keyword overlap. Returns only lessons whose tokens match; deterministic and local.",
        args: {
          prompt: tool.schema.string(),
          root: tool.schema.string().optional(),
          k: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["recall", args.prompt]
          if (args.k) flags.push("--limit", String(args.k))
          if (args.root) flags.push(args.root)
          return run(flags)
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
      kern_security: tool({
        description:
          "Local security scan of a project's source files: hardcoded secrets, dynamic SQL, shell command injection, weak crypto, insecure randomness and unsafe deserialization. Deterministic and line-scoped. Use before reviewing code or shipping changes.",
        args: {
          root: tool.schema.string().optional(),
          severity: tool.schema.string().optional(),
          max: tool.schema.number().optional(),
          format: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["sec"]
          if (args.root) flags.push(args.root)
          if (args.severity) flags.push("--severity", args.severity)
          if (args.max) flags.push("--max", String(args.max))
          if (args.format === "json") flags.push("--json")
          return runPayload(flags)
        },
      }),
      kern_safe_delete: tool({
        description:
          "Check whether a symbol can be safely deleted: reports in-project callers (production vs test-only), whether it is exported or an entry point, and a conservative SAFE/NOT SAFE verdict. Use before removing dead code.",
        args: {
          symbol: tool.schema.string(),
          root: tool.schema.string().optional(),
          format: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["delete", args.symbol]
          if (args.root) flags.push(args.root)
          if (args.format === "json") flags.push("--json")
          return runPayload(flags)
        },
      }),
      kern_rename: tool({
        description:
          "Structural symbol rename on the AST index: previews every definition/reference for a Go package-level symbol (types, funcs, vars, consts) with file:line:col edits, then applies them when apply=true. Edits come from a real go/ast parse, so strings, comments, struct-field names, composite-literal keys, import aliases and the package clause are never touched; cross-package references (pkg.Symbol) are handled for exported symbols. apply=true commits transactionally with backups under .kern/rename-backup/ and rollback on failure. Method and non-Go symbols are refused. Preview first, review the edits, then re-run with apply=true.",
        args: {
          symbol: tool.schema.string(),
          new_name: tool.schema.string(),
          root: tool.schema.string().optional(),
          apply: tool.schema.boolean().optional(),
          format: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["rename", args.symbol, args.new_name]
          if (args.root) flags.push(args.root)
          if (args.apply) flags.push("--apply")
          if (args.format === "json") flags.push("--json")
          return run(flags)
        },
      }),
      kern_exec: tool({
        description:
          "Run code in an isolated local runtime and return ONLY stdout (Think in Code). Language is selected by lang= or a shebang line; runtimes resolve from PATH (python3, node, go, bash, perl, ...). Runs in a fresh temp dir with a hard timeout (default 15s via the CLI; timeout is in MILLISECONDS) and a stdout byte cap (default 16KiB); HOME/XDG point into the sandbox and secrets are stripped. When unprivileged user namespaces are available the script also runs in a private network namespace (network egress blocked); otherwise it degrades to env isolation. stderr is only surfaced on failure. Use to compute exact answers (math, data munging, JSON transforms) without polluting context.",
        args: {
          code: tool.schema.string(),
          lang: tool.schema.string().optional(),
          timeout: tool.schema.number().optional(),
          max: tool.schema.number().optional(),
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
      kern_doc_search: tool({
        description:
          "Local search over a project's documents (markdown, text, rst, adoc). Chunks docs locally with deterministic n-gram hashing and returns only the most relevant fragments. Use instead of pasting whole documents into context.",        args: {
          query: tool.schema.string(),
          root: tool.schema.string().optional(),
          k: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["docs", args.query]
          if (args.k) flags.push("--limit", String(args.k))
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_doc_index: tool({
        description:
          "Pre-index a project's documents for kern_doc_search. Run once after documents change; searches auto-index on first use. Pass semantic=true to also embed chunks with a local Ollama embedding model (KERN_EMBED_MODEL, default nomic-embed-text); queries then fuse a real-meaning dense signal with the deterministic n-gram vectors and BM25.",
        args: {
          root: tool.schema.string().optional(),
          semantic: tool.schema.boolean().optional(),
        },
        async execute(args) {
          const flags: string[] = ["docs", "index"]
          if (args.root) flags.push(args.root)
          if (args.semantic) flags.push("--semantic")
          return run(flags)
        },
      }),
      kern_doc_fetch: tool({
        description:
          "Fetch a public documentation page and merge it into the project's local doc index so kern_doc_search can find it. This is the ONLY network call in kern and is invoked explicitly by the user; everything else stays local. The page is HTML-stripped, capped, stored under the cache and indexed as fetch/<name>.md (re-fetching a name replaces it). Pass semantic=true to also attach dense embeddings via the local Ollama model.",
        args: {
          url: tool.schema.string(),
          root: tool.schema.string().optional(),
          name: tool.schema.string().optional(),
          semantic: tool.schema.boolean().optional(),
        },
        async execute(args) {
          const flags: string[] = ["docs", "fetch", args.url]
          if (args.name) flags.push(args.name)
          if (args.root) flags.push(args.root)
          if (args.semantic) flags.push("--semantic")
          return run(flags)
        },
      }),
      kern_commitmsg: tool({
        description:
          "Generate a deterministic conventional-commit message (type, scope, subject, per-file body) from the git diff — rule-based, no LLM, no network; the same diff always yields the same message. Use when a commit needs a starting message the human can tweak.",
        args: {
          root: tool.schema.string().optional(),
          staged: tool.schema.boolean().optional(),
          range: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["commitmsg"]
          if (args.staged) flags.push("--staged")
          if (args.range) flags.push("--range", args.range)
          return run(flags)
        },
      }),
      kern_precache: tool({
        description:
          "Speculative pre-caching: scan the project once and fill the code-summary and document-vector caches so later kern calls are instant. Run periodically or after bulk edits.",
        args: {
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["precache", "--once"]
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_swap: tool({
        description:
          "Budget swapping: in a context document, replace fenced code blocks tagged `lang:path` with per-file symbolic signatures to fit a token budget, or expand `lang:path:summary` blocks back to full file contents. Returns the budget-fitted document.",
        args: {
          text: tool.schema.string(),
          root: tool.schema.string().optional(),
          mode: tool.schema.string().optional(),
          max_tokens: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["swap"]
          if (args.mode) flags.push("--mode", args.mode)
          if (args.max_tokens) flags.push("--max", String(args.max_tokens))
          if (args.root) flags.push(args.root)
          return withTempFile("swap-input.txt", args.text, (file) => run([...flags, file]))
        },
      }),
      kern_diff_files: tool({
        description:
          "Delta streaming: compute a unified line diff between two files (or two versions of the same file) using pure Go. Returns the full patch, or a note when files are identical.",
        args: {
          a: tool.schema.string(),
          b: tool.schema.string(),
        },
        async execute(args) {
          return run(["udiff", args.a, args.b])
        },
      }),
      kern_heal: tool({
        description:
          "Self-correction loop: run validation; on failure ask a local Ollama model to rewrite the failing files, apply the fix inside a throwaway snapshot, re-validate, and report a diff to review. Never edits the user's working tree. timeout is in MILLISECONDS.",
        args: {
          root: tool.schema.string().optional(),
          task: tool.schema.string().optional(),
          model: tool.schema.string().optional(),
          max_rounds: tool.schema.number().optional(),
          timeout: tool.schema.number().optional(),
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
      kern_validate: tool({
        description:
          "Auto-validation: detect the project's language-appropriate build/test/syntax command and run it. Returns exit status, truncated output and duration. Use after editing code to gate correctness before final answers.",
        args: {
          root: tool.schema.string().optional(),
          command: tool.schema.string().optional(),
          timeout: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["validate"]
          if (args.command) flags.push("--cmd", args.command)
          if (args.timeout) flags.push("--timeout", String(Math.max(1, Math.ceil(args.timeout / 1000))))
          if (args.root) flags.push(args.root)
          // validate exits 1 on build failure with the errors on stdout —
          // use runPayload so the output is surfaced, not dropped.
          return runPayload(flags, args.timeout)
        },
      }),
      kern_analyze: tool({
        description:
          "HIGH-LEVEL (ADR-0006): analyze a proposed change against the whole system — relevant code, architecture, dependencies, historical memory, blast radius, risks, evidence, and required validation. This is the Kern 2.0 killer workflow 'Analyze this proposed change'.",
        args: {
          root: tool.schema.string().optional(),
          change: tool.schema.string(),
        },
        async execute(args) {
          const flags: string[] = ["analyze"]
          if (args.root) flags.push("--root", args.root)
          return run([...flags, args.change])
        },
      }),
      kern_plan: tool({
        description:
          "HIGH-LEVEL (ADR-0006): produce an implementation plan for a proposed change — affected files, dependencies, risks and required validation. Deterministic plan over the analysis; no LLM required.",
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
      kern_execute: tool({
        description:
          "HIGH-LEVEL (ADR-0006): execute a change inside an isolated sandbox worktree (autonomy L2). Applies the given unified diff, verifies it builds, and returns the resulting diff. Never mutates the live repository.",
        args: {
          root: tool.schema.string().optional(),
          patch: tool.schema.string(),
        },
        async execute(args) {
          const flags: string[] = ["execute"]
          if (args.root) flags.push("--root", args.root)
          return withTempFile("execute.patch", args.patch, (file) => run([...flags, file]))
        },
      }),
      kern_verify: tool({
        description:
          "HIGH-LEVEL (ADR-0006): verify a change with the unified verification engine — build, unit tests, security, architecture, dependency. Returns the typed verdict (PASS/FAIL/WARN) and per-check summary.",
        args: {
          root: tool.schema.string().optional(),
          types: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["verify"]
          if (args.root) flags.push("--root", args.root)
          if (args.types) flags.push(args.types)
          return run(flags)
        },
      }),
      kern_incident: tool({
        description:
          "HIGH-LEVEL (ADR-0006): investigate a production incident end-to-end — correlate an alert to the affected service and evidence, derive the root cause and hypotheses, and summarize. Provide the alert as JSON; optionally a runtime snapshot (events/deployments/commits) as JSON.",
        args: {
          root: tool.schema.string().optional(),
          alert: tool.schema.string(),
          snapshot: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["incident"]
          if (args.root) flags.push("--root", args.root)
          flags.push(args.alert)
          if (args.snapshot) flags.push(args.snapshot)
          return run(flags)
        },
      }),
      kern_what_if: tool({
        description:
          "HIGH-LEVEL (Workflow C / ADR-0012): simulate the impact of a hypothetical change on the knowledge graph — transitively affected symbols, files, services, tests, a deterministic risk level, and a typed RECOMMENDATION claim. Read-only; never mutates the graph or index.",
        args: {
          root: tool.schema.string().optional(),
          change: tool.schema.string(),
          kind: tool.schema.string().optional(),
          new_target: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["what-if"]
          if (args.root) flags.push("--root", args.root)
          flags.push(args.change)
          if (args.kind) flags.push(args.kind)
          if (args.new_target) flags.push(args.new_target)
          return run(flags)
        },
      }),
      kern_impact: tool({
        description:
          "HIGH-LEVEL: estimate the impact/blast-radius of a change to a symbol — transitively affected symbols/files/services/tests, deterministic risk, and typed claims. Read-only.",
        args: {
          root: tool.schema.string().optional(),
          change: tool.schema.string(),
          kind: tool.schema.string().optional(),
          new_target: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["impact"]
          if (args.root) flags.push("--root", args.root)
          flags.push(args.change)
          if (args.kind) flags.push(args.kind)
          if (args.new_target) flags.push(args.new_target)
          return run(flags)
        },
      }),
      kern_memory: tool({
        description:
          "HIGH-LEVEL (Workflow E): manage engineering memory — add a lesson, list stored lessons, or recall the most relevant lessons for a prompt.",
        args: {
          action: tool.schema.string(),
          lesson: tool.schema.string().optional(),
          prompt: tool.schema.string().optional(),
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["memory", args.action]
          if (args.lesson) flags.push(args.lesson)
          if (args.prompt) flags.push(args.prompt)
          if (args.root) flags.push("--root", args.root)
          return run(flags)
        },
      }),
      kern_agents: tool({
        description:
          "HIGH-LEVEL (Workflow E): build the standard specialist team and list its roster — name, role, capabilities — plus the current task states from the agent registry. Read-only and deterministic.",
        args: {
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["team"]
          if (args.root) flags.push("--root", args.root)
          return run(flags)
        },
      }),
      kern_loop: tool({
        description:
          "HIGH-LEVEL (Workflow E): run the closed autonomy loop against an intent string and return the stage timeline plus the deployed / observed-healthy / learned outcome. The autonomy level (L0-L5, default L0 read-only) gates which stages run.",
        args: {
          root: tool.schema.string().optional(),
          intent: tool.schema.string(),
          level: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["loop", args.intent]
          if (args.level) flags.push("--level", args.level)
          if (args.root) flags.push("--root", args.root)
          return run(flags)
        },
      }),
      kern_meta: tool({
        description:
          "Single entry point: describe what you need in natural language and kern classifies the request and runs the right tool(s) internally. Examples: 'how does dispatch work?' → kern_explore, 'what breaks if I change dispatch?' → kern_impact, 'compress this log: ...' → kern_optimize_log, 'mask secrets in: ...' → kern_mask_pii, 'find the dispatch function' → kern_search, 'show me the architecture' → kern_arch. Prefer this over calling individual kern_* tools — it picks the right one for you.",
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
      kern_workflow: tool({
        description:
          "HIGH-LEVEL: select and coordinate the agent team without the external caller manually sequencing it. Classifies the intent, registers the kind-specific workflow, wires the standard team, and drives the steps to the human approval gate. The run parks at the gate with an approval ID; resolve it via kern_approve then call kern_workflow again with the same task_id to resume.",
        args: {
          root: tool.schema.string().optional(),
          intent: tool.schema.string(),
          task_id: tool.schema.string().optional(),
        },
        async execute(args) {
          if (args.task_id) {
            const flags: string[] = ["workflow", "--task", args.task_id]
            if (args.root) flags.push("--root", args.root)
            return run(flags)
          }
          const flags: string[] = ["workflow", args.intent]
          if (args.root) flags.push("--root", args.root)
          return run(flags)
        },
      }),
      kern_onboard: tool({
        description:
          "Session onboarding: ensure the working directory is indexed and registered for kern (build/refresh the index, register the repo, write AGENTS.md if missing) and report status. Call at session start in a new repo instead of re-selecting files manually.",
        args: {
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["onboard"]
          if (args.root) flags.push("--root", args.root)
          return run(flags)
        },
      }),
      kern_audit: tool({
        description:
          "HIGH-LEVEL: return the tamper-evident governance audit log for the project (every firewall decision/approval). CLI-equivalent: kern audit. Backs the AUDIT intent workflow.",
        args: {
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["audit"]
          if (args.root) flags.push("--root", args.root)
          return run(flags)
        },
      }),
      kern_approve: tool({
        description:
          "HIGH-LEVEL: resolve a governance approval gate. With no id, lists pending approvals. With an id, approves it; set reject=true to reject instead. CLI-equivalent: kern approve. Agents hit this when kern_run/kern_workflow parks at the human approval gate.",
        args: {
          root: tool.schema.string().optional(),
          id: tool.schema.string().optional(),
          reject: tool.schema.string().optional(),
          reason: tool.schema.string().optional(),
          approver: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["approve"]
          if (args.id) flags.push(args.id)
          if (args.reject === "true") flags.push("--reject")
          if (args.reason) flags.push("--reason", args.reason)
          if (args.approver) flags.push("--approver", args.approver)
          if (args.root) flags.push("--root", args.root)
          return run(flags)
        },
      }),
      kern_correlate: tool({
        description:
          "HIGH-LEVEL: correlate a production alert against the runtime to produce a deep evidence chain (alert→service→deployment→commit→symbol→task/pr/agent). Deterministic — derived from runtime source and git history, not LLM.",
        args: {
          root: tool.schema.string().optional(),
          alert: tool.schema.string(),
          // snapshot is intentionally absent: the kern CLI correlate command
          // takes only <alert-json> and --root, so the arg would be dropped.
        },
        async execute(args) {
          const flags: string[] = ["correlate", args.alert]
          if (args.root) flags.push("--root", args.root)
          return run(flags)
        },
      }),
      kern_learn: tool({
        description:
          "HIGH-LEVEL: extract recurring patterns from engineering memory and surface those above a threshold. Patterns are promoted to memory (evidence-based). Deterministic — the LLM may explain but does not create patterns.",
        args: {
          root: tool.schema.string().optional(),
          threshold: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["learn"]
          if (args.threshold) flags.push(args.threshold)
          if (args.root) flags.push("--root", args.root)
          return run(flags)
        },
      }),
      kern_modernize: tool({
        description:
          "HIGH-LEVEL: analyze the monolith and produce a phased modernization plan (communities→bridges→churn→candidate boundaries→impact→risk→migration plan). Each extraction phase becomes an auditable Task.",
        args: {
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["modernize"]
          if (args.root) flags.push("--root", args.root)
          return run(flags)
        },
      }),
      kern_schema_validate: tool({
        description:
          "Deterministically validate JSON output against a JSON schema (subset: object/array/primitives, required, enum, min/max/length, pattern, additionalProperties). Returns either a conform message or one line per violation.",
        args: {
          data: tool.schema.string(),
          schema: tool.schema.string(),
        },
        async execute(args) {
          return withTempFile("schema-data.json", args.data, (dataFile) =>
            withTempFile("schema-def.json", args.schema, (schemaFile) =>
              runPayload(["schema", dataFile, "--schema", schemaFile])
            )
          )
        },
      }),
      kern_verify_output: tool({
        description:
          "Hallucination check: extract file:line, symbol-name and route references from an agent's output text and confirm each against the real source tree and index. Returns ok/MISS verdicts for every reference.",
        args: {
          text: tool.schema.string(),
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["verify"]
          if (args.root) flags.push(args.root)
          return withTempFile("verify-input.txt", args.text, (file) => run([...flags, file]))
        },
      }),
      kern_entry_points: tool({
        description:
          "List framework entry points found in the index: handlers, controllers and route targets. Use to know what endpoints a codebase exposes.",
        args: {
          root: tool.schema.string().optional(),
          limit: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["entries"]
          if (args.limit) flags.push("--limit", String(args.limit))
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_frameworks: tool({
        description:
          "Detect the frameworks and libraries a project uses (Spring, Rails, Express, gin, etc.) by scanning manifests and source markers. Use to know what stack the codebase is on.",
        args: {
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["fw"]
          if (args.root) flags.push(args.root)
          return run(flags)
        },
      }),
      kern_sandbox: tool({
        description:
          "Run a risky command inside the project with a filesystem snapshot: if the command fails, the tree is restored to the pre-run snapshot; if it passes, changes are kept. Use for destructive or uncertain operations.",
        args: {
          command: tool.schema.string(),
          root: tool.schema.string().optional(),
          timeout: tool.schema.number().optional(),
        },
        async execute(args) {
          const parts = args.command.trim().split(/\s+/).filter(Boolean)
          if (parts.length === 0) return "error: empty command"
          const flags: string[] = ["sandbox"]
          if (args.timeout) flags.push("--timeout", String(Math.max(1, Math.ceil(args.timeout / 1000))))
          if (args.root) flags.push(args.root)
          flags.push("--", ...parts)
          return runPayload(flags, args.timeout)
        },
      }),
      // --- Shadow built-ins: route read/grep/glob/bash to kern transparently ---
      // A plugin tool with the same name as a built-in takes precedence, so the
      // agent's "read the file" call hits kern_compact_file under the hood. If
      // kern is unavailable (missing binary, no index), fall back to the raw
      // built-in behavior (node:fs / the `$` shell — the opencode ToolContext
      // has no context.tool to re-invoke the replaced tool) so the agent is
      // never blocked.
      read: tool({
        description:
          "Read a file. Routes to kern_compact_file (symbolic summary) by default for large codebases; set full=true for verbatim content. Falls back to raw read if kern is unavailable.",
        args: {
          filePath: tool.schema.string(),
          full: tool.schema.boolean().optional(),
        },
        async execute(args) {
          if (args.full) {
            // Explicit verbatim request — read directly, no kern.
            return readFallback(args.filePath)
          }
          try {
            return await run(["compact", args.filePath])
          } catch {
            // kern unavailable — fall back to a raw read so the agent is never blocked.
            return readFallback(args.filePath)
          }
        },
      }),
      glob: tool({
        description:
          "Find files by glob pattern. Routes to kern_project_map (symbol map) for repo exploration; set raw=true for plain file listing. Falls back to raw glob if kern is unavailable.",
        args: {
          pattern: tool.schema.string(),
          path: tool.schema.string().optional(),
          raw: tool.schema.boolean().optional(),
        },
        async execute(args) {
          if (args.raw) {
            return globFallback(args.pattern, args.path ?? ".")
          }
          // A glob pattern with metacharacters can't be expressed by the kern
          // project symbol map — route it to the raw glob so the pattern is
          // honored instead of silently dropped.
          if (/[*?[\]{}]/.test(args.pattern)) {
            return globFallback(args.pattern, args.path ?? ".")
          }
          try {
            return await run(["project", args.path ?? "."])
          } catch {
            return globFallback(args.pattern, args.path ?? ".")
          }
        },
      }),
      grep: tool({
        description:
          "Search file contents by regex. Routes to kern_ast_search (code symbols) by default; set docs=true for kern_doc_search, or raw=true for plain grep. Falls back to raw grep if kern is unavailable.",
        args: {
          pattern: tool.schema.string(),
          path: tool.schema.string().optional(),
          include: tool.schema.string().optional(),
          docs: tool.schema.boolean().optional(),
          raw: tool.schema.boolean().optional(),
        },
        async execute(args) {
          if (args.raw) {
            return grepFallback(args.pattern, args.path, args.include)
          }
          if (args.include) {
            // kern ast/docs searches have no include filter — route to the raw
            // grep so the filter is honored instead of silently dropped.
            return grepFallback(args.pattern, args.path, args.include)
          }
          try {
            if (args.docs) {
              const flags: string[] = ["docs", args.pattern]
              if (args.path) flags.push("--root", args.path)
              return await run(flags)
            }
            // Code search via AST
            const flags: string[] = ["ast", args.pattern]
            if (args.path) flags.push("--root", args.path)
            return await run(flags)
          } catch {
            return grepFallback(args.pattern, args.path, args.include)
          }
        },
      }),
      bash: tool({
        description:
          "Run a shell command. Routes to kern build (governed, compact output) when kern is available; set raw=true to bypass kern. Falls back to raw bash (host `$` shell) if kern is unavailable, so the agent is never blocked. timeout is in MILLISECONDS (default 120000, max 1800000).",
        args: {
          command: tool.schema.string(),
          workdir: tool.schema.string().optional(),
          timeout: tool.schema.number().optional(),
          raw: tool.schema.boolean().optional(),
        },
        async execute(args) {
          const cmd = args.command.trim()
          if (cmd === "") return "error: empty command"
          if (args.raw) {
            return runRaw(args.command, args.workdir, args.timeout)
          }
          // kern build runs any command (sh -c) in the project dir, gated by
          // the governance firewall, and returns compact output. kern exec is
          // NOT used for this: it executes code in an isolated temp sandbox
          // (wrong cwd, no network, stripped env), which would break ordinary
          // shell usage — so every command goes through the same governed path.
          try {
            const flags: string[] = ["build", cmd]
            if (args.workdir) flags.push("--dir", args.workdir)
            if (args.timeout) flags.push("--timeout", String(Math.max(1, Math.ceil(args.timeout / 1000))))
            // Pass the agent's budget (ms) through so the governed path honors
            // it instead of the 2-minute default ceiling.
            return await run(flags, args.timeout)
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
            if (/blocked|denied|allowlist|approval|firewall|governance|not permitted|refused|timed out|timeout/i.test(text)) throw err
            if (!existsSync(bin)) return runRaw(args.command, args.workdir, args.timeout)
            throw err
          }
        },
      }),
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
        }
      } catch {
        /* swallow */
      }

      // Compression: only bash/read/grep outputs above the threshold.
      if (input.tool !== "bash" && input.tool !== "read" && input.tool !== "grep") {
        return
      }
      if (text.length < DEFAULT_COMPACT_THRESHOLD) return
      try {
        const compressed = await withTempFile("tool-output.txt", text, (file) => run(["log", file]))
        if (!compressed || compressed.trim() === "") return
        output.output = `[kern] compressed ${text.length} -> ${compressed.length} chars\n${compressed}`
      } catch {
        // Never break tool execution on optimizer failure.
      }
    },

    // P0-2: capture user decisions at low volume so the session's direction
    // survives compaction. Only brief, substantive prompts are recorded;
    // questions, tiny messages, repeated prompts and rapid-fire chatter are
    // skipped so project memory isn't flooded (that would evict real lessons).
    "chat.message": async (_input, output) => {
      try {
        const parts: any[] = (output.parts as any[]) ?? []
        let text = ""
        for (const p of parts) {
          if (typeof p.text === "string") text += p.text
          else if (typeof p.content === "string") text += p.content
        }
        text = text.trim()
        if (!text || text.length < 16 || text.length > 600) return
        if (text.endsWith("?")) return
        const lower = text.toLowerCase()
        if (/\b(remember|forget)\b|^\/(remember|forget)\b/.test(lower)) return
        const now = Date.now()
        if (text === lastChatText || now - lastChatAt < 60_000) return
        lastChatAt = now
        lastChatText = text
        await remember(`User: ${text}`, 200)
      } catch {
        /* swallow */
      }
    },
  }
}) satisfies Plugin
