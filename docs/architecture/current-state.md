# Kern 2.0 — Current-State (Phase 0.1)

Baseline frozen at `894fb10` (2026-08-23). Go 1.23, stdlib-only default build (tree-sitter + sqlite behind build tags).

## Repository topology

- `cmd/kern` — CLI, ~50+ subcommands in a main.go split across cmd_*.go (run/task/approve/audit/incident/modernize/what-if/memory/index/exec/setup/doc/graph/security/context/artifacts/agent/meta/optimize/mcp).
- `cmd/kern-mcp` — MCP server entry (~68 lines, delegates to internal/mcp; graceful shutdown implemented).
- `cmd/kern-server` — web/REST server (`-root`, `-addr`), single-project + enterprise modes, graceful shutdown.
- `internal/` — 74 packages. Core control-plane surface:
  - `domain` — Task/Artifact/Evidence/CompiledIntent/RunResult/Capability/ToolDecisionTrace/Consistency/Freshness/ContextClass/ContextState/TaskBoundary/SafetyBudget/Constitution.
  - `app` — control-plane services: `Platform` facade + `TaskService` (Run, Analyze, Plan, Impact, Risk, WhatIf, Verify, Execute, Approve, Deploy, Observe, Modernize, Retry, Cancel, Resume, HumanTakeover), `CompileIntent` (Intent Compiler), `ArtifactStore` (persistence + replay + compare).
  - `index` — multi-language indexing pipeline (17 langs stdlib, 13 tree-sitter opt-in).
  - `intelligence` — call graphs, blast radius, impact, hubs/bridges, communities, dead code, flows, paths, probe, trace, wiki, guard/boundaries, coverage, churn, cochange, rename, delete.
  - `context` — context engine, GC, snapshots, normalizer, rules/crossesBoundary, consistency check, git/freshness.
  - `eventbus` — async pub/sub, 62 Kind constants, in-memory History.
  - `governance` — firewall, gateway (identity→task→resource→action→permission→risk→policy→approval→budget), constitution (.kern/constitution.yaml + ValidatePlan), approval store, audit log, identity, risk, exec gating.
  - `agent` / `agents` — agent runtime (Task, workflow, session, registry, provider, handoff, snapshot) + specialist roles (Planner/Architect/Coder/Reviewer/Security/Tester/SRE), dynamic selection, model routing, agent eval.
  - `loop` — 9-stage loop (intent→remember→plan→code→verify→protect→deploy→observe→learn), autonomy L0-L5, budget pause.
  - `verification` — build/test/security/architecture validation engine.
  - `sandbox` — snapshot isolation (100MiB cap, SkipDirs), restore, heal.
  - `execution` — worktree execution, apply/verify.
  - `incident` — incident engine, correlation, store.
  - `runtime` — runtime sources (prometheus/otel/k8s live + local), correlate chain, store, events.
  - `deployment` — Deployer (Noop/Shell), approval-gated.
  - `modernization` — modernization analysis (communities/bridges/churn).
  - `twin` — digital-twin API/data/edges/extractors/merge.
  - `evidence` — evidence builder/digest/factories.
  - `memory` — engineering memory store + AuthorizedRecall.
  - `learning` — pattern extraction from memory.
  - `flight` — flight recorder for audit.
  - `mcp` — MCP server, 77 kern_* tools.
  - `sdk` — Go SDK client.
  - `web` — web console (dashboard.html, task_detail.html) + REST API routes.
  - `enterprise` — org/team memory isolation + LRU project cache.
  - `llm` — provider abstraction (ollama/openai/anthropic/google).
  - `whatif`, `metrics`, `stats`, `evaluate/bench`, `cicd`, `prprovider`, `planner`, `learning`.
- `sdk/python`, `sdk/typescript` — Python + TypeScript SDKs.
- `docs/` — only `examples/kern-gate.yml` + this new architecture set.
- `AGENTS.md`, `.opencode/plugins/kern.ts`, `.mcp.json` — agent wiring.

