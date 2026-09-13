# Structural Code Duplication Benchmark

This document details the benchmarking and evaluation methodology for Kern's
structural code duplication checks ([`internal/blueprint/checks/duplication/`](../internal/blueprint/checks/duplication/)).

---

## Architecture: Two-Pass Duplication Detection

Kern employs a two-pass detection model to balance AST efficiency with detection accuracy:

1. **Pass-1 (In-House Structural Triage):**
   * Computes normalized AST fingerprints (signature shape, control flow branching, literal counts, statement volume).
   * Operates 100% offline and in-memory with sub-millisecond latency.
   * Emits advisory candidate pairs when structural similarity exceeds the configured threshold.
2. **Pass-2 (Definitive Clone Verification):**
   * High-scoring candidates (>0.90) are verified via `jscpd` when available.
   * Escalates to a blocking gate violation only when both passes confirm exact or near-exact clone instances.

---

## Benchmark Evaluation

Evaluated against standard benchmark fixtures in [`docs/benchmarks/fixtures/`](benchmarks/fixtures/):

| Metric | Pass-1 (AST Triage @ 0.60) | Pass-1 + Pass-2 (Combined @ 0.85) |
|---|---|---|
| **Precision** | 0.50 | 0.96 |
| **Recall** | 0.88 | 0.91 |
| **False Positive Rate (FPR)** | 0.75 | 0.04 |
| **Execution Latency** | < 2 ms | ~ 85 ms |

### Policy Recommendation

Because Pass-1 triage alone has a high false positive rate at loose thresholds (0.60),
it is configured as **advisory/WARN-only** by default in Gate G6. Definitive blocking
is reserved for confirmed clones from the combined pipeline.
