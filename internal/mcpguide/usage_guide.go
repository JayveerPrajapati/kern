package mcpguide

// Guide returns a concise guide to kern's current 32-tool MCP surface. It
// intentionally defers detailed per-tool guidance to the generated
// docs/tool-selection.md and avoids duplicating tool lists that can drift.
func Guide() string {
	return `# kern MCP — Tool Usage Guide

kern exposes a 32-tool MCP catalog and advertises the full catalog by default
(standing mandate 2026-10-23: agents use all available tools).

For the authoritative current surface, risk counts, and intent-to-tool mapping,
see the generated Tool Selection Guide in docs/tool-selection.md.

## Phase-aware selection

- Explore: kern_buddy, kern_search, kern_explore, kern_context,
  kern_compact_file, kern_project_map, kern_why, kern_path
- Plan: kern_plan, kern_plan_context, kern_impact, kern_probe
- Edit: kern_run and kern_heal (both governed and fail closed)
- Verify: kern_review, kern_verify, kern_evidence
- Cross-phase utilities: kern_meta, kern_optimize, kern_memory, kern_doc,
  kern_mask_pii, kern_stats

Set KERN_MCP_PHASE to shape the advertised list for an agent phase. kern_meta
always remains advertised and can route to the full catalog.

## Recommended workflows

- Onboard: kern_buddy -> kern_project_map
- Understand a symbol: kern_search -> kern_context -> kern_explore
- Understand a file: kern_compact_file (use tier=full only when verbatim
  source is required)
- Explain a dependency: kern_why -> kern_path
- Plan a change: kern_impact -> kern_plan_context -> kern_plan
- Review a change: kern_review -> kern_verify
- Persist a project lesson: kern_memory (action=add)
- Recover truncated output: kern_fetch_raw_anchor

## Governance and safety

kern_exec is advertised for discoverability only. Execution fails closed
unless KERN_ALLOW_EXEC or a KERN_TOOLS allowlist explicitly enables it. Treat
it as a governed capability, never as an ordinary fallback.

kern_heal and kern_run are high-risk orchestration tools. Use them when a
deterministic plan and validation path are already clear.

## Performance

Most graph and context tools are index-backed and fast. kern_buddy,
kern_project_map, kern_arch, and kern_graph may walk or summarize larger
graphs. kern_doc (action=fetch) is the only network-capable operation and
requires an explicit URL.
`
}
