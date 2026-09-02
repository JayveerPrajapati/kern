// Package compress strips noise from logs and prompts deterministically.
// Everything here is rule-based and offline: no model calls.
package compress

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/terse"
)

var (
	timestampRe  = regexp.MustCompile(`(?i)(^\s*\[?(([\d]{2,4}[-/:][\d]{2}[-/:][\d]{2,4}[ T])?[\d]{2}:[\d]{2}(:[\d]{2}([\.,]\d+)?)?(Z|[+-][\d:]{2,5})?)\]?\s*)`)
	infoLevelRe  = regexp.MustCompile(`(?i)(^\s*(INFO|DEBUG|TRACE|VERBOSE|NOTICE)\s*[:=#]?\s*)`)
	warnLevelRe  = regexp.MustCompile(`(?i)(^\s*(WARN|WARNING|ERROR|ERR|FAIL|FATAL|CRITICAL|SEVERE|PANIC|EXCEPTION|EXCEPTION\b)\s*[:=#]?\s*)`)
	stackFrameRe = regexp.MustCompile(`^\s*(at |\t|from |\.go:\d+|\.py:\d+|\.java:\d+|\d+\) )`)
	buildErrRe   = regexp.MustCompile(`(?i)(error|failed|failure|undefined|unresolved|exception|cannot find|no such)`)
	separatorRe  = regexp.MustCompile(`(?m)^\s*([-=_#*]{3,}|\.{3,}|[<>]{3,})\s*$`)
	hexRe        = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	uuidRe       = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	goroutineRe  = regexp.MustCompile(`goroutine [0-9]+`)
	numRe        = regexp.MustCompile(`\b[0-9]+\b`)
	ipRe         = regexp.MustCompile(`[0-9]{1,3}(\.[0-9]{1,3}){3}`)
)

const (
	// clusterMergeRatio is the minimum Levenshtein similarity (on normalized
	// lines) for a singleton line to be folded into an existing cluster.
	clusterMergeRatio = 0.85
	// clusterMaxLenDiff is the maximum relative length difference between two
	// normalized lines allowed for a fuzzy merge.
	clusterMaxLenDiff = 0.30
)

// Options controls log compression behaviour.
type Options struct {
	// MaxLines caps the number of output lines.
	MaxLines int
	// KeepContext keeps one neutral line before each error cluster to give
	// surrounding context without retaining full noise.
	KeepContext bool
	// Cluster collapses near-duplicate lines (stack traces differing only in
	// hex addresses, UUIDs, goroutine IDs, standalone numbers or IPs) into one
	// representative line annotated with "(repeated Nx)". The zero value keeps
	// the legacy exact-dedup behaviour.
	Cluster bool
}

// CompressLog reduces noisy log output to its meaningful core: error/warning
// lines, stack traces and build failures. Repeated lines are deduplicated;
// when opts.Cluster is set, near-duplicate lines are additionally folded into
// one representative line with an inline repetition count.
func CompressLog(text string, opts Options) string {
	if opts.MaxLines <= 0 {
		opts.MaxLines = 200
	}
	raw := strings.Split(text, "\n")
	var out []string
	seen := make(map[string]bool)
	blankPend := false

	flush := func() {
		if blankPend && len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
		blankPend = false
	}

	// When clustering is enabled, important lines are collected in full so the
	// repetition counts can span the whole log before the cap is applied.
	var important []string
	for idx, line := range raw {
		normalized := timestampRe.ReplaceAllString(line, "")
		trimmed := strings.TrimSpace(normalized)
		if trimmed == "" {
			blankPend = true
			continue
		}
		if separatorRe.MatchString(trimmed) {
			continue
		}
		importantLine := false
		if stackFrameRe.MatchString(trimmed) || warnLevelRe.MatchString(trimmed) || buildErrRe.MatchString(trimmed) {
			importantLine = true
		}
		if !importantLine && infoLevelRe.MatchString(trimmed) {
			continue
		}
		if !importantLine && !isUsefulLogLine(trimmed) {
			continue
		}
		if opts.Cluster {
			important = append(important, strings.TrimRight(normalized, " \t"))
			continue
		}
		key := trimmed
		if seen[key] {
			continue
		}
		seen[key] = true
		flush()
		out = append(out, strings.TrimRight(normalized, " \t"))
		if len(out) >= opts.MaxLines {
			// Say the cap was hit so a truncated compression is never mistaken
			// for the full log.
			if remaining := len(raw) - idx - 1; remaining > 0 {
				out = append(out, fmt.Sprintf("… (%d lines omitted)", remaining))
			}
			break
		}
	}

	if opts.Cluster {
		out = clusterLines(important)
		if len(out) > opts.MaxLines {
			// The cap applies to the final clustered result.
			keep := out[:opts.MaxLines]
			// Count the raw input lines the surviving clusters represent so
			// the omission message reflects the pre-cluster line count.
			keptInput := 0
			for _, rep := range keep {
				norm := normalizeForCluster(rep)
				for _, l := range important {
					if normalizeForCluster(l) == norm {
						keptInput++
					}
				}
			}
			if remaining := len(raw) - keptInput; remaining > 0 {
				out = append(keep, fmt.Sprintf("… (%d lines omitted)", remaining))
			} else {
				out = keep
			}
		}
	}

	return strings.Join(out, "\n")
}

// normalizeForCluster rewrites volatile tokens (timestamps, goroutine IDs,
// UUIDs, hex addresses, IPs and standalone numbers) to fixed placeholders so
// near-identical lines share a cluster key.
func normalizeForCluster(s string) string {
	n := timestampRe.ReplaceAllString(s, "")
	n = goroutineRe.ReplaceAllString(n, "goroutine N")
	n = uuidRe.ReplaceAllString(n, "UUID")
	n = hexRe.ReplaceAllString(n, "0xH")
	n = ipRe.ReplaceAllString(n, "IP")
	n = numRe.ReplaceAllString(n, "N")
	return strings.TrimSpace(n)
}

