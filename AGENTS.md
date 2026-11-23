# kern usage rules for agents — READ FIRST

`kern` is a local context engine and **every agent uses it by default** — in
every project, on every harness. Call its `kern_*` tools FIRST for any file,
code, log, build, or docs task: it runs locally (nothing leaves your machine)
and is FASTER and CHEAPER than built-in read/grep/glob on any non-trivial
codebase because it queries a prebuilt symbol index instead of re-reading
files. This is not conditional on the harness: Claude Code, Codex, Cursor,
Gemini, Copilot, Qwen, Qoder, Kiro, Continue, Windsurf, Zed and opencode all
follow the same kern-first default; hook hosts and the opencode plugin add
enforcement on top, they never change the default.
If no `kern_*` tools are present, run `kern setup` (then restart the agent)
before falling back to built-ins. Only an explicit operator opt-out
(`KERN_ENFORCE=0` / `KERN_BYPASS=1`, audited) or a platform kern cannot run on
exempts a session; when one kern call errors, fall back for that call and keep
using kern for the rest.

## Direct tools (call these first)

Call the obvious direct tool first:
- `kern_buddy` — onboard to an unfamiliar repo.
- `kern_search` — find a symbol by name.
- `kern_explore` — understand a symbol, file, or flow.
- `kern_context` — get the minimal source slice.
- `kern_probe` — build a task-level context bundle.
- `kern_research` — read-only multi-symbol research with impact and affected files.
- `kern_impact` — assess what breaks.
- `kern_graph` — inspect one-hop callers/callees.
- `kern_review` — build review context.
- `kern_verify` — run build/test/security verification.
- `kern_meta` — optional router when the right direct tool is unclear.

Set `KERN_MCP_FULL=1` for all 34 individual `kern_*` tools;
`KERN_MCP_PHASE=explore|plan|edit|verify` selects phase subsets, and
`KERN_MCP_SINGLE_TOOL=1` collapses to `kern_meta`.

Default surface: 11 tools (`kern_buddy`, `kern_search`, `kern_explore`,
`kern_context`, `kern_probe`, `kern_research`, `kern_impact`, `kern_graph`, `kern_review`,
`kern_verify`, `kern_meta`). No security-gated tool is advertised by default. `kern_meta`
still routes to every sub-tool behind the existing governance gates. In full mode,
`kern_exec` is governance-gated with default `KERN_EXEC_POSTURE=ask` (LOW/MEDIUM
audited, HIGH/CRITICAL approval); `KERN_EXEC_POSTURE=closed` restores strict
fail-closed. Hosts that
prefix the MCP server name list each tool twice — expected dual registration,
not a second toolset.

<!-- kern:global-omit:begin -->
Think in phases: explore (read/discover), plan (analyze/simulate), edit
(mutate/execute), verify (check/validate) — and select direct tools by intent. More examples:
- `kern_meta(request="show me the architecture")` → runs `kern_arch`
- `kern_meta(request="compress this log: ...")` → runs `kern_optimize` (action=log)
- `kern_meta(request="mask secrets in: ...")` → runs `kern_mask_pii`
<!-- kern:global-omit:end -->

## Kern-first route

- Find a symbol → `kern_search`
- Understand a symbol/file → `kern_explore`
- What breaks if changed → `kern_impact`
- Onboard/repo layout → `kern_buddy`
- Build/test/lint → `kern_verify`
- Unsure which tool → `kern_meta`

If one kern call fails, fall back for that call only and keep using kern for the rest.



<!-- kern:global-omit:begin -->

## Kern-first policy (the universal default; enforced on hook hosts)

**Call a `kern_*` tool before any built-in `read`, `grep`, `glob`, or `bash`
for the tasks below.** On hosts with pre-tool hooks (Claude Code, Cursor,
Gemini, Copilot, Qwen, Qoder, Codex) the built-in call is BLOCKED with a
redirect — opencode too: its plugin shadows serve content-intent commands
(reads are answered from the kern index or the readonly sandbox, never
denied; only GATE commands block). Operator break-glass:
HOST env KERN_ENFORCE=0/KERN_BYPASS=1, audited (~/.kern/audit/bypass.jsonl).

