import { type Plugin, tool } from "@opencode-ai/plugin"
import { resolve } from "node:path"
import { existsSync } from "node:fs"
import { writeFile, rm } from "node:fs/promises"
import { tmpdir } from "node:os"

// Resolve the kern binary. Prefer an explicit env var, then the project's
// bin/ directory, then PATH.
function kernBin(directory: string): string {
  if (process.env.KERN_BIN) return process.env.KERN_BIN
  const local = resolve(directory, "bin/kern")
  if (existsSync(local)) return local
  return "kern"
}

// Local cache file for text passed through the CLI (avoids arg-length limits).
function cacheFile(name: string): string {
  return resolve(tmpdir(), `kern-${name}`)
}

const DEFAULT_COMPACT_THRESHOLD = 4000

export default (async ({ directory, $ }) => {
  const bin = kernBin(directory)

  const run = async (args: string[]): Promise<string> => {
    // Bun's shell escapes each interpolated array element as one argument.
    const out = await $`${bin} ${args}`.quiet()
    return out.stdout.toString()
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
            const file = cacheFile("prompt.attached.log")
            await writeFile(file, args.attached_log)
            flags.push("--attach", file)
          }
          if (args.session) flags.push("--session", args.session)
          if (args.model) flags.push("--model", args.model)
          const out = await run([...flags, args.prompt])
          if (args.attached_log) await rm(cacheFile("prompt.attached.log"), { force: true })
          return out
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
          const root = args.root ?? "."
          return run(["project", root])
        },
      }),
      kern_run_build: tool({
        description:
          "Run a build/test command locally and return only the compact result (exit status + errors), not full output. Use for builds, tests, linting to save context.",
        args: {
          command: tool.schema.string(),
          dir: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["build"]
          if (args.dir) flags.push("--dir", args.dir)
          return run([...flags, args.command])
        },
      }),
      kern_optimize_log: tool({
        description:
          "Strip noise from log output: keeps errors, warnings, stack traces and build failures, removes timestamps and chatter. Use before pasting logs into context.",
        args: { log: tool.schema.string() },
        async execute(args) {
          const file = cacheFile("log.input.log")
          await writeFile(file, args.log)
          const out = await run(["log", file])
          await rm(file, { force: true })
          return out
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
          max_tokens: tool.schema.number().optional(),
        },
        async execute(args) {
          const flags: string[] = ["review"]
          if (args.root) flags.push(args.root)
          if (args.range) flags.push("--range", args.range)
          if (args.file) flags.push("--file", args.file)
          if (args.max_tokens) flags.push("--max", String(args.max_tokens))
          return run(flags)
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
          "Dead-code detection: symbols nothing in the project calls. Private names are dead for certain; public names may be external API. Sorted by size so the biggest cleanup wins show first.",
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
          const file = cacheFile("trace.input.txt")
          await writeFile(file, args.trace)
          const flags: string[] = ["trace", file]
          if (args.limit) flags.push("--limit", String(args.limit))
          if (args.root) flags.push(args.root)
          const out = await run(flags)
          await rm(file, { force: true })
          return out
        },
      }),
      kern_lock: tool({
        description:
          "Acquire an advisory workspace lock on a scope (flock-based) so concurrent agents coordinate before touching shared files. Held until kern_unlock. Errors when the scope is already held.",
        args: {
          scope: tool.schema.string(),
          root: tool.schema.string().optional(),
        },
        async execute(args) {
          const flags: string[] = ["lock", args.scope]
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
          return run(flags)
        },
      }),
    },

    // Auto-compress large tool outputs before they enter context.
    "tool.execute.after": async (input, output) => {
      if (input.tool !== "bash" && input.tool !== "read" && input.tool !== "grep") {
        return
      }
      const text = typeof output?.output === "string" ? output.output : ""
      if (text.length < DEFAULT_COMPACT_THRESHOLD) return
      try {
        const file = cacheFile("tool-output.txt")
        await writeFile(file, text)
        const compressed = await run(["log", file])
        await rm(file, { force: true })
        if (!compressed || compressed.trim() === "") return
        output.output = `[kern] compressed ${text.length} -> ${compressed.length} chars\n${compressed}`
      } catch {
        // Never break tool execution on optimizer failure.
      }
    },
  }
}) satisfies Plugin
