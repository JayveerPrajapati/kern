# Agent Note: Silent Mode and Doctor Parity
Status: implemented

## Problem
The north-star verification tracker's medium/low findings remained open: [kern] markers visible in plugin output (NS-5), no reuse visibility in health (NS-6), and no staleness signal for installed binaries (NS-7).

## Decision
- NS-5: plugin honors KERN_SILENT=1 — the [kern] compressed marker and the [kern context · ...] envelope head are stripped (compression and body preserved).
- NS-6: kern_health index block reports reused_results (incremental prior-index reuse).
- NS-7: kern doctor gains a parity check — stamped binary build commit vs repo HEAD (ok/warn), with the stamping incantation reported for unstamped (dev) builds.

## Consequence
- KERN_SILENT=1 makes the plumbing invisible; health shows cold-start mitigation; doctor detects stale installs when binaries are stamped.
