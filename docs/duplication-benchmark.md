# Duplication Detector Benchmark & Advisory Posture (historical)

**Status:** [historical] — the benchmark suite and the two-pass blocking wiring it
measured were deleted in `a05c91c7` (P4 pivot, "delete the blueprint family");
the numbers below are dated records of that deleted suite.

> [!IMPORTANT]
> **This page is a historical record.** The measured suite lived in
> `internal/scanners/duplication/benchmark_test.go` and the two-pass policy
> wiring in the blueprint family (`internal/bppolicy`, `internal/bpcli`) —
> both deleted in `a05c91c7`. The **structural-similarity scoring core
> survives** as `internal/duplication` (fingerprint projection + weighted
> `Similarity` scoring), consumed by `internal/verification`'s reuse checks
> (`verify_reuse.go`) — but the advisory-vs-blocking two-pass posture below
> describes deleted code and must not be read as current behavior.

This document records the empirical measurements, confusion matrix, and design rationale for kern's AST duplication scanner (`internal/scanners/duplication`, deleted in `a05c91c7`).

---

## 1. Overview

Kern included an AST-level structural duplicate detector alongside `jscpd` token-based matching to identify redundant implementations across packages. Because AST structural similarity can flag idiomatically similar but functionally distinct algorithms as duplicates, kern enforced strict precision floors before any duplication finding could block CI. (The detector's scoring core — size-floor, signature/control-flow/called-symbol/size weighted similarity — survives in `internal/duplication`; the scanner CLI and policy wiring do not.)

---

## 2. Benchmark Confusion Matrix & Metrics (dated: deleted suite)

The benchmark suite (`internal/scanners/duplication/benchmark_test.go`) evaluated the detector against a calibrated fixture corpus consisting of:
- Exact duplicate functions
- Slightly-refactored functions (renamed variables / reshuffled statements)
- Structurally similar algorithms (distinct logic, shared control flow patterns)
- Idiomatic control flow boilerplate (HTTP handlers, error bubbling)

### Single-Pass AST Scan (Advisory-Only Baseline)

At the default exploratory threshold (0.60):
- **Precision**: `0.50`
- **Recall**: `1.00`
- **False Positive Rate (FPR)**: `0.75`

> [!IMPORTANT]
> **Advisory-Only Posture (as measured):** Because an exploratory single-pass AST scan at threshold 0.60 yields a 75% false-positive rate on structural boilerplate, the duplication leg **must remain advisory-only** (`severity: warning` or `info`). It cannot reject a change or fail a blueprint gate on its own.

### Calibrated High-Confidence Threshold (0.95)

At the calibrated 0.95 threshold combined with minimum AST node size filtering:
- **True Positives (TP)**: 2 (exact duplicate, slightly-refactored duplicate)
- **False Negatives (FN)**: 1 (heavily renamed/reordered variants)
- **False Positives (FP)**: 0 (structural false positives eliminated)
- **True Negatives (TN)**: 4
- **Precision**: `1.00`
- **Recall**: `0.67`
- **FPR**: `0.00`

---

## 3. Two-Pass Blocking Path (P1.1 Escalation; deleted with the blueprint family)

To enable hard blocking in CI without causing developer friction from false positives, kern implemented a **two-pass verification pipeline**:

1. **Candidate Pass (AST Level)**:
   The candidate must score `> 0.90` AST similarity and meet the `BlockEligible` criteria (sufficient AST depth and token volume).
2. **Confirmation Pass (`jscpd` Token Confirmer)**:
   The finding only escalates to `duplication:confirmed-block` if independent token-based analysis (`jscpd`) confirms the identical token sequences across both files.

If the token confirmer rejects the candidate, the finding remains advisory (`duplication:advisory`) and cannot block execution.

---

## 4. Code References (as of 2026-10-19)

- Detector scoring core (survives): [`internal/duplication`](../internal/duplication/) — fingerprint types + weighted `Similarity` scoring.
- Reuse-check consumer (survives): [`internal/verification/verify_reuse.go`](../internal/verification/verify_reuse.go).
- Deleted with `a05c91c7`: `internal/scanners/duplication/` (scanner + benchmark suite), the blueprint-family policy firewall (`internal/bppolicy`) and MCP finding reporting (`internal/bpcli`).