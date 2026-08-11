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
// and tests for each. This is the query-driven micro-context router: the graph
// is the retrieval index, never the payload.
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
		for _, c := range localCallees(ix, r) {
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
	// Trim the payload itself to the budget so JSON consumers (kern probe
	// --json, MCP) never receive an oversized bundle flagged truncated (W2-24).
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
