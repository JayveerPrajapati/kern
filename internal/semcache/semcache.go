// Package semcache is a deterministic, local semantic cache: it serves a
// previously stored result for a *similar* (not just identical) input, using
// Jaccard similarity over word-shingle signatures. Unlike the exact content-hash
// cache in internal/cache, this catches reworded or near-duplicate prompts and
// logs without any network or embedding model — everything is computed locally
// and is fully reproducible.
package semcache

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
)

// DefaultTTL is how long a stored entry stays servable before Lookup treats
// it as a miss and reclaims it. Without a timestamp an entry would serve a
// stale result forever; the TTL bounds how old an answer may be. Overridable
// via KERN_SEMCACHE_TTL (a Go duration, e.g. "72h"); a value <= 0 or garbage
// falls back to this default.
const DefaultTTL = 168 * time.Hour // 7 days

// ttl returns the semcache entry TTL from KERN_SEMCACHE_TTL (0 = default).
func ttl() time.Duration {
	if v := strings.TrimSpace(os.Getenv("KERN_SEMCACHE_TTL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return DefaultTTL
}

// DefaultThreshold is the minimum Jaccard similarity for a hit on inputs with
// enough shingles to compare. 0.60 requires ~60% shingle overlap, which clear
// near-duplicates reach while unrelated text (even when it shares a common
// attached log) stays far below. This is deliberately conservative to avoid
// serving one prompt's cached result for another.
const DefaultThreshold = 0.60

// ShortThreshold applies to very short inputs (< 6 shingles) where Jaccard is
// noisy; require a closer match to avoid false positives.
const ShortThreshold = 0.7

// MaxEntries bounds the in-memory and on-disk index per namespace.
const MaxEntries = 200

// touchPersistInterval throttles the on-disk write of a hit's last-use touch:
// At is refreshed (and persisted) at most once per hour per entry, which keeps
// the index write off the hot path while preserving LRU order and idle-time
// TTL semantics across restarts. A frequently-hit entry therefore keeps a
// last-use clock with one-hour resolution — plenty for eviction ordering.
const touchPersistInterval = time.Hour

// MaxShingles caps the signature size so a multi-megabyte log costs the same to
// compare as a prompt. Shingles are dropped deterministically (by hash), so the
// signature is stable across runs.
const MaxShingles = 2000

// MaxInputLen caps the stored raw input per entry. Matching uses the bounded
// shingle signature; only a truncated preview of the input is kept on disk/in
// memory for the "matched" report and Entries() so long prompts and attached
// logs cannot balloon the index into many MB.
const MaxInputLen = 2048

type entry struct {
	Key   string    `json:"key"`   // cache.Load/Store payload key
	Input string    `json:"input"` // the stored input (for the "matched" report)
	Sig   []uint32  `json:"sig"`   // shingle signature (sorted)
	At    time.Time `json:"at"`    // last use (store time initially, refreshed on hit) — drives TTL expiry and LRU eviction
	Bytes int       `json:"bytes"` // payload JSON size at store time — the per-hit savings estimate
}

// newEntry builds a fresh index entry stamped with the current time. Entries
// written before the At field existed unmarshal with a zero At, which Lookup
// treats as stale (older than any TTL), so legacy entries are reclaimed on
// the first lookup instead of being served forever.
func newEntry(key, input string, bytes int) entry {
	return entry{Key: key, Input: truncate(input), Sig: shingles(input), At: time.Now(), Bytes: bytes}
}

// nsState bundles a namespace's stripe lock with its in-memory index. Each
// namespace is guarded by its own lock, so Lookup/Store on different
// namespaces never serialize on each other (the old single process-wide mu
// held the lock across blocking disk I/O). The index lives inside the lock
// value so the shared map is only touched by lockFor's LoadOrStore, never
// concurrently by namespace critical sections.
type nsState struct {
	mu        sync.Mutex
	es        []entry
	hits      atomic.Int64 // lookups that served a payload (this process)
	misses    atomic.Int64 // lookups that found nothing servable (this process)
	evictions atomic.Int64 // entries dropped by the MaxEntries cap
	saved     atomic.Int64 // payload bytes served by hits (savings estimate)
	// pc is the PERSISTED hit/miss accounting (see nsCounters): it compounds
	// across process restarts, unlike the atomics above which reset every
	// restart. Loaded lazily from the counters file; nil until first use.
	pc        *nsCounters
	pcSavedAt time.Time // last counters-file write (throttles the disk write)
}

// nsCounters is the per-namespace hit/miss accounting persisted on disk. It
// exists because the atomic counters on nsState are per-process and reset on
// restart — without persistence, `kern cache` (a fresh process) would always
// report zero lookups and the cache could never be seen to compound. The
// counters live in their own file (sem/<ns>-counters) alongside the index;
// a missing file reads as zeros, so every index written before counters
// existed is fully backward compatible.
type nsCounters struct {
	Hits   int64 `json:"hits"`
	Misses int64 `json:"misses"`
}

// countersPersistInterval throttles the on-disk write of the persisted
// counters: increments always land in memory immediately, and the file write
// happens at most once per second so a hot lookup loop cannot spam the disk.
// Same best-effort contract as the index (memory is the source of truth; disk
// is a snapshot that lags at most ~1s behind the final increment).
const countersPersistInterval = time.Second

// countersPath is the on-disk counters file for ns. It deliberately carries
// NO .json extension so the cache GC (Maintain) and the `kern cache` walk
// treat it as accounting metadata, never a cache entry — it must not be
// archived, evicted, or counted as cache data.
func countersPath(ns string) string {
	return cache.Path("data", "sem", ns+"-counters")
}

// loadCounters reads the persisted counters for ns; a missing or malformed
// file reads as zeros (backward compatible with pre-counter indexes).
func loadCounters(ns string) *nsCounters {
	b, err := os.ReadFile(countersPath(ns))
	if err != nil {
		return &nsCounters{}
	}
	var c nsCounters
	if json.Unmarshal(b, &c) != nil {
		return &nsCounters{}
	}
	return &c
}

// saveCounters writes the persisted counters for ns via temp + rename (the
// same atomic pattern as saveIndex, so a crash mid-write cannot corrupt the
// accounting into garbage). Callers must NOT hold the namespace lock.
func saveCounters(ns string, c *nsCounters) error {
	if err := cache.Ensure(); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	path := countersPath(ns)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// bumpPersisted records one persisted hit (hit=true) or miss for ns and
// persists the counters file (throttled to once per second). It never
// returns an error — counter accounting must never fail a lookup. Callers
// must NOT hold the namespace lock (the file write is blocking disk I/O).
func (st *nsState) bumpPersisted(ns string, hit bool) {
	st.mu.Lock()
	if st.pc == nil {
		st.pc = loadCounters(ns)
	}
	if hit {
		st.pc.Hits++
	} else {
		st.pc.Misses++
	}
	snapshot := *st.pc
	now := time.Now()
	write := st.pcSavedAt.IsZero() || now.Sub(st.pcSavedAt) >= countersPersistInterval
	if write {
		st.pcSavedAt = now
	}
	st.mu.Unlock()
	if write {
		_ = saveCounters(ns, &snapshot)
	}
}

// nsLocks is the striped lock map: namespace -> *nsState.
var nsLocks sync.Map

// lockFor returns the per-namespace lock state, creating it on first use.
func lockFor(ns string) *nsState {
	st, _ := nsLocks.LoadOrStore(ns, &nsState{})
	return st.(*nsState)
}

// truncate bounds the raw input stored in an index entry to MaxInputLen bytes,
// without splitting a UTF-8 rune. The full shingle signature is computed and
// bounded separately, so this only trims what is persisted for display/debugging,
// keeping index files small.
func truncate(s string) string {
	if len(s) <= MaxInputLen {
		return s
	}
	return s[:utf8safecut([]byte(s[:MaxInputLen]))]
}

// utf8safecut returns the largest index <= len(b) that ends on a rune boundary.
func utf8safecut(b []byte) int {
	for i := len(b); i > 0; i-- {
		if b[i-1]&0xC0 != 0x80 { // not a continuation byte -> rune boundary
			return i
		}
	}
	return 0
}

// shingles returns the sorted, de-duplicated shingle set of text, capped at
// MaxShingles. Words are lowercased, alphanumeric-only, length >= 2; the set
func fnv32(s string) uint32 {
	const offset32 = 2166136261
	const prime32 = 16777619
	h := uint32(offset32)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= prime32
	}
	return h
}

func fnv32Bigram(w1, w2 string) uint32 {
	const offset32 = 2166136261
	const prime32 = 16777619
	h := uint32(offset32)
	for i := 0; i < len(w1); i++ {
		h ^= uint32(w1[i])
		h *= prime32
	}
	h ^= uint32(' ')
	h *= prime32
	for i := 0; i < len(w2); i++ {
		h ^= uint32(w2[i])
		h *= prime32
	}
	return h
}

// shingles returns the sorted, de-duplicated shingle set of text, capped at
// MaxShingles. Words are lowercased, alphanumeric-only, length >= 2; the set
// is the union of single words (for short inputs) and word bigrams.
func shingles(text string) []uint32 {
	words := tokenizeWords(text)
	if len(words) == 0 {
		return nil
	}
	set := map[uint32]struct{}{}
	for _, w := range words {
		set[fnv32(w)] = struct{}{}
	}
	if len(words) > 2 {
		// Always mix unigrams and bigrams so short near-duplicate phrasing
		// overlaps enough; long inputs are capped by the deterministic sample
		// below, which keeps the signature stable across runs.
		for i := 0; i+1 < len(words); i++ {
			set[fnv32Bigram(words[i], words[i+1])] = struct{}{}
		}
	}
	out := make([]uint32, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) > MaxShingles {
		out = out[:MaxShingles]
	}
	return out
}

func tokenizeWords(text string) []string {
	var words []string
	var b strings.Builder
	for _, r := range text {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte(byte(r + ('a' - 'A')))
		} else if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteByte(byte(r))
		} else {
			if b.Len() >= 2 {
				words = append(words, b.String())
			}
			b.Reset()
		}
	}
	if b.Len() >= 2 {
		words = append(words, b.String())
	}
	return words
}