## Owners / responsibilities

All 20 spec-required packages present and owning their spec responsibilities. No critical subsystem is unknown.

## Baseline (Phase 0.3)

- `go build ./...` ✓
- `go vet ./...` ✓
- `go test ./...` ✓ (90 packages, 0 failures)
- `go test -short ./...` ✓ (~60s)
- `go test -race -short ./...` ✓ (90 packages, ~2.5 min)
- Build-tag variants (`-tags treesitter`, `-tags sqlite`, `GOOS=windows`) verified pass.
- MCP catalog 77/77 tools; web server + graceful shutdown verified.
- Test matrix A–J all present (acceptance_matrix_test.go).

### Baseline fixes (2026-08-25)

Two defects found while freezing the baseline and fixed:

1. **Cross-process store race (data loss).** `TaskStore`/`ArtifactStore`/`SnapshotStore`
   serialized writers only per-process (`cache.PathLock` is an in-process mutex
   registry). Separate kern processes on the same project (kern-mcp server,
   kern-server, CLI — or parallel `go test ./...` binaries) interleaved their
   load→modify→save on the shared JSON file and lost each other's updates;
   process-local task-ID counters also collided (`t-1` from two processes, and
   Save replaced by ID so one task vanished). Fixed by adding a cross-process
   file lock (`internal/cache/filelock.go`: blocking flock on Unix, LockFileEx
   on Windows, on a persistent `<path>.lock` sidecar) around every store write,
   and by making the store assign task IDs (`t-<max+1>` from persisted content
   under the lock) when a registry with a store submits an empty-ID task.
   Regression tests: `internal/agent/taskstore_crossprocess_test.go` (spawns
   real child processes; fails without the lock) and
   `taskstore_concurrency_test.go` (in-process + same-ID variants).

