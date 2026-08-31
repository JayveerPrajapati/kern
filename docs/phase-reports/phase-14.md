# PHASE 14 — CROSS-ENGINE CONSISTENCY — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 14.1–14.4.
Go: 1.23, stdlib-only default build.

## Scope

Conflicting system knowledge is never silently collapsed into certainty.

## Micro-phase audit (all verified in source + tests)

- **14.1 (P0) Consistency model** — 7 knowledge sources (GRAPH, TWIN, MEMORY,
  GIT, RUNTIME, ARCHITECTURE, TESTS) in `domain.KnowledgeSource`.
- **14.2 (P0) Conflict result** — `ConflictResult`: NO_CONFLICT / CONFLICT /
  STALE / UNKNOWN; conflict → confidence downgraded (halved) + evidence
  requested + no certainty stated.
- **14.3 (P1) Stale detection** — version-mismatch staleness in
  `internal/consistency` + timestamp staleness (7d bound) in
  `internal/context`; stale attribution on conflicts.
- **14.4 (P2) Conflict explanation** — `ConsistencyConflict` carries
  Explanation ("why they conflict"), VersionA/B, SourceNewer, StaleSource.

## Gap found and closed

### G1 — The consistency engine was unwired (exit gate not enforced anywhere)

`CheckConsistency` (internal/context) and `internal/consistency.Engine` existed
with tests but had ZERO production callers — no analysis, plan, or risk flow
ever checked cross-source consistency, so conflicting claims could flow into
high-confidence recommendations unchanged.

**Fix (`internal/context/`):**
- New `ApplyConsistency(pkt *ContextPacket)`: runs `CheckConsistency` over the
  packet's claims; whenever the result is not a clean NO_CONFLICT it applies
  the confidence downgrades (never raising any claim's confidence) and
  attaches the report to the packet.
- `domain.ContextPacket` gains `Consistency *ConsistencyReport` (nil when
  consistent — backward compatible).
- `Engine.assemble` now calls `ApplyConsistency(&pkt)` after the freshness
  block, so every context packet produced for any change carries its
  consistency verdict.

## Exit gate

> "Conflicting system knowledge is never silently collapsed into certainty."
> — **MET and LOCKED**:
> - `TestApplyConsistencyDowngradesConflictingClaims` — a GRAPH claim
>   contradicting a MEMORY claim about the same subject downgrades both
>   confidences, attaches a CONFLICT report with an explanation (14.4).
> - `TestApplyConsistencyLeavesConsistentPacketUntouched` — agreeing claims
>   leave the packet nil-report (no spurious downgrades).

## Verification

- `go vet ./...`, `go build ./...`, `-tags treesitter`, `-tags sqlite` — PASS
- **90/90 packages pass, exit 0** (context + consistency + app + 87 rest)
- Existing context/consistency suites still PASS (assemble change is additive)