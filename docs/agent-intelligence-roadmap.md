# AI Agent Intelligence & Token Optimization Roadmap

This document tracks features designed to solve major pain points for AI agents, optimize token efficiency, reduce multi-step round-trips, and provide self-observability and safety mechanisms.

Branch: `feat/agent-intelligence-pack`

---

## 🎯 Target Overview & Status Matrix

| ID | Feature (MCP Tool / Subsystem) | Category | Priority | Status | Target Impact |
|---|---|---|---|---|---|
| **F-01** | `kern_health` | Observability | 🔴 High | ✅ Completed | Self-diagnosis; eliminates blind retries & status polls (~5% token savings) |
| **F-02** | `kern_compose` | Orchestration | 🔴 High | ✅ Completed | Executes deterministic multi-tool pipelines in 1 RPC (~30% token & latency cut) |
| **F-03** | `kern_pre_edit` | Safety & Context | 🔴 High | ✅ Completed | Pre-edit blast-radius & call-edge check before modifying code (~10% token reduction) |
| **F-04** | `kern_prompt_fill` | Context / Template | 🔴 High | ✅ Completed | Dynamic template filling with auto-injected context & memory slices (~20% token cut) |
| **F-05** | `kern_semantic_diff` | Context Intelligence | 🟡 Medium | ✅ Completed | Symbol-level functional diff (signature/callers) instead of noisy line-diffs |
| **F-06** | `kern_evidence_anchor` | Verifiability | 🟡 Medium | ✅ Completed | Zero-hallucination citation verification with deterministic SHA-256 evidence proof |
| **F-07** | `kern_context_watch` | Token Optimization | 🟡 Medium | ✅ Completed | Proactive token budget monitoring and automated budget/swap suggestions |
| **F-08** | `kern_agent_fingerprint` | Observability & Safety | 🟡 Medium | ✅ Completed | Tool-call pattern hashing to detect agent loops or context drift |
| **F-09** | `kern_stream` | Transport & Latency | 🟢 Low / Long-term | ✅ Completed | Partial streaming chunking, channel descriptors & progress token notification support |
| **F-10** | `kern_cross_repo_impact` | Multi-repo Intel | 🟢 Low / Long-term | ✅ Completed | Blast-radius and contract validation across linked repositories |
| **F-11** | `kern_policy_dsl` | Governance | 🟢 Low / Long-term | ✅ Completed | Declarative policy rules evaluated live at pre-check / check gates |
| **F-12** | `kern_agent_coordination` | Multi-agent | 🟢 Low / Long-term | ✅ Completed | Relay-socket structured hand-off and resource claim/release protocol |
| **F-13** | `kern_memory_ranked` | Context & Memory | 🟢 Low / Long-term | ✅ Completed | Access-frequency and decay-weighted memory retrieval |
| **F-14** | `kern_explain` | Architecture & NL | 🟢 Low / Long-term | ✅ Completed | Graph-backed end-to-end narrative explainer in a single call |
| **F-15** | `kern_agent_role_rbac` | Security | 🟢 Low / Long-term | ✅ Completed | Identity-based role permissions (e.g. junior agents denied exec/sandbox) |
| **F-16** | `kern_diagnostics` | Safety / Verification | 🔴 High | ✅ Completed | Multi-language draft pre-flight: Go, Java, Python, TS/JS, JSON syntax & import validation |
| **F-17** | `kern_context_budget_slice` | Token Optimization | 🔴 High | ✅ Completed | Dynamic `max_tokens` context slicing: fits symbol & call-graph slices strictly into token budget |
| **F-18** | `kern_ast_transform` | Code Intelligence | 🟡 Medium | ✅ Completed | AST-level semantic mutation & patch engine (interface stubs, safe symbol propagation) |
| **F-19** | `kern_semantic_merge` | Multi-Agent / Collaboration | 🟡 Medium | 📋 Planned | AST-aware 3-way merge arbiter resolving non-conflicting parallel agent edits |
| **F-20** | `kern_synthesize_test` | Test Intelligence | 🟢 Low / Long-term | 📋 Planned | Deterministic test harness & invariant synthesizer using call-graph types & existing fixtures |

