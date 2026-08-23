# Intent Engine — internal/app, internal/domain

## Purpose
The intent engine turns a raw intent string into a compiled, classified, and
capability-mapped plan of execution. It selects a workflow, gates the intent
through a unified policy precheck, and produces a `RunResult` describing the
required capabilities, tools, agents, risk, and approval state. Everything here
is deterministic (keyword/verb matching, no LLM) — the LLM planner lives in
`internal/planner`.

## IntentType taxonomy — `internal/domain/intent_type.go`
Ten intent types (`intent_type.go:7-18`): `UNDERSTAND`, `CODE_CHANGE`, `REVIEW`,
`WHAT_IF`, `INCIDENT`, `MODERNIZATION`, `SECURITY`, `TEST`, `DEPLOY`, `AUDIT`.

`CompiledIntent` (`intent_type.go:21-29`): `Type`, `Objective`, `Target`,
`Scope`, `Environment`, `DesiredOutcome`, `RawText`.

`WorkflowID` (`intent_type.go:33-40`): the five primary workflows `A-E` —
`A_UNDERSTAND`, `B_SAFE_CHANGE`, `C_PREDICT`, `D_OPERATE`, `E_GOVERN`.

`RunResult` (`intent_type.go:45-60`) — the output of `kern_run`: `TaskID`,
`Workflow`, `Intent`, `Capabilities`, `Tools`, `Agents`, `ContextPlan`, `Risk`,
`ApprovalState`, `NextAction`, plus an optional `Precheck`.

## Compilation — `internal/app/intent.go`
- `CompileIntent(raw)` (`intent.go:23`) classifies via keyword/verb matching
  (e.g. `"explain"/"understand"` → `UNDERSTAND`, `"add"/"implement"/"fix"` →
  `CODE_CHANGE`, `"what if"/"simulate"` → `WHAT_IF`, `"incident"/"alert"` →
  `INCIDENT`, `"modernize"/"split"` → `MODERNIZATION`, `"security"/"vulnerab"` →
  `SECURITY`, `"deploy"/"release"` → `DEPLOY`, `"audit"` → `AUDIT`), defaulting
  to `CODE_CHANGE`. It extracts a `Target` via `extractTarget` (first CamelCase
  word or word after `to`/`in`/`the`) and fills scope `"repository"`, env
  `"development"`.
- `SelectWorkflow(it)` (`intent.go:65`) maps an `IntentType` to a `WorkflowID`
  (e.g. `CODE_CHANGE`/`REVIEW`/`TEST`/`SECURITY` → `B_SAFE_CHANGE`).
- `DefaultCapabilities(it)` (`intent.go:85`) returns the capability list
  required for each intent (e.g. `CODE_CHANGE` → analyze/plan/impact/execute/
  verify/pr; `INCIDENT` → correlate/investigate). `CapabilitiesToTools` and
  `CapabilitiesToAgents` flatten these into tool and agent lists.

## Capability registry — `internal/app/capability.go`
- `CapabilityRegistry` (`capability.go:27`) is the single source of truth,
  seeded from the static catalog `allCapabilities()` (`capability.go:33`), each
  entry carrying a `Purpose` (P6.6), tools, permissions, risk, outputs,
  artifacts.
- Discovery (P6.9): `Get`, `All`, `Tools()`, `Agents()` (`capability.go:60-97`).
- `CapabilityPrecheck` (`capability.go:113`) runs the P6.4 precheck: identity
  non-empty, scope non-empty, high-risk caps require an explicit environment,
  and every required tool must be available in the toolset.
- `DeterministicPlan` (`capability.go:157`) produces a rule-based implementation
  plan when no LLM planner is available (P6.7 fallback).
- `FallbackFor` (`capability.go:149`) maps unavailable tools to equivalents
  (e.g. `kern_what_if` → `kern_impact`, `kern_validate` → `kern_verify`) (P6.10).
- `ToolDecisionTraceRecorder` (`capability.go:205`) collects
  `domain.ToolDecisionTrace` entries (`Record`/`Traces`/`Len`) so the tool-
  decision trail can be audited (P6.8).

## Task entry — `internal/app/task.go`
- `TaskService.Run(intent)` (`task.go:254`) is the `kern_run` entry point. It
  compiles the intent, selects the workflow, derives capabilities/tools/agents,
  computes a preliminary risk (any `"high"` capability sets `RiskHigh` +
  `ApprovalRequired`), creates the `Task`, runs the unified `PolicyPrecheck`,
  and returns a `RunResult`.
- `actionForIntent` (`task.go:324`) maps each intent to its representative
  governed action (`deploy`, `write`, `scan`, `audit`, `read`) used in precheck.
- `PolicyPrecheck(ctx, req)` (`task.go:356`) is the unified pre-execution gate
  combining environment → scope/path → firewall permission+risk into one
  `PrecheckResult` (`domain.PrecheckRequest`/`PrecheckResult`,
  `domain.go:284,297`). It is read-only and never mutates state; any gate
  failure returns a `DenyReason` with the failing `Stage`.
- `RunLoop(intent, level)` (`task.go:429`) is the task-scoped closed-loop entry
  point that creates an authoritative `Task`, runs the loop at the requested
  autonomy level, and records the run as an artifact.

## Dependencies
- `internal/domain` (types), `internal/agent`/`internal/agents` (task + roles),
  `internal/loop` (autonomy loop), `internal/governance` (firewall), and
  `internal/planner` (optional LLM planner).

## Storage / security
- `Run` persists the created `Task` (`s.persist`). Governance is enforced by
  `PolicyPrecheck` up front (advisory on `RunResult`) and by the firewall/
  gateway at execution (see tool-gateway.md). Execution is always gated
  separately — precheck is never authoritative by itself.

## Failure modes
- Unknown capability → precheck problem `"unknown capability: <name>"`.
- High-risk capability without env → precheck problem (fail-closed).
- Missing required tool → precheck problem.

## Tests
- `internal/app/intent_test.go`, `capability_test.go`, `policy_precheck_test.go`,
  `internal/context/intent_test.go`, `internal/app/task_test.go`
  (`TestRunWorkflowClassifiesTask`).

## Performance / trade-offs
- Deterministic keyword classification is fast and cheap but less expressive
  than an LLM; the LLM planner is available via `internal/planner` when a
  provider is configured. Capability planning is a static catalog lookup, so
  precheck is O(tools) and deterministic.