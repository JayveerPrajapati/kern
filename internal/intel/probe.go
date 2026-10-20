package intel

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// ProbeAnchor is one resolved symbol the probe surfaced from the task text,
// with the minimal context an agent needs about it: definition, who calls it,
// what it calls, and which tests exercise it.
type ProbeAnchor struct {
	Name     string   `json:"name"`
	Resolved string   `json:"resolved"`
	Kind     string   `json:"kind"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Callers  []string `json:"callers,omitempty"`
	Callees  []string `json:"callees,omitempty"`
	Tests    []string `json:"tests,omitempty"`
	// Fuzzy marks an anchor that came from the fuzzy fallback (keyword or
	// camelCase partial match), not an exact index resolution: prose-noise
	// anchors (the word "test" matching Test* functions) must never read as
	// confident identifications. B2, QA 2026-10-15.
	Fuzzy bool `json:"fuzzy,omitempty"`
}

// ProbeReport is the assembled micro-context bundle for a task: every symbol
// named in the task text, resolved against the index, plus the shortest paths
// connecting the first few anchors.
type ProbeReport struct {
	Task      string        `json:"task"`
	MaxTokens int           `json:"max_tokens"`
	Tokens    int           `json:"tokens"`
	Truncated bool          `json:"truncated,omitempty"`
	Anchors   []ProbeAnchor `json:"anchors"`
	Paths     [][]string    `json:"paths,omitempty"`
}

var identRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*`)

// hyphenPairRe matches hyphenated word compounds in task prose
// ("delivery-report") — joined into identifier forms and resolved exactly.
var hyphenPairRe = regexp.MustCompile(`([A-Za-z][a-z0-9]*)-([A-Za-z][a-z0-9]*)`)

// joinVariants returns the identifier spellings a hyphenated prose compound
// can take: camelCase, PascalCase, and snake_case.
func joinVariants(a, b string) []string {
	aUp := strings.ToUpper(a[:1]) + a[1:]
	bUp := strings.ToUpper(b[:1]) + b[1:]
	return []string{a + bUp, aUp + bUp, a + "_" + b}
}

