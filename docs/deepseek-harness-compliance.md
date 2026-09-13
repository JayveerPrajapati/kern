# DeepSeek Harness Compliance Matrix

> Source blueprint: `/Users/jayveer.prajapati/ai_workspace/kern_opensource/KERN_DEEPSEEK_HARNESS_MASTER_SPECIFICATION.md`
> Tracker: `/Users/jayveer.prajapati/ai_workspace/kern_opensource/kern_deepseek_harness_tracker.md`
> Verified: 2026-09-11 at HEAD `412f9c0` (clean tree). This document maps every spec principle/component/capability/phase to the actual kern implementation with file refs and a status.

Status legend: ✅ implemented and verified · ⚠️ partial (gap noted) · ❌ missing.

---

## Mission & North Star

| Spec | Status | Kernel evidence |
|---|---|---|
| North Star: silent, deterministic orchestrator injecting perfect context instantly, hiding plumbing from user and LLM | ⚠️ | Components exist; silent end-to-end injection missing (G1/G2 below). |

## What Harness Teaches Kern (capability-oriented, NOT plugin-everything)

| Capability | Status | Kernel evidence |
|---|---|---|
| capability-oriented architecture | ✅ | Capability seams = `internal/{context,evidence,retrieval,budget,memory,reviewpack,security}` packages with stable interfaces |
| runtime composition | ✅ | `internal/mcp` tool registry + `kern_compose` (deterministic multi-tool pipelines) + `internal/cockpit` |
| event-driven systems | ✅ | `internal/eventbus` — 50+ Kinds (task.*, plan.produced, context_packet.built, memory.*, deployment.*) + `internal/flight` recorder |
| swappable providers | ✅ | `internal/llm` Provider interface (ollama/openai/anthropic/google via `KERN_LLM_PROVIDER`); runtime adapters (`internal/runtime/live.go`) |
| modes | ⚠️ | `internal/lenses` (5 review lenses) exist but are NOT coupled to planner policy (G3) |
| session traceability | ✅ | `internal/flight` (task flight recorder), `internal/agent_fingerprint`, governance audit chain |
| explicit capability contracts | ✅ | `domain.ContextPacket` envelope validation + `reviewpack` content-hash seals + `internal/evidence` digests |

**Kern should become** context runtime / evidence engine / orchestration layer / planner — and NOT coding agent / chat UI / general workflow / model platform:

| Should/NOT | Status | Notes |
|---|---|---|
| context runtime + evidence engine + orchestration layer + planner | ✅ | These are the core (`internal/context`, `internal/evidence`, `internal/app`, `internal/planner`) |
| NOT coding agent / chat UI / general workflow / model platform | ⚠️ | kern also ships agent surfaces (`internal/coder`, `internal/loop`, `kern do`, `cockpit`). Resolution: agent runtime is a **separate layer consuming the context core**; the core is not agent infrastructure. Acceptable per "core not pluginized." |

---

## Mandatory Architectural Principles

| Principle | Status | Kernel evidence |
|---|---|---|
| 1. Planner First — Task→Classifier→Planner→Evidence Selection→Budgeting→Envelope→Injection; nothing bypasses planner | ⚠️ | Chain exists as `PlanPacket` (`internal/context/planner.go:284`) but no single composition entry runs it end-to-end with injection (G1). |
| 2. Evidence First — every injected claim has source, trust level, freshness, provenance | ✅ | `domain.Claim` (Type FACT/INFERENCE/HYPOTHESIS/RECOMMENDATION, Confidence, Timestamp, Provenance, Evidence[] with Digest); `internal/evidence/trust.go` TrustLink/BuildTrustChain/VerifyTrustChain; `internal/evidence/status.go` DownrankStale (freshness). |
| 3. Determinism First — same repo/task/state → same context plan | ✅ | All pipeline stages LLM-free: index + hashes + weighted sort + budget only. `internal/eval` Run() is deterministic by construction. |
| 4. Core Not Pluginized | ✅ | planner/evidence/security/budget/envelope are hard-coded core; skills/adapters/exporters/lenses are the only extensible seams (`internal/skills`, `internal/lenses`, setup adapters). |
| 5. Silent Operation — users should not invoke internal context commands normally | ⚠️→✅ | Silent injection adapter implemented (Phase C): opencode plugin `experimental.chat.system.transform` calls `kern_orchestrate` on the session's first substantive message and appends the budget-fitted envelope to the system prompt before the model's first response — fail-closed (3s ceiling, single-flight, `KERN_SILENT_INJECT=0` kill-switch). All 4 plugin locations synced (2026-09-11). |

