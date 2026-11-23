package research

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/budget"
	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

// ResearchAnchor is one compact research result: the probe-style definition
// and call/test evidence plus the bounded blast radius for that symbol.
type ResearchAnchor struct {
	intel.ProbeAnchor
	Impact        []string `json:"impact,omitempty"`
	AffectedFiles []string `json:"affected_files,omitempty"`
	Alternatives  []string `json:"alternatives,omitempty"` // same-simple-name definitions not chosen

	Evidence string `json:"evidence,omitempty"`
}

// ResearchReport is a read-only, deterministic research bundle for a
// multi-symbol task. Unlike Probe, it deliberately reaches beyond tokens that
// exactly resolve: adjacent task keywords are searched together so prose such
// as "guard command classifier" can find ClassifyCommand.
type ResearchReport struct {
	Task      string           `json:"task"`
	MaxTokens int              `json:"max_tokens"`
	Tokens    int              `json:"tokens"`
	Truncated bool             `json:"truncated,omitempty"`
	Anchors   []ResearchAnchor `json:"anchors"`
	Paths     [][]string       `json:"paths,omitempty"`
	Missing   []string         `json:"missing,omitempty"`
}

const defaultResearchAnchors = 6

type researchCandidate struct {
	sym    index.Symbol
	score  int
	exact  bool
	forced bool
}

// Research turns a task into a small set of ranked symbol anchors, then for
// each anchor combines definition, direct calls, tests, impact and affected
// files. It stays read-only and index-backed; no shell, network, or model call
// participates.
func Research(ix *index.Index, task string, maxTokens int) *ResearchReport {
	if maxTokens <= 0 {
		maxTokens = 6000
	}
	report := &ResearchReport{Task: task, MaxTokens: maxTokens}
	if ix == nil {
		report.Missing = missingResearchContext()
		return report
	}

	probe := intel.Probe(ix, task, 12000)
	candidates := rankResearchCandidates(ix, task, probe)
	if len(candidates) == 0 {
		for _, a := range probe.Anchors {
			if def, ok := intel.FindDefinition(ix, a.Resolved); ok {
				candidates = append(candidates, researchCandidate{sym: def, score: 1, exact: !a.Fuzzy})
			}
		}
	}
	if len(candidates) > defaultResearchAnchors {
		candidates = candidates[:defaultResearchAnchors]
	}

	for _, candidate := range candidates {
		explore, err := intel.ExploreMin(ix, candidate.sym.FullName(), 2, 30, "")
		if err != nil {
			continue
		}
		anchor := ResearchAnchor{
			ProbeAnchor: intel.ProbeAnchor{
				Name:     explore.Definition.Name,
				Resolved: explore.Resolved,
				Kind:     explore.Definition.Kind,
				File:     explore.Definition.File,
				Line:     explore.Definition.Line,
				End:      explore.Definition.End,
				Callers:  firstStrings(explore.Callers, 3),
				Callees:  firstStrings(explore.Callees, 3),
				Tests:    testsFor(ix, explore.Definition),
				Fuzzy:    !candidate.exact,
			},
			Impact:        firstStrings(explore.BlastRadius, 5),
			AffectedFiles: firstStrings(explore.BlastFiles, 4),
			Alternatives:  researchAlternatives(ix, candidate.sym),

			Evidence: explore.Evidence,
		}
		report.Anchors = append(report.Anchors, anchor)
	}

	var resolved []string
	for _, a := range report.Anchors {
		resolved = append(resolved, a.Resolved)
	}
	for i := 0; i+1 < len(resolved) && len(report.Paths) < 1; i++ {
		if path := intel.ShortestPath(ix, resolved[i], resolved[i+1]); len(path) > 1 {
			report.Paths = append(report.Paths, path)
		}
	}
	report.Missing = missingResearchContext()
	fitResearchToBudget(report, maxTokens)
	return report
}

