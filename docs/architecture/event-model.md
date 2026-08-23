# Event Model — internal/eventbus

## Purpose
A deterministic, in-process publish/subscribe event bus carrying typed system
events. It is the async backbone that fans events out to webhooks, the audit
trail, and consumers, and it carries the Phase 4 reliability primitives
(idempotency, retry/dead-letter, persisted replay).

- Package: `internal/eventbus` (`eventbus.go`, `eventbus_test.go`).
- Stdlib-only (`sync`, `time`, `strconv`, `encoding/json`, `log`, `os`); no
  network I/O and no imports from other internal packages (`eventbus.go:1-17`).

## Envelope
`Event` (`eventbus.go:144-171`) is a single typed system event:

| Field | Meaning |
|---|---|
| `ID` | stable id; auto-generated as `e-<unixnano>-<atomic>` when empty on publish (collision-safe) |
| `Kind` | discriminates the class of event (`eventbus.go:25`) |
| `Source` | agent/service that emitted it |
| `Subject` | subject id (task, incident, …) |
| `Service` | optional affected service |
| `ProjectID`/`RepositoryID`/`TaskID`/`AgentID` | structured tracing fields |
| `Provenance` | e.g. `"loop"`, `"mcp"`, `"web"`, `"incident"` |
| `EventVersion` | schema version, defaults to `1` on publish |
| `EntityRefs` | related entity ids for correlation without parsing payload |
| `Payload` | optional structured payload (dropped from history if > 4 KiB) |
| `OccurredAt` | defaults to now when zero |

## Kind taxonomy (~62 constants)
Kinds follow a `domain.noun_verb` dotted naming (`eventbus.go:27-141`):

- `repository.*` — `repository.discovered`, `repository.indexed`, `graph.built`,
  `module.analyzed`, `symbol.discovered`.
- `task.*` — `task.created`, `task.started`, `task.updated`, `task.completed`,
  `task.failed`, `task.blocked`, `task.rejected`, `task.cancelled`,
  `task.approval_requested`, `task.approved`.
- `agent.*` — `agent.state_changed`, `agent.tool_called`, `agent.handoff`,
  `agent.error`, `agent.completed`, `agent.failed`.
- `policy.*` — `policy.evaluated`, `policy.blocked`.
- `approval.*` — `approval.requested`, `approval.granted`, `approval.rejected`.
- `verification.*` — `verification.started`, `verification.completed`,
  `verification.failed`.
- `pr.*` — `pr.created`, `pr.updated`, `pr.merged`, `pr.rejected`.
- `deployment.*` — `deployment.started`, `deployment.completed`,
  `deployment.failed`, `deployment.rolled_back`.
- `runtime.*` — `runtime.anomaly`, `observe.healthy`, `code.changed`.
- `incident.*` — `incident.created`, `incident.updated`, `incident.resolved`,
  `incident.investigated`, `root_cause.determined`, `fix.proposed`,
  `fix.approved`, `fix.verified`.
- `memory.*` — `memory.created`, `memory.recalled`.
- `risk.*` — `risk.calculated`.
- `architecture.*` — `architecture.violation`.
- `security.*` — `security.finding`.
- `learning.*` — `learning.recorded`, `learning.lesson_recorded`,
  `learning.pattern_surfaced`.
- misc — `context_packet.built`, `impact.computed`, `plan.produced`,
  `code.produced`, `test_run.completed`, `audit.recorded`.

## Publish / delivery
- `Publish(ev)` (`eventbus.go:275`) delivers to every active subscription
  matching the kind (empty kind = all) **asynchronously**, one goroutine per
  handler, so a slow/panicking subscriber never blocks the publisher.
- Defaults `ID`, `OccurredAt`, `EventVersion` when zero; dedups by `ID` before
  building the history entry; persists before delivery (crash-safe).
- `Subscribe(kind, h)` (`eventbus.go:225`) returns an idempotent unsubscribe
  func that swap-removes the subscription so the captured closure can be GC'd.
- `Flush()` (`eventbus.go:418`) blocks until all handler goroutines return
  (used by tests for deterministic async assertions).

## History
- Bounded in **count** (`max`, default 100 via `New()`) and in **bytes**: a
  payload larger than `maxHistoryPayloadSize` (4 KiB) is nulled in the retained
  history copy (`eventbus.go:22,256-265`). This keeps history bounded in both
  dimensions (Bug #19).
- `History(kind)` (`eventbus.go:424`) returns the stored events (empty = all),
  oldest first.

## Idempotency / dedup (P4.3)
- `EnableIdempotency(cap)` (`eventbus.go:455`) turns on de-duplication: an event
  whose `ID` was already published is dropped silently, so producers may retry
  publishing the same event without duplicate delivery.
- The seen-set is FIFO-evicted at `cap` (`rememberLocked`, `eventbus.go:377`).

## Retry / dead-letter (P4.4)
- `SetRetryPolicy(maxRetries, backoff)` (`eventbus.go:467`): a handler that
  panics is retried up to `maxRetries` times with the backoff between attempts.
  Exhaustion (or `maxRetries == 0`) routes the event to the dead-letter queue.
- `deliverWithRetry`/`runSafe` (`eventbus.go:343,363`) recover panics and log
  them rather than crashing the process.
- `SubscribeDeadLetter(h)` (`eventbus.go:483`) registers a handler receiving
  every dead-lettered event.

## Persistence + Replay (P4.5)
- `EnablePersistence(path)` (`eventbus.go:509`): every published event is
  appended as one JSON line (best-effort — a failure logs and does not fail
  Publish). Written before delivery so a crash can be replayed.
- `Replay(path)` (`eventbus.go:546`): reads the file and re-delivers every
  stored event via `Publish`. With idempotency enabled, replaying the same file
  twice is a no-op for the second pass.

## Storage
- In-memory history + in-process subscribers. Optional append-only JSONL
  persistence file for replay; no external broker or DB.

## Security
- Local only; no secrets transported in events. `Payload` is serialized for
  history and size-bounded.

## Failure modes
- Subscriber panics → recovered, retried, dead-lettered (never crash the bus).
- Persist append failure → logged, best-effort, does not block publish.
- Oversized payload → dropped from history, still delivered live.

## Tests
- `eventbus_test.go` covers publish/history, idempotency, retry/dead-letter,
  persistence+replay, unsubscribe idempotency, and panic recovery.

## Performance
- Asynchronous fan-out: publish returns immediately; handlers run in their own
  goroutines. Overhead is one goroutine per handler per event. Bounded history
  keeps memory constant regardless of event volume.

## Trade-offs
- In-process only: no cross-process/networked pub-sub today; durability is via
  the append-only JSONL file, not a broker. Event schemas are versioned
  (`EventVersion`) so consumers can handle evolution safely.