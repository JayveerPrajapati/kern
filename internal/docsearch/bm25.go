package docsearch

import (
	"math"
	"sort"
	"strings"
)

// BM25 ranking over the chunk corpus, fused with the cosine embedding score
// via Reciprocal Rank Fusion (RRF). Adopted from the grepai hybrid-search and
// jpoindexter/kern suggest-engine patterns: BM25 handles exact keyword
// matching (rare terms, quoted identifiers) where feature-hashed n-grams blur
// word boundaries, while cosine stays better at fuzzy and morphological
// matches. Fusing both with RRF keeps the strengths of each without
// calibrating their scores against each other.

const (
	bm25K1 = 1.2
	bm25B  = 0.75
	rrfK   = 60
)

// corpusStats is the per-Search term statistics needed for BM25.
type corpusStats struct {
	total    int                       // number of documents (chunks)
	avgLen   float64                   // mean chunk length in terms
	docFreq  map[string]int            // term -> docs containing it
	termFreq map[string]map[string]int // doc ID -> term -> count
}

func buildCorpusStats(docs []Doc) *corpusStats {
	st := &corpusStats{
		docFreq:  map[string]int{},
		termFreq: map[string]map[string]int{},
	}
	var lenSum int
	for _, d := range docs {
		tf := map[string]int{}
		n := 0
		for _, t := range tokenizeWords(strings.ToLower(d.Chunk.Text)) {
			tf[t]++
			n++
		}
		lenSum += n
		st.termFreq[d.ID] = tf
		for t := range tf {
			st.docFreq[t]++
		}
	}
	st.total = len(docs)
	if len(docs) > 0 {
		st.avgLen = float64(lenSum) / float64(len(docs))
	}
	return st
}

// score is the BM25 score of a single chunk against the query terms.
func (st *corpusStats) score(query []string, doc Doc) float64 {
	tf := st.termFreq[doc.ID]
	if tf == nil || st.avgLen == 0 {
		return 0
	}
	docLen := 0
	for _, c := range tf {
		docLen += c
	}
	n := float64(st.total)
	var score float64
	for _, q := range query {
		f := tf[q]
		if f == 0 {
			continue
		}
		df := float64(st.docFreq[q])
		idf := math.Log(1 + (n-df+0.5)/(df+0.5))
		denom := float64(f) + bm25K1*(1-bm25B+bm25B*float64(docLen)/st.avgLen)
		score += idf * (float64(f) * (bm25K1 + 1)) / denom
	}
	return score
}

// rankBM25 returns the chunks that match at least one query term, ranked by
// BM25 score, best first.
func (st *corpusStats) rankBM25(query []string, docs []Doc) []Score {
	var out []Score
	for _, d := range docs {
		if s := st.score(query, d); s > 0 {
			out = append(out, Score{Doc: d, Sim: s})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sim != out[j].Sim {
			return out[i].Sim > out[j].Sim
		}
		return out[i].Doc.ID < out[j].Doc.ID
	})
	return out
}

// fuseRRF merges two ranked lists by reciprocal rank (k=60). Each chunk keeps
// the fused score; chunks absent from a list simply get no contribution from
// it.
func fuseRRF(lists ...[]Score) []Score {
	scores := map[string]float64{}
	byID := map[string]Doc{}
	for _, list := range lists {
		for rank, s := range list {
			byID[s.Doc.ID] = s.Doc
			scores[s.Doc.ID] += 1 / (rrfK + float64(rank) + 1)
		}
	}
	out := make([]Score, 0, len(scores))
	for id, sc := range scores {
		out = append(out, Score{Doc: byID[id], Sim: sc})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sim != out[j].Sim {
			return out[i].Sim > out[j].Sim
		}
		return out[i].Doc.ID < out[j].Doc.ID
	})
	return out
}
