# Testing Model — internal/app and package-level tests

## Purpose
Kern's test suite is deterministic and offline: no live LLM, no network, and a
preferred pattern of fixtures + golden assertions. It covers the end-to-end
acceptance matrix (A–J), the 20-step vertical slice, a flagship slice with
failure drills, artifact coverage, replay/resume, and unit tests across every
package. The baseline (`docs/architecture/current-state.md`) verifies
`go build ./...`, `go vet ./...`, and `go test ./...` all pass.

## Test principles
- **Deterministic** — assertions never depend on an LLM or wall-clock timing
  beyond fixed clocks; async delivery is flushed via `eventbus.Bus.Flush()`.
- **Fixtures / golden** — tests use real on-disk repos (fixture tree) rather
  than inline consts; failure drills inject known-bad source.
- **No live LLM** — every engine under test is exercised through its
  deterministic path.

## Acceptance matrix (A–J) — `internal/app/acceptance_matrix_test.go`
`TestAcceptanceMatrix` (`acceptance_matrix_test.go:89`) runs the 10 mandatory
E2E scenarios §7 as labeled subtests, reusing existing helpers:

- **A** code change (verified PR) — `Analyze` reaches `COMPLETED` with a
  recorded artifact (`:90`).
- **B** high-risk change (approval required) — `security.write` is not allowed
  and yields a pending `Approval` at `HIGH`/`CRITICAL` risk (`:114`).
- **C** what-if scenario report — `WhatIf(SplitService)` attaches an
  `ImpactReport` with risk and text (`:136`).
- **D** incident → remediation PR — `InvestigateIncident` produces a task +
  incident + artifact (`:159`).
- **E** resume — persist, reload, resume to the same logical state (`:190`).
- **F** policy block — unauthorized prod op denied + audit + no side effect.
- **G** context pruning — irrelevant removed, constraints retained.
- **H** context sufficiency — aggressive pruning retains required constraint.
- **I** cross-task isolation — no memory leakage between tasks.
- **J** agent routing — policy-aware, auditable routing.

The shared `Platform`/`TaskService` is built lazily via `matrixOnce`
(`:65`) since building indexes the repo; indexing subtests skip under `-short`.

## Vertical slices — `internal/app/vertical_slice_test.go`
- `TestVerticalSlice1AnalyzePlanImpactVerifyPR` (`:34`),
  `TestVerticalSlice3WhatIfScenario` (`:188`),
  `TestVerticalSlice2IncidentCorrelateRootCause` (`:422`).
- `TestFullLifecycle20StepVerticalSlice` (`:616`) drives the complete 20-step
  lifecycle end to end.
- Also: `TestTaskServiceAgentIdentity`, `TestArtifactImmutability`,
  `TestDeployApprovalGate`, `TestDeployNoopSkipsApprovalGate`,
  `TestWebhookIdempotency`.

## Flagship slice + failure drills — `internal/app/flagship_slice_test.go`
- `TestFlagshipVerticalSlice` (`:87`) runs a measured multi-stage lifecycle
  (with `lifecycleMetrics` tracking stage order).
- `TestSevenFailureDrill` (`:169`) drives seven failure modes under one test.

## Failure drills — `internal/app/failure_drill_test.go`
Seven substantive deterministic drills (`failure_drill_test.go:15-19`):

- `TestFailureDrillPolicyDenial` (`:23`) — unauthorized prod op DENIED, audited,
  no side effect.
- `TestFailureDrillTestFailure` (`:53`) — injected failing test → verification
  `FAIL`.
- `TestFailureDrillSecurityFailure` (`:74`) — critical SQL-injection finding →
  `FAIL`.
- `TestFailureDrillArchitectureFailure` (`:96`) — `client→lib` boundary
  violation → `FAIL`.
- `TestFailureDrillAgentTimeout` (`:119`) — timeout fails the task and publishes
  `task.failed`.
- `TestFailureDrillToolFailure` (`:136`) — governed exec refused when not
  allowlisted (fail-closed).
- `TestFailureDrillSandboxFailure` (`:147`) — path-escaping patch rejected, tree
  unchanged.

