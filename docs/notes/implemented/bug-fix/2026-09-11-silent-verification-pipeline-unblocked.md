# Agent Note: Silent Verification Pipeline Unblocked
Status: implemented

## Problem
The north-star verification tracker (NS-1, NS-2, both CRITICAL) showed the flagship "silent orchestrator" claim was DISPROVEN by kern's own verifier on its own repo: over-broad markers made every Go repo non-silent, and a socket-copy bug meant the inject/extract proof had never actually run.

## Decision
- NS-1: silentMarkers scoped from [".kern/", "kern_", "internal/", "MCP"] to [".kern/", "kern_"] — bare Go conventions (internal/ dirs, the MCP protocol name) are not kern plumbing and were matching every repo's own file paths.
- NS-2: copyTreeToTemp skips the whole .kern/ runtime dir (mirrors sandbox SkipDirs) and every non-regular entry (sockets/FIFOs/devices) — .kern/events.sock no longer kills the stage.

## Consequence
- Acceptance met: `kern verify --verify-silent NewServer` PASS; `--scan internal/lenses` 30/30 silent (was 0/30); `--verify-pipeline NewServer` injected=true extracted=true (was false/false). Two regression tests added. NS-3 (token-reduction unit mismatch, reduction=1.00) remains open.
