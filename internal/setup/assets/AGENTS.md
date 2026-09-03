# kern usage rules for agents — READ FIRST

`kern` is this workspace's local context engine. These are the kern usage rules for agents. It is ALREADY INSTALLED and
WIRED — the `kern_*` MCP tools below are available to you right now. They run
locally (nothing leaves your machine) and are FASTER and CHEAPER than the
built-in read/grep/glob for any non-trivial codebase because they query a
prebuilt symbol index instead of re-reading files.

## The kern_meta tool (preferred entry point)

Instead of choosing among 86 individual `kern_*` tools, call the single
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

Prefer `kern_meta` as your default. By default only a minimal 11-tool surface
is advertised (the high-level task-oriented entry points plus `kern_meta`);
set `KERN_MCP_FULL=1` to expose the full catalog to agents,
`KERN_MCP_PHASE=explore|plan|edit|verify` to filter the advertised list to a
phase's shortlist (plus the always-on meta/cross tools), and
`KERN_MCP_SINGLE_TOOL=1` to expose ONLY `kern_meta` (useful when an agent is
overwhelmed by the catalog). Note `kern_meta`'s NL router still reaches
every sub-tool handler internally regardless of what is advertised, so no
capability is lost — only the advertised surface shrinks. The classifier is
deterministic keyword matching — no LLM, no
network. When you know the exact tool you need, calling it directly is fine
and slightly faster.

## The kern_authorize_context tool

`kern_authorize_context` computes the context an agent may legally read for a
task: the exact set of symbols and call edges permitted by the agent's
identity and task scope, plus an auditable authorization proof (decision,
fingerprint, index freshness). Call it before retrieval when a task must not
leak out-of-scope code. It is part of the default tool surface (no
`KERN_MCP_FULL` needed).

- CLI: `kern authorize-context -agent <id> -task <desc> [-root .] [-symbol <filter>] [-deny-path <path>] [-json]` — exit 0 = allowed, 2 = denied (proof printed), 1 = error
- MCP: `kern_authorize_context(agent_id, task, [root], [symbol_filter], [scope])` → `{scope, proof}` JSON
- `kern_meta` NL routing: "authorize", "authorized", "allowed to see", "permitted", "what can i" — e.g. "what can I touch in this repo for the refactor task"
- Full reference: `docs/authorized-context.md`

## Kern-first policy (ENFORCED)

**You MUST call a `kern_*` tool before any built-in `read`, `grep`, `glob`,
or `bash` for the tasks below.** On hosts that support pre-tool hooks
(Claude Code, Cursor, Gemini, Copilot, Qwen, Qoder), calling a built-in
instead of the kern equivalent will be BLOCKED with a redirect. On opencode,
the built-in is transparently routed to kern. So the path of least resistance
is to use kern directly.

### Decision procedure (follow this BEFORE reaching for a built-in)

1. **Do I need to read a file?** → call `kern_compact_file` (symbolic summary)
   first. Only fall back to `read` if you need the verbatim full text (rare).
2. **Do I need to list/explore a repo?** → call `kern_project_map`, NOT `glob`.
3. **Do I need to grep for a pattern?** → call `kern_ast_search` (code) or
   `kern_doc_search` (docs), NOT `grep`.
4. **Do I need to understand a symbol (callers/callees)?** → call
   `kern_code_graph` or `kern_explore`, NOT `read` + `grep`.