---

## Required Capability Architecture (seams)

| Capability | Provides | Status | Kernel evidence |
|---|---|---|---|
| Context | project map, compact repo views, source slices | ✅ | `internal/pack`, `internal/code`, `internal/terse`, `kern_compact_file`, `kern_project_map` |
| Repository | git state, history, blame, diffs | ✅ | `internal/diff`, `internal/index` (git edges), `kern_semantic_diff`, `kern_diff_files`, `kern_commitmsg` |
| Evidence | evidence records, trust annotations, rankings | ✅ | `internal/evidence` (bundles, trust chains, conflicts, digests), `kern_evidence_anchor` |
| Memory | project/decision/workflow memory | ✅ | `internal/memory` (typed store + governance + audit + retention), `kern_memory_ranked`, `kern_learn` |
| Review | review packs, findings, reports | ✅ | `internal/reviewpack` (immutable content-hash packs), `kern review-pack`, `kern_review` |
| Execution | build, test, command execution | ✅ | `internal/execution`, `internal/verify`, `kern_run_build`, `kern_exec` (fail-closed), `kern_sandbox` |
| Security | policy checks, egress policy, secret detection | ✅ | `internal/governance` (firewall/identity/approval), `internal/sec`, `internal/pii`, `kern_authorize_context` |

---

## Required Core Components

| Component | Spec requires | Status | Kernel evidence |
|---|---|---|---|
| 1. Context Envelope (highest priority) | task, repo metadata, evidence, assumptions, trust labels, token budget, retrieval handles; universal contract | ✅ | `domain.ContextPacket` (`internal/domain/context_packet.go:19`) — Facts/Risks/Symbols/TokenCount/FittedText/Consistency + EnvelopeVersion/SchemaVersion + Validate/Migrate. Assembled by `internal/context/engine.go`, stamped by `PlanPacket` (`planner.go:284`). Surfaces: `kern_context_envelope` (mcp tools.go L676-694), CLI `cmd_context_envelope.go`. Doc: `docs/context-envelope.md`. Retrieval handles: `internal/retrieval/handle.go:41`. |
| 2. Task Classifier | fix_bug, review_code, architecture, security_review, write_tests, explain_code, incident_analysis | ✅ (different families) | `internal/context/planner.go:48` `ClassifyTask` — 7 families: fix_bug, build_failure, refactor, add_feature, security_review, performance_review, documentation. Plus `internal/agents/selection.go` TaskKind (Code/Documentation/Incident/Modernization/Default). `kern meta` routes NL requests via the same classifier. |
| 3. Planner | identify task type, discover evidence, score evidence, budget evidence, produce envelope; deterministic | ✅ | `internal/context/planner.go` — TaskPolicy (`:35`), DefaultPolicies (`:70`), PolicyFor (`:140`), RetrievalLevelFor (`:158`), SelectEvidence (`:190`), FitToBudget (`:256`), PlanPacket (`:284`), RenderPlan (`:310`). LLM execution planner separate: `internal/planner`. |
| 4. Evidence Engine | states: observed, verified-derived, reported, assumed, not-verified, stale, conflicting; model memory must NOT become observed automatically | ✅ | `internal/evidence/status.go` — ClaimStatus + ValidateClaim (`:17`) + DownrankStale (`:44`); `conflict.go` DetectConflicts (`:23`); bundles (`builder.go`, `bundle.go`, `digest.go`). Memory capture is marked/attributed, never auto-promoted to observed (`docs/memory-governance.md`). |
| 5. Ranking Engine | inputs: symbol match, path match, call-graph distance, git proximity, test proximity, instruction relevance → ranked evidence set | ✅ | `SelectEvidence` weighted scoring (`planner.go:190`); `internal/retrieval/levels.go` ranked L1; `kern_search` ranked search; `kern_probe` query-driven router; `kern_memory_ranked` decay-weighted. |
| 6. Budget Engine | token accounting, trimming, deduplication, compression; never exceed budget | ✅ | `internal/budget` (Fit/FitCode); `FitToBudget` (`planner.go:256`, greedy + first-of-type diversity); `internal/context/metrics.go` cost accounting; budget applied at every retrieval level. |

