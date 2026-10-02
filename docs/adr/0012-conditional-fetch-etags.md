# ADR-0012: Conditional-Fetch ETags for MCP Read Tools

## Status

Accepted

## Context

Kern's MCP read tools (kern_context, kern_compact_file, kern_retrieve,
kern_explore) return deterministic, index-backed content that agents re-read
repeatedly — same symbol, same file, same query — across retries, loop
iterations and parallel agents. The D1 tool-response cache (ADR-0006,
fast-inference F1-F11) already saves the *recompute*: a repeat call is served
from the cache in microseconds. But the full response text is still
serialized back to the agent, and the agent still pays the same output tokens
to ingest it. For a large kern_explore blast-radius dump or a whole-file
kern_compact_file tier=full, that is thousands of tokens per repeat read.

The cheap fix is conditional fetch: attach a content etag to every response;
a caller that already holds the etag re-asks with `etag=<previous>` and gets
a tiny "unchanged" response when nothing changed. HTTP conditional requests
(ETag/If-None-Match) have solved this for decades; MCP has no such primitive,
so kern implements it at the tool-argument level.

## Decision

Every eligible read tool response carries an **etag** (scheme v2): the
sha256 hex of the RAW pre-sandbox response text combined with the tool's
catalog schema version, the etag scheme version, and the effective
max_output budget (resolved per-call via `max_output=` / `KERN_MCP_MAX_OUTPUT`
/ the tool default). A caller that varies max_output gets a different etag
for the same underlying text — re-asking with a larger view after a smaller
one is never answered "unchanged". No per-file hashing — the whole response
is hashed, which is exactly the token cost the caller would otherwise
re-pay.

- **Short-circuit**: in `Server.runTool`, after the raw text is obtained (a
  D1 cache hit or a fresh dispatch), the etag is computed; when the request's
  `etag` argument equals it, the response is the short text
  `unchanged (etag <E>)` with result fields `"etag": "<E>"` and
  `"unchanged": true`. Short-circuit responses are never stored into the D1
  cache (serving from cache then short-circuiting is expected).
- **Cache interplay**: `etag` is stripped from the D1 cache key (the F8
  serve-time-arg pattern), but `max_output` is NOT — different serve views
  are different cache entries, and the scheme-v2 etag folds that view in, so
  re-asking with a larger view after a smaller one is never answered
  "unchanged". The etag itself is computed from the raw pre-mask text and
  carried inside the D1 entry, so a cache-hit replay answers conditional
  fetch with the same etag a fresh run would — masking must not mint a
  different hash over the replayed bytes.
- **File-backed freshness**: for file-backed cacheable tools (kern_compact_file
  via its `path` argument), the D1 cache key also mixes in a file
  fingerprint — the resolved path's size and mtime in nanoseconds — because
  these tools build no index and their index-identity component is the
  literal "noindex". Editing the file rotates the key, so a stale "unchanged"
  verdict within the TTL window is impossible.
- **Eligible tools**: kern_context, kern_compact_file, kern_retrieve,
  kern_explore — each gains an optional `etag` string property in its
  catalog InputSchema. The four CLI mirrors (`kern context`, `kern compact`,
  `kern retrieve`, `kern explore`) gain `--etag <value>`; they print an
  `etag: <hash>` footer (stderr) and `unchanged (etag <E>)` + exit 0 on a
  match.
- **Working-set registry**: the new leaf package `internal/mcp/etag` owns a
  per-agent registry — `map[agentID] → {canonical-args-key → etag}`,
  LRU-bounded (256 entries/agent, 64 agents), mutex-guarded, agent_id flows
  per-call (empty → "_"). Every eligible response records
  (tool, args digest, etag, timestamp). The canonical args key does NOT
  strip max_output — different serve views are different registry entries,
  matching the D1 cache key.
- **kern_meta route**: requests like "my working set" / "workingset"
  classify to a non-cacheable marker route and render the caller's registry
  entries (tool, args digest, etag, timestamp). The D1 cacheability gate
  excludes the route (it is per-agent state, never stored/served).

## Consequences

- Easier: repeat reads of unchanged content cost a few dozen tokens instead
  of the full payload; the working-set route gives agents an inventory of
  what they already hold. Each surface is SELF-consistent: the MCP server
  hashes raw handler output plus provenance, while the CLI hashes its own
  rendered pipeline (profiles.ApplyProfile / RenderTier / PruneCode /
  rendered retrieval) — so cross-surface etag equality is NOT guaranteed and
  never was; an etag from one surface is only meaningful back to that same
  surface.
- Protocol contract: a caller that passes a stale etag always receives the
  full fresh response with the new etag. Conditional fetch is a token
  optimization, and its correctness rests precisely on what the etag covers:
  the response text, the tool's catalog schema version, the etag scheme
  version, the serve view (max_output), and — for file-backed tools — the
  file's size/mtime. Etags rotate whenever any of those change.
- Governed/REST passthrough: `CallToolGoverned` (the web-console/SDK REST
  path) runs `runTool` with a bare context that carries no per-call scope,
  so it cannot speak the result["etag"]/result["unchanged"] protocol. It
  ALWAYS serves full text — no short-circuit — until that path learns to
  carry the scope (a matching etag would otherwise render the literal
  "unchanged (etag E)" string as the entire tool output).
- Working-set args digest: the registry key is a sha256 of the canonical
  args (symbols, paths, query strings). For small argument spaces this
  digest is brute-forceable and is NOT anonymization — it exists only to
  keep the registry compact and comparable. Acceptable under the same
  local-trust model as the D1 cache (entries never leave the machine).
## Trust and byte-stream notes
Three honest caveats about what conditional fetch does and does not certify:
- **PII-mask asymmetry**: the D1 disk entry stores `pii.Mask(text)` but
  carries the etag of the PRE-MASK raw text, so the same etag certifies two
  different byte streams — masked on cache-hit replay, unmasked on fresh
  serve. This is safe only because `pii.Mask` is deterministic: the same raw
  text always mints the same masked text, so the two streams are stable
  functions of each other.
- **Client-asserted identity**: the working-set registry and the kern_meta
  "my working set" route key on the client-supplied `agent_id` argument —
  an unauthenticated, client-asserted identity. Acceptable only inside the
  local stdio/loopback trust boundary; the route and registry must never be
  exposed over unauthenticated REST.
- **Replayed provenance**: a D1 cache entry replays the provenance envelope
  stored with it, so a tampered local cache entry can forge a provenance
  envelope. Same local-trust model as `.kern/index.json` — the audit chain
  never attested output content.
- Trade-off: a per-response hash is added to the serve path (cheap: one
  sha256 over text the server already holds).
- Non-goals (deferred): per-file hashing, delta/diff serving, and
  range-style partial responses. The design deliberately hashes the whole
  response — granular deltas would need stable per-file identity and are a
  separate feature.