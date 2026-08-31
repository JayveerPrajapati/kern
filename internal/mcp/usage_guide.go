package mcp

// Guide returns a categorized guide to kern's MCP tools: performance tiers,
// recommended workflows and pitfalls. Exported so the `kern guide` CLI command
// and the opencode plugin share it verbatim.
func Guide() string {
	return `# kern MCP — Tool Usage Guide

## Phase-aware tool selection
Don't memorize 84 tools. Identify your phase (explore / plan / edit / verify),
use the phase shortlist below, and let kern_meta route within it. Set
KERN_MCP_PHASE=<phase> at server start to filter the advertised tool list to
that phase's shortlist plus the always-on meta/cross utilities; kern_meta
itself is always available and routes to any sub-tool regardless of the phase.

### explore — read / discover
kern_search, kern_explore, kern_context, kern_project_map, kern_graph,
kern_code_graph, kern_arch, kern_probe

### plan — analyze / simulate
kern_analyze, kern_plan, kern_impact, kern_what_if, kern_pack, kern_trace,
kern_usage_guide

### edit — mutate / execute
kern_run, kern_execute, kern_exec, kern_run_build, kern_rename, kern_safe_delete,
kern_commitmsg, kern_guard_check

### verify — check / validate
kern_verify, kern_validate, kern_review, kern_security, kern_changes,
kern_schema_validate, kern_diff_files

Always available regardless of phase (meta/cross): kern_meta, kern_search,
kern_context, kern_run, kern_optimize_prompt, kern_optimize_log, kern_mask_pii,
kern_doc_search, kern_memory_*, kern_stats, kern_onboard, kern_incident,
kern_workflow, kern_loop.

## Performance tiers
Most tools are index-backed and return in well under 100ms. The tiers below
only matter for the exceptions.

### Fast (index-backed, use freely)
- kern_project_map, kern_search, kern_ast_search, kern_entry_points
- kern_code_graph, kern_context, kern_walk, kern_near, kern_path, kern_why
- kern_graph (one-call names-only adjacency: callers first, confidence tags, community)
- kern_explore (one-call: verbatim source + call flow + transitive blast radius)
- kern_inherits (supertypes/subtypes hierarchy)
- kern_changes (file= form), kern_hubs, kern_bridges, kern_larges, kern_dead, kern_frameworks
- kern_probe, kern_trace, kern_test_gaps, kern_repo_search
- kern_optimize_prompt, kern_optimize_log, kern_optimize_output, kern_context_budget, kern_mask_pii, kern_swap
- kern_compact_file, kern_diff_files, kern_doc_search, kern_doc_index, kern_doc_fetch
- kern_memory_*, kern_stats, kern_semcache, kern_verify_output, kern_schema_validate
- kern_run_build (runs a build/test, returns exit status + errors only)
- kern_lock, kern_lock_status, kern_unlock (advisory workspace-scoped locks)
- kern_security (line-scoped security scan, walks source files)
- kern_commitmsg (deterministic commit message from git diff — rule-based, no LLM)
- kern_doc_fetch (explicit network opt-in: pull one public docs page into the local index; semantic=true adds Ollama embeddings)
- kern_safe_delete (callers + exported/entry-point verdict before removing code)
- kern_rename (AST-scoped structural rename preview; cheap, no filesystem writes unless apply=true)
- kern_exec (run a script in an isolated runtime, get pure stdout back)

### Moderate (first call rebuilds the index or shells out to git)
- kern_changes/kern_review/kern_guard_check with a range= (git diff)
- kern_churn, kern_cochange, kern_arch, kern_walk with depth>3 on large repos
- kern_validate (runs the project's build/test)

### Expensive (LLM, network, or full-tree work — use deliberately)
- kern_heal (spawns a local LLM to rewrite and re-validate failing files)
- kern_sandbox (snapshots the tree; destructive commands)
- kern_precache (walks the whole tree to warm caches)
- kern_pack (reads every source file; sized to max_tokens so it still fits context)

## Recommended workflows
- Understand a codebase: kern_project_map -> kern_arch -> kern_communities -> kern_hubs -> kern_entry_points
- Onboard to a new repo: kern_buddy (session digest of conventions, layout, gotchas)
- Minimal graph context in one call: kern_graph (caller-first adjacency, budgeted)
- Locate code: kern_search -> kern_context -> kern_walk (or kern_near for blast radius)
- Give the agent the full source to edit against: kern_pack (tree + instructions + contents, sized to a token budget)
- Why does X exist: kern_why -> kern_code_graph
- Class hierarchy: kern_inherits (supertypes/subtypes)
- Before proposing edits: kern_guard_check -> kern_changes -> kern_review
- Trim context: kern_optimize_prompt -> kern_context_budget -> kern_swap
- Diagnose a crash/hot path: kern_trace -> kern_probe
- Audit health: kern_test_gaps -> kern_larges -> kern_dead
- Review a diff for regressions or vulns: kern_review -> kern_security
- Refactor safely: kern_why -> kern_near -> kern_rename (preview, then apply=true)
- Compute something without context noise: kern_exec (pure stdout, isolated runtime)

## High-level Kern 2.0 workflows (ADR-0006 / Workflow C / Workflow E)
These are the flagship end-to-end workflows. Each is a single MCP call that
chains the fast primitives above into a full context packet, so you can reason
about a change against the whole system without assembling it call-by-call.

### The delivery pipeline: analyze → plan → execute → verify
- kern_analyze(change) — the kernel workflow. Renders a whole-system analysis
  of a proposed change: relevant code, architecture, dependencies, historical
  project memory, blast radius, risks, evidence and the validation that will be
  required. Call it first, before writing anything, whenever a change is
  non-trivial.
- kern_plan(change) — turn that analysis into an implementation plan: affected
  files, dependencies, risks and required validation. Deterministic over the
  analysis; no LLM. Chain after kern_analyze once the change is understood.
- kern_execute(patch) — apply a unified diff inside an isolated sandbox
  worktree, verify it builds, and return the resulting diff. Never mutates your
  live tree; safe to try a patch before committing to it.
- kern_verify(types) — gate the result with the unified verification engine
  (build, test, security, architecture, dependency). Returns a typed
  PASS/FAIL/WARN verdict and per-check summary. Run at the end to confirm the
  plan landed correctly.

Chain: kern_analyze → kern_plan → kern_execute → kern_verify. You can stop
early (analyze alone when you only need insight; analyze → plan when you only
need the roadmap) — each step is useful on its own.

### Incident investigation: incident
- **kern_incident(alert, [snapshot])** — investigate a production incident
  end-to-end. Correlate an alert JSON to the affected service and evidence,
  derive the root cause and hypotheses, and summarize. Pass the alert as JSON
  ({id,severity,message,service,source,occurred_at}) and optionally a runtime
  snapshot ({events,deployments,commits}) as JSON. When the investigation lands
  on a fix, run it through the analyze → plan → execute → verify pipeline.

### What-if analysis: what-if → impact
- **kern_what_if(change, [kind], [new_target])** — simulate a hypothetical
  change on the knowledge graph without touching it: transitively affected
  symbols, files, services and tests, a deterministic risk level and a typed
  RECOMMENDATION claim. Read-only — safe to ask "what if I remove this?".
  kind is 'remove_symbol' (default) or 'change_dependency' (with new_target).
- **kern_impact(change, [kind], [new_target])** — the same blast-radius engine
  for a change you intend to make: affected symbols/files/services/tests,
  deterministic risk and typed claims. Read-only. Use when you actually plan to
  edit, not just speculate.

Chain: kern_what_if to explore the option space first, then kern_impact on the
chosen option to size the real edit — both before you run kern_execute.

### Autonomy & teams: agents → loop
- **kern_agents** — build the standard specialist team and list its roster
  (name, role, capabilities) plus current task states from the agent registry.
  Read-only and deterministic; call to see what specialists are available and
  what's in flight.
- **kern_loop(intent, [level])** — run the closed autonomy loop against an
  intent string and get the stage timeline plus the deployed / observed-healthy
  / learned outcome. The autonomy level (L0-L5, default L0 read-only) gates
  which stages actually run. Use for an end-to-end intent, from analysis to
  deployment, without micromanaging each stage.

### Shared context: kern_context
- **kern_context(symbol)** — the minimal relevant source slice for a symbol:
  its definition source, its callers and what it calls. Use instead of reading
  an entire file. It is the low-level primitive the other graph tools build on
  and is useful inside any workflow when you need a symbol's true shape before
  you plan or edit around it.

## Pitfalls
- kern_walk/kern_near default depth is 2; depth 0 returns only the root symbol.
- kern_changes with a range needs git; a bare file= list avoids it.
- kern_repo_search only searches repos you registered (kern repos add).
- kern_doc_index only indexes .md/.txt/.rst/.adoc/.org files, skips vendor.
- Generated files (.pb.go, *_mock.go, codegen output) are demoted in
  kern_search results, not hidden — pass a narrower query if only stubs match.
- Provenance: index-backed tools append a [kern] index stamp (symbols, edges,
  packages, commit) so you know how fresh the answer is.
- kern_guard_check: add format=sarif for a SARIF 2.1.0 report (GitHub code
  scanning / Azure DevOps). threshold=N fails the call (isError) only when the
  violation count exceeds N — the CLI mirror is 'kern guard check --sarif
  --threshold N' (exit 2).
- kern_rename: v1 renames package-level Go symbols only — methods, qualified
  names and non-Go symbols are refused, not guessed. Preview with apply unset;
  apply=true commits with backups under <root>/.kern/rename-backup/ and
  rollback on failure. Strings, comments, struct-field names, composite-literal
  keys, import aliases and the package clause are never touched.
- kern_exec: the script runs in a fresh temp dir with a 10s timeout and a 16KiB
  stdout cap; network egress is NOT blocked in v1, so do not pass untrusted
  code. stderr is only surfaced on failure; a non-zero exit fails the call.
- kern_pack: the bundle is budgeted at file granularity — oversize files are
  dropped (never truncated mid-file) and counted in the STATS section; root
  instruction docs (AGENTS.md/README.md/...) are capped at 1000 tokens each and
  trimmed with a marker. For a full unbounded dump pass max_tokens=0 with
  max_output=0. format=json returns the same bundle machine-readable.
- Output sandbox: every tool response is capped at 24KiB (configurable via the
  KERN_MCP_MAX_OUTPUT env var). Oversized results are truncated with a marker
  naming a narrower tool for the detail you lost. Pass max_output=N (bytes,
  0=off) as an extra argument to any tool to override the cap per call — e.g.
  kern_exec with max_output=0 returns the full script stdout, and
  kern_project_map with a larger max_output fits a big repo.
- kern_search matches prose to symbols by name segments: camelCase humps,
  plural folding ('user services' -> UserService), accent normalization
  ('résolution'), and camelCase queries ('stateMachine') all work — no exact
  keyword needed.
- kern_fts_search requires a kern build with -tags sqlite (FTS5); the default
  build's tool errors with an explicit rebuild hint — use kern_search instead.
- kern_lock/kern_unlock are scoped to a workspace + server process: a lock
  acquired by one MCP server (or CLI process) is not releasable by another;
  the lock marker persists in <root>/.kern/locks until kern unlock runs in
  the owning process.
`
}
