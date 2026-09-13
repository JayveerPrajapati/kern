# Agent Note: Token Reduction Proof Shows Real Compression
Status: implemented

## Problem
`kern verify --verify-token-reduction` proved destruction, not compression (NS-3): score 25, reduction 1.00, retention 0.50 — `budget.Fit` is line-oriented and collapsed the dense packet render down to its first line.

## Decision
- New `budget.FitProportional(text, maxTokens)`: keeps the leading budget-proportional portion with a rune-safe ratio loop guaranteeing the ceiling — the head-preserving counterpart to the log fitter.
- `VerifyTokenReduction` uses FitProportional; the header (symbol + file, both critical fragments) survives the proportional cut.

## Consequence
- Acceptance met on NewServer: score 100.00, retention 1.00, reduction 0.50 ∈ [0.3, 0.8]. Three FitProportional tests added (proportional head, small input, dense single line).
