# PHASE 13 — RUNTIME ↔ CODE ↔ AGENT CORRELATION — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 13.1–13.4.
Go: 1.23, stdlib-only default build.

## Scope

Make the runtime↔code↔agent chain traceable end to end with a typed
correlation contract shared across every runtime lane.

## Micro-phase audit (all verified in source + tests)

- **13.1 (P0) Canonical chain** — `runtime.Correlator.CorrelateChain` resolves
  alert → service → deployment → commit → PR (from commit-message refs) →
  task/agent/symbol (from error-event attributes) → trace (TraceLinks from
  trace IDs). ChainLink stages: service, deployment, commit, pr, task, agent,
  symbol, trace, event.
- **13.2 (P0) Correlation contract** — `runtime.CorrelationContract`:
  overall + per-link confidence (FACTUAL/INFERRED/UNKNOWN), relationship,
  evidence count, source, provenance ("runtime:correlate"), timestamp.
- **13.3 (P1) Shared service** — `runtime.SharedCorrelator` (single source +
  lookback window) is used by Correlate, InvestigateIncident, and
  RemediateIncident; deploy/observe read the same runtime source.
- **13.4 (P1) Change fingerprint** — `runtime.ChangeFingerprint` with ALL 13
  dimensions (kind, target, newTarget, files, symbols, services, APIs,
  database, events, risk, tests, agent, model, task) + normalized SHA-256
  digest (case/order-insensitive).

## Gap found and closed

### G1 — No app-layer exit-gate test (deployment → Task/PR/commit/symbol)

The canonical chain, contract, and fingerprint were fully implemented and
tested at the runtime level, but nothing proved the exit gate THROUGH the app
layer: a controlled deployment correlated via `TaskService.Correlate` reaching
the Task/PR/agent/symbol hops (my Phase 11 slice asserted only
service/deployment/commit/symbol).

**Fix (tests, `internal/app/correlation_slice_test.go`):**
- `TestControlledDeploymentTraceableToTaskPR` — controlled deployment of
  commit "abc123def" (message references PR #123) + error events carrying
  task/agent/symbol/trace attributes; asserts the FULL chain
  service → deployment → commit → pr → task → agent → symbol AND trace links
  (the 13.1 Event → Trace hop needs a TraceID on telemetry — documented).
- `TestSharedCorrelatorConsistentAcrossLanes` — correlate + investigate lanes
  resolve the SAME affected service from the same shared correlation service
  (13.3).

## Exit gate

> "A controlled deployment can be traced back to Task/PR/commit/symbol where
> supported." — **MET and LOCKED**: the app-level test proves a controlled
> deployment traces to PR #123, TASK-9, agent-b, CacheService.Get, plus the
> trace link back to raw telemetry.

## Verification

- `go vet ./...`, `go build ./...`, `-tags treesitter`, `-tags sqlite` — PASS
- **90/90 packages pass, exit 0** (app + runtime + 88 rest)
- Existing runtime chain/contract/fingerprint suites still PASS