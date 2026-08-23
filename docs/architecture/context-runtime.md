# Context Runtime — internal/domain, internal/context

## Purpose
The context runtime builds and maintains the minimum-sufficient context that is
fed to a model. It classifies and state-tracks every context item, authorizes
each item against governance, deduplicates, selects a minimum sufficient subset,
runs GC over the retention window, pages warm/cold items, manages leases,
normalizes large tool outputs, applies freshness policies, and snapshots for
resume/replay. All of it is deterministic — nothing calls an LLM.

## Taxonomy — `internal/domain/context_runtime.go`
- `ContextClass` (15 classes, `context_runtime.go:9-25`): `USER_INTENT`,
  `TASK_STATE`, `FACT`, `CONSTRAINT`, `DECISION`, `PLAN`, `EVIDENCE`,
  `SOURCE_CODE`, `TOOL_RESULT`, `ERROR`, `TEST_RESULT`, `MEMORY`, `HISTORY`,
  `HYPOTHESIS`, `ARTIFACT`.
- `ContextState` (5 states, `context_runtime.go:32-38`): `ACTIVE` (in model
  context), `WARM` (paging in), `COLD` (available), `ARCHIVED` (compact ref
  kept), `DROPPED` (removed).
- `ContextItem` (`context_runtime.go:44-60`) tags each item with class/state/
  relevance/freshness/digest/`LastUsed` and per-item `Authorized`/`DenyReason`.
- `GCAction` (`context_runtime.go:66-72`): `KEEP`/`COMPRESS`/`DEMOTE`/`ARCHIVE`/
  `DROP`.
- Supporting types: `ContextSnapshot`, `ToolResultSummary`, `ContextPage`,
  `ContextLease`, `FreshnessPolicy`/`Freshness`/`ClassifyAge`,
  `ContextReplay`.

## Per-item authorization (P5.4) — `internal/context/phase5.go`
- `AuthorizeItems` (`phase5.go:41`) asks the firewall whether the holder may
  `read` the item's resource. Sensitive classes (`SOURCE_CODE`, `FACT`,
  `EVIDENCE`) gate on `"source"`; everything else gates on `"context"`
  (`resourceForItem`, `phase5.go:28-35`). Denied items are filtered out with
  `Authorized=false`/`DenyReason` set; a nil firewall authorizes everything.

## Dedup (P5.8)
- `DedupItems` (`phase5.go:66`) keeps the first occurrence of each content
  digest and drops later duplicates. Runs before GC so GC scores only unique
  items.

## Minimum-sufficient selection (P5.3) — `internal/context/select.go`
- `SelectContext` (`select.go:115`) is the unified engine. Order of operations:
  1. Authorize first (`AuthorizeItems`) — denied items never enter the pool.
  2. Apply freshness policy — stale items dropped from active set.
  3. Classify each item into a selection stage; note protected items
     (`CONSTRAINT` + `EVIDENCE` always retained, `isProtected` `select.go:103`).
  4. Rank by selection stage asc, tie-broken by relevance desc.
  5. Reduce to minimum sufficient, keeping constraints + evidence.
- Selection stage order (`selectionStage`, `select.go:67-90`): intent → task
  state → direct dependencies → required constraints → relevant memory →
  relevant tests → historical evidence → runtime evidence → default.
- `SelectMinimal` (`phase5.go:222`) picks the smallest subset whose combined
  relevance reaches `threshold` or `maxItems`, whichever comes first.

## GC pipeline (P5.1/P5.5) — `internal/context/gc.go`
- `GC` scores each item and assigns a `GCAction`. Scoring factors
  (`gc.go:15-24`): task relevance, freshness, authority (graph > memory > tool >
  history), duplicate relationship, last use, `dependency_distance` (hop count
  from the task target), and `task_relation`.
- `NewGC(intent, target, maxItems)`; `SetDependencyDistance` /
  `SetTaskRelation` register the completeness signals; `Run` sorts by score,
  keeps the top `maxItems` as `ACTIVE` (`KEEP`/`COMPRESS`), demotes the rest
  (`DEMOTE`/`ARCHIVE`/`DROP` by score).
- `lastUsePenalty` (`phase5.go:249`) scales down long-unused items.

## Normalization (P5.1) — `internal/context/normalizer.go`
- `NormalizeToolResult` (`normalizer.go:20`) turns large raw tool output into a
  compact `ToolResultSummary`, storing the raw output outside active model
  context (as an artifact reference). Heuristics: errors by keywords
  (`error`/`fail`/`panic`/`fatal`), facts by `=`/`:=`/`type `/`func `, evidence
  by `file:line` refs, references by file extensions, and a summary of the first
  N non-empty lines. `TokenSaved` = raw − summary tokens (approx).

## Freshness (P5.9, Phase 15) — `freshness.go`, `phase5.go`
- `ApplyFreshnessPolicy` (`phase5.go:269`) drops items older than `MaxAge` and
  returns per-item `FRESH`/`AGING`/`STALE` classes.
- Phase 15 adds `InvalidationMarker` / `NewInvalidationMarker` (`freshness.go:27`)
  for invalidating stale context, and `EvidenceFreshness` /
  `RiskFreshnessMultiplier` (`freshness.go:89,98`) to discount stale evidence.

## Paging (P5.10) & leases (P5.11) — `phase5.go`
- `PageItems` (`phase5.go:84`) returns a 1-indexed `ContextPage` with
  page metadata.
- `LeaseManager` (`phase5.go:119`) reserves an item for a bounded duration:
  `Acquire`/`Active`/`Renew`/`Release`/`Expired`/`Len`, so the GC won't evict an
  in-use item.

## Snapshot + Replay (P5.12) — `snapshot.go`, `phase5.go`
- `Snapshot(pkt, taskState, nextAction)` (`snapshot.go:10`) builds a compact
  `ContextSnapshot` from a `ContextPacket` (goal/decisions/constraints/files/
  risks/tests/next action).
- `Replay(r)` (`phase5.go:187`) reconstructs a minimal `ContextPacket` from a
  persisted `ContextReplay` without re-running the retrieval pipeline.

## Dependencies
- `internal/domain` (types), `internal/governance/firewall` (authorization),
  and the graph/memory/tool sources that assemble the candidate item pool.

## Storage / security
- In-memory item pools + persisted snapshots/replay records. Every item is
  authorization-gated per-holder before entering the active set; denied items
  are excluded (no cross-task leakage).

## Failure modes
- A nil firewall degrades to allow-all (backward compatible).
- Items without digest are never deduped (cannot prove duplicates).
- Unknown last-use is not penalized (avoid surprise evictions).

## Tests
- `context/phase5_test.go`, `select_test.go`, `gc_test.go`, `freshness_test.go`,
  `consistency_test.go`, `engine_test.go`, `runtime_test.go`, `risk_test.go`,
  `arch_runtime_test.go`.

## Performance / trade-offs
- Deterministic heuristics (no LLM) keep selection/GC fast and reproducible.
- Trade-off: heuristic normalization/selection may drop nuance vs. a model, but
  is predictable, cheap, and authorization-correct.