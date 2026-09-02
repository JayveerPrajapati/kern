// TiktokenCounter: exact token counting for OpenAI's cl100k_base and
// o200k_base encodings using the official BPE rank tables, embedded
// gzipped so the binary stays self-contained and offline.
//
// The tables are the canonical files published by the tiktoken project
// (MIT license, https://github.com/openai/tiktoken):
//
//	cl100k_base.tiktoken  sha256 223921b7...  (100,256 mergeable ranks)
//	o200k_base.tiktoken   sha256 446a9538...  (199,998 mergeable ranks)
//
// Counting follows the tiktoken 0.14.0 reference semantics exactly:
// the encoding's pre-tokenizer regex splits the input, then each
// pre-token is reduced by the standard lowest-rank byte-pair merge.
// The pre-tokenizers are hand-rolled scanners (stdlib-only) because the
// reference patterns use possessive quantifiers and lookaheads that
// Go's RE2 regexp engine does not support.
package tokenize

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

//go:embed data/cl100k_base.tiktoken.gz
var cl100kGZ []byte

//go:embed data/o200k_base.tiktoken.gz
var o200kGZ []byte

// Special tokens per encoding (each occurrence counts as exactly one
// token, matching encode(..., allowed_special="all")).
var cl100kSpecials = []string{
	"<|endoftext|>", "<|fim_prefix|>", "<|fim_middle|>",
	"<|fim_suffix|>", "<|end_of_prompt|>",
}

var o200kSpecials = []string{"<|endoftext|>", "<|end_of_prompt|>"}

// TiktokenCounter counts tokens with a real byte-level BPE merge table.
// It implements Counter; the content Kind is ignored (the encoding is
// exact, so density heuristics do not apply).
type TiktokenCounter struct {
	name     string
	vocab    map[string]int
	specials []string
	o200k    bool
}

var (
	cl100kOnce sync.Once
	cl100kCtr  *TiktokenCounter
	cl100kErr  error

	o200kOnce sync.Once
	o200kCtr  *TiktokenCounter
	o200kErr  error
)

// NewCl100kCounter returns the cl100k_base counter (GPT-3.5/4 and a
// close approximation for other vendors). The rank table is decompressed
// once and shared.
func NewCl100kCounter() (*TiktokenCounter, error) {
	cl100kOnce.Do(func() {
		cl100kCtr, cl100kErr = newTiktoken("cl100k_base", cl100kGZ, cl100kSpecials, false)
	})
	return cl100kCtr, cl100kErr
}

// NewO200kCounter returns the o200k_base counter (GPT-4o and newer).
func NewO200kCounter() (*TiktokenCounter, error) {
	o200kOnce.Do(func() {
		o200kCtr, o200kErr = newTiktoken("o200k_base", o200kGZ, o200kSpecials, true)
	})
	return o200kCtr, o200kErr
}

func newTiktoken(name string, gz []byte, specials []string, o200k bool) (*TiktokenCounter, error) {
	vocab, err := loadVocab(gz)
	if err != nil {
		return nil, fmt.Errorf("tokenize: load %s: %w", name, err)
	}
	return &TiktokenCounter{name: name, vocab: vocab, specials: specials, o200k: o200k}, nil
}

// Name returns the encoding name ("cl100k_base" or "o200k_base").
func (t *TiktokenCounter) Name() string { return t.name }

// Count returns the exact number of tokens in s under this encoding.
func (t *TiktokenCounter) Count(s string) int {
	if s == "" {
		return 0
	}
	total := 0
	rest := s
	for {
		idx, length := findSpecial(rest, t.specials)
		if idx < 0 {
			total += t.countText(rest)
			break
		}
		total += t.countText(rest[:idx]) + 1 // one token per special
		rest = rest[idx+length:]
	}
	return total
}

// findSpecial returns the byte index and length of the earliest special
// token occurrence in s, or (-1, 0).
func findSpecial(s string, specials []string) (int, int) {
	best, bestLen := -1, 0
	for _, sp := range specials {
		if i := strings.Index(s, sp); i >= 0 && (best < 0 || i < best || (i == best && len(sp) > bestLen)) {
			best, bestLen = i, len(sp)
		}
	}
	return best, bestLen
}