1. Read a file → `kern_explore` with the file path as symbol (symbolic
   summary; tier=full for the verbatim file, start_line/end_line for a line
   window); `read` serves only non-code files (markdown/config/data).
   `kern_compact_file` and `kern_context` (minimal symbol slice) do the
   same.
2. List/explore a repo → `kern_buddy` (default surface) or
   `kern_project_map`, NOT `glob`.
3. Grep → `kern_search` (symbol query, not regex); true regex needs
   bash `kern ast <pattern>` (CLI; wildcards, kind prefixes) — plain bash
   `grep -rn` is denied by the same guard (operator break-glass only:
   host env KERN_BYPASS=1 with raw=true). Docs: `kern_doc` (action=search). AST
   pattern search → `kern_ast_search` first (indexed, every indexed
   language, Go extraction richest); external ast-grep tools only as
   fallback or for AST rewriting.
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
   through; content-intent bash (cat/head/grep) is SERVED (kern index
   Tier 1/2 or the readonly sandbox Tier 3; GATE commands write/network/
   installers are blocked) — use the kern tool for the intent; break-glass:
   HOST env KERN_ENFORCE=0/ KERN_BYPASS=1. `kern_exec` (isolated runner,
   default surface) is governance-gated: the default `ask` posture runs
   LOW/MEDIUM audited and mints an approval for HIGH/CRITICAL;
   `KERN_EXEC_POSTURE=closed` restores the strict empty-allowlist denial
   (`KERN_ALLOW_EXEC=1` / `KERN_TOOLS` are the opt-ins).
7. Search web/docs → `kern_doc` (action=fetch) then `kern_doc`
   (action=search), NOT `webfetch`/`websearch`. `kern_doc` fetch is the
   sanctioned named-URL network path (no approval); `kern_exec` network
   commands are NOT — they mint an approval under the ask posture.
8. Unsure which tool → `kern_meta` (NL router) or `kern_buddy`.

<!-- kern:global-omit:end -->

<!-- kern:global-omit:begin -->
9. Past sessions learned about this project → `kern_buddy` (default; digest
   includes project memory) or `kern_memory` (action=recall).

### Decision procedure (follow this BEFORE reaching for a built-in)

1. **Do I need to read a file?** → call `kern_explore` with the file path as
   symbol (symbolic summary) first. `read` serves only non-code files
   (markdown/config/data); code files always route to the summary — for
   verbatim code add tier=full, or start_line/end_line for a line window.
   `kern_compact_file` and `kern_context` (minimal symbol slice) do the
   same.
2. **Do I need to list/explore a repo?** → call `kern_buddy` (default
   surface) or `kern_project_map`, NOT `glob`.
3. **Do I need to find a symbol / grep for a pattern?** → call `kern_search`
   (symbol query, NOT regex), NOT `grep`. Note: on hosts that route grep
   transparently (opencode), the built-in `grep` IS kern's symbol search —
   patterns are symbol queries, NOT regex. For regex use bash
   `kern ast <pattern>` (CLI; wildcards, kind prefixes) — plain bash
   `grep -rn` is denied by the same guard (operator break-glass only:
   KERN_BYPASS=1 with raw=true).
4. **Do I need to understand a symbol (callers/callees/blast radius)?** → call
   `kern_explore`, NOT `read` + `grep`.
5. **Do I need to build/test/lint?** → call `kern_verify` (build + tests +
   security in one verdict), NOT `bash`. To run your own build/test/lint
   command, pass it: `kern_verify command="go test ./..." output=summary`
   (output: summary|failures|tail:N|lines:A-B|full); the full run is kept under
   an anchor, so ask for more with `kern_verify anchor=<id> output=lines:A-B`.
