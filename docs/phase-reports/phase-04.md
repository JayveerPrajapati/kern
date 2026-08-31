# PHASE 4 — EVENT BACKBONE — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 4.1–4.5.
Go: 1.23, stdlib-only default build (tree-sitter + sqlite behind build tags).

## Scope

Standardize asynchronous communication: a typed event envelope, a canonical
event-name taxonomy, idempotent delivery, retry/dead-letter for failed
consumers, and safe replay (exit gate: "task lifecycle is event-observable
and retry-safe").

## Work completed (micro-phases)

### 4.1 — Event envelope (P0)

Verified `eventbus.Event` carries all required fields: event_id (`ID`, stable
or auto-generated), event_type (`Kind`), event_version (`EventVersion`,
defaults to 1), occurred_at (`OccurredAt`, defaults to now), source
(`Source`), project_id / repository_id / task_id / agent_id (structured
tracing fields), entity_refs, payload (any), provenance.

### 4.2 — Event names (P0)

Verified the taxonomy covers all 13 required domains with typed `Kind`
constants: task.* (11 kinds: created/started/updated/state_changed/completed/
failed/blocked/rejected/cancelled/approval_requested/approved), agent.* (7),
policy.* (2), approval.* (3), verification.* (3), pr.* (4), deployment.* (4),
runtime.* (1), incident.* (4), memory.* (3), risk.* (1), architecture.* (1),
security.* (1). `TestEventKindsAreDistinct` asserts uniqueness.

### 4.3 — Idempotency (P0)

- Bus level (existing): `EnableIdempotency(cap)` dedups on non-empty event
  IDs with FIFO-evicted bounded set (`TestIdempotencyDedupOnPublish`,
  `TestIdempotencyCapEvictsOldest`).
- **Closed a gap**: the bus's idempotency was never exercised by the primary
  producer — `TaskService.publish` published with an empty ID, so every event
  was auto-generated and duplicate deliveries would have duplicated side
  effects. Now every app event carries a deterministic content-addressed ID
  (`stableEventID`: SHA-256 over kind|subject|canonical payload), so a
  retried/duplicated publish of an identical event is a no-op while distinct
  state changes (different payload) still flow. Go's json.Marshal sorts map
  keys, so the payload serialization is canonical.
- New test: `TestPublishIdempotentAtAppLayer` — asserts every app event has a
  stable ID, duplicate re-publish delivers 0 times and does not grow history,
  and a distinct event (recomputed ID) still flows.

### 4.4 — Retry/dead-letter (P1)

Verified (existing): `SetRetryPolicy(max, backoff)` retries panicking
handlers up to max with backoff, then routes to the dead-letter queue via
`SubscribeDeadLetter` (observable + recoverable).
`TestRetryThenDeadLetter`, `TestNoRetryDeadLettersImmediately`. Handler
panics are recovered so a bad subscriber can't crash the process.

### 4.5 — Event replay (P2)

Verified (existing): `EnablePersistence(path)` appends every event as a JSON
line; `Replay(path)` re-delivers stored events, and with idempotency enabled
replaying the same file twice is a no-op. `TestPersistedReplay`.

## Tests

- `go vet ./...` — PASS; `go build ./...` — PASS; `-tags treesitter`,
  `-tags sqlite` — PASS
- `go test ./internal/eventbus/` — PASS (15 tests, all micro-phases)
- `go test ./internal/app/` — PASS (90s, incl. new `TestPublishIdempotentAtAppLayer`)
- Remaining 88 packages — PASS, exit 0
- `go test -race ./internal/app/ -run 'PublishIdempotent|CancelPublishes'` — PASS

## Exit gate

> "Task lifecycle is event-observable and retry-safe." — MET. Every
> TaskService lifecycle operation publishes a typed event: Create →
> task.created, Start → task.started/task.updated, Analyze/Plan/Impact/etc →
> task.updated with stage, Complete → task.completed, Fail/Timeout →
> task.failed, Cancel → task.updated (cancelled), Retry → task.updated,
> Resume → task.updated, Pause/HumanTakeover → task.blocked, Rollback →
> task.updated; approval gates publish task.approval_requested/approved.
> Events are idempotent (content-addressed IDs) and delivered retry-safe
> (retry + dead-letter), and the stream persists for replay.

## Notes / non-changes

- Only the TaskService publisher was made idempotent. Other publishers (web
  approval handlers, loop, agent registry/workflow) still emit empty-ID events
  that are auto-generated — they were not producing duplicates and the bus
  machinery covers them identically if they adopt stable IDs later. The
  `e-<hex>` (content-addressed) and `e-<ts>-<counter>` (auto) ID formats are
  disjoint, so no collision is possible.