// Probe turns a natural-language task (bug report, prompt, error text) into a
// budget-capped micro-context bundle: it extracts candidate identifiers,
// resolves them against the index, and returns the definition, callers, callees
// and tests for each.
// When no exact identifier resolves, a keyword-based fuzzy fallback scans
// symbol names so natural-language tasks still produce useful context.
func Probe(ix *index.Index, task string, maxTokens int) *ProbeReport {
	if maxTokens <= 0 {
		maxTokens = 4000
	}
	yamlish := isYAMLishTask(task)
	// kindByFull maps each symbol's canonical FullName to its kind, so the
	// anchor filter below can drop non-code symbols (data-file props, docs
	// headings) before they become anchors or path endpoints.
	kindByFull := map[string]string{}
	for _, s := range ix.Symbols {
		if _, ok := kindByFull[s.FullName()]; !ok {
			kindByFull[s.FullName()] = s.Kind
		}
	}
	candidates := map[string]bool{}
	exact := map[string]bool{}
	fuzzyHits := map[string]bool{}
	for _, w := range identRe.FindAllString(task, -1) {
		// Consult the index FIRST: a token that resolves (case-sensitively)
		// to a real symbol wins over the stopword filter, so a symbol whose
		// name collides with a common English/change verb (Add, Get, Run,
		// Set, Make, Call, Fix, ...) is never dropped. Stopword filtering
		// applies only to tokens that do NOT resolve to an index symbol —
		// and, QA 5.6(a), to ALL-LOWERCASE stopword tokens even when they
		// resolve: "on"/"to"/"body" in task prose must not anchor a YAML
		// workflow property that merely shares the spelling.
		if r, ok := Resolve(ix, w); ok {
			if !anchorableCandidate(kindByFull[r], yamlish) {
				continue
			}
			candidates[r] = true
			exact[r] = true
			continue
		}
	}

	// F4 (eval 2026-10-03): hyphenated compounds ("delivery-report",
	// "retry-policy") never resolve as bare tokens. Join each hyphen pair
	// into its camelCase / PascalCase / snake_case forms and resolve those
	// exactly — exact resolution only, so this adds anchors without fuzzy
	// noise.
	for _, m := range hyphenPairRe.FindAllStringSubmatch(task, -1) {
		for _, j := range joinVariants(m[1], m[2]) {
			if r, ok := Resolve(ix, j); ok {
				if !anchorableCandidate(kindByFull[r], yamlish) {
					continue
				}
				candidates[r] = true
				exact[r] = true
				break
			}
		}
	}

	// Fuzzy fallback for natural-language tasks like "decommission a network
	// service": match extracted keywords against symbol names and segments.
	if len(candidates) == 0 {
		keywords := extractKeywords(task)
		for _, kw := range keywords {
			for _, match := range fuzzyMatchSymbols(ix, kw, 5) {
				if !anchorableCandidate(kindByFull[match], yamlish) {
					continue
				}
				candidates[match] = true
				fuzzyHits[match] = true
			}
		}
	} else {
		// F4: the fallback used to fire only when NOTHING resolved, so a
		// task that anchored 1 of 4 symbols never recovered the rest.
		// camelCase-shaped tokens in the raw text that did not resolve
		// exactly (case mismatch, plural, ...) still get a tight fuzzy pass
		// (limit 2) — prose "retryPolicy" must anchor the symbol
		// RetryPolicy.
		for _, tok := range strings.Fields(task) {
			tok = strings.Trim(tok, ".,;:!?\"'()[]{}<>")
			if !isCamelCase(tok) {
				continue
			}
			if _, ok := Resolve(ix, tok); ok {
				continue // already anchored exactly
			}
			for _, match := range fuzzyMatchSymbols(ix, tok, 2) {
				if !anchorableCandidate(kindByFull[match], yamlish) {
					continue
				}
				candidates[match] = true
				fuzzyHits[match] = true
			}
		}
		// F5 (QA 2026-10-19 campaign): the F4 recovery only reaches
		// camelCase-shaped tokens, so an all-prose task like "exec approval
		// TTL bug" anchored just the `approval` struct while
		// CheckExecCommand / execRejectionTTLText stayed unfound. Run the
		// stopword-filtered keywords through a ranked fuzzy pass — a symbol
		// whose name covers MULTIPLE task keywords is a precise
		// identification and anchors unconditionally; a single-keyword match
		// anchors only the most-connected one. Anchors stay labeled Fuzzy
		// (low confidence). A keyword that already anchored exactly is
		// skipped.
		for _, kw := range extractKeywords(task) {
			if r, ok := Resolve(ix, kw); ok && candidates[r] {
				continue
			}
			for _, match := range fuzzyKeywordMatches(ix, kw, task) {
				if !anchorableCandidate(kindByFull[match], yamlish) {
					continue
				}
				candidates[match] = true
				fuzzyHits[match] = true
			}
		}
	}

	var resolved []string
	for r := range candidates {
		resolved = append(resolved, r)
	}
	sort.Strings(resolved)
	if len(resolved) > 12 {
		resolved = resolved[:12]
	}

	meta := map[string]index.Symbol{}
	for _, s := range ix.Symbols {
		if _, ok := meta[s.FullName()]; !ok {
			meta[s.FullName()] = s
		}
	}
	local := localNames(ix) // hoisted: localCalleesWith is O(V) if rebuilt per anchor

	var anchors []ProbeAnchor
	for _, r := range resolved {
		a := ProbeAnchor{Name: simpleName(r), Resolved: r, Fuzzy: fuzzyHits[r] && !exact[r]}
		if s, ok := meta[r]; ok {
			a.Kind = s.Kind
			a.File = s.File
			a.Line = s.Line
		}
		for _, c := range ix.Callers[r] {
			if c != r && !contains(a.Callers, c) {
				a.Callers = append(a.Callers, c)
			}
		}
		for _, c := range localCalleesWith(ix, r, local) {
			if c != r && !contains(a.Callees, c) {
				a.Callees = append(a.Callees, c)
			}
		}
		for _, c := range ix.Callers[r] {
			if c == r {
				continue
			}
			if s, ok := meta[c]; ok && IsTestFile(s.File) {
				a.Tests = append(a.Tests, c)
			}
		}
		if len(a.Callers) > 12 {
			a.Callers = a.Callers[:12]
		}
		if len(a.Callees) > 12 {
			a.Callees = a.Callees[:12]
		}
		anchors = append(anchors, a)
	}

	var paths [][]string
	for i := 0; i+1 < len(resolved) && len(paths) < 5; i++ {
		if p := ShortestPath(ix, resolved[i], resolved[i+1]); len(p) > 1 {
			paths = append(paths, p)
		}
	}

	report := &ProbeReport{Task: task, MaxTokens: maxTokens, Anchors: anchors, Paths: paths}
	// Trim the payload to the budget so JSON consumers never get an oversized bundle.
	fitReportToBudget(report, maxTokens)
	return report
}

