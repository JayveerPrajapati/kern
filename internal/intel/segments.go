package intel

import (
	"strings"
	"unicode"
)

// Identifier-segment utilities: symbol names split into the words a human
// would use in prose, and prose normalized into candidate words to match those
// segments with. "OrderStateMachine" -> order/state/machine, so a query like
// "state machine" can hit it without a keyword list ever knowing the words.

// Bounds keep degenerate identifiers (minified names, hashes) from bloating the
// vocabulary: segments outside them carry no prose signal anyway.
const (
	minSegmentChars    = 2
	maxSegmentChars    = 32
	maxSegmentsPerName = 12
	minProseChars      = 4
	maxProseChars      = 24
)

// splitIdentifierSegments splits a symbol or file name into lowercase word
// segments. Handles camelCase / PascalCase (inner lower->Upper), acronym runs
// ("HTMLParser" -> html/parser), snake_case / kebab-case / dotted names
// (non-alphanumerics separate), and keeps digits glued to their word
// ("base64Encode" -> base64/encode). Digit-only fragments are dropped.
func splitIdentifierSegments(name string) []string {
	if name == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, run := range wordRuns(name) {
		for _, part := range splitCamel(run) {
			if len(out) >= maxSegmentsPerName {
				return out
			}
			seg := strings.ToLower(part)
			if len(seg) < minSegmentChars || len(seg) > maxSegmentChars {
				continue
			}
			if isAllDigits(seg) {
				continue
			}
			if !seen[seg] {
				seen[seg] = true
				out = append(out, seg)
			}
		}
	}
	return out
}

// splitCamel splits a run of letters/digits at camelCase humps and acronym-run
// ends: "orderState" -> [order, State], "base64Encode" -> [base64, Encode],
// "HTMLParser" -> [HTML, Parser]. Digits act like lowercase for hump detection.
func splitCamel(run string) []string {
	rs := []rune(run)
	if len(rs) <= 1 {
		return []string{run}
	}
	var parts []string
	start := 0
	for i := 1; i < len(rs); i++ {
		prev, cur := rs[i-1], rs[i]
		split := false
		if isUpper(cur) && (isLower(prev) || isDigit(prev)) {
			split = true // camelCase hump: lower|digit -> Upper
		} else if isUpper(cur) && isUpper(prev) && i+1 < len(rs) && isLower(rs[i+1]) {
			split = true // acronym run end: Upper Upper ... followed by Lower
		}
		if split {
			parts = append(parts, string(rs[start:i]))
			start = i
		}
	}
	parts = append(parts, string(rs[start:]))
	return parts
}

// wordRuns splits s into maximal runs of letters and digits; anything else
// (spaces, hyphens, underscores, dots) is a separator.
func wordRuns(s string) []string {
	var runs []string
	start := -1
	for i, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = i
			}
		} else if start >= 0 {
			runs = append(runs, s[start:i])
			start = -1
		}
	}
	if start >= 0 {
		runs = append(runs, s[start:])
	}
	return runs
}

func isUpper(r rune) bool { return unicode.IsUpper(r) }
func isLower(r rune) bool { return unicode.IsLower(r) }
func isDigit(r rune) bool { return unicode.IsDigit(r) }
func isAllDigits(s string) bool {
	for _, r := range s {
		if !isDigit(r) {
			return false
		}
	}
	return true
}

// normalizeProseWord lowercases and strips Latin diacritics so "résolution"
// matches the segment "resolution". Identifiers are overwhelmingly ASCII; this
// is what buys Latin-script languages their cross-lingual reach on loanwords.
func normalizeProseWord(word string) string {
	return strings.ToLower(stripDiacritics(word))
}

// accentMap maps common accented Latin letters to their base letter. Kept as a
// dependency-free stand-in for NFD decomposition; covers the diacritics that
// actually appear in technical prose (é, ü, ñ, ç, å, ...).
var accentMap = func() map[rune]rune {
	const accented = "ÀÁÂÃÄÅàáâãäåÈÉÊËèéêëÌÍÎÏìíîïÒÓÔÕÖòóôõöÙÚÛÜùúûüÑñÇç"
	const base = "AAAAAAaaaaaaEEEEeeeeIIIIiiiiOOOOOoooooUUUUuuuuNnCc"
	aa, bb := []rune(accented), []rune(base)
	if len(aa) != len(bb) {
		panic("accent map misaligned")
	}
	m := make(map[rune]rune, len(aa))
	for i, a := range aa {
		m[a] = bb[i]
	}
	return m
}()

func stripDiacritics(s string) string {
	var b strings.Builder
	for _, r := range s {
		if base, ok := accentMap[r]; ok {
			b.WriteRune(base)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// segmentLookupVariants returns lookup variants for a prose word: the word
// itself plus light plural folding ("services" -> service). Only unambiguous
// English plural spellings are stripped; a trailing -ss is a singular
// (class, process) and never stripped.
func segmentLookupVariants(word string) []string {
	variants := []string{word}
	canStrip2 := len(word) >= minProseChars+2
	canStrip1 := len(word) >= minProseChars+1
	switch {
	case hasSuffixAny(word, "xes", "shes", "sses", "zzes"):
		if canStrip2 {
			variants = append(variants, word[:len(word)-2])
		}
	case hasSuffixAny(word, "ches", "ses", "zes", "oes"):
		if canStrip2 {
			variants = append(variants, word[:len(word)-2])
		}
		if canStrip1 {
			variants = append(variants, word[:len(word)-1])
		}
	case strings.HasSuffix(word, "s") && !strings.HasSuffix(word, "ss"):
		if canStrip1 {
			variants = append(variants, word[:len(word)-1])
		}
	}
	return variants
}

func hasSuffixAny(s string, suffixes ...string) bool {
	for _, suf := range suffixes {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}
