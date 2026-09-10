# Progressive Disclosure — internal/retrieval
Progressive disclosure is the L1/L2/L3 retrieval protocol: a request is
answered with the *least* context that suffices (L1: names and costs), and
escalates only when the consumer needs more (L2: neighborhood, L3: source).
Everything is deterministic, content-hash-validated, and token-budget aware.

## Handles — `internal/retrieval/handle.go`
- `Handle` (`handle.go:41`): `{ID, Type, Name, Source, Line, TokenCost,
  Confidence, ContentHash}` — an opaque reference to a retrievable item.
  `ContentHash` is the staleness gate: a handle whose content changed is
  rejected rather than silently serving stale context.
- `Registry` (`handle.go:61`): `Register`, `Resolve(id) (*Handle, bool)`
  (ID-only lookup), `Invalidate(id)`, `Len`. Resolved handles must be
  validated against the current content hash before use.

## Levels — `internal/retrieval/levels.go`
- `retrieve.Retrieve(ix *index.Index, opts Options)` with
  `Options{Level, Query | Symbol}`:
  - **L1** via `Query` — index-level summary: names, types, token costs,
    confidence (ranked search result set).
  - **L2** via `Symbol` — neighborhood: callers, callees, tests, git context
    for the resolved symbol.
  - **L3** via `Symbol` — the full source slice containing the definition.
- Escalation is strictly increasing: rendered L1 < L2 < L3 token counts for
  the same symbol (integration-tested). Render keeps the symbol name at
  every level so the consumer can decide whether to escalate.

## Caching — `internal/retrieval/cache.go`
- `Cache` (`cache.go:28`): `Get(id, contentHash) (content, tokens, ok)`,
  `Set(h *Handle, content, tokens)`, `Len`, `Clear`; oldest-first eviction.
- Staleness-gated: `Get` with a content hash that no longer matches the
  registered handle misses — a changed symbol is re-retrieved, never served
  stale.

## Surfaces
- MCP `kern_retrieve` (`internal/mcp/handlers_retrieve.go`): query or symbol
  + `level` l1|l2|l3 + optional `budget`/`root` → rendered level content.
- MCP `kern_resolve`: handle ID → L2/L3 with content-hash staleness check
  (stale handle → explicit miss, not stale content).
- CLI `kern retrieve`, `kern resolve` (`cmd/kern/cmd_retrieve.go`).
- Both MCP tools run through the governance governor (agent identity +
  provenance stamp + freshness footer), mirroring `kern_search`.

## Guarantees
- No LLM anywhere in the retrieval path (index + budget + hashes only).
- Handles carry content hashes → stale rejection is deterministic.
- Budget fitting applies at every level (`internal/budget.Fit`), so the
  answer always fits the consumer's token cap.