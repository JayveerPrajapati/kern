# Agent Note: Tests for Untested Hotspots
Status: implemented

## Problem
kern testgaps showed 74.1% reachable coverage with untested hotspots: web builders (buildMemory/buildTasks/buildApprovals, 4 callers each), graphService.index (7 callers), ShouldIgnore.

## Decision
- 6 new tests: web builders (empty + field-mapping paths for memory; empty tasks; nil-gate approvals), graphService index load-or-build + cancelled-context + end-to-end explore, ShouldIgnore exclusion contract.
- The approvals test exposed a REAL bug: buildApprovals panicked on a nil approvals store — added a nil guard in the builder.

## Consequence
- Hotspot coverage closed; one latent nil-panic fixed. Remaining known-untested: TreesitterEnabled stubs + mcpRoot (trivial, documented).
