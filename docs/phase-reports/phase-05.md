# PHASE 5 — CONTEXT RUNTIME — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 5.1–5.12.
Go: 1.23, stdlib-only default build (tree-sitter + sqlite behind build tags).

## Scope

Transform context optimization into an intelligent context lifecycle:
taxonomy, lifecycle states, minimum-sufficient selection, authorization, GC,
tool-output normalization, snapshots, dedup, freshness, paging, leases, and
replay (exit gate: the same engineering task demonstrates less irrelevant
context, less duplicate output, critical constraints retained, required
evidence retained, no unauthorized context, no correctness regression).

## Work completed (micro-phases)

### 5.1 — Context taxonomy (P0)

Verified all 15 spec classes exist as `domain.ContextClass` in
`context_runtime.go`: USER_INTENT, TASK_STATE, FACT, CONSTRAINT, DECISION,
PLAN, EVIDENCE, SOURCE_CODE, TOOL_RESULT, ERROR, TEST_RESULT, MEMORY, HISTORY,
HYPOTHESIS, ARTIFACT. `domain.ContextItem` carries class + state + relevance +
freshness + digest + scoped authorization fields + EvidenceRefs.

### 5.2 — Context lifecycle state (P0)

Verified all 5 states (`domain.ContextState`): ACTIVE, WARM, COLD, ARCHIVED,
DROPPED.

### 5.3 — Minimum sufficient context (P0)

Verified `SelectContext` (`select.go`) implements the documented selection
order: permissions FIRST, then selection stages (intent → target → direct
deps → constraints → memory → tests → historical evidence → runtime evidence),
ranked by relevance, reduced to the minimum sufficient subset, keeping
constraints + evidence (protected) under aggressive reduction. Tests:
`TestSelectContextAppliesSelectionOrder`, `TestSelectContextPermissionsFilter`,
`TestSelectContextKeepsConstraintsUnderAggressiveReduction`,
`TestSelectMinSufficientReduction`.

### 5.4 — Context authorization (P0)

Verified `AuthorizeItemsScoped` (`phase5.go`) checks all 5 dimensions before
adding any item: agent (firewall), repository, task, tenant/team, security
classification — fail-closed with DenyReason. `SelectContext` runs it first so
denied items never enter the pool. Tests:
`TestAuthorizeItemsScopedAllDimensions`, `TestSelectContextUsesScopedAuth`.

### 5.5 — Context GC (P1)

Verified `GC` (`gc.go`) scores all 7 factors (relevance, freshness, authority,
last_used, dependency_distance, task_relation, duplicate_score) and emits one
of KEEP/COMPRESS/DEMOTE/ARCHIVE/DROP. Tests: `TestGCScoresAndActions`,
`TestGCDuplicatePenalty`, `TestGCDependencyDistance`, `TestGCTaskRelation`,
`TestGCUsesLastUsed`.

### 5.6 — Tool output normalization (P1)

`NormalizeToolResult` (`normalizer.go`) converts large raw output
(log/JSON/grep/test) into a compact ToolResultSummary (summary/facts/errors/
evidence/references/artifact_id + token_saved); raw stays outside active
context.

- **Closed a gap**: the normalizer had ZERO production callers. Wired it into
  the workflow engine (`agent/workflow.go`): step outputs over 4 KiB are
  normalized in the step history (facts/errors retained, `[normalized N → M
  chars]` marker) while the raw output remains on `task.Output` and in the
  artifact chain. Small outputs are stored verbatim. New test:
  `TestLargeStepOutputNormalized` (agent).

### 5.7 — Context snapshots (P1)

Verified `ContextSnapshot` (`domain.ContextSnapshot`): goal/state/decisions/
constraints/files/tests/risks/next_action; `Task.Snapshot()` +
`SnapshotStore.Record` persist compact task context. Tests:
`TestTaskSnapshotMethod`, `TestSnapshotRecordsRichFields`.

### 5.8 — Context deduplication (P1)

Verified `DedupItems` / `CanonicalizeItems` (`phase5.go`): one canonical fact
+ many evidence references (digest-based collapse, EvidenceRefs populated).
Tests: `TestDedupItems`, `TestCanonicalizeItemsEvidenceRefs`.

### 5.9 — Context freshness (P2)

Verified `ApplyFreshnessPolicy` + `ClassifyAge` + invalidation markers
(`freshness.go`); `SelectContext` applies it before ranking. Tests:
`TestApplyFreshnessPolicy`, `TestClassifyAge`, `TestEvidenceFreshness`,
`TestRiskFreshnessMultiplier`, `TestInvalidationMarker`,
`TestFreshnessAdjustedEvidenceConfidence`, `TestFreshnessAdjustedRisk`.

### 5.10 — Context paging (P2)

Verified `PageItems` + `ContextPage` (page slice + metadata). Test:
`TestPageItems`.

### 5.11 — Context leases (P2)

Verified `LeaseManager` + `ContextLease` (TTL/step expiry). Test:
`TestLeaseManagerLifecycle`.

### 5.12 — Context replay (P2)

Verified `ReplayPacket`. Test: `TestReplay`.

### Phase 5 exit gate

- **Added the closure test** `TestPhase5ExitGate` (`phase5_exit_gate_test.go`):
  drives ONE engineering task ("add tenant-aware caching" → tenant_cache)
  through the full pipeline (authorize → dedup → freshness → select → GC) and
  asserts all six exit-gate properties on the same input: irrelevant noise
  dropped, duplicates collapsed, critical constraint retained, required
  evidence retained, secret-class items denied, and the target's own facts
  still selected (no correctness regression).

## Tests

- `go vet ./...` — PASS; `go build ./...` — PASS; `-tags treesitter`,
  `-tags sqlite` — PASS
- `go test ./internal/context/` — PASS (incl. new `TestPhase5ExitGate`)
- `go test ./internal/agent/` — PASS (incl. new `TestLargeStepOutputNormalized`)
- `go test ./internal/app/` — PASS (100s; no ripple from the agent→context wiring)
- Remaining 87 packages — PASS, exit 0
- `go test -race ./internal/context/ ./internal/agent/` — PASS

## Exit gate

> The same engineering task demonstrates: less irrelevant context, less
> duplicate output, critical constraints retained, required evidence retained,
> no unauthorized context, no correctness regression. — MET and LOCKED by
> `TestPhase5ExitGate`.

## Notes / non-changes

- All P0 micro-phases (5.1–5.4) already existed and were verified; the P1/P2
  features (5.5–5.12) already existed with tests. The two genuine gaps closed:
  P5.6 normalizer wiring (dead code → live in the workflow engine) and the
  exit-gate closure test.
- Wiring the normalizer adds an `agent → context` import; verified no cycle
  (context and its deps never import agent). Behavior change is bounded:
  only step outputs over 4 KiB are normalized, raw output stays on the task,
  and no test asserted raw step-result content.