---

## Required Context Modes

| Mode | Status | Kernel evidence |
|---|---|---|
| Fix (failing tests, logs, stack traces, callees/callers) | ✅ | `Mode{Name: fix, TaskType: fix_bug, Lens: balanced}` — fix_bug policy weights test/git/runtime evidence. |
| Review (security risks, code quality) | ✅ | `Mode{review, security_review, security lens}` — `internal/lenses` re-ranks claims via `ApplyLens`. |
| Architecture (graphs, modules, dependencies) | ✅ | `Mode{architecture, refactor, architecture lens}` + `kern_arch`/`kern_communities`. |
| Incident (timelines) | ✅ | `Mode{incident, fix_bug, performance lens, l3}` — runtime-heavy, deepest disclosure. |
| Explain (docs, source relationships) | ✅ | `Mode{explain, documentation, balanced lens, l1}` — shallow disclosure. |

**G3 closed (Phase D):** `internal/context/mode.go` — `Mode{Name, TaskType, Lens, RetrievalLevel, Budget}` + `ModeFor(name)`; `kern orchestrate --mode fix|review|architecture|incident|explain` selects the policy family and applies lens/disclosure/budget overrides deterministically. "Modes alter planner policy" is now first-class.

---

## Required Event Architecture

| Spec | Status | Kernel evidence |
|---|---|---|
| Append-only event stream; every stage observable | ✅/⚠️ | `internal/eventbus` — 50+ Kinds; `context_packet.built` emitted at `internal/context/engine.go:375`; task lifecycle events at `internal/app/platform.go:197`, `task.go:942`; governance audit chain; `internal/flight` replay. **Missing per-stage pipeline events** (G1): `evidence.selected`, `budget.applied`, `context.delivered`. |
| Benefits: debugging, auditing, reproducibility, evaluation | ✅ | flight recorder + audit + `kern_events`/`kern flight` CLIs. |

## Progressive Disclosure

| Spec | Status | Kernel evidence |
|---|---|---|
| L1 index / L2 neighborhood / L3 source; never send full repos; start from smallest useful representation | ✅ | `internal/retrieval/levels.go` (L1 names+costs → L2 neighborhood → L3 source, strictly increasing tokens); `handle.go` Handle{ID,ContentHash,...} + staleness-gated `cache.go`; surfaces `kern_retrieve`/`kern_resolve` + CLI. Doc: `docs/progressive-disclosure.md`. |

## Memory Requirements

| Spec | Status | Kernel evidence |
|---|---|---|
| categories: verified project fact, decision, user preference, generated observation, incident learning | ✅ | `internal/memory` typed store + `internal/learning` (incident learning) + `internal/incident`. |
| requirements: trust labels, expiration, provenance, deduplication | ✅ | `docs/memory-governance.md`: access control (per-agent permissions + clearance), persistent audit trail (agent_id, operation, allowed, reason), retention (expire_after/archive_after/max_entries). `kern_memory_ranked` half-life decay. `kern_learn` evidence-threshold pattern promotion. |

