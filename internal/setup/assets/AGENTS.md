# kern usage rules for agents — READ FIRST

`kern` is this workspace's local context engine. It is ALREADY INSTALLED and
WIRED — the `kern_*` MCP tools below are available to you right now. They run
locally (nothing leaves your machine) and are FASTER and CHEAPER than the
built-in read/grep/glob for any non-trivial codebase because they query a
prebuilt symbol index instead of re-reading files.

## The kern_meta tool (preferred entry point)

Instead of choosing among 117 individual `kern_*` tools, call the single
**`kern_meta`** tool with a natural-language request. Kern classifies the
request and runs the right tool(s) internally — you get the same result
without having to know which tool fits. Think in phases: explore (read/discover),
plan (analyze/simulate), edit (mutate/execute), verify (check/validate) — and
let `kern_meta` route within the active phase.

Examples:
- `kern_meta(request="how does dispatch work?")` → runs `kern_explore`
- `kern_meta(request="what breaks if I change dispatch?")` → runs `kern_impact`
- `kern_meta(request="show me the architecture")` → runs `kern_arch`
- `kern_meta(request="compress this log: ...")` → runs `kern_optimize` (action=log)
- `kern_meta(request="mask secrets in: ...")` → runs `kern_mask_pii`
- `kern_meta(request="find the NewServer function")` → runs `kern_search`

Prefer `kern_meta` as your default. Set `KERN_MCP_FULL=1` for all 117 tools,
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
   first. `read` serves only non-code files (markdown/config/data); code files
   always route to the compact summary — for verbatim code use `kern_compact_file`
   tier=full, or `kern_context`/`kern_explore` for a symbol slice.
2. **Do I need to list/explore a repo?** → call `kern_project_map`, NOT `glob`.
3. **Do I need to find a symbol / grep for a pattern?** → call `kern_search`
   (symbol query, NOT regex), NOT `grep`. Note: on hosts that route grep
   transparently (opencode), the built-in `grep` IS kern's symbol search —
   patterns are symbol queries, NOT regex. For regex grep use bash
   `grep -rn <pattern>` (or raw=true) or the kern CLI `kern ast <pattern>`.
4. **Do I need to understand a symbol (callers/callees/blast radius)?** → call
   `kern_explore`, NOT `read` + `grep`.
5. **Do I need to build/test/lint?** → call `kern_verify` (build + tests +
   security in one verdict), NOT `bash`.
6. **Do I need to run a command?** → `bash` simple commands
   (pwd/ls/which/echo/git status…) pass through; content-intent bash
   (cat/sed/head/grep emitting file content) is governed/blocked — use the
   kern tool for the intent; break-glass is host env `KERN_ENFORCE=0` /
   `KERN_BYPASS=1`. `kern_exec` (isolated runner: macOS Seatbelt with network
   egress blocked, Linux unshare, Windows fail-closed with the override) is
   `KERN_MCP_FULL=1` only.
7. **Do I need to search the web/docs?** → call `kern_doc` (action=fetch) then
   `kern_doc` (action=search), NOT `webfetch`/`websearch`. Pre-index docs with
   `kern_doc` (action=index) (optional, for semantic search via local Ollama).
   `kern_doc` is `KERN_MCP_FULL=1` only.
8. **None of the above / unsure which kern tool?** → call `kern_meta` (the NL
   router) or `kern_buddy` to enumerate options, BEFORE falling back.
9. **Do I need what past sessions learned about this project?** → call
`kern_memory` (action=recall) (or `kern_buddy` for the digest including project
memory), NOT plain exploration — lessons persist across sessions.

### Understanding code

- File structure (functions/types/lines) → `kern_compact_file`
- File verbatim → `kern_compact_file` tier=full
- One symbol's minimal source slice → `kern_context`
- Symbol + callers/callees + blast radius → `kern_explore`
- What breaks if I change X → `kern_impact`
- Find a symbol by name/fragment → `kern_search`
- Context bundle from a task/bug/prompt → `kern_probe`
- Summary → neighborhood → source, on demand → `kern_retrieve`
- Squeeze files/symbols into a token budget → `kern_fit_context`
- Repo layout / onboarding digest → `kern_project_map`, `kern_buddy`

