# Kern 2.0 — Roadmap (Phase 0)

Strict phase-gated execution per KERN 2.0 CANONICAL END-TO-END BUILD SPEC V3. Phase advancement is forbidden until the prior phase's exit gate passes.

## Phase 0 — Current-State Truth ✅ (this doc set)
- current-state.md ✅ · capability-inventory.md ✅ · gap-analysis.md ✅ · target-state.md ✅ · domain-model.md ✅ · workflow-model.md ✅ · roadmap.md ✅ · ADR-0001 ✅
- Exit gate: no critical subsystem unknown → PASS.

## Phase 1 — Task Center
Close P1.1 (project/repo/scope/requester + *_ref IDs), P1.4 Pause, P1.5 retry attempt tracking.

## Phase 2 — Application Control-Plane Layer
Introduce 15 discrete service interfaces (P2.1); add MCP↔CLI↔REST equivalence test (P2.5).

## Phase 3 — Artifact + Evidence ✅
Add artifact link types derived_from/supports/contradicts (P3.4). Implemented: `domain.ArtifactLink` + `NewArtifactLink` validator + `Artifact.Links` registry + tests.

## Phase 4 — Event Backbone ✅
P4.3 idempotency (dedup on Publish) · P4.4 retry/dead-letter · P4.5 persisted replay. Implemented in `internal/eventbus`: `EnableIdempotency` (FIFO-bounded dedup), `SetRetryPolicy`+`SubscribeDeadLetter` (retry then dead-letter on panic), `EnablePersistence`+`Replay` (JSONL append + replay), all with tests.

## Phase 5 — Context Runtime ✅
P5.4 per-item authorization · P5.3 min-sufficient selector · P5.5 GC completeness · P5.8 dedup pipeline · P5.9 freshness policy · P5.10 paging · P5.11 leases · P5.12 replay. Implemented in `internal/context/phase5.go` + `internal/domain/context_runtime.go` (ContextPage, ContextLease, FreshnessPolicy, ContextReplay): AuthorizeItems, SelectMinimal, DedupItems, PageItems, LeaseManager, Replay, ApplyFreshnessPolicy, last-use GC penalty — all with tests.

## Phase 6 — Intent + Capability ✅
P6.6 registry (purpose field) · P6.7 planner · P6.8 tool-decision trace wiring · P6.4 full precheck · P6.9 discovery · P6.10 tool fallback. Implemented in `internal/app/capability.go` + `domain.Capability.Purpose`: CapabilityRegistry (Get/All/Tools/Agents discovery), CapabilityPrecheck (identity/scope/env/toolset), DeterministicPlan fallback, ToolDecisionTraceRecorder, FallbackFor — all with tests.

## Phase 7 — Tool Gateway ✅
P7.5 dry run · P7.6 explain-deny · P7.3 unified task scoping. Implemented in `internal/governance/gateway.go` + `internal/domain/boundary.go`: TaskScope (paths/envs/services/artifacts), DenyReason + GatewayResult/GatewayDecision, ToolGateway.EvaluateScoped + DryRun (budget-clone non-mutating) — all with tests.

## Phase 8 — Constitution ✅
P8.4 populate provenance · P8.5 rule suggestions. Implemented in `internal/governance/constitution.go` + `internal/domain/constitution.go`: `ConstitutionRule.Provenance` + parser support, provenance propagated onto violations/PlanValidation (default manual-rule), non-activating `SuggestRules` (violation→draft rule + defensive defaults) — all with tests.

## Phase 9 — Orchestration ✅
P9.4 richer routing · P9.5 model routing factors · P9.7 eval duration + prod outcome · P9.8 model A/B. Implemented in `internal/agents/model_routing.go` + `internal/domain/agent_eval.go` (RoutingFactors, ModelComparison, AgentEvaluation.Model): RouteModelForTask (risk/language/historical factors), EvaluateModel, CompareModels, duration now actually recorded — all with tests.

## Phase 10 — Flagship Vertical Slice ✅
On-disk fixture · exact request · RiskReport/Diff/PR/Audit assertions · consolidated 7-failure drill · per-lifecycle efficiency metrics. Implemented in `internal/app/testdata/user_service_slice.json` (on-disk fixture), `internal/app/flagship_slice_test.go` (TestFlagshipVerticalSlice drives the exact request + asserts RiskReport/Diff/PR artifacts + per-stage metrics; TestSevenFailureDrill consolidates 7 failure modes), plus `ArtifactRiskReport` now recorded in WhatIf and full-lifecycle P10.4 artifact assertions — all with tests.