// levenshteinRatio returns 1 - dist/max(len(a),len(b)) for the classic
// (rune-aware) Levenshtein distance between a and b.
func levenshteinRatio(a, b string) float64 {
	ar, br := []rune(a), []rune(b)
	la, lb := len(ar), len(br)
	if la == 0 && lb == 0 {
		return 1
	}
	if la == 0 || lb == 0 {
		return 0
	}
	prev := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur := make([]int, lb+1)
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 0
			if ar[i-1] != br[j-1] {
				cost = 1
			}
			m := prev[j] + 1 // deletion
			if v := cur[j-1] + 1; v < m {
				m = v // insertion
			}
			if v := prev[j-1] + cost; v < m {
				m = v // substitution
			}
			cur[j] = m
		}
		prev = cur
	}
	max := la
	if lb > max {
		max = lb
	}
	return 1 - float64(prev[lb])/float64(max)
}

// firstToken returns the first whitespace-delimited token of s.
func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

// clusterLines deterministically collapses near-duplicate lines into clusters.
// Lines sharing a normalized form form one cluster; singleton lines are then
// fuzzy-merged into the first eligible cluster (Levenshtein ratio above the
// threshold, identical first token, similar length). Each cluster contributes
// its first original line, annotated with "(repeated Nx)" when it holds two or
// more members, in first-occurrence order.
func clusterLines(lines []string) []string {
	type cluster struct {
		norm   string
		orig   []string
		merged bool
	}
	order := make([]*cluster, 0, len(lines))
	byNorm := make(map[string]*cluster, len(lines))
	for _, l := range lines {
		n := normalizeForCluster(l)
		if c, ok := byNorm[n]; ok {
			c.orig = append(c.orig, l)
			continue
		}
		c := &cluster{norm: n, orig: []string{l}}
		byNorm[n] = c
		order = append(order, c)
	}

	// Fuzzy merge pass: each singleton folds into the first eligible cluster.
	// The eligibility criteria live in eligible below. Small logs scan every
	// cluster pairwise (the original behaviour); logs above
	// minHashClusterThreshold prune the candidate set with an LSH band index
	// (G-9) so the pass stays sub-quadratic. The band index only decides
	// which pairs are compared — never whether a pair merges — so the banded
	// path cannot merge lines the pairwise path would keep apart.
	eligible := func(c, other *cluster, tok string) bool {
		if other == c || other.merged || len(other.orig) == 0 {
			return false
		}
		if tok != firstToken(other.norm) {
			return false
		}
		diff := len(c.norm) - len(other.norm)
		if diff < 0 {
			diff = -diff
		}
		maxLen := len(c.norm)
		if otherLen := len(other.norm); otherLen > maxLen {
			maxLen = otherLen
		}
		if maxLen > 0 && float64(diff)/float64(maxLen) > clusterMaxLenDiff {
			return false
		}
		return levenshteinRatio(c.norm, other.norm) >= clusterMergeRatio
	}
	banded := len(lines) > minHashClusterThreshold
	var bands *bandIndex
	var linear []int
	if banded {
		norms := make([]string, len(order))
		for i, c := range order {
			norms[i] = c.norm
		}
		bands = newBandIndex(norms)
	} else {
		linear = make([]int, len(order))
		for i := range linear {
			linear[i] = i
		}
	}
	for pos, c := range order {
		if c.merged || len(c.orig) > 1 {
			continue
		}
		tok := firstToken(c.norm)
		candidates := linear
		if banded {
			candidates = bands.candidates(pos)
		}
		for _, j := range candidates {
			other := order[j]
			if !eligible(c, other, tok) {
				continue
			}
			other.orig = append(other.orig, c.orig[0])
			c.merged = true
			break
		}
	}

	out := make([]string, 0, len(order))
	for _, c := range order {
		if c.merged {
			continue
		}
		rep := c.orig[0]
		if len(c.orig) >= 2 {
			rep = strings.TrimRight(rep, " \t") + fmt.Sprintf(" (repeated %dx)", len(c.orig))
		}
		out = append(out, rep)
	}
	return out
}

func isUsefulLogLine(s string) bool {
	// Keep lines that mention files, modules, IPs or key/value pairs; drop pure
	// chatter like heartbeats and periodic counters.
	if len(s) > 200 {
		return true
	}
	lower := strings.ToLower(s)
	if strings.Contains(lower, "heartbeat") || strings.Contains(lower, "keepalive") || strings.Contains(lower, "polling") {
		return false
	}
	if strings.ContainsAny(s, "=:{}[]") || strings.Contains(s, "/") {
		return true
	}
	return len(s) > 40
}

// CompressPrompt normalizes a raw prompt: collapses whitespace, trims repeated
// empty lines and drops leading/trailing filler lines.
func CompressPrompt(text string) string {
	var out []string
	blankPend := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			blankPend = true
			continue
		}
		if blankPend && len(out) > 0 {
			out = append(out, "")
		}
		blankPend = false
		out = append(out, t)
	}
	joined := strings.Join(out, "\n")
	// Collapse runs of consecutive identical lines into a single line.
	lines := strings.Split(joined, "\n")
	final := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if i > 0 && lines[i] == lines[i-1] {
			continue
		}
		final = append(final, lines[i])
	}
	// Strip unambiguous conversational filler (greetings, thanks, hedges).
	// Conservative by design: only clichés that cannot carry technical payload;
	// lines with paths, code, "file:line" or braces always survive.
	cleaned, _ := terse.StripPromptFluff(strings.Join(final, "\n"))
	return cleaned
}
