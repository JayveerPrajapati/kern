# Tool Selection Guide (117-tool MCP catalog)

> Generated 2026-10-01. Grounded in
> [`internal/mcp/catalog/tools.go`](../internal/mcp/catalog/tools.go) (the registration table),
> [`internal/mcp/catalog/catalog.go`](../internal/mcp/catalog/catalog.go) (Tool type, phase/risk/category constants), and
> [`internal/mcp/toolpolicy.go`](../internal/mcp/toolpolicy.go) (default surface, env knobs). Every tool name below was
> cross-checked letter-by-letter against the catalog source.

kern ships a **117-tool** MCP catalog. **22 tools are advertised by default**; the rest are opt-in via
environment variables. This guide maps intents to the right tool so you rarely need the full list.

## Default surface vs full catalog

By default the server advertises the `defaultTools` surface (toolpolicy.go:203+) — evidence-based
(usage telemetry + phase coverage): the original 11-tool set had zero edit-phase tools and hid the
most-used token savers; the 22-tool set covers every phase (explore/plan/edit/verify/meta/cross) and
the tools the AGENTS.md kern-rules mandate first, at ~14k advertised tokens (well under the 24.1k
full-catalog gate):

| # | Tool | Purpose (from catalog description) |
|---|------|------------------------------------|
| 1 | `kern_meta` | NL router — classify a natural-language request and run the right tool(s) |
| 2 | `kern_explore` | One-call symbol exploration: source, callers, callees, blast radius |
| 3 | `kern_impact` | Blast radius of a change |
| 4 | `kern_review` | Token-optimised review context |
| 5 | `kern_search` | Ranked symbol search |
| 6 | `kern_context` | Minimal source slice |
| 7 | `kern_optimize` | Compress prompts / logs / outputs (action=prompt\|log\|output) |
| 8 | `kern_plan` | Implementation plan |
| 9 | `kern_verify` | Unified verification |
| 10 | `kern_run` | Orchestrate a whole task |
| 11 | `kern_authorize_context` | Authorized-context primitive (P0.1) |
| 12 | `kern_compact_file` | Symbolic file summary — the top token saver in usage telemetry |
| 13 | `kern_project_map` | Repo onboarding map |
| 14 | `kern_probe` | Task-driven context bundle (replaces 5–10 searches) |
| 15 | `kern_retrieve` | L1–L3 progressive-disclosure retrieval |
| 16 | `kern_memory` | Cross-session project memory (action: add / list / recall / ranked / remove / clear) |
| 17 | `kern_buddy` | Session onboarding digest |
| 18 | `kern_fit_context` | Fit context to a token budget |
| 19 | `kern_repair` | Deterministic compiler-error → AST fix (edit phase) |
| 20 | `kern_heal` | Self-correct failing files (edit phase) |
| 21 | `kern_commitmsg` | Deterministic conventional commit message (edit phase) |
| 22 | `kern_synthesize_test` | Table-driven test generation (verify phase) |

Opt-in knobs (all read once per server lifetime in `filteredTools`, toolpolicy.go:242+):

| Env var | Effect |
|---------|--------|
| *(unset)* | Advertise the 22 default tools above |
| `KERN_MCP_FULL=1` | Advertise the full 117-tool catalog |
| `KERN_MCP_PHASE=explore\|plan\|edit\|verify` | Filter either surface to the active phase (meta/cross tools always stay) |
| `KERN_MCP_SINGLE_TOOL=1` | Collapse to `kern_meta` alone |
| `KERN_MCP_HIGH_LEVEL_ONLY=1` | Legacy mid-size `highLevelTools` set (backward compat) |
| `KERN_MCP_CATEGORY=<family>` | Filter to one functional family; `kern_meta` always stays |
| `KERN_TOOLS=a,b,c` | Allowlist; CLI-style aliases normalize to `kern_`-prefixed names |

Nothing is lost when the advertised surface shrinks: `kern_meta`'s NL router still reaches every sub-tool
handler internally (toolpolicy.go:194-204, server.go:111).

## Phase model

Every tool carries a `Phase` tag (catalog.go:19-24). Verified counts from tools.go:

| Phase | Count | Meaning |
|-------|-------|---------|
| explore | 38 | Reading and understanding code |
| plan | 15 | Analyzing and simulating before a change |
| edit | 17 | Mutating files and executing commands |
| verify | 19 | Validating and reviewing after a change |
| cross | 27 | Phase-agnostic utilities — always advertised |
| meta | 1 | `kern_meta` itself — always advertised |
| **Total** | **117** | |

Risk levels (low/medium/high/critical, catalog.go:119-135) and 23 functional categories (catalog.go:105-116:
analyze, agent, arch, ast, context, doc, edit, evidence, exec, framework, graph, governance, incident, lock,
memory, meta, mcpbridge, optimize, org, project, review, task, verify) carry the remaining metadata.

## Decision trees by intent

### "I need to understand code"

