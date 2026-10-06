package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/etag"
	"github.com/JayveerPrajapati/kern/internal/mcpserve"
	"github.com/JayveerPrajapati/kern/internal/metaroute"
	"github.com/JayveerPrajapati/kern/internal/pii"
	"github.com/JayveerPrajapati/kern/internal/version"
)

// D1 — MCP tool-response cache (fast-inference design, F1-F11). Deterministic
// tool outputs are cached keyed by (tool name, canonical args, resolved root,
// index identity, tool schema version, kern version) so repeated identical
// calls — retries, loop iterations, parallel agents — are served with zero
// recompute. The disk store (internal/cache, key prefix "mcp-toolcache-") is
// the source of truth; a small bounded in-process LRU (F9) skips the JSON
// round-trip on hot paths. Knobs: KERN_MCP_CACHE=0 disables entirely (default
// on); KERN_MCP_CACHE_TTL sets entry expiry (default 24h, 0 = no expiry);
// per-call no_cache=1 bypasses. Trust model (F5): entries are local-trust,
// exactly like .kern/index.json — the audit chain never attested output
// content, so a tampered entry is undetectable by audit; acceptable for a
// local, deterministic, index-keyed store.

// toolCacheKeyPrefix namespaces D1 entries inside the shared internal/cache
// store, keeping them distinct from every other cached artifact.
const toolCacheKeyPrefix = "mcp-toolcache-"

// toolCacheEntrySizeCap (F9) skips caching outputs above this size: a huge
// dump (kern_arch/kern_impact) costs more to persist than to recompute.
const toolCacheEntrySizeCap = 512 << 10 // 512 KiB

// toolByName indexes the registered catalog by tool name once at init, so
// per-call Cacheable/schema-version lookups are O(1) instead of a linear
// scan of the ~140-tool catalog on every tools/call (cacheableForCall and
// cacheKeyFor each scan in both the lookup and store paths). catalog.All is
// immutable after package init (WithDiffgateTools only reads it), so the map
// cannot drift from the slice.
var toolByName = func() map[string]Tool {
	m := make(map[string]Tool, len(tools))
	for _, t := range tools {
		m[t.Name] = t
	}
	return m
}()

// toolCacheEntry is the value persisted for one cached tool response.
type toolCacheEntry struct {
	Text string      // RAW pre-sandbox handler output (max_output applied at serve time)
	Prov *Provenance // provenance stamped at store time (nil for tools that loaded no index)
	Ts   time.Time   // store time — drives TTL expiry
	ETag string      // conditional-fetch etag (B1, ADR-0012); computed from the RAW PRE-MASK text
}

// cacheEnabled reports whether the D1 cache is active. KERN_MCP_CACHE=0
// disables it entirely; any other value (including unset) enables it.
func cacheEnabled() bool {
	return os.Getenv("KERN_MCP_CACHE") != "0"
}

// cacheTTL returns the entry TTL from KERN_MCP_CACHE_TTL. Default 24h;
// 0 means no expiry (GC still prunes dormant entries). An unparsable
// (or negative) value warns on stderr instead of silently falling back
// (R6, fails-loud).
func cacheTTL() time.Duration {
	if v := strings.TrimSpace(os.Getenv("KERN_MCP_CACHE_TTL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			return d
		}
		fmt.Fprintf(os.Stderr, "kern-mcp: KERN_MCP_CACHE_TTL=%q not a duration, using 24h\n", v)
	}
	return 24 * time.Hour
}

// noCacheArg reports whether the caller bypassed the cache for this call
// (no_cache=1). Stripped from the cache key when present (F8).
func noCacheArg(args map[string]any) bool {
	switch v := args["no_cache"].(type) {
	case string:
		return v == "1" || strings.EqualFold(v, "true")
	case bool:
		return v
	case float64:
		return v != 0
	}
	return false
}

// toolCacheable reports whether the named tool opts into the D1 cache
// allowlist (F1): its Cacheable registration flag is set.
func toolCacheable(name string) bool {
	return toolByName[name].Cacheable
}

