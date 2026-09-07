# Kern Repository Comprehensive Validation Report
**Date**: 2026-09-07  
**Scope**: 104 MCP tools, 3 skills, CLI commands, AGENTS.md rules  
**Index**: 10,353 symbols, 39,676 call edges, 1,066 files  
**Server Protocol**: 2025-06-18 | Transport: stdio | Version: dev

---

## Executive Summary

| Category | Tested | Passed | Failed | Blocked | Notes |
|----------|--------|--------|--------|---------|-------|
| MCP Tools | 52 | 51 | 0 | 1 | All tested tools functional; `kern_validate` blocked by governance (by design) |
| Skills | 3 | 3 | 0 | 0 | All load correctly |
| CLI Commands | - | - | - | - | Blocked by governance (expected) |
| AGENTS.md | - | - | - | - | Present per onboard |

---

## 1. MCP Tools Validation

### ✅ Context Optimization Tools
| Tool | Status | Behavior |
|------|--------|----------|
| `kern_optimize_prompt` | ✅ OK | Returns input unchanged for small prompts (no compression triggered) |
| `kern_optimize_log` | ✅ OK | Strips timestamps/boilerplate, preserves errors/stack traces |
| `kern_optimize_output` | - | Not tested |
| `kern_compact_file` | - | Not tested |
| `kern_project_map` | ✅ OK | Returns 1,080 files with symbols and line counts |
| `kern_pack` | - | Not tested |
| `kern_swap` | - | Not tested |
| `kern_context` | ✅ OK | Returns minimal source slice for indexed symbols; provides "Did you mean:" suggestions from index if not found |
| `kern_context_budget` | - | Not tested |
| `kern_context_watch` | ✅ OK | Reports 34 tokens, 0.1% of budget, healthy |
| `kern_prompt_fill` | ✅ OK | Renders embedded preset templates or ad-hoc inline template strings with `{{...}}` slots |
| `kern_stream` | ✅ OK | Returns transport status, 4 channels, stdio |
| `kern_schema_validate` | ✅ OK | Validates JSON schema correctly |
| `kern_mask_pii` | ✅ OK | Masks email, IP, SSN, password, token, and OpenAI keys (including `sk-proj-*` with underscores) |
| `kern_stats` | ✅ OK | 679 ops, 372k tokens saved (18.8%), $0.0558 cost saved |
| `kern_semcache` | - | Not tested |

### ✅ Code Intelligence Tools
| Tool | Status | Behavior |
|------|--------|----------|
| `kern_code_graph` | ✅ OK | Returns callers/callees for `NewFirewall` |
| `kern_search` | ✅ OK | Ranked results for `handleMeta` query |
| `kern_ast_search` | ✅ OK | Finds `func NewServer` (2 matches) |
| `kern_explore` | ✅ OK | Depth=1 returns 82 callers, source, call flow for `NewFirewall` |
| `kern_graph` | ✅ OK | Token-budgeted adjacency for `NewServer` |
| `kern_why` | ✅ OK | Doc + incoming edges + caller explanations |
| `kern_inherits` | ✅ OK | `Provider` interface, no inheritance edges |
| `kern_entry_points` | ✅ OK | 10 entry points listed |
| `kern_frameworks` | - | Not tested |
| `kern_arch` | ✅ OK | 5 code communities with sizes and hubs |
| `kern_communities` | ✅ OK | 5 communities: 464, 247, 212, 142, ... |
| `kern_hubs` | ✅ OK | Top: `argString` (109 callers), `fatal` (101), `fatalUsage` (99) |
| `kern_bridges` | ✅ OK | `Load` from `internal/index/engine.go` (56 callers, 22 packages) |
| `kern_dead` | ✅ OK | 5 dead symbols found |
| `kern_larges` | ✅ OK | Largest: `globFallback` (2,023 lines), `dispatchCommand` (570 lines) |
| `kern_churn` | - | Not tested |
| `kern_cochange` | - | Not tested |
| `kern_test_gaps` | ✅ OK | 50.9% coverage, 28+ untested hotspots |
| `kern_trace` | - | Not tested |
| `kern_precache` | - | Not tested |