func rankResearchCandidates(ix *index.Index, task string, probe *intel.ProbeReport) []researchCandidate {
	candidates := map[string]researchCandidate{}
	keywords := normalizedResearchKeywords(task)
	add := func(sym index.Symbol, score int, exact bool) {
		key := sym.FullName()
		old := candidates[key]
		if old.sym.FullName() == "" {
			old.sym = sym
		}
		old.score += score
		old.exact = old.exact || exact
		candidates[key] = old
	}

	for _, query := range researchQueries(task) {
		hits := intel.RankedSearchScored(ix, query.text, 8)
		for rank, hit := range hits {
			score := 8 - rank
			if score < 1 {
				score = 1
			}
			if hit.MatchedAll {
				score += 2
			}
			add(hit.Symbol, score, false)
			if query.forceTop && rank < 2 {
				forceResearchCandidate(candidates, hit.Symbol)
			}
		}
	}
	for _, anchor := range probe.Anchors {
		def, ok := intel.FindDefinition(ix, anchor.Resolved)
		if !ok {
			continue
		}
		if anchor.Fuzzy {
			add(def, 1, false)
			continue
		}
		add(def, 4, true)
	}

	out := make([]researchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.score += 5 * researchKeywordCoverage(candidate.sym, keywords)
		if intel.IsNonProduction(candidate.sym) {
			candidate.score -= 2
		} else {
			candidate.score += 2
		}
		out = append(out, candidate)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].forced != out[j].forced {
			return out[i].forced
		}
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		pi := len(ix.CallersFor(out[i].sym))
		pj := len(ix.CallersFor(out[j].sym))
		if pi != pj {
			return pi > pj
		}
		return out[i].sym.FullName() < out[j].sym.FullName()
	})
	return out
}

// researchAlternatives names other definitions that share the selected
// symbol's simple name. A bare task token can resolve to only one of them, so
// the alternatives stay visible instead of silently narrowing the task.
func researchAlternatives(ix *index.Index, selected index.Symbol) []string {
	hits := ix.Search(selected.Name, 5)
	out := make([]string, 0, len(hits))
	for _, hit := range hits {
		name := hit.FullName()
		if name == "" || (hit.File == selected.File && hit.Line == selected.Line) {
			continue
		}
		out = append(out, fmt.Sprintf("%s — %s:%d", name, hit.File, hit.Line))
	}
	return firstStrings(out, 3)
}

func forceResearchCandidate(candidates map[string]researchCandidate, sym index.Symbol) {
	key := sym.FullName()
	candidate := candidates[key]
	candidate.sym = sym
	candidate.forced = true
	candidates[key] = candidate
}

type researchQuery struct {
	text     string
	forceTop bool
}

func researchQueries(task string) []researchQuery {
	keywords := normalizedResearchKeywords(task)
	queries := []researchQuery{{text: task}}
	addPairs := func(keywords []string) {
		for i := 0; i+1 < len(keywords); i++ {
			query := keywords[i] + " " + keywords[i+1]
			if !containsResearchQuery(queries, query) {
				queries = append(queries, researchQuery{text: query})
			}
		}
	}
	addPairs(intel.ExtractKeywords(task))
	addPairs(keywords)
	for i := 0; i+2 < len(keywords); i++ {
		query := keywords[i] + " " + keywords[i+1] + " " + keywords[i+2]
		if !containsResearchQuery(queries, query) {
			queries = append(queries, researchQuery{text: query, forceTop: true})
		}
	}
	return queries
}

func containsResearchQuery(values []researchQuery, needle string) bool {
	for _, value := range values {
		if value.text == needle {
			return true
		}
	}
	return false
}

func normalizedResearchKeywords(task string) []string {
	original := intel.ExtractKeywords(task)
	normalized := make([]string, len(original))
	// Common nominalizations do not share a token with the implementation
	// verb. This is the only morphology special case in the first slice; if
	// more forms prove necessary, move them into RankedSearch, not here.
	for i, keyword := range original {
		normalized[i] = keyword
		if strings.HasSuffix(keyword, "ier") {
			normalized[i] = keyword[:len(keyword)-3] + "y"
		}
	}
	return normalized
}

func researchKeywordCoverage(sym index.Symbol, keywords []string) int {
	full := strings.ToLower(sym.FullName())
	covered := 0
	for _, keyword := range keywords {
		if keyword != "" && strings.Contains(full, keyword) {
			covered++
		}
	}
	return covered
}

func testsFor(ix *index.Index, def index.Symbol) []string {
	var tests []string
	seen := map[string]bool{}
	for _, caller := range ix.CallersFor(def) {
		callerDef, ok := intel.FindCallerDefinition(ix, caller, def)
		if !ok || !intel.IsTestFile(callerDef.File) || seen[callerDef.FullName()] {
			continue
		}
		seen[callerDef.FullName()] = true
		tests = append(tests, callerDef.FullName())
	}
	sort.Strings(tests)
	return firstStrings(tests, 2)
}

