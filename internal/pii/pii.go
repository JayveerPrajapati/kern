// Package pii scans text for secrets and personally-identifiable information
// (API keys, passwords, tokens, URLs with credentials, IPs, emails) and swaps
// them for safe placeholders like [MASKED_IP_1]. It is 100% local and
// deterministic — nothing leaves the machine. Placeholders can be mapped back
// to the original values when the masked text needs to be unmasked locally.
package pii

import (
	"os"
	"regexp"
	"sort"
	"strings"
)

// Pattern describes one detection rule.
type Pattern struct {
	Label string
	RE    *regexp.Regexp
}

// DefaultPatterns covers the common secrets and identifiers found in source
// code, logs and documents.
var DefaultPatterns = []Pattern{
	{Label: "PRIVATE_KEY", RE: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`)},
	{Label: "AWS", RE: regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{Label: "AWS_SECRET", RE: regexp.MustCompile(`\b(?:aws_)?secret(?:_access_key)?\s*[=:]\s*["']?[A-Za-z0-9/+=]{40}["']?`)},
	{Label: "GITHUB", RE: regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}\b`)},
	{Label: "SLACK", RE: regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)},
	{Label: "STRIPE", RE: regexp.MustCompile(`\bsk_(?:live|test)_[A-Za-z0-9]{20,}\b`)},
	{Label: "OPENAI", RE: regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`)},
	{Label: "JWT", RE: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)},
	{Label: "BEARER", RE: regexp.MustCompile(`\bBearer\s+[A-Za-z0-9._~+/=-]{20,}\b`)},
	{Label: "KEY", RE: regexp.MustCompile(`\b(?:api[_-]?key|apikey|auth[_-]?token|access[_-]?token|refresh[_-]?token|client[_-]?secret|private[_-]?key|app[_-]?secret|consumer[_-]?(?:key|secret))["']?\s*[=:]\s*["'][A-Za-z0-9_/\-+=]{12,}["']`)},
	{Label: "PASSWORD", RE: regexp.MustCompile(`\b(?:password|passwd|pwd)["']?\s*[=:]\s*["'][A-Za-z0-9_/\-+=@!]{6,}["']`)},
	{Label: "TOKEN", RE: regexp.MustCompile(`\b(?:token|secret)["']?\s*[=:]\s*["'][A-Za-z0-9_/\-+=]{16,}["']`)},
	{Label: "URL_CRED", RE: regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@]+:[^/\s@]+@`)},
	{Label: "IP", RE: regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\b`)},
	{Label: "IPV6", RE: regexp.MustCompile(`\b[0-9a-fA-F]{1,4}::(?:[0-9a-fA-F]{1,4}:){0,6}[0-9a-fA-F]{1,4}\b`)},
	{Label: "EMAIL", RE: regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+\b`)},
	{Label: "SSN", RE: regexp.MustCompile(`\b[0-9]{3}-[0-9]{2}-[0-9]{4}\b`)},
}

// Result of a masking pass.
type Result struct {
	Text     string
	Replaced int
	// Mapping of placeholder -> original value. Placeholders are unique per
	// label (e.g. [MASKED_IP_1], [MASKED_IP_2]) so Unmask can restore text.
	Mapping map[string]string
	// Found is the number of distinct secrets detected (counts by label).
	ByLabel map[string]int
}

// Mask scans text with DefaultPatterns and replaces each secret with a
// [MASKED_LABEL_N] placeholder. Name calls MaskNames with no extra names.
func Mask(text string) Result {
	return MaskCustom(text, DefaultPatterns, nil)
}

// MaskNames is Mask with an additional set of known client/project names to
// mask as [MASKED_NAME_N].
func MaskNames(text string, names []string) Result {
	return MaskCustom(text, DefaultPatterns, names)
}

// MaskCustom scans text with the given patterns plus optional name literals.
// Patterns are applied greedily: overlapping matches keep the longest, so a
// URL-with-credentials is masked before its password could be picked up.
func MaskCustom(text string, patterns []Pattern, names []string) Result {
	type hit struct {
		start, end int
		label      string
	}
	var hits []hit
	for _, p := range patterns {
		for _, m := range p.RE.FindAllStringIndex(text, -1) {
			hits = append(hits, hit{m[0], m[1], p.Label})
		}
	}
	for _, n := range names {
		if n == "" {
			continue
		}
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(n) + `\b`)
		for _, m := range re.FindAllStringIndex(text, -1) {
			hits = append(hits, hit{m[0], m[1], "NAME"})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].start != hits[j].start {
			return hits[i].start < hits[j].start
		}
		return hits[i].end > hits[j].end
	})

	// Greedy selection: keep the longest match at each position, drop any hit
	// that overlaps an already-selected one.
	var chosen []hit
	var lastEnd = -1
	for _, h := range hits {
		if lastEnd >= 0 && h.start < lastEnd {
			continue
		}
		chosen = append(chosen, h)
		lastEnd = h.end
	}

	res := Result{Mapping: map[string]string{}, ByLabel: map[string]int{}}
	if len(chosen) == 0 {
		res.Text = text
		return res
	}
	counts := map[string]int{}
	placeholders := make([]string, len(chosen))
	// Build placeholders first so ordering is deterministic.
	for i, h := range chosen {
		counts[h.label]++
		ph := "[MASKED_" + h.label + "_" + itoa(counts[h.label]) + "]"
		placeholders[i] = ph
		res.Mapping[ph] = text[h.start:h.end]
		res.ByLabel[h.label]++
		res.Replaced++
	}
	// Rebuild text from the end so positions stay valid.
	var b strings.Builder
	b.Grow(len(text))
	prev := 0
	for i, h := range chosen {
		b.WriteString(text[prev:h.start])
		b.WriteString(placeholders[i])
		prev = h.end
	}
	b.WriteString(text[prev:])
	res.Text = b.String()
	return res
}

// Unmask restores original values for every placeholder in text using r's
// mapping. Unknown placeholders are left untouched.
func (r Result) Unmask(text string) string {
	for ph, orig := range r.Mapping {
		text = strings.ReplaceAll(text, ph, orig)
	}
	return text
}

// MaskFile reads path (or stdin when path is "-") and returns its masked text.
func MaskFile(path string) (Result, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Result{}, err
	}
	return Mask(string(b)), nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
