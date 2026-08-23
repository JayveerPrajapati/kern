# Agent Orchestration — internal/agent, internal/agents, internal/loop

## Purpose
The multi-agent runtime orchestrates specialist agents (Planner, Architect,
Coder, Reviewer, Security, Tester, SRE) through task lifecycles and workflows,
drives them through the 9-stage autonomy loop, and records auditable results and
snapshots. Orchestration spans three packages: `internal/agent` (runtime model,
workflow engine, registry, sessions, handoffs, snapshots), `internal/agents`
(roles, dynamic selection, model routing, result contract), and `internal/loop`
(the closed loop).

## Task lifecycle — `internal/agent/task.go`
- `Task` (`task.go:15`) embeds `domain.Task` plus runtime state: `WorkflowID`,
  `AgentID`, `ParentID`, `Steps` (execution history), `Dependencies`,
  `Artifacts`, `CreatedBy`, aggregate refs (`Project`, `Repository`, `Scope`,
  `Requester`, `MemoryRef`, `PolicyRef`, `ApprovalRef`, `DeploymentRef`,
  `OutcomeRef`), retry tracking, and the structured result contract
  (`Evidence`, `Risks`, `Confidence`, `RecommendedAction`) and lifecycle results
  (`ContextPacket`, `ImpactReport`, `Plan`, `Verification`, `Intent`, `PRURL`).
- State machine `validTransitions` (`task.go:102-118`): the code-change
  lifecycle runs `CREATED → ANALYZING → PLANNING → WAITING_FOR_APPROVAL →
  APPROVED → EXECUTING → VERIFYING → READY_FOR_PR → PR_CREATED → DEPLOYING →
  OBSERVING → COMPLETED`. Terminal states are not keys (fail closed). Retry
  reopens `FAILED → ANALYZING`; Resume reopens `BLOCKED`. `Transition`,
  `Start`, `Complete`, `Fail`, `Cancel` enforce legal transitions.

## Workflow engine — `internal/agent/workflow.go`
- `Workflow`/`WorkflowStep` (`workflow.go:40-52`): a workflow is an ordered list
  of steps (`action`, `AgentType`, `RequiresApproval`, `Timeout`). The
  `DefaultWorkflow` is `request → analyze → plan → approve → code → verify →
  pr` (`workflow.go:161`), where `approve` is a human-approval gate.
- `WorkflowEngine` (`workflow.go:77`): `Run` drives each step, transitions the
  task state (`taskStateForAction`, `workflow.go:55`), resolves the step's agent
  from the registry (fails closed when the `AgentType` is unregistered), and
  records a step log. A `RequiresApproval` step requests approval, parks the
  task in `WAITING_FOR_APPROVAL`, and returns `(task, ErrApprovalRequired)`; the
  caller calls `CompleteApproval` and re-runs, resuming past the gate
  (`progress` map). `approvalKey`/`approvalRef` map governance approval IDs back
  to `(task, step)`. Emits task-lifecycle events on an optional bus.

## Agent registry & store — `internal/agent/registry.go`
- `Registry` (`registry.go:24`) tracks `Agent` identities (with `Capabilities`)
  and submitted `Task`s, backed by an in-memory map plus an optional persisted
  `TaskStore`. `Register` fails closed on empty/duplicate IDs. `SubmitTask`
  assigns an ID, persists to the store, and publishes `task.created`. `GetTask`
  aliases the stored pointer so workflow mutations are visible.
- `TaskStore` (`taskstore.go`) persists tasks; `SnapshotStore`
  (`snapshot.go:28`) records a point-in-time `Snapshot` per task on every
  persist (state, agent, output, timestamp), queryable by `History`, by current
  `ListByState`, or `ListSince` — supporting audit and resume-after-restart.

## Sessions & handoffs
- `Session`/`SessionStore` (`session.go:10`): tracks an agent's working context
  across tasks (`Create`, `Get`, `Touch`, `ForAgent`).
- `HandoffManager` (`handoff.go:15`): records task transfers between agents
  (`Handoff`, `History`, `Last`).

## Specialist roles — `internal/agents/roles.go`
- `Role` constants (`roles.go:7-14`): `planner`, `architect`, `coder`,
  `reviewer`, `security`, `tester`, `sre`. `RoleInfo`/`allRoles`
  (`roles.go:27`) describe each role's purpose, produces/consumes, and autonomy
  range (e.g. Coder `L2-L3`, SRE `L0-L4`). `AllRoles()`/`ForRole` expose them.