---

## 📌 Phase 1: High-Priority Sprint Details

### 1. `kern_health` (Observability) - ✅ Done
- **Goal:** Give AI agents a single call to inspect server health, index freshness, metrics, cache hit-rate, audit chain depth, and in-flight operations.
- **Specification:**
  - MCP tool: `kern_health` (Phase: `cross`)
  - HTTP endpoint: `GET /health` on Streamable HTTP transport
  - Data sources: `internal/metrics` (Recorder snapshot), `s.sessionFor(root).CachedIndex()`, `s.audit.Len()`, `s.Inflight()`.
- **Inputs:** `root` (optional, string)
- **Outputs:** JSON object containing `status`, `index`, `tools`, `cache`, `governance`, `server`.

### 2. `kern_compose` (Pipeline Composition) - ✅ Done
- **Goal:** Allow an agent to run an ordered sequence of tools with variable interpolation (`$var`) in a single round-trip.
- **Specification:**
  - MCP tool: `kern_compose` (Phase: `cross`)
  - Internal execution via `s.runTool` without wire overhead.
  - Supports `pipeline`: `[{tool, args, bind, on_error}]`.
  - Guarded against recursive invocation (`kern_compose` calling itself).
- **Inputs:** `pipeline` (array of step objects), `timeout` (optional)
- **Outputs:** Formatted multi-step execution report with aggregated results and execution times.

### 3. `kern_pre_edit` (Predictive Safety Check) - ✅ Done
- **Goal:** Evaluate the structural blast radius before an edit is made to avoid costly mistakes and rollbacks.
- **Specification:**
  - MCP tool: `kern_pre_edit` (Phase: `plan` / `edit`)
  - Evaluates target file + line range or symbol name against the call graph, untested hot spots, and architectural boundary rules.
  - Reports direct callers, transitive blast radius, untested hotspots, and risk rating (`LOW`, `MEDIUM`, `HIGH`).
- **Inputs:** `file` (string), `lines` (optional, string e.g. "40-90"), `symbol` (optional, string), `root` (optional, string)
- **Outputs:** Predictive impact report with risk rating, caller breakdown, and actionable recommendation.

### 4. `kern_prompt_fill` (Dynamic Template Filling) - ✅ Done
- **Goal:** Generate context-enriched prompts deterministically using registered templates and indexed facts, saving tokens spent on hand-crafted boilerplates.
- **Specification:**
  - MCP tool: `kern_prompt_fill` (Phase: `cross` / `plan`)
  - Injects compact project map summaries (`code.BuildProject`), relevant lessons from project brain (`memory.Recall`), and slot constraints.
- **Inputs:** `template` (string, e.g. "debug", "code-review", "fix-bug", "explain", "write-tests", "onboard"), `task` (string), `file` (string), `slots` (optional map), `inject_memory` (bool), `root` (string)
- **Outputs:** Ready-to-use, token-optimized prompt.

---

## 📌 Phase 2: Medium-Priority Sprint Details

### 5. `kern_semantic_diff` (AST-Level Functional Diff) - ✅ Done
- **Goal:** Provide symbol-level changes (signatures, modified functions, impacted callers) rather than raw text line-diffs.
- **Specification:**
  - MCP tool: `kern_semantic_diff` (Phase: `cross`)
  - Engine: `internal/intel/semantic_diff.go`
- **Inputs:** `from` (string), `to` (string), `range` (string), `root` (string)
- **Outputs:** Markdown summary with modified symbols, signature diffs, and callers requiring updates.

### 6. `kern_evidence_anchor` (Zero-Hallucination Citation Verification) - ✅ Done
- **Goal:** Verify that cited files, lines, and symbol names exist in the actual codebase, with automatic drift correction and cryptographic SHA-256 certificate.
- **Specification:**
  - MCP tool: `kern_evidence_anchor` (Phase: `verify`)
  - Validates `file:line` or symbol existence, computes content hash and verification certificate.
