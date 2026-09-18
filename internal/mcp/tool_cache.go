package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/index"
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

// toolCacheEntry is the value persisted for one cached tool response.
type toolCacheEntry struct {
	Text string      // RAW pre-sandbox handler output (max_output applied at serve time)
	Prov *Provenance // provenance stamped at store time (nil for tools that loaded no index)
	Ts   time.Time   // store time — drives TTL expiry
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
	for i := range tools {
		if tools[i].Name == name {
			return tools[i].Cacheable
		}
	}
	return false
}

// cacheableForCall reports whether the D1 cache applies to THIS call,
// combining the tool's Cacheable registration flag with call-level gates.
// kern_meta is Cacheable:true (F11) but its responses are only stored/served
// when the sub-tool it routes to is itself cacheable — a kern_meta request
// that classifies to an exec-gated, mutating or stateful sub-tool (kern_plan,
// kern_verify, kern_memory_ranked, the skill routers, ...) must never be
// cached (R1). The routed name is resolved deterministically from the request
// via classifyMetaRequest, so the lookup gate (before dispatch) and the store
// gate (after dispatch) agree on the same verdict. Semantic calls are also
// excluded: their results depend on KERN_EMBED_MODEL, which is not part of
// the cache key (R3).
func cacheableForCall(name string, args map[string]any) bool {
	if !toolCacheable(name) {
		return false
	}
	if argBool(args, "semantic") {
		return false // R3: embedding-model-dependent output is never cached
	}
	if name == "kern_meta" || name == "kern_ask" {
		routed, _ := classifyMetaRequest(argString(args, "request"))
		return toolCacheable(routed)
	}
	return true
}

// cacheKeyFor builds the sha256 hex cache key for one call. Components, in
// order: tool name | canonical JSON of args (serve-time and identity-only
// concerns stripped: max_output, no_cache, agent_id, task — F8) | resolved
// root (F2) | index identity (F2/F6) | tool SchemaVersion (F8) |
// version.BuildID (build identity — stamped version for releases, binary
// size+mtime for dev builds so a rebuild mints fresh keys). json.Marshal
// sorts map keys, so the JSON form is canonical for free. The identity is
// passed in because lookup and store must agree on the same string.
func cacheKeyFor(name string, args map[string]any, root, identity string) string {
	keyArgs := map[string]any{}
	for k, v := range args {
		switch k {
		case "max_output", "no_cache", "agent_id", "task":
			// Serve-time or identity-only concerns: identical answers share
			// one entry across max_output/agent_id/task variance.
			continue
		}
		keyArgs[k] = v
	}
	canon, _ := json.Marshal(keyArgs)
	var schemaVersion string
	for i := range tools {
		if tools[i].Name == name {
			schemaVersion = tools[i].SchemaVersion
			break
		}
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%s", name, canon, root, identity, schemaVersion, version.BuildID())
	return hex.EncodeToString(h.Sum(nil))
}

// cacheKey computes the D1 key for a call using the session's currently
// cached index identity for its root (peeked, never built — see
// cacheIndexIdentity).
func (s *Server) cacheKey(name string, args map[string]any) string {
	root := resolveRoot(argString(args, "root"))
	return cacheKeyFor(name, args, root, s.cacheIndexIdentity(root))
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
	s.mu.Lock()
	sess, ok := s.sessions[root]
	s.mu.Unlock()
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
	if scope, ok := ctx.Value(indexScopeKey{}).(*indexScope); ok && e.Prov != nil {
		scope.prov = e.Prov
		s.mu.Lock()
		sess, hasSess := s.sessions[root]
		s.mu.Unlock()
		if hasSess {
			if ix, ok := sess.CachedIndex(); ok && ix != nil && ix.Identity != nil {
				scope.ix = ix
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
	text = pii.Mask(text).Text // mask before persisting (D1 cache honesty)
	e := toolCacheEntry{Text: text, Ts: time.Now()}
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
