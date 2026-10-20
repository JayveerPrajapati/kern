# ADR-0012: Conditional-Fetch ETags for MCP Read Tools

## Status

Accepted

## Context

Kern's MCP read tools (kern_context, kern_compact_file, kern_retrieve,
kern_explore, kern_search, kern_why, kern_inherits, kern_graph) return
deterministic, index-backed content that agents re-read
repeatedly — same symbol, same file, same query — across retries, loop
iterations and parallel agents. The D1 tool-response cache (fast-inference
F1-F11) already saves the *recompute*: a repeat call is served
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

- **Pre-render short-circuit (C2/F4, W6)**: when the caller's etag claim is
  registry-verified AND the working-set entry's recorded index identity and
  serve view are unchanged, `Server.runTool` answers "unchanged" WITHOUT
  running the handler at all — the recompute D1 saves is also skipped for
  verified re-reads that bypass D1 (no_cache, LRU eviction, TTL expiry). Any
  mismatch (foreign etag, content change via the probe-gated identity,
  view drift) falls back to the render path below. File-backed tools that
  load no index (kern_compact_file) record an empty identity and never take
  this path (measured live: 1200ms full render vs 1ms pre-render on a warm
  index).
- **Short-circuit**: in `Server.runTool`, after the raw text is obtained (a
  D1 cache hit or a fresh dispatch), the etag is computed; when the request's
  `etag` argument equals it AND the working-set registry shows THIS agent was
  actually served that etag for these args (trust-but-verify, C1/P1: a caller
  claiming an etag it never received — a different agent sharing a client-side
  cache, a foreign etag — gets the full payload), the response is the short
  self-describing text
  `unchanged (etag <E>) — identical to your earlier read; reuse your earlier copy. If this content is not in your context, call again without the etag argument to receive the full payload.`
  with result fields `"etag": "<E>"` and
  `"unchanged": true` (the recovery sentence is C1/P3: a fresh subagent or a
  restarted session told "unchanged" with no copy in context is instructed
  inline how to recover). Short-circuit responses are never stored into the D1
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
  kern_explore, kern_search, kern_why, kern_inherits, kern_graph (extended
  from the original four in C1/P4) — each gains an optional `etag` string
  property in its catalog InputSchema. The eight CLI mirrors (`kern
  context`, `kern compact`, `kern retrieve`, `kern explore`, `kern search`,
  `kern why`, `kern inherits`, `kern graph`) gain `--etag <value>`; they
  print an `etag: <hash>` footer (stderr) and `unchanged (etag <E>)` +
  exit 0 on a match. On `kern graph` the stdout render paths participate
  (default text and `--one-line`); a `--out FILE` write always runs.
  (C2/F2: the CLI default render was switched to the adaptive context-min
  slice, mirroring the MCP `kern_graph` default — previously the default
  rendered the same `ix.Graph` one-line text as `--one-line`, making that
  flag a no-op and collapsing both views to one etag; they now genuinely
  differ and mint different etags.)
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
- Governed/REST passthrough: the RAW `CallToolGoverned` (string-only SDK
  callers) still runs `runTool` with a bare context that carries no per-call
  scope, so it cannot speak the result["etag"]/result["unchanged"] protocol
  and ALWAYS serves full text — no short-circuit (a matching etag would
  otherwise render the literal "unchanged (etag E)" string as the entire
  tool output). The structured variant `CallToolGovernedResult` (C2/F3 —
  the /v1/tools REST route and the SDK `CallResult`) attaches the per-call
  scope: eligible responses carry `etag` (surfaced as response fields, never
  as the whole output), a matching etag yields `unchanged=true` with the
  self-describing one-liner output, and the serve is recorded in the
  working-set registry exactly like a tools/call, so trust-but-verify
  (C1/P1) accepts this caller's next etag claim.
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

## Consistency and race windows

The etag binds to the RESPONSE TEXT (plus the tool's schema version), never
to the index generation or cache identity. That choice is what makes
conditional fetch composable with the freshness machinery:

- **Unrelated edits do not invalidate a view.** Editing file B rebuilds the
  index (new tree identity; D1 entries orphaned) but leaves a read of
  unchanged file A with the same response text — so the old etag still
  matches and the short-circuit holds. A generation-bound etag would force
  a full re-serve of EVERY view after ANY edit. Pinned by
  `TestEtagContentScopedNotGenerationBound` (internal/mcp/etag_serve_test.go).
- **Own-content edits always flip the etag.** The etag is recomputed over
  the served text at call time, so a changed view can never match a stale
  etag; there is no TTL and no expiry path.

Two bounded, self-healing race windows remain, documented honestly:

1. **Stale-while-revalidate.** During an in-flight rebuild, concurrent
   callers are served from the previous snapshot, so an "unchanged" answer
   refers to the pre-edit view. The window is seconds-scale, closes itself
   on the next probe after the atomic swap, and the caller whose edit
   triggered the rebuild always blocks for the fresh index.
2. **Freshness-probe cooldown.** The per-tool-call freshness probe is
   rate-limited to once per second per session; only surfaces without a
   native file-event watcher can observe up to ~1s of staleness. The MCP
   server's filesystem watcher bypasses the cooldown entirely (file events
   wake it immediately and mark the session stale).

One honest interaction caveat between conditional fetch and the D1
response cache, discovered while pinning the property above
(TestEtagContentScopedNotGenerationBound): a D1 hit replays the stored
response text and etag without running the handler, so the session's
staleness probe never fires for same-args calls. The D1 key rotates with
the index identity, but that identity was only re-derived after a rebuild —
and rebuilds only happen when a handler runs. On hosts WITH the native
watcher tool (inotifywait/fswatch) the watcher breaks the cycle by
invalidating the session on every edit; on hosts without it, same-args
calls could replay the cached response (and answer "unchanged" with its
stored etag) until some other call forced a rebuild or the 24h TTL
expired. The CLI surface was unaffected (EnsureFresh probes every run).

**Fixed in C2/F1**: cacheIndexIdentity (tool_cache.go) now validates the
cached identity with the session's rate-limited staleness probe
(Session.ProbeFresh, project.go) before trusting it. A stale probe
invalidates the session's cached index (never builds), the identity
reports "noindex", the lookup misses, and the handler rebuilds — so a D1
hit can no longer be keyed to an index that does not match the tree,
watcher or not. The probe respects the same cooldown as Index (1s without
a native watcher, 5s with one), so burst same-args calls keep the fast
replay path and pay at most one staleness check per cooldown window.
Pinned by TestD1LookupProbesFreshness (D1 enabled; edit → same-args
re-serve must contain the fresh content). KERN_MCP_CACHE=0 and no_cache=1
remain available when freshness must outrun even the cooldown window.