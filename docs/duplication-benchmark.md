# Duplication Detector Benchmark & Advisory Posture

This document records the empirical measurements, confusion matrix, and design rationale for kern's AST duplication scanner (`internal/scanners/duplication`).

---

## 1. Overview

Kern includes an AST-level structural duplicate detector alongside `jscpd` token-based matching to identify redundant implementations across packages. Because AST structural similarity can flag idiomatically similar but functionally distinct algorithms as duplicates, kern enforces strict precision floors before any duplication finding can block CI.

---

## 2. Benchmark Confusion Matrix & Metrics

The benchmark suite (`internal/scanners/duplication/benchmark_test.go`) evaluates the detector against a calibrated fixture corpus consisting of:
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
> **Advisory-Only Posture:** Because an exploratory single-pass AST scan at threshold 0.60 yields a 75% false-positive rate on structural boilerplate, the duplication leg **must remain advisory-only** (`severity: warning` or `info`). It cannot reject a change or fail a blueprint gate on its own.

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

## 3. Two-Pass Blocking Path (P1.1 Escalation)

To enable hard blocking in CI without causing developer friction from false positives, kern implements a **two-pass verification pipeline**:

1. **Candidate Pass (AST Level)**:
   The candidate must score `> 0.90` AST similarity and meet the `BlockEligible` criteria (sufficient AST depth and token volume).
2. **Confirmation Pass (`jscpd` Token Confirmer)**:
   The finding only escalates to `duplication:confirmed-block` if independent token-based analysis (`jscpd`) confirms the identical token sequences across both files.

If the token confirmer rejects the candidate, the finding remains advisory (`duplication:advisory`) and cannot block execution.

---

## 4. Code References

- Detector implementation: [`internal/scanners/duplication/check.go`](../internal/scanners/duplication/check.go)
- Oracle benchmark suite: [`internal/scanners/duplication/benchmark_test.go`](../internal/scanners/duplication/benchmark_test.go)
- Policy firewall rules: [`internal/bppolicy/policy/loader.go`](../internal/bppolicy/policy/loader.go)
- MCP finding reporting: [`internal/bpcli/mcp/handlers.go`](../internal/bpcli/mcp/handlers.go)
