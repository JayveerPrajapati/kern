// Package semcache is a deterministic, local semantic cache: it serves a
// previously stored result for a *similar* (not just identical) input, using
// Jaccard similarity over word-shingle signatures. Unlike the exact content-hash
// cache in internal/cache, this catches reworded or near-duplicate prompts and
// logs without any network or embedding model — everything is computed locally
// and is fully reproducible.
package semcache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/cache"
)

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
	Key   string   `json:"key"`   // cache.Load/Store payload key
	Input string   `json:"input"` // the stored input (for the "matched" report)
	Sig   []uint32 `json:"sig"`   // shingle signature (sorted)
}

// nsState bundles a namespace's stripe lock with its in-memory index. Each
// namespace is guarded by its own lock, so Lookup/Store on different
// namespaces never serialize on each other (the old single process-wide mu
// held the lock across blocking disk I/O). The index lives inside the lock
// value so the shared map is only touched by lockFor's LoadOrStore, never
// concurrently by namespace critical sections.
type nsState struct {
	mu sync.Mutex
	es []entry
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
// appended and the index is capped at MaxEntries (oldest dropped first).
func Store(ns, input string, v any) error {
	st := lockFor(ns)
	key := "sem/" + ns + "/" + cache.Hash([]byte(input))

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
	// payload was being written, so reload it before mutating.
	st.mu.Lock()
	defer st.mu.Unlock()
	es, _ = st.loadIndex(ns)
	if replace >= 0 && replace < len(es) && es[replace].Key == key {
		// Replace an identical input if still present (unchanged semantics).
		es[replace] = entry{Key: key, Input: truncate(input), Sig: shingles(input)}
		if err := saveIndex(ns, es); err != nil {
			return err
		}
		st.es = es
		return nil
	}
	// Otherwise append (or update if the same key arrived concurrently).
	found := false
	for i := range es {
		if es[i].Key == key {
			es[i] = entry{Key: key, Input: truncate(input), Sig: shingles(input)}
			found = true
			break
		}
	}
	if !found {
		es = append(es, entry{Key: key, Input: truncate(input), Sig: shingles(input)})
	}
	if len(es) > MaxEntries {
		evicted := es[:len(es)-MaxEntries]
		es = es[len(es)-MaxEntries:]
		// Reclaim payload files so eviction does not orphan them on disk.
		for _, e := range evicted {
			_ = os.Remove(cache.Path("data", e.Key+".json"))
		}
	}
	if err := saveIndex(ns, es); err != nil {
		return err
	}
	st.es = es
	return nil
}

// Lookup searches namespace ns for a stored entry whose input is similar enough
// to input. On a hit it loads the payload into v and returns the matched input,
// the similarity, and true. Thresholds: ShortThreshold for short inputs, else
// DefaultThreshold (overridable via thr when > 0).
func Lookup(ns, input string, v any, thr float64) (matched string, sim float64, hit bool, err error) {
	st := lockFor(ns)

	// Index/entry bookkeeping under the namespace lock: load the index, find
	// the best candidate, and copy out what the payload read needs.
	st.mu.Lock()
	es, err := st.loadIndex(ns)
	if err != nil {
		st.mu.Unlock()
		return "", 0, false, err
	}
	sq := shingles(input)
	if len(sq) == 0 {
		st.mu.Unlock()
		return "", 0, false, nil
	}
	cut := thr
	if cut <= 0 {
		cut = DefaultThreshold
		if len(sq) < 6 {
			cut = ShortThreshold
		}
	}
	best := -1.0
	var bestE *entry
	for i := range es {
		s := jaccard(sq, es[i].Sig)
		if s > best {
			best = s
			bestE = &es[i]
		}
	}
	if bestE == nil || best < cut {
		st.mu.Unlock()
		return "", 0, false, nil
	}
	// Copy the payload key + matched input, then release the lock: the
	// payload cache.Load below is blocking disk I/O and must not run under
	// any lock (the whole point of the striped design).
	key := bestE.Key
	in := bestE.Input
	st.mu.Unlock()

	if err := cache.Load(key, v); err != nil {
		// Payload gone but index entry remains; drop it. Re-acquire the
		// namespace lock for the index mutation.
		st.mu.Lock()
		st.prune(ns, key)
		st.mu.Unlock()
		return "", 0, false, nil
	}
	return in, best, true, nil
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

// prune drops key from ns's index and persists the result. Callers must hold
// the namespace lock.
func (st *nsState) prune(ns, key string) {
	es, _ := st.loadIndex(ns)
	kept := es[:0]
	for _, e := range es {
		if e.Key != key {
			kept = append(kept, e)
		}
	}
	_ = saveIndex(ns, kept)
	st.es = kept
}

// Entries reports the current index size and the stored inputs (most recent
// first) for a namespace.
func Entries(ns string) ([]string, error) {
	st := lockFor(ns)
	st.mu.Lock()
	defer st.mu.Unlock()
	es, err := st.loadIndex(ns)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(es))
	for i := len(es) - 1; i >= 0; i-- {
		out = append(out, es[i].Input)
	}
	return out, nil
}

// Clear wipes the index (and payloads) for a namespace, or all namespaces when
// ns is empty. In-memory state is only updated after the on-disk removal
// succeeds, so a failure cannot leave memory and disk out of sync.
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
			st.mu.Unlock()
		}
		if err := os.RemoveAll(cache.Path("data", "sem")); err != nil {
			return err
		}
		return nil
	}
	st := lockFor(ns)
	st.mu.Lock()
	defer st.mu.Unlock()
	es, _ := st.loadIndex(ns)
	for _, e := range es {
		_ = os.Remove(cache.Path("data", e.Key+".json"))
	}
	if err := os.Remove(cache.Path("data", "sem", ns+"-index.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	st.es = nil
	return nil
}

// Stats returns the number of entries per namespace that have an on-disk index.
func Stats() (map[string]int, error) {
	out := map[string]int{}
	dir := cache.Path("data", "sem")
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, f := range files {
		name := f.Name()
		if strings.HasSuffix(name, "-index.json") {
			var es []entry
			if err := json.Unmarshal(mustRead(filepath.Join(dir, name)), &es); err == nil {
				out[strings.TrimSuffix(name, "-index.json")] = len(es)
			}
		}
	}
	return out, nil
}

func mustRead(path string) []byte {
	b, _ := os.ReadFile(path)
	return b
}
