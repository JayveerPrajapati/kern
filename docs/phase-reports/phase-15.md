# PHASE 15 — FRESHNESS + VERSION AWARENESS — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 15.1–15.5.
Go: 1.23, stdlib-only default build.

## Scope

Stale knowledge cannot be silently presented as current fact.

## Micro-phase audit (all verified in source + tests)

- **15.1 (P0) Metadata** — `FreshnessRecord` (created_at/observed_at/
  source_version/freshness), `ContextItem.Freshness`, `Evidence.Timestamp` all
  present.
- **15.2 (P0) State** — FRESH/AGING/STALE (`domain.Freshness`) +
  `FreshnessState` (FreshCurrent/FreshStale/FreshUnknown).
- **15.3 (P1) Context invalidation** — `Engine.gitDiff` surfaces a working-tree
  change as a GIT FACT in the packet; the P5.9 freshness policy drops stale
  items from the active-context selection.
- **15.4 (P1) Memory supersession** — `MemoryStore.Supersede` (current →
  superseded) + `MarkHistorical` + `CurrentMemories`.
- **15.5 (P2) Freshness in scoring** — `FreshnessAdjustedRisk` /
  `FreshnessAdjustedConfidence` (opt-in via `WithFreshnessScoring`, keeping v1
  output unchanged).

## Gap found and closed

### G1 — Recall silently returned superseded/historical memory (exit gate violated)

`MemoryStore.Recall` — the path every packet's memory comes from (context
engine assemble, incident engine, loop, platform, MCP, web) — did NOT filter by
status. Superseded/historical memories flowed into context packets as if they
were current, violating the exit gate. `CurrentMemories` existed as a separate
method but no producer used it.

**Fix (`internal/memory/query.go` + `store.go`):**
- `Query` gains `IncludeNonCurrent bool` — the default (false) now excludes
  superseded and historical memories, so Recall returns the authoritative
  current set. Audit-style consumers opt in explicitly.
- `MemoryStore.Add` now sets `Status = MemoryCurrent` explicitly (previously
  implicit empty), so every memory's 15.4 state is recorded.

## Exit gate

> "Stale knowledge cannot be silently presented as current fact." — **MET and
> LOCKED**:
> - `TestRecallExcludesSupersededMemory` — after `Supersede`, default Recall
>   returns only the current memory; `IncludeNonCurrent` retrieves the full
>   history for audit.
> - `TestMemoryCarriesFreshnessMetadata` — created_at + current status recorded
>   on every memory (15.1/15.4).

## Verification

- `go vet ./...`, `go build ./...`, `-tags treesitter`, `-tags sqlite` — PASS
- **90/90 packages pass, exit 0** (memory + context + app + 87 rest)
- Existing memory/context/app suites still PASS (the Add-status change is
  additive; no caller relied on recalling superseded memory)