2. **Test-suite speed + cross-binary interference.** Every test run appended to
   the same per-root JSON stores in the user's `~/.cache/kern`, which grew to
   77MB/72MB; every store save was a full rewrite of that file, and the app
   suite's `TestPhaseTaskIDIsSetByModernizePhaseTasks` (329 phase tasks × 4
   saves) took 130s (9+ min under -race). Fixed by isolating the test cache:
   store-touching packages (app/agent/mcp/web/loop/enterprise/cicd/incident)
   redirect `XDG_CACHE_HOME` to a per-run temp dir in `TestMain` (skipped when
   already set so child test processes keep the parent's store). Result:
   `internal/app` suite 168s→87s, the modernize test 130s→14s, full `-short`
   suite ~60s. Also fixed `TestUserServiceFixtureBuildable`, which was
   overwriting the committed fixture binary by building in-place.

### Phase 1 (Task Center, 2026-08-25)

Task is the authoritative unit of engineering work, independent of any
MCP/CLI-specific state (see docs/phase-reports/phase-01.md):

- All 21 aggregate refs (intent, project, repository, scope, requester,
  workflow, agent_refs, context/memory/impact/risk/policy/approval/plan/
  verification/pr/deployment/outcome/learning refs, artifact refs) on
  `agent.Task`, JSON-persisted through TaskStore.
- `Task.Validate()` enforces aggregate invariants (required fields, valid
  canonical state, whitespace-only ref rejection).
- `Transition` + every compound mutator (start/complete/fail/cancel/timeout/
  block/pause/resume/retry/rollback/human-takeover/return-to-agent) are now
  concurrency-safe via a pointer `stateMu` (`-race` clean with concurrent
  cancel + workflow stepping the same task).
- `Task.CurrentStage` records the in-progress workflow step and persists with
  the task (spec 1.3 "current stage").
- 17-state machine, cancel/pause/resume, retry trail (attempt/reason/prior
  result), human takeover (agent→human, human→agent), and the spec-shaped
  rich snapshot (goal/state/decisions/constraints/files/tests/risks/next_action)
  are all covered by tests incl. duplicate/concurrent transition, corrupt
  record, missing artifact, legacy-JSON compatibility, and restart/resume.

### Phase 2 (Application control-plane layer, 2026-08-25)

Business orchestration moved out of interfaces: 15 service contracts live in
`internal/app/services.go` (Task, Context, Analysis, Impact, WhatIf, Memory,
Risk, Policy, Agent, Execution, Verification, Incident, Modernization,
Deployment, Audit + Loop) as interfaces satisfied by `*TaskService` /
`*Platform` with compile-time assertions. Every interface is a thin adapter:

- MCP: all 9 named tools route through TaskService; zero inline engines.
- CLI: `kern incident` refactored from inline `incident.NewEngine` +
  IngestAlert/Correlate/RootCause to the shared
  `TaskService.InvestigateIncident` (P2 exit-gate violation fixed); other
  commands already delegated.
- REST: `handleV1Context` (was `a.ctx.AnalyzeChange` inline) now routes
  through `Platform.Analyze`; `handleV1IncidentInvestigate` (was `a.inc`
  inline engine) now routes through `TaskService.InvestigateIncident`. The
  dead `App.ctx` / `App.inc` engine fields and their startup wiring were
  removed.
- Cross-interface consistency tests (P2.5): MCP kern_analyze + REST /v1/analyze
  + CLI service path share the authoritative task store;
  `TestCrossInterfaceIncidentConsistency` added for the incident workflow.
  Phase report: docs/phase-reports/phase-02.md.

### Phase 3 (Artifact + evidence, 2026-08-25)

All meaningful workflow outputs are persistent and auditable:

- Artifact contract: all 18 spec report types as typed domain structs +
  13-field `domain.Artifact` envelope (id/type/task_id/created_by/created_at/
  version/status/scope/provenance/digest/parent_artifact_id/related_entities/
  URI) with deterministic id + SHA-256 digest (`NewArtifact`).
- Evidence contract: 4 claim types (FACT/INFERENCE/HYPOTHESIS/RECOMMENDATION)
  + 7 sources (graph/test/build/git/runtime/memory/policy) on `domain.Claim`/
  `domain.Evidence`. Closed the gap where `agent.Task.Evidence` was never
  populated — `TaskService.Analyze` now attaches `pkt.Facts` to `t.Evidence`
  so the Task carries + persists its evidence trail
  (`TestAnalyzeAttachesEvidenceClaims`).
- ArtifactStore: cross-process-locked JSON persistence (restart-safe), all 5
  link kinds (parent/child/derived_from/supports/contradicts), finalized
  immutability (Save rejects final overwrite; NewVersion supersedes; drafts
  replaceable), and Replay/Compare (chain reconstruction by ParentArtifactID).
- Every workflow stage records a typed artifact (analyze/analysis/impact/risk/
  plan/verify/test/security/architecture/diff/pr/deploy/observe/audit/
  incident/root-cause/modernization) with parent links; the flagship slice test
  asserts all 12 required kinds. Exit gate (no workflow output exists only
  in-memory) MET. Phase report: docs/phase-reports/phase-03.md.

### Phase 4 (Event backbone, 2026-08-25)

Asynchronous communication standardized on the in-process event bus
(`internal/eventbus`):

- Envelope: event_id/event_type/event_version/occurred_at/source/project_id/
  repository_id/task_id/agent_id/entity_refs/payload/provenance all on
  `eventbus.Event`.
- Taxonomy: 13 spec domains covered with typed kinds (task.* 11, agent.* 7,
  policy.*, approval.*, verification.*, pr.*, deployment.*, runtime.*,
  incident.*, memory.*, risk.*, architecture.*, security.*);
  `TestEventKindsAreDistinct`.
- Idempotency (4.3): bus dedups on non-empty IDs (bounded FIFO set). FIXED
  gap: `TaskService.publish` now sets a content-addressed ID
  (`stableEventID` = SHA-256 over kind|subject|canonical-payload), so the
  primary producer is idempotent — identical re-publishes are no-ops, distinct
  state changes flow (`TestPublishIdempotentAtAppLayer`). Other publishers
  (web/loop/agent) still use auto IDs; formats are disjoint (`e-<hex>` vs
  `e-<ts>-<counter>`).
- Retry/dead-letter (4.4): `SetRetryPolicy` + `SubscribeDeadLetter`;
  panicking handlers are recovered, retried with backoff, then dead-lettered.
- Replay (4.5): `EnablePersistence` (JSONL) + `Replay` (idempotent re-delivery).
- Exit gate (task lifecycle event-observable + retry-safe) MET: every
  lifecycle op publishes a typed event (created/started/updated/state_changed/
  completed/failed/blocked/cancelled/approval_requested/approved). Phase
  report: docs/phase-reports/phase-04.md.

### Phase 5 (Context runtime, 2026-08-25)

Context optimization is an intelligent context lifecycle
(`internal/context`):

- Taxonomy: all 15 spec classes (USER_INTENT … ARTIFACT) + 5 lifecycle states
  (ACTIVE/WARM/COLD/ARCHIVED/DROPPED) on `domain.ContextItem`.
- Minimum-sufficient selection: `SelectContext` enforces permissions first,
  then the documented selection order, ranks by relevance, reduces to the
  minimum sufficient subset keeping constraints + evidence (protected).
- Authorization: `AuthorizeItemsScoped` checks all 5 dimensions (agent via
  firewall, repository, task, tenant, security class) fail-closed.
- GC: all 7 scoring factors → KEEP/COMPRESS/DEMOTE/ARCHIVE/DROP. Paging
  (`PageItems`), leases (`LeaseManager`), dedup (`DedupItems`/canonical fact +
  evidence refs), freshness policy + invalidation, snapshots, and replay.
- **FIXED gap**: `NormalizeToolResult` (P5.6) was dead code — wired into the
  workflow engine (`agent/workflow.go`): step outputs over 4 KiB are normalized
  in step history while raw stays on task.Output + artifact chain
  (`TestLargeStepOutputNormalized`).
- **Added exit-gate closure test** `TestPhase5ExitGate`: one task through the
  full pipeline asserting all six exit-gate properties (less irrelevant
  context, less duplicate output, constraints retained, evidence retained, no
  unauthorized context, no correctness regression).
  Phase report: docs/phase-reports/phase-05.md.

### Phase 6 (Intent engine + kern_run, 2026-08-25)

One high-level entry point: `kern run` / kern_run → TaskService.Run returns
task + workflow + caps + tools + agents + risk + approval + next action:

- Intent compiler (`CompileIntent`) produces the spec JSON (intent_type/
  objective/target/scope/environment/desired_outcome); all 10 intent types;
  `SelectWorkflow` maps to the 5 user workflows (A-E) preserving the approval
  gate.
- Policy precheck unifies identity/scope/permission/environment/risk into one
  ALLOW/DENY result. FIXED gap: the default "kern" operator was never
  registered on the Platform firewall, so every kern_run precheck denied —
  registered it (repository/source read+write, repository scan/audit, security
  scan, tests/config/docs write, context read/write; DEPLOY still separately
  gated). Live-verified all 10 intent types clear
  (`TestKernRunOperatorClearsPrecheck`).
- Capability registry (9-field Capability), intent-scoped planner
  (`DefaultCapabilities` + `DeterministicPlan`), and semantic discovery exist.
- FIXED gap (P6.8): `ToolDecisionTraceRecorder` was dead code — wired into
  `TaskService.RunWorkflow` (every step records tool/why/expected/actual/
  latency; `WithTraceRecorder`).
- FIXED gap (P6.10): `FallbackFor` was dead code — wired into the MCP tool
  gate: KERN_TOOLS-blocked tools reroute to a policy-approved alternative when
  allowed, fail closed otherwise (kern_what_if→kern_impact etc.).
  Phase report: docs/phase-reports/phase-06.md.

### Phase 7 (Tool gateway + task boundary, 2026-08-25)

Every governed tool use is task-scoped:

- `ToolGateway` (governance/gateway.go): request → identity (firewall) → task
  boundary → resource → action → permission → risk → policy → approval →
  tool; `EvaluateScopedFull` gates env→service→artifact→path→firewall→budget
  (one task policy across tools/artifacts/runtime); `DryRun` previews on a
  budget clone; explain-deny (`GatewayResult.Deny`) carries decision/reason/
  policy/risk/required approval/safe alternative; budget exceed → PAUSE.
- `TaskBoundary` (allowed/denied paths, spec example tested) +
  `SafetyBudget` (files/services/tools/tokens/cost/runtime/risk/env).
- **FIXED gap**: the boundary was enforced NOWHERE in the live execution path
  (gateway + authorizeResource had no production callers). Added
  `TaskScope.ValidatePatch(patch)` — extracts every path a diff touches and
  requires each to pass the task boundary — and wired it into
  `TaskService.Execute` + `ExecuteAndVerify` (out-of-scope patches fail the
  task before any execution). `TestUnifiedTaskPolicyScope` proves one scope
  gates context/memory/artifact/runtime uniformly.
- Tests: `TestTaskScopeValidatePatch`, `TestExecuteEnforcesTaskBoundary`,
  plus the full gateway/dry-run/explain-deny suite. Exit gate (no controlled
  action bypasses task-scoped governance) MET.
  Phase report: docs/phase-reports/phase-07.md.

### Phase 8 (Engineering constitution + plan validation, 2026-08-25)

Engineering rules are explicit and executable:

- Constraint model: MUST/MUST_NOT/SHOULD/SHOULD_NOT on
  `domain.ConstitutionRule` (typed fields: cannot_depend_on, never_log,
  required, approval, require_tests) + provenance (adr/incident/policy/
  team-rule/manual-rule).
- `.kern/constitution.yaml`: `LoadConstitution` (stdlib-only line parser,
  no YAML dep; missing file = empty constitution, not an error). Spec's exact
  example tested.
- `ValidatePlan`: plan → architecture → security → constraints → impact →
  risk → policy; MUST/MUST_NOT violations are blocking
  (`PlanViolation.IsBlocking`), SHOULD/SHOULD_NOT are warnings.
- **FIXED gap (exit gate)**: `ValidatePlan` had zero production callers —
  mandatory rules could not block a plan. Wired into `TaskService.Plan`
  after `assemblePlan`: a blocking violation fails the task before completion
  ("plan blocked by constitution: …") so the plan cannot reach execution;
  missing constitution = pass. Tests: `TestPlanBlockedByConstitution`,
  `TestPlanPassesWithoutConstitution`.
- `SuggestRules` (P8.5): non-activating draft rules from violations +
  defensive defaults (never writes the constitution).
  Phase report: docs/phase-reports/phase-08.md.

### Phase 9 (Agent orchestration, 2026-08-25)

Kern owns agent workflow execution:

- Specialist team: 7 roles (planner/architect/coder/reviewer/security/tester/
  sre) with capabilities; `StandardTeam` builds the roster; result contract
  (`AgentResult`) matches the spec JSON; model routing (`RouteModelForTask`,
  KERN_MODEL_* overrides); agent evaluation + model A/B (`CompareModels`).
- **Exit gate closed**: `TaskService.RunWorkflowDefault(intent)` selects the
  kind workflow (ClassifyTask → SelectWorkflow), wires the standard team, and
  drives the steps with Kern's own default handler (real deterministic
  analyze/plan; data-backed stage outcomes). CLI `kern workflow` + MCP
  `kern_workflow` are the production entries.