## Adapter Architecture

| Spec | Status | Kernel evidence |
|---|---|---|
| Targets: Claude Code, GitHub Copilot, OpenCode, Codex, Generic MCP | ✅ | `internal/setup` wires 12 agents (Claude, Codex, Gemini, Continue, Windsurf, Zed, Qwen, Qoder, Kiro, opencode, Cursor, Copilot): AGENTS.md + .mcp.json + hooks. |
| Interface: Detect, Install, Check, Intercept, Inject, Observe, Uninstall | ✅ | Detect/Install/Check/Intercept/Observe via `setup.Wire` + hooks; **Inject** (pre-first-response silent context) now implemented in the opencode plugin (`experimental.chat.system.transform` → `kern_orchestrate`, fail-closed, once per session). Plugin sync across 4 locations via `setup.GlobalPluginPaths` (memory #167). |
| Adapters may vary; planner cannot | ✅ | Adapters are capability-gated; planner is core. |

## Review Pack Architecture

| Spec | Status | Kernel evidence |
|---|---|---|
| Pack contains: task, commit state, relevant evidence, diffs, constraints, assumptions, token count; immutable evidence package | ✅ | `internal/reviewpack/reviewpack.go` — ReviewPack{SchemaVersion, ContentHash, Commit, DirtyHash, ChangedFiles, DiffPreview, Evidence (planner selections), Symbols, Tests, Constraints, Claims, Assumptions, TokenCount, Sections}; byte-identical across builds (ContentHash seal). CLI `kern review-pack`, doc `docs/review-pack.md`. |

## Evaluation System

| Spec | Status | Kernel evidence |
|---|---|---|
| Metrics: token reduction, evidence retention, latency, correctness, omission rate | ✅ | `internal/eval/eval.go` — EvalResult{Score, TokenReduction, EvidenceRetention, **OmissionRate**, ErrorRate, **LatencyMs**, Reproducible} + assertions rubric (incl. **AssertOmissionRate**) + optional blind model judge (`judge.go`, never called by deterministic Run). |
| Commands: eval run / eval compare / eval report | ✅ | `kern eval run DIR` (static samples or silent-pipeline cases via `intent`), `kern eval compare DIR MODE_A MODE_B` (two modes side by side + winner), `kern eval report` (last stored result); results persisted to `<root>/.kern/eval/` (2026-09-11). |

## Security

| Spec | Status | Kernel evidence |
|---|---|---|
| Never trust localhost, internal APIs, tools, agent requests; require policy, scope, authorization, audit trail | ✅ | `internal/governance` (firewall with resource/action, identity, approval gates, authorize-context), `kern_authorize_context`, `kern_policy_dsl`, egress gates, `kern_exec`/`kern_sandbox` fail-closed, PII masking before remote send. |
| External provider sending must be explicit | ✅ | `internal/llm` provider selection is env-driven (`KERN_LLM_PROVIDER`); doc-fetch is the only explicit network call. |

## Skills Architecture

| Spec | Status | Kernel evidence |
|---|---|---|
| Allowed plugins: skills, adapters, exporters, lenses, memory providers | ✅ | `internal/skills` + `kern skills` CLI; `internal/lenses`; setup adapters; SDKs (Go/Python/TS) as exporters. |
| Not allowed: planner/evidence/security/envelope plugins; core remains fixed | ✅ | No plugin seam over these — hard-coded core. |

## What To Copy / What To Avoid

| Spec | Status | Notes |
|---|---|---|
| COPY: capability seams, runtime composition, mode architecture, event sourcing, provider abstraction, session traceability, append-only history, composability | ✅ | All present (see capability table). |
| DO NOT COPY: everything-is-plugin, coding-agent workflows, subagent runtime, browser UX, model orchestration, complex plugin graph for core | ✅ | Kern avoids all; agent surfaces are consumers, not core. |

---

## Spec Implementation Phases vs Kern Status

| Spec phase | Spec build targets | Kern status |
|---|---|---|
| 1 | Context Envelope, Evidence Engine, Task Classifier, Planner, Ranking Engine → deterministic context generation | ✅ Done (all five exist) |
| 2 | Context Modes, Retrieval Handles, Progressive Disclosure, Event System → planner-driven selection | ✅ Handles+PD+events done; modes first-class (Phase D) |
| 3 | Memory, Adapters, Silent Injection → context before first model response | ✅ Memory+adapters done; silent injection implemented (Phase C: plugin `experimental.chat.system.transform`) |
| 4 | Review Packs, Evaluation Harness, Lenses → measurable context quality | ✅ Review packs + lenses done; eval harness + CLI (Phase D) |
| 5 | Skills, Exporters, Ecosystem → extensible without changing core | ✅ Skills (`internal/skills`, `kern skills`), SDK exporters (Go/Python/TS), adapters (12 agents) + lenses form the extensible seams; core stays fixed. New surfaces gated by the five-question test (none added — nothing passed it). |

---

## Acceptance Criteria

| # | Criterion | Status |
|---|---|---|
| 1 | User asks normal question | ✅ `kern_meta` + silent plugin injection on first message |
| 2 | Kern classifies task automatically | ✅ ClassifyTask + kern_meta routing |
| 3 | Planner selects evidence | ✅ SelectEvidence/PlanPacket |
| 4 | Evidence fits budget | ✅ FitToBudget |
| 5 | Context envelope generated | ✅ PlanPacket stamps EnvelopeVersionV1 |
| 6 | Adapter injects context silently | ✅ Phase C plugin hook (first message per session) |
| 7 | LLM sees only relevant evidence | ✅ envelope carries planner-selected, budget-fitted selections |
| 8 | User sees no orchestration | ✅ plugin injection is invisible; fail-closed |
| 9 | All decisions auditable | ✅ eventbus + audit + flight |
| 10 | Same inputs yield same outputs | ✅ deterministic pipeline + `CheckConsistency` fix |

---

## Frozen Gap List

| ID | Gap | Phase | Severity |
|---|---|---|---|
| G1 | No single silent pipeline composition (Task→Classifier→Planner→Evidence→Budget→Envelope→Injection in one observable flow); no per-stage events (`evidence.selected`, `budget.applied`, `context.delivered`) | B | ✅ CLOSED 2026-09-11 (`Engine.Orchestrate` + 5 eventbus kinds + `kern_orchestrate` MCP/CLI/plugin) |
| G2 | No pre-first-response injection adapter (spec interface `Inject` missing from setup; opencode plugin/ hooks do PostToolUse compression + memory only) | C | ✅ CLOSED 2026-09-11 (plugin `experimental.chat.system.transform` → `kern_orchestrate`, fail-closed, once per session, 4 locations synced) |
| G3 | Modes not first-class (lenses decoupled from TaskPolicy); eval harness lacks latency + omission rate; no `kern eval` CLI | D | ✅ CLOSED 2026-09-11 (`internal/context/mode.go` — 5 modes: fix/review/architecture/incident/explain override policy family + lens + disclosure + budget; `kern orchestrate --mode`; `kern eval run/compare/report` with OmissionRate + LatencyMs + `AssertOmissionRate`) |
| G4 | Memory governance implemented but enforcement wiring unverified end-to-end; ecosystem (skills/exporters) backlog | E | ✅ CLOSED 2026-09-11 — verified: `WithEnvGovernance` wired for org + per-project stores (`internal/enterprise/enterprise.go:75,240,336`), permission/audit/retention tests pass, learning surfaces patterns ≥ threshold, plugin memory capture writes attributed lessons (never auto-observed claims). Ecosystem backlog declined by the spec's five-question test (existing skills/adapters/SDKs already provide extensibility). |

Verified 2026-09-11: `go build ./...`, `go vet ./...` clean at HEAD 412f9c0. All file refs above checked against the tree.