// countText pre-tokenizes and BPE-counts a special-free segment.
func (t *TiktokenCounter) countText(s string) int {
	total := 0
	if t.o200k {
		o200kWords(s, func(w []byte) { total += t.countWord(w) })
	} else {
		cl100kWords(s, func(w []byte) { total += t.countWord(w) })
	}
	return total
}

// countWord reduces one pre-token by repeatedly merging the adjacent
// pair whose byte concatenation has the lowest rank (the tiktoken
// byte_pair_merge algorithm). Returns the resulting piece count.
func (t *TiktokenCounter) countWord(w []byte) int {
	n := len(w)
	if n <= 1 {
		return n
	}
	// parts[i]:pieces[i+1] is piece i; pieces = len(parts)-1.
	parts := make([]int32, n+1)
	for i := range parts {
		parts[i] = int32(i)
	}
	for len(parts) > 2 {
		bestRank := int(^uint(0) >> 1)
		bestIdx := -1
		for i := 0; i+2 < len(parts); i++ {
			if r, ok := t.vocab[string(w[parts[i]:parts[i+2]])]; ok && r < bestRank {
				bestRank, bestIdx = r, i
			}
		}
		if bestIdx < 0 {
			break
		}
		parts = append(parts[:bestIdx+1], parts[bestIdx+2:]...)
	}
	return len(parts) - 1
}

// loadVocab parses a gzipped .tiktoken rank file (base64 token, rank
// per line) into a bytes-to-rank map.
func loadVocab(gz []byte) (map[string]int, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	vocab := make(map[string]int, 100270)
	scratch := make([]byte, 0, 16)
	for len(data) > 0 {
		line := data
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			data = nil
		}
		sp, rankB, ok := bytes.Cut(line, []byte{' '})
		if !ok {
			return nil, fmt.Errorf("malformed line %q", line)
		}
		rank, err := strconv.Atoi(string(rankB))
		if err != nil {
			return nil, fmt.Errorf("malformed rank in %q", line)
		}
		var decErr error
		scratch, decErr = base64.StdEncoding.AppendDecode(scratch[:0], sp)
		if decErr != nil || len(scratch) == 0 {
			return nil, fmt.Errorf("malformed token in %q", line)
		}
		vocab[string(scratch)] = rank
	}
	if len(vocab) < 256 {
		return nil, fmt.Errorf("suspiciously small vocab: %d entries", len(vocab))
	}
	return vocab, nil
}

// ---------------------------------------------------------------------------
// Pre-tokenizers.
//
// Each scanner returns the byte length of the pre-token at the start of
// s (the remaining input). The rules replicate the reference regexes
// branch-for-branch, including their backtracking semantics (the
// comments cite the branch). See tiktoken_ext/openai_public.py.

// cl100kWords splits s per the cl100k_base pattern:
//
//	'(?i:[sdmt]|ll|ve|re)|[^\r\n\p{L}\p{N}]?+\p{L}++|\p{N}{1,3}+|
//	 ?[^\s\p{L}\p{N}]++[\r\n]*+|\s++$|\s*[\r\n]|\s+(?!\S)|\s
func cl100kWords(s string, fn func(word []byte)) {
	for len(s) > 0 {
		n := cl100kWord(s)
		if n <= 0 { // unreachable: every rune matches some branch
			r, sz := utf8.DecodeRuneInString(s)
			if sz == 0 {
				sz = 1
			}
			_ = r
			n = sz
		}
		fn([]byte(s[:n]))
		s = s[n:]
	}
}