- **Fixed 3 latent bugs**: (1) `task.WorkflowID` was never set — kind
  workflows never actually ran; (2) kind workflows violated the strict state
  machine (opening at "plan") — engine now `driveToState`s along the canonical
  lifecycle; (3) the approval gate + run state were process-local — `ResumeStep`
  + `ApprovalRefs` persist on the task, the engine consults/writes the
  persistent approval store (`kern approve`), and `RunWorkflowResume` recovers
  a parked task from the TaskStore. Cross-process CLI cycle verified live.
  Phase report: docs/phase-reports/phase-09.md.

### Phase 10 (Flagship safe-change vertical slice, 2026-08-25)

Proven control plane: the flagship request "Add tenant-aware caching to
UserService" passes through the ENTIRE safe-change workflow repeatedly:

- Fixture repository (10.1): `internal/app/testdata/safechange/` — real Go
  source (UserService, UserRepository, CacheService, TenantContext), tests,
  and `.kern/constitution.yaml` architecture rule (cache must not depend on
  the repository; no ORM in the service layer).
- **Fixed a real robustness gap (10.2)**: `Platform.Analyze`/`Risk` failed on
  the flagship request because `ExtractSymbols` returns "tenant" first.
  New `analyzeChangeResolvable` tries the change text + every candidate until
  one resolves in the graph.
