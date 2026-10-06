# kern usage rules for agents — READ FIRST

`kern` is a local context engine. When kern is available — `kern_*` MCP tools
wired or a kern-rules section in this file (or the repo's AGENTS.md) — use it
FIRST: it runs locally (nothing leaves your machine) and is FASTER and CHEAPER
than built-in read/grep/glob for any non-trivial codebase because it queries a
prebuilt symbol index instead of re-reading files. If no kern tools are
present, consider running `kern setup` (then restart the agent); otherwise
ignore this file.

## The kern_meta tool (preferred entry point)

Instead of choosing among 117 individual `kern_*` tools, call **`kern_meta`**
with a natural-language request; it classifies the request (deterministic
keyword matching — no LLM, no network) and runs the right tool internally.
Examples:
- `kern_meta(request="how does dispatch work?")` → runs `kern_explore`
- `kern_meta(request="what breaks if I change dispatch?")` → runs `kern_impact`
- `kern_meta(request="find the NewServer function")` → runs `kern_search`

Set `KERN_MCP_FULL=1` for all 117 tools, `KERN_MCP_PHASE=explore|plan|edit|verify`
for phase subsets, or `KERN_MCP_SINGLE_TOOL=1` for only `kern_meta`. Only 6
tools are advertised by default (toolpolicy.go defaultTools): `kern_meta`,
`kern_explore`, `kern_impact`, `kern_search`, `kern_verify`, `kern_buddy`.
Every other tool is `KERN_MCP_FULL=1` only (`kern_doc`, `kern_exec`,
`kern_mask_pii`, `kern_stats`, `kern_context`, `kern_compact_file`, …).
That is by design, not a gap: `kern_meta` still routes to every sub-tool
internally. Hosts that prefix the MCP server name list each tool twice —
expected dual registration, not a second toolset.

<!-- kern:global-omit:begin -->
Think in phases: explore (read/discover), plan (analyze/simulate), edit
(mutate/execute), verify (check/validate) — and let `kern_meta` route within
the active phase. More examples:
- `kern_meta(request="show me the architecture")` → runs `kern_arch`
- `kern_meta(request="compress this log: ...")` → runs `kern_optimize` (action=log)
- `kern_meta(request="mask secrets in: ...")` → runs `kern_mask_pii`
<!-- kern:global-omit:end -->

## Kern-first policy (enforced on hook hosts; routed elsewhere)

**Call a `kern_*` tool before any built-in `read`, `grep`, `glob`, or `bash`
for the tasks below.** On hosts with pre-tool hooks (Claude Code, Cursor,
Gemini, Copilot, Qwen, Qoder, Codex) the built-in call is BLOCKED with a
redirect; on opencode it routes to kern (advisory). Operator break-glass:
HOST env KERN_ENFORCE=0/KERN_BYPASS=1, audited (~/.kern/audit/bypass.jsonl).

1. Read a file → `kern_explore` with the file path as symbol (symbolic
   summary; tier=full for the verbatim file, start_line/end_line for a line
   window); `read` serves only non-code files (markdown/config/data).
   `kern_compact_file` and `kern_context` (minimal symbol slice) do the
   same with `KERN_MCP_FULL=1`.
2. List/explore a repo → `kern_buddy` (default surface) or
   `kern_project_map` (`KERN_MCP_FULL=1`), NOT `glob`.
3. Grep → `kern_search` (symbol query, not regex); regex needs bash
   `grep -rn` (or raw=true). Docs: `kern_doc` (action=search) —
   `KERN_MCP_FULL=1` only.
4. Understand a symbol → `kern_explore`, NOT `read` + `grep`.
5. Build/test/lint → `kern_verify`, NOT `bash`. Bare `kern_verify` (MCP
   tool) = the build+test+security verdict; bare CLI `kern verify` prints
   usage — run `kern verify build,test`. `kern_verify command="go test ./..."
   output=summary` runs YOUR go/mvn/gradle/npm/cargo/pytest/make command and
   returns only the slice you ask for: `summary` (default: counts + failing
   tests), `failures`, `tail:N`, `lines:A-B` or `full`. The full run is kept —
   re-slice it with `kern_verify anchor=<id> output=lines:A-B`, no rerun.
   CLI: `kern verify --command "go test ./..." --output summary`.
6. Run a command → simple bash (pwd/ls/which/echo/git status…) passes
   through; content-intent bash (cat/sed/head/grep) is governed/blocked —
   use the kern tool for the intent; break-glass: HOST env KERN_ENFORCE=0/
   KERN_BYPASS=1. `kern_exec` (isolated runner) is `KERN_MCP_FULL=1` only.
7. Search web/docs → `kern_doc` (action=fetch) then `kern_doc`
   (action=search), NOT `webfetch`/`websearch` — `KERN_MCP_FULL=1` only.
8. Unsure which tool → `kern_meta` (NL router) or `kern_buddy`.

<!-- kern:global-omit:begin -->
9. Past sessions learned about this project → `kern_buddy` (default; digest
   includes project memory) or `kern_memory` (action=recall,
   `KERN_MCP_FULL=1`).

### Decision procedure (follow this BEFORE reaching for a built-in)

1. **Do I need to read a file?** → call `kern_explore` with the file path as
   symbol (symbolic summary) first. `read` serves only non-code files
   (markdown/config/data); code files always route to the summary — for
   verbatim code add tier=full, or start_line/end_line for a line window.
   `kern_compact_file` and `kern_context` (minimal symbol slice) do the
   same with `KERN_MCP_FULL=1`.
2. **Do I need to list/explore a repo?** → call `kern_buddy` (default
   surface) or `kern_project_map` (`KERN_MCP_FULL=1`), NOT `glob`.
3. **Do I need to find a symbol / grep for a pattern?** → call `kern_search`
   (symbol query, NOT regex), NOT `grep`. Note: on hosts that route grep
   transparently (opencode), the built-in `grep` IS kern's symbol search —
   patterns are symbol queries, NOT regex. For regex grep use bash
   `grep -rn <pattern>` (or raw=true) or the kern CLI `kern ast <pattern>`.
4. **Do I need to understand a symbol (callers/callees/blast radius)?** → call
   `kern_explore`, NOT `read` + `grep`.
5. **Do I need to build/test/lint?** → call `kern_verify` (build + tests +
   security in one verdict), NOT `bash`. To run your own build/test/lint
   command, pass it: `kern_verify command="go test ./..." output=summary`
   (output: summary|failures|tail:N|lines:A-B|full); the full run is kept under
   an anchor, so ask for more with `kern_verify anchor=<id> output=lines:A-B`.
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
   `kern_buddy` (default surface; digest includes project memory) or
   `kern_memory` (action=recall, `KERN_MCP_FULL=1`), NOT plain exploration —
   lessons persist across sessions.

### Quick reference table

| When you need to…            | CALL THIS FIRST                       | Replaces      |
|------------------------------|---------------------------------------|---------------|
| Grep for a pattern (code)    | `kern_search`                         | `grep`        |
| Read a file or line range    | `kern_explore` (path as symbol)       | `read`        |
| Understand a symbol          | `kern_explore`                        | `read`        |
| Orchestrate a whole task     | `kern_run` (`KERN_MCP_FULL=1`)       | manual chain  |

### Understanding code

Default surface: only `kern_meta`, `kern_explore`, `kern_impact`,
`kern_search`, `kern_verify`, `kern_buddy` are advertised; every other tool
below is `KERN_MCP_FULL=1` — ask `kern_meta` to route, or use the CLI.

- File structure (functions/types/lines) → `kern_compact_file`
  (`KERN_MCP_FULL=1`)
- File verbatim → `kern_compact_file` tier=full (`KERN_MCP_FULL=1`)
- One symbol's minimal source slice → `kern_context` (`KERN_MCP_FULL=1`)
- Symbol + callers/callees + blast radius → `kern_explore`
- What breaks if I change X → `kern_impact`
- Find a symbol by name/fragment → `kern_search`
- Context bundle from a task/bug/prompt → `kern_probe` (`KERN_MCP_FULL=1`)
- Summary → neighborhood → source, on demand → `kern_retrieve`
  (`KERN_MCP_FULL=1`)
- Squeeze files/symbols into a token budget → `kern_fit_context`
  (`KERN_MCP_FULL=1`)
- Repo layout / onboarding digest → `kern_buddy` (default) /
  `kern_project_map` (`KERN_MCP_FULL=1`)

### Etags (conditional fetch)

Read tools (`kern_explore`, `kern_context`, `kern_compact_file`,
`kern_retrieve`) mint an etag per response; re-calling with the last-seen
etag returns `unchanged (etag …)` instead of the payload. Pass etags back
when re-requesting the same symbol/context (CLI: `kern explore --etag <E>`).

### Plan / execute / verify

Only `kern_verify` (and `kern_meta` routing) is default-surface; the rest
are `KERN_MCP_FULL=1`.

- Implementation plan for a change → `kern_plan` (`KERN_MCP_FULL=1`)
- Token-optimised review context → `kern_review` (`KERN_MCP_FULL=1`)
- Build + tests + security in one verdict → `kern_verify`
- Orchestrate a whole task end-to-end → `kern_run` (`KERN_MCP_FULL=1`)
- Compiler errors → deterministic AST fix → `kern_repair` (`KERN_MCP_FULL=1`)
- Self-correct failing files (snapshot loop) → `kern_heal` (`KERN_MCP_FULL=1`)
- Commit message from the diff → `kern_commitmsg` (`KERN_MCP_FULL=1`)
- Generate table-driven tests → `kern_synthesize_test` (`KERN_MCP_FULL=1`)

### Context hygiene / memory / routing

- Compress a prompt / log / LLM output → `kern_optimize` (`KERN_MCP_FULL=1`)
- Recall or store cross-session lessons → `kern_memory` (`KERN_MCP_FULL=1`;
  `kern_buddy`'s digest includes project memory)
- Governance-scoped authorized reads → `kern_authorize_context`
  (`KERN_MCP_FULL=1`)
- Unsure which kern tool fits → `kern_meta` (NL router) or `kern_buddy`

### Built-in shadow mappings (what read/grep/glob/bash become under Contract Y)

- `read` code file → compact summary + pointer line; verbatim escape:
  `kern_explore` tier=full (default) / `kern_compact_file` tier=full /
  `kern_context <symbol>` (both `KERN_MCP_FULL=1`)
- `read` non-code (markdown/config/data) → raw passthrough
- `grep` → `kern_search` (symbol query, NOT regex); regex needs bash grep with
  raw=true (governed)
- `glob` → `kern_project_map` (`KERN_MCP_FULL=1`; `kern_buddy` on the
  default surface)
- `bash` content-intent (cat/sed/head/grep emitting file content) →
  governed/blocked; use the kern tool for the intent; break-glass is host env
  KERN_ENFORCE=0 / KERN_BYPASS=1
- `bash` simple (pwd/ls/which/echo/git status…) → allowlist passthrough

**Heuristic: if the task involves files, code, logs, builds, or web content,
start with kern.** If the specific kern tool is unavailable or errors, fall
back to the built-in.
<!-- kern:global-omit:end -->

## Session start: onboard before exploring (MANDATORY)

New or unindexed repo? Call `kern_buddy` FIRST — it returns the onboarding
digest: conventions, layout, entry points, gotchas (and refreshes the index).
(`kern onboard` is the CLI equivalent.) **Prefer the index over re-exploring.**

<!-- kern:global-omit:begin -->
When you begin work in a repo you have not worked in before — or any time you
suspect a repo may not be indexed or registered — call `kern_buddy` FIRST,
before reading/grepping/globbing the tree. It returns the onboarding digest:
conventions, layout, entry points and gotchas (and refreshes the index).
(`kern onboard` is the CLI equivalent.)

Do this automatically on session start so the repo is indexed before you search
it. **Prefer the index over re-exploring.** Once indexed, use `kern_search`,
`kern_explore` (default surface), `kern_probe` (`KERN_MCP_FULL=1`) etc.
instead of raw grep/read.
Then recall what past sessions learned: call `kern_buddy` (default surface;
its digest includes the "Project memory (from past sessions)" section) or
`kern_memory` (action=recall, `KERN_MCP_FULL=1`) before starting substantive
work. Project memory persists per project
and is written by the loop's learn stage, the plugin's session capture, and
`kern_memory` (action=add, `KERN_MCP_FULL=1`) — read it by default so prior lessons inform the work.

## Full capability catalog

`kern` ships 117 `kern_*` MCP tools across many domains. Only 6 are advertised
by default (toolpolicy.go defaultTools): `kern_meta`, `kern_explore`,
`kern_impact`, `kern_search`, `kern_verify`, `kern_buddy`. Tools outside that
surface — `kern_doc`, `kern_exec`, `kern_mask_pii`, `kern_stats` and every
other non-listed tool — are `KERN_MCP_FULL=1` only; `kern_meta` reaches them
all internally. If you are unsure which tool fits, call `kern_meta` or
`kern_buddy` to
enumerate options.

## Additional capabilities

- **Multi-agent squad**: kern embeds 7 specialist roles (Planner, Architect,
  Coder, Reviewer, Security, Tester, SRE). See the `kern-team-orchestration` skill.
- **Git workflows**: Use `kern_commitmsg` (`KERN_MCP_FULL=1`) for
  deterministic commit messages.
- **Prompt hygiene**: Use `kern_optimize` (`KERN_MCP_FULL=1`, action=prompt)
  to strip noise before processing.
- **Token savings**: Use `kern_stats` to report before/after token savings
  (`KERN_MCP_FULL=1` only).
<!-- kern:global-omit:end -->