// fitReportToBudget shrinks the report until its rendered text fits maxTokens:
// the largest caller/callee/test list is halved, then trailing anchors are
// dropped. Deterministic and bounded (halving), and the Truncated flag tells
// callers the payload was cut rather than reported oversized.
func fitReportToBudget(r *ProbeReport, maxTokens int) {
	text := RenderProbe(r)
	r.Tokens = tokenize.Count(text)
	if r.Tokens <= maxTokens || len(r.Anchors) == 0 {
		return
	}
	r.Truncated = true
	for r.Tokens > maxTokens {
		var biggest *[]string
		for i := range r.Anchors {
			for _, list := range []*[]string{&r.Anchors[i].Callers, &r.Anchors[i].Callees, &r.Anchors[i].Tests} {
				if len(*list) > 1 && (biggest == nil || len(*list) > len(*biggest)) {
					biggest = list
				}
			}
		}
		if biggest != nil {
			*biggest = (*biggest)[:(len(*biggest)+1)/2]
		} else if len(r.Anchors) > 1 {
			r.Anchors = r.Anchors[:len(r.Anchors)-1]
		} else {
			break
		}
		text = RenderProbe(r)
		r.Tokens = tokenize.Count(text)
	}
}

// RenderProbe renders the probe bundle as compact text.
func RenderProbe(r *ProbeReport) string {
	var b strings.Builder
	b.WriteString("probe: ")
	b.WriteString(r.Task)
	b.WriteString("\n\n")
	if len(r.Anchors) == 0 {
		b.WriteString("  (no task symbols resolved against the index — try naming a function or type)\n")
		return b.String()
	}
	allFuzzy := true
	for _, a := range r.Anchors {
		if !a.Fuzzy {
			allFuzzy = false
			break
		}
	}
	if allFuzzy {
		b.WriteString("warning: no exact symbol matches — anchors below are fuzzy (low confidence, may be unrelated)\n\n")
	}
	fmt.Fprintf(&b, "anchors: %d\n", len(r.Anchors))
	for _, a := range r.Anchors {
		label := ""
		if a.Fuzzy {
			label = "  [fuzzy match — low confidence]"
		}
		fmt.Fprintf(&b, "\n%s  (%s)  %s:%d%s\n", a.Name, a.Kind, a.File, a.Line, label)
		if len(a.Callers) > 0 {
			fmt.Fprintf(&b, "  callers: %s\n", strings.Join(a.Callers, ", "))
		}
		if len(a.Callees) > 0 {
			fmt.Fprintf(&b, "  callees: %s\n", strings.Join(a.Callees, ", "))
		}
		if len(a.Tests) > 0 {
			fmt.Fprintf(&b, "  tests  : %s\n", strings.Join(a.Tests, ", "))
		}
	}
	if len(r.Paths) > 0 {
		b.WriteString("\nconnecting paths:\n")
		for _, p := range r.Paths {
			b.WriteString("  " + strings.Join(p, " → ") + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// FitProbe caps the probe bundle to maxTokens using kern's budget fitter.
func FitProbe(text string, maxTokens int) string {
	return budget.Fit(text, maxTokens)
}

// stopWords are common English words that should not be used as symbol-match
// keywords. They are lowercase for case-insensitive comparison.
var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "have": true,
	"has": true, "had": true, "do": true, "does": true, "did": true,
	"will": true, "would": true, "could": true, "should": true, "may": true,
	"might": true, "must": true, "shall": true, "can": true, "need": true,
	"of": true, "in": true, "on": true, "at": true, "to": true, "for": true,
	"with": true, "by": true, "from": true, "as": true, "into": true,
	"about": true, "than": true, "then": true, "so": true, "if": true,
	"but": true, "or": true, "and": true, "not": true, "no": true,
	"this": true, "that": true, "these": true, "those": true, "it": true,
	"its": true, "their": true, "there": true, "here": true, "where": true,
	"when": true, "how": true, "why": true, "what": true, "which": true,
	"who": true, "whom": true, "whose": true,
	"i": true, "you": true, "he": true, "she": true, "we": true, "they": true,
	"me": true, "him": true, "her": true, "us": true, "them": true,
	"my": true, "your": true, "his": true, "our": true,
	"fix": true, "bug": true, "issue": true, "problem": true, "error": true,
	"task": true, "todo": true, "feature": true, "change": true,
	"add": true, "remove": true, "update": true, "create": true, "delete": true,
	"get": true, "set": true, "new": true, "old": true,
	"find": true, "found": true, "search": true, "check": true, "call": true,
	"calls": true, "see": true, "try": true, "tries": true, "run": true, "runs": true,
	"want": true, "wants": true, "make": true, "makes": true, "know": true, "give": true,
	"show": true, "shows": true, "like": true, "work": true, "works": true,
}

func isCamelCase(s string) bool {
	hasLower := false
	hasUpperAfterLower := false
	for i, r := range s {
		if r >= 'a' && r <= 'z' {
			hasLower = true
		} else if r >= 'A' && r <= 'Z' {
			if i > 0 {
				hasUpperAfterLower = true
			}
		}
	}
	return hasLower && hasUpperAfterLower
}

// isYAMLishTask reports whether a task text names YAML/yml artifacts, in
// which case data-file property keys ("on:", "runs-on:") are legitimate
// anchors for the task. Anything else treats prop-kind symbols as non-code.
func isYAMLishTask(task string) bool {
	low := strings.ToLower(task)
	return strings.Contains(low, "yaml") || strings.Contains(low, ".yml")
}

// anchorableCandidate is the probe anchor filter (QA 5.6(a)): a resolved
// symbol can anchor a probe — and therefore appear in its "connecting paths"
// — only when it is real code for this task. Data-file property keys
// (JSON/YAML kind "prop": "on", "to", "body" in a GitHub workflow) and docs
// headings (kind "heading") are excluded unless the task is YAML-ish. Code
// symbols whose names collide with stopwords (Add, Get, Run, find) keep their
// documented precedence — the index consult precedes the stopword filter
// (pinned by TestProbeFiltersStopwords); the stopword filter keeps applying
// to tokens that do NOT resolve.
func anchorableCandidate(kind string, yamlish bool) bool {
	switch kind {
	case "prop":
		return yamlish
	case "heading":
		return false
	}
	return true
}

// extractKeywords pulls meaningful keywords from a natural-language task
// string, filtering stop words and short tokens. Keywords are lowercased
// and deduped. For example, "decommission a network service" yields
// ["decommission", "network", "service"].
func extractKeywords(task string) []string {
	words := strings.Fields(strings.ToLower(task))
	seen := map[string]bool{}
	var out []string
	for _, w := range words {
		// Strip punctuation.
		w = strings.Trim(w, ".,;:!?\"'()[]{}<>")
		if len(w) < 3 || stopWords[w] {
			continue
		}
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// fuzzyMatchSymbols finds symbols whose names contain the keyword (case-
// insensitive), splitting CamelCase names into segments for matching. Returns
// at most limit matches, preferring symbols with source files (project-local).
func fuzzyMatchSymbols(ix *index.Index, keyword string, limit int) []string {
	kw := strings.ToLower(keyword)
	var matches []string
	for _, s := range ix.Symbols {
		full := s.FullName()
		// Match against the full name, the simple name, and CamelCase segments.
		lower := strings.ToLower(full)
		if strings.Contains(lower, kw) {
			matches = append(matches, full)
			if len(matches) >= limit {
				break
			}
			continue
		}
		// Split CamelCase: "ZTPServiceImpl" -> ["ztp", "service", "impl"]
		for _, seg := range splitCamelCase(full) {
			if seg == kw || strings.Contains(seg, kw) {
				matches = append(matches, full)
				if len(matches) >= limit {
					return matches
				}
				break
			}
		}
	}
	return matches
}

// fuzzyKeywordMatches returns the symbols matching one prose keyword of the
// task, ranked for a tight cut instead of raw scan order: every symbol whose
// name covers MULTIPLE task keywords (execRejectionTTL covers "exec" and
// "ttl") anchors — that is a precise identification, not noise — capped at 4
// to bound a pathological task; among single-keyword matches only the most
// connected one anchors (a call-graph hub like CheckExecCommand outranks an
// incidental substring), ties broken lexicographically for determinism.
func fuzzyKeywordMatches(ix *index.Index, kw, task string) []string {
	kws := extractKeywords(task)
	lower := strings.ToLower(kw)
	var multi, single []string
	for _, s := range ix.Symbols {
		full := s.FullName()
		lf := strings.ToLower(full)
		if !strings.Contains(lf, lower) {
			continue
		}
		cov := 0
		for _, k := range kws {
			if k != kw && strings.Contains(lf, k) {
				cov++
			}
		}
		if cov > 0 {
			multi = append(multi, full)
		} else {
			single = append(single, full)
		}
	}
	if len(multi) > 4 {
		multi = multi[:4]
	}
	if len(single) > 1 {
		sort.Slice(single, func(i, j int) bool {
			ci, cj := len(ix.Callers[single[i]]), len(ix.Callers[single[j]])
			if ci != cj {
				return ci > cj
			}
			return single[i] < single[j]
		})
		single = single[:1]
	}
	return append(multi, single...)
}

// splitCamelCase breaks a CamelCase or PascalCase identifier into lowercase
// segments. "HttpRequest" -> ["http", "request"], "ZTPServiceImpl" ->
// ["ztp", "service", "impl"], "getXMLParser" -> ["get", "xml", "parser"].
func splitCamelCase(s string) []string {
	var segs []string
	var cur strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			if cur.Len() > 0 {
				segs = append(segs, strings.ToLower(cur.String()))
				cur.Reset()
			}
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		segs = append(segs, strings.ToLower(cur.String()))
	}
	return segs
}