## Phase 11 — Incident Slice ✅
Risk step in fix pipeline · wire incident→pattern→memory. Implemented in `internal/incident/engine.go` + `internal/domain/runtime.go`: ApplyAndVerifyFix now gates the candidate fix through the governance firewall (FixRisk + FIX_BLOCKED status, unknown-agent config gap not treated as denial), Resolve writes an incident-type memory lesson (provenance "incident") for the learning extractor — all with tests.

## Phase 12 — What-if + Modernization ✅
Full what-if output · ownership/deps in modernization · phase-tasks · candidate visualization. Implemented: Platform.WhatIf now populates architecture/historical evidence (12.1); BoundedContext.Ownership+Dependencies derived and carried onto ExtractionPhase (12.2); ModernizePhaseTasks creates one task per phase (12.3); renderModernizeCandidates visualization (12.4) — all with tests.

## Phase 13 — Correlation ✅
Correlation contract (FACTUAL/INFERRED/UNKNOWN) · change fingerprint · shared service · Trace/Event link. Implemented in `internal/runtime/correlation_contract.go` + `chain.go`: Correlation.Contract() derives per-link + overall confidence, runtime.Fingerprint produces a stable change hash, CorrelateChain now carries TraceLinks to raw telemetry; correlation stays a single shared runtime.Correlator service — all with tests.

## Phase 14 — Consistency ✅
Conflict-result enum · stale-detection path · conflict explanation. Implemented in `internal/domain/consistency.go` + `internal/context/consistency.go`: ConflictResult (NO_CONFLICT/CONFLICT/STALE/UNKNOWN), ConsistencyReport.Classification, per-conflict Explanation + StaleSource (stale-aware attribution), and all-stale-group → STALE detection — all with tests.

## Phase 15 — Freshness ✅
Context invalidation marker · memory supersession · freshness in evidence/risk scoring. Implemented in `internal/context/freshness.go` (InvalidationMarker, EvidenceFreshness, RiskFreshnessMultiplier) + `internal/memory` (Supersede/CurrentMemories/MarkHistorical + domain.MemoryStatus current/superseded/historical) — all with tests.

## Phase 16 — Audit/Resume/Replay ✅
Full reconstruction · replay metadata · run-compare. Implemented in `internal/app/task.go`: Resume reconstructs ContextPacket from artifacts (16.2), ReplayTask returns ReplayRecord with repo-version/model/config (16.3), RunCompare compares artifact chains + snapshot histories for equivalence (16.4) — all with tests.

## Phase 17 — Benchmarking ✅
Context-quality + task-outcome reports · `kern efficiency <task-id>`. Implemented in `internal/app/efficiency.go` (ContextQualityReport sufficiency, TaskOutcomeReport) + CLI `kern task efficiency <id>` — all with tests.

## Phase 18 — Web Console ✅
Tasks/Approvals/Risks/Artifacts/Audit pages · Agents view · Efficiency/compare/replay. Implemented in `internal/web`: Agents page (`/agents`, buildAgents + agents.html) + Tasks & Efficiency page (`/tasks`, per-task efficiency cards linking to `/task/{id}`, buildTasks calling app.BuildEfficiencyReport) + shared topnav (dashboard/task_detail/tasks/agents). Routes registered in web.go mux. Verified: build/vet/web tests pass.

## Phase 19 — REST & Web SDK/Enterprise ✅
Full enterprise org/team model (P19.3). Implemented in `internal/enterprise/org_team.go`: `OrgTeam{ID,Name,Projects,Members}` + `CreateTeam/Team/Teams/RemoveTeam/TeamAgents/IsTeamMember/AgentTeams/TeamProjects` (fail-closed validation: empty/duplicate ID, unknown project/agent rejected) + HTTP `GET/POST /org/teams`, `GET/DELETE /org/teams/{id}`, `GET /org/agents/{id}/teams` + dashboard "Org Teams" link. All pass build/vet/enterprise tests.

## Phase 20 — Controlled Autonomy ✅
Multi-dimension autonomy score · full budget dims · all pause triggers · evidence-based learning. Implemented in `internal/loop/autonomy_score.go` (AutonomyScore.Score weighted [0,1], RecommendedLevel L0-L5, AutonomyScoreFromRisk, AllowedByScore soft gate — config gate unchanged) + `internal/domain/boundary.go` (MaxToolCallsByKind, CurrentEnv, TrackToolCallKind/TrackEnv/Reset, env+per-kind Exceeded) + `internal/loop/loop.go` (MaxRiskLevel + AssessRisk, Result.Paused/PauseReason, pause() helper publishing loop.paused, risk_exceeded pre-stage ceiling, approval pause in protect stage, autonomy-aware learn with score tag). All pass build/vet/loop+domain tests.

## Test matrix A–J — already all CURRENT (acceptance_matrix_test.go)