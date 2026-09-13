# Agent Note: Verify Command Mode Extraction and God Function Audit
Status: implemented

## Problem
runVerify (323 lines) dispatched six verification modes inline; kern_larges lists several god functions in the most-churned files.

## Decision
- Extracted verifySilentMode + verifyTokenReductionMode into named helpers; the switch cases now thin-dispatch. Verified behavior-preserving on all four paths (bare/symbol silent, token-reduction, both JSON variants).
- God-function audit (kern_larges + kern churn): remaining decomposition backlog, ordered by risk:
  1. runVerify remaining modes (eval/skill/scan + the verify-types form) — mechanical, same extraction pattern.
  2. parseFlags (267 lines, cmd/kern/flags.go) — flat switch; low value, mechanical.
  3. index.Update (235) + synthtest.Synthesize (227) — core logic; require test coverage BEFORE splitting.
  4. internal/mcp/server.go (2030 lines, risk 28.9 — most-churned x most-risky) — core MCP surface; needs a dedicated careful pass.
  5. Plugin kern.ts globFallback (2688 lines) — TS + 4-location sync; the highest-risk split.

## Consequence
- Rule: split the untested god functions only AFTER adding smoke coverage (the extraction above was safe only because each mode is a closed, live-verifiable path).
