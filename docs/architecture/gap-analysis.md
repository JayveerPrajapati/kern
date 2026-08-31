# Kern 2.0 — Gap Analysis (Phase 0.4)

Classification: CURRENT / PARTIAL / MISSING. Baseline frozen at `894fb10` (2026-08-23). Reconciled against current source on **2026-08-27** — many items previously marked PARTIAL are now implemented; see the reconciled PARTIAL section below for per-item verdicts with file:line evidence.

## Fully CURRENT (no work needed)
1.2 Task state machine (17 states + explicit transitions) · 1.3 Persistence · 1.6 Human takeover · 1.7 Task snapshot
2.2 MCP→services · 2.3 CLI→services · 2.4 REST→services
3.1 Artifact contract · 3.2 Evidence contract · 3.3 Persistence · 3.5 Immutability · 3.6 Replay
4.1 Event envelope · 4.2 Event names (62 kinds)
5.1 Context taxonomy · 5.2 Lifecycle states · 5.6 Tool-output normalization · 5.7 Context snapshots
6.1 Intent compiler · 6.2 Intent taxonomy · 6.3 Workflow selector · 6.5 kern_run
7.1 Tool gateway · 7.2 Task boundary · 7.4 Safety budget
8.1 Constraint model · 8.2 Constitution YAML · 8.3 Plan validator
9.1 Agent lifecycle · 9.2 Specialist roles (7) · 9.3 Result contract · 9.6 Shared memory (authorized)
10.3 Full 20-step vertical slice
11.1 N+1 incident · 11.2 Correlation · 11.3 Root cause
14.1 Consistency model (7 sources)
15.1 Freshness metadata · 15.2 Freshness state
16.1 Flight recorder
17.1 Benchmark repos · 17.2 Benchmark task classes
19.1 REST · 19.2 Go SDK · 19.4 Py/TS SDK
20.2 Autonomy levels L0-L5
Test matrix A-J (all 10)
Build/vet/tests clean; MCP 77 tools
All 16 former MISSING items (4.3, 4.4, 5.4, 5.10-5.12, 6.9-6.10, 7.5-7.6, 8.5, 13.2, 13.4, 14.2, 15.4, 17.6) — verified CURRENT, see table below

## PARTIAL — reconciled 2026-08-27
Re-verified every item against current source on 2026-08-27. Items marked ✅ are now implemented (moved to CURRENT) with evidence. Genuinely-partial items stay PARTIAL with updated notes. (2.5, 19.3 not re-verified this pass.)

