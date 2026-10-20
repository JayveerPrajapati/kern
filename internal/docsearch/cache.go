// In-process per-root cache for the decoded doc index.
//
// kern_doc Search() is the one reload-per-call spot in the MCP path: every
// search previously JSON-decoded the persisted doc index from disk (with
// dense Ollama embeddings stored as JSON float arrays this can be MBs per
// call). The web path and the MCP Platform cache their expensive per-root
// state keyed to an index instance; this is the doc index's equivalent,
// package-level because the cache serves the doc tool family through plain
// functions (LoadCached/InvalidateCache) with no server instance involved.
//
// Design:
//   - keyed by the absolute project root; each entry binds a decoded
//     *Index to a CHEAP stat-based fingerprint of the persisted index file
//     (resolved path + size + mtime of the plain .json, or its .gz twin
//     when archived) — never a content hash, never a re-decode.
//   - an entry is served only while the current fingerprint equals the one
//     the entry was cached under, so a deleted, recreated or rewritten index
//     file always falls through to Load (which keeps its ghost-purge/
//     PurgeDeleted semantics on that reload path).
//   - writers invalidate explicitly: the doc layer calls InvalidateCache
//     after Index (action=index), Fetch (action=fetch ->
//     MergeFetched/ReembedFetch) and Precache (precache.Warm re-indexes a
//     missing doc index) so the next search re-loads even when a rewrite
//     happened to preserve size+mtime.
//   - the map is mutex-guarded and capped (maxDocCacheEntries, FIFO evict
//     oldest) so a pathological multi-root host cannot grow it unboundedly.
//     A missing or empty index file fingerprints as "missing", which never
//     matches a stored entry: an entry is never served for a root whose
//     index file was deleted.
//
// Cached *Index instances are never mutated after storage: every in-process
// writer (MergeFetched, ReembedFetch, Warm) decodes its own fresh instance
// via Load, and Index.Search serializes its reads with the index's own
// RWMutex.

package docsearch

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/cache"
)

// maxDocCacheEntries caps the per-process doc-index cache. Typical MCP
// servers touch 1-3 roots; 16 covers aggressive root rotation while keeping
// eviction trivially cheap (drop the oldest insertion).
const maxDocCacheEntries = 16

// docCacheEntry is one cached decoded index bound to the persisted index
// file's fingerprint at cache time.
type docCacheEntry struct {
	fp  string
	ix  *Index
	seq uint64 // insertion order, for FIFO eviction
}

var (
	docCacheMu  sync.Mutex
	docCache    = map[string]docCacheEntry{}
	docCacheSeq uint64
)

// cachedLoad is the Load indirection LoadCached uses; tests swap it to count
// disk decodes. Production behavior is identical (the real Load is the
// default).
var cachedLoad = Load

// docFingerprint returns a cheap stat-based fingerprint of the persisted doc
// index file Load reads for root: the resolved file path plus size+mtime of
// the active plain .json (or its .gz twin when the plain file is absent). A
// missing, empty or unstat-able file yields the sentinel "missing" so no
// cached entry can ever be served for a root whose index file was deleted or
// truncated. Deliberately NOT a content hash and NOT a decode: a search pays
// two stats at most.
func docFingerprint(root string) string {
	base := cache.Path("data", CacheKey(root)+".json")
	for _, p := range []string{base, base + ".gz"} {
		st, err := os.Stat(p)
		if err != nil || st.Size() == 0 {
			continue
		}
		return "P:" + p + ":" + strconv.FormatInt(st.Size(), 10) + ":" + strconv.FormatInt(st.ModTime().UnixNano(), 10)
	}
	return "missing"
}

// docCacheGet returns the cached decoded index for root when the persisted
// index file still matches the fingerprint the entry was cached under; nil
// otherwise (no entry, or the file changed/deleted/recreated since).
func docCacheGet(root, fp string) *Index {
	key, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	docCacheMu.Lock()
	defer docCacheMu.Unlock()
	e, ok := docCache[key]
	if !ok || e.fp != fp {
		return nil
	}
	return e.ix
}

// docCachePut binds a freshly decoded index to the fingerprint the persisted
// file had at decode time and stores it, evicting the oldest entry when the
// cache is at capacity. A "missing" fingerprint is never stored: a root
// whose index file is absent has no entry to serve.
func docCachePut(root, fp string, ix *Index) {
	if fp == "missing" {
		return
	}
	key, err := filepath.Abs(root)
	if err != nil {
		return
	}
	docCacheMu.Lock()
	defer docCacheMu.Unlock()
	docCacheSeq++
	docCache[key] = docCacheEntry{fp: fp, ix: ix, seq: docCacheSeq}
	if len(docCache) > maxDocCacheEntries {
		var oldestKey string
		var oldestSeq uint64
		first := true
		for k, e := range docCache {
			if first || e.seq < oldestSeq {
				oldestKey, oldestSeq = k, e.seq
				first = false
			}
		}
		delete(docCache, oldestKey)
	}
}

// InvalidateCache drops any cached entry for root. Called by every
// in-process writer of the doc index (the doc layer's Index, Fetch,
// Precache) so a subsequent search re-loads even if the rewrite preserved
// size+mtime.
func InvalidateCache(root string) {
	key, err := filepath.Abs(root)
	if err != nil {
		return
	}
	docCacheMu.Lock()
	defer docCacheMu.Unlock()
	delete(docCache, key)
}

// LoadCached returns the decoded doc index for root, loading it from the
// persisted index file on a cache miss and caching the decoded instance
// under the file's stat fingerprint. Load's ghost-purge (PurgeDeleted)
// semantics run on every reload path exactly as before the cache existed; a
// cache hit skips the disk decode entirely.
func LoadCached(root string) (*Index, error) {
	fp := docFingerprint(root)
	if ix := docCacheGet(root, fp); ix != nil {
		return ix, nil
	}
	ix := cachedLoad(root)
	if ix != nil {
		docCachePut(root, fp, ix)
		return ix, nil
	}
	// Rebuild path — identical to the pre-cache behavior (IndexDir + Save).
	// Nothing is cached here: the persisted file (and so its fingerprint)
	// changed under this call, so the next search sees the new fingerprint
	// and caches the decoded index then.
	var err error
	ix, err = IndexDir(root)
	if err != nil {
		return nil, err
	}
	if err := ix.Save(); err != nil {
		return nil, err
	}
	return ix, nil
}