5. **Do I need to build/test/lint?** → call `kern_run_build`, NOT `bash`.
6. **Do I need to run a command?** → call `kern_exec`, NOT `bash`.
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
| Read a file                  | `kern_compact_file`                   | `read`        |
| List / explore a repo        | `kern_project_map`                    | `glob`/`ls`   |
| Grep for a pattern (code)    | `kern_ast_search` / `kern_search`     | `grep`        |
| Grep for a pattern (docs)    | `kern_doc_search`                     | `grep`        |
| Read a source slice          | `kern_context`                        | `read`        |
| Understand a symbol          | `kern_code_graph` / `kern_explore`    | `read`        |
| Search the web / docs        | `kern_doc_fetch` → `kern_doc_search`  | `webfetch`    |
| Build / test / lint          | `kern_run_build`                      | `bash`        |
| Run a command / script       | `kern_exec`                           | `bash`        |
| Commit                       | `kern_commitmsg`                      | git CLI       |
| Search (task/bug/error)      | `kern_probe`                          | grep + read   |
| Orchestrate a whole task     | `kern_run`                            | manual chain  |
| Onboard / wire a new repo    | `kern_onboard`                        | manual setup  |

## Session start: onboard before exploring (MANDATORY)

When you begin work in a repo you have not worked in before — or any time you
suspect a repo may not be indexed or registered — call `kern_onboard` (or
`kern_buddy`) FIRST, before reading/grepping/globbing the tree. `kern_onboard`:

- registers the working directory in kern's repo registry (if not already),
- builds/refreshes kern's index of the repo (if stale or missing),
- writes the `AGENTS.md` kern-first rules (if missing),
- returns a status report (registered · indexed · symbols/edges/files · wired).

Do this automatically on session start so the repo is indexed before you search
it. If `kern_onboard` reports the repo is not registered or not indexed, fix
that (register + index) before doing manual discovery.

**Prefer the index over re-exploring.** Once a repo is indexed, fetch details
from the index instead of re-searching the filesystem with read/grep/glob/git:
- `kern_project_map` / `kern_buddy` — repo layout + conventions (not raw `ls`)
- `kern_search` / `kern_ast_search` / `kern_fts_search` / `kern_repo_search` — find symbols (not raw `grep`/`rg`)
- `kern_code_graph` / `kern_graph` / `kern_explore` / `kern_context` — understand a symbol and its callers/callees (not read-every-file)
- `kern_probe` — answer "what does this touch / what breaks if I change X" from the index
- `kern_diff_files` / `kern_commitmsg` — git-adjacent work against the index/diff (not raw `git diff`/`git log` alone)
- `kern_why` / `kern_inherits` / `kern_path` — graph answers for symbols

If kern already has the index, fetch the answer from it directly — do not
re-explore or re-parse files that the index already covers.

## Full capability catalog

`kern` ships 86 `kern_*` MCP tools across these domains. If you are unsure
which tool fits, call `kern_usage_guide` (categorized guide with performance
tiers) or `kern_agents` (specialist roster) first to enumerate options. Reach
into these groups for the "full capabilities" — do not limit yourself to the
context-optimization tools above.

**Onboard / single entry points:**
- `kern_onboard` — session-start wiring: ensure the working repo is registered,
  indexed, and has AGENTS.md rules; returns status. Call first in any repo you
  have not indexed yet.
- `kern_run` — run an intent through the full task pipeline (compile → select
  workflow/capabilities/agents → create Task → policy precheck → result with
  risk/approval/next-action). Use this instead of hand-chaining a dozen tools.
  `kern_loop` (autonomy L0-L5) is the closed feedback loop over the same
  pipeline.
- `kern_workflow` — select and coordinate the agent team for an intent,
  driving steps to the human approval gate (resume via `kern_approve`)

**Context optimization (the local focus):**
- `kern_optimize_prompt` / `kern_optimize_log` / `kern_optimize_output` — compress prompts/logs/replies
- `kern_compact_file` / `kern_project_map` / `kern_pack` / `kern_swap` — compact source / map repo / pack project / budget-swap
- `kern_context` / `kern_context_budget` — minimal source slices sized to a token budget
- `kern_semcache` / `kern_stats` — inspect the semantic cache / report token savings
- `kern_schema_validate` — validate JSON against a schema deterministically

