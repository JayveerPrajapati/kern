package mcp

// Guide returns a categorized guide to kern's MCP tools with performance
// tiers, recommended workflows and pitfalls. Adopted from code-graph-mcp's
// get_usage_guide tool: giving the agent the map up front stops it from
// burning calls on the wrong tool (or re-running expensive ones). Exported so
// the `kern guide` CLI command and the opencode plugin can share it verbatim.
func Guide() string {
	return `# kern MCP — Tool Usage Guide

## Performance tiers
Most tools are index-backed and return in well under 100ms. The tiers below
only matter for the exceptions.

### Fast (index-backed, use freely)
- kern_project_map, kern_search, kern_ast_search, kern_entry_points
- kern_code_graph, kern_context, kern_walk, kern_near, kern_path, kern_why
- kern_graph (one-call names-only adjacency: callers first, confidence tags, community)
- kern_inherits (supertypes/subtypes hierarchy)
- kern_changes (file= form), kern_hubs, kern_larges, kern_dead, kern_frameworks
- kern_probe, kern_trace, kern_test_gaps, kern_repo_search
- kern_optimize_prompt, kern_context_budget, kern_mask_pii, kern_swap
- kern_compact_file, kern_diff_files, kern_doc_search, kern_doc_index, kern_doc_fetch
- kern_memory_*, kern_stats, kern_verify_output, kern_schema_validate
- kern_security (line-scoped security scan, walks source files)
- kern_commitmsg (deterministic commit message from git diff — rule-based, no LLM)
- kern_doc_fetch (explicit network opt-in: pull one public docs page into the local index; semantic=true adds Ollama embeddings)
- kern_safe_delete (callers + exported/entry-point verdict before removing code)
- kern_rename (AST-scoped structural rename preview; cheap, no filesystem writes unless apply=true)
- kern_exec (run a script in an isolated runtime, get pure stdout back)

### Moderate (first call rebuilds the index or shells out to git)
- kern_changes/kern_review/kern_guard_check with a range= (git diff)
- kern_churn, kern_arch, kern_walk with depth>3 on large repos
- kern_validate (runs the project's build/test)

### Expensive (LLM, network, or full-tree work — use deliberately)
- kern_heal (spawns a local LLM to rewrite and re-validate failing files)
- kern_sandbox (snapshots the tree; destructive commands)
- kern_precache (walks the whole tree to warm caches)
- kern_pack (reads every source file; sized to max_tokens so it still fits context)

## Recommended workflows
- Understand a codebase: kern_project_map -> kern_arch -> kern_hubs -> kern_entry_points
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
`
}
