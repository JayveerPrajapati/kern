# Agent Note: Release Stamping and Cold Start Measurement
Status: implemented

## Problem
Two north-star remainders: the 605s cold-start figure was stale, and parity checks could never prove anything because default builds stamped "dev".

## Decision
- Cold start: measured at HEAD — a full in-memory index build is ~2.2s (kern orchestrate) and the incremental store path ~0.9s (kern index); the 605s review figure predates the parallel update-walk perf commit (412f9c0). The claim is obsolete, not a remaining cost.
- Parity: Makefile VERSION defaults to the HEAD short hash and stamps BOTH main.version and internal/version.Version; release builds stamp the tag. doctor checkParity accepts matching hashes and tag-shaped (vX.Y.Z) stamps; mismatches warn "stale".

## Consequence
- Every `make build` produces a parity-checkable binary; `kern doctor` proves binary-vs-repo parity at a glance; the cold-start claim in the tracker is corrected with measured evidence.