### Moved to CURRENT since baseline (verified 2026-08-27)
1.1 Task aggregate — ✅ `agent.Task` carries Project/Repository/Scope/Requester + `*_ref` IDs (MemoryRef/PolicyRef/ApprovalRef/…/PRRef/LearningRef) (internal/agent/task.go:48-78)
1.4 Dedicated Pause — ✅ `Task.Pause` (internal/agent/task.go:405), `TaskService.Pause` (internal/app/task.go:998), PriorState for Resume (internal/domain/domain.go:386)
1.5 Retry attempt/reason/prev-result — ✅ `RetryCount`/`RetryReason`/`LastResult` + `RetryWithReason` (internal/agent/task.go:83-85, 467-473)
2.1 Discrete service interfaces — ✅ 16 typed service interfaces replace the Platform facade (internal/app/services.go:32-180)
3.4 Artifact links — ✅ `ArtifactLink` derived_from/supports/contradicts + `Artifact.Links` (internal/domain/entities.go:143, 179-198)
4.5 Persisted event replay — ✅ `EnablePersistence` + `Replay` (internal/eventbus/eventbus.go:527, 564)
6.4 Policy precheck — ✅ env/scope/path + firewall identity/permission/risk gate (internal/app/task.go:474-560)
6.6 Registry purpose field — ✅ `Capability.Purpose` (internal/app/capability.go:30)
6.7 Planner static — ✅ planner selects role-based model via `agents.ModelOverride(RolePlanner)` (internal/planner/planner.go:60)
7.3 Unified task scoping — ✅ `TaskScope` unifies paths/services/envs/artifacts + `SetTaskScope` (internal/domain/boundary.go:266-296; internal/app/task.go:642)
8.4 Constitution provenance — ✅ provenance propagated, default "manual-rule" (internal/governance/constitution.go:145-165)
9.4/9.5 Routing by risk/language/historical — ✅ `routeModelWithFactors`/`RouteModelForTask` (internal/agents/model_routing.go:120-175)
9.7 Eval duration — ✅ `EvaluateAgent` records real `Duration` (internal/agents/model_routing.go:102-115)
9.8 Agent/model A/B — ✅ `CompareModels`/`CompareAgents`/`EvaluateModel` (internal/agents/model_routing.go:175-217)
10.1/10.2 Fixture — ✅ on-disk fixture `testdata/user_service_slice.json`; flagship drives the exact UserService request (internal/app/flagship_slice_test.go:15, 36-48, 98)
10.4 Artifact asserts — ✅ RiskReport/Diff/PR asserted + audit via firewall log (internal/app/artifact_coverage_test.go:21-33, 40)
10.5 Failures consolidated — ✅ single `TestSevenFailureDrill` (internal/app/failure_drill_test.go)
10.6 Per-lifecycle metrics — ✅ per-op `Recorder` (internal/metrics/metrics.go:83-289)
11.4 Fix pipeline risk step — ✅ (internal/incident/engine.go:310-314)
11.5 Learning wired to incident — ✅ resolved incident → `buildIncidentLesson` memory + `LessonRecorded` event (internal/incident/engine.go:219-246)
12.1 What-if output — ✅ arch/confidence/limitations + historical-evidence surfaced (internal/whatif/whatif.go:73-87; render.go:46-75)
12.2 Modernization ownership/deps — ✅ `Ownership`/`Dependencies` (internal/modernization/modernization.go:30-33, 206-238)
12.3 One task per phase — ✅ `ModernizePhaseTasks` materializes each extraction phase as its own task (internal/app/task.go:2783-2796)
13.1 Trace/Event link — ✅ `Correlation` captures TraceSpans/MetricEvents into the contract (internal/runtime/correlate.go:19-22; correlation_contract.go:51)
13.3 Single shared correlator — ✅ `SharedCorrelator` (internal/runtime/shared.go:14)
14.4 Explanation/resolution — ✅ `Explanation`/`Resolution` fields + `Explain()` (internal/domain/consistency.go:44-62)
16.2 Resume full reconstruction — ✅ `Resume` + `reconstructContext` (internal/app/task.go:748-790)
16.4 Run-compare — ✅ `RunCompare` (internal/app/task.go:909) + `CompareRuns` (internal/app/efficiency.go:331)
20.1/20.3/20.4/20.5 Autonomy — ✅ multi-dimension `AutonomyScore` (risk/confidence/env/perms/verification), budget env/perms, multi pause triggers, learning at autonomy level (internal/loop/autonomy_score.go:25; autonomy_triggers.go:13-60; loop.go:224, 588)

