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
	candidates := map[string]bool{}
	for _, w := range identRe.FindAllString(task, -1) {
		if r, ok := Resolve(ix, w); ok {
			candidates[r] = true
		}
	}

	// Fuzzy fallback for natural-language tasks like "decommission a network
	// service": match extracted keywords against symbol names and segments.
	if len(candidates) == 0 {
		keywords := extractKeywords(task)
		for _, kw := range keywords {
			for _, match := range fuzzyMatchSymbols(ix, kw, 5) {
				candidates[match] = true
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
		a := ProbeAnchor{Name: simpleName(r), Resolved: r}
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
			if s, ok := meta[c]; ok && isTestFile(s.File) {
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
	fmt.Fprintf(&b, "anchors: %d\n", len(r.Anchors))
	for _, a := range r.Anchors {
		fmt.Fprintf(&b, "\n%s  (%s)  %s:%d\n", a.Name, a.Kind, a.File, a.Line)
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