## Dynamic selection — `internal/agents/selection.go`
- `TaskKind` (`selection.go:19`): code, documentation, incident, modernization,
  default. `ClassifyTask(intent, taskType)` (`selection.go:71`) maps an intent
  via deterministic keyword matching.
- `SelectPipeline` (`selection.go:40`) returns the ordered stage sequence per
  kind; `SelectWorkflow` (`selection.go:103`) returns the governance-preserving
  `agent.Workflow` that keeps the human `approve` gate before the first
  execution step.

## Model routing & evaluation — `internal/agents/model_routing.go`
- `RouteModel(role, risk, complexity)` (`model_routing.go:24`) picks a model by
  risk/complexity (HIGH→most capable, MEDIUM→balanced, LOW→cheap), overridable
  by `KERN_MODEL_*` env vars. `RouteModelForTask` adds `RoutingFactors`
  (language, historical success) adjustments. `CompareModels` (`model_routing.go:172`)
  does a model A/B comparison scoring `domain.AgentEvaluation`s.
- `EvaluateAgent` (`model_routing.go:92`) records an `AgentEvaluation`
  (`domain/agent_eval.go`) with success, tokens, cost, duration, retries, human
  intervention, defects.

## Result contract — `internal/agents/result.go`
- `AgentResult` (`result.go:18`) is the serializable contract for a specialist
  step: `TaskID`, `Agent`, `Status` (`success`/`failure`/`blocked`), `Result`,
  `Evidence` (`[]domain.Claim`), `Risks` (`[]domain.Risk`), `Confidence`,
  `Artifacts`, `RecommendedAction`. Fluent `With*`/`Add*` methods build it;
  `NewAgentResult`/`ProduceResult` init it. `MarshalJSON` guarantees slices
  serialize as arrays, never null.

## The closed loop — `internal/loop/loop.go`
- `Loop` drives the 9-stage loop; `LoopConfig` (`loop.go:35`) wires the root,
  autonomy `Level` (L0-L5), runtime `Source`, memory, incident store, approval
  workflow, deployer, learning extractor, flight recorder, coder/planner
  agents, and a `SafetyBudget`.
- `Run(intent, step)` (`loop.go:228`) walks the 9 stages (`autonomy.go:26-34`):
  `intent → remember → plan → code → verify → protect → deploy → observe →
  learn`. Stages the autonomy level does not permit are skipped with
  `"skipped:below-autonomy"`. The `protect` stage is the approval gate.
- Pauses fail-closed: a budget/risk/approval breach sets `Result.Paused` +
  `PauseReason` (`"budget"`/`"risk_exceeded"`/`"approval"`) and stops subsequent
  stages. `Result` (`loop.go:103`) carries `Stages`, `Diff`, `Deployed`,
  `ObservedHealthy`, `Remembered`, `Protected`, `Learned`, `BudgetPaused`.

## Dependencies
- `internal/domain` (TaskState, Task, Plan, Risk, Alert), `internal/eventbus`,
  `internal/governance` (approval, firewall), `internal/planner`/`internal/coder`
  (optional LLM agents), `internal/verification`, `internal/deployment`,
  `internal/incident`, `internal/memory`, `internal/learning`,
  `internal/flight`, `internal/runtime`, `internal/execution`.

## Storage / security
- Tasks + snapshots persist to JSON stores (`<cache>/snapshots/...`). Human
  approval gates are enforced in the workflow engine; unregistered agents fail
  closed. Task state transitions are validated against the state machine.

## Failure modes
- Approval required → task parked in `WAITING_FOR_APPROVAL`, engine returns
  `ErrApprovalRequired` (resume-safe). Step handler error → task `FAILED`.
- Agent `AgentType` not registered → workflow fails closed.
- Budget/risk breach in the loop → run pauses before further write stages.

## Tests
- `internal/agent/task_test.go`, `workflow_test.go`, `lifecycle_test.go`,
  `registry_test.go`, `session_test.go`, `snapshot_test.go`, `handoff_test.go`,
  `bus_test.go`, `task_pause_retry_test.go`.
- `internal/agents/agents_test.go`, `selection_test.go`, `model_routing_test.go`,
  `result_test.go`, `pipeline.go`-backed tests.
- `internal/loop/loop_test.go`, `autonomy_test.go`, `autonomy_score_test.go`,
  `pause_test.go`, `bus_test.go`.

## Performance / trade-offs
- Selection/routing are deterministic keyword lookups (cheap, no LLM), which
  trades expressive classification for predictability. The 9-stage loop runs
  optional LLM agents only when wired; un-wired stages are no-ops (L0-L2
  read-only). Snapshots are written on every persist (whole-file JSON), trading
  write cost for a full, replayable audit history.