### Still PARTIAL (implementation exists but not wired into the primary run path)
5.3 Min-sufficient selector — `SelectContext` has explicit memory+freshness (internal/context/select.go:119) but is not invoked from `AnalyzeChange`/`assemble`
5.5 GC completeness — `SetDependencyDistance`/`SetTaskRelation`/`lastUsePenalty` present (internal/context/gc.go:50-59,162,184) but GC not wired into assembly
5.8 Dedup — `DedupItems` pipeline exists (internal/context/phase5.go:124) but not wired
5.9 Freshness policy — `ApplyFreshnessPolicy` exists (internal/context/phase5.go:355) but only runs inside the unwired `SelectContext`
6.8 Tool-decision-trace — recorder exists (internal/app/capability.go:339-360) but `WithTraceRecorder` has no non-test caller; in-memory only
12.4 Candidate/migration visualization — text render of candidates + phase-tasks exists (internal/app/render.go:348-363); no graph/diagram (DOT/mermaid) output
14.3 Stale-detection path — consistency engine exists (internal/consistency/engine.go:72) but is not wired into a run path
15.3 Invalidation marker — `InvalidationMarker`/`InvalidateContext` exist (internal/context/freshness.go:159) + consistency `Invalidations` but not wired
15.5 Freshness beyond GC — policy only inside unwired `SelectContext`; no wired full-policy application
16.3 Replay — metadata-level: `ReplayTask(taskID, repoVersion, model, configHash)` accepts args (internal/app/task.go:853-872) but replays the chain without re-execution
17.3-17.5 Benchmarking — `classMetrics` includes toolCalls/retries/latency/cost/… but all are deterministic token-derived estimates; `reportClassMetrics` returns nil (no gates) (evaluate/bench/main.go:666-730)

### Unchanged from prior pass (not re-verified this pass)
2.5 No MCP↔CLI↔REST equivalence test
19.3 enterprise org/team partial (now CURRENT — see Phase 19 roadmap ✅)

## MISSING — none (all 16 items implemented; verified 2026-08-23)
| ID | Item | Impl (location) |
|----|------|------|
| 4.3 | Event idempotency (dedup on Publish) | ✅ `eventbus.EnableIdempotency` (eventbus.go:186) |
| 4.4 | Event retry / dead-letter queue | ✅ `eventbus.SetRetryPolicy`+`SubscribeDeadLetter` (eventbus.go:467/483) |
| 5.4 | Context authorization (per-item) | ✅ `context.AuthorizeItems` (phase5.go:41) |
| 5.10 | Context paging | ✅ `context.PageItems` (phase5.go:81) |
| 5.11 | Context leases | ✅ `context.LeaseManager` (phase5.go:119) |
| 5.12 | Context replay engine | ✅ `context.Replay` (phase5.go:187) |
| 6.9 | Capability discovery | ✅ `app.CapabilityRegistry` (capability.go) |
| 6.10 | Tool fallback | ✅ `app.FallbackFor` (capability.go:18) |
| 7.5 | Gateway dry run | ✅ `governance.ToolGateway.DryRun` (gateway.go:124) |
| 7.6 | Explain-deny object | ✅ `domain.DenyReason` (boundary.go:206) |
| 8.5 | Rule suggestions (non-activating) | ✅ `governance.SuggestRules` (constitution.go:194) |
| 13.2 | Correlation contract (FACTUAL/INFERRED/UNKNOWN) | ✅ `runtime.Correlation.Contract` (correlation_contract.go:44) |
| 13.4 | Change fingerprint | ✅ `runtime.Fingerprint`/`ChangeFingerprint` (correlation_contract.go:128) |
| 14.2 | Conflict-result enum (NO_CONFLICT/CONFLICT/STALE/UNKNOWN) | ✅ `domain.ConflictResult` (consistency.go:17) |
| 15.4 | Memory supersession (current/historical/superseded) | ✅ `memory.Supersede` (store.go:132) |
| 17.6 | `kern efficiency <task-id>` | ✅ `kern task efficiency <id>` (cmd_task.go:51) |

## Docs (all MISSING, now being created)
docs/architecture/{current-state,target-state,gap-analysis,capability-inventory,domain-model,workflow-model,event-model,artifact-model,context-runtime,intent-engine,tool-gateway,agent-orchestration,runtime-correlation,testing-model}.md + docs/roadmap.md + docs/adr/0001-kern-control-plane.md

## Test matrix A-J — all CURRENT
A normal change · B high-risk approval · C what-if split · D incident→PR · E resume · F policy DENY · G pruning · H sufficiency · I isolation · J agent routing.

## Recommended execution order (spec: strict phase order)
Follow Phase 1→20 micro-phase order; the MISSING/priority items above are the concrete work. Docs first (this phase).