// cacheableForCall reports whether the D1 cache applies to THIS call,
// combining the tool's Cacheable registration flag with call-level gates.
// kern_meta is Cacheable:true (F11) but its responses are only stored/served
// when the sub-tool it routes to is itself cacheable — a kern_meta request
// that classifies to an exec-gated, mutating or stateful sub-tool (kern_plan,
// kern_verify, kern_memory, the skill routers, ...) must never be
// cached (R1). The routed name is resolved deterministically from the request
// via classifyMetaRequest, so the lookup gate (before dispatch) and the store
// gate (after dispatch) agree on the same verdict. Semantic calls are also
// excluded: their results depend on KERN_EMBED_MODEL, which is not part of
// the cache key (R3). The kern_meta workingset route (B1, ADR-0012: "my
// working set" / "workingset") is per-agent registry state, so it is never
// cached either — the classifier marks it with the workingset argument and
// this gate detects the marker.
// resolveCacheCall maps a top-level tool call to the effective (routed)
// call it will execute: a kern_meta call is classified to its sub-tool with
// the F3 structured passthrough args merged over the classifier-synthesized
// ones, so the cache gates, file fingerprints, and keys all see the call
// that actually runs (F-1 fingerprint parity with the direct call; R3
// semantic detection through the passthrough). Any other tool maps to
// itself. Pure and deterministic: lookup and store paths agree.
func resolveCacheCall(name string, args map[string]any) (string, map[string]any) {
	if name != "kern_meta" {
		return name, args
	}
	routed, rargs := classifyMetaRequest(argString(args, "request"))
	if sub, ok := args["args"].(map[string]any); ok {
		for k, v := range sub {
			rargs[k] = v
		}
	}
	return routed, rargs
}

func cacheableForCall(name string, args map[string]any) bool {
	if !toolCacheable(name) {
		return false
	}
	if argBool(args, "semantic") {
		return false // R3: embedding-model-dependent output is never cached
	}
	if name == "kern_meta" {
		routed, rargs := resolveCacheCall(name, args)
		if argBool(rargs, "semantic") {
			return false // R3: embedding-model-dependent output is never cached (passthrough included)
		}
		if argBool(rargs, metaroute.WorkingsetArg) {
			return false // B1: the workingset listing is per-agent state
		}
		return toolCacheable(routed)
	}
	return true
}

// fileFingerprintArgs lists the Cacheable tools whose output is a function of
// a single file's CONTENT rather than the project index, keyed by the
// file-path argument names from each tool's catalog InputSchema. The D1 key
// for these has no index identity to rotate (they build no index → identity
// "noindex"), so the file fingerprint IS the freshness contract (F-1). Every
// other Cacheable tool in the catalog is index-backed (its identity already
// rotates the key) or takes no file-path argument — verified against
// catalog/tools.go at fix time — so this map stays minimal; add a tool here
// only when its output depends on a file the index does not cover.
var fileFingerprintArgs = map[string][]string{
	"kern_compact_file": {"path"},
}

// fileFingerprint resolves each mapped file-path argument exactly as the
// tool's handler does (root via resolveRoot, path via rootedPath) and returns
// a deterministic "size:mtime" component per file, joined for multiple args.
// A file that cannot be resolved or stat'd contributes "missing" so lookup
// and store still agree on a stable key. Returns "" for tools not in
// fileFingerprintArgs — a byte-identical key for the index-backed trio and
// pure-arg tools (zero behavior change).
func fileFingerprint(name string, args map[string]any, root string) string {
	name, args = resolveCacheCall(name, args) // kern_meta: fingerprint the ROUTED file-backed tool (F-1)
	argNames, ok := fileFingerprintArgs[name]
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(argNames))
	for _, arg := range argNames {
		p := argString(args, arg)
		if p == "" {
			parts = append(parts, "missing")
			continue
		}
		abs, err := rootedPath(root, p)
		if err != nil {
			parts = append(parts, "missing")
			continue
		}
		info, err := os.Stat(abs)
		if err != nil {
			parts = append(parts, "missing")
			continue
		}
		parts = append(parts, fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano()))
	}
	return strings.Join(parts, ",")
}

