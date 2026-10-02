# Specialist Pipeline State Machine

> Generated 2026-09-30 against HEAD `5939a0e`. Grounded in
> [`internal/agents/selection.go`](../internal/agents/selection.go) (task kinds, stage/workflow selection),
> [`internal/agents/pipeline.go`](../internal/agents/pipeline.go) (stage runner, handoffs, event bus),
> [`internal/agents/roles.go`](../internal/agents/roles.go) (the 7 roles), and
> [`internal/agent/workflow.go`](../internal/agent/workflow.go) (WorkflowEngine, approval gates).

kern's multi-agent execution has two cooperating tracks: the **specialist Pipeline** (ordered stages run by
role specialists, no built-in approval gate) and the **WorkflowEngine** (`agent.Workflow` steps that drive
the task state machine, including the human approval gate). `SelectPipeline` serves the first,
`SelectWorkflow` the second; `PipelineForKind` deliberately does NOT insert an approval gate — callers that
need governance must use `SelectWorkflow` with the WorkflowEngine, where Invariant #2 (high-risk execution
requires approval) holds (selection.go:93-102).

## The 7 specialist roles (roles.go:27-36)

| Role | Purpose | Produces | Autonomy |
|------|---------|----------|----------|
| Planner | Analyze the request and produce an implementation plan | plan | L0-L3 |
| Architect | Design the change against the existing code graph and boundaries | design | L0-L3 |
| Coder | Implement the planned change in source and tests | code | L2-L3 |
| Reviewer | Review the change for correctness and consistency | review | L0-L2 |
| Security | Scan the change for security risks and policy violations | security report | L0-L2 |
| Tester | Run and extend tests to verify the change | test results | L0-L2 |
| SRE | Assess runtime/deployability impact and production readiness | ops assessment | L0-L4 |

SRE is registered in the standard team but is **not** part of the default 6-stage pipeline (team.go:14-16).

## Task kinds (selection.go:20-35)

| Constant | Meaning |
|----------|---------|
| `TaskKindCode` | Regular code change — full default pipeline |
| `TaskKindDocumentation` | Docs-only change |
| `TaskKindIncident` | Incident / root-cause task |
| `TaskKindModernization` | Refactor / modernization task |
| `TaskKindDefault` | Backward-compatible default; same pipeline as `TaskKindCode` |

`ClassifyTask(intent, taskType)` (selection.go:72-86) is deterministic keyword matching — no LLM. An
explicit `taskType` wins over intent keywords:

* `incident`, `correlate`, `root-cause`, `alert` → Incident
* `modernize`, `refactor`, `extract`, `split-monolith` → Modernization
* `document`, `docs`, `readme` → Documentation
* anything else → Code

## Workflow kinds and step sequences

### Specialist Pipeline stages (`SelectPipeline`, selection.go:41-68)

Each stage = {name, role, action}; the default sequence is `DefaultStages()` (pipeline.go:23-36).

| Kind | Stage sequence (role) |
|------|----------------------|
| Code / Default | plan (Planner) → architect (Architect) → code (Coder) → review (Reviewer) → security (Security) → test (Tester) |
| Documentation | plan (Planner) → review (Reviewer) |
| Incident | plan (Planner) → code (Coder) → security (Security) → test (Tester) → sre (SRE) |
| Modernization | architect (Architect) → plan (Planner) → review (Reviewer) |

### WorkflowEngine steps (`SelectWorkflow`, selection.go:104-147)

Each workflow hardcodes a human `approve` step (`AgentType: "human"`, `RequiresApproval: true`) placed
before its first execution step; the code-change default comes from `agent.DefaultWorkflow()`
(workflow.go:292-306).

| Kind (workflow ID) | Step sequence (agent) |
|--------------------|----------------------|
| Code / Default (`default`) | request (planner) → analyze (planner) → plan (planner) → **approve (human)** → code (coder) → verify (reviewer) → pr (reviewer) |
| Documentation (`documentation`) | plan (planner) → **approve (human)** → review (reviewer) → pr (reviewer) |
| Incident (`incident`) | plan (planner) → **approve (human)** → code (coder) → security (security) → test (tester) → sre (sre) → pr (reviewer) |
| Modernization (`modernization`) | architect (architect) → plan (planner) → **approve (human)** → review (reviewer) → pr (reviewer) |

