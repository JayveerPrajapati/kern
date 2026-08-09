package intel

import (
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// RankedSearch returns symbols matching a free-text query, ranked by how
// strongly they match: exact name > exact full name > whole-segment match >
// prefix > plural/diacritic-folded segment > substring in name > substring in
// full name > file-name hit. Multi-word queries are split and symbols matching
// more words rank higher. Query words are normalized (lowercased, Latin
// diacritics stripped) and camelCase query words are split into segments so
// prose like "state machine" or "résolution" hits OrderStateMachine /
// resolveUnresolved without a keyword list. Symbols whose file was marked
// generated at index time are penalized so hand-written implementations of the
// same name rank first (generated stubs like `.pb.go` output frequently shadow
// real code otherwise); they remain reachable, just demoted. Unlike
// Index.Search (AST wildcard patterns) this is forgiving free-text lookup, so
// a human can type "load index" or "readindex" and get useful anchors.
func RankedSearch(ix *index.Index, query string, limit int) []index.Symbol {
	if limit <= 0 {
		limit = 50
	}
	words := queryWords(query)
	if len(words) == 0 {
		return nil
	}
	segCache := map[string][]string{}
	type hit struct {
		s     index.Symbol
		score int
	}
	var hits []hit
	for _, s := range ix.Symbols {
		name := strings.ToLower(s.Name)
		full := strings.ToLower(s.FullName())
		file := strings.ToLower(s.File)
		segs, ok := segCache[s.Name]
		if !ok {
			segs = splitIdentifierSegments(s.Name)
			segCache[s.Name] = segs
		}
		var score, matched int
		for _, w := range words {
			sc := matchWord(w, name, full, file, segs)
			if sc > 0 {
				score += sc
				matched++
			}
		}
		if matched == 0 {
			continue
		}
		if matched == len(words) {
			score += 150 // matches every token: boost strongly
		}
		if ix.IsGenerated(s.File) {
			score -= 60 // demote generated stubs below real implementations
		}
		hits = append(hits, hit{s: s, score: score})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		if hits[i].s.FullName() != hits[j].s.FullName() {
			return hits[i].s.FullName() < hits[j].s.FullName()
		}
		return hits[i].s.File < hits[j].s.File
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]index.Symbol, len(hits))
	for i, h := range hits {
		out[i] = h.s
	}
	return out
}

// queryWords splits and normalizes a free-text query into matchable words:
// separated on any non-letter/digit run ("state-machine", "load index") AND on
// camelCase humps ("stateMachine" -> state/machine), then lowercased with
// Latin diacritics stripped. Splitting before lowercasing preserves the hump
// signal so a camelCase query names its concept in whole segments.
func queryWords(query string) []string {
	seen := map[string]bool{}
	var words []string
	for _, run := range wordRuns(query) {
		for _, part := range splitCamel(run) {
			w := normalizeProseWord(part)
			if w == "" {
				continue
			}
			if !seen[w] {
				seen[w] = true
				words = append(words, w)
			}
		}
	}
	return words
}

// matchWord scores how strongly one normalized query word matches a symbol.
// Only the strongest tier for the word counts. Segment tiers are whole-word
// evidence: an exact segment match outranks prefix/substring hits, and a
// plural-folded segment ("services" -> service) still hits its symbol.
func matchWord(w, name, full, file string, segs []string) int {
	switch {
	case name == w:
		return 100
	case full == w:
		return 95
	case segmentExact(w, segs):
		return 90
	case strings.HasPrefix(full, w):
		return 80
	case strings.HasPrefix(name, w):
		return 70
	case segmentFolded(w, segs):
		return 66
	case strings.Contains(name, w):
		return 60
	case strings.Contains(full, w):
		return 45
	case strings.Contains(file, w):
		return 20
	}
	return 0
}

func segmentExact(w string, segs []string) bool {
	for _, seg := range segs {
		if seg == w {
			return true
		}
	}
	return false
}

// segmentFolded matches when a plural variant of the query word equals a
// segment ("services" hitting the segment "service"). The query word itself is
// already diacritic-normalized upstream.
func segmentFolded(w string, segs []string) bool {
	for _, v := range segmentLookupVariants(w) {
		if v == w {
			continue
		}
		if segmentExact(v, segs) {
			return true
		}
	}
	return false
}