### Plan / execute / verify

- Implementation plan for a change → `kern_plan`
- Token-optimised review context → `kern_review`
- Build + tests + security in one verdict → `kern_verify`
- Orchestrate a whole task end-to-end → `kern_run`
- Compiler errors → deterministic AST fix → `kern_repair`
- Self-correct failing files (snapshot loop) → `kern_heal`
- Commit message from the diff → `kern_commitmsg`
- Generate table-driven tests → `kern_synthesize_test`

### Context hygiene / memory / routing

- Compress a prompt / log / LLM output → `kern_optimize`
- Recall or store cross-session lessons → `kern_memory`
- Governance-scoped authorized reads → `kern_authorize_context`
- Unsure which kern tool fits → `kern_meta` (NL router) or `kern_buddy`

### Built-in shadow mappings (what read/grep/glob/bash become under Contract Y)

- `read` code file → compact summary + pointer line; verbatim escape:
  `kern_compact_file` tier=full / `kern_context <symbol>`
- `read` non-code (markdown/config/data) → raw passthrough
- `grep` → `kern_search` (symbol query, NOT regex); regex needs bash grep with
  raw=true (governed)
- `glob` → `kern_project_map`
- `bash` content-intent (cat/sed/head/grep emitting file content) →
  governed/blocked; use the kern tool for the intent; break-glass is host env
  KERN_ENFORCE=0 / KERN_BYPASS=1
- `bash` simple (pwd/ls/which/echo/git status…) → allowlist passthrough

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
suspect a repo may not be indexed or registered — call `kern_buddy` FIRST,
before reading/grepping/globbing the tree. It returns the onboarding digest:
conventions, layout, entry points and gotchas (and refreshes the index).
(`kern onboard` is the CLI equivalent.)

Do this automatically on session start so the repo is indexed before you search
it. **Prefer the index over re-exploring.** Once indexed, use `kern_search`,
`kern_explore`, `kern_probe` etc. instead of raw grep/read.
Then recall what past sessions learned: call `kern_memory` (action=recall) (or
`kern_buddy`, whose digest includes the "Project memory (from past sessions)"
section) before starting substantive work. Project memory persists per project
and is written by the loop's learn stage, the plugin's session capture, and
`kern_memory` (action=add) — read it by default so prior lessons inform the work.

## Full capability catalog

`kern` ships 117 `kern_*` MCP tools across many domains. Only 22 are advertised
by default (toolpolicy.go defaultTools): `kern_meta`, `kern_explore`,
`kern_impact`, `kern_review`, `kern_search`, `kern_context`, `kern_optimize`,
`kern_plan`, `kern_verify`, `kern_run`, `kern_authorize_context`,
`kern_compact_file`, `kern_project_map`, `kern_probe`, `kern_retrieve`,
`kern_memory`, `kern_buddy`, `kern_fit_context`, `kern_repair`, `kern_heal`,
`kern_commitmsg`, `kern_synthesize_test`. Tools outside that surface
(`kern_doc`, `kern_exec`, `kern_mask_pii`, `kern_stats`) are `KERN_MCP_FULL=1`
only. If you are unsure which tool fits, call `kern_meta` or `kern_buddy` to
enumerate options.

## Additional capabilities

- **Multi-agent squad**: kern embeds 7 specialist roles (Planner, Architect,
  Coder, Reviewer, Security, Tester, SRE). See the `kern-team-orchestration` skill.
- **Git workflows**: Use `kern_commitmsg` for deterministic commit messages.
- **Prompt hygiene**: Use `kern_optimize` (action=prompt) to strip noise before processing.
- **Token savings**: Use `kern_stats` to report before/after token savings
  (`KERN_MCP_FULL=1` only).