### CODE_CHANGE as a state machine

States = workflow steps; transitions = specialist handoffs. Task states advance along the canonical
lifecycle (workflow.go:89-102) via `driveToState`, which walks intermediate states so a workflow need not
spell out every one:

```
                     WorkflowEngine, default workflow (workflow.go:292)
 ┌──────────┐   ┌───────────┐   ┌────────┐   ┌──────────────┐   ┌────────┐   ┌──────────┐   ┌─────┐
 │  request │──▶│  analyze  │──▶│  plan  │──▶│ *** BLOCKS ***│──▶│  code  │──▶│  verify  │──▶│ pr  │
 │ (planner)│   │ (planner) │   │(planner)│  │   approve    │   │ (coder)│   │(reviewer)│   │(rev.)│
 └──────────┘   └───────────┘   └────────┘   │   (human)    │   └────────┘   └──────────┘   └─────┘
      TaskCreated   Analyzing     Planning    │ RequiresAppr │    Executing    Verifying   ReadyForPR
                                              │ = true       │                              PRCreated
                                              └──────┬───────┘
                                        WAITING_FOR_APPROVAL
                                        resolve → APPROVED, re-run
```

At the `approve` step the engine requests approval, parks the task in `WAITING_FOR_APPROVAL`, and returns
`ErrApprovalRequired` (workflow.go:24-26, :308-315). The caller either calls `CompleteApproval` and re-runs,
or resolves the gate out-of-band (`kern_approve` writes the same `ApprovalStore`); `gateSatisfied` and
`seedFromTask` (workflow.go:235-285) let a fresh engine resume a run parked at a gate across processes.

The full canonical task lifecycle is: CREATED → ANALYZING → PLANNING → WAITING_APPROVAL → APPROVED →
EXECUTING → VERIFYING → READY_FOR_PR → PR_CREATED → DEPLOYING → OBSERVING → COMPLETED (workflow.go:89-102);
`deploy` and `observe` actions drive the tail states.

## Customizing: NewPipelineWithStages

`NewPipeline(team, runtime, approvals)` builds a pipeline with the default 6 stages; a nil team, runtime,
or approval workflow is replaced with fresh defaults (pipeline.go:63-68). `NewPipelineWithStages` runs a
caller-supplied stage sequence instead; an **empty stages slice falls back to the default 6-stage sequence**
(pipeline.go:70-84). `PipelineForKind(kind, ...)` is the convenience wrapper that passes
`SelectPipeline(kind)` as those stages (selection.go:93-102).

## Handoff and event tracking

`Pipeline.Run` (pipeline.go:111-175):

* Creates a fresh `agent.HandoffManager` per run and records every between-stage specialist change via
  `handoffs.Handoff(taskID, from, to, stage)`.
* Publishes to the optional event bus (`WithBus`, pipeline.go:87-89; nil = no-op):
  * `AgentHandoff` — {from, to, stage} on each specialist change
  * `AgentToolCalled` + `AgentCompleted` — per successful stage
  * `AgentError` + `AgentFailed` — when a stage's handler errors
* Stops on a missing specialist for a stage or on any handler error; a handler error wrapping
  `agent.ErrApprovalRequired` propagates so the caller can resolve the gate and re-run.
* Records one `StageResult{Stage, Specialist, Output, OK}` per stage and appends an `agent.Step`
  {action, agentID, result} to the task.

## Approval gates, in one paragraph

The two-track split is deliberate: the specialist Pipeline is for direct callers that handle approval
externally, while the WorkflowEngine path (used by `TaskService.RunWorkflow`) enforces Invariant #2 —
high-risk execution requires approval — via the hardcoded human `approve` step that appears in **every**
workflow kind before the first execution step (selection.go:104-147). Approvals persist through an
`ApprovalStore` (`Get`/`AddPending`/`Decide`, workflow.go:32-36) so `kern approve` and the engine share
state; a parked run resumes with its approval references intact.
