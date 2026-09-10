# ADR-0006: Multi-repo identity — pinned, edge-aware cross-repo references

- Status: Accepted (2026-09-09)
- Deciders: maintainer (ADR review with orchestrator)
- Scope: repo registry schema, cross-repo search/impact, shared-index
  daemon, enterprise multi-project views

## Context

The C6 use case (2026-09-08 audit): *"I change this interface; kern tells
me three other repos break, at pinned commits."* Cross-repo surfaces
already exist:

- `RepoRegistry` (internal/intel/repos.go) persists registered projects in
  `~/.cache/kern/repos.json` — a per-user aggregate (allowed by ADR-0002,
  which keys central stores by (repo root, fingerprint) and requires them
  to be rebuildable from per-repo `.kern/` state).
- `SearchRepos` / `SemanticSearchRepos` pool ranked hits across registered
  repos; `CrossRepoImpact` (internal/intel/cross_repo.go) finds callers of
  a subject in every other registered repo.

Three gaps keep the story from being trustworthy (the C6 "missing"
column):

1. **No repo-scoped symbol identity.** `CrossRepoImpact` matches by bare
   or suffix-qualified name (`strings.HasSuffix(callee, "."+subject)`).
   A symbol in repo B that merely shares a name with the subject in repo A
   is reported as a caller — false positives with no way to distinguish
   them. ADR-0003 already decided the boundary contract: cross-repo
   references are repo-qualified (`repo:<root-or-alias>!pkg.Symbol`) and
   de-qualified when delegating to a single-repo engine — but no surface
   implements it yet.
2. **No repo dependency edges.** Every registered repo is treated as a
   peer. There is no notion that repo B depends on repo A, so impact
   cannot say *"these three repos depend on this interface"* vs *"some
   repo happens to contain a same-named symbol"*.
3. **No index commit pinning.** A cross-repo report is computed against
   whatever each repo's last-built index holds. Nothing records which
   commit an index reflects, so a stale index silently produces a report
   that cannot be reproduced or trusted. The user story demands "at
   pinned commits".

## Decision

**Cross-repo references become repo-scoped, edge-filtered, and
commit-pinned.** All three layers extend the existing registry —
no reindex of any repo, no change to per-repo identity (ADR-0003 stands).

1. **Repo-scoped symbol identity at the boundary.** Implement ADR-0003's
   qualification contract: `repo:alias!pkg.Symbol` is the canonical
   cross-repo reference. `CrossRepoImpact` resolves the subject against
   the home repo first (repo-qualified), then queries other repos only
   for the qualified form — never a bare-name suffix match alone. Name-
   only matches may remain as an explicit low-confidence tier, labeled
   "name-match (unverified)" in the report, never mixed with verified
   callers. Registry names are the alias (Add() already replaces on
   name); name uniqueness is the qualification contract — ambiguity falls
   back to the root path form.

2. **Repo dependency edges.** `repos.json` gains an optional directed
   edge set: `{from, to, kind, pinned}` with kind ∈
   `module|import|manifest|manual`. Edges are derived automatically at
   registry/index time (repo A's module path or import-path prefixes
   observed in repo B's index; go.mod `replace` directives; manifest
   deps when detectable) and overridable manually (`kern repos link`).
   Cross-repo impact reports along edges first — repo B is a *verified*
   affected repo only if an edge (or a repo-qualified reference) exists —
   then the name-match fallback tier for unlinked repos.

3. **Index commit pinning.** Each registry entry records
   `IndexedCommit` (HEAD SHA when the index was built) and `IndexedAt`.
   `kern repos status` compares live HEAD per repo and flags STALE.
   Every cross-repo report prints the pinned commits it was computed
   against; a stale repo's contributions are reported as stale, never
   silently dropped. A report is a claim about the pinned commits, and
   only about them.

## Consequences

- False positives shrink to an explicit, labeled tier — the report can
  now be read as *"verified breakage: 2 repos (pinned)"* + *"name-match
  only: 1 repo"*.
- Registry schema grows by optional fields only — existing registries
  keep working (missing pin = unknown, no edges = name-match tier only).
- Staleness becomes visible instead of silent; cross-repo claims become
  reproducible at pinned commits.
- ADR-0002 stays intact: the registry remains a rebuildable per-user
  aggregate; pinning records which commit a per-repo `.kern/` index
  reflects, and the index itself never moves.
- The shared-index daemon (C5) writes the same pin fields, keeping its
  cache verifiable against per-repo state.
- No reindex-the-world migration; per-repo digests and evidence chains
  are untouched.