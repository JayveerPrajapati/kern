package compress

import "sort"

// MinHash + LSH banding for the large-log fuzzy-merge pass (G-9).
//
// clusterLines' fuzzy merge compares each singleton against every cluster —
// O(n²) Levenshtein comparisons that dominate on very large logs. Above
// minHashClusterThreshold the comparison set is pruned with MinHash
// locality-sensitive hashing: every normalized line is reduced to a set of
// character 3-gram shingles, and each of minHashK seeded hashes records the
// minimum shingle hash — the classic MinHash signature, where two lines
// share a given signature value with probability equal to the Jaccard
// similarity of their shingle sets. Each of the K signature values acts as
// its own LSH band (r=1): two lines become merge candidates when they agree
// on ANY of the K values.
//
// r=1 is deliberate. Conventional LSH uses bands of several rows to suppress
// false positives, but here every candidate still passes the exact
// Levenshtein-ratio test, so a false candidate costs one cheap comparison —
// while a MISSED pair would split a family that the pairwise scan merges.
// With r=1 the miss probability for a true pair is (1-J)^K, which is
// vanishingly small (J=0.3, K=32 → ~2e-17) even for shingle sets that share
// barely a third of their grams. Bands therefore only decide WHICH pairs are
// compared — never whether a pair merges — so the banded path cannot merge
// lines the pairwise path would keep apart, and in practice finds the same
// merges with far less work.

// minHashClusterThreshold is the cluster-line count above which clusterLines
// switches from pairwise fuzzy merging to the banded path. A var (not a
// const) so tests can exercise both sides of the boundary on one input.
var minHashClusterThreshold = 512

const (
	// minHashK is the number of MinHash signature values per line; each one
	// doubles as an LSH band (r=1, see the package comment).
	minHashK = 32
	// minHashCandidateCap bounds the candidate list a single singleton
	// inspects (in first-occurrence order). The merge takes the FIRST
	// eligible cluster, so truncating the tail is almost always a no-op; the
	// cap exists so a degenerate bucket cannot reintroduce quadratic work.
	minHashCandidateCap = 128
	// shingleLen is the character n-gram size the signatures are built
	// from. 3-grams make single-character edits perturb only a few shingles,
	// keeping the Jaccard similarity of near-identical lines high.
	shingleLen = 3
)

const (
	fnvOffset32 = 2166136261
	fnvPrime32  = 16777619
)

// seededFnv1a is a byte-wise FNV-1a with a one-byte seed prepended, inlined
// to keep the K-hashes-per-shingle sweep allocation-free.
func seededFnv1a(seed byte, data []byte) uint32 {
	h := uint32(fnvOffset32)
	h ^= uint32(seed)
	h *= fnvPrime32
	for _, c := range data {
		h ^= uint32(c)
		h *= fnvPrime32
	}
	return h
}

// minHashSignature returns the MinHash signature of a normalized line: for
// each of minHashK seeds, the minimum seeded hash over the line's character
// shingles. Lines shorter than a full shingle hash their whole bytes.
func minHashSignature(norm string) [minHashK]uint32 {
	var sig [minHashK]uint32
	for i := range sig {
		sig[i] = ^uint32(0)
	}
	b := []byte(norm)
	if len(b) < shingleLen {
		for i := 0; i < minHashK; i++ {
			sig[i] = seededFnv1a(byte(i), b)
		}
		return sig
	}
	for j := 0; j+shingleLen <= len(b); j++ {
		sh := b[j : j+shingleLen]
		for i := 0; i < minHashK; i++ {
			if v := seededFnv1a(byte(i), sh); v < sig[i] {
				sig[i] = v
			}
		}
	}
	return sig
}

// bandIndex maps (signature slot, value) band keys to the cluster positions
// carrying them (G-9).
type bandIndex struct {
	buckets map[uint64][]int
	sigs    [][minHashK]uint32
}

// newBandIndex indexes the normalized forms of all clusters by position.
func newBandIndex(norms []string) *bandIndex {
	bi := &bandIndex{
		buckets: make(map[uint64][]int, len(norms)),
		sigs:    make([][minHashK]uint32, len(norms)),
	}
	for pos, n := range norms {
		bi.sigs[pos] = minHashSignature(n)
		for slot, v := range bi.sigs[pos] {
			key := uint64(slot)<<32 | uint64(v)
			bi.buckets[key] = append(bi.buckets[key], pos)
		}
	}
	return bi
}

// candidates returns the positions whose normalized line shares at least one
// MinHash signature value with the cluster at pos: ascending order
// (first-occurrence preference, matching the pairwise scan), deduplicated,
// without pos itself, truncated to minHashCandidateCap.
func (bi *bandIndex) candidates(pos int) []int {
	var out []int
	for slot, v := range bi.sigs[pos] {
		key := uint64(slot)<<32 | uint64(v)
		out = append(out, bi.buckets[key]...)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Ints(out)
	dedup := out[:0]
	for i, p := range out {
		if p == pos || (i > 0 && p == out[i-1]) {
			continue
		}
		dedup = append(dedup, p)
	}
	if len(dedup) > minHashCandidateCap {
		dedup = dedup[:minHashCandidateCap]
	}
	return dedup
}