- `TestSafeChangeVerticalSlice` (10.3/10.4/exit gate): full chain
  intent → Task → context → memory → evidence → impact → risk → policy →
  plan (constitution) → approval → sandbox → code → verification → artifacts
  → PR → deploy → observe → audit, all 10 required artifacts + parent-chain
  integrity, run TWICE with no governance bypass.
- 10.5 failure drills + 10.6 per-stage efficiency metrics verified present.
  Phase report: docs/phase-reports/phase-10.md.

### Phase 11 (Incident vertical slice, 2026-08-25)

Proven production-to-engineering closed loop — a controlled incident becomes a
verified remediation PR:

- `TaskService.RemediateIncident(alert, apply, branch, approver)` — NEW
  app-layer entry closing the exit gate: IngestAlert → Correlate (alert →
  service → deployment → commit → symbol) → RootCause (hypotheses with
  confidence/score/evidence, promoted only on INFERENCE/FACT + non-git
  evidence) → human approval gate → sandboxed fix → build verification →
  remediation PR (status PR_CREATED, diff/verification/PR artifacts recorded,
  incident.resolved published). The incident engine's ApplyAndVerifyFix +
  CreateFixPR had ZERO production callers before.
- `TestIncidentVerticalSlice` drives the whole loop on an N+1 regression
  fixture (11.1-11.5) and asserts the full chain + artifacts.
  Phase report: docs/phase-reports/phase-11.md.

