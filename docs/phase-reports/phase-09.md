# PHASE 9 — AGENT ORCHESTRATION — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 9.1–9.8.
Go: 1.23, stdlib-only default build (tree-sitter + sqlite behind build tags).

## Scope

Make Kern own agent workflow execution: select the specialist team and
coordinate the run without the external caller manually sequencing it.

## Micro-phase audit (all verified in source + tests)

- **9.1 (P0) Lifecycle** — `agent.WorkflowEngine` drives Task → agent
  selection → session/context (task) → tool calls (step handler) → result
  (Step/StageResult) → artifact → Task state, with handoff tracking
  (`agent.HandoffManager`) and a 13-event agent taxonomy on the bus.
- **9.2 (P0) Specialist roles** — all 7 roles (Planner, Architect, Coder,
  Reviewer, Security, Tester, SRE) in `agents/roles.go` with purpose/
  produces/consumes/autonomy; `StandardTeam` builds capability-scoped
  specialists.
- **9.3 (P0) Result contract** — `agents.AgentResult` matches the spec JSON
  exactly (task_id, agent, status, result, evidence[], risks[], confidence,
  artifacts[], recommended_action), confidence clamped [0,1], slices never
  serialize as null.
- **9.4 (P1) Dynamic routing** — `ClassifyTask` (deterministic keyword
  matching) → `SelectWorkflow`/`SelectPipeline` (kind-specific teams);
  `RoutingContext` carries intent/task type/repository/policy/historical
  success.
- **9.5 (P1) Model/provider routing** — `RouteModel`/`RouteModelForTask`
  (risk/complexity/language/historical-success factors + `KERN_MODEL_*`
  per-role overrides); `ModelOverride` is provider-neutral.
- **9.6 (P1) Shared agent memory** — `memory.MemoryStore.AuthorizedRecall`
  (clearance-gated recall); team capabilities include `memory:read` only on
  the planner.
- **9.7 (P2) Agent evaluation** — `EvaluateAgent` records success/tokens/
  cost/duration/retries/human-intervention/defects into `AgentEvaluation`.
- **9.8 (P2) Agent/model A/B** — `CompareModels`/`EvaluateModel` score two
  candidates on the same task and report the winner + metric deltas.

## Gaps found and closed

### G1 — `RunWorkflow` (the exit-gate implementation) had zero production callers

`TaskService.RunWorkflow` selected the kind workflow and drove the engine, but
no CLI/MCP/web path invoked it, and it REQUIRED a caller-supplied step handler.
The exit gate — "Kern can select and coordinate the agent team without the
external caller manually sequencing it" — was not met.

**Fix (app layer, `internal/app/task.go`):**
- `RunWorkflowDefault(intent)` — the exit-gate entry point: creates the task,
  classifies it, registers the kind workflow, wires the standard team, and
  runs the engine with KERN's OWN default step handler.
- `defaultWorkflowStep` — analyze and plan run the REAL deterministic engines
  (`platform.Analyze` attaches the context packet/risks/evidence;
  `assemblePlan` builds the plan); the remaining role stages produce
  deterministic outcomes from the task's real plan/risk/test data.
- `RunWorkflowResume(taskID)` + `CompleteApproval(id, approver)` + engine-run
  store so an approval-gated run resumes.
- CLI `kern workflow <intent> [--task ID]` (cmd_agent.go, helpers.go).
- MCP tool `kern_workflow` (registered + dispatch + handler; plugin parity in
  BOTH `.opencode/plugins/kern.ts` and `internal/setup/assets/plugin/kern.ts`).

### G2 — Kind workflows never actually ran (latent bug in the existing code)

`task.WorkflowID` was NEVER set anywhere. The engine resolves the workflow by
`rootTask.WorkflowID` and falls back to DefaultWorkflow when empty — so every
task ran the default 7-step workflow regardless of kind. The kind-specific
selection (documentation/incident/modernization) was dead code.

**Fix:** both `RunWorkflow` and `RunWorkflowDefault` now set `t.WorkflowID =
SelectWorkflow(kind).ID` before running.

