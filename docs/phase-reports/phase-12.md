# PHASE 12 — WHAT-IF + MODERNIZATION — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 12.1–12.4.
Go: 1.23, stdlib-only default build.

## Scope

Make what-if and modernization workflows Task/Artifact/Evidence aware.

## Micro-phase audit

- **12.1 (P0) What-if** — `TaskService.WhatIf` + `whatif.Impact` cover all 7
  output dimensions: Facts, Impact (Affected/Files/Services/Tests), Architecture
  (ArchitectureViolations), Memory (HistoricalEvidence), Risk,
  Confidence (0..1), Limitations (Summary). Records impact_report +
  risk_report artifacts.
- **12.2 (P1) Modernization analysis** — `modernization.Analyzer` wraps the
  intel layer (communities → bounded contexts, bridges, churn → phased
  extraction ordered by risk) — deterministic, no LLM.
- **12.3 (P1) Phases → Tasks** — `ModernizePhaseTasks` materializes each
  extraction phase as its own Task (Task Group → Tasks → Plan → Risk →
  Validation), linked by ParentID + architecture artifact.
- **12.4 (P2) Visualization** — web console architecture/agents views +
  graphviz/graphml exports (existing; P2-partial, unchanged).

## Gaps found and closed

### G1 — Production Modernize never materialized phase tasks (12.3 unwired)

`ModernizePhaseTasks` existed with tests but had ZERO production callers:
`kern modernize` / `kern_modernize` / web modernize completed the plan task and
stopped — the per-phase Tasks existed only in tests, so modernization was not
Task-aware in the real flow.

**Fix (`internal/app/task.go`):** `Modernize()` now calls
`ModernizePhaseTasks(plan, t.ID)` after completing the plan task, recording a
"materialized N phase tasks" step. Phase tasks link back to the plan task
(ParentID) and carry architecture artifacts.

### G2 — WhatIf never attached its typed claims as task evidence

The simulation produced `Impact.Claims` (FACT/INFERENCE/HYPOTHESIS/
RECOMMENDATION) but the what-if task's Evidence stayed empty — the impact
estimate was artifacts + text only, not evidence-aware.

**Fix (`internal/app/task.go`):** `TaskService.WhatIf` appends `imp.Claims` to
`t.Evidence` before completing, so the simulation's typed evidence trail is
persisted on the task.

## Exit gate

> "What-if and modernization workflows are Task/Artifact/Evidence aware."
> — **MET and LOCKED**:
> - What-if: a Task with impact_report + risk_report artifacts AND typed
>   evidence claims (`TestWhatIfIsEvidenceAware`).
> - Modernization: a plan Task plus one Task per extraction phase, linked and
>   artifacted (`TestModernizeMaterializesPhaseTasks` — verified against the
>   full kern repo index: all N phases materialized, each with an
>   architecture artifact).

## Verification

- `go vet ./...`, `go build ./...`, `-tags treesitter`, `-tags sqlite` — PASS
- **90/90 packages pass, exit 0** (app incl. the two new tests; Modernize e2e
  27.7s)
- Existing what-if/modernize suites still PASS
- MCP/CLI/web modernize paths unaffected (they route through `ts.Modernize`,
  which now also materializes phase tasks — additive)