```
What do you have?
├─ A symbol name
│   ├─ Want everything at once (source + callers + callees + blast radius)?
│   │    → kern_explore        (one call, N hops, optional why-rationale)
│   ├─ Want just the minimal source slice?
│   │    → kern_context        (definition + direct callers/callees)
│   └─ Want the whole neighborhood?
│        → kern_near           (every symbol within N hops, budget-capped)
├─ A file path
│    → kern_compact_file       (symbolic summary: functions, types, line numbers)
├─ No name yet, just a question
│    → kern_why                (why-rationale for a symbol)
│    → kern_graph              (one-call graph context)
└─ Deep index wiring of one symbol (micro-context)
     → kern_probe
```

Prefer `kern_explore` when you want a single round-trip answer; `kern_context` when you are about to edit and
need only the exact lines; `kern_compact_file` when the file — not the symbol — is the unit of interest.

### "I'm about to change code" (plan phase)

```
1. Simulate first (nothing touched):
   ├─ Hypothetical edit on the graph → kern_what_if
   └─ Named symbol's blast radius    → kern_impact
2. Plan the change:
   ├─ Implementation plan (files, deps, risks, validation; deterministic) → kern_plan
   └─ Review-lens analysis of a symbol/change → kern_analyze
3. Pre-flight the exact target:
   └─ Blast radius + untested deps + boundary risks of a file/symbol → kern_pre_edit
4. Execute (edit phase):
   ├─ Rename across the index → kern_rename
   ├─ Pattern-based AST rewrite → kern_ast_transform
   ├─ Multi-file transactional refactor (sandbox worktree) → kern_refactor_transaction
   ├─ LLM-guided code swap    → kern_swap
   └─ Run a command in an isolated runtime → kern_exec
```

Governance gates apply before/around execution: `kern_validate_proposed` (blueprint firewall on a proposed
change, tools.go:77), `kern_guard_check` (boundary crossings vs `.kern/boundaries.json`), and
`kern_authorize_context` for agent-scoped read authorization.

### "I need to search"

```
What shape is the query?
├─ A symbol name (maybe misspelled, camelCase, plural)
│    → kern_search             (ranked, forgiving, semantic=true re-ranks via Ollama)
├─ A structural pattern ("func greet", "type *User*")
│    → kern_ast_search         (AST-level, Go)
├─ Prose words that might name symbols
│    → kern_prose              (prose-word → symbol candidates)
├─ Full-text over the SQLite index (FTS5 MATCH syntax)
│    → kern_fts_search
├─ Across every repo in the multi-repo registry
│    → kern_repo_search
└─ Docs, not code (markdown/text/rst/adoc, vector search)
     → kern_doc (action=search)  (kern_doc action=fetch / action=index manage the corpus)
```

### "I need to run / verify"

```
1. Quick check: detect and run the project's build/test command → kern_validate
2. Full verification (build, unit tests, security, architecture, dependency) → kern_verify
3. Arbitrary command in isolation → kern_exec (fails closed without network
   isolation unless KERN_ALLOW_UNISOLATED=1 / KERN_ALLOW_NET=1)
4. Post-change gates:
   ├─ Diff review context for changed files → kern_review
   ├─ Untested code paths → kern_test_gaps
   └─ Stage/commit firewall → kern_validate_staged
5. Governance: resolve an approval gate → kern_approve; read the audit log → kern_audit
```

### "Orchestration"

```
├─ One natural-language request, whole task pipeline → kern_run
├─ Autonomous closed-loop task (kern do's contract) → kern_do
├─ Coordinate the agent team (analyze → plan → approval → code → verify → pr) → kern_workflow
├─ Run the silent context pipeline over an intent → kern_orchestrate
├─ Unsure which tool fits? → kern_usage_guide (categorized guide) or kern_buddy (onboarding digest)
└─ First session in a repo → kern_onboard (register + index)
```

See [agents-pipeline.md](agents-pipeline.md) for the specialist pipeline those tools drive.

### Adjacent families worth knowing

* **Incident**: `kern_correlate` (alert → service → deployment → commit → symbol), `kern_incident`
  (end-to-end investigation), `kern_taint`, `kern_mask_pii`, `kern_safe_delete`, `kern_heal`.
* **Memory**: `kern_memory` — one tool, action argument: `add` / `list` / `recall` / `ranked`
  (decay-weighted, half-life) / `remove` (index or prefix) / `clear` (deterministic keyword lessons).
* **Token hygiene** (cross phase): `kern_optimize` (action=prompt|log|output),
  `kern_fit_context`, `kern_stats`, `kern_compact_file`.

## kern_meta routes automatically

`kern_meta` is the single natural-language entry point: describe what you need and kern classifies the
request (deterministic keyword matching — no LLM, no network) and runs the right tool(s) internally
(tools.go:1315). It participates in phase filtering only when the sub-tool it routes to is itself allowed
(server.go:1142), and it reaches every sub-tool handler regardless of what the connection advertises — so
the 22-tool default surface loses no capability, only visibility.
