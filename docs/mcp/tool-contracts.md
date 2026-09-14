# Kern MCP Tool Contracts

> **Source of truth:** `internal/mcp/tools.go` — the tool registration table. This document is generated from that table; if the two disagree, the table wins.

This page is the authoritative reference for every tool the kern MCP server exposes. Each contract lists the tool's name, agent phase, risk level, description, input parameters and required parameters.

## Catalog at a glance

| Metric | Value |
|---|---|
| Total tools | 143 |
| Phases | explore (40), plan (16), edit (22), verify (21), meta (1), cross (43) |
| Risk levels | low (86), medium (33), high (19), critical (5) |

## Full catalog (summary)

| Tool | Phase | Risk | Description |
|---|---|---|---|
| `kern_optimize_prompt` | cross | low | Compress and clean a raw prompt before sending it to an LLM |
| `kern_optimize_output` | cross | low | Compress an LLM's response (assistant output) by stripping filler, pleasantries and hedge language while prese |
| `kern_memory_add` | cross | medium | Persist a distilled, cross-session lesson for a project (the project 'brain') |
| `kern_memory_list` | cross | low | List all stored lessons for a project, most recent first with timestamps |
| `kern_memory_recall` | cross | low | Recall the up-to-k most relevant past lessons for a prompt by keyword overlap |
| `kern_mask_pii` | cross | low | Locally scan text for secrets and PII (API keys, passwords, tokens, URLs with credentials, IPs, emails) and re |
| `kern_security` | verify | high | Local security scan of a project's source files: hardcoded secrets, dynamic SQL, shell command injection, weak |
| `kern_safe_delete` | edit | high | Check whether a symbol can be safely deleted: reports in-project callers (production vs test-only), whether it |
| `kern_doc_search` | cross | low | Local vector search over a project's documents (markdown, text, rst, adoc) |
| `kern_doc_index` | cross | medium | Pre-index a project's documents for kern_doc_search |
| `kern_doc_fetch` | cross | medium | Fetch a public documentation page and merge it into the project's local doc index so kern_doc_search can find  |
| `kern_commitmsg` | edit | low | Generate a deterministic conventional-commit message (type, scope, subject, per-file body) from the git diff — |
| `kern_precache` | verify | medium | Speculative pre-caching (#20): scan the project once and fill the code-summary and document-vector caches so l |
| `kern_swap` | plan | low | Budget swapping (#18): in a context document, replace fenced code blocks tagged `lang:path` with per-file symb |
| `kern_sandbox` | edit | critical | Run a risky command inside a snapshot of the project (#15): on non-zero exit the tree is rolled back exactly ( |
| `kern_diff_files` | verify | low | Delta streaming (#13): compute a unified line diff between two files (or two versions of the same file) using  |
| `kern_heal` | edit | high | Self-correction loop (#9): run validation; on failure ask a local Ollama model to rewrite the failing files, a |
| `kern_validate` | verify | high | Auto-validation (#7): detect the project's language-appropriate build/test/syntax command and run it |
| `kern_schema_validate` | verify | low | Deterministically validate JSON output against a JSON schema (subset: object/array/primitives, required, enum, |
| `kern_verify_output` | verify | low | Hallucination check: extract file:line, symbol-name and route references from an agent's output text and confi |
| `kern_check_draft` | verify | low | Validate an agent's draft code against the project index (lighter than LSP, deterministic): Go parse errors, r |
| `kern_taint` | verify | high | Taint-lite analysis: flag security sinks (SQL injection, command injection, unsafe deserialization, Python eva |
| `kern_compact_file` | explore | low | Return a compact symbolic summary of a source file (functions, types, line numbers) instead of reading the who |
| `kern_project_map` | explore | low | Return a compressed map of a whole project: every source file with its symbols and line counts |
| `kern_pack` | plan | low | Pack a whole project into one paste-ready bundle: project instructions, a directory tree with per-file token c |
| `kern_buddy` | explore | low | Session onboarding digest for any agent: the project's conventions, layout, entry points and gotchas distilled |
| `kern_run_build` | edit | critical | Run a build/test command locally and return only the compact result (exit status + errors), not full output |
| `kern_optimize_log` | cross | low | Strip noise from log output: keeps errors, warnings, stack traces and build failures, removes timestamps and c |
| `kern_context_budget` | plan | low | Fit text into a token budget: deduplicate lines, keep the head plus important lines (errors, stack frames), th |
| `kern_stats` | cross | low | Return before/after token savings and cost estimates from kern optimizations, optionally filtered to today or  |
| `kern_semcache` | cross | medium | Inspect and manage the semantic cache that serves similar (not just identical) prior queries instantly |
| `kern_ast_search` | explore | low | AST-level symbol search across a Go project |
| `kern_frameworks` | explore | low | Detect the frameworks and libraries a project uses (Spring, Rails, Django, Express, gin, etc |
| `kern_entry_points` | explore | low | List framework entry points found in the index: handlers, controllers and route targets with their framework a |
| `kern_search` | explore | low | Ranked free-text symbol search: returns symbols matching a query by name or file, best matches first |
| `kern_repo_search` | explore | low | Ranked free-text symbol search across every repo in the kern multi-repo registry (kern repos add) |
| `kern_why` | explore | low | Rationale and doc-reference report for a symbol: its doc comment, who depends on it and why (each caller's own |
| `kern_code_graph` | explore | low | Return the call graph neighbourhood of a symbol: its definition, its callers, and what it calls |
| `kern_inherits` | explore | low | Return the inheritance edges of a symbol: its supertypes (extends/implements/embeds) and subtypes (what extend |
| `kern_context` | explore | low | Return the minimal relevant source slice for a symbol: its definition source, its callers, and what it calls |
| `kern_changes` | verify | low | Line-aware change-impact analysis for a diff: scopes each changed file to the symbols its added lines actually |
| `kern_review` | verify | low | Token-optimised code-review context for changed files: line-scoped changed symbols (with file:line spans), the |
| `kern_hubs` | explore | low | Architectural hotspots: the most depended-on symbols (hubs) and cross-package bridges where a change in one su |
| `kern_test_gaps` | plan | low | Test-coverage analysis from the call graph: what percent of callable symbols are exercised by tests, plus unte |
| `kern_path` | explore | low | Shortest call path between two symbols, following in-project call edges in either direction |
| `kern_dead` | explore | low | Dead-code detection: symbols nothing in the project calls |
| `kern_larges` | explore | low | Find the largest function/method declarations by source lines |
| `kern_arch` | explore | low | Architecture overview from call-graph communities: subsystems with their hubs/packages, plus coupling warnings |
| `kern_communities` | explore | low | Call-graph communities (label propagation): which symbols cluster together as subsystems, with each cluster's  |
| `kern_churn` | explore | low | Change-frequency risk: which files were touched by the most commits in a range, whether they are being edited  |
| `kern_near` | explore | low | Dependency-tree expansion: every symbol within N hops of a symbol, in both directions (callers + callees), bud |
| `kern_walk` | explore | low | Graph-guided walk: the /walk-graph primitive |
| `kern_probe` | explore | low | Query-driven micro-context router: given a task (bug report, prompt, error text), extract the symbol names it  |
| `kern_trace` | plan | low | Runtime-impact overlay: parse a pprof -top dump, a crash stack trace, or a plain list of function names and ma |
| `kern_lock` | edit | medium | Acquire an advisory workspace lock on a scope (flock-based) |
| `kern_unlock` | edit | medium | Release a workspace lock previously acquired via kern_lock |
| `kern_lock_status` | edit | low | List workspace locks with whether each is held and by which PID |
| `kern_guard_check` | edit | low | Deterministic architectural guardrails: validate changed files against  |
| `kern_authorize_context` | cross | low | Authorized-context primitive (P0 |
| `kern_rename` | edit | high | Structural symbol rename on the AST index (P0-5): previews every definition/reference for a Go package-level s |
| `kern_exec` | edit | critical | Run code in an isolated local runtime and return ONLY stdout — the 'Think in Code' surface |
| `kern_explore` | explore | low | Single-call explore (#2): return a symbol's verbatim source, direct call flow (callers + callees) and transiti |
| `kern_graph` | explore | low | One-call graph context: token-budgeted names-only adjacency for a symbol — callers first (the direction that m |
| `kern_fts_search` | explore | low | FTS5 full-text search (#3) over the SQLite symbol index |
| `kern_bridges` | explore | low | Bridge detection (#4): symbols called from two or more distinct packages/directories — the coupling points whe |
| `kern_cochange` | explore | low | Co-change mode (#6): which files are actually changed together in the same commits (from git history), indepen |
| `kern_usage_guide` | plan | low | Categorized usage guide for every kern MCP tool with performance tiers (fast/moderate/expensive), recommended  |
| `kern_analyze` | plan | medium | HIGH-LEVEL (ADR-0006): analyze a proposed change against the whole system — relevant code, architecture, depen |
| `kern_plan` | plan | medium | HIGH-LEVEL (ADR-0006): produce an implementation plan for a proposed change — affected files, dependencies, ri |
| `kern_execute` | edit | critical | HIGH-LEVEL (ADR-0006): execute a change inside an isolated sandbox worktree (autonomy L2) |
| `kern_verify` | verify | medium | HIGH-LEVEL (ADR-0006): verify a change with the unified verification engine — build, unit tests, security, arc |
| `kern_incident` | cross | medium | HIGH-LEVEL (ADR-0006): investigate a production incident end-to-end — correlate an alert to the affected servi |
| `kern_flight` | cross | low | Replay the AI flight recorder (Workflow E observability): the full recorded trail for one task — every stage,  |
| `kern_what_if` | plan | medium | HIGH-LEVEL (Workflow C / ADR-0012): simulate the impact of a hypothetical change on the knowledge graph — tran |
| `kern_impact` | plan | medium | HIGH-LEVEL: estimate the impact/blast-radius of a change to a symbol — transitively affected symbols/files/ser |
| `kern_risk` | plan | medium | HIGH-LEVEL: the governance risk assessment for a proposed change — the same engine behind `kern risk` (CLI) an |
| `kern_memory` | cross | medium | HIGH-LEVEL (Workflow E): manage engineering memory — add a lesson, list stored lessons, or recall the most rel |
| `kern_agents` | cross | low | HIGH-LEVEL (Workflow E): build the standard specialist team and list its roster — name, role, capabilities — p |
| `kern_loop` | cross | high | HIGH-LEVEL (Workflow E): run the closed autonomy loop against an intent string and return the stage timeline p |
| `kern_do` | cross | high | HIGH-LEVEL (Workflow E): the MCP counterpart of `kern do` — run the autonomous closed loop (understand→remembe |
| `kern_run` | cross | high | HIGH-LEVEL (Workflow E): run an intent through the full task pipeline — compiles the intent, selects workflow  |
| `kern_meta` | meta | medium | Single entry point: describe what you need in natural language and kern classifies the request and runs the ri |
| `kern_workflow` | cross | high | HIGH-LEVEL (Workflow E): select and coordinate the agent team without the external caller manually sequencing  |
| `kern_onboard` | cross | medium | Session-start onboarding: ensure the working directory is fully wired to kern in one call |
| `kern_audit` | verify | low | HIGH-LEVEL: return the tamper-evident governance audit log for the project (every firewall decision/approval) |
| `kern_approve` | edit | medium | HIGH-LEVEL: resolve a governance approval gate |
| `kern_correlate` | cross | medium | HIGH-LEVEL: correlate a production alert against the runtime to produce a deep evidence chain (alert→service→d |
| `kern_learn` | cross | medium | HIGH-LEVEL: extract recurring patterns from engineering memory and surface those above a threshold |
| `kern_modernize` | cross | medium | HIGH-LEVEL: analyze the monolith and produce a phased modernization plan (communities→bridges→churn→candidate  |
| `kern_health` | cross | low | Returns a real-time health and self-observability snapshot of the kern MCP server: index freshness, symbol cou |
| `kern_compose` | cross | high | Executes an ordered pipeline of kern tools in a single RPC round-trip, passing intermediate outputs to downstr |
| `kern_pre_edit` | plan | medium | Predicts the blast radius, direct callers, untested dependencies, and boundary risks of modifying a specific f |
| `kern_prompt_fill` | cross | low | Dynamically renders standardized, token-efficient agent prompts with auto-injected project layout and memory l |
| `kern_semantic_diff` | cross | low | Computes a functional AST-level symbol diff instead of raw line noise: surfaces modified functions, changed si |
| `kern_evidence_anchor` | verify | medium | Validates code claims or citations (symbol, file:line), corrects line drift, and generates a tamper-evident SH |
| `kern_context_watch` | cross | low | Monitors and audits rolling agent context, detects bloated log/code dumps, and recommends concrete determinist |
| `kern_agent_fingerprint` | cross | low | Hashes and evaluates an agent's tool-call pattern from the audit trail to detect repetitive loops, anomalous t |
| `kern_explain` | explore | low | Synthesizes an end-to-end architectural narrative for a symbol or file: purpose, callers, callees, interfaces, |
| `kern_cross_repo_impact` | plan | medium | Evaluates multi-repository blast radius: detects contract breaking changes, shared symbol dependencies, and cr |
| `kern_memory_ranked` | cross | low | Retrieves past project lessons weighted by keyword relevance and exponential time decay (half-life), ensuring  |
| `kern_policy_dsl` | verify | low | Evaluates diffs, changed files, and imported libraries against declarative policy-as-code rules (banned packag |
| `kern_agent_coordination` | cross | medium | Workspace coordination protocol for multi-agent teams: register handoffs, claim/release exclusive resource loc |
| `kern_agent_role_rbac` | cross | high | Enforces identity-based role access control (RBAC): restricts sensitive tools (exec, delete, fix) based on age |
| `kern_stream` | cross | low | Inspects streaming status, partitions large responses into token-friendly chunks, and manages progress notific |
| `kern_ast_transform` | edit | high | Executes deterministic AST-level transformations on code: scaffolding interface method stubs, adding struct fi |
| `kern_semantic_merge` | edit | high | Performs AST-aware 3-way code merge between base, local, and remote versions |
| `kern_deploy` | edit | critical | Deploy a task through TaskService |
| `kern_runtime` | explore | low | Production-intelligence snapshot: kern_runtime with action=status reports which runtime source is wired (live  |
| `kern_evidence` | verify | medium | Signed-evidence read path: kern_evidence with action=verify validates an evidence bundle (args |
| `kern_synthesize_test` | verify | medium | Automatically synthesizes comprehensive table-driven unit tests, parameter fixtures, and boundary invariants f |
| `kern_org_projects` | cross | low | Enterprise org admin: list registered projects (C11) |
| `kern_org_agents` | cross | medium | Enterprise org admin: register or list agent identities (C11) |
| `kern_org_teams` | cross | medium | Enterprise org admin: manage teams that group agents and own projects (C11) |
| `kern_org_memory` | cross | medium | Enterprise org admin: org-level shared memory visible across all projects (C11) |
| `kern_org_tasks` | cross | low | Enterprise org admin: aggregate task visibility (C11) |
| `kern_org_search` | cross | low | Enterprise org admin: cross-project symbol search (C11) |
| `kern_org_audit` | cross | low | Enterprise org admin: org-level audit log (C11) |

## Detailed contracts

Each section documents one tool: its description, risk level, the parameters it accepts and which parameters are required. Parameter types: `string`, `boolean`, `object`, `array` (default `string`).

## Explore — read / discover

### `kern_compact_file`

- **Phase:** explore
- **Risk level:** low
- **Required:** `path`

Return a compact symbolic summary of a source file (functions, types, line numbers) instead of reading the whole file. Use before reading files in large codebases. Optional tier: 'summary' (default, symbol list), 'full' (entire source), or 'folded' (signatures kept, bodies replaced with 'body elided: N lines' placeholders).

| Parameter | Type | Required | Description |
|---|---|---|---|
| `path` | string | yes | Absolute or relative path of the file to summarize |
| `root` | string | no | Project root; when set, path must stay inside it (defaults to unrestricted) |
| `tier` | string | no | 'summary' (default), 'full', or 'folded' |

### `kern_project_map`

- **Phase:** explore
- **Risk level:** low
- **Required:** `root`

Return a compressed map of a whole project: every source file with its symbols and line counts. Use instead of listing/reading every file in a repo.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | yes | Project root directory |
| `max_files` | string | no | Maximum number of files to include (default 500) |

### `kern_buddy`

- **Phase:** explore
- **Risk level:** low
- **Required:** none

Session onboarding digest for any agent: the project's conventions, layout, entry points and gotchas distilled from the index, docs and recent history. Call once at the start of a session on an unfamiliar repo.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `max_output` | string | no | Raise the MCP output sandbox cap for this call (bytes; 0 disables). The digest is compact by design; use kern_project_map for the full map. |

### `kern_ast_search`

- **Phase:** explore
- **Risk level:** low
- **Required:** `pattern`

AST-level symbol search across a Go project. Supports patterns like 'func greet', 'type *User*', 'method *', '*Handler*'. Returns definitions with file:line.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `pattern` | string | yes | Symbol pattern. Prefixes: func, method, struct, interface, type, const, var. '*' wildcards supported |
| `root` | string | no | Project root (defaults to current directory) |
| `limit` | string | no | Max results (default 50) |

### `kern_frameworks`

- **Phase:** explore
- **Risk level:** low
- **Required:** none

Detect the frameworks and libraries a project uses (Spring, Rails, Django, Express, gin, etc.) by scanning manifests and source markers. Use to know what stack the codebase is on.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |

### `kern_entry_points`

- **Phase:** explore
- **Risk level:** low
- **Required:** none

List framework entry points found in the index: handlers, controllers and route targets with their framework and route (e.g. spring-mvc UserController.list /api/users). Search for all symbols with the 'entry' kind prefix via kern_ast_search.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `limit` | string | no | Max results (default 50) |
| `pattern` | string | no | Optional route/name wildcard filter, e.g. '*admin*' |

### `kern_search`

- **Phase:** explore
- **Risk level:** low
- **Required:** `query`

Ranked free-text symbol search: returns symbols matching a query by name or file, best matches first. Forgiving lookup for humans — 'load index' or 'login handler' work, and prose hits camelCase symbols by name segment ('state machine' -> OrderStateMachine), plural-folded ('user services' -> UserService), accent-normalized ('résolution' -> ResolveResolution), or as a camelCase query ('stateMachine'). Set semantic=true to re-rank results by dense embeddings from a local Ollama server (embedding model 

| Parameter | Type | Required | Description |
|---|---|---|---|
| `query` | string | yes | Free-text query (symbol name, path fragment, or partial name) |
| `root` | string | no | Project root (defaults to current directory) |
| `limit` | string | no | Max results (default 20) |
| `semantic` | string | no | When 'true', re-rank by Ollama dense embeddings (requires embedding model) |

### `kern_repo_search`

- **Phase:** explore
- **Risk level:** low
- **Required:** `query`

Ranked free-text symbol search across every repo in the kern multi-repo registry (kern repos add). Returns matches tagged with their repo name, best hits first. Set semantic=true to re-rank pooled results by Ollama dense embeddings.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `query` | string | yes | Free-text query (symbol name, path fragment, or partial name) |
| `limit` | string | no | Max results (default 20) |
| `semantic` | string | no | When 'true', re-rank by Ollama dense embeddings (requires embedding model) |

### `kern_why`

- **Phase:** explore
- **Risk level:** low
- **Required:** `symbol`

Rationale and doc-reference report for a symbol: its doc comment, who depends on it and why (each caller's own doc line), and its in/out edge counts. Use to answer 'why does this exist and who needs it'.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `symbol` | string | yes | Symbol name or Receiver.Name |
| `root` | string | no | Project root (defaults to current directory) |

### `kern_code_graph`

- **Phase:** explore
- **Risk level:** low
- **Required:** `symbol`

Return the call graph neighbourhood of a symbol: its definition, its callers, and what it calls. Use to understand dependencies without reading whole files.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `symbol` | string | yes | Symbol name (e.g. 'greet' or 'User.Login') |
| `root` | string | no | Project root (defaults to current directory) |

### `kern_inherits`

- **Phase:** explore
- **Risk level:** low
- **Required:** `symbol`

Return the inheritance edges of a symbol: its supertypes (extends/implements/embeds) and subtypes (what extends/implements/embeds it). Use to see class hierarchies without reading whole files.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `symbol` | string | yes | Symbol name (e.g. 'Item' or 'Logger') |
| `root` | string | no | Project root (defaults to current directory) |

### `kern_context`

- **Phase:** explore
- **Risk level:** low
- **Required:** `symbol`

Return the minimal relevant source slice for a symbol: its definition source, its callers, and what it calls. Use instead of reading an entire file.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `symbol` | string | yes | Symbol name (e.g. 'greet') |
| `root` | string | no | Project root (defaults to current directory) |
| `lines` | string | no | Lines of source context around the definition (default 12) |
| `agent_id` | string | no | Agent identity for governed mode (P1.2): enables authorized-context filtering — results are scoped to what this agent may read. Omit for raw (ungoverned) mode. |
| `task` | string | no | Task ID for governed mode; pairs with agent_id to scope authorization to the task paths. |
| `scope` | object | no | Optional task scope object {paths, denied_paths, services, envs, artifacts} for governed mode. |
| `max_tokens` | string | no | Optional token budget cap. When provided, automatically fits the slice within this limit using deterministic budget compaction. |
| `with_freshness` | string | no | When 'true', append a ---freshness-proof--- footer with the index's content-addressed freshness proof |

### `kern_hubs`

- **Phase:** explore
- **Risk level:** low
- **Required:** none

Architectural hotspots: the most depended-on symbols (hubs) and cross-package bridges where a change in one subsystem can break another.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `limit` | string | no | Max hubs to return (default 10) |

### `kern_path`

- **Phase:** explore
- **Risk level:** low
- **Required:** `from`, `to`

Shortest call path between two symbols, following in-project call edges in either direction. Traces how two things connect without reading files.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `from` | string | yes | Source symbol (simple name or Type.Method) |
| `to` | string | yes | Target symbol (simple name or Type.Method) |

### `kern_dead`

- **Phase:** explore
- **Risk level:** low
- **Required:** none

Dead-code detection: symbols nothing in the project calls. Private names are dead for certain; public names may be external API. Sorted by size so the biggest cleanup wins show first. Callers reached through function values or interface dispatch are invisible to the index and are reported as dead — confirm before removing.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `limit` | string | no | Max entries (default all) |

### `kern_larges`

- **Phase:** explore
- **Risk level:** low
- **Required:** none

Find the largest function/method declarations by source lines. Use to locate god functions that beg for refactoring.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `min_lines` | string | no | Size threshold in source lines (default 60) |
| `limit` | string | no | Max results (default all) |

### `kern_arch`

- **Phase:** explore
- **Risk level:** low
- **Required:** none

Architecture overview from call-graph communities: subsystems with their hubs/packages, plus coupling warnings ranking the cross-community call bundles that make changes ripple.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |

### `kern_communities`

- **Phase:** explore
- **Risk level:** low
- **Required:** none

Call-graph communities (label propagation): which symbols cluster together as subsystems, with each cluster's size and hub. Use to name the architecture's parts before refactoring.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `limit` | string | no | Max communities to return (default all) |

### `kern_churn`

- **Phase:** explore
- **Risk level:** low
- **Required:** none

Change-frequency risk: which files were touched by the most commits in a range, whether they are being edited right now, and how risky they are in the call graph.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `range` | string | no | Git range like 'HEAD~10..HEAD' (default last 30 commits) |

### `kern_near`

- **Phase:** explore
- **Risk level:** low
- **Required:** `symbol`

Dependency-tree expansion: every symbol within N hops of a symbol, in both directions (callers + callees), budget-capped. The graph-guided traversal primitive that replaces blind grep — e.g. 'everything two degrees from this database model' in one call.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `symbol` | string | yes | Root symbol (simple name or Type.Method) |
| `depth` | string | no | Number of hops to expand (default 2) |
| `max` | string | no | Maximum nodes to return (default 100) |
| `root` | string | no | Project root (defaults to current directory) |

### `kern_walk`

- **Phase:** explore
- **Risk level:** low
- **Required:** `symbol`

Graph-guided walk: the /walk-graph primitive. Returns an indented parent-child dependency tree of every symbol up to N hops away from a symbol, across files, with file:line per node. Alias of kern_near with a tree-oriented description; use instead of grepping or reading whole files to locate code.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `symbol` | string | yes | Root symbol (simple name or Type.Method) |
| `depth` | string | no | Number of hops to expand (default 2) |
| `max` | string | no | Maximum nodes to return (default 100) |
| `root` | string | no | Project root (defaults to current directory) |

### `kern_probe`

- **Phase:** explore
- **Risk level:** low
- **Required:** `task`

Query-driven micro-context router: given a task (bug report, prompt, error text), extract the symbol names it mentions, resolve them against the index, and return a budget-capped bundle of definitions, callers, callees and tests. The graph is the retrieval index, never the payload.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `task` | string | yes | Natural-language task, bug report or error text mentioning symbols |
| `root` | string | no | Project root (defaults to current directory) |
| `max_tokens` | string | no | Token budget for the bundle (default 4000) |

### `kern_explore`

- **Phase:** explore
- **Risk level:** low
- **Required:** `symbol`

Single-call explore (#2): return a symbol's verbatim source, direct call flow (callers + callees) and transitive blast radius (with affected files) in one shot. The primitive that replaces three separate calls (graph/near/path) for 'what touches this and how'. Pass depth=N to cap the blast radius to N hops and max=N to cap node count.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `symbol` | string | yes | Symbol name (e.g. 'greet' or 'User.Login') |
| `root` | string | no | Project root (defaults to current directory) |
| `depth` | string | no | Cap blast radius to N hops from the symbol (default 0 = unlimited) |
| `max` | string | no | Maximum blast-radius symbols to return (default 0 = unlimited) |
| `agent_id` | string | no | Agent identity for governed mode (P1.2): enables authorized-context filtering — results are scoped to what this agent may read. Omit for raw (ungoverned) mode. |
| `task` | string | no | Task ID for governed mode; pairs with agent_id to scope authorization to the task paths. |
| `scope` | object | no | Optional task scope object {paths, denied_paths, services, envs, artifacts} for governed mode. |
| `with_freshness` | string | no | When 'true', append a ---freshness-proof--- footer with the index's content-addressed freshness proof |

### `kern_graph`

- **Phase:** explore
- **Risk level:** low
- **Required:** `symbol`

One-call graph context: token-budgeted names-only adjacency for a symbol — callers first (the direction that matters for impact), then callees, every edge tagged EXTRACTED/INFERRED/AMBIGUOUS, plus community membership. Calls to interface methods carry dispatch hints listing the concrete implementations they can reach. Parity with code-review-graph's minimal_context: the minimal caller-first answer sized to the context window, no source text.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `symbol` | string | yes | Symbol name (simple name or Type.Method) |
| `root` | string | no | Project root (defaults to current directory) |
| `max_tokens` | string | no | Token budget for the names-only adjacency (default 400) |
| `agent_id` | string | no | Agent identity for governed mode (P1.2): enables authorized-context filtering — results are scoped to what this agent may read. Omit for raw (ungoverned) mode. |
| `task` | string | no | Task ID for governed mode; pairs with agent_id to scope authorization to the task paths. |
| `scope` | object | no | Optional task scope object {paths, denied_paths, services, envs, artifacts} for governed mode. |
| `with_freshness` | string | no | When 'true', append a ---freshness-proof--- footer with the index's content-addressed freshness proof |

### `kern_fts_search`

- **Phase:** explore
- **Risk level:** low
- **Required:** `query`

FTS5 full-text search (#3) over the SQLite symbol index. Supports MATCH syntax ('greet', 'func AND greet', `file:\"main.go\"`). Requires a build with -tags sqlite and a persisted index. Falls back to a clear error on the default build.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `query` | string | yes | FTS5 MATCH query over symbols |
| `root` | string | no | Project root (defaults to current directory) |
| `limit` | string | no | Max results (default 20) |

### `kern_bridges`

- **Phase:** explore
- **Risk level:** low
- **Required:** none

Bridge detection (#4): symbols called from two or more distinct packages/directories — the coupling points where a change in one subsystem can break another. Ranks bridges by number of calling packages then caller count.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `limit` | string | no | Max bridges to return (default 15) |

### `kern_cochange`

- **Phase:** explore
- **Risk level:** low
- **Required:** none

Co-change mode (#6): which files are actually changed together in the same commits (from git history), independent of the call graph. Grades change risk by co-change frequency: files that co-change with the current edits are the ones most likely to break next. Use before a commit to see what else must change in lockstep.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `range` | string | no | Git range like 'HEAD~10..HEAD' (default last 30 commits) |
| `limit` | string | no | Max co-change pairs to return (default 20) |

### `kern_explain`

- **Phase:** explore
- **Risk level:** low
- **Required:** `target`

Synthesizes an end-to-end architectural narrative for a symbol or file: purpose, callers, callees, interfaces, and testing posture in a single call.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `target` | string | yes | Target symbol name or relative file path to explain |
| `root` | string | no | Project root directory (defaults to current directory) |

### `kern_runtime`

- **Phase:** explore
- **Risk level:** low
- **Required:** none

Production-intelligence snapshot: kern_runtime with action=status reports which runtime source is wired (live adapter via KERN_PROMETHEUS_URL/KERN_OTEL_URL/KERN_K8S_API or .kern/runtime.json) plus per-service profiles (events/errors/error rate); action=drift compares runtime routes against code-declared routes (template-aware). JSON output, mirroring `kern runtime status|drift --json`.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `action` | string | no | 'status' (default) or 'drift' |
| `root` | string | no | Project root (defaults to current directory) |

### `kern_retrieve`

- **Phase:** explore
- **Risk level:** low
- **Required:** `query` (level l1) or `symbol` (level l2/l3)

Progressive-disclosure retrieval (P1): fetch a project's symbols through escalating levels of detail — L1 index summary (names/kinds/token costs), L2 neighborhood (callers/callees/tests/files), L3 verbatim source — with every result attached to a stable, content-hashable Handle that kern_resolve can later map back to the symbol. Retrieval is deterministic and local. Governance (P0.1): without agent_id the default agent's cwd-scoped scope governs; pass agent_id/task/scope for authorized-context filtering.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `query` | string | no | L1 search query (required for level l1) |
| `symbol` | string | no | Symbol name (required for level l2/l3) |
| `level` | string | no | Disclosure level: 'l1', 'l2', or 'l3' (default 'l2') |
| `limit` | string | no | L1 result cap (default 10) |
| `depth` | string | no | L2 blast-radius depth (0 = unlimited) |
| `max` | string | no | L2 node cap (0 = unlimited) |
| `lines` | string | no | L3 context lines around definition (default 12) |
| `max_tokens` | string | no | Render budget; results over it are fitted/truncated |
| `root` | string | no | Project root (defaults to current directory) |

### `kern_resolve`

- **Phase:** explore
- **Risk level:** low
- **Required:** `handle`

Map a retrieval Handle back to the symbol it describes and re-render it at a disclosure level (default L2 neighborhood). Accepts the full handle ID or the 8-char prefix that kern_retrieve output renders. Handles expire with the registry (in-memory, per-server) — an expired handle errors and must be re-retrieved. Governance mirrors kern_retrieve.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `handle` | string | yes | Handle ID (or 8-char prefix) from a kern_retrieve result |
| `level` | string | no | Disclosure level to render: 'l1', 'l2', or 'l3' (default 'l2') |
| `max_tokens` | string | no | Render budget; results over it are fitted/truncated |
| `root` | string | no | Project root (defaults to current directory) |

### `kern_context_envelope`

- **Phase:** explore
- **Risk level:** medium
- **Required:** `change`

Assemble the versioned context envelope for a proposed change: runs the context engine, stamps EnvelopeVersion V1 and SchemaVersion 1.0.0, optionally fits the rendered packet text to a token budget, and returns the envelope as pretty-printed JSON. `with_freshness` appends the index freshness footer (best-effort; a load failure never fails the call).

| Parameter | Type | Required | Description |
|---|---|---|---|
| `change` | string | yes | Proposed change description the envelope is built for |
| `max_tokens` | string | no | Token budget; the packet's FittedText is budget-fitted when set |
| `with_freshness` | boolean | no | If true, append the index freshness footer (default false) |
| `root` | string | no | Project root (defaults to current directory) |

## Plan — analyze / simulate

### `kern_plan_context`

- **Phase:** plan
- **Risk level:** medium
- **Required:** `change`

Deterministic context planner (P3): classify a proposed change, select the packet facts that fit the task type, and size the selection to a token budget. Returns the plan via RenderPlan (task type, per-fact selections with token costs, total budget) or as raw JSON with `json=true`. No LLM — pure deterministic planning over the analyzed context packet.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `change` | string | yes | Proposed change description to plan context for |
| `budget` | string | no | Token budget for the selected facts (0 = no fitting) |
| `json` | boolean | no | If true, return the plan as raw JSON (default false) |
| `root` | string | no | Project root (defaults to current directory) |

### `kern_swap`

- **Phase:** plan
- **Risk level:** low
- **Required:** `text`

Budget swapping (#18): in a context document, replace fenced code blocks tagged `lang:path` with per-file symbolic signatures to fit a token budget, or expand `lang:path:summary` blocks back to full file contents. Returns the budget-fitted document.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `text` | string | yes | The context document containing fenced code blocks |
| `root` | string | no | Project root used to resolve block paths (defaults to current directory) |
| `max_tokens` | string | no | Token budget; if the document exceeds it, blocks are swapped to summaries |
| `mode` | string | no | force mode: summary, expand, or fit (default) |

### `kern_pack`

- **Phase:** plan
- **Risk level:** low
- **Required:** `root`

Pack a whole project into one paste-ready bundle: project instructions, a directory tree with per-file token counts, and file contents, sized to fit max_tokens. Use when an agent needs the full working picture (source to edit against), not just a map. Files are ordered by sha256 of their relative path so re-packs of the same tree are byte-identical (LLM prompt-cache friendly). Set fold=true to pack signatures with bodies elided.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | yes | Project root directory |
| `max_tokens` | string | no | Token budget for the bundle (default 8000; 0 = unlimited — use with max_output=0 to avoid the output sandbox) |
| `format` | string | no | 'text' (default) or 'json' |
| `instructions` | string | no | 'true' to include root-level docs as instructions (default), 'false' to skip them |
| `fold` | string | no | 'true' to pack tier=folded content (signatures kept, bodies elided with line counts) |
| `tier` | string | no | Content tier: 'full' (default), 'folded', or 'summary' |

### `kern_context_budget`

- **Phase:** plan
- **Risk level:** low
- **Required:** `text`

Fit text into a token budget: deduplicate lines, keep the head plus important lines (errors, stack frames), then trim. Use to manage a crowded context window before adding more content.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `text` | string | yes | The text (log output, file dump, conversation) to fit into the budget |
| `max_tokens` | string | no | Maximum tokens the result may use (default 4000) |

### `kern_test_gaps`

- **Phase:** plan
- **Risk level:** low
- **Required:** none

Test-coverage analysis from the call graph: what percent of callable symbols are exercised by tests, plus untested hotspots (called by many, covered by none).

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `limit` | string | no | Max hotspots to list (default 10) |

### `kern_trace`

- **Phase:** plan
- **Risk level:** low
- **Required:** `trace`

Runtime-impact overlay: parse a pprof -top dump, a crash stack trace, or a plain list of function names and map the hot symbols onto the call graph — file:line, blast radius, test coverage and risk. Use to see what a hot path touches at runtime.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `trace` | string | yes | The trace text (pprof -top, stack trace, or symbol list) |
| `root` | string | no | Project root (defaults to current directory) |
| `limit` | string | no | Max hot symbols to return (default all) |

### `kern_usage_guide`

- **Phase:** plan
- **Risk level:** low
- **Required:** none

Categorized usage guide for every kern MCP tool with performance tiers (fast/moderate/expensive), recommended workflows, and pitfalls. Consult this first when deciding which tool fits a task.

_No input parameters._

### `kern_analyze`

- **Phase:** plan
- **Risk level:** medium
- **Required:** `change`

HIGH-LEVEL (ADR-0006): analyze a proposed change against the whole system — relevant code, architecture, dependencies, historical memory, blast radius, risks, evidence, and required validation. This is the Kern 2.0 killer workflow 'Analyze this proposed change' exposed over MCP.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `change` | string | yes | The change/symbol to analyze, e.g. 'Add a Greet function' or 'helper' |

### `kern_plan`

- **Phase:** plan
- **Risk level:** medium
- **Required:** `change`

HIGH-LEVEL (ADR-0006): produce an implementation plan for a proposed change — affected files, dependencies, risks and required validation. Deterministic plan over the analysis; no LLM required.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `change` | string | yes | The change to plan, e.g. 'Add a Greet function to main.go' |

### `kern_what_if`

- **Phase:** plan
- **Risk level:** medium
- **Required:** `change`

HIGH-LEVEL (Workflow C / ADR-0012): simulate the impact of a hypothetical change on the knowledge graph — transitively affected symbols, files, services, tests, a deterministic risk level, and a typed RECOMMENDATION claim. Read-only; never mutates the graph or index.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `change` | string | yes | The symbol to change/remove (qualified name), e.g. 'helper' |
| `kind` | string | no | Change kind: 'remove_symbol' (default) or 'change_dependency' |
| `new_target` | string | no | For change_dependency: the symbol Target now depends on |

### `kern_impact`

- **Phase:** plan
- **Risk level:** medium
- **Required:** `change`

HIGH-LEVEL: estimate the impact/blast-radius of a change to a symbol — transitively affected symbols/files/services/tests, deterministic risk, and typed claims. Read-only.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `change` | string | yes | The symbol to change/remove (qualified name), e.g. 'helper' |
| `kind` | string | no | Change kind: 'remove_symbol' (default) or 'change_dependency' |
| `new_target` | string | no | For change_dependency: the symbol Target now depends on |

### `kern_risk`

- **Phase:** plan
- **Risk level:** medium
- **Required:** `change`

HIGH-LEVEL: the governance risk assessment for a proposed change — the same engine behind `kern risk` (CLI) and POST /v1/risk (REST): the context engine's risk claims (level, score, factors), firewall check result (allowed/blocked, approval requirement), and required validations. Read-only.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `change` | string | yes | The proposed change: a bare symbol name or a description containing one, e.g. 'add a new route handler in the auth package' |

### `kern_pre_edit`

- **Phase:** plan
- **Risk level:** medium
- **Required:** none

Predicts the blast radius, direct callers, untested dependencies, and boundary risks of modifying a specific file or symbol BEFORE changes are made. Saves agents from making risky changes or incurring expensive rollback cycles.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `file` | string | no | Relative or absolute path to the file to be edited |
| `lines` | string | no | Optional line or line range being edited, e.g. '45-90' or '120' |
| `symbol` | string | no | Optional specific symbol name targeted for modification |
| `root` | string | no | Project root directory (defaults to current directory) |

### `kern_cross_repo_impact`

- **Phase:** plan
- **Risk level:** medium
- **Required:** `target_symbol`

Evaluates multi-repository blast radius: detects contract breaking changes, shared symbol dependencies, and cross-repo interface divergences.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `target_symbol` | string | yes | Exported symbol or interface undergoing changes |
| `linked_repos` | array | no | Array of paths to linked repositories or microservices to scan |
| `root` | string | no | Project root directory (defaults to current directory) |

## Edit — mutate / execute

### `kern_safe_delete`

- **Phase:** edit
- **Risk level:** high
- **Required:** `symbol`

Check whether a symbol can be safely deleted: reports in-project callers (production vs test-only), whether it is exported or an entry point, and a conservative SAFE/NOT SAFE verdict. Use before removing dead code.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `symbol` | string | yes | Symbol name (simple name like 'greet' or qualified like 'User.Login') |
| `root` | string | no | Project root (defaults to current directory) |
| `format` | string | no | Output format: text or json (default text) |

### `kern_commitmsg`

- **Phase:** edit
- **Risk level:** low
- **Required:** none

Generate a deterministic conventional-commit message (type, scope, subject, per-file body) from the git diff — rule-based, no LLM, no network; the same diff always yields the same message. Use when a commit needs a starting message the human can tweak.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `staged` | string | no | If true, read the staged diff (git diff --cached) instead of the working tree vs HEAD |
| `range` | string | no | Optional commit range like a..b; overrides staged and HEAD defaults |

### `kern_sandbox`

- **Phase:** edit
- **Risk level:** critical
- **Required:** `command`

Run a risky command inside a snapshot of the project (#15): on non-zero exit the tree is rolled back exactly (files restored, new files removed). Success keeps changes unless they touch HIGH-risk files, in which case the tree is likewise restored unless force=true. Use before destructive operations, migrations, or agent-applied edits. Gated by the command-execution governance firewall (KERN_ALLOW_EXEC / KERN_TOOLS) and command output is PII/secret-masked before return.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root to snapshot and run in (defaults to current directory) |
| `command` | string | yes | Full command to run, e.g. \"make migrate\" or \"sh -c 'npm test'\" (shell words, not a shell string) |
| `timeout` | string | no | Timeout in seconds (default 120) |
| `force` | string | no | If true, keep changes even when touched files carry a HIGH pre-edit verdict (default restores) |

### `kern_heal`

- **Phase:** edit
- **Risk level:** high
- **Required:** none

Self-correction loop (#9): run validation; on failure ask a local Ollama model to rewrite the failing files, apply the fix inside a throwaway snapshot, re-validate, and report a diff to review. Never edits the user's working tree. Requires Ollama at localhost:11434.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `task` | string | no | The original task the code is meant to fulfil |
| `model` | string | no | Ollama model (default KERN_MODEL or llama3.2) |
| `max_rounds` | string | no | Correction attempts (default 3) |
| `timeout` | string | no | Validation timeout in seconds (default 121) |
| `force` | string | no | If true, attempt repairs even when the failing files carry a HIGH pre-edit verdict (default refuses) |

### `kern_run_build`

- **Phase:** edit
- **Risk level:** critical
- **Required:** `command`

Run a build/test command locally and return only the compact result (exit status + errors), not full output. Use for builds, tests, linting to save context.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `command` | string | yes | Shell command to run |
| `dir` | string | no | Working directory for the command |

### `kern_lock`

- **Phase:** edit
- **Risk level:** medium
- **Required:** `scope`

Acquire an advisory workspace lock on a scope (flock-based). Held by this server until kern_unlock. Lets concurrent agents coordinate before touching shared files. Errors when the scope is already held.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `scope` | string | yes | Lock scope, e.g. 'db-models' or 'checkout' |
| `root` | string | no | Project root (defaults to current directory) |

### `kern_unlock`

- **Phase:** edit
- **Risk level:** medium
- **Required:** `scope`

Release a workspace lock previously acquired via kern_lock.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `scope` | string | yes | Lock scope to release |

### `kern_lock_status`

- **Phase:** edit
- **Risk level:** low
- **Required:** none

List workspace locks with whether each is held and by which PID. Use to see what other agents are working on.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |

### `kern_guard_check`

- **Phase:** edit
- **Risk level:** low
- **Required:** none

Deterministic architectural guardrails: validate changed files against .kern/boundaries.json rules and return every forbidden dependency crossing (e.g. a frontend importing a backend DB model) with file evidence. Rejects a proposal before it touches the filesystem. Use format=sarif for a SARIF 2.1.0 report (GitHub code scanning / Azure DevOps) and threshold=N to fail (isError) when the violation count exceeds N.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `file` | string | no | Optional comma-separated explicit file list, overrides git range |
| `range` | string | no | Git range like 'HEAD~2..HEAD'. Empty = working-tree changes |
| `format` | string | no | Output format: text (default) or sarif |
| `threshold` | string | no | Fail (isError) when the violation count exceeds this number (default 0 = any violation fails) |

### `kern_rename`

- **Phase:** edit
- **Risk level:** high
- **Required:** `symbol`, `new_name`

Structural symbol rename on the AST index (P0-5): previews every definition/reference for a Go package-level symbol (types, funcs, vars, consts) with file:line:col edits, then applies them transactionally when apply=true. Edits come from a real go/ast parse, so strings, comments, struct-field names, composite-literal keys, import aliases and the package clause are never touched; cross-package references (pkg.Symbol) are handled for exported symbols. Before applying, every touched file is backed up under <root>/.kern/rename-backup/ and a mid-flight failure restores all files. Method rename and non-Go symbols are refused. Returns the preview (or apply result) as text.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `symbol` | string | yes | Symbol to rename (package-level Go name, e.g. Widget) |
| `new_name` | string | yes | New identifier |
| `apply` | string | no | If true, commit the rename (with backups + rollback); otherwise return the preview only |
| `force` | string | no | If true, apply even when the symbol carries a HIGH pre-edit verdict (default refuses) |

### `kern_exec`

- **Phase:** edit
- **Risk level:** critical
- **Required:** `code`

Run code in an isolated local runtime and return ONLY stdout — the 'Think in Code' surface. Language is selected by --lang or a shebang line; runtimes are resolved from PATH (python3, node, go, bash, perl, ruby, php, lua, julia, R, bun, deno, rust, ...). The script runs in a fresh temp dir with a hard timeout (default 10s, override timeout=N), a stdout byte cap (default 16KiB, override max=N), and a sanitized environment (HOME/XDG pointed into the sandbox, secrets stripped). Isolation is enforced: the script runs in a private network namespace when the platform supports it, and the run refuses to execute if network isolation is unavailable (never silently runs with full network). stderr is never mixed into stdout and is only surfaced on failure. On platforms where private network namespaces are unavailable (e.g. macOS, some containers) the run refuses to execute — it fails closed instead of degrading to full network egress — unless the local operator sets KERN_ALLOW_UNISOLATED=1 (alias KERN_ALLOW_NET=1). Use it to compute things (math, data munging, JSON transforms) without polluting context.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `code` | string | yes | The script body (required) |
| `lang` | string | no | Language override (e.g. python3, node, bash, go); otherwise detected from the shebang |
| `timeout` | string | no | Timeout in seconds (default 10) |
| `max` | string | no | Max stdout bytes to return (default 16384) |
| `stdin` | string | no | Input piped to the script's stdin |
| `list` | string | no | If true, return the installed runtimes and supported languages and do nothing else |
| `no_isolate` | string | no | Ignored unless the local operator sets KERN_ALLOW_NO_ISOLATE=1; isolation is enforced by default |

### `kern_execute`

- **Phase:** edit
- **Risk level:** critical
- **Required:** `patch`

HIGH-LEVEL (ADR-0006): execute a change inside an isolated sandbox worktree (autonomy L2). Applies the given unified diff, verifies it builds, and returns the resulting diff. Never mutates the live repository.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `patch` | string | yes | A unified diff to apply to the sandbox worktree |

### `kern_approve`

- **Phase:** edit
- **Risk level:** medium
- **Required:** none

HIGH-LEVEL: resolve a governance approval gate. With no id, lists pending approvals. With an id, approves it; set reject=true to reject instead. CLI-equivalent: kern approve. Agents hit this when kern_run/kern_workflow parks at the human approval gate — the returned error carries the approval ID.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `id` | string | no | Approval ID to approve/reject (omit to list pending approvals) |
| `reject` | string | no | If true, reject the approval instead of approving it (default false) |
| `reason` | string | no | Optional reason for the decision |
| `approver` | string | no | Optional approver identity (defaults to 'mcp-user') |

### `kern_ast_transform`

- **Phase:** edit
- **Risk level:** high
- **Required:** none

Executes deterministic AST-level transformations on code: scaffolding interface method stubs, adding struct fields, or inserting methods without fragile whitespace or regex diff errors.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `action` | string | no | Action: 'implement_interface' (default), 'add_field', 'add_method' |
| `file` | string | no | Path to target source file |
| `code` | string | no | Raw code snippet if file is not provided |
| `target_symbol` | string | no | Target struct or type name (e.g. 'Server') |
| `interface_name` | string | no | Interface to implement (e.g. 'io.Reader', 'http.Handler') |
| `receiver_name` | string | no | Receiver variable name (e.g. 's') |
| `receiver_type` | string | no | Receiver type name (e.g. '*Server') |
| `field_name` | string | no | For add_field: field name |
| `field_type` | string | no | For add_field: field type |
| `field_tag` | string | no | For add_field: struct tag |
| `method_signature` | string | no | For add_method: method signature |
| `method_body` | string | no | For add_method: method body |
| `apply` | string | no | If true, writes changes directly to file |
| `format` | string | no | Output format: 'text' (default) or 'json' |
| `root` | string | no | Project root directory |

### `kern_semantic_merge`

- **Phase:** edit
- **Risk level:** high
- **Required:** none

Performs AST-aware 3-way code merge between base, local, and remote versions. Resolves non-overlapping struct fields, methods, imports, and declarations cleanly, and flags precise semantic conflicts.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `file` | string | no | Target file path |
| `base` | string | no | Base version code or file path |
| `local` | string | no | Local version code or file path |
| `remote` | string | no | Remote version code or file path |
| `base_file` | string | no | Optional file path for base version |
| `local_file` | string | no | Optional file path for local version |
| `remote_file` | string | no | Optional file path for remote version |
| `apply` | string | no | If true and clean, writes merged result to target file (default false) |
| `format` | string | no | Output format: 'text' (default) or 'json' |
| `root` | string | no | Project root directory |

### `kern_deploy`

- **Phase:** edit
- **Risk level:** critical
- **Required:** `task_id`

Deploy a task through TaskService.Deploy so the governance firewall, the human-approval gate (real deploys require approval), and lifecycle events all apply — the same path as `kern deploy <task-id>` and POST /v1/tasks/{id}/deploy. Returns the updated task state and deployment ref.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `task_id` | string | yes | Task ID to deploy |
| `version` | string | no | Optional version string (default empty) |
| `root` | string | no | Project root (defaults to current directory) |

## Verify — check / validate

### `kern_security`

- **Phase:** verify
- **Risk level:** high
- **Required:** none

Local security scan of a project's source files: hardcoded secrets, dynamic SQL, shell command injection, weak crypto, insecure randomness and unsafe deserialization. Deterministic and line-scoped. Use before reviewing code or shipping changes.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root to scan (defaults to current directory) |
| `severity` | string | no | Comma-separated severities to include: error,warning,info (default all) |
| `max` | string | no | Max findings to return (default 100; 0 = no cap) |
| `format` | string | no | Output format: text or json (default text) |

### `kern_precache`

- **Phase:** verify
- **Risk level:** medium
- **Required:** none

Speculative pre-caching (#20): scan the project once and fill the code-summary and document-vector caches so later kern calls are instant. Run periodically or after bulk edits.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |

### `kern_diff_files`

- **Phase:** verify
- **Risk level:** low
- **Required:** `a`, `b`

Delta streaming (#13): compute a unified line diff between two files (or two versions of the same file) using pure Go. Returns the full patch, or a note when files are identical. Feed the output back to the model as a compact edit description.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `a` | string | yes | Path to the old/base file |
| `b` | string | yes | Path to the new/changed file |
| `root` | string | no | Project root; when set, a and b must stay inside it (defaults to unrestricted) |

### `kern_validate`

- **Phase:** verify
- **Risk level:** high
- **Required:** none

Auto-validation (#7): detect the project's language-appropriate build/test/syntax command and run it. Returns exit status, truncated output and duration. Use after editing code to gate correctness before final answers.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `command` | string | no | Optional override command, e.g. \"go test ./...\" (defaults to auto-detected) |
| `timeout` | string | no | Timeout in seconds (default 121) |

### `kern_schema_validate`

- **Phase:** verify
- **Risk level:** low
- **Required:** `data`, `schema`

Deterministically validate JSON output against a JSON schema (subset: object/array/primitives, required, enum, min/max/length, pattern, additionalProperties). Returns either a conform message or one line per violation.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `data` | string | yes | The JSON output to validate |
| `schema` | string | yes | The JSON schema to validate against |

### `kern_verify_output`

- **Phase:** verify
- **Risk level:** low
- **Required:** `text`

Hallucination check: extract file:line, symbol-name and route references from an agent's output text and confirm each against the real source tree and index. Returns ok/MISS verdicts for every reference.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `text` | string | yes | The agent output text to verify |
| `root` | string | no | Project root (defaults to current directory) |

### `kern_check_draft`

- **Phase:** verify
- **Risk level:** low
- **Required:** `code`

Validate an agent's draft code against the project index (lighter than LSP, deterministic): Go parse errors, relative imports that do not resolve under root, calls to symbols that are neither declared in the draft nor indexed, and method calls on package aliases not found in the indexed package. Non-Go languages are skipped conservatively.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `code` | string | yes | The draft code to validate |
| `root` | string | no | Project root (defaults to current directory) |
| `lang` | string | no | Code language: \"go\" or empty for Go; any other language is skipped conservatively |

### `kern_taint`

- **Phase:** verify
- **Risk level:** high
- **Required:** none

Taint-lite analysis: flag security sinks (SQL injection, command injection, unsafe deserialization, Python eval/exec/subprocess/pickle/yaml sinks) whose containing function is transitively called by a framework entry point (Symbol.Entry) or whose file contains source expressions (request params, bodies, CLI args). With generate=true, emits a deterministic test scaffold per tainted sink (go test for Go sinks, pytest for Python sinks, G-4) for LLM-assisted fill. The optional range argument scopes findings to files changed in a 'from..to' git range ('..' = working tree). Deterministic, bounded BFS.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root to scan (defaults to current directory) |
| `file` | string | no | Optional filter: only findings in this file path |
| `range` | string | no | Optional git range 'from..to' to scope findings to changed files; '..' means the working tree |
| `generate` | boolean | no | When true, emit a test scaffold per tainted sink (default false) |

### `kern_changes`

- **Phase:** verify
- **Risk level:** low
- **Required:** none

Line-aware change-impact analysis for a diff: scopes each changed file to the symbols its added lines actually touch (from git diff hunks), then computes blast radius (transitive callers), risk scores, and test gaps. Use to review what a PR could break before reading files.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `range` | string | no | Git range like 'HEAD~2..HEAD'. Empty = working-tree changes |
| `file` | string | no | Optional comma-separated explicit file list, overrides git range |

### `kern_review`

- **Phase:** verify
- **Risk level:** low
- **Required:** none

Token-optimised code-review context for changed files: line-scoped changed symbols (with file:line spans), their callers, blast radius, risk and test gaps, sized to fit a token budget. The smallest answer a reviewer needs.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `range` | string | no | Git range like 'HEAD~2..HEAD'. Empty = working-tree changes |
| `file` | string | no | Optional comma-separated explicit file list, overrides git range |
| `max_tokens` | string | no | Maximum tokens for the review context (default 8000) |

### `kern_verify`

- **Phase:** verify
- **Risk level:** medium
- **Required:** none

HIGH-LEVEL (ADR-0006): verify a change with the unified verification engine — build, unit tests, security, architecture, dependency. Returns the typed verdict (PASS/FAIL/WARN) and per-check summary.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `types` | string | no | Comma-separated checks: build,test,security,architecture,dependency (default 'build'; pass 'build,test' for full test suite) |

### `kern_audit`

- **Phase:** verify
- **Risk level:** low
- **Required:** none

HIGH-LEVEL: return the tamper-evident governance audit log for the project (every firewall decision/approval). CLI-equivalent: kern audit. Backs the AUDIT intent workflow.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |

### `kern_evidence_anchor`

- **Phase:** verify
- **Risk level:** medium
- **Required:** none

Validates code claims or citations (symbol, file:line), corrects line drift, and generates a tamper-evident SHA-256 evidence certificate for zero-hallucination code claims.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `claim` | string | no | Claim or citation to verify, e.g. 'internal/index/engine.go:45' or 'NewServer' |
| `file` | string | no | Optional file path of the cited reference |
| `line` | string | no | Optional 1-based line number of the cited reference |
| `symbol` | string | no | Optional symbol name of the cited reference |
| `root` | string | no | Project root directory (defaults to current directory) |

### `kern_policy_dsl`

- **Phase:** verify
- **Risk level:** low
- **Required:** none

Evaluates diffs, changed files, and imported libraries against declarative policy-as-code rules (banned packages, protected paths, max diff size).

| Parameter | Type | Required | Description |
|---|---|---|---|
| `policy` | string | no | Optional inline YAML/JSON policy rules or path to policy file |
| `files` | array | no | List of files being changed or checked |
| `diff` | string | no | Optional unified git diff to scan for banned patterns/imports |
| `imports` | array | no | Optional list of package imports to evaluate |
| `root` | string | no | Project root directory (defaults to current directory) |

### `kern_evidence`

- **Phase:** verify
- **Risk level:** medium
- **Required:** none

Signed-evidence read path: kern_evidence with action=verify validates an evidence bundle (args.file or args.url — fetched without cloning) and reports tamper-seal status, signature status, audit-chain replay and, when args.expect_fingerprint is given, the fingerprint trust-anchor match; action=explain renders the bundle in plain language; action=export builds a bundle from the project's evidence store for args.task_id (or the current state) and returns its path + id. Mirrors `kern evidence export|verify|explain`.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `action` | string | no | 'verify' (default), 'explain', or 'export' |
| `file` | string | no | Bundle JSON file to verify/explain |
| `url` | string | no | Bundle URL to fetch and verify/explain without cloning |
| `task_id` | string | no | Task ID to scope the export (default: current state, like the CLI) |
| `expect_fingerprint` | string | no | Require the bundle to be signed by this key fingerprint (the trust anchor) |
| `root` | string | no | Project root (defaults to current directory) |

### `kern_synthesize_test`

- **Phase:** verify
- **Risk level:** medium
- **Required:** none

Automatically synthesizes comprehensive table-driven unit tests, parameter fixtures, and boundary invariants for untested functions or methods based on AST signatures.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `target` | string | no | Target function or method name (e.g. 'Compute', 'Worker.Process') |
| `file` | string | no | Target source file path |
| `code` | string | no | Raw source code snippet if file is not provided |
| `auto_gap` | string | no | If true and target is empty, picks top untested hotspot from index |
| `apply` | string | no | If true, writes synthesized test to <file>_test.go directly |
| `format` | string | no | Output format: 'text' (default) or 'json' |
| `root` | string | no | Project root directory |

## Meta — routing

### `kern_meta`

- **Phase:** meta
- **Risk level:** medium
- **Required:** `request`

Single entry point: describe what you need in natural language and kern classifies the request and runs the right tool(s) internally. Examples: 'how does dispatch work?' → kern_explore, 'what breaks if I change dispatch?' → kern_impact, 'compress this log: ...' → kern_optimize_log, 'mask secrets in: ...' → kern_mask_pii, 'find the dispatch function' → kern_search, 'show me the architecture' → kern_arch. Prefer this over calling individual kern_* tools — it picks the right one for you.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `request` | string | yes | Natural-language request describing what you need from kern |
| `phase` | string | no | Agent phase: explore\|plan\|edit\|verify. Hints the phase context for routing; the advertised tool list is filtered server-wide via KERN_MCP_PHASE. Optional. |
| `agent_id` | string | no | Agent identity for governed mode (P1.2): enables authorized-context filtering — results are scoped to what this agent may read. Omit for raw (ungoverned) mode. |
| `task` | string | no | Task ID for governed mode; pairs with agent_id to scope authorization to the task paths. |
| `scope` | object | no | Optional task scope object {paths, denied_paths, services, envs, artifacts} for governed mode. |
| `root` | string | no | Project root (defaults to current directory) |

## Cross — phase-agnostic utilities

### `kern_optimize_prompt`

- **Phase:** cross
- **Risk level:** low
- **Required:** `prompt`

Compress and clean a raw prompt before sending it to an LLM. Returns the optimized prompt plus token savings. Use this to reduce context cost for large or noisy prompts. When OLLAMA_HOST points at a non-local (remote) LLM, secrets/PII are masked automatically before processing and restored in the output (the result may contain [MASKED_*] placeholders).

| Parameter | Type | Required | Description |
|---|---|---|---|
| `prompt` | string | yes | The raw prompt text to optimize |
| `attached_log` | string | no | Optional noisy log output to compress and attach |
| `session` | string | no | Optional session identifier for stats tracking |
| `model` | string | no | Optional model name for cost estimation |
| `mask` | string | no | If true, strip secrets/PII before processing and restore placeholders in the output (default false; also auto-enabled for non-local LLM hosts) |
| `mask_names` | string | no | Comma-separated client/project names to mask as [MASKED_NAME_N] |
| `cache` | string | no | If true, serve identical requests from the local response cache (default false) |
| `few_shot` | string | no | If true, inject top recalled lessons from project memory as baseline examples (default false) |
| `root` | string | no | Project root used for few-shot memory (defaults to current directory) |

### `kern_optimize_output`

- **Phase:** cross
- **Risk level:** low
- **Required:** `text`

Compress an LLM's response (assistant output) by stripping filler, pleasantries and hedge language while preserving code blocks, lists, errors and technical content. Deterministic and local, no LLM involved. Use on verbose model replies before they are stored or echoed back into context.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `text` | string | yes | The LLM output text to compress |

### `kern_memory_add`

- **Phase:** cross
- **Risk level:** medium
- **Required:** `lesson`

Persist a distilled, cross-session lesson for a project (the project 'brain'). Agents record what they learned so future sessions can recall it. Appends to the project memory store (most recent 50 entries kept).

| Parameter | Type | Required | Description |
|---|---|---|---|
| `lesson` | string | yes | The lesson to remember, e.g. 'deploy tags are pushed from a manual release workflow, not CI' |
| `root` | string | no | Project root whose memory store to append (defaults to current directory) |

### `kern_memory_list`

- **Phase:** cross
- **Risk level:** low
- **Required:** none

List all stored lessons for a project, most recent first with timestamps.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |

### `kern_memory_recall`

- **Phase:** cross
- **Risk level:** low
- **Required:** `prompt`

Recall the up-to-k most relevant past lessons for a prompt by keyword overlap. Returns only lessons whose tokens match; deterministic and local.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `prompt` | string | yes | Query to match lessons against |
| `root` | string | no | Project root (defaults to current directory) |
| `limit` | string | no | Max lessons to return (default 5) |

### `kern_mask_pii`

- **Phase:** cross
- **Risk level:** low
- **Required:** `text`

Locally scan text for secrets and PII (API keys, passwords, tokens, URLs with credentials, IPs, emails) and replace them with safe [MASKED_*] placeholders. Use before sending any text to a remote LLM. Pure local, deterministic, reversible via the returned mapping.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `text` | string | yes | The raw text to mask |
| `mask_names` | string | no | Optional comma-separated client/project names to mask |

### `kern_doc_search`

- **Phase:** cross
- **Risk level:** low
- **Required:** `query`

Local vector search over a project's documents (markdown, text, rst, adoc). Chunks and embeds docs locally with deterministic n-gram hashing (no ML deps) and returns only the most relevant fragments. Use instead of pasting whole documents into context.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `query` | string | yes | Natural-language or keyword query |
| `root` | string | no | Project root (defaults to current directory) |
| `k` | string | no | Max fragments to return (default 5) |

### `kern_doc_index`

- **Phase:** cross
- **Risk level:** medium
- **Required:** none

Pre-index a project's documents for kern_doc_search. Run once after documents change; searches auto-index on first use. Pass semantic=true to also embed chunks with a local Ollama embedding model (KERN_EMBED_MODEL, default nomic-embed-text); queries then fuse a real-meaning dense signal with the deterministic n-gram vectors and BM25.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `semantic` | string | no | If true, add dense Ollama embeddings to the index (requires a local Ollama with the embedding model pulled) |

### `kern_doc_fetch`

- **Phase:** cross
- **Risk level:** medium
- **Required:** `url`

Fetch a public documentation page and merge it into the project's local doc index so kern_doc_search can find it. This is the ONLY network call in kern and is invoked explicitly by the user; everything else stays local. The page is HTML-stripped, capped, stored under cache/data/docs-fetch and indexed as fetch/<name>.md (re-fetching a name replaces it). Pass semantic=true to also attach dense embeddings via the local Ollama model so the page ranks in semantic search.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `url` | string | yes | https URL of the documentation page to fetch |
| `root` | string | no | Project root whose doc index receives the page (defaults to current directory) |
| `name` | string | no | Optional index name (default derived from the URL host+path) |
| `semantic` | string | no | If true, attach dense embeddings via the local Ollama model (skipped when the model is unavailable) |

### `kern_optimize_log`

- **Phase:** cross
- **Risk level:** low
- **Required:** `log`

Strip noise from log output: keeps errors, warnings, stack traces and build failures, removes timestamps and chatter. Use before pasting logs into context.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `log` | string | yes | The log text to compress |

### `kern_stats`

- **Phase:** cross
- **Risk level:** low
- **Required:** none

Return before/after token savings and cost estimates from kern optimizations, optionally filtered to today or a session.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `days` | string | no | Aggregate over the last N days (default 7) |
| `session` | string | no | Filter to a session identifier |

### `kern_semcache`

- **Phase:** cross
- **Risk level:** medium
- **Required:** `action`

Inspect and manage the semantic cache that serves similar (not just identical) prior queries instantly. Actions: 'stats' (default) lists entries per namespace (prompt/log), 'list' shows the stored inputs of a namespace, 'clear' wipes it (or all), 'similarity' reports the Jaccard overlap of two inputs so you can predict whether a near-duplicate will hit. Use to verify or reset the fuzzy layer.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `action` | string | yes | 'stats' (default), 'list', 'clear', or 'similarity' |
| `namespace` | string | no | prompt or log (default: all) |
| `a` | string | no | First input for similarity |
| `b` | string | no | Second input for similarity |

### `kern_authorize_context`

- **Phase:** cross
- **Risk level:** low
- **Required:** `agent_id`, `task`

Authorized-context primitive (P0.1): compute the exact set of symbols and call edges an agent may legally read for a task, filtered by the agent's identity (firewall context.read permission) and an optional task scope, and return it with an auditable authorization proof (decision, fingerprint, index freshness). Denied symbols are listed with their denial stage and reason. Use before retrieval when a task must not leak out-of-scope code.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `agent_id` | string | yes | Agent ID to authorize (must be registered, e.g. via identity.RegisterAgent / kern_agent) |
| `task` | string | yes | Task ID the authorization is scoped to |
| `root` | string | no | Project root (defaults to current directory) |
| `symbol_filter` | string | no | Optional substring filter applied to the allowed symbols only |
| `scope` | string | no | Optional task scope object: {paths: [], denied_paths: [], services: [], envs: [], artifacts: []} |

### `kern_incident`

- **Phase:** cross
- **Risk level:** medium
- **Required:** `alert`

HIGH-LEVEL (ADR-0006): investigate a production incident end-to-end — correlate an alert to the affected service and evidence, derive the root cause and hypotheses, and summarize. Provide the alert as JSON; optionally a runtime snapshot (events/deployments/commits) as JSON.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `alert` | string | yes | JSON of a domain.Alert: {id,severity,message,service,source,occurred_at} |
| `snapshot` | string | no | Optional JSON of a runtime snapshot: {events,deployments,commits} |

### `kern_flight`

- **Phase:** cross
- **Risk level:** low
- **Required:** `task`

Replay the AI flight recorder (Workflow E observability): the full recorded trail for one task — every stage, tool call, decision, approval, and outcome, in chronological order. Read-only; answers 'what did the agent do, why, and what happened?'. Records live under <root>/.kern/flight.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `task` | string | yes | Task id whose flight trail to replay |

### `kern_memory`

- **Phase:** cross
- **Risk level:** medium
- **Required:** `action`

HIGH-LEVEL (Workflow E): manage engineering memory — add a lesson, list stored lessons, or recall the most relevant lessons for a prompt.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `action` | string | yes | Action to perform: 'add', 'list', or 'recall' |
| `lesson` | string | no | For 'add': the lesson to remember |
| `prompt` | string | no | For 'recall': query to match lessons against |
| `root` | string | no | Project root (defaults to current directory) |

### `kern_agents`

- **Phase:** cross
- **Risk level:** low
- **Required:** none

HIGH-LEVEL (Workflow E): build the standard specialist team and list its roster — name, role, capabilities — plus the current task states from the agent registry. Read-only and deterministic.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |

### `kern_loop`

- **Phase:** cross
- **Risk level:** high
- **Required:** `intent`

HIGH-LEVEL (Workflow E): run the closed autonomy loop against an intent string and return the stage timeline plus the deployed / observed-healthy / learned outcome. The autonomy level (L0-L5, default L0 read-only) gates which stages run; the AI stages use the deterministic no-op step by default and are pluggable via the loop's StepFunc mechanism.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `intent` | string | yes | The intent/goal to run the loop against |
| `level` | string | no | Autonomy level L0-L5 (default L0, read-only) |

### `kern_do`

- **Phase:** cross
- **Risk level:** high
- **Required:** `intent`

HIGH-LEVEL (Workflow E): the MCP counterpart of `kern do` — run the autonomous closed loop (understand→remember→plan→code→verify→protect→observe→learn) for an intent. Unlike kern_loop's read-only no-op stages, this wires the LLM coder and planner (provider-neutral factory, default local Ollama) as the default stage handlers, grounded with project context (relevant files + impact set) and verified with the polyglot verification engine. Default level L2 (sandboxed code changes); L3 adds PR creation, L4 deploy-with-approval.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `intent` | string | yes | The change to implement, e.g. 'add a cache layer to the user service' |
| `level` | string | no | Autonomy level L2-L5 (default L2, sandboxed code changes) |

### `kern_run`

- **Phase:** cross
- **Risk level:** high
- **Required:** `intent`

HIGH-LEVEL (Workflow E): run an intent through the full task pipeline — compiles the intent, selects workflow + capabilities + agents, creates a Task, runs policy precheck, and returns the run result (task id, workflow, risk/approval, capabilities, tools, agents, next action). This is the single entry point that orchestrates the whole workflow from one call.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `intent` | string | yes | The intent/goal to run through the pipeline |

### `kern_workflow`

- **Phase:** cross
- **Risk level:** high
- **Required:** `intent`

HIGH-LEVEL (Workflow E): select and coordinate the agent team without the external caller manually sequencing it. Classifies the intent, registers the kind-specific workflow (only the specialists that apply), wires the standard team, and drives the steps (analyze → plan → [human approval gate] → code → verify → pr for code changes; kind-specific stages for incident/documentation/modernization tasks). The run parks at the human approval gate before the first execution step: the returned error carries the approval ID, resolve it via kern_approve then call kern_workflow again with the same task_id to resume.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `intent` | string | yes | The intent/goal to run through the agent team |
| `task_id` | string | no | Resume an approval-parked run for this task (omit to start a new run) |

### `kern_onboard`

- **Phase:** cross
- **Risk level:** medium
- **Required:** none

Session-start onboarding: ensure the working directory is fully wired to kern in one call. Checks whether the repo is registered (repos registry) and indexed; if not, registers it, builds/refreshes the index, and writes AGENTS.md rules if missing. Returns a status report (registered, indexed, wired, symbols/edges/files). Call this at session start in a new project instead of manually indexing or re-exploring with read/grep/glob.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |

### `kern_correlate`

- **Phase:** cross
- **Risk level:** medium
- **Required:** `alert`

HIGH-LEVEL: correlate a production alert against the runtime to produce a deep evidence chain (alert→service→deployment→commit→symbol→task/pr/agent). Deterministic — derived from runtime source and git history, not LLM.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `alert` | string | yes | JSON of a domain.Alert: {id,severity,message,service,source,occurred_at} |
| `snapshot` | string | no | Optional JSON of a runtime snapshot: {events,deployments,commits} |

### `kern_learn`

- **Phase:** cross
- **Risk level:** medium
- **Required:** none

HIGH-LEVEL: extract recurring patterns from engineering memory and surface those above a threshold. Patterns are promoted to memory (evidence-based). Deterministic — the LLM may explain but does not create patterns.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |
| `threshold` | string | no | Minimum pattern count to surface (default 3) |

### `kern_modernize`

- **Phase:** cross
- **Risk level:** medium
- **Required:** none

HIGH-LEVEL: analyze the monolith and produce a phased modernization plan (communities→bridges→churn→candidate boundaries→impact→risk→migration plan). Each extraction phase becomes an auditable Task.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root (defaults to current directory) |

### `kern_health`

- **Phase:** cross
- **Risk level:** low
- **Required:** none

Returns a real-time health and self-observability snapshot of the kern MCP server: index freshness, symbol counts, cache hit-rate, audit chain length, active tools, and in-flight operations. Enables AI agents to self-diagnose server state and avoid blind retries.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root to check index freshness for (defaults to workspace root) |

### `kern_compose`

- **Phase:** cross
- **Risk level:** high
- **Required:** `pipeline`

Executes an ordered pipeline of kern tools in a single RPC round-trip, passing intermediate outputs to downstream steps using $variable bindings. Drastically reduces agent latency and token overhead for multi-step workflows.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `pipeline` | array | yes | List of pipeline steps: [{tool: 'tool_name', args: {...}, bind: 'var_name', on_error: 'stop\|skip\|continue'}] |
| `timeout` | string | no | Per-step timeout in seconds (default 60) |

### `kern_prompt_fill`

- **Phase:** cross
- **Risk level:** low
- **Required:** `template`

Dynamically renders standardized, token-efficient agent prompts with auto-injected project layout and memory lessons. Prevents agents from wasting tokens on repetitive prompt boilerplate.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `template` | string | yes | Template name to fill, e.g. 'debug', 'code-review', 'fix-bug', 'explain', 'write-tests', 'onboard' |
| `task` | string | no | Task or error description to inject |
| `file` | string | no | Optional target file path to inject into the template |
| `slots` | object | no | Optional custom key-value slot overrides |
| `inject_memory` | string | no | Whether to auto-inject relevant lessons from project brain (default true if task provided) |
| `root` | string | no | Project root directory (defaults to current directory) |

### `kern_semantic_diff`

- **Phase:** cross
- **Risk level:** low
- **Required:** none

Computes a functional AST-level symbol diff instead of raw line noise: surfaces modified functions, changed signatures, and newly impacted callers between commits or working tree.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `from` | string | no | Starting git revision (defaults to HEAD) |
| `to` | string | no | Ending git revision or leave empty for working tree |
| `range` | string | no | Optional git range, e.g. 'HEAD~1..HEAD' |
| `root` | string | no | Project root directory (defaults to current directory) |

### `kern_context_watch`

- **Phase:** cross
- **Risk level:** low
- **Required:** `text`

Monitors and audits rolling agent context, detects bloated log/code dumps, and recommends concrete deterministic compression actions to prevent context window overflow.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `text` | string | yes | Active conversation context or candidate tool output to audit |
| `budget` | string | no | Session token budget limit (default 32000) |
| `format` | string | no | Output format: 'text' (default) or 'json' |

### `kern_agent_fingerprint`

- **Phase:** cross
- **Risk level:** low
- **Required:** none

Hashes and evaluates an agent's tool-call pattern from the audit trail to detect repetitive loops, anomalous tool polarization, or behavioral drift.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `agent_id` | string | no | Agent identifier to analyze (defaults to current agent) |
| `format` | string | no | Output format: 'text' (default) or 'json' |

### `kern_memory_ranked`

- **Phase:** cross
- **Risk level:** low
- **Required:** `prompt`

Retrieves past project lessons weighted by keyword relevance and exponential time decay (half-life), ensuring stale memories don't obscure fresh lessons.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `prompt` | string | yes | Current task prompt or error context to find lessons for |
| `k` | string | no | Maximum number of ranked lessons to return (default 5) |
| `half_life_days` | string | no | Decay half-life in days (default 7.0) |
| `root` | string | no | Project root directory (defaults to current directory) |

### `kern_agent_coordination`

- **Phase:** cross
- **Risk level:** medium
- **Required:** none

Workspace coordination protocol for multi-agent teams: register handoffs, claim/release exclusive resource locks, and query inbox tasks.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `action` | string | no | Action to perform: 'handoff', 'claim', 'release', 'inbox', 'status' |
| `agent_id` | string | no | Calling agent identifier |
| `from_agent` | string | no | Source agent ID for handoff |
| `to_agent` | string | no | Destination agent ID (or '*' for broadcast) |
| `task_id` | string | no | Task identifier |
| `resource` | string | no | Resource name to claim or release |
| `ttl_seconds` | string | no | Time-to-live in seconds for resource claims (default 300) |
| `notes` | string | no | Handoff notes or completion description |
| `payload` | object | no | Structured state payload passed in handoff |
| `root` | string | no | Project root directory (defaults to current directory) |

### `kern_agent_role_rbac`

- **Phase:** cross
- **Risk level:** high
- **Required:** none

Enforces identity-based role access control (RBAC): restricts sensitive tools (exec, delete, fix) based on agent roles (junior_dev, reviewer, auditor, admin).

| Parameter | Type | Required | Description |
|---|---|---|---|
| `action` | string | no | Action: 'evaluate' (default), 'roles', 'assign', 'check' |
| `agent_id` | string | no | Agent identifier |
| `role` | string | no | Role name (e.g. 'admin', 'architect', 'developer', 'junior_dev', 'reviewer', 'auditor') |
| `tool` | string | no | Tool name being requested to evaluate |
| `root` | string | no | Project root directory (defaults to current directory) |

### `kern_stream`

- **Phase:** cross
- **Risk level:** low
- **Required:** none

Inspects streaming status, partitions large responses into token-friendly chunks, and manages progress notification channels for long-running operations.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `action` | string | no | Action: 'status' (default), 'chunk', 'channels', 'emit' |
| `channel` | string | no | Stream channel name |
| `payload` | string | no | Payload string to partition into chunks |
| `chunk_size` | string | no | Maximum character size per chunk (default 1000) |
| `progress_token` | string | no | Client-supplied progress token for notification dispatch |
| `percent` | string | no | Progress percentage (0-100) |
| `message` | string | no | Progress message text |

### `kern_org_projects`

- **Phase:** cross
- **Risk level:** low
- **Required:** none

Enterprise org admin: list registered projects (C11). Returns {projects:[{name,root}],count}.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root for the default single project (defaults to current directory) |
| `projects` | string | no | Optional comma-separated NAME=PATH pairs registering multiple projects |

### `kern_org_agents`

- **Phase:** cross
- **Risk level:** medium
- **Required:** none

Enterprise org admin: register or list agent identities (C11). action=list returns {agents:[{id,name,type}],count}; action=register creates an agent from id/name (type defaults to 'default') and returns the created agent.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `action` | string | no | 'list' (default) or 'register' |
| `id` | string | no | Agent ID (required for register) |
| `name` | string | no | Agent display name (required for register) |
| `type` | string | no | Agent type (default 'default') |
| `root` | string | no | Project root for the default single project (defaults to current directory) |
| `projects` | string | no | Optional comma-separated NAME=PATH pairs registering multiple projects |

### `kern_org_teams`

- **Phase:** cross
- **Risk level:** medium
- **Required:** none

Enterprise org admin: manage teams that group agents and own projects (C11). action=list|show|create|remove — create takes id/name plus optional projects (team project names) and members (agent IDs); show/remove take id.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `action` | string | no | 'list' (default), 'show', 'create', or 'remove' |
| `id` | string | no | Team ID (required for show/create/remove) |
| `name` | string | no | Team display name (required for create) |
| `projects` | string | no | For create: comma-separated project names the team owns; for other tools: NAME=PATH registration pairs |
| `members` | string | no | For create: comma-separated agent IDs that are team members |
| `root` | string | no | Project root for the default single project (defaults to current directory) |

### `kern_org_memory`

- **Phase:** cross
- **Risk level:** medium
- **Required:** none

Enterprise org admin: org-level shared memory visible across all projects (C11). action=list returns {memories:[{id,content,type}],count}; action=add stores a memory from content with optional type.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `action` | string | no | 'list' (default) or 'add' |
| `content` | string | no | Memory content (required for add) |
| `type` | string | no | Optional memory type (e.g. lesson, decision, incident, constraint) |
| `root` | string | no | Project root for the default single project (defaults to current directory) |
| `projects` | string | no | Optional comma-separated NAME=PATH pairs registering multiple projects |

### `kern_org_tasks`

- **Phase:** cross
- **Risk level:** low
- **Required:** none

Enterprise org admin: aggregate task visibility (C11). Returns {projects:{name:[{id,state,intent,type}]},total}.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root for the default single project (defaults to current directory) |
| `projects` | string | no | Optional comma-separated NAME=PATH pairs registering multiple projects |

### `kern_org_search`

- **Phase:** cross
- **Risk level:** low
- **Required:** `q`

Enterprise org admin: cross-project symbol search (C11). Requires q; returns {hits:[{repo,root,symbol,score}],count}.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `q` | string | yes | Search query (required) |
| `root` | string | no | Project root for the default single project (defaults to current directory) |
| `projects` | string | no | Optional comma-separated NAME=PATH pairs registering multiple projects |

### `kern_org_audit`

- **Phase:** cross
- **Risk level:** low
- **Required:** none

Enterprise org admin: org-level audit log (C11). Returns {entries:[...],count} with AuditEntry's raw JSON field names.

| Parameter | Type | Required | Description |
|---|---|---|---|
| `root` | string | no | Project root for the default single project (defaults to current directory) |
| `projects` | string | no | Optional comma-separated NAME=PATH pairs registering multiple projects |

## Usage examples

### Example 1 — explore: understand a symbol (`kern_code_graph`)

```json
{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"kern_code_graph","arguments":{"symbol":"User.Login","root":"/path/to/project"}}}
```

### Example 2 — plan: simulate an impact (`kern_impact`)

```json
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"kern_impact","arguments":{"change":"rename Widget to Gadget","root":"/path/to/project"}}}
```

### Example 3 — edit: run a command safely (`kern_sandbox`)

```json
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"kern_sandbox","arguments":{"root":"/path/to/project","command":"make migrate","timeout":"121"}}}
```

### Example 4 — verify: check an agent's output for hallucinations (`kern_verify_output`)

```json
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"kern_verify_output","arguments":{"text":"The fix lives in internal/mcp/server.go, function Serve."}}}
```

### Example 5 — meta: route by intent (`kern_meta`)

```json
{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"kern_meta","arguments":{"request":"how does dispatch work?","root":"/path/to/project"}}}
```