func cl100kWord(s string) int {
	// R1: '(?i:[sdmt]|ll|ve|re)
	if s[0] == '\'' {
		r1, sz1 := nextRune(s, 1)
		lo1 := unicode.ToLower(r1)
		if lo1 == 's' || lo1 == 'd' || lo1 == 'm' || lo1 == 't' {
			return 1 + sz1
		}
		if sz1 > 0 {
			r2, sz2 := nextRune(s, 1+sz1)
			lo2 := unicode.ToLower(r2)
			if (lo1 == 'l' && lo2 == 'l') || (lo1 == 'v' && lo2 == 'e') || (lo1 == 'r' && lo2 == 'e') {
				return 1 + sz1 + sz2
			}
		}
	}
	// R2: [^\r\n\p{L}\p{N}]?+\p{L}++   (possessive: no retry without prefix)
	{
		j := 0
		if r, sz := nextRune(s, 0); r >= 0 && r != '\r' && r != '\n' && !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			j = sz
		}
		k := j
		for {
			r, sz := nextRune(s, k)
			if r >= 0 && unicode.IsLetter(r) {
				k += sz
			} else {
				break
			}
		}
		if k > j {
			return k
		}
	}
	// R3: \p{N}{1,3}+
	if n, k := digitRun(s, 0, 3); n > 0 {
		return k
	}
	// R4:  ?[^\s\p{L}\p{N}]++[\r\n]*+
	{
		j := 0
		if s[0] == ' ' {
			j = 1
		}
		k := j
		for {
			r, sz := nextRune(s, k)
			if r >= 0 && !unicode.IsSpace(r) && !unicode.IsLetter(r) && !unicode.IsNumber(r) {
				k += sz
			} else {
				break
			}
		}
		if k > j {
			for {
				r, sz := nextRune(s, k)
				if r == '\r' || r == '\n' {
					k += sz
				} else {
					break
				}
			}
			return k
		}
	}
	// Whitespace branches share the maximal-run scan.
	return cl100kSpace(s)
}

// cl100kSpace implements the whitespace tail of the cl100k pattern:
// \s++$ | \s*[\r\n] | \s+(?!\S) | \s
func cl100kSpace(s string) int {
	run := spaceRun(s)
	if run == 0 {
		return 0
	}
	// \s++$: everything to end-of-input is whitespace.
	if run == len(s) {
		return run
	}
	// \s*[\r\n]: through the last CR/LF inside the run (regex
	// backtracking shrinks \s* from the whole run until a CR/LF sits
	// immediately after it; the longest such split is the last CR/LF).
	if n := throughLastCRLF(s, run); n > 0 {
		return n
	}
	// \s+(?!\S): run minus its last rune (a following non-space rune
	// makes the lookahead fail at full length; backtracking gives back
	// exactly one rune).
	if _, last := utf8.DecodeLastRuneInString(s[:run]); run-last > 0 {
		return run - last
	}
	// \s: single whitespace rune.
	_, sz := nextRune(s, 0)
	return sz
}

// o200kWords splits s per the o200k_base pattern (7 branches; see
// o200kWord for the citation).
func o200kWords(s string, fn func(word []byte)) {
	for len(s) > 0 {
		n := o200kWord(s)
		if n <= 0 {
			r, sz := utf8.DecodeRuneInString(s)
			if sz == 0 {
				sz = 1
			}
			_ = r
			n = sz
		}
		fn([]byte(s[:n]))
		s = s[n:]
	}
}

func o200kWord(s string) int {
	// R1: [^\r\n\p{L}\p{N}]?[U]*[L]+(?i:'s|'t|'re|'ve|'m|'ll|'d)?
	// R2: [^\r\n\p{L}\p{N}]?[U]+[L]*(?i:'s|'t|'re|'ve|'m|'ll|'d)?
	// where U = [Lu Lt Lm Lo M], L = [Ll Lm Lo M].
	{
		j := 0
		if r, sz := nextRune(s, 0); r >= 0 && r != '\r' && r != '\n' && !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			j = sz // optional prefix (retrying without it can never help: prefix runes are never letters)
		}
		upEnd := j
		for {
			r, sz := nextRune(s, upEnd)
			if r >= 0 && upperish(r) {
				upEnd += sz
			} else {
				break
			}
		}
		// R1 with [U]* backtracking: give back one upperish rune at a
		// time (Lm/Lo/M are in both classes) until [L]+ can start.
		for p := upEnd; p >= j; p-- {
			loEnd, ok := lowerRun(s, p)
			if ok {
				return loEnd + o200kSuffix(s[loEnd:])
			}
			if p == j {
				break
			}
		}
		// R2: [U]+[L]*suffix? (no failure mode after [U]+: [L]* and the
		// suffix are optional).
		if upEnd > j {
			loEnd, _ := lowerRun(s, upEnd)
			return loEnd + o200kSuffix(s[loEnd:])
		}
	}
	// R3: \p{N}{1,3}
	if n, k := digitRun(s, 0, 3); n > 0 {
		return k
	}
	// R4:  ?[^\s\p{L}\p{N}]+[\r\n/]*
	{
		j := 0
		if s[0] == ' ' {
			j = 1
		}
		k := j
		for {
			r, sz := nextRune(s, k)
			if r >= 0 && !unicode.IsSpace(r) && !unicode.IsLetter(r) && !unicode.IsNumber(r) {
				k += sz
			} else {
				break
			}
		}
		if k > j {
			for {
				r, sz := nextRune(s, k)
				if r == '\r' || r == '\n' || r == '/' {
					k += sz
				} else {
					break
				}
			}
			return k
		}
	}
	// R5: \s*[\r\n]+ — through the last CR/LF of the run (both \s*
	// backtracking and the + land there; equivalent to cl100k's \s*[\r\n]).
	if run := spaceRun(s); run > 0 {
		if n := throughLastCRLF(s, run); n > 0 {
			return n
		}
		// R6: \s+(?!\S) — at end of input the lookahead succeeds and the
		// whole run is one pre-token; otherwise the run minus its last
		// rune (a following non-space rune fails the lookahead at full
		// length; backtracking gives back exactly one rune).
		if run == len(s) {
			return run
		}
		if _, last := utf8.DecodeLastRuneInString(s[:run]); run-last > 0 {
			return run - last
		}
		// R7: \s+ — the single remaining whitespace rune.
		_, sz := nextRune(s, 0)
		return sz
	}
	return 0
}