6. **Do I need to run a command?** → `bash` simple commands
   (pwd/ls/which/echo/git status…) pass through; content-intent bash
   (cat/head/grep emitting file content) is SERVED — symbol reads answer
   from the kern index, everything else pure-read runs sandboxed via
   kern exec --readonly, and only GATE commands (writes/network/installers)
   are blocked — use the kern tool for the intent; break-glass is host env
   `KERN_ENFORCE=0` / `KERN_BYPASS=1`. `kern_exec` (isolated runner: macOS
   Seatbelt with network egress blocked, Linux unshare, Windows fail-closed
   with the override) is on the default surface and governance-gated: the
   default `ask` posture runs LOW/MEDIUM audited and mints an approval for
   HIGH/CRITICAL; `KERN_EXEC_POSTURE=closed` restores strict fail-closed.
7. **Do I need to search the web/docs?** → call `kern_doc` (action=fetch) then
   `kern_doc` (action=search), NOT `webfetch`/`websearch`. Pre-index docs with
   `kern_doc` (action=index) (optional, for semantic search via local Ollama).
8. **None of the above / unsure which kern tool?** → call `kern_meta` (the NL
   router) or `kern_buddy` to enumerate options, BEFORE falling back.
9. **Do I need what past sessions learned about this project?** → call
   `kern_buddy` (default surface; digest includes project memory) or
   `kern_memory` (action=recall), NOT plain exploration —
   lessons persist across sessions.

### Quick reference table

| When you need to…            | CALL THIS FIRST                       | Replaces      |
|------------------------------|---------------------------------------|---------------|
| Grep for a pattern (code)    | `kern_search`                         | `grep`        |
| Search by AST pattern        | `kern_ast_search`                     | ast-grep      |
| Read a file or line range    | `kern_explore` (path as symbol)       | `read`        |
| Understand a symbol          | `kern_explore`                        | `read`        |
| Orchestrate a whole task     | `kern_run`                             | manual chain  |

### Understanding code

Every tool not on the default surface is `KERN_MCP_FULL=1` only — ask
`kern_meta` to route, or use the CLI.

- File structure (functions/types/lines) → `kern_compact_file`
- File verbatim → `kern_compact_file` tier=full
- One symbol's minimal source slice → `kern_context`
- Symbol + callers/callees + blast radius → `kern_explore`
- What breaks if I change X → `kern_impact`
- Find a symbol by name/fragment → `kern_search`
- Context bundle from a task/bug/prompt → `kern_probe`
- Multi-symbol research with impact → `kern_research`
- Summary → neighborhood → source, on demand → `kern_probe`
- Squeeze files/symbols into a token budget → `kern_fit_context`
- Repo layout / onboarding digest → `kern_buddy` (default) /
  `kern_project_map`

### Etags (conditional fetch)

Read tools (`kern_explore`, `kern_context`, `kern_compact_file`,
`kern_probe`, `kern_research`, `kern_search`, `kern_why`, `kern_graph`)
mint an etag per response; re-calling with the last-seen etag returns
`unchanged (etag …)` instead of the payload. Pass etags back when
re-requesting the same symbol/context (CLI: `kern explore --etag <E>`).

### Plan / execute / verify

`kern_verify`, `kern_plan`, `kern_run`, `kern_heal`, and `kern_review`
(plus `kern_meta` routing) are default-surface; the rest are
`KERN_MCP_FULL=1`.

- Implementation plan for a change → `kern_plan`
- Token-optimised review context → `kern_review`
- Build + tests + security in one verdict → `kern_verify`
- Orchestrate a whole task end-to-end → `kern_run`
- Compiler errors → deterministic AST fix → `kern_heal` (snapshot loop)
- Self-correct failing files (snapshot loop) → `kern_heal`

### Context hygiene / memory / routing

- Compress a prompt / log / LLM output → `kern_optimize`
- Recall or store cross-session lessons → `kern_memory` (`kern_buddy`'s
  digest includes project memory)
- Unsure which kern tool fits → `kern_meta` (NL router) or `kern_buddy`

### Built-in shadow mappings (what read/grep/glob/bash become under Contract Y)

