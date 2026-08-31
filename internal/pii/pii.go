// Package pii scans text for secrets and personally-identifiable information
// (API keys, passwords, tokens, URLs with credentials, IPs, emails) and swaps
// them for safe placeholders like [MASKED_IP_1]. It is 100% local and
// deterministic — nothing leaves the machine. Placeholders can be mapped back
// to the original values when the masked text needs to be unmasked locally.
package pii

import (
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
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
	{Label: "GITHUB_PAT", RE: regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{60,}\b`)},
	{Label: "SLACK", RE: regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)},
	{Label: "STRIPE", RE: regexp.MustCompile(`\bsk_(?:live|test)_[A-Za-z0-9]{20,}\b`)},
	{Label: "OPENAI", RE: regexp.MustCompile(`\bsk-(?:proj-[A-Za-z0-9-]{20,}|[A-Za-z0-9]{20,})\b`)},
	{Label: "OPENAI_SHORT", RE: regexp.MustCompile(`\bsk-[A-Za-z0-9]{10,19}\b`)},
	{Label: "JWT", RE: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)},
	{Label: "BEARER", RE: regexp.MustCompile(`\bBearer\s+[A-Za-z0-9._~+/=-]{20,}\b`)},
	{Label: "KEY", RE: regexp.MustCompile(`(?i)\b(?:api[_\s-]?key|apikey|auth[_\s-]?token|access[_\s-]?token|refresh[_\s-]?token|client[_\s-]?secret|private[_\s-]?key|app[_\s-]?secret|consumer[_\s-]?(?:key|secret))["']?\s*[=:]\s*["']?(?:[A-Za-z0-9_/\-+=]{12,}["']|[A-Za-z0-9_/\-+=]*[0-9][A-Za-z0-9_/\-+=]*)`)},
	{Label: "PASSWORD", RE: regexp.MustCompile(`(?i)\b(?:password|passwd|pwd)["']?\s*[=:]\s*["']?(?:[A-Za-z0-9_/\-+=@!]{6,}["']|[A-Za-z0-9_/\-+=@!]*[0-9][A-Za-z0-9_/\-+=@!]*)`)},
	{Label: "TOKEN", RE: regexp.MustCompile(`(?i)\b(?:token|secret)["']?\s*[=:]\s*["']?(?:[A-Za-z0-9_/\-+=]{16,}["']|[A-Za-z0-9_/\-+=]*[0-9][A-Za-z0-9_/\-+=]*)`)},
	{Label: "URL_CRED", RE: regexp.MustCompile(`\b[a-zA-Z][a-zA-Z0-9+.-]*://[^/\s:@]+:[^/\s@]+@`)},
	{Label: "IP", RE: regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\b`)},
	{Label: "IPV6", RE: regexp.MustCompile(`(?i)\b(?:[0-9a-fA-F]{1,4}(?::[0-9a-fA-F]{1,4}){0,6}::[0-9a-fA-F]{1,4}(?::[0-9a-fA-F]{1,4}){0,6}|::[0-9a-fA-F]{1,4}(?::[0-9a-fA-F]{1,4}){0,6}|[0-9a-fA-F]{1,4}(?::[0-9a-fA-F]{1,4}){1,6}::)\b`)},
	{Label: "EMAIL", RE: regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+\b`)},
	// PHONE is split into per-shape patterns because RE2 lacks lookarounds
	// and alternation backtracks: a single pattern cannot span the 3-3-4
	// (US), 3-4-4 (UK landline), 5-5 (India) and parenthesized forms. The
	// country-code group requires a "+" or a separator, or it would absorb
	// leading digits of a longer numeric run.
	{Label: "PHONE", RE: regexp.MustCompile(`(?:\+\d{1,3}[\s.-]?|\d{1,3}[\s.-])?\d{3}[\s.-]?\d{3}[\s.-]?\d{3,4}\b`)},
	{Label: "PHONE", RE: regexp.MustCompile(`\(\d{2,4}\)[\s.-]?\d{3}[\s.-]?\d{4}\b`)},
	{Label: "PHONE", RE: regexp.MustCompile(`(?:\+\d{1,3}[\s.-]?|\d{1,3}[\s.-])?\d{5}[\s.-]?\d{5}\b`)},
	{Label: "PHONE", RE: regexp.MustCompile(`\b0\d{2}\s\d{4}\s\d{4}\b`)},
	{Label: "PHONE", RE: regexp.MustCompile(`\b0\d{4}\s\d{6}\b`)},
	{Label: "PHONE", RE: regexp.MustCompile(`\+\d{1,3}\s?\d{2}\s\d{4}\s\d{4}\b`)},
	{Label: "SSN", RE: regexp.MustCompile(`\b[0-9]{3}-[0-9]{2}-[0-9]{4}\b`)},
}