// Similarity returns the Jaccard similarity of a and b over their shingle
// sets: intersection / union. Returns 1 for identical, 0 for disjoint.
func Similarity(a, b string) float64 {
	sa, sb := shingles(a), shingles(b)
	if len(sa) == 0 || len(sb) == 0 {
		return 0
	}
	var inter int
	i, j := 0, 0
	for i < len(sa) && j < len(sb) {
		switch {
		case sa[i] == sb[j]:
			inter++
			i++
			j++
		case sa[i] < sb[j]:
			i++
		default:
			j++
		}
	}
	union := len(sa) + len(sb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func (st *nsState) loadIndex(ns string) ([]entry, error) {
	if st.es != nil {
		return st.es, nil
	}
	var es []entry
	if err := cache.Load("sem/"+ns+"-index", &es); err == nil && len(es) > 0 {
		st.es = es
		return es, nil
	}
	return nil, nil
}

func saveIndex(ns string, es []entry) error {
	if err := cache.Ensure(); err != nil {
		return err
	}
	data, err := json.Marshal(es)
	if err != nil {
		return err
	}
	path := cache.Path("data", "sem", ns+"-index.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Store records input -> v under namespace for future fuzzy hits. Entries are
// appended and the index is capped at MaxEntries (least-recently-used dropped
// first, ordered by the last-use At that hits refresh).
func Store(ns, input string, v any) error {
	st := lockFor(ns)
	key := "sem/" + ns + "/" + cache.Hash([]byte(input))
	// Record the payload size so hits can report a savings estimate (bytes
	// served without recomputation). One extra marshal here is cheaper than
	// re-measuring on the hit path; cache.Store marshals again internally.
	pb, _ := json.Marshal(v)

	// Bookkeeping decision under the namespace lock: load the index and
	// determine whether an identical input is already present.
	st.mu.Lock()
	es, err := st.loadIndex(ns)
	if err != nil {
		st.mu.Unlock()
		return err
	}
	replace := -1
	for i := range es {
		if es[i].Key == key {
			replace = i
			break
		}
	}
	st.mu.Unlock()

	// Payload write happens OUTSIDE the lock so one namespace's disk I/O
	// never blocks another (or itself). The index is only updated after the
	// payload is on disk, preserving the old invariant that an index entry
	// always has a readable payload.
	if err := cache.Store(key, v); err != nil {
		return err
	}

	// Re-acquire for index bookkeeping: the index may have changed while the
	// payload was being written, so reload it before mutating. The mutated
	// entries are published to memory UNDER the lock; the disk write happens
	// after release (blocking disk I/O must not run under any lock).
	st.mu.Lock()
	es, _ = st.loadIndex(ns)
	if replace >= 0 && replace < len(es) && es[replace].Key == key {
		// Replace an identical input if still present (unchanged semantics).
		es[replace] = newEntry(key, input, len(pb))
	} else {
		// Otherwise append (or update if the same key arrived concurrently).
		found := false
		for i := range es {
			if es[i].Key == key {
				es[i] = newEntry(key, input, len(pb))
				found = true
				break
			}
		}
		if !found {
			es = append(es, newEntry(key, input, len(pb)))
		}
	}
	var evicted []entry
	if len(es) > MaxEntries {
		// LRU-touch eviction: order by last use (At, refreshed on hit) so
		// frequently-reused entries survive. The survivor slice stays
		// At-ascending, so Entries()' "most recent first" stays correct.
		sort.SliceStable(es, func(i, j int) bool { return es[i].At.Before(es[j].At) })
		evicted = es[:len(es)-MaxEntries]
		es = es[len(es)-MaxEntries:]
		st.evictions.Add(int64(len(evicted)))
	}
	next := make([]entry, len(es))
	copy(next, es)
	st.es = next
	st.mu.Unlock()

	// Disk I/O outside the lock: reclaim evicted payloads, then persist the
	// index snapshot. A save that races a newer one may lag on disk (memory
	// is the source of truth; disk is best-effort persistence, and the
	// in-memory-only entry after a failed save is a documented state).
	for _, e := range evicted {
		_ = os.Remove(cache.Path("data", e.Key+".json"))
	}
	if err := saveIndex(ns, next); err != nil {
		return err
	}
	return nil
}

// Lookup searches namespace ns for a stored entry whose input is similar enough
// to input. On a hit it loads the payload into v and returns the matched input,
// the similarity, and true. Thresholds: ShortThreshold for short inputs, else
// DefaultThreshold (overridable via thr when > 0). Entries older than the TTL
// (DefaultTTL, or KERN_SEMCACHE_TTL) are treated as misses and reclaimed, so a
// stale result is never served — entries written before the At field existed
// (zero At) are stale by definition.
func Lookup(ns, input string, v any, thr float64) (matched string, sim float64, hit bool, err error) {
	st := lockFor(ns)

	// Index bookkeeping under the namespace lock: load the index, compute the
	// query signature, and copy the entries out. The Jaccard similarity scan
	// below is O(entries x MaxShingles) of pure CPU and must NOT run under
	// the lock. The copy is safe to scan lock-free: entry structs are copied
	// by value, and each entry's Sig backing array is immutable after store
	// (only At is ever mutated, and always under the lock — the copy carries
	// its own At snapshot). All disk I/O (index save, payload reclaim)
	// happens after the lock is released.
	st.mu.Lock()
	es, err := st.loadIndex(ns)
	if err != nil {
		st.mu.Unlock()
		return "", 0, false, err
	}
	sq := shingles(input)
	if len(sq) == 0 {
		st.mu.Unlock()
		st.misses.Add(1)
		st.bumpPersisted(ns, false)
		return "", 0, false, nil
	}
	cut := thr
	if cut <= 0 {
		cut = DefaultThreshold
		if len(sq) < 6 {
			cut = ShortThreshold
		}
	}
	ttlDur := ttl()
	now := time.Now()
	scanEs := append([]entry(nil), es...)
	st.mu.Unlock()

	best := -1.0
	bestKey := ""
	var bestE *entry
	stale := false
	for i := range scanEs {
		if ttlDur > 0 && now.Sub(scanEs[i].At) > ttlDur {
			stale = true // past its TTL: never served, reclaimed below
			continue
		}
		s := jaccard(sq, scanEs[i].Sig)
		if s > best {
			best = s
			bestE = &scanEs[i]
			bestKey = scanEs[i].Key
		}
	}
	if bestE == nil || best < cut {
		if stale {
			// Reclaim stale entries even on a miss so they cannot resurface.
			st.mu.Lock()
			next, dead := st.pruneStaleSnapshot(ns, ttlDur, now)
			st.mu.Unlock()
			_ = st.persist(ns, next, dead)
		}
		st.misses.Add(1)
		st.bumpPersisted(ns, false)
		return "", 0, false, nil
	}
	// Copy the payload key + matched input: the payload cache.Load below is
	// blocking disk I/O and must not run under any lock (the whole point of
	// the striped design).
	key := bestE.Key
	in := bestE.Input
	bytes := bestE.Bytes
	// LRU touch: record this hit as the entry's last use so frequently-reused
	// entries survive eviction. Persisted (throttled by touchPersistInterval)
	// so last-use order and idle-time TTL semantics survive restarts; when
	// stale entries are pruned below, that prune's own save carries the touch
	// (the mutation happens before the snapshot is taken). The touch runs
	// under the lock — the index may have moved on since the scan (the lock
	// was released), so it is applied by key on the current index rather
	// than by stale scan position.
	needSave := false
	st.mu.Lock()
	es, _ = st.loadIndex(ns)
	for i := range es {
		if es[i].Key == bestKey && ttlDur > 0 && now.Sub(es[i].At) > touchPersistInterval {
			es[i].At = now
			needSave = true
			break
		}
	}
	var next []entry
	var dead []string
	if stale {
		next, dead = st.pruneStaleSnapshot(ns, ttlDur, now)
	} else if needSave {
		next = make([]entry, len(es))
		copy(next, es)
		st.es = next
	}
	st.mu.Unlock()

	if needSave || stale {
		_ = st.persist(ns, next, dead)
	}

	if err := cache.Load(key, v); err != nil {
		// Payload gone but index entry remains; drop it. Re-acquire the
		// namespace lock for the index mutation, persist after release.
		st.mu.Lock()
		next, dead := st.prune(ns, key)
		st.mu.Unlock()
		_ = st.persist(ns, next, dead)
		st.misses.Add(1)
		st.bumpPersisted(ns, false)
		return "", 0, false, nil
	}
	st.hits.Add(1)
	st.saved.Add(int64(bytes))
	st.bumpPersisted(ns, true)
	return in, best, true, nil
}

// persist writes es to the on-disk index and reclaims the given payload keys.
// It must be called WITHOUT the namespace lock held: blocking disk I/O never
// runs under a lock (design invariant). A payload removal failure is
// best-effort; the index save failure is returned so callers can decide.
func (st *nsState) persist(ns string, es []entry, reclaim []string) error {
	for _, k := range reclaim {
		_ = os.Remove(cache.Path("data", k+".json"))
	}
	return saveIndex(ns, es)
}

// prune drops key from ns's in-memory index and returns the resulting entries
// snapshot plus the removed payload key for the caller to reclaim. Callers
// must hold the namespace lock; the disk I/O (payload removal + index save)
// happens after release via persist.
func (st *nsState) prune(ns, key string) ([]entry, []string) {
	es, _ := st.loadIndex(ns)
	kept := es[:0]
	var dead []string
	for _, e := range es {
		if e.Key != key {
			kept = append(kept, e)
		} else {
			dead = append(dead, e.Key)
		}
	}
	next := make([]entry, len(kept))
	copy(next, kept)
	st.es = next
	return next, dead
}

// pruneStaleSnapshot drops entries older than the TTL from ns's in-memory
// index and returns the resulting entries snapshot plus the payload keys to
// remove, so stale answers can never be served again. Callers must hold the
// namespace lock; the disk I/O (payload removal + index save) happens after
// release via persist. Best-effort: a failed save leaves the in-memory index
// consistent and disk is reconciled on the next load.
func (st *nsState) pruneStaleSnapshot(ns string, ttlDur time.Duration, now time.Time) ([]entry, []string) {
	es, _ := st.loadIndex(ns)
	var dead []string
	kept := es[:0]
	for _, e := range es {
		if ttlDur > 0 && now.Sub(e.At) > ttlDur {
			dead = append(dead, e.Key)
			continue
		}
		kept = append(kept, e)
	}
	next := make([]entry, len(kept))
	copy(next, kept)
	st.es = next
	return next, dead
}

// jaccard is Similarity's set comparison on pre-computed signatures.
func jaccard(a, b []uint32) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var inter int
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			inter++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// Entries reports the current index size and the stored inputs (most recent
// first by last use) for a namespace.
func Entries(ns string) ([]string, error) {
	st := lockFor(ns)
	st.mu.Lock()
	defer st.mu.Unlock()
	es, err := st.loadIndex(ns)
	if err != nil {
		return nil, err
	}
	// Sort by last use (At): Store replaces an identical input in place, so
	// slice position is not a reliable recency signal.
	sorted := make([]entry, len(es))
	copy(sorted, es)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At.After(sorted[j].At) })
	out := make([]string, 0, len(sorted))
	for _, e := range sorted {
		out = append(out, e.Input)
	}
	return out, nil
}

// Clear wipes the index (and payloads) for a namespace, or all namespaces when
// ns is empty. In-memory state is only updated after the on-disk removal
// succeeds, so a failure cannot leave memory and disk out of sync. Payload
// removal and the index-file removal run OUTSIDE the namespace lock — blocking
// disk I/O never runs under a lock (design invariant).
func Clear(ns string) error {
	if ns == "" {
		// Reset every known namespace's in-memory index under its own lock,
		// then wipe the on-disk tree. An in-flight operation that already
		// passed its payload I/O may re-create an index file afterwards; that
		// mirrors a concurrent Store racing Clear and is equally benign.
		var states []*nsState
		nsLocks.Range(func(_, v any) bool {
			states = append(states, v.(*nsState))
			return true
		})
		for _, st := range states {
			st.mu.Lock()
			st.es = nil
			st.pc = nil // counters file is wiped with the tree; reload as zeros
			st.pcSavedAt = time.Time{}
			st.mu.Unlock()
		}
		if err := os.RemoveAll(cache.Path("data", "sem")); err != nil {
			return err
		}
		return nil
	}
	st := lockFor(ns)
	st.mu.Lock()
	es, _ := st.loadIndex(ns)
	var paths []string
	for _, e := range es {
		paths = append(paths, cache.Path("data", e.Key+".json"))
	}
	idxPath := cache.Path("data", "sem", ns+"-index.json")
	st.mu.Unlock()

	for _, p := range paths {
		_ = os.Remove(p)
	}
	if err := os.Remove(idxPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// Wipe the persisted counters with the namespace: a cleared cache starts
	// its accounting fresh.
	_ = os.Remove(countersPath(ns))
	st.mu.Lock()
	st.es = nil
	st.pc = nil
	st.pcSavedAt = time.Time{}
	st.mu.Unlock()
	return nil
}

// NamespaceStats describes one namespace's semantic-cache accounting.
// Hit/miss/eviction/saved counters are per-process atomics (they reset on
// restart); Entries is always current from the on-disk index, which Stats
// sweeps for stale entries first so the report is truthful even for
// namespaces no lookup has touched this process.
type NamespaceStats struct {
	Entries    int   `json:"entries"`     // entries in the on-disk index after the age sweep
	Hits       int64 `json:"hits"`        // lookups that served a payload
	Misses     int64 `json:"misses"`      // lookups that found nothing servable
	Evictions  int64 `json:"evictions"`   // entries dropped by the MaxEntries cap
	SavedBytes int64 `json:"saved_bytes"` // payload bytes served on hits (savings estimate)
}

// Stats returns per-namespace accounting. As a side effect it sweeps every
// on-disk namespace for entries older than the TTL and reclaims their
// index+payloads: an untouched namespace never sees a Lookup, so pruneStale
// would otherwise never run for it and stale entries would linger forever.
func Stats() (map[string]NamespaceStats, error) {
	out := map[string]NamespaceStats{}
	// In-memory namespaces first: report live counters, and entries that
	// exist only in this process (e.g. after a failed save) instead of
	// silently dropping them.
	nsLocks.Range(func(k, v any) bool {
		ns := k.(string)
		st := v.(*nsState)
		st.mu.Lock()
		s := NamespaceStats{Hits: st.hits.Load(), Misses: st.misses.Load(), Evictions: st.evictions.Load(), SavedBytes: st.saved.Load()}
		if st.es != nil {
			s.Entries = len(st.es)
		}
		st.mu.Unlock()
		out[ns] = s
		return true
	})
	dir := cache.Path("data", "sem")
	files, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	now := time.Now()
	ttlDur := ttl()
	for _, f := range files {
		name := f.Name()
		if !strings.HasSuffix(name, "-index.json") {
			continue
		}
		ns := strings.TrimSuffix(name, "-index.json")
		st := lockFor(ns)
		st.mu.Lock()
		es, lerr := st.loadIndex(ns)
		if lerr == nil {
			// Age-sweep under the lock (snapshot only); reclaim + save on
			// disk after release.
			var dead []string
			kept := es[:0]
			for _, e := range es {
				if ttlDur > 0 && now.Sub(e.At) > ttlDur {
					dead = append(dead, e.Key)
					continue
				}
				kept = append(kept, e)
			}
			if len(kept) != len(es) {
				next := make([]entry, len(kept))
				copy(next, kept)
				st.es = next
				st.mu.Unlock()
				_ = st.persist(ns, next, dead)
			} else {
				st.mu.Unlock()
			}
			s := out[ns]
			s.Entries = len(kept)
			out[ns] = s
			continue
		}
		st.mu.Unlock()
	}
	return out, nil
}

// NamespaceStat is one namespace's semantic-cache report for `kern cache`:
// the on-disk index shape (entries + payload bytes) plus the PERSISTED
// hit/miss counters, which compound across process restarts — unlike the
// per-process atomics in NamespaceStats, so a fresh process can see whether
// the cache is actually compounding. HitRate is hits/(hits+misses); with no
// recorded lookups it is 0 and the renderer shows "-" instead.
type NamespaceStat struct {
	Namespace string `json:"namespace"`
	Entries   int    `json:"entries"`
	Bytes     int64  `json:"bytes"`  // payload bytes on disk for this namespace's entries (estimate)
	Hits      int64  `json:"hits"`   // persisted lifetime lookups that served a payload
	Misses    int64  `json:"misses"` // persisted lifetime lookups that found nothing servable
}

// HitRate returns hits/(hits+misses), 0 when no lookups were recorded.
func (s NamespaceStat) HitRate() float64 {
	total := s.Hits + s.Misses
	if total == 0 {
		return 0
	}
	return float64(s.Hits) / float64(total)
}

// Namespaces returns per-namespace semantic-cache reports for every
// namespace with an on-disk index, sorted by namespace name. Namespaces with
// no on-disk index are absent; a missing sem directory yields an empty list
// (no error). Bytes is the sum of each entry's recorded payload size, so the
// total is an estimate, not a disk-measured footprint. Unlike Stats(), this
// does NOT age-sweep: `kern cache` is a report, and the maintenance pass it
// runs (cache.MaintainDefaults) already handles eviction.
func Namespaces() ([]NamespaceStat, error) {
	dir := cache.Path("data", "sem")
	files, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []NamespaceStat
	for _, f := range files {
		name := f.Name()
		if !strings.HasSuffix(name, "-index.json") {
			continue
		}
		ns := strings.TrimSuffix(name, "-index.json")
		st := lockFor(ns)
		st.mu.Lock()
		es, lerr := st.loadIndex(ns)
		if st.pc == nil {
			st.pc = loadCounters(ns)
		}
		pc := *st.pc
		st.mu.Unlock()
		s := NamespaceStat{Namespace: ns, Hits: pc.Hits, Misses: pc.Misses}
		if lerr == nil {
			s.Entries = len(es)
			for _, e := range es {
				s.Bytes += int64(e.Bytes)
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Namespace < out[j].Namespace })
	return out, nil
}