### Phase 12 (What-if + modernization, 2026-08-25)

What-if and modernization are Task/Artifact/Evidence aware:

- **Fixed G1**: `Modernize()` now materializes each extraction phase as its own
  Task (`ModernizePhaseTasks` had zero production callers) — Task Group →
  Tasks → Plan → Risk → Validation, linked by ParentID + architecture artifact.
- **Fixed G2**: `TaskService.WhatIf` now appends the simulation's typed Claims
  to the task's Evidence (previously artifacts + text only).
- `TestWhatIfIsEvidenceAware` (impact/risk artifacts + typed evidence claims)
  and `TestModernizeMaterializesPhaseTasks` (all phases materialized against
  the real kern index) lock the exit gate.
  Phase report: docs/phase-reports/phase-12.md.

### Phase 13 (Runtime↔code↔agent correlation, 2026-08-25)

Runtime traceability locked at the app layer:

- Canonical chain (13.1): alert → service → deployment → commit → PR → task →
  agent → symbol + trace links; contract (13.2) FACTUAL/INFERRED/UNKNOWN with
  provenance; shared correlator (13.3) across correlate/investigate/remediate;
  change fingerprint (13.4) with all 13 dimensions.
- **Exit-gate test added**: `TestControlledDeploymentTraceableToTaskPR`
  (controlled deployment → PR #123 / TASK-9 / agent-b / CacheService.Get /
  trace link) + `TestSharedCorrelatorConsistentAcrossLanes`.
  Phase report: docs/phase-reports/phase-13.md.