- **Inputs:** `claim` (string), `file` (string), `line` (int), `symbol` (string), `root` (string)
- **Outputs:** Verification status, corrected line, SHA-256 evidence certificate, and context snippet.

### 7. `kern_context_watch` (Token Budget Surveillance) - ✅ Done
- **Goal:** Monitor token usage within conversation context or tool outputs, warning when bloat occurs and providing actionable compression suggestions.
- **Specification:**
  - MCP tool: `kern_context_watch` (Phase: `cross`)
  - Estimates tokens, identifies large code/log blocks, recommends `kern_compact_file` or `kern_optimize_log`.
- **Inputs:** `text` (string), `budget` (int), `format` (string)
- **Outputs:** Token usage assessment, bloat alerts, and recommended reduction actions.

### 8. `kern_agent_fingerprint` (Behavioral Loop & Drift Detection) - ✅ Done
- **Goal:** Analyze audit trail patterns to identify repetitive tool loops, tool polarization, or context drift.
- **Specification:**
  - MCP tool: `kern_agent_fingerprint` (Phase: `cross`)
  - Hashes tool-call sequences and evaluates repetition and balance.
- **Inputs:** `agent_id` (string), `format` (string)
- **Outputs:** Unique pattern hash, loop detection warnings, entropy rating, and tool distribution.

---

## 📌 Phase 3: Architectural, Multi-Repo & Governance Sprint Details

### 9. `kern_explain` (Architecture Narrator) - ✅ Done
- **Goal:** Synthesize an end-to-end architectural narrative for a symbol or file: purpose, callers, callees, interfaces, and testing posture in a single call.
- **Specification:**
  - MCP tool: `kern_explain` (Phase: `explore`)
  - Engine: `internal/intel/explain.go`
- **Inputs:** `target` / `symbol` / `subject` (string), `root` (string)
- **Outputs:** Markdown narrative detailing declaration, direct callers, outbound dependencies, and test coverage posture.

### 10. `kern_cross_repo_impact` (Multi-Repository Blast Radius) - ✅ Done
- **Goal:** Detect breaking changes, external call sites, and shared symbol dependencies across linked repositories.
- **Specification:**
  - MCP tool: `kern_cross_repo_impact` (Phase: `plan`)
  - Engine: `internal/intel/cross_repo.go`
- **Inputs:** `target_symbol` (string), `limit` (int)
- **Outputs:** Cross-repository impact report breaking down external callers and file locations per repository.

### 11. `kern_memory_ranked` (Decay-Weighted Memory Retrieval) - ✅ Done
- **Goal:** Retrieve project lessons weighted by keyword overlap and exponential time decay (half-life), ensuring stale memories do not obscure fresh patterns.
- **Specification:**
  - MCP tool: `kern_memory_ranked` (Phase: `cross`)
  - Engine: `internal/memory/ranked.go`
- **Inputs:** `prompt` (string), `k` (int), `half_life_days` (float), `root` (string)
- **Outputs:** Top-k lessons with breakdown of score, relevance, recency multiplier, and age.

### 12. `kern_policy_dsl` (Declarative Policy-as-Code Evaluation) - ✅ Done
- **Goal:** Evaluate git diffs, changed files, and imported libraries against declarative policy rules (banned packages, protected paths, max diff size).
- **Specification:**
  - MCP tool: `kern_policy_dsl` (Phase: `verify`)
  - Engine: `internal/intel/policy_dsl.go`
- **Inputs:** `policy` (string), `files` ([]string), `diff` (string), `imports` ([]string), `root` (string)
- **Outputs:** Policy evaluation verdict (`ALLOWED` vs `BLOCKED`), violations list, warnings, and remediation advice.

### 13. `kern_agent_coordination` (Multi-Agent Workspace Protocol) - ✅ Done
- **Goal:** Provide workspace coordination for multi-agent teams: handoffs with structured state, exclusive resource locking with TTL, and agent inboxes.
- **Specification:**
  - MCP tool: `kern_agent_coordination` (Phase: `cross`)
  - Handlers: `internal/mcp/handlers_coordination.go`
