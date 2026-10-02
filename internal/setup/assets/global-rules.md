<!-- kern:global-rules begin (managed by `kern setup --global-rules`; keep personal prefs outside the markers) -->
# kern usage rules for agents — READ FIRST

Applies when kern is available in this workspace: `kern_*` MCP tools are
wired, or the repo's AGENTS.md carries a kern-rules section. If no kern
tools are present, consider running `kern setup` (then restart the agent);
otherwise ignore this file.

kern is a local context engine. It runs locally (nothing leaves your
machine) and is FASTER and CHEAPER than the built-in read/grep/glob for any
non-trivial codebase because it queries a prebuilt symbol index instead of
re-reading files.

## The kern_meta tool (preferred entry point)

Instead of choosing among 117 individual `kern_*` tools, call **`kern_meta`**
with a natural-language request; it classifies the request (deterministic
keyword matching — no LLM, no network) and runs the right tool internally.
Examples:

- `kern_meta(request="how does dispatch work?")` → runs `kern_explore`
- `kern_meta(request="what breaks if I change dispatch?")` → runs `kern_impact`
- `kern_meta(request="find the NewServer function")` → runs `kern_search`

Set `KERN_MCP_FULL=1` for all 117 tools, `KERN_MCP_PHASE=explore|plan|edit|verify`
for phase subsets, or `KERN_MCP_SINGLE_TOOL=1` for only `kern_meta`. Only 22
tools are advertised by default (toolpolicy.go defaultTools): `kern_meta`,
`kern_explore`, `kern_impact`, `kern_review`, `kern_search`, `kern_context`,
`kern_optimize`, `kern_plan`, `kern_verify`, `kern_run`,
`kern_authorize_context`, `kern_compact_file`, `kern_project_map`,
`kern_probe`, `kern_retrieve`, `kern_memory`, `kern_buddy`,
`kern_fit_context`, `kern_repair`, `kern_heal`, `kern_commitmsg`,
`kern_synthesize_test`. Tools outside that surface (`kern_doc`, `kern_exec`,
`kern_mask_pii`, `kern_stats`) are `KERN_MCP_FULL=1` only.

## Kern-first policy (ENFORCED & AUTONOMOUS)

**Call a `kern_*` tool before any built-in `read`, `grep`, `glob`, or `bash`
for the tasks below.** On hosts with pre-tool hooks (Claude Code, Cursor,
Gemini, Copilot, Qwen, Qoder, Codex) the built-in call is BLOCKED with a
redirect; on opencode the built-in is transparently routed to kern.

1. Read a file → `kern_compact_file` (symbolic summary); `read` serves only
   non-code files (markdown/config/data) — verbatim code needs
   `kern_compact_file` tier=full, or `kern_context`/`kern_explore` for a
   symbol slice.
2. List/explore a repo → `kern_project_map`, NOT `glob`.
3. Grep → `kern_search` (symbol query, not regex); regex needs bash
   `grep -rn` (or raw=true). Docs: `kern_doc` (action=search) —
   `KERN_MCP_FULL=1` only.
4. Understand a symbol → `kern_explore`, NOT `read` + `grep`.
5. Build/test/lint → `kern_verify`, NOT `bash`.
6. Run a command → simple bash (pwd/ls/which/echo/git status…) passes
   through; content-intent bash (cat/sed/head/grep) is governed/blocked —
   use the kern tool for the intent; break-glass is host env
   `KERN_ENFORCE=0` / `KERN_BYPASS=1`. `kern_exec` (isolated runner) is
   `KERN_MCP_FULL=1` only.
7. Search web/docs → `kern_doc` (action=fetch) then `kern_doc`
   (action=search), NOT `webfetch`/`websearch` — `KERN_MCP_FULL=1` only.
8. Unsure which tool → `kern_meta` (NL router) or `kern_buddy`.

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

## Session start: onboard before exploring (MANDATORY)

New or unindexed repo? Call `kern_buddy` FIRST — it returns the onboarding
digest: conventions, layout, entry points, gotchas (and refreshes the index).
(`kern onboard` is the CLI equivalent.) **Prefer the index over re-exploring.**

Extras: `kern_commitmsg` (commit messages),
`kern_optimize` (action=prompt — strip noise), `kern_stats` (token-savings
report; `KERN_MCP_FULL=1` only); 7-role specialist squad via the
`kern-team-orchestration` skill.
<!-- kern:global-rules end -->
