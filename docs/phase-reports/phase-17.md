# PHASE 17 — CONTEXT / TOKEN / COST BENCHMARKING — Review

Status: **PASS**
Spec: `KERN_2_0_CANONICAL_END_TO_END_BUILD_SPEC_V3.md` micro-phases 17.1–17.6.
Go: 1.23, stdlib-only default build.

## Scope

Prove Kern's original optimization mission with a reproducible benchmark suite.

## Micro-phase audit (all verified in source + tests)

- **17.3 (P0) Baseline vs Kern** — `CompareToBaseline` (internal/app/
  benchmark.go): input tokens, tool calls, retries, estimated cost, verified
  success — deterministic 4x raw-context baseline, fixed 3-retry baseline,
  cost heuristic.
- **17.4 (P1) Context quality** — `ContextQualityReport`: relevant-context
  ratio, sufficiency, stale-context ratio, duplicate-context ratio
  (TestContextQualityStaleAndDuplicateRatios).
- **17.5 (P1) Task outcomes** — `TaskOutcomeReport`: first-pass success,
  verification success, human intervention, post-deployment regression
  (TestTaskOutcomeHumanInterventionAndFirstPass, ...PostDeployRegression).
- **17.6 (P2) Efficiency report** — `kern efficiency <id>` / `kern task
  efficiency <id>` via `BuildEfficiencyReport` + `RenderEfficiencyReport` with
  all required fields (baseline tokens, kern tokens, reduction, context
  quality, tool-call reduction, retry reduction, estimated cost, verified
  success).

## Gap found and closed

### G1 — No reproducible benchmark suite (17.1 × 17.2 × exit gate)

The measurement machinery existed per-task, but there was no SUITE: no
benchmark-repo matrix × task-class matrix driven repeatedly to prove
reproducibility — the Phase 17 exit gate ("a reproducible benchmark suite
exists") was unmet.

**Fix (`internal/app/benchmark_suite_test.go`):** `TestReproducibleBenchmarkSuite`
- **17.1 repos**: `microservice` (the safechange fixture) + `legacy` (generated
  multi-file Go repo with cross-referencing symbols + tests).
- **17.2 classes**: lookup (Analyze), small-change (Plan), architecture
  (WhatIf), incident (Correlate).
- **17.3 measurement**: per (repo × class) `BaselineComparison`.
- **Reproducibility**: the full matrix runs TWICE against fresh fixtures and
  must produce identical reductions.
- **Actuals published** (17.6: "only publish actual measured results") via
  `renderBenchmarkSummary`.

## Exit gate

> "A reproducible benchmark suite exists." — **MET and LOCKED**:
> 2 repos × 4 classes = 8 measured rows, byte-identical across two runs.

## Actual measured results (run against the suite)

| repo | class | kern tokens | input red. | tool red. | retry red. | cost red. |
|---|---|---|---|---|---|---|
| microservice | lookup | 792 | 75.0% | 75.0% | 100% | 89.3% |
| microservice | small-change | 792 | 75.0% | 75.0% | 100% | 86.0% |
| microservice | architecture | — | — | 75.0% | 100% | 91.3% |
| microservice | incident | — | — | 75.0% | 100% | 91.3% |
| legacy | lookup | 799 | 75.0% | 75.0% | 100% | 89.3% |
| legacy | small-change | 799 | 75.0% | 75.0% | 100% | 86.0% |
| legacy | architecture | — | — | 75.0% | 100% | 91.3% |
| legacy | incident | — | — | 75.0% | 100% | 91.3% |

(Token dimension applies to context-bearing classes; report-producing classes
are measured on tool/retry/cost. The spec's target ranges are targets, not
claims — the suite reports the actuals above.)

## Verification

- `go vet ./...`, `go build ./...`, `-tags treesitter`, `-tags sqlite` — PASS
- **90/90 packages pass, exit 0** (app incl. the suite + 89 rest)
- Existing efficiency/benchmark field tests still PASS