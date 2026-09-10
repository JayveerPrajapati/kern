# ADR-0003: Qualified strings + repo scoping at the cross-repo boundary

- Status: Accepted (2026-09-08)
- Deciders: maintainer (ADR review with orchestrator)
- Scope: index schema, graph node IDs, twin merges, cross-repo impact

## Context

Symbols are identified by name-qualified strings (e.g. `Save`,
`TaskService.Run`, package-scoped node IDs like `internal/governance.Save`)
throughout the index, the intelligence graph, and every twin merge. Gap F2
(2026-09-08 audit) asked whether identity should become repo-scoped,
content-hashed, or registry-issued before cross-repo features harden:
migrating later means reindexing every repo and breaking every digest
that embeds symbol references.

## Decision

**Name-qualified strings remain the per-repo symbol identity.** No
registry-issued or content-hash identity is introduced at the index
schema level.

Cross-repo layers get repo scoping **only at their boundary**:

1. Within a repo, symbols keep their current qualified forms and
   package-scoped node IDs — no schema change, no reindex.
2. Cross-repo surfaces (intel multi-repo search, cross-repo impact,
   enterprise multi-project views) qualify references with an explicit
   repo prefix at the point where two repos meet, e.g.
   `repo:<root-or-alias>!pkg.Symbol`, and de-qualify back to the local
   form when delegating to a single-repo engine.
3. Content hashes stay where they already are — evidence bundles,
   fingerprints, freshness proofs — as integrity checks, not as identity.
4. Ambiguity within a repo is resolved by the existing rules (package
   scoping, caller-import linking per commit d114132), not by new IDs.

## Consequences

- No reindex-the-world migration; existing indexes and digests stay valid.
- Cross-repo correctness depends on the boundary prefix being applied
  consistently; the ADR makes that prefix part of the contract for any new
  cross-repo surface.
- Symbol renames remain name-based events (no ID stability guarantee
  across renames) — acceptable: kern already treats renames as structural
  change events.