// Encoding pre-pass regexes. These detect runs of encoded text that would
// otherwise let a secret slip past the plain-text patterns above. The minimum
// run lengths keep false positives out of ordinary prose and image data. The
// actual decode + secret check happens in maskEncoded; the regex only locates
// candidate runs.
var (
	reB64 = regexp.MustCompile(`[A-Za-z0-9+/]{20,}={0,2}`)
	reHex = regexp.MustCompile(`[0-9a-fA-F]{32,}`)
	rePct = regexp.MustCompile(`(?:%[0-9a-fA-F]{2}){8,}`)
	reUni = regexp.MustCompile(`(?:(?:\\u[0-9a-fA-F]{4}){4,}|(?:\\x[0-9a-fA-F]{2}){8,})`)
)

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

// IsVersionLike reports whether s looks like a semantic-version string
// (e.g. "2.1.4", "1.0") rather than a domain. CDNs use pkg@version URLs that
// trigger the EMAIL pattern; the part after @ is a version, not an email host.
// Exported so the security scanner can apply the same filter to EMAIL findings.
func IsVersionLike(s string) bool {
	// Must start with a digit and contain only digits and dots.
	if s == "" || s[0] < '0' || s[0] > '9' {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && c != '.' {
			return false
		}
	}
	return true
}

// IsNonSecretIP reports whether a hit from the IP/IPV6 patterns is an address
// that should not be treated as a secret: loopback, RFC1918/ULA private
// ranges, link-local, multicast and unspecified addresses are all ubiquitous
// in code (local dev configs, comments, tests) and not secrets.
func IsNonSecretIP(label string, hit string) bool {
	if label != "IP" && label != "IPV6" {
		return false
	}
	ip := net.ParseIP(hit)
	if ip == nil {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
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
	return maskCustom(text, patterns, names, true)
}

// MaskAll is like Mask but also masks private/loopback IPs. Use for PII
// masking before sending text to a remote LLM, where even private IPs are PII.
func MaskAll(text string) Result {
	return MaskAllCustom(text, DefaultPatterns, nil)
}

// MaskAllCustom is like MaskCustom but does not suppress private IPs.
func MaskAllCustom(text string, patterns []Pattern, names []string) Result {
	return maskCustom(text, patterns, names, false)
}

// maskCustom is the shared masking implementation. When suppressNonSecretIPs
// is true, loopback/private IPs (ubiquitous in code and not secrets) stay
// unmasked; when false every IP is masked, as needed when sending text to a
// remote LLM where even private IPs are PII.
func maskCustom(text string, patterns []Pattern, names []string, suppressNonSecretIPs bool) Result {
	// Encoding pre-pass: secrets hidden behind base64/hex/percent/unicode
	// encodings would otherwise bypass the plain-text regex patterns below.
	// maskEncoded decodes each candidate run, checks the decoded content
	// against the secret patterns, and — only on a match — replaces the
	// ENCODED form (what actually appears in the input) with a
	// [MASKED_<KIND>_N] placeholder, recording it for reverse Unmask.
	encMapping := make(map[string]string)
	reList := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		reList = append(reList, p.RE)
	}
	text = maskEncoded(text, reList, encMapping)

	type hit struct {
		start, end int
		label      string
	}
	var hits []hit
	for _, p := range patterns {
		for _, m := range p.RE.FindAllStringIndex(text, -1) {
			if suppressNonSecretIPs && IsNonSecretIP(p.Label, text[m[0]:m[1]]) {
				continue
			}
			// CDNs use pkg@version URLs (e.g. boxicons@2.1.4) whose "@2.1.4"
			// looks like an email host to the EMAIL pattern. A domain part that
			// is a semantic-version string is not an email address.
			if p.Label == "EMAIL" && IsVersionLike(text[m[0]:m[1]][strings.IndexByte(text[m[0]:m[1]], '@')+1:]) {
				continue
			}
			// The PHONE shapes are unanchored at the start (a leading \b
			// cannot precede "(" or "+"), so a longer digit run would match
			// mid-number. Reject any PHONE hit that starts inside a digit.
			if p.Label == "PHONE" && m[0] > 0 && text[m[0]-1] >= '0' && text[m[0]-1] <= '9' {
				continue
			}
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
	for ph, orig := range encMapping {
		res.Mapping[ph] = orig
		res.Replaced++
		res.ByLabel[labelOf(ph)]++
	}
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

// maskEncoded scans text for runs of encoded secrets (base64, hex,
// percent-encoding, unicode escapes). For each run it decodes the content and
// checks the decoded bytes against the given secret patterns; only on a match
// does it replace the ENCODED run (the form present in the input) with a
// [MASKED_<KIND>_N] placeholder recorded in mapping for Unmask. It never masks
// encoded content whose decoded bytes are not a secret, so legitimate code and
// base64 image data are left untouched. Decode errors are skipped silently.
func maskEncoded(text string, patterns []*regexp.Regexp, mapping map[string]string) string {
	type cand struct {
		start, end int
		kind       string // b64, hex, pct, uni
	}
	var cands []cand
	scan := func(re *regexp.Regexp, kind string) {
		for _, m := range re.FindAllStringIndex(text, -1) {
			decoded, ok := decodeEncoded(kind, text[m[0]:m[1]])
			if !ok {
				continue
			}
			if matchesSecret(decoded, patterns) {
				cands = append(cands, cand{m[0], m[1], kind})
			}
		}
	}
	scan(reB64, "b64")
	scan(reHex, "hex")
	scan(rePct, "pct")
	scan(reUni, "uni")

	if len(cands) == 0 {
		return text
	}

	// Longest-first ordering keeps an enclosing run over a nested one, then
	// greedy selection drops any candidate overlapping an already-chosen one.
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].start != cands[j].start {
			return cands[i].start < cands[j].start
		}
		return cands[i].end > cands[j].end
	})
	var chosen []cand
	lastEnd := -1
	for _, c := range cands {
		if lastEnd >= 0 && c.start < lastEnd {
			continue
		}
		chosen = append(chosen, c)
		lastEnd = c.end
	}

	counts := map[string]int{}
	var b strings.Builder
	b.Grow(len(text))
	prev := 0
	for _, c := range chosen {
		b.WriteString(text[prev:c.start])
		counts[c.kind]++
		ph := "[MASKED_" + strings.ToUpper(c.kind) + "_" + itoa(counts[c.kind]) + "]"
		mapping[ph] = text[c.start:c.end]
		b.WriteString(ph)
		prev = c.end
	}
	b.WriteString(text[prev:])
	return b.String()
}

