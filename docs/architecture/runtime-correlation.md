# Runtime Correlation — internal/runtime, internal/incident, internal/consistency

## Purpose
The runtime correlation layer maps production signals (alerts, deployments,
commits, errors, logs, traces, metrics) back to the code that produced them,
building a deterministic evidence chain from a service down to a symbol/task/
PR/agent. It adds a typed confidence contract (FACTUAL/INFERRED/UNKNOWN), a
stable change fingerprint, and a shared process-wide correlator. It pairs with
the incident engine (root-cause, fix-and-verify) and a cross-engine consistency
checker. All logic is deterministic — no live LLM.

## Sources & events — `internal/runtime`
- `Source` interface + adapters (`internal/runtime/adapters.go`) abstract
  prometheus/otel/k8s (live) and local sources; `live.go`, `local.go`.
- Events are typed (`internal/runtime/events.go`): `EventError`, `EventLog`,
  `EventTrace`, `EventMetric`, with `IsError()`, attributes (`symbol`, `file`,
  `task`, `agent`), and service linkage.

## Correlate — `internal/runtime/correlate.go`
- `Correlator` (`correlate.go:29`) maps an alert to the affected service and the
  evidence observed in a lookback window (default 30 min, `NewCorrelator`).
- `Correlate(a)` (`correlate.go:46`) returns a `Correlation` with the alert,
  severity, affected service, deployments, recent commits, and error/log/trace/
  metric events sorted within `[OccurredAt-window, OccurredAt]`.
- `resolveService` (`correlate.go:83`) prefers the alert-declared service,
  otherwise infers from the most recent error event (deterministic fallback).

## Correlation chain — `internal/runtime/chain.go`
- `CorrelateChain(a)` (`chain.go:35`) builds `CorrelationChain` with typed
  `ChainLink`s (`chain.go:16`, stages `service/deployment/commit/pr/symbol/task/
  agent`): service → deployments (by version/commit) → commits (short SHA + PR
  refs parsed from commit messages via `prRefRe`, `chain.go:12`) → symbols/files
  and task/agent refs from error-event attributes. `dedupeLinks` keeps first-seen
  order. `TraceLinks` (`Phase 13.1`) tie the chain back to the raw
  trace/event evidence.

## Correlation contract & fingerprint — `internal/runtime/correlation_contract.go`
- `CorrelationContract` (`correlation_contract.go:34`) classifies a correlation
  and each link as `domain.CorrelationConfidence`: `FACTUAL` (backed by direct
  runtime evidence), `INFERRED` (derived), `UNKNOWN` (unverifiable).
  `Correlation.Contract()` (`correlation_contract.go:44`) sets overall UNKNOWN
  when there is no evidence, else aggregate of per-link classes.
- `ChangeFingerprint` (`correlation_contract.go:126`) is a stable, comparable
  digest of a change over the full dimension set (kind, target, files, symbols,
  services, APIs, database, events, risk, tests, agent, model, task).
  `FingerprintChange` (`correlation_contract.go:158`) computes a SHA-256 hash
  over the normalized (lowercased, trimmed, sorted) canonical form, so changes
  differing only in casing/whitespace/order fingerprint identically and any
  dimension difference fingerprints differently.

## Shared correlator — `internal/runtime/shared.go`
- `SharedCorrelator` (`shared.go:14`) is a single process-wide correlator
  injected into every consumer (incident, deployment, audit, learning) so they
  reason over the same source and window. `NewSharedCorrelator` wraps a
  `*Correlator`; `Correlator()` exposes it for DI; `DefaultSharedCorrelator`
  (`shared.go:49`) returns a singleton built once via `sync.Once`.

## Incident domain — `internal/incident/engine.go`
- `Engine` (`engine.go:39`) ingests and investigates incidents. `IngestAlert`
  (`engine.go:111`) sanitizes the alert (clamps severity, bounds future
  `OccurredAt`, caps service length) and creates an `Incident`.
- `Correlate` (`engine.go:159`) maps the alert to the affected service and
  gathers deployments/commits/runtime evidence, folding in the deep chain as
  additive evidence. `RootCause` (`engine.go:231`) derives typed hypotheses
  (`FACT`/`INFERENCE`/`HYPOTHESIS`) ranked deterministically.
- `ApplyAndVerifyFix` (`engine.go:274`) applies a fix and verifies it; `Resolve`
  (`engine.go:179`) closes the incident and writes an incident memory so the
  learning extractor can surface recurring patterns. `RequestApproval`/
  `Approve` route the fix through a human approval gate.
- `InjectRegression` (`engine.go:363`) injects a regression (used by drills).

## Consistency — `internal/consistency/engine.go`, `internal/domain/consistency.go`
- `Source` (`engine.go:19`) is any knowledge source (`GRAPH`, `TWIN`, `MEMORY`,
  `GIT`, `RUNTIME`, `ARCHITECTURE`, `TESTS`; `domain/consistency.go:12`) that
  reports a `Version()`, `UpdatedAt()`, and `Claim(subject)`.
- `Engine.Check` (`engine.go:75`) checks subjects across sources, classifying
  each as `domain.ConflictResult` — `NO_CONFLICT`, `CONFLICT`, `STALE`, `UNKNOWN`
  (`domain/consistency.go:29`). Stale sources (version mismatch or older than
  the freshness bound) are invalidated and excluded; the overall classification
  is the highest severity `CONFLICT > STALE > UNKNOWN > NO_CONFLICT`.
- `ConsistencyConflict.Explain()` (`domain/consistency.go:66`) renders a
  human-readable explanation naming the two disagreeing sources, their claims,
  which source is newer, and the next recommended check — deterministic.

## Dependencies
- `internal/domain` (Alert, Deployment, Commit, Event, Severity, Incident,
  ConflictResult), `internal/memory`, `internal/governance`, `internal/intelligence`
  (code graph for root cause), `internal/eventbus`, `internal/context`
  (invalidation markers).

## Storage / security
- Incidents persist via `incident/store.go` (JSON file store); runtime evidence
  is read from configured sources. `IngestAlert` sanitizes untrusted alert
  fields so a client cannot inject fabricated evidence or force correlation.

## Failure modes
- No runtime evidence → correlation is `UNKNOWN` overall (never fabricates).
- Alert with no service and no recent errors → service unresolved (empty).
- A stale source is invalidated and excluded, so conflicts are attributed to
  fresher sources with an explanation.

## Tests
- `internal/runtime/correlate_test.go` (folded in `runtime_test.go`),
  `chain_test.go`, `correlation_contract_test.go`, `shared_test.go`,
  `live_test.go`, `adapters_test.go`, `events.go` tests.
- `internal/incident/engine_test.go`, `regression_test.go`, `store_test.go`,
  `bus_test.go`, `mvp3_gate_test.go`.
- `internal/consistency/engine_test.go`, `internal/domain/runtime_test.go` and
  `domain_test.go` (conflict/explain).

## Performance / trade-offs
- Lookback-window evidence gathering is O(events in window); the chain build is
  O(deployments + commits + error events) and deduplicates. Fingerprints are
  cheap SHA-256 digests enabling O(1) change comparison/grouping. The trade-off:
  deterministic inference is explainable and fast but may label a link
  `INFERRED`/`UNKNOWN` where an LLM might guess — Kern keeps the guess typed and
  explicit rather than presenting it as fact.