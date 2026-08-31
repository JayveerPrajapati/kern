# PHASE 1 — TASK AS CENTRAL LIFECYCLE — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 1.1–1.7.
Go: 1.23, stdlib-only default build (tree-sitter + sqlite behind build tags).

## Scope

Make Task the authoritative unit of engineering work: aggregate refs, canonical
state machine, persistence, cancel/pause/resume, retry, human takeover, and
task snapshot — all independent of MCP/CLI-specific state (exit gate).

## Work completed (micro-phases)

### 1.1 — Task aggregate (P0)

- Verified every required reference exists on `agent.Task` (task.go), additive
  and JSON-persisted through `TaskStore`:
  `task_id, intent, project, repository, scope, requester, state, workflow,
  agent_refs, context_ref, memory_refs, impact_ref, risk_ref, policy_ref,
  approval_ref, plan_ref, artifact_refs, verification_ref, pr_ref,
  deployment_ref, outcome_ref, learning_ref`.
- **Added `Task.Validate()`** — aggregate invariants: required fields
  (id/type/state/created_at), valid canonical state, whitespace-only reference
  rejection. This was missing; the spec's "missing required fields / invalid
  state / invalid reference" cases had no enforcement point.
- Tests: `TestTaskValidate{MissingRequiredFields,InvalidState,InvalidReference,Valid}`
  (new) + existing `TestTaskAggregateRefsRoundTrip` / `TestTaskAggregateRefsJSONRoundTrip`.
- Compatibility: `TestTaskSerializationCompatibility` (new) proves a legacy
  pre-refs JSON record still unmarshals with zero-valued new fields and
  survives a store round trip.

### 1.2 — Task state machine (P0)

- All 17 canonical states present (`domain.TaskCreated`…`TaskRolledBack`) with
  the explicit `validTransitions` table; terminal states are not keys so no
  transition leaves them (fail closed); `TaskFailed`/`TaskBlocked` are
  recoverable (retry/resume).
- **Concurrency safety added** — `Transition` was a lock-free read-modify-write
  on a shared `*Task`. The workflow engine steps a task while a cancel/pause
  handler may mutate the same pointer; under `-race` that is a data race and the
  spec's "concurrent transition" test would fail. Fixed:
  - `Task.stateMu *sync.RWMutex` (pointer, so `TaskStore.Save(*tk)` value
    copies share the lock instead of tripping `go vet` copylocks; never JSON'd).
  - `Transition` → locked `transitionUnlocked` core; every compound mutator
    (`Start/Complete/Fail/Cancel/Timeout/Block/Pause/Resume/RetryWithReason/
    Rollback/HumanTakeover/ReturnToAgent`) holds the lock across its whole
    read-modify-write and calls the unlocked core.
  - `Terminal()` locked accessor; workflow engine aborts on it instead of the
    unlocked `IsTerminal()`.
- Tests: `TestTaskConcurrentTransition` (8 racing goroutines through the legal
  path + concurrent cancel; race-clean under `-race`), `TestTaskDuplicateTransition`
  (new). Existing valid/invalid/restart coverage retained
  (`TestTaskTransitionValid`, `TestTransitionInvalid`, `TestRestartResume`).

### 1.3 — Persistence (P0)

- Persists (verified): state, timestamps, artifacts + artifact refs, approval
  (`ApprovalRef`), agent state (`AgentID`), errors (Output on fail), current
  stage.
- **Added `Task.CurrentStage`** (spec explicitly lists "current stage") — the
  workflow engine records the in-progress step (`workflow.go` sets
  `rootTask.CurrentStage = step.Action` before executing), so a failure or
  pause mid-step records where the task was. Persisted with the task.
- Tests: `TestCurrentStagePersisted`, `TestWorkflowSetsCurrentStage` (engine
  drives `analyze`→`plan`, stage persisted as `plan`), `TestTaskStoreCorruptRecord`
  (corrupt file surfaces its error, not a silent empty store), `TestTaskMissingArtifact`
  (task refs to absent artifacts persist fine; resolution is the artifact
  store's job — `TestArtifactStoreGetMissing` in `internal/app` verifies
  `os.ErrNotExist`), plus existing `TestRestartResume` (new-store reload +
  resume) and the cross-process tests.

### 1.4 — Cancel / pause / resume (P1)

- Already present: `Cancel` (CANCELLED), `Pause`/`Block` (BLOCKED + PriorState),
  `Resume` (→ PriorState), `Timeout` (FAILED). Behavior defined for every
  state; idempotent where appropriate. Service layer: `TaskService.Cancel/Timeout/
  Retry/Resume/Pause/HumanTakeover` all persist + publish events.
- Tests: `TestPauseResumeCancelAcrossStates`, `TestPauseSetsBlockedAndPriorState`,
  `TestPauseFailsOnTerminal`, `TestCancelFromRunningState`, `TestCancelFromTerminalFails`,
  `TestResumeFromBlocked`, `TestResumeFromBlockedProductionStates`,
  `TestResumeIdempotency`.

### 1.5 — Retry (P1)

- Already present: `Retry`/`RetryWithReason` reopen FAILED → ANALYZING with
  `RetryCount`, `RetryReason`, `LastResult` (prior Output) tracked; idempotent;
  terminal tasks reject retry. Service layer persists + publishes retry_count/
  retry_reason.
- Tests: `TestRetryTracksAttemptReasonAndResult`, `TestRetryFromFailed`,
  `TestRetryIdempotency`, `TestRetryFailsOnTerminal`.

### 1.6 — Human takeover (P1)

- Already present: `HumanTakeover(agentID)` → BLOCKED + binds human + PriorState
  preserved; `ReturnToAgent(agentID)` → resumes to PriorState + rebinds. Behavior
  preserved exactly (Output "human takeover"). Service layer:
  `TaskService.HumanTakeover`.
- Tests: `TestHumanTakeover`, `TestReturnToAgent`.

### 1.7 — Task snapshot (P2)

- Already present: `Task.Snapshot()` → `domain.ContextSnapshot` with the spec's
  exact JSON shape (`goal, state, decisions, constraints, files, tests, risks,
  next_action`); `SnapshotStore.Record` persists a rich snapshot on every task
  persist with History/ListByState/ListSince indexes.
- Tests: `TestTaskSnapshotMethod`, `TestSnapshotRecordsRichFields`.

## Tests

- `go vet ./...` — PASS
- `go build ./...` — PASS; `-tags treesitter`, `-tags sqlite` — PASS
- `go test ./internal/agent/` — PASS (incl. 11 new spec tests, race-clean under `-race`)
- `go test ./internal/app/` — PASS (87s, incl. new `TestArtifactStoreGetMissing`)
- Remaining 88 packages — PASS, exit 0 (no ripple from the Task refactor)
- `go test -race ./internal/agent/ -run 'Concurrent|CrossProcess|SameID'` — PASS

## Exit gate

> "A Task can start, progress, pause, resume, cancel, retry, fail, rollback,
> and complete with persistence and audit." — MET (agent.Task + TaskStore +
> TaskService + SnapshotStore, all interface-independent).

## Notes / non-changes

- TaskStore cross-process locking + store-assigned IDs were delivered in the
  Phase 0 close-out (filelock + `nextStoreTaskID`); Phase 1 verified them in
  the concurrent/cross-process tests and left the design intact.
- No MCP/CLI handler changes were needed: the aggregate, state machine, and
  stores are the single source of truth; handlers already delegate through
  `TaskService`.