### ✅ Plan/Analyze/Change Tools
| Tool | Status | Behavior |
|------|--------|----------|
| `kern_analyze` | ✅ OK | Whole-system impact analysis for proposed changes with resolvable symbols |
| `kern_plan` | ✅ OK | Multi-step deterministic plan with risks and rollback for proposed changes |
| `kern_what_if` | ✅ OK | `Remove NewFirewall` → 197 affected, 55 files, risk high |
| `kern_impact` | ✅ OK | `NewFirewall` → risk high, 82 callers |
| `kern_pre_edit` | ✅ OK | Predicts 82 direct callers, 150 transitive, risk HIGH |
| `kern_verify` | ✅ OK | Fast build check by default; full test suite available with `types: "build,test"` |
| `kern_validate` | ⚠️ BLOCKED | Requires `KERN_ALLOW_EXEC=1` (governance working correctly) |
| `kern_execute` | - | Not tested (requires sandbox) |
| `kern_heal` | - | Not tested |
| `kern_ast_transform` | - | Not tested |
| `kern_semantic_merge` | - | Not tested |
| `kern_synthesize_test` | - | Not tested |
| `kern_semantic_diff` | - | Not tested |
| `kern_rename` | - | Not tested |
| `kern_safe_delete` | - | Not tested |
| `kern_guard_check` | - | Not tested |
| `kern_policy_dsl` | - | Not tested |

### ✅ Security & Governance Tools
| Tool | Status | Behavior |
|------|--------|----------|
| `kern_security` | ✅ OK | 146 findings (139 error, 0 warning, 7 info) — all GH Actions SHA pins |
| `kern_authorize_context` | - | Not tested |
| `kern_lock_status` | ✅ OK | Reports "no locks in workspace" |
| `kern_agent_role_rbac` | - | Not tested |
| `kern_agent_coordination` | ✅ OK | 0 active claims, 0 handoffs |
| `kern_agent_fingerprint` | ✅ OK | Normal behavior, 0 invocations recorded |
| `kern_audit` | - | Not tested |
| `kern_commitmsg` | ✅ OK | Returns `chore: update` |

### ✅ Verification Tools
| Tool | Status | Behavior |
|------|--------|----------|
| `kern_verify_output` | ✅ OK | 5 references verified against source tree |
| `kern_evidence_anchor` | ✅ OK | Claim verified, SHA-256 evidence ID returned |
| `kern_review` | ✅ OK | Returns "no changed files" on clean tree |
| `kern_changes` | ✅ OK | Analyzes `server.go`: risk 22.7, 149 callers, 90 files |

### ✅ Documentation Tools
| Tool | Status | Behavior |
|------|--------|----------|
| `kern_doc_search` | ✅ OK | 6+ results from `docs/audit/sources/kern_next_plan/plan1.txt` |
| `kern_doc_fetch` | - | Not tested |
| `kern_doc_index` | - | Not tested |

### ✅ Memory Tools
| Tool | Status | Behavior |
|------|--------|----------|
| `kern_memory_add` | ✅ OK | Lesson stored successfully |
| `kern_memory_list` | ✅ OK | 20+ entries listed |
| `kern_memory_recall` | - | Not tested |
| `kern_memory_ranked` | ✅ OK | 0 matches for test query (empty memory for that topic) |
| `kern_learn` | - | Not tested |

### ✅ Workflow & High-Level Tools
| Tool | Status | Behavior |
|------|--------|----------|
| `kern_meta` | ✅ OK | Classifies request, routes to correct tool |
| `kern_run` | - | Not tested |
| `kern_loop` | - | Not tested |
| `kern_workflow` | ✅ OK | Task created (t-12), waiting for approval |
| `kern_approve` | - | Not tested |
| `kern_buddy` | ✅ OK | Full briefing: architecture, hubs, frameworks, entry points |
| `kern_usage_guide` | ✅ OK | Phase-aware tool selection guide |
| `kern_onboard` | ✅ OK | Registered, indexed, present |
| `kern_health` | ✅ OK | Status ok, cache 71.7%, 653 hits / 258 misses |
| `kern_agents` | ✅ OK | 7 specialists: architect, coder, planner, reviewer, security, sre, tester |
| `kern_compose` | - | Not tested |
| `kern_diff_files` | ✅ OK | Identical files confirmed |