func firstStrings(in []string, limit int) []string {
	if limit < 0 || len(in) <= limit {
		return in
	}
	return in[:limit]
}

func missingResearchContext() []string {
	return []string{
		"source slices are omitted; use kern_context or kern_explore for focused definitions",
		"recent changes are not included; use kern_review",
		"execution is intentionally excluded; kern_research is read-only",
	}
}

func fitResearchToBudget(report *ResearchReport, maxTokens int) {
	text := RenderResearch(report)
	report.Tokens = tokenize.Count(text)
	if report.Tokens <= maxTokens || len(report.Anchors) == 0 {
		return
	}
	report.Truncated = true
	for report.Tokens > maxTokens {
		var largest *[]string
		largestLen := 0
		for i := range report.Anchors {
			for _, list := range []*[]string{
				&report.Anchors[i].Callers,
				&report.Anchors[i].Callees,
				&report.Anchors[i].Tests,
				&report.Anchors[i].Impact,
				&report.Anchors[i].AffectedFiles,
			} {
				if len(*list) > largestLen {
					largest = list
					largestLen = len(*list)
				}
			}
		}
		if largestLen > 1 {
			half := largestLen / 2
			if half == 0 {
				half = 1
			}
			*largest = (*largest)[:half]
		} else {
			report.Anchors = report.Anchors[:len(report.Anchors)-1]
			if len(report.Anchors) == 0 {
				break
			}
		}
		text = RenderResearch(report)
		report.Tokens = tokenize.Count(text)
	}
}

// RenderResearch renders the compact evidence bundle. Every anchor carries a
// file:line and content-identity anchor; omitted depth is explicit rather than
// mistaken for an empty graph.
func RenderResearch(report *ResearchReport) string {
	var b strings.Builder
	if report == nil {
		return ""
	}
	b.WriteString("research: " + report.Task + "\n")
	b.WriteString("anchors: " + strconv.Itoa(len(report.Anchors)))
	if report.Truncated {
		b.WriteString(" (token-capped)")
	}
	b.WriteString("\n")
	if len(report.Anchors) == 0 {
		b.WriteString("no indexed research anchors matched this task\n")
	}
	for i, anchor := range report.Anchors {
		line := fmt.Sprintf("%d. %s %s — %s:%d", i+1, anchor.Resolved, anchor.Kind, anchor.File, anchor.Line)
		if anchor.End > anchor.Line {
			line += "-" + strconv.Itoa(anchor.End)
		}
		if anchor.Fuzzy {
			line += " [ranked]"
		} else {
			line += " [exact]"
		}
		b.WriteString(line + "\n")
		writeResearchList(&b, "callers", anchor.Callers)
		writeResearchList(&b, "callees", anchor.Callees)
		writeResearchList(&b, "tests", anchor.Tests)
		writeResearchList(&b, "impact", anchor.Impact)
		writeResearchList(&b, "affected files", anchor.AffectedFiles)
		writeResearchList(&b, "alternatives", anchor.Alternatives)

		if anchor.Evidence != "" {
			b.WriteString("  " + anchor.Evidence + "\n")
		}
	}
	for _, path := range report.Paths {
		b.WriteString("path: " + strings.Join(path, " -> ") + "\n")
	}
	for _, missing := range report.Missing {
		b.WriteString("missing: " + missing + "\n")
	}
	b.WriteString("tokens: " + strconv.Itoa(report.Tokens) + "/" + strconv.Itoa(report.MaxTokens) + "\n")
	return strings.TrimSuffix(b.String(), "\n")
}

// FitResearch caps a rendered research bundle to maxTokens with the shared
// budget fitter. Mirrors FitProbe so graph/CLI callers keep one budget policy.
func FitResearch(text string, maxTokens int) string {
	return budget.Fit(text, maxTokens)
}

func writeResearchList(b *strings.Builder, label string, values []string) {
	if len(values) == 0 {
		b.WriteString("  " + label + ": none indexed\n")
		return
	}
	b.WriteString("  " + label + " (" + strconv.Itoa(len(values)) + "): " + strings.Join(values, ", ") + "\n")
}