- `read` code file → compact summary + pointer line; verbatim escape:
  `kern_explore` tier=full (default) / `kern_compact_file` tier=full /
  `kern_context <symbol>` (both default-surface)
- `read` non-code (markdown/config/data) → raw passthrough
- `grep` → `kern_search` (symbol query, NOT regex); regex needs bash
  `kern ast <pattern>` (plain bash grep is denied by the same guard;
  operator break-glass: KERN_BYPASS=1 with raw=true)
- `glob` → `kern_project_map` (`kern_buddy` on the
  default surface)
- `bash` content-intent (cat/head/grep emitting file content) →
  SERVED, never denied: symbol-shaped reads are answered from the kern index
  (Tier 1), read-head + simple tail (grep X | wc -l / head / tail / grep -v)
  is computed in-process (Tier 2), and everything else pure-read runs
  sandboxed via kern exec --readonly (Tier 3; raw with a one-line note when
  kern is unavailable). GATE commands (rm/mv/cp/dd/curl|sh/installers/
  git push --force/write redirects) are blocked (exit 3). Everything else is
  governed (kern build). Break-glass is host env KERN_ENFORCE=0 / KERN_BYPASS=1.
  **One-line contract: reads → kern, writes → approval, exec → sandbox.**
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
`kern_explore` (default surface), `kern_probe` etc.
instead of raw grep/read.
Then recall what past sessions learned: call `kern_buddy` (default surface;
its digest includes the "Project memory (from past sessions)" section) or
`kern_memory` (action=recall) before starting substantive
work. Project memory persists per project
and is written by the loop's learn stage, the plugin's session capture, and
`kern_memory` (action=add) — read it by default so prior lessons inform the work.

## Anti-stall session knobs (opt-in/opt-out)

Server-side knobs; default ON unless noted. Unset = default behavior.
- `KERN_SERVED_LEDGER=0` — disable session already-served dedup (repeat read-tool calls return a one-line pointer; re-send with the etag, vary the view, or `no_cache=1` to re-emit)
- `KERN_LOOP_DETECT=0` — disable advisory `[kern loop-suspect]` markers on near-duplicate reads
- `KERN_EXPLORE_BUDGET=N` — opt-in per-session explore-phase call cap; beyond N, explore tools deny with `explore budget exceeded — emit a plan (kern_plan)`
- `KERN_HINTS=0` — disable in-band `next: …` hints and usage-gap nudges

`kern_plan` output now ends with two sections:
- `## Context sufficiency` — `ready-to-execute: true|false` vs the affected files this session was already served (`served: X/Y affected files`)
- `## First slice` — deterministic smallest-first edit (fewest deps → fewest symbols → smallest LOC) plus its `go test ./…` verify command

When you need to… (the underused tools, ranked from real usage stats — call them before re-deriving):
- Session start → `kern_buddy` (onboarding digest incl. project memory)
- About to edit → `kern_impact` (what breaks; blast radius first)
- Who depends on this → `kern_why`
- Stuck or repeating reads → `kern_memory` (action=recall — cross-session lessons may already hold the answer)
- Near-duplicate asks → `kern_semcache` (similar queries may already be cached)
- Before implementing → `kern_plan` (read its `## First slice`)

## Full capability catalog

`kern` ships 34 `kern_*` MCP tools across many domains. Eleven are advertised
by default (toolpolicy.go defaultTools — the same list as in the direct-tool
section above). Set `KERN_MCP_FULL=1` to advertise all 34 directly. `kern_meta`
still reaches them internally through the existing governance gates.

## Additional capabilities

- **Multi-agent squad**: kern embeds 7 specialist roles (Planner, Architect,
  Coder, Reviewer, Security, Tester, SRE). See the `kern-team-orchestration` skill.
- **Prompt hygiene**: Use `kern_optimize` (action=prompt)
  to strip noise before processing.
- **Token savings**: Use `kern_stats` to report before/after token savings.
<!-- kern:global-omit:end -->