### Phase 14 (Cross-engine consistency, 2026-08-25)

Conflicting knowledge is never silently collapsed into certainty:

- **Fixed the unwired consistency engine**: `context.CheckConsistency` had zero
  production callers. New `context.ApplyConsistency(pkt)` runs the check over
  every packet's claims, downgrades conflicting claims' confidence, and
  attaches the report (`ContextPacket.Consistency`, nil when clean);
  `Engine.assemble` calls it for every change.
- 14.1 (7 sources), 14.2 (NO_CONFLICT/CONFLICT/STALE/UNKNOWN + downgrade),
  14.3 (stale attribution), 14.4 (explanation + which source is newer) all
  present.
- Exit gate locked: `TestApplyConsistencyDowngradesConflictingClaims` +
  `TestApplyConsistencyLeavesConsistentPacketUntouched`.
  Phase report: docs/phase-reports/phase-14.md.

### Phase 15 (Freshness + version awareness, 2026-08-25)

Stale knowledge is never silently presented as current fact:

- **Fixed G1**: `MemoryStore.Recall` returned superseded/historical memory (the
  exit gate violated on every packet's memory path). Now current-only by
  default with an explicit `IncludeNonCurrent` escape hatch; `Add` sets
  `Status = MemoryCurrent` explicitly.
- 15.1 metadata, 15.2 states, 15.3 git-diff invalidation + P5.9 freshness
  gating, 15.4 supersession (Supersede/MarkHistorical/CurrentMemories), 15.5
  opt-in freshness scoring all verified.
- Exit gate locked: `TestRecallExcludesSupersededMemory` +
  `TestMemoryCarriesFreshnessMetadata`.
  Phase report: docs/phase-reports/phase-15.md.

### Phase 16 (Audit + resume + replay, 2026-08-25)

Tasks survive restart and resume without losing state:

- 16.1 flight recorder (full action vocabulary incl. outcome/verification/PR),
  loop records per stage; 16.2 Resume + reconstructContext (full context
  rehydration) + `kern task resume`; 16.4 RunCompare (agent/model/tool/cost/
  success) all verified.
- **Fixed 16.3 gap**: `ReplayRecord` now carries ContextVersion (digest of the
  replayed context packet) + ToolVersions (digest of the distinct tools used)
  alongside repo/model/config hash, so replays are comparable on context and
  tool surface.
- Exit gate (restart/resume without state loss) locked since Phase 9 + the
  resume/replay/compare suites.
  Phase report: docs/phase-reports/phase-16.md.

### Phase 17 (Context/token/cost benchmarking, 2026-08-25)

Reproducible benchmark suite:

- 17.3 baseline-vs-Kern (CompareToBaseline), 17.4 context quality, 17.5 task
  outcomes, 17.6 `kern efficiency <id>` all verified.
- **Exit gate closed**: `TestReproducibleBenchmarkSuite` — 2 benchmark repos
  (microservice fixture + legacy) × 4 task classes (lookup/small-change/
  architecture/incident), run twice with identical measurements. Actuals:
  75% input-token reduction, 75% tool-call, 100% retries, ~86-91% cost on
  context-bearing classes.
  Phase report: docs/phase-reports/phase-17.md.

### Phase 18 (Web control console, 2026-08-25)

A human can inspect and approve/reject a Task through the UI:

- All 18.1 core pages (Dashboard/Tasks/Task Detail/Approvals/Risks/Artifacts/
  Audit), 18.2 engineering views (System Map/Graph/Memory/Agents/Incidents/
  Architecture), and 18.3 efficiency verified.
- **Fixed G1**: the UI's approval surface was in-memory only — disconnected
  from the workflow engine's persistent `.kern/approvals.json` gates. Web
  approve/reject now write through the FileStore and the pending list merges
  it, so a human can approve/reject a parked workflow in the console.
- Exit gate locked: `TestWorkflowApprovalThroughUI` (inspect → pending →
  approve → workflow completes) + `TestWorkflowRejectionThroughUI`.
  Phase report: docs/phase-reports/phase-18.md.

### Phase 19 (REST/SDK/enterprise, 2026-08-25)

One control plane across every surface:

- 19.1 REST: 25 /v1 routes covering all 12 required categories (tasks/analyze/
  plan/impact/what-if/approve/execute/verify/memory/incidents/artifacts/audit).
- **19.2 Go SDK NEW**: `sdk/go/kernsdk` — stdlib-only client mirroring the 28
  Python methods, integration-tested against a REAL kern-server (same
  TaskService services as CLI/MCP).
- 19.3 enterprise /org/* (memory/tasks/agents/teams/audit/policies/search)
  verified; 19.4 Python (16 tests) + TypeScript (12 tests) SDKs pass.
- Exit gate locked: `TestGoSDKAgainstControlPlane`.
  Phase report: docs/phase-reports/phase-19.md.

### Phase 20 (Controlled autonomy, 2026-08-25)

Autonomy passes failure, security, rollback, budget, and policy-bypass tests:

- 20.1 autonomy score (risk/confidence/reversibility/env/permissions/evidence/
  historical), 20.2 L0-L5 + L5 proof gate, 20.3 SafetyBudget → PAUSE, 20.4
  pause triggers (scope/confidence/tool/file/policy/verification), 20.5
  evidence-based level raising — all verified.
- **Exit gate locked**: `TestAutonomyExitGate` — consolidated drill proving
  failure surfacing, L5 security proof gating, deploy rollback (failed
  Deployer → rolled-back event, never silent success), budget PAUSE, and L0
  policy-bypass prevention.
  Phase report: docs/phase-reports/phase-20.md.

### Final (E2E matrix + docs, 2026-08-25)

- E2E test matrix §12 Tests A-J all PASS (acceptance_matrix_test.go): code
  change, high-risk approval, what-if, incident→PR, resume, policy block,
  context pruning, sufficiency, isolation, agent routing.
- Documentation set §13 complete (14/14 architecture docs, roadmap, ADR-0001,
  21 phase reports).
- Final verification: `go test ./...` 91/91 packages PASS, exit 0; vet +
  treesitter + sqlite builds clean; Python SDK 16/16, TypeScript SDK 12/12.
  Phase report: docs/phase-reports/phase-final.md.

## Known TODOs / limitations

- docs/architecture deliverables being written in this phase.
- Several spec micro-phases PARTIAL/MISSING — see gap-analysis.md.

## Failure modes
- Stale index: the graph can lag the filesystem after an edit (no live file-watch invalidation). Mitigation: rebuild before impact/blast-radius analysis; freshness is best-effort.
- Incomplete index: only 17 langs are indexed in the stdlib build, so symbols in tree-sitter-only languages are invisible until `-tags treesitter` is used. Mitigation: enable the optional tag when analyzing those languages; document coverage.
- Per-request reindex latency: indexing is triggered on demand rather than pre-built, so first impact/what-if queries pay the full pipeline cost. Mitigation: pre-warm the index for large repos before interactive analysis.
- Optional-tag build variance: `tree-sitter` and `sqlite` builds diverge from the stdlib baseline, so behavior can differ by build. Mitigation: run the test matrix under each tag variant; treat stdlib-only as the default contract.
- Cache staleness: enterprise LRU project cache and in-memory event history can serve stale state after out-of-band changes. Mitigation: invalidate/refresh before governance approval or audit reads.
- Governance gating on a stale constitution: if `.kern/constitution.yaml` changes, in-flight policy prechecks may not reflect the new rules. Mitigation: reload the constitution at task boundary; fail-closed on parse errors.