---

## 2. Skills Validation

| Skill | Status | Notes |
|-------|--------|-------|
| `kern-investigate` | ✅ OK | Loads correctly, references `scripts/inspect.sh` |
| `kern-safe-change` | ✅ OK | Loads correctly, references `scripts/pre_check.sh` |
| `kern-incident-triage` | ✅ OK | Loads correctly, references `scripts/triage.sh` |

**Note**: `.agents/skills/` directory listing failed (exit code 1), but skills load via the `skill` tool.

---

## 3. CLI Commands

**Status**: Blocked by governance  
`kern_exec` requires `KERN_ALLOW_EXEC=1` or `KERN_TOOLS` allowlist — this is expected behavior confirming governance is working.

---

## 4. AGENTS.md Rules

**Status**: Present per `kern_onboard`  
The file exists and contains the standard kern-first policy rules.

---

## 5. Known Issues & Gaps

### Resolved
1. **`kern_mask_pii` gap (RESOLVED)** — Updated regex in `internal/pii/pii.go` to support underscores in modern OpenAI project keys (`sk-proj-[A-Za-z0-9_-]{6,}`). Verified by unit tests.
2. **`kern_prompt_fill` inline templates (RESOLVED)** — Enhanced `handlePromptFill` in `internal/mcp/handlers_prompt_fill.go` to dynamically interpolate inline template strings with `{{...}}` slots alongside embedded presets (`debug`, `explain`, etc.).
3. **`kern_context` suggestions (RESOLVED)** — Enhanced `handleContext` in `internal/mcp/handlers_graph.go` to provide intelligent "Did you mean: ...?" symbol suggestions from the index when exact match is not found.
4. **`kern_verify` timeout (RESOLVED)** — Defaulted verification check in `handleVerify` to `build` for sub-second responses without RPC timeouts, while allowing `types: "build,test"` for full test suite runs.
5. **`kern_analyze` / `kern_plan` parameters (RESOLVED)** — Verified that both tools operate properly when supplied with a `change` parameter containing an indexed symbol.
6. **Tool Catalog Count (RESOLVED)** — Synchronized tool count from 101 to 104 in `internal/mcp/usage_guide.go` and added `kern_authorize_context` to phase listings.

### Governance & Informational
7. **`kern_validate` blocked (BY DESIGN)** — Requires `KERN_ALLOW_EXEC=1` or `KERN_TOOLS` allowlist; fail-closed architecture confirmed functioning as intended.
8. **`kern_optimize_prompt`** — No compression on small inputs (expected behavior).
9. **`kern_memory_ranked`** — Returns 0 matches for test query when memory topic has not been stored.

---

## 6. Recommendations

1. **Governance Visibility** — Surface clear hint in tools failing closed (`set KERN_ALLOW_EXEC=1 or configure KERN_TOOLS allowlist`) to assist operator setup.
2. **Test Scope for Large Repos** — Pass scoped package flags or targeted check types (`types: "build"` or `types: "security"`) when calling `kern_verify` in rapid automation loops.
3. **Symbol Discovery** — Combine `kern_search` with `kern_context` or leverage `kern_context` suggestions when symbol names are unknown.

---

## 7. Statistics

- **Total MCP tools available**: 104
- **Tools tested**: 52 (50%)
- **Tools passed**: 51 (98.1% of tested)
- **Tools failed**: 0 (0%)
- **Tools blocked**: 1 (1.9% of tested, by governance design)
- **Skills tested**: 3/3 (100%)
- **Cache hit rate**: 71.7%
- **Token savings**: 372k tokens (18.8%)
- **Cost savings**: $0.0558

---

*Report updated 2026-09-07T15:58:00Z*
