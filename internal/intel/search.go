package intel

import (
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// RankedSearch returns symbols matching a free-text query, ranked by how
// strongly they match: exact name > exact full name > prefix > substring in
// name > substring in full name > file-name hit. Multi-word queries are split
// and symbols matching more words rank higher. Unlike Index.Search (AST
// wildcard patterns) this is forgiving free-text lookup, so a human can type
// "load index" or "readindex" and get useful anchors.
func RankedSearch(ix *index.Index, query string, limit int) []index.Symbol {
	if limit <= 0 {
		limit = 50
	}
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil
	}
	type hit struct {
		s     index.Symbol
		score int
	}
	var hits []hit
	for _, s := range ix.Symbols {
		name := strings.ToLower(s.Name)
		full := strings.ToLower(s.FullName())
		file := strings.ToLower(s.File)
		var score, matched int
		for _, w := range words {
			sc := 0
			switch {
			case name == w:
				sc = 100
			case full == w:
				sc = 95
			case strings.HasPrefix(full, w):
				sc = 80
			case strings.HasPrefix(name, w):
				sc = 70
			case strings.Contains(name, w):
				sc = 60
			case strings.Contains(full, w):
				sc = 45
			case strings.Contains(file, w):
				sc = 20
			}
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
