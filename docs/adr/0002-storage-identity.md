# ADR-0002: Per-repo `.kern/` is the canonical storage identity

- Status: Accepted (2026-09-08)
- Deciders: maintainer (ADR review with orchestrator)
- Scope: storage layout, evidence chains, future central/multi-project services

## Context

kern today stores every per-repo artifact — index, SQLite/JSON caches,
approvals, audit chain, evidence bundles — under `<repo>/.kern/`, keyed by
repository path, and those keys are embedded in tamper-evident evidence
bundle digests. Kern 2.0 planning (gap F1, 2026-09-08 audit) raised
whether identity should move to a central multi-project store before the
shared-index daemon (C5) or cross-repo work (C6) ship. Changing the key
after adoption breaks every existing evidence-bundle verification.

## Decision

**The per-repo `.kern/` directory is the canonical, authoritative storage
identity.** A process' identity key for a project is its repository root
path plus the content fingerprint of that root.

Central or multi-project stores — the enterprise control plane, a future
shared-index daemon, cross-repo search caches — may exist ONLY as
aggregates and caches. They are:

1. keyed by (repo root, fingerprint) — never by a self-issued ID,
2. rebuildable from the per-repo store (a wiped central store loses
   nothing authoritative),
3. never a source of identity: a central entry can be replaced by the
   repo's own `.kern/` state at any time.

Evidence-bundle verification continues to resolve keys through the
per-repo store, so existing chains stay valid.

## Consequences

- No migration is required; existing evidence chains remain verifiable.
- Cross-process concurrency within one repo is served by the SQLite WAL
  store (opt-in tag), which lives in the same `.kern/` identity.
- The future shared-index daemon must treat its cache as disposable and
  re-derive from per-repo state on any mismatch.
- Multi-project features (org memory, shared task visibility) store their
  own state OUTSIDE the per-repo identity (e.g. the enterprise store) and
  reference repos by (root, fingerprint), never the other way around.
