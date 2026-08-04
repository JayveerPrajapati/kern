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
