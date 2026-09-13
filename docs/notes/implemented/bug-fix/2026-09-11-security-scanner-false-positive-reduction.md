# Agent Note: Security Scanner False Positive Reduction
Status: implemented

## Problem
`kern sec` flagged 165 hardcoded-secret findings on kern's own repo — almost all false positives, making the scanner unusable on the repo it ships with.

## Decision
- HEX: GitHub Actions SHA pins (`uses: owner/repo[/subpath]@<40-hex>`) are supply-chain pins, not secrets — skipped (isGhActionsShaPin). A 64-hex action input whose description line names a checksum (gitleaks tarball SHA-256) is also skipped (isDocumentedChecksum).
- VAULT: the `s.` alternative matched ANY 16+ char identifier (`s.getTaskForMutation(taskID)` → flag). Tightened to `s.` + hex-only (Vault dev-token shape) — the dominant ~100-finding family.

## Consequence
- 165 → 9 findings, all benign and audited: CI templates with ${SECRET} refs, doc/changelog examples, example.com fixtures, a Go type name, and a rule-ID string. No real secrets were hidden by the filters (tests pin the hvs./s.<hex> detections). Minor residual families (EMAIL on action refs, KEY on type names) documented as acceptable noise.
