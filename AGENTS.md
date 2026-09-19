# kern usage rules for agents — READ FIRST

`kern` is this workspace's local context engine. It is ALREADY INSTALLED and
WIRED — the `kern_*` MCP tools below are available to you right now. They run
locally (nothing leaves your machine) and are FASTER and CHEAPER than the
built-in read/grep/glob for any non-trivial codebase because they query a
prebuilt symbol index instead of re-reading files.

## The kern_meta tool (preferred entry point)

Instead of choosing among 146 individual `kern_*` tools, call the single
**`kern_meta`** tool with a natural-language request. Kern classifies the
request and runs the right tool(s) internally — you get the same result
without having to know which tool fits. Think in phases: explore (read/discover),
plan (analyze/simulate), edit (mutate/execute), verify (check/validate) — and
let `kern_meta` route within the active phase.

Examples:
- `kern_meta(request="how does dispatch work?")` → runs `kern_explore`
- `kern_meta(request="what breaks if I change dispatch?")` → runs `kern_impact`
- `kern_meta(request="show me the architecture")` → runs `kern_arch`
- `kern_meta(request="compress this log: ...")` → runs `kern_optimize_log`
- `kern_meta(request="mask secrets in: ...")` → runs `kern_mask_pii`
- `kern_meta(request="find the NewServer function")` → runs `kern_search`

Prefer `kern_meta` as your default. Set `KERN_MCP_FULL=1` for all 146 tools,
`KERN_MCP_PHASE=explore|plan|edit|verify` for phase subsets, or
`KERN_MCP_SINGLE_TOOL=1` for only `kern_meta`. The classifier is deterministic
keyword matching — no LLM, no network.

## Kern-first policy (ENFORCED & AUTONOMOUS)

**You MUST call a `kern_*` tool before any built-in `read`, `grep`, `glob`,
or `bash` for the tasks below. Do NOT wait for the user to tell you to use kern.**
As an autonomous AI agent in this repository, you are expected to proactively
invoke `kern_meta` or the respective `kern_*` tools for any codebase exploration,
search, architectural analysis, test, or verification.

On hosts that support pre-tool hooks (Claude Code, Cursor, Gemini, Copilot, Qwen,
Qoder, Codex), calling a built-in instead of the kern equivalent will be BLOCKED
with a redirect. On opencode, the built-in is transparently routed to kern. So the
path of least resistance is to use kern directly.

### Decision procedure (follow this BEFORE reaching for a built-in)

1. **Do I need to read a file?** → call `kern_compact_file` (symbolic summary)
   first. Only fall back to `read` if you need the verbatim full text (rare).
2. **Do I need to list/explore a repo?** → call `kern_project_map`, NOT `glob`.
3. **Do I need to grep for a pattern?** → call `kern_ast_search` (code) or
   `kern_doc_search` (docs), NOT `grep`. Note: on hosts that route grep
   transparently (opencode), the built-in `grep` IS kern's AST symbol search —
   patterns are symbol queries, NOT regex. For regex grep use bash
   `grep -rn <pattern>` (or raw=true) or the kern CLI `kern ast <pattern>`.
4. **Do I need to understand a symbol (callers/callees)?** → call
   `kern_code_graph` or `kern_explore`, NOT `read` + `grep`.
5. **Do I need to build/test/lint?** → call `kern_run_build`, NOT `bash`.
6. **Do I need to run a command?** → call `kern_exec`, NOT `bash`. Note:
   on macOS (no unprivileged-userns network isolation) `kern_exec` FAILS
   CLOSED by design; the refusal names the override —
   `KERN_ALLOW_UNISOLATED=1` (or `KERN_ALLOW_NET=1`). Expect it, don't
   retry blindly.
7. **Do I need to search the web/docs?** → call `kern_doc_fetch` then
   `kern_doc_search`, NOT `webfetch`/`websearch`. Pre-index docs with
   `kern_doc_index` (optional, for semantic search via local Ollama).
8. **None of the above / unsure which kern tool?** → call `kern_usage_guide`
   or `kern_buddy` to enumerate options, BEFORE falling back.

**Heuristic: if the task involves files, code, logs, builds, or web content,
start with kern.** If the specific kern tool is unavailable or errors, fall
back to the built-in — but never skip kern when it's available.

### Quick reference table

| When you need to…            | CALL THIS FIRST                       | Replaces      |
|------------------------------|---------------------------------------|---------------|
| Grep for a pattern (code)    | `kern_search`                         | `grep`        |
| Read a source slice          | `kern_context`                        | `read`        |
| Understand a symbol          | `kern_explore`                        | `read`        |
| Orchestrate a whole task     | `kern_run`                            | manual chain  |

## Session start: onboard before exploring (MANDATORY)

When you begin work in a repo you have not worked in before — or any time you
suspect a repo may not be indexed or registered — call `kern_onboard` (or
`kern_buddy`) FIRST, before reading/grepping/globbing the tree. `kern_onboard`
registers the repo, builds/refreshes the index, and returns a status report.

Do this automatically on session start so the repo is indexed before you search
it. **Prefer the index over re-exploring.** Once indexed, use `kern_search`,
`kern_explore`, `kern_code_graph`, `kern_probe` etc. instead of raw grep/read.

## Full capability catalog

`kern` ships 146 `kern_*` MCP tools across many domains. If you are unsure
which tool fits, call `kern_usage_guide` or `kern_buddy` to enumerate options.

## Additional capabilities

- **Multi-agent squad**: kern embeds 7 specialist roles (Planner, Architect,
  Coder, Reviewer, Security, Tester, SRE). See the `kern-team-orchestration` skill.
- **Git workflows**: Use `kern_commitmsg` for deterministic commit messages.
- **Prompt hygiene**: Use `kern_optimize_prompt` to strip noise before processing.
- **Token savings**: Use `kern_stats` to report before/after token savings.
