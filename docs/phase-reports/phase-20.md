# PHASE 20 — CONTROLLED AUTONOMY — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 20.1–20.5.
Go: 1.23, stdlib-only default build. Phases 0-19 all PASS before this phase
started (per spec §20 "MUST NOT START until Phases 0–19 are completely PASS").

## Scope

Autonomy passes failure, security, rollback, budget, and policy-bypass tests.

## Micro-phase audit (all verified in source + tests)

- **20.1 (P0) Autonomy score** — `internal/loop/autonomy_score.go` +
  `autonomy_score_test.go`: score from risk, confidence, reversibility,
  environment, permissions, evidence, historical success; recommended level
  mapping; hard cap (AllowedByScoreHardCap).
- **20.2 (P0) Levels** — L0 Read-only … L5 Low-risk autonomous with
  `AllowsStage` per stage (plan/learn ≥ L1, code ≥ L2, deploy/protect ≥ L4,
  remember/verify/observe always) + the L5 proof gate
  (`AllowsStageWithProofs`, 6 proofs, fail-closed).
- **20.3 (P0) Safety budget** — `domain.SafetyBudget` (files, services, tools,
  tokens, cost, runtime, risk, environments, per-kind tool caps) →
  PAUSE on exceed.
- **20.4 (P0) Pause triggers** — `autonomy_triggers.go`: ScopeExpanded,
  ConfidenceDropped, UnexpectedTool, UnexpectedFile, PolicyChanged,
  VerificationRegressed + `LoopConfig.PauseTrigger` hook + Result.Paused/
  PauseReason.
- **20.5 (P1) Evidence-based learning** — historical success raises the
  recommended level only on recorded evidence (TestHistoricalSuccessRaises
  RecommendedLevel).

## Gap found and closed

### G1 — No consolidated exit-gate drill

Individual guards were tested (L5 proof gate ×5, budget pause ×3, risk pause,
tool-kind limits, learn autonomy-aware), but the exit gate — "autonomy passes
failure, security, rollback, budget, and policy-bypass tests" — had no single
drill proving all five dimensions at the loop level.

**Fix (`internal/loop/autonomy_exit_gate_test.go`):**
`TestAutonomyExitGate` — five sub-tests:
- **failure**: a failing stage surfaces its error (no silent success)
- **security**: L5 without proofs denies code/deploy; all 6 proofs required
- **rollback**: a failing Deployer → res.Deployed=false + DeploymentRolledBack
  event + error (never a silent success)
- **budget**: budget overrun PAUSES the loop
- **policy-bypass**: at L0 the code stage never executes even with a handler

## Exit gate

> "Autonomy passes failure, security, rollback, budget, and policy-bypass
> tests." — **MET and LOCKED** by the consolidated drill (plus the existing
> per-guard suites).

## Verification

- `go vet ./...`, `go build ./...`, `-tags treesitter`, `-tags sqlite` — PASS
- **91/91 packages pass, exit 0** (loop incl. the new drill + 90 rest)
- Full loop suite (36 existing + 1 new test) PASS