- **Inputs:** `action` ("handoff", "claim", "release", "inbox", "status"), `agent_id`, `from_agent`, `to_agent`, `task_id`, `resource`, `ttl_seconds`, `notes`, `payload`
- **Outputs:** Claim confirmations, conflict notifications, handoff registrations, and inbox queries.

### 14. `kern_agent_role_rbac` (Role-Based Tool Authorization) - ✅ Done
- **Goal:** Enforce identity-based role permissions (e.g. `junior_dev` denied `kern_exec`, `kern_safe_delete`, `kern_sandbox`).
- **Specification:**
  - MCP tool: `kern_agent_role_rbac` (Phase: `cross`)
  - Handlers: `internal/mcp/handlers_rbac.go`
- **Inputs:** `action` ("evaluate", "roles", "assign", "check"), `agent_id`, `role`, `tool`
- **Outputs:** Permission evaluation verdict (`ALLOWED` vs `DENIED`), denial reason, and role matrix.

### 15. `kern_stream` (Streaming & Progress Transport Scaffold) - ✅ Done
- **Goal:** Partition large responses into token-friendly chunks, inspect streaming capabilities, and manage progress notifications.
- **Specification:**
  - MCP tool: `kern_stream` (Phase: `cross`)
  - Handlers: `internal/mcp/handlers_stream.go`
- **Inputs:** `action` ("status", "chunk", "channels", "emit"), `channel`, `payload`, `chunk_size`, `progress_token`, `percent`, `message`
- **Outputs:** Streaming capability status, partitioned chunks descriptor, or emitted event confirmations.

---

## 📌 Phase 4: Autonomous Coding Agent Substrate (Next Frontier)

### 16. `kern_diagnostics` (Multi-Language Pre-flight Draft Diagnostics) - ✅ Done
- **Goal:** Provide sub-second syntax, bracket balance, block header, and relative import validation for draft code before writing to disk, avoiding slow, failing build cycles.
- **Specification:**
  - Integrated into `verify.CheckDraft` & `kern_check_draft` (Phase: `edit` / `verify`)
  - Multi-language coverage: Go (AST parse & undeclared symbols), Java (unresolved method calls), Python (bracket balance, header colon enforcement, relative import validation), TypeScript/JavaScript (brace/bracket balance, module resolution), JSON (syntax error line offset).
- **Inputs:** `code` (string), `lang` (optional string), `root` (optional string)
- **Outputs:** List of `DraftFinding` objects with line number, finding kind (`parse_error`, `unknown_import`, `unknown_symbol`), and actionable error messages.

### 17. `kern_context_budget_slice` (Dynamic Context-Budget Slicing) - ✅ Done
- **Goal:** Allow agents to pass strict token limits to symbol and call-graph retrieval, guaranteeing context slices fit within narrow conversation limits.
- **Specification:**
  - Extended MCP tool: `kern_context` with `max_tokens` argument (Phase: `explore`)
  - Uses `budget.FitCode` to deterministically compact and cap source slices and caller lists to the requested token ceiling.
- **Inputs:** `symbol`, `max_tokens` (optional int), `lines`, `root`
- **Outputs:** Minimal symbol definition, callers, and callees fitted strictly within `max_tokens`.

### 18. `kern_ast_transform` (AST-Level Semantic Mutation & Patch Engine) - ✅ Done
- **Goal:** Replace fragile text search-and-replace with AST-guided transformations (e.g. interface implementation scaffolding, parameter additions propagated across call-sites).
- **Specification:**
  - MCP tool: `kern_ast_transform` (Phase: `edit`)
  - Safe AST mutations without whitespace diff failures.
  - CLI command: `kern ast-transform`
  - Engine: `internal/transform`
- **Inputs:** `file`, `action` ("implement_interface", "add_field", "add_method"), `target_symbol`, `interface_name`, `field_name`, `field_type`, `apply`
- **Outputs:** Clean AST-transformed code diff or file content.