// cacheKeyFor builds the sha256 hex cache key for one call. Components, in
// order: tool name | canonical JSON of args (identity-only and
// conditional-fetch concerns stripped: etag, no_cache, agent_id, task — F8;
// max_output is NOT stripped, F-2 — the etag is bound to the serve-time
// view, so different serve views are different content) | resolved root (F2)
// | index identity (F2/F6) | tool SchemaVersion (F8) | version.BuildID (build
// identity — stamped version for releases, binary size+mtime for dev builds
// so a rebuild mints fresh keys) | file fingerprint (F-1, file-backed tools
// only). json.Marshal sorts map keys, so the JSON form is canonical for
// free. The identity is passed in because lookup and store must agree on the
// same string.
func cacheKeyFor(name string, args map[string]any, root, identity string) string {
	// kern_meta keys key the ROUTED call, so the fingerprint and schema
	// version match what a direct call to the same tool would use (F-1/R3).
	name, args = resolveCacheCall(name, args)
	// Serve-time and identity-only conditional-fetch concerns (etag, no_cache,
	// agent_id, task) are stripped by the ONE shared helper
	// (etag.StripServeTimeArgs — the same F8 set the working-set registry key
	// uses, NIT-10). max_output is deliberately NOT stripped (F-2): the etag
	// is view-bound, so different serve views key separate entries.
	canon, _ := json.Marshal(etag.StripServeTimeArgs(args))
	schemaVersion := toolByName[name].SchemaVersion
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%s", name, canon, root, identity, schemaVersion, version.BuildID())
	// F-1: file-backed tools (kern_compact_file) have no index identity to
	// rotate the key, so the fingerprint of the target file IS the freshness
	// contract — an edited file must mint a new key, otherwise the next call
	// replays the stale cached summary and answers "unchanged" for changed
	// content. Index-backed tools never reach the fingerprint (empty string →
	// byte-identical key).
	if fp := fileFingerprint(name, args, root); fp != "" {
		_, _ = fmt.Fprintf(h, "\x00%s", fp)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// indexIdentityString renders the cache-relevant identity of an index:
// ContentRoot|TreeOID|GitCommit. BuiltAt is deliberately excluded (R4): it is
// a build timestamp, so every rebuild mints a new cache key even for
// byte-identical content, defeating cross-rebuild/cross-process hits. The
// content-addressed ContentRoot/TreeOID/GitCommit already rotate when the
// indexed content changes (F2/F6).
func indexIdentityString(id *index.IndexIdentity) string {
	return id.ContentRoot + "|" + id.TreeOID + "|" + id.GitCommit
}

// cacheIndexIdentity returns the identity string of the session's currently
// cached index for root: the content-addressed fingerprint used for freshness
// proofs (ContentRoot + tree OID + git commit; BuiltAt excluded, see
// indexIdentityString — R4). An index whose content changed produces a
// different identity → a different cache key → stale entries can never be
// served (F2/F6). It peeks the session without triggering a build: pure-arg
// tools (kern_mask_pii, ...) must never force an index build just to compute
// a cache key. "noindex" when no index is cached for this root.
func (s *Server) cacheIndexIdentity(root string) string {
	sess, ok := s.sessCache.Peek(root)
	if !ok {
		return "noindex"
	}
	ix, ok := sess.CachedIndex()
	if !ok || ix == nil || ix.Identity == nil {
		return "noindex"
	}
	return indexIdentityString(ix.Identity)
}

// cacheLookup serves a call from the D1 cache. On a hit it returns the raw
// pre-sandbox handler text and stamps the per-call scope with the cached
// provenance plus the identity-matched index, so toolCallResponse replays the
// exact response shape (text, provenance field, summary line, token
// metadata) of a fresh run (F6/F11). Expired entries are treated as a miss
// and deleted. Lookups never trigger an index build.
func (s *Server) cacheLookup(ctx context.Context, name string, args map[string]any) (string, bool) {
	root := resolveRoot(argString(args, "root"))
	identity := s.cacheIndexIdentity(root)
	key := toolCacheKeyPrefix + cacheKeyFor(name, args, root, identity)
	// In-process LRU first, then the disk store (source of truth).
	var e toolCacheEntry
	if got, ok := lruGet(key); ok {
		e = got
	} else if err := cache.Load(key, &e); err != nil {
		return "", false
	}
	// TTL: expired = miss (and delete) — cheap insurance beyond the index
	// identity (F6).
	if ttl := cacheTTL(); ttl > 0 && time.Since(e.Ts) > ttl {
		lruDel(key)
		_ = cache.Remove(key)
		return "", false
	}
	lruPut(key, e)
	// Replay path: stamp the scope with the stored provenance and the
	// identity-matched index (peeked, no rebuild) so the response is
	// byte-identical to a fresh run on the same index.
	if scope, ok := ctx.Value(indexScopeKey{}).(*indexScope); ok {
		// B1 (ADR-0012): the entry carries the etag computed from the raw
		// pre-mask text, so a cache-hit replay answers conditional-fetch with
		// the same etag a fresh run would (masking would otherwise mint a
		// different hash over the replayed bytes).
		if e.ETag != "" {
			scope.etag = e.ETag
		}
		if e.Prov != nil {
			scope.prov = e.Prov
			if sess, hasSess := s.sessCache.Peek(root); hasSess {
				if ix, ok := sess.CachedIndex(); ok && ix != nil && ix.Identity != nil {
					scope.ix = ix
				}
			}
		}
	}
	return e.Text, true
}

// cacheStore persists a successful tool response (raw pre-sandbox text plus
// the provenance stamped at store time) under the call's key. Entries whose
// raw text exceeds the size cap are skipped (F9). Best-effort by design: a
// failed store never fails the call that produced it. Only successful
// (non-error) results reach here (F1 correctness rule 1). The stored text is
// PII-masked before persisting, so a cached entry on disk never carries raw
// secrets (e.g. source lines with API keys) — cache hits replay the masked
// form.
func (s *Server) cacheStore(ctx context.Context, name string, args map[string]any, text string) {
	if len(text) > toolCacheEntrySizeCap {
		return // F9: huge outputs cost more to persist than recompute
	}
	// B1 (ADR-0012): capture the conditional-fetch etag from the RAW
	// pre-mask text — the bytes a fresh caller received — so a later cache
	// hit answers etag=<that value> with the unchanged short-circuit. The
	// etag is REUSED, not recomputed: runTool's maybeShortCircuit already
	// minted it over this identical text and stamped it on the per-call
	// scope (NIT-9); the recompute fallback only serves direct callers with
	// no scope (tests).
	e := toolCacheEntry{Text: pii.Mask(text).Text, Ts: time.Now()}
	if etag.Eligible(name) {
		if scope, ok := ctx.Value(indexScopeKey{}).(*indexScope); ok && scope.etag != "" {
			e.ETag = scope.etag
		} else {
			// F-2: the etag is bound to the serve-time view (max_output
			// budget); mint it with the SAME view maybeShortCircuit uses so
			// the fallback and the short-circuit agree byte-for-byte. A
			// malformed max_output errors the call downstream; -1 keeps the
			// hash deterministic here too.
			budget, err := mcpserve.CallOutputBudget(name, args)
			if err != nil {
				budget = -1
			}
			e.ETag = etag.HashView(text, toolByName[name].SchemaVersion, strconv.Itoa(budget))
		}
	}
	root := resolveRoot(argString(args, "root"))
	// R5: key the entry on the identity of the index that actually produced
	// the answer (the handler's scope) rather than a re-peek of the session's
	// current index — a watcher invalidation landing between loadIndex and
	// store would otherwise persist under "noindex". Lookup keeps peeking the
	// session's current identity: a hit must key on what the session serves
	// now, and store/lookup agree while the producing index is still current.
	identity := s.cacheIndexIdentity(root)
	if scope, ok := ctx.Value(indexScopeKey{}).(*indexScope); ok {
		if scope.ix != nil && scope.ix.Identity != nil {
			identity = indexIdentityString(scope.ix.Identity)
		}
		if scope.prov != nil {
			// Structured provenance stamped by the handler.
			e.Prov = scope.prov
		} else if scope.ix != nil {
			// Mirror toolCallResponse: index-only tools get raw identity
			// provenance so a replay attaches the identical envelope.
			e.Prov = s.rawProvenance(scope.ix, nil)
		}
	}
	key := toolCacheKeyPrefix + cacheKeyFor(name, args, root, identity)
	lruPut(key, e)
	_ = cache.Store(key, e)
}

// --- In-process LRU (F9) ---
// A small bounded LRU over decoded entries. Disk remains the source of truth;
// the LRU only skips the JSON round-trip on hot repeated calls. Bounded at
// toolCacheLRUCap entries with LRU eviction; guarded by one mutex.

// toolCacheLRUCap bounds the in-process LRU.
const toolCacheLRUCap = 128

var (
	lruMu    sync.Mutex
	lruOrder []string // most-recently-used first
	lruItems = map[string]toolCacheEntry{}
)

// lruGet returns the entry for key, promoting it to MRU. ok=false on miss.
func lruGet(key string) (toolCacheEntry, bool) {
	lruMu.Lock()
	defer lruMu.Unlock()
	e, ok := lruItems[key]
	if !ok {
		return toolCacheEntry{}, false
	}
	lruPromoteLocked(key)
	return e, true
}

// lruPut stores entry under key (inserting or updating and promoting it to
// MRU), evicting the LRU tail when over cap.
func lruPut(key string, e toolCacheEntry) {
	lruMu.Lock()
	defer lruMu.Unlock()
	lruPromoteLocked(key)
	lruItems[key] = e
	for len(lruOrder) > toolCacheLRUCap {
		tail := lruOrder[len(lruOrder)-1]
		lruOrder = lruOrder[:len(lruOrder)-1]
		delete(lruItems, tail)
	}
}

// lruDel removes key from the LRU (used when an entry expires).
func lruDel(key string) {
	lruMu.Lock()
	defer lruMu.Unlock()
	delete(lruItems, key)
	for i, k := range lruOrder {
		if k == key {
			lruOrder = append(lruOrder[:i], lruOrder[i+1:]...)
			return
		}
	}
}

// lruPromoteLocked moves key to the MRU front. Caller holds lruMu.
func lruPromoteLocked(key string) {
	for i, k := range lruOrder {
		if k == key {
			lruOrder = append(lruOrder[:i], lruOrder[i+1:]...)
			lruOrder = append([]string{key}, lruOrder...)
			return
		}
	}
	lruOrder = append([]string{key}, lruOrder...)
}

// lruReset clears the LRU. Test-only: keeps cache tests deterministic across
// cases sharing the package-global LRU.
func lruReset() {
	lruMu.Lock()
	defer lruMu.Unlock()
	lruOrder = nil
	lruItems = map[string]toolCacheEntry{}
}