## Artifact coverage — `internal/app/artifact_coverage_test.go`
`TestSafeChangeProducesAllArtifacts` (`:38`) verifies a safe change produces
every expected artifact kind (`recordKinded` chains `ParentArtifactID`).

## Fixture repository — `internal/app/fixture_test.go` + `testdata/`
- `fixtureDir = "testdata/fixtures/user_service"` (`fixture_test.go:13`) is a
  real, buildable Go module (go.mod, main.go, user.go, cache.go, tenant.go,
  service.go, user_test.go, `.kern/boundaries.json`).
- `loadUserFixture` (`:31`) asserts all required files exist; it is the single
  helper for tests needing the on-disk UserService repo.
- `TestUserServiceFixtureBuildable` (`:45`) runs `go build ./...` and
  `go test ./...` inside the fixture, proving it is a real source tree.

## Replay / resume — `internal/app/replay_resume_test.go`
- `TestReplayTaskMetadata` (`:12`) — task metadata survives replay.
- `TestRunCompare` (`:47`) — compares two runs (artifact comparison).
- `TestResumeReconstructsContext` (`:80`) — resume rebuilds task context.

## Consistency — `internal/consistency/engine_test.go`
- Exercises `Engine.Check` across `domain.ConflictResult` (NO_CONFLICT /
  CONFLICT / STALE / UNKNOWN) and `ConsistencyConflict.Explain()`.

## Package-level unit tests
Every package ships focused unit tests, e.g.:
- `internal/app`: `intent_test.go`, `capability_test.go`,
  `policy_precheck_test.go`, `task_test.go`, `task_policy_scope_test.go`,
  `lifecycle_test.go`, `runloop_test.go`, `efficiency_test.go`,
  `platform_test.go`, `artifact_store_test.go`, `artifact_replay_test.go`.
- `internal/context`: `engine_test.go`, `phase5_test.go`, `select_test.go`,
  `gc_test.go`, `freshness_test.go`, `consistency_test.go`, `risk_test.go`,
  `runtime_test.go`, `arch_runtime_test.go`.
- `internal/governance`: `gateway_test.go`, `exec_test.go`,
  `firewall_test.go`, `approval_test.go`, `audit_test.go`,
  `constitution_test.go`, `risk_test.go`.
- `internal/agent` / `internal/agents` / `internal/loop` / `internal/runtime` /
  `internal/incident` / `internal/evidence` / `internal/domain` all carry
  dedicated suites.

## Build / vet / test
- `go build ./...` and `go vet ./...` pass.
- `go test ./...` passes (app suite ~338s; core control-plane packages pass).
- Build-tag variants (`-tags treesitter`, `-tags sqlite`) verified at prior audit.
- Indexing-heavy tests (scenarios A, D) are gated behind `testing.Short()` so
  `-short` runs skip repo indexing.

## Performance / trade-offs
- Deterministic, no-LLM tests are fast and reproducible but cannot exercise the
  optional LLM planner/coder paths; those are covered by unit tests of the
  deterministic fallback (`DeterministicPlan`, `RouteModel`, etc.). Using a
  real on-disk fixture (`go build`/`go test` inside it) verifies end-to-end
  behavior at the cost of a slower test run, mitigated by lazy construction and
  `-short` skips.

## Named gates - shared convention (E-4)

Blueprint's suite organizes verification as named gates (`TestG<n>_<what>`:
G14 contract, G22 repair-loop isolation, G26 MCP root confinement ...) so
"what is verified" is legible per gate. Kern adopts the same convention so
coverage reads consistently across both repos:

- **Gate-named tests**: `TestGate*` / `TestG<n>_<what>` - one gate, one
  verifiable claim, with a comment stating the claim.
- **Existing kern named coverage maps onto it**: the acceptance matrix A-J
  (internal/app), the guard/intel tests (`TestGuard*`,
  `TestGuardCheck*`, `TestGuardImportAttribution*`), and the MCP gate tests
  (`TestGate*`, `TestDispatchGate*` in internal/mcp).
- **Rule**: a new test that verifies a gate ("a change is rejected /
  approved / isolated under condition X") is named `TestGate<n>_<what>`
  (kern) or `TestG<n>_<what>` (blueprint) with a comment naming the claim.
  Gate numbers are stable; new gates append.