### 19. `kern_semantic_merge` (Semantic 3-Way Merge Arbiter) - ✅ Done
- **Goal:** Enable multi-agent teams to edit overlapping files on concurrent branches by performing AST-aware merges rather than failing on line-based git conflicts.
- **Specification:**
  - MCP tool: `kern_semantic_merge` (Phase: `edit`)
  - CLI command: `kern semantic-merge`
  - Engine: `internal/diff` (`SemanticMerge3Way`) & `internal/mcp/handlers_merge.go`
  - Merges non-overlapping AST declarations (struct fields, methods, functions, and imports) cleanly without conflict markers, and produces structured conflict diagnostics when overlapping AST symbols are modified incompatibly.
- **Inputs:** `file`, `base`, `local`, `remote`, `base_file`, `local_file`, `remote_file`, `apply`, `format`, `root`
- **Outputs:** `clean` boolean, `merged_code`, list of `conflicts`, and unified diff.

### 20. `kern_synthesize_test` (Deterministic Test Harness & Invariant Synthesizer) - ✅ Done
- **Goal:** Automatically synthesize comprehensive unit tests, table-driven test cases, parameter fixtures, and boundary invariants for untested functions based on AST signatures and untested hotspots.
- **Specification:**
  - MCP tool: `kern_synthesize_test` (Phase: `verify`)
  - CLI command: `kern synthesize-test`
  - Engine: `internal/synthtest` (`Synthesize`) & `internal/mcp/handlers_synthtest.go`
  - Inspects AST signatures (parameters, return types, receiver pointer/value semantics, error returns) or automatically selects uncovered hotspots (`auto_gap=true` via `intel.TestGaps`), scaffolds idiomatic table-driven test suites, asserts error and deep value equivalence, and cleanly creates or appends to `*_test.go` files without overwriting existing tests.
- **Inputs:** `target`, `file`, `code`, `auto_gap`, `apply`, `format`, `root`
- **Outputs:** `target_symbol`, `target_file`, `test_file`, `test_function`, `test_code`, `diff`, `applied`, and synthesized test `cases`.

---

## 📈 Tracking & Verification Log

- [x] Branch created: `feat/agent-intelligence-pack`
- [x] Tracking document created: `docs/agent-intelligence-roadmap.md`
- [x] Task 1: Implement `kern_health` (metrics snapshot, tool handler, HTTP endpoint, tests)
- [x] Task 2: Implement `kern_compose` (pipeline runner, variable binding, tests)
- [x] Task 3: Implement `kern_pre_edit` (pre-edit impact intelligence, tests)
- [x] Task 4: Implement `kern_prompt_fill` (template compiler with context injection, tests)
- [x] Task 5: Implement `kern_semantic_diff` (AST symbol diff & caller impact, tests)
- [x] Task 6: Implement `kern_evidence_anchor` (citation verification & SHA-256 evidence certificate, tests)
- [x] Task 7: Implement `kern_context_watch` (token budget audit & segment bloat detection, tests)
- [x] Task 8: Implement `kern_agent_fingerprint` (tool-call sequence hashing & loop detection, tests)
- [x] Task 9: Implement `kern_explain` (architecture narrative compiler, tests)
- [x] Task 10: Implement `kern_cross_repo_impact` (multi-repo blast radius, tests)
- [x] Task 11: Implement `kern_memory_ranked` (decay-weighted memory retrieval, tests)
- [x] Task 12: Implement `kern_policy_dsl` (policy-as-code evaluation engine, tests)
- [x] Task 13: Implement `kern_agent_coordination` (multi-agent handoff & resource locking, tests)
- [x] Task 14: Implement `kern_agent_role_rbac` (role-based access control matrix, tests)
- [x] Task 15: Implement `kern_stream` (chunking & progress notification transport, tests)
- [x] Task 16: Multi-language draft pre-flight diagnostics (`internal/verify/draft.go`, Python, TS/JS, JSON)
- [x] Task 17: Dynamic `max_tokens` budget capping on `kern_context` (`internal/mcp/handlers_graph.go`)
- [x] Task 18: Implement `kern_ast_transform` for deterministic AST patch generation
- [x] Task 19: Implement `kern_semantic_merge` for AST-aware multi-agent branch merges
- [x] Task 20: Implement `kern_synthesize_test` for automated unit test scaffolding