**Code intelligence & graph (understand the codebase):**
- `kern_code_graph` / `kern_graph` / `kern_explore` / `kern_near` / `kern_walk` — call graphs, adjacency, blast radius, dependency trees
- `kern_path` / `kern_why` / `kern_inherits` — shortest call path / why a symbol exists / class hierarchy
- `kern_ast_search` / `kern_search` / `kern_fts_search` / `kern_repo_search` — symbol search (AST / ranked / FTS / multi-repo)
- `kern_entry_points` / `kern_frameworks` / `kern_arch` / `kern_communities` — entry points, frameworks, architecture, subsystem clusters
- `kern_hubs` / `kern_bridges` / `kern_dead` / `kern_larges` — hotspots, cross-package coupling, dead code, god functions
- `kern_churn` / `kern_cochange` / `kern_changes` / `kern_review` — change-frequency risk, co-change coupling, diff impact, review context
- `kern_test_gaps` / `kern_trace` / `kern_precache` — test coverage, runtime impact overlay, warm caches

**Plan / analyze / change safely:**
- `kern_analyze` / `kern_plan` / `kern_what_if` / `kern_impact` — ADR-0006 workflows: analyze a change, plan implementation, simulate impact, blast radius
- `kern_verify` / `kern_validate` / `kern_execute` / `kern_heal` — verify a change / auto-validate / execute in sandbox / self-correct
- `kern_verify_output` — hallucination check: confirm file:line/symbol/route references in agent output against the real source tree

**Security / safety / governance:**
- `kern_security` — scan for secrets, SQL injection, weak crypto, unsafe deserialization
- `kern_mask_pii` — mask PII before sending anything remote
- `kern_guard_check` / `kern_sandbox` / `kern_exec` / `kern_lock` / `kern_unlock` / `kern_lock_status` — architectural guardrails, sandboxed exec, workspace locks
- `kern_safe_delete` / `kern_rename` — safe symbol deletion / structural rename
- `kern_approve` — resolve a governance approval gate (approve/reject pending approvals)
- `kern_audit` — return the tamper-evident governance audit log for the project

**Engineering memory & agents:**
- `kern_memory` / `kern_memory_add` / `kern_memory_list` / `kern_memory_recall` / `kern_learn` — project brain: store / list / recall lessons, extract patterns
- `kern_agents` — specialist team roster
- `kern_incident` / `kern_correlate` / `kern_modernize` — incident investigation / alert-to-evidence chain / monolith modernization

- Before pasting logs into context, use `kern_optimize_log`.
- For library/framework docs, `kern_doc_fetch` pulls one public page into the
  project's local doc index, then `kern_doc_search` queries it; pass
  `semantic=true` to also attach local Ollama embeddings to the page.
- `kern_buddy` gives a session onboarding digest (conventions, layout, gotchas)
  for an unfamiliar repo — call it once at session start on a new codebase.

## When a build, test, or long-running command is needed

Prefer `kern_run_build` over running the command directly and pasting full
output into context.

## Git workflows

- Before committing, use `kern_commitmsg` to get a deterministic conventional
  commit message (type/scope/subject + per-file body) from the diff; it is
  rule-based and offline, so edit the result freely. `kern commit` can stage
  and commit in one step (CLI only — a machine committing on its own is too
  destructive to expose as a tool).
- `kern pack` and `kern project map` honor a root `.gitignore` and `.kernignore`
  (`.kernignore` wins), and packed bundles carry a `SECURITY` section of
  secrets/injection findings for files being sent to an agent.

## Prompt hygiene

If a user prompt or attached data is large or noisy, optimize it first with
`kern_optimize_prompt` before processing. Optimization results are cached, so
a repeated or reworded query (semantic cache) returns instantly with a
`served from ... cache` marker. Use `kern_semcache` to inspect or clear the
cache, or to preview whether two inputs are similar enough to hit.

## Savings tracking

Report token savings when asked: use `kern_stats`. This shows before/after
tokens and estimated cost saved.
