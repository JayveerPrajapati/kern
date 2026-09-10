# ADR-0007: Session-scoped context ledger

- Status: Accepted (2026-09-09)
- Deciders: maintainer (ADR review with orchestrator)
- Scope: context economics, context_watch, plugin compression, .kern/
  storage layout

## Context

The C8 use case (2026-09-08 audit): *"kern tracks what it added to
context across turns and compacts proactively."*

Today kern can measure and compress, but remembers nothing:

- `kern_context_watch` (internal/mcp/handlers_context_watch.go) is
  pull-only: it analyzes whatever text is handed to it and suggests
  actions — no record of what kern itself contributed to a session
  across turns.
- The opencode plugin compresses tool output in place at emission time —
  reactive, not budget-aware.
- `tokenize.Count` and `context.Metrics` (internal/context/metrics.go)
  are the counting primitives; nothing persists their results.

There is no history: no record of which tool calls added how many tokens,
what compression released, or what the session's running total is.
Proactive compaction is therefore impossible — the analysis has to be
re-derived from a pasted blob instead of read from the actual sequence of
context events. ADR-0002 already fixed the storage identity: everything
session-scoped belongs under the repo's `.kern/`.

## Decision

**The context ledger is an append-only, session-scoped JSONL file under
`.kern/context/ledger-<session>.jsonl`, written by every kern surface
that adds or releases context.**

1. **Session identity.** An explicit session id from `KERN_SESSION_ID` or
   the host (MCP client / plugin session); process-scoped fallback =
   `<pid>-<start-time>`. One file per session; the file is created
   append-only on first write.

2. **Entry schema — metadata only, never content.** Each entry is a JSON
   line:
   `{seq, ts, session, kind, surface, tool, target,
   tokens_added, tokens_released, source_kind, budget_pct_after}`
   with kind ∈ `add|release|summarize|watch`, surface ∈
   `mcp|cli|plugin`, source_kind ∈ `code|log|prose|graph`. `target` is a
   path, command, or tool name — never code, never log text, never PII.
   The ledger is safe to keep and safe to inspect. `tokenize.Count` is
   the single counting primitive shared by all writers.

3. **Writers.** MCP tool responses (post-handler), CLI output
   (post-command), plugin compressions, and the `kern_optimize_*` /
   `kern_compact_file` surfaces append one entry per context-affecting
   event. Writers are idempotent and must never fail the caller: a ledger
   write error is logged and ignored.

4. **Read path.** `kern context ledger [--session X] [--summary] [--json]`
   lists or summarizes entries; `kern_context_watch` gains a ledger-aware
   mode — pass the session id and it fuses the ledger's running totals and
   top unreleased entries into the analysis instead of requiring a pasted
   blob.

5. **Proactive compaction policy — deterministic, no LLM.** A lightweight
   evaluator reads the session ledger and emits candidates when
   (a) cumulative unreleased tokens exceed a threshold (default 70% of
   the session budget, env-overridable) and (b) the top-N unreleased
   entries account for ≥ 40% of the total. Candidates are ordered by
   `tokens_releasable = tokens_added × factor(source_kind)` — log .60,
   code .50, prose .30 — the same factors context_watch already uses.
   The evaluator only RECOMMENDS (surfaced via CLI / MCP / plugin);
   compaction executes through the existing compress/reduce tools
   (`kern_optimize_log`, `kern_compact_file`, the plugin's in-place
   compression).

6. **Retention.** Entries are capped per session (10,000); the
   `.kern/context/` directory is GC'd under the same retention rules as
   flight task trails (C3). The ledger is disposable — deleting it loses
   history, never identity or state (per ADR-0002).

## Consequences

- `kern_context_watch` evolves from pull-only raw analysis to
  history-aware proactive advice: it can say *"this session added 240k
  tokens; these 6 entries are 61% of it — compress them"* from facts,
  not guesses.
- The plugin's compression becomes budget-aware instead of purely
  emission-time reactive.
- Three surfaces (MCP, CLI, plugin) must write entries — the plugin
  remains the 4-place sync discipline.
- Deterministic: the ledger stores facts; no LLM in the loop; estimates
  and factors are documented constants.
- Small footprint: metadata-only, one file per session, capped and
  disposable — no content retention, no PII amplification, no
  confidentiality burden.