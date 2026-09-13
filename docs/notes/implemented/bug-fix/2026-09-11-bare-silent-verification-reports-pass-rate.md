# Agent Note: Bare Silent Verification Reports Pass Rate
Status: implemented

## Problem
`kern verify --verify-silent` with no symbol was a guaranteed FAIL ("symbol not found in graph") — a default invocation that could never succeed (NS-4).

## Decision
- Bare `--verify-silent` now falls back to `ScanSilent(root, "internal", 100)`: a deterministic sample of core symbols, reporting the pass rate instead of failing.
- JSON mode mirrors the report shape; advisory exit 0 preserved.

## Consequence
- Acceptance met: bare `kern verify --verify-silent` → "100/100 symbols silent (100%)" on kern's own repo; a target symbol still runs the single-symbol check.
