// Package semcache is a deterministic, local semantic cache: it serves a
// previously stored result for a *similar* (not just identical) input, using
// Jaccard similarity over word-shingle signatures. Unlike the exact content-hash
// cache in internal/cache, this catches reworded or near-duplicate prompts and
// logs without any network or embedding model — everything is computed locally
// and is fully reproducible.
package semcache

import (
	"encoding/json"
	"hash/fnv"
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

var (
	mu      sync.Mutex
	indexes = map[string][]entry{}
)

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
// is the union of single words (for short inputs) and word bigrams.
func shingles(text string) []uint32 {
	words := tokenizeWords(text)
	if len(words) == 0 {
		return nil
	}
	set := map[uint32]struct{}{}
	hashes := func(ss []string) {
		for _, s := range ss {
			h := fnv.New32a()
			_, _ = h.Write([]byte(s))
			set[h.Sum32()] = struct{}{}
		}
	}
	if len(words) <= 2 {
		hashes(words)
	} else {
		// Always mix unigrams and bigrams so short near-duplicate phrasing
		// overlaps enough; long inputs are capped by the deterministic sample
		// below, which keeps the signature stable across runs.
		hashes(words)
		for i := 0; i+1 < len(words); i++ {
			hashes([]string{words[i] + " " + words[i+1]})
		}
	}
	if len(set) > MaxShingles {
		// Deterministic sample: keep the smallest MaxShingles hashes.
		sorted := make([]uint32, 0, len(set))
		for h := range set {
			sorted = append(sorted, h)
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
		sorted = sorted[:MaxShingles]
		set = make(map[uint32]struct{}, len(sorted))
		for _, h := range sorted {
			set[h] = struct{}{}
		}
	}
	out := make([]uint32, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func tokenizeWords(text string) []string {
	var words []string
	for _, f := range strings.Fields(strings.ToLower(text)) {
		f = strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				return r
			}
			return ' '
		}, f)
		for _, w := range strings.Fields(f) {
			if len(w) >= 2 {
				words = append(words, w)
			}
		}
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

func loadIndex(ns string) ([]entry, error) {
	if es, ok := indexes[ns]; ok {
		return es, nil
	}
	var es []entry
	if err := cache.Load("sem/"+ns+"-index", &es); err == nil && len(es) > 0 {
		indexes[ns] = es
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
	mu.Lock()
	defer mu.Unlock()
	es, err := loadIndex(ns)
	if err != nil {
		return err
	}
	key := "sem/" + ns + "/" + cache.Hash([]byte(input))
	if err := cache.Store(key, v); err != nil {
		return err
	}
	// Replace an identical input if already present.
	for i := range es {
		if es[i].Key == key {
			es[i] = entry{Key: key, Input: truncate(input), Sig: shingles(input)}
			return saveIndex(ns, es)
		}
	}
	es = append(es, entry{Key: key, Input: truncate(input), Sig: shingles(input)})
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
	indexes[ns] = es
	return nil
}

// Lookup searches namespace ns for a stored entry whose input is similar enough
// to input. On a hit it loads the payload into v and returns the matched input,
// the similarity, and true. Thresholds: ShortThreshold for short inputs, else
// DefaultThreshold (overridable via thr when > 0).
func Lookup(ns, input string, v any, thr float64) (matched string, sim float64, hit bool, err error) {
	mu.Lock()
	defer mu.Unlock()
	es, err := loadIndex(ns)
	if err != nil {
		return "", 0, false, err
	}
	sq := shingles(input)
	if len(sq) == 0 {
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
		return "", 0, false, nil
	}
	if err := cache.Load(bestE.Key, v); err != nil {
		// Payload gone but index entry remains; drop it.
		prune(ns, bestE.Key)
		return "", 0, false, nil
	}
	return bestE.Input, best, true, nil
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

func prune(ns, key string) {
	es, _ := loadIndex(ns)
	kept := es[:0]
	for _, e := range es {
		if e.Key != key {
			kept = append(kept, e)
		}
	}
	_ = saveIndex(ns, kept)
	indexes[ns] = kept
}

// Entries reports the current index size and the stored inputs (most recent
// first) for a namespace.
func Entries(ns string) ([]string, error) {
	mu.Lock()
	defer mu.Unlock()
	es, err := loadIndex(ns)
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
	mu.Lock()
	defer mu.Unlock()
	if ns == "" {
		if err := os.RemoveAll(cache.Path("data", "sem")); err != nil {
			return err
		}
		indexes = map[string][]entry{}
		return nil
	}
	es, _ := loadIndex(ns)
	for _, e := range es {
		_ = os.Remove(cache.Path("data", e.Key+".json"))
	}
	if err := os.Remove(cache.Path("data", "sem", ns+"-index.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(indexes, ns)
	return nil
}

// Stats returns the number of entries per namespace that have an on-disk index.
func Stats() (map[string]int, error) {
	mu.Lock()
	defer mu.Unlock()
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