// o200kSuffix matches (?i:'s|'t|'re|'ve|'m|'ll|'d)? at the start of s
// and returns its byte length (0 when absent). Branch first-letters are
// distinct, so alternation order is irrelevant.
func o200kSuffix(s string) int {
	if len(s) == 0 || s[0] != '\'' {
		return 0
	}
	try := func(word string) int {
		off := 1
		for _, want := range word {
			r, sz := nextRune(s, off)
			if r < 0 || sz == 0 || unicode.ToLower(r) != want {
				return 0
			}
			off += sz
		}
		return off
	}
	for _, cand := range []string{"s", "t", "re", "ve", "m", "ll", "d"} {
		if n := try(cand); n > 0 {
			return n
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// Rune helpers.

// nextRune decodes the rune at s[i]; returns (-1, 0) at end of input.
func nextRune(s string, i int) (rune, int) {
	if i >= len(s) {
		return -1, 0
	}
	r, sz := utf8.DecodeRuneInString(s[i:])
	if sz == 0 {
		sz = 1
	}
	return r, sz
}

// upperish reports membership in [Lu Lt Lm Lo M] (o200k "uppercase" class).
func upperish(r rune) bool {
	return unicode.In(r, unicode.Lu, unicode.Lt, unicode.Lm, unicode.Lo, unicode.M)
}

// lowerish reports membership in [Ll Lm Lo M] (o200k "lowercase" class).
func lowerish(r rune) bool {
	return unicode.In(r, unicode.Ll, unicode.Lm, unicode.Lo, unicode.M)
}

// spaceRun returns the byte length of the maximal whitespace prefix.
func spaceRun(s string) int {
	k := 0
	for {
		r, sz := nextRune(s, k)
		if r >= 0 && unicode.IsSpace(r) {
			k += sz
		} else {
			break
		}
	}
	return k
}

// throughLastCRLF returns the end offset of the last CR/LF inside
// s[:run] (the \s*[\r\n]+ backtrack point), or 0 when the run holds no
// CR/LF.
func throughLastCRLF(s string, run int) int {
	last := 0
	for pos := 0; pos < run; {
		r, sz := nextRune(s, pos)
		if r == '\r' || r == '\n' {
			last = pos + sz
		}
		pos += sz
	}
	return last
}

// digitRun consumes up to max unicode-number runes from s[at:] and
// returns (count, endOffset).
func digitRun(s string, at, max int) (int, int) {
	k, n := at, 0
	for n < max {
		r, sz := nextRune(s, k)
		if r >= 0 && unicode.IsNumber(r) {
			k += sz
			n++
		} else {
			break
		}
	}
	return n, k
}

// lowerRun consumes the maximal lowerish rune run from s[at:] and
// returns (endOffset, matchedAtLeastOne).
func lowerRun(s string, at int) (int, bool) {
	k, ok := at, false
	for {
		r, sz := nextRune(s, k)
		if r >= 0 && lowerish(r) {
			k += sz
			ok = true
		} else {
			break
		}
	}
	return k, ok
}

// VocabSize reports the number of mergeable ranks in the loaded table
// (100,256 for cl100k_base, 199,998 for o200k_base).
func (t *TiktokenCounter) VocabSize() int { return len(t.vocab) }