// decodeEncoded decodes a candidate run of the given encoding kind. It returns
// ok=false when the run is not actually valid for that encoding, in which case
// the caller skips it.
func decodeEncoded(kind, s string) (string, bool) {
	switch kind {
	case "b64":
		dec, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return "", false
		}
		return string(dec), true
	case "hex":
		dec, err := hex.DecodeString(s)
		if err != nil {
			return "", false
		}
		return string(dec), true
	case "pct":
		dec, err := url.QueryUnescape(s)
		if err != nil {
			return "", false
		}
		return dec, true
	case "uni":
		var b strings.Builder
		for i := 0; i < len(s); {
			if s[i] == '\\' && i+6 <= len(s) && s[i+1] == 'u' {
				cp, err := strconv.ParseUint(s[i+2:i+6], 16, 32)
				if err == nil {
					b.WriteRune(rune(cp))
					i += 6
					continue
				}
			}
			if s[i] == '\\' && i+4 <= len(s) && s[i+1] == 'x' {
				by, err := strconv.ParseUint(s[i+2:i+4], 16, 8)
				if err == nil {
					b.WriteByte(byte(by))
					i += 4
					continue
				}
			}
			b.WriteByte(s[i])
			i++
		}
		return b.String(), true
	}
	return "", false
}

// matchesSecret reports whether the decoded content matches any of the secret
// patterns. This gates masking: encoded runs whose decoded bytes are not a
// secret are left untouched.
func matchesSecret(decoded string, patterns []*regexp.Regexp) bool {
	for _, re := range patterns {
		if re.MatchString(decoded) {
			return true
		}
	}
	return false
}

// labelOf extracts the label from a placeholder like "[MASKED_B64_1]" -> "B64".
func labelOf(ph string) string {
	start := strings.IndexByte(ph, '_') + 1 // after "MASKED_"
	rel := strings.IndexByte(ph[start:], '_')
	return ph[start : start+rel]
}