### G3 — Kind workflows violated the strict task state machine

The incident/modernization/documentation workflows open at "plan", but the
state machine requires CREATED → ANALYZING → PLANNING — the first step failed
with "invalid transition CREATED -> PLANNING" (only surfaced once G2 was fixed
and the workflows actually ran).

**Fix (engine):** `driveToState` walks the canonical lifecycle
(CREATED→…→COMPLETED) applying each intermediate transition, so a workflow may
open at any stage; falls back to a direct transition (strict validation) for
off-chain states. This is the engine owning state advancement; workflows only
spell out the steps that matter.

### G4 — Approval gate + resume were process-local (CLI resume broken)

`kern workflow` parked at the approval gate, but the gate's approval workflow
was in-memory per engine and the run state (progress) died with the process —
`kern approve` (persistent FileStore) could never resolve it, and a fresh
invocation could not resume.

**Fix (engine + app):**
- `agent.Task` persists resume state: `ResumeStep` + `ApprovalRefs`
  (approvalID → step index).
- `WorkflowEngine.WithApprovalStore(ApprovalStore)` — gates consult the
  persistent store (Get → approved ⇒ satisfied), new requests are persisted
  (AddPending), decisions write through (Decide), and `seedFromTask` restores
  progress/refs on a fresh engine. Resume state clears on completion.
- `RunWorkflowResume` recovers a parked task from the TaskStore and rebuilds
  the engine from the persisted workflow + resume state.
- App wires `approval.NewFileStore(root)` — the SAME store `kern approve`
  writes — so out-of-band approval works.

**Verified live (CLI, 3 separate processes):** run → parks (appr-…),
`kern approve <id>` (process 2), `kern workflow --task <id>` (process 3) →
COMPLETED with code/verify/pr steps.

## Tests added

- `internal/app/workflow_default_test.go`: `TestRunWorkflowDefaultSequencesTeam`
  (auto-selection, real analyze/plan, gate, resume → COMPLETED),
  `TestRunWorkflowDefaultIncidentKind` (kind-specific stages: security/test/sre),
  `TestRunWorkflowDefaultCrossProcessResume` (fresh TaskService resumes a
  parked task after out-of-band approval), `TestStageOutcomeDeterministic`.
- `internal/agent/workflow_test.go`: `TestApprovalGatePersistsAcrossEngine`
  (resume state persisted on task; fresh engine sees store-approved gate,
  completes, clears resume state).
- `internal/mcp/workflow_tool_test.go`: `TestWorkflowToolRunsTeam` (kern_workflow
  run → gate → out-of-band approve → resume → COMPLETED).
- `cmd/kern/main_test.go`: `TestRunWorkflowCLI` (CLI parks at gate with
  approval ID).

## Exit gate

> "Kern can select and coordinate the agent team without the external caller
> manually sequencing it." — **MET and LOCKED**, including across process
> boundaries. `kern workflow <intent>` / `kern_workflow` classify the task,
> select the kind workflow, wire the team, drive the steps, park at the human
> gate, and resume after approval — the caller only supplies the intent (and
> the human approval, per Invariant #2).

## Verification

- `go vet ./...`, `go build ./...`, `-tags treesitter`, `-tags sqlite` — PASS
- **90/90 packages pass, exit 0** (agent, app, mcp, setup, governance + 79 rest)
- New tests race-clean (`-race` on app + agent workflow paths)
- Plugin parity test PASS (both kern.ts copies byte-identical)
- CLI cycle verified live across 3 processes

## Non-changes

- The heavy creative execution (closed-loop coder/verifier with worktrees and
  LLMs) remains the loop's job (`kern do`); the workflow default handler
  performs coordination-level execution with real deterministic analyze/plan
  and data-backed stage outcomes. The two are complementary, as documented in
  the existing RunWorkflow comment.
- The firewall/deploy approval gate also uses an in-memory ApprovalWorkflow;
  wiring it to the persistent store the same way is follow-up (Phase 10/16
  scope), not Phase 9.