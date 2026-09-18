// Prose→symbol vocab: a build-time inverted word→symbol table so
// agents can skip the miss-chain (kern_search miss → kern_ast_search miss).
// The Index.ProseVocab field is declared in engine.go's Index struct (Go
// structs cannot be extended across files); this file owns the build, the
// lookup, and the cap that keeps common words from blowing up the index.

package index

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// proseVocabMaxPerWord caps how many symbols any single prose word maps to.
// Common words like "new" or "get" would otherwise accumulate tens of
// thousands of entries and blow up the serialized index; 200 candidates is
// far more than a disambiguation list needs.
const proseVocabMaxPerWord = 200

// ProseHit is one LookupProse result: a candidate symbol full name and the
// number of DISTINCT query words that matched it.
type ProseHit struct {
	Symbol  string `json:"symbol"`
	Matched int    `json:"matched"`
}

// buildProseVocab builds ProseVocab (word → symbol full names) from every
// symbol in the index. Words come from s.Name (camelCase/snake/dot-segment
// tokens, lowercased) plus the basename of the defining file's parent
// directory when that basename is a useful word. Segments shorter than 3
// chars are dropped ("to", "id"); words are deduped per symbol; each word's
// list is capped at proseVocabMaxPerWord symbols and sorted, so the table is
// deterministic. Called at the end of every finalize sequence (buildSerial,
// buildParallel, Update) before the index is returned or saved.
func (ix *Index) buildProseVocab() {
	vocab := make(map[string][]string)
	for _, s := range ix.Symbols {
		full := s.FullName()
		seen := map[string]bool{}
		var words []string
		add := func(w string) {
			if w == "" || seen[w] {
				return
			}
			seen[w] = true
			words = append(words, w)
		}
		for _, w := range proseWords(s.Name) {
			add(w)
		}
		add(usefulDirWord(s.File))
		for _, w := range words {
			if len(vocab[w]) < proseVocabMaxPerWord {
				vocab[w] = append(vocab[w], full)
			}
		}
	}
	for w := range vocab {
		sort.Strings(vocab[w])
	}
	ix.ProseVocab = vocab
}

// LookupProse maps prose words to candidate symbols via the build-time
// inverted vocab. The query is tokenized with the same ≥3-char rule as
// buildProseVocab; each candidate scores the number of DISTINCT query words
// that matched it. Results rank by Matched desc, then symbol name asc, capped
// at limit (default 20 when ≤0). A nil ProseVocab (an index built before this
// feature) returns an empty result without panicking.
func (ix *Index) LookupProse(query string, limit int) []ProseHit {
	if ix.ProseVocab == nil {
		return nil
	}
	words := proseWords(query)
	if len(words) == 0 {
		return nil
	}
	// Count DISTINCT query words per candidate: the same full name may appear
	// several times in one word's list (several Symbol entries share it), and
	// a candidate must score 1 per matching word, not per list entry.
	matched := make(map[string]map[string]bool) // symbol -> word -> true
	add := func(word, vocabWord string) {
		for _, sym := range ix.ProseVocab[vocabWord] {
			if matched[sym] == nil {
				matched[sym] = map[string]bool{}
			}
			matched[sym][word] = true
		}
	}
	for _, w := range words {
		add(w, w)
		if _, exact := ix.ProseVocab[w]; !exact {
			// Light stemming: "greeting" resolves to the vocab word "greet",
			// "indexes" to "index". Deterministic suffix stripping only.
			for _, stem := range proseStems(w) {
				add(w, stem)
			}
		}
	}
	hits := make([]ProseHit, 0, len(matched))
	for sym, ws := range matched {
		hits = append(hits, ProseHit{Symbol: sym, Matched: len(ws)})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Matched != hits[j].Matched {
			return hits[i].Matched > hits[j].Matched
		}
		return hits[i].Symbol < hits[j].Symbol
	})
	if limit <= 0 {
		limit = 20
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// proseWords lowercases name and splits it into prose words of length ≥ 3.
// Split points: lowercase→uppercase boundaries ("OrderStateMachine" →
// "order", "state", "machine"), the separators _ - . / and space, and digit
// boundaries ("extract2x" → "extract"; the "2x" fragment is dropped by the
// length rule). Results are deduplicated and returned in first-seen order.
// Deterministic, stdlib only.

// proseStems returns light-stem variants of a prose word so inflected prose
// ("greeting", "indexes", "running") still resolves to vocab words ("greet",
// "index", "run"). Deterministic suffix stripping: plural/verb endings,
// chained one level deep, with doubled consonants undone ("running" →
// "runn" → "run"). Stems shorter than the vocab's 3-char word rule are
// never produced.
func proseStems(w string) []string {
	var out []string
	seen := map[string]bool{}
	var add func(s string, depth int)
	add = func(s string, depth int) {
		if depth == 0 {
			return
		}
		for _, suf := range []string{"ing", "es", "ed", "s"} {
			if !strings.HasSuffix(s, suf) || len(s) <= len(suf)+2 {
				continue
			}
			stem := strings.TrimSuffix(s, suf)
			if !seen[stem] {
				seen[stem] = true
				out = append(out, stem)
				add(stem, depth-1)
			}
			if len(stem) >= 2 && stem[len(stem)-1] == stem[len(stem)-2] && !isVowelByte(stem[len(stem)-1]) {
				und := stem[:len(stem)-1]
				if !seen[und] {
					seen[und] = true
					out = append(out, und)
					add(und, depth-1)
				}
			}
		}
	}
	add(w, 2)
	return out
}

// isVowelByte reports whether the ASCII byte is a vowel (a, e, i, o, u).
func isVowelByte(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}
	return false
}
func proseWords(name string) []string {
	var out []string
	seen := map[string]bool{}
	var cur []rune
	flush := func() {
		if len(cur) >= 3 {
			w := strings.ToLower(string(cur))
			if !seen[w] {
				seen[w] = true
				out = append(out, w)
			}
		}
		cur = cur[:0]
	}
	isSep := func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == '/' || r == ' '
	}
	var prev rune
	for _, r := range name {
		boundary := isSep(r) ||
			(prev != 0 && unicode.IsLower(prev) && unicode.IsUpper(r)) ||
			(prev != 0 && isDigitRune(prev) != isDigitRune(r))
		if boundary {
			// Flush the current token, then start the next one with r (it
			// begins a new segment, e.g. the 'S' in "OrderState"); separators
			// are consumed and contribute nothing.
			flush()
			if !isSep(r) {
				cur = append(cur, r)
			}
		} else {
			cur = append(cur, r)
		}
		prev = r
	}
	flush()
	return out
}

// isDigitRune reports whether r is an ASCII digit.
func isDigitRune(r rune) bool {
	return r >= '0' && r <= '9'
}

// usefulDirWord returns the lowercased basename of file's parent directory
// when it is a useful prose word (at least 3 chars, contains a letter, not a
// hidden directory), else "". Files at the repo root have dir "." and
// contribute no word.
func usefulDirWord(file string) string {
	dir := filepath.Dir(file)
	if dir == "." || dir == "/" || dir == "" {
		return ""
	}
	base := filepath.Base(dir)
	if len(base) < 3 || strings.HasPrefix(base, ".") {
		return ""
	}
	hasLetter := false
	for _, r := range base {
		if unicode.IsLetter(r) {
			hasLetter = true
			break
		}
	}
	if !hasLetter {
		return ""
	}
	return strings.